package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/skip2/go-qrcode"
)

const totpPeriod = 30

func GenerateTOTPSecret() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b), nil
}

func totpAt(secret string, counter int64) (string, error) {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		return "", err
	}
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(counter))
	mac := hmac.New(sha1.New, key)
	mac.Write(buf[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	code := (int32(sum[offset])&0x7f)<<24 |
		(int32(sum[offset+1])&0xff)<<16 |
		(int32(sum[offset+2])&0xff)<<8 |
		(int32(sum[offset+3]) & 0xff)
	return fmt.Sprintf("%06d", code%1000000), nil
}

func TOTPCode(secret string, t time.Time) (string, error) {
	return totpAt(secret, t.Unix()/totpPeriod)
}

// ValidateTOTP accepts the current step +/- 1 and returns the matching step so
// callers can persist it and reject replays.
func ValidateTOTP(secret, code string, t time.Time) (int64, bool) {
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return 0, false
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	step := t.Unix() / totpPeriod
	for offset := int64(-1); offset <= 1; offset++ {
		candidate, err := totpAt(secret, step+offset)
		if err != nil {
			return 0, false
		}
		if subtle.ConstantTimeCompare([]byte(candidate), []byte(code)) == 1 {
			return step + offset, true
		}
	}
	return 0, false
}

func OTPAuthURL(issuer, account, secret string) string {
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", "6")
	q.Set("period", "30")
	return "otpauth://totp/" + url.PathEscape(issuer+":"+account) + "?" + q.Encode()
}

func QRDataURI(content string) (string, error) {
	png, err := qrcode.Encode(content, qrcode.Medium, 256)
	if err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(png), nil
}

func HashRecoveryCode(code string) string {
	normalized := strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(code), "-", ""), " ", ""))
	sum := sha256.Sum256([]byte(normalized))
	return fmt.Sprintf("%x", sum[:])
}

func GenerateRecoveryCodes(n int) (codes []string, hashes []string, err error) {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	for i := 0; i < n; i++ {
		raw := make([]byte, 10)
		if _, err := rand.Read(raw); err != nil {
			return nil, nil, err
		}
		var b strings.Builder
		for j, v := range raw {
			if j == 5 {
				b.WriteByte('-')
			}
			b.WriteByte(alphabet[int(v)%len(alphabet)])
		}
		code := b.String()
		codes = append(codes, code)
		hashes = append(hashes, HashRecoveryCode(code))
	}
	return codes, hashes, nil
}
