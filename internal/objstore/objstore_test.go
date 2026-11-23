package objstore

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The "GET Object" example from the AWS Signature V4 documentation.
func TestSigV4AWSExample(t *testing.T) {
	s := &S3{
		Region: "us-east-1", AccessKey: "AKIAIOSFODNN7EXAMPLE", SecretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		now: func() time.Time { return time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC) },
	}
	req, _ := http.NewRequest("GET", "https://examplebucket.s3.amazonaws.com/test.txt", nil)
	req.Header.Set("Range", "bytes=0-9")
	s.signWith(req, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855")
	want := "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request, " +
		"SignedHeaders=host;range;x-amz-content-sha256;x-amz-date, " +
		"Signature=f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41"
	if got := req.Header.Get("Authorization"); got != want {
		t.Errorf("signature:\n %s\nwanted\n %s", got, want)
	}
}

func TestObjectURL(t *testing.T) {
	s := &S3{Endpoint: "https://s3.example.com/", Bucket: "backups", Prefix: "/dockfly/"}
	if got := s.objectURL("postgres/2026 10 20.sql.gz"); got != "https://s3.example.com/backups/dockfly/postgres/2026%2010%2020.sql.gz" {
		t.Errorf("url: %s", got)
	}
	if s.Describe() != "s3://backups/dockfly" {
		t.Errorf("describe: %s", s.Describe())
	}
}

func TestS3Errors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=AK/") {
			t.Errorf("not signed: %v", r.Header)
		}
		switch r.Method {
		case "PUT":
			w.WriteHeader(403)
			io.WriteString(w, `<Error><Code>AccessDenied</Code><Message>no</Message></Error>`)
		case "GET":
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	s := &S3{Endpoint: srv.URL, Bucket: "b", AccessKey: "AK", SecretKey: "SK"}
	if err := s.Check(context.Background()); err == nil || !strings.Contains(err.Error(), "write permission") {
		t.Errorf("check: %v", err)
	}
	if _, err := s.Get(context.Background(), "x"); err != ErrNotFound {
		t.Errorf("get of a missing object: %v", err)
	}
}

func TestLocal(t *testing.T) {
	l := &Local{Dir: t.TempDir()}
	ctx := context.Background()
	if err := l.Put(ctx, "pg/1.sql.gz", strings.NewReader("data"), 4); err != nil {
		t.Fatal(err)
	}
	rc, err := l.Get(ctx, "pg/1.sql.gz")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	if string(b) != "data" {
		t.Errorf("read %q", b)
	}
	if err := l.Put(ctx, "../outside", strings.NewReader("x"), 1); err == nil {
		t.Error("a key with .. must not escape the directory")
	}
	l.Delete(ctx, "pg/1.sql.gz")
	if _, err := l.Get(ctx, "pg/1.sql.gz"); err != ErrNotFound {
		t.Errorf("after deleting: %v", err)
	}
}
