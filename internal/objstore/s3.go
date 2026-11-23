package objstore

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/dockfly/dockfly/internal/egress"
)

// safeClient rejects link-local IPs (cloud metadata) to guard against SSRF
// through a malicious endpoint; it allows loopback/private ones for
// self-hosted MinIO.
var safeClient = &http.Client{Transport: egress.SafeTransport()}

// S3 talks to any S3-compatible storage, signing the requests with AWS
// Signature V4 (without the SDK, which weighs tens of MB).
type S3 struct {
	Endpoint  string // "https://s3.eu-west-1.amazonaws.com", "https://<account>.r2.cloudflarestorage.com"…
	Region    string // "auto" on R2
	Bucket    string
	Prefix    string // directory inside the bucket
	AccessKey string
	SecretKey string
	// HTTP allows swapping the client in the tests.
	HTTP *http.Client
	// now allows pinning the time in the tests.
	now func() time.Time
}

func (s *S3) client() *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	return safeClient
}

func (s *S3) Describe() string {
	return "s3://" + strings.TrimSuffix(s.Bucket+"/"+strings.Trim(s.Prefix, "/"), "/")
}

// objectURL uses path style (endpoint/bucket/key), which every S3-compatible
// service understands.
func (s *S3) objectURL(key string) string {
	p := strings.Trim(s.Prefix, "/")
	if p != "" {
		key = p + "/" + key
	}
	segs := strings.Split(key, "/")
	for i, seg := range segs {
		segs[i] = uriEncode(seg)
	}
	return strings.TrimRight(s.Endpoint, "/") + "/" + uriEncode(s.Bucket) + "/" + strings.Join(segs, "/")
}

func (s *S3) Put(ctx context.Context, key string, r io.Reader, size int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, s.objectURL(key), r)
	if err != nil {
		return err
	}
	req.ContentLength = size
	req.Header.Set("Content-Type", "application/gzip")
	return s.do(req)
}

func (s *S3) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.objectURL(key), nil)
	if err != nil {
		return nil, err
	}
	s.sign(req)
	resp, err := s.client().Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, ErrNotFound
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		return nil, s3Error(resp)
	}
	return resp.Body, nil
}

func (s *S3) Delete(ctx context.Context, key string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, s.objectURL(key), nil)
	if err != nil {
		return err
	}
	return s.do(req)
}

// Check uploads and deletes a small object: that checks the credentials, the
// bucket and the write permission all at once.
func (s *S3) Check(ctx context.Context) error {
	key := ".dockfly-check"
	if err := s.Put(ctx, key, strings.NewReader("ok"), 2); err != nil {
		return err
	}
	return s.Delete(ctx, key)
}

func (s *S3) do(req *http.Request) error {
	s.sign(req)
	resp, err := s.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return s3Error(resp)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

func s3Error(resp *http.Response) error {
	var e struct {
		Code    string `xml:"Code"`
		Message string `xml:"Message"`
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	_ = xml.Unmarshal(body, &e)
	switch {
	case e.Code == "NoSuchBucket":
		return fmt.Errorf("the bucket does not exist")
	case e.Code == "InvalidAccessKeyId" || e.Code == "SignatureDoesNotMatch":
		return fmt.Errorf("wrong credentials (%s)", e.Code)
	case e.Code == "AccessDenied":
		return fmt.Errorf("access denied: the key has no write permission on the bucket")
	case e.Code != "":
		return fmt.Errorf("S3: %s %s", e.Code, e.Message)
	}
	return fmt.Errorf("S3 responded %s", resp.Status)
}

// --- AWS Signature V4 ---

const unsignedPayload = "UNSIGNED-PAYLOAD"

func (s *S3) sign(req *http.Request) { s.signWith(req, unsignedPayload) }

// signWith signs using the given body hash.
func (s *S3) signWith(req *http.Request, payloadHash string) {
	now := time.Now
	if s.now != nil {
		now = s.now
	}
	t := now().UTC()
	amzDate := t.Format("20060102T150405Z")
	day := t.Format("20060102")
	region := s.Region
	if region == "" {
		region = "us-east-1"
	}

	// The body is not signed (it is streamed): S3 allows that over HTTPS.
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("Host", req.URL.Host)

	names := make([]string, 0, len(req.Header))
	for k := range req.Header {
		lk := strings.ToLower(k)
		if lk == "host" || lk == "content-type" || lk == "range" || strings.HasPrefix(lk, "x-amz-") {
			names = append(names, lk)
		}
	}
	sort.Strings(names)
	var canonHeaders strings.Builder
	for _, k := range names {
		canonHeaders.WriteString(k + ":" + strings.TrimSpace(req.Header.Get(k)) + "\n")
	}
	signed := strings.Join(names, ";")

	canonical := strings.Join([]string{
		req.Method,
		req.URL.EscapedPath(),
		canonicalQuery(req.URL.Query()),
		canonHeaders.String(),
		signed,
		payloadHash,
	}, "\n")
	scope := day + "/" + region + "/s3/aws4_request"
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hexSHA256([]byte(canonical))

	key := hmacSHA256([]byte("AWS4"+s.SecretKey), day)
	key = hmacSHA256(key, region)
	key = hmacSHA256(key, "s3")
	key = hmacSHA256(key, "aws4_request")
	sig := hex.EncodeToString(hmacSHA256(key, toSign))

	req.Header.Set("Authorization", fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		s.AccessKey, scope, signed, sig))
}

func canonicalQuery(q url.Values) string {
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		vals := append([]string(nil), q[k]...)
		sort.Strings(vals)
		for _, v := range vals {
			parts = append(parts, uriEncode(k)+"="+uriEncode(v))
		}
	}
	return strings.Join(parts, "&")
}

// uriEncode encodes the way SigV4 requires: everything but A-Z a-z 0-9 - _ . ~
func uriEncode(s string) string {
	var b bytes.Buffer
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

func hexSHA256(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
