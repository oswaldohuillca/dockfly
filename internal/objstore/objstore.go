// Package objstore stores the backups: in an S3-compatible bucket (AWS,
// Cloudflare R2, Backblaze B2, MinIO…) or, without one, on disk.
package objstore

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ErrNotFound means the object does not exist.
var ErrNotFound = errors.New("the backup does not exist")

type Store interface {
	// Put uploads size bytes from r under the key key.
	Put(ctx context.Context, key string, r io.Reader, size int64) error
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
	// Check verifies that writing works (for the "Test" button).
	Check(ctx context.Context) error
	// Describe says where the backups are stored ("s3://bucket/prefix").
	Describe() string
}

// Local stores the objects in a directory.
type Local struct {
	Dir string
}

func (l *Local) path(key string) (string, error) {
	clean := filepath.Clean("/" + key)
	if strings.Contains(key, "..") {
		return "", errors.New("invalid key")
	}
	return filepath.Join(l.Dir, clean), nil
}

func (l *Local) Put(_ context.Context, key string, r io.Reader, _ int64) error {
	p, err := l.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	tmp := p + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, p)
}

func (l *Local) Get(_ context.Context, key string) (io.ReadCloser, error) {
	p, err := l.path(key)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	return f, err
}

func (l *Local) Delete(_ context.Context, key string) error {
	p, err := l.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (l *Local) Check(context.Context) error { return os.MkdirAll(l.Dir, 0o700) }

func (l *Local) Describe() string { return l.Dir }
