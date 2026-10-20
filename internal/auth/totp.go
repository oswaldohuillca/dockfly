package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"

	"rsc.io/qr"
)

// TOTP (RFC 6238) con los parámetros que entienden todas las apps de
// autenticación: SHA-1, 6 dígitos y pasos de 30 segundos.
const (
	totpPeriod = 30
	totpDigits = 6
	// totpSkew acepta el código anterior y el siguiente, por si el reloj del
	// móvil va algo desfasado.
	totpSkew = 1
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewTOTPSecret genera un secreto de 160 bits en base32.
func NewTOTPSecret() string {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b32.EncodeToString(b)
}

// TOTPURI es el enlace otpauth:// que se muestra como código QR.
func TOTPURI(secret, account, issuer string) string {
	v := url.Values{}
	v.Set("secret", secret)
	v.Set("issuer", issuer)
	v.Set("algorithm", "SHA1")
	v.Set("digits", fmt.Sprint(totpDigits))
	v.Set("period", fmt.Sprint(totpPeriod))
	label := url.PathEscape(issuer) + ":" + url.PathEscape(account)
	return "otpauth://totp/" + label + "?" + v.Encode()
}

// GenerateTOTP calcula el código de secret en el instante t (lo que
// mostraría la app del móvil).
func GenerateTOTP(secret string, t time.Time) string {
	key, err := b32.DecodeString(strings.ToUpper(secret))
	if err != nil {
		return ""
	}
	return totpCode(key, t.Unix()/totpPeriod)
}

func totpCode(key []byte, step int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	n := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", n%1_000_000)
}

// VerifyTOTP comprueba code y devuelve el paso al que corresponde. lastStep
// es el último paso aceptado para este usuario: un código ya usado no vale
// otra vez (aunque siga dentro de su ventana de 30 s).
func VerifyTOTP(secret, code string, now time.Time, lastStep int64) (step int64, ok bool) {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != totpDigits {
		return 0, false
	}
	key, err := b32.DecodeString(strings.ToUpper(secret))
	if err != nil {
		return 0, false
	}
	cur := now.Unix() / totpPeriod
	for s := cur - totpSkew; s <= cur+totpSkew; s++ {
		if s <= lastStep {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(totpCode(key, s)), []byte(code)) == 1 {
			return s, true
		}
	}
	return 0, false
}

// NewRecoveryCodes genera n códigos de recuperación ("abcde-12345").
func NewRecoveryCodes(n int) []string {
	const alphabet = "abcdefghjkmnpqrstuvwxyz23456789" // sin 0/o ni 1/l/i
	codes := make([]string, n)
	for i := range codes {
		b := make([]byte, 10)
		if _, err := rand.Read(b); err != nil {
			panic(err)
		}
		for j := range b {
			b[j] = alphabet[int(b[j])%len(alphabet)]
		}
		codes[i] = string(b[:5]) + "-" + string(b[5:])
	}
	return codes
}

// HashRecoveryCode normaliza y resume un código de recuperación. Tienen
// suficiente entropía (~50 bits) para que baste SHA-256.
func HashRecoveryCode(code string) string {
	code = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(code), " ", ""))
	code = strings.ReplaceAll(code, "-", "")
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

// QRSVG dibuja text como un código QR en SVG.
func QRSVG(text string) (string, error) {
	c, err := qr.Encode(text, qr.M)
	if err != nil {
		return "", err
	}
	const quiet = 4
	size := c.Size + 2*quiet
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges">`, size, size)
	fmt.Fprintf(&b, `<rect width="%d" height="%d" fill="#fff"/><path fill="#000" d="`, size, size)
	// Una línea por cada tramo horizontal de módulos negros.
	for y := 0; y < c.Size; y++ {
		for x := 0; x < c.Size; x++ {
			if !c.Black(x, y) {
				continue
			}
			run := 1
			for x+run < c.Size && c.Black(x+run, y) {
				run++
			}
			fmt.Fprintf(&b, "M%d %dh%dv1h-%dz", x+quiet, y+quiet, run, run)
			x += run - 1
		}
	}
	b.WriteString(`"/></svg>`)
	return b.String(), nil
}
