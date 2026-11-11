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

// Cifrado opcional de las copias con una contraseña. El formato va por bloques
// autenticados (AES-256-GCM) para poder cifrar/descifrar en streaming sin
// cargar toda la copia en memoria, y detecta truncados y manipulaciones.
//
// Formato: magic | salt(16) | noncePrefix(8) | [ bloque ]...
// Cada bloque: flag(1) | len(4, big-endian) | ciphertext.
// nonce = noncePrefix | contador(4). AAD = flag. flag=1 marca el último bloque
// (siempre hay uno, aunque esté vacío), así un corte del stream se nota.
var encMagic = []byte("DFLYENC1")

const (
	encChunk    = 64 * 1024
	encSaltLen  = 16
	encNoncePre = 8
	// Parámetros de scrypt (recomendados para derivar claves interactivas).
	scryptN = 1 << 15
	scryptR = 8
	scryptP = 1
)

// ErrEncrypted indica que la copia está cifrada y hace falta la contraseña.
var ErrEncrypted = errors.New("la copia está cifrada: hace falta la contraseña")

// ErrWrongPassword indica que la contraseña no descifra la copia.
var ErrWrongPassword = errors.New("contraseña incorrecta o copia dañada")

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

// EncryptWriter devuelve un io.WriteCloser que cifra lo que se le escribe con
// password y lo manda a w. Hay que llamar a Close para emitir el último bloque.
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
	return e.seal(e.buf, true) // último bloque (puede ir vacío)
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

// IsEncrypted mira si r empieza por el magic de una copia cifrada. Devuelve un
// lector equivalente (hay que usarlo en vez de r, porque consume la cabecera).
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

// DecryptReader descifra una copia creada con EncryptWriter. r debe incluir el
// magic (tal cual lo deja IsEncrypted).
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
			return io.ErrUnexpectedEOF // stream cortado antes del último bloque
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
