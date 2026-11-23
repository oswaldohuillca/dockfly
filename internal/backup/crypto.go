package backup

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"

	"golang.org/x/crypto/scrypt"
)

// Optional password encryption of the backups. The format is a chain of
// authenticated chunks (AES-256-GCM) so it can be encrypted/decrypted as a
// stream without holding the whole backup in memory, and it detects
// truncation and tampering.
//
// Format: magic | salt(16) | noncePrefix(8) | [ chunk ]...
// Each chunk: flag(1) | len(4, big-endian) | ciphertext.
// nonce = noncePrefix | counter(4). AAD = flag. flag=1 marks the last chunk
// (there is always one, even if empty), so a cut-off stream is noticed.
var encMagic = []byte("DFLYENC1")

const (
	encChunk    = 64 * 1024
	encSaltLen  = 16
	encNoncePre = 8
	// scrypt parameters (the recommended ones for interactive key derivation).
	scryptN = 1 << 15
	scryptR = 8
	scryptP = 1
)

// ErrEncrypted means the backup is encrypted and the password is required.
var ErrEncrypted = errors.New("the backup is encrypted: the password is required")

// ErrWrongPassword means the password does not decrypt the backup.
var ErrWrongPassword = errors.New("wrong password or damaged backup")

func deriveKey(password string, salt []byte) ([]byte, error) {
	return scrypt.Key([]byte(password), salt, scryptN, scryptR, scryptP, 32)
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// EncryptWriter returns an io.WriteCloser that encrypts whatever is written to
// it with password and sends it to w. Close must be called to emit the last chunk.
func EncryptWriter(w io.Writer, password string) (io.WriteCloser, error) {
	salt := make([]byte, encSaltLen)
	noncePre := make([]byte, encNoncePre)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	if _, err := rand.Read(noncePre); err != nil {
		return nil, err
	}
	key, err := deriveKey(password, salt)
	if err != nil {
		return nil, err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(encMagic); err != nil {
		return nil, err
	}
	if _, err := w.Write(salt); err != nil {
		return nil, err
	}
	if _, err := w.Write(noncePre); err != nil {
		return nil, err
	}
	return &encWriter{w: w, gcm: gcm, noncePre: noncePre, buf: make([]byte, 0, encChunk)}, nil
}

type encWriter struct {
	w        io.Writer
	gcm      cipher.AEAD
	noncePre []byte
	buf      []byte
	counter  uint32
	closed   bool
}

func (e *encWriter) Write(p []byte) (int, error) {
	n := len(p)
	e.buf = append(e.buf, p...)
	for len(e.buf) >= encChunk {
		if err := e.seal(e.buf[:encChunk], false); err != nil {
			return 0, err
		}
		e.buf = e.buf[encChunk:]
	}
	return n, nil
}

func (e *encWriter) Close() error {
	if e.closed {
		return nil
	}
	e.closed = true
	return e.seal(e.buf, true) // last chunk (may be empty)
}

func (e *encWriter) seal(chunk []byte, last bool) error {
	e.counter++
	nonce := make([]byte, 12)
	copy(nonce, e.noncePre)
	binary.BigEndian.PutUint32(nonce[8:], e.counter)
	flag := byte(0)
	if last {
		flag = 1
	}
	ct := e.gcm.Seal(nil, nonce, chunk, []byte{flag})
	var hdr [5]byte
	hdr[0] = flag
	binary.BigEndian.PutUint32(hdr[1:], uint32(len(ct)))
	if _, err := e.w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := e.w.Write(ct)
	return err
}

// IsEncrypted checks whether r starts with the magic of an encrypted backup.
// It returns an equivalent reader (use it instead of r, since the header has
// already been consumed).
func IsEncrypted(r io.Reader) (bool, io.Reader, error) {
	head := make([]byte, len(encMagic))
	n, err := io.ReadFull(r, head)
	rest := io.MultiReader(bytes.NewReader(head[:n]), r)
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		return false, rest, nil
	}
	if err != nil {
		return false, rest, err
	}
	return bytes.Equal(head, encMagic), rest, nil
}

// DecryptReader decrypts a backup created with EncryptWriter. r must include
// the magic (exactly as IsEncrypted leaves it).
func DecryptReader(r io.Reader, password string) (io.Reader, error) {
	head := make([]byte, len(encMagic))
	if _, err := io.ReadFull(r, head); err != nil || !bytes.Equal(head, encMagic) {
		return nil, ErrEncrypted
	}
	salt := make([]byte, encSaltLen)
	noncePre := make([]byte, encNoncePre)
	if _, err := io.ReadFull(r, salt); err != nil {
		return nil, ErrWrongPassword
	}
	if _, err := io.ReadFull(r, noncePre); err != nil {
		return nil, ErrWrongPassword
	}
	key, err := deriveKey(password, salt)
	if err != nil {
		return nil, err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	return &decReader{r: r, gcm: gcm, noncePre: noncePre}, nil
}

type decReader struct {
	r        io.Reader
	gcm      cipher.AEAD
	noncePre []byte
	counter  uint32
	buf      []byte
	done     bool
}

func (d *decReader) Read(p []byte) (int, error) {
	for len(d.buf) == 0 {
		if d.done {
			return 0, io.EOF
		}
		if err := d.next(); err != nil {
			return 0, err
		}
	}
	n := copy(p, d.buf)
	d.buf = d.buf[n:]
	return n, nil
}

func (d *decReader) next() error {
	var hdr [5]byte
	if _, err := io.ReadFull(d.r, hdr[:]); err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return io.ErrUnexpectedEOF // stream cut off before the last chunk
		}
		return err
	}
	flag := hdr[0]
	n := binary.BigEndian.Uint32(hdr[1:])
	if n > encChunk+uint32(d.gcm.Overhead()) {
		return ErrWrongPassword
	}
	ct := make([]byte, n)
	if _, err := io.ReadFull(d.r, ct); err != nil {
		return io.ErrUnexpectedEOF
	}
	d.counter++
	nonce := make([]byte, 12)
	copy(nonce, d.noncePre)
	binary.BigEndian.PutUint32(nonce[8:], d.counter)
	pt, err := d.gcm.Open(nil, nonce, ct, []byte{flag})
	if err != nil {
		return ErrWrongPassword
	}
	d.buf = pt
	if flag == 1 {
		d.done = true
	}
	return nil
}
