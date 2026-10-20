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
)

// S3 habla con cualquier almacenamiento compatible con S3 firmando las
// peticiones con AWS Signature V4 (sin el SDK, que pesa decenas de MB).
type S3 struct {
	Endpoint  string // "https://s3.eu-west-1.amazonaws.com", "https://<cuenta>.r2.cloudflarestorage.com"…
	Region    string // "auto" en R2
	Bucket    string
	Prefix    string // carpeta dentro del bucket
	AccessKey string
	SecretKey string
	// HTTP permite cambiar el cliente en los tests.
	HTTP *http.Client
	// now permite fijar la hora en los tests.
	now func() time.Time
}

func (s *S3) client() *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	return http.DefaultClient
}

func (s *S3) Describe() string {
	return "s3://" + strings.TrimSuffix(s.Bucket+"/"+strings.Trim(s.Prefix, "/"), "/")
}

// objectURL usa el estilo de ruta (endpoint/bucket/clave), que entienden
// todos los compatibles con S3.
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
	return s.do(req, nil)
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
	return s.do(req, nil)
}

// Check sube y borra un objeto pequeño: así se comprueban a la vez las
// credenciales, el bucket y el permiso de escritura.
func (s *S3) Check(ctx context.Context) error {
	key := ".dockfly-check"
	if err := s.Put(ctx, key, strings.NewReader("ok"), 2); err != nil {
		return err
	}
	return s.Delete(ctx, key)
}

func (s *S3) do(req *http.Request, out any) error {
	s.sign(req)
	resp, err := s.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return s3Error(resp)
	}
	if out != nil {
		return xml.NewDecoder(resp.Body).Decode(out)
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
		return fmt.Errorf("el bucket no existe")
	case e.Code == "InvalidAccessKeyId" || e.Code == "SignatureDoesNotMatch":
		return fmt.Errorf("credenciales incorrectas (%s)", e.Code)
	case e.Code == "AccessDenied":
		return fmt.Errorf("acceso denegado: la clave no tiene permiso de escritura en el bucket")
	case e.Code != "":
		return fmt.Errorf("S3: %s %s", e.Code, e.Message)
	}
	return fmt.Errorf("S3 respondió %s", resp.Status)
}

// --- AWS Signature V4 ---

const unsignedPayload = "UNSIGNED-PAYLOAD"

func (s *S3) sign(req *http.Request) { s.signWith(req, unsignedPayload) }

// signWith firma con el hash del cuerpo indicado.
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

	// El cuerpo no se firma (se envía en streaming): S3 lo permite por HTTPS.
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

// uriEncode codifica como pide SigV4: todo salvo A-Z a-z 0-9 - _ . ~
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
