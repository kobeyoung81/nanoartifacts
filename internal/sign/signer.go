// Package sign mints the short-lived session token the preview frame carries.
//
// The iframe cannot send an Authorization header, so opening a document needs
// some credential in the request itself. Two ways to do that, and this package
// takes the second:
//
//   - put a signed URL in the frame's src. It expires, and when it does the user
//     sees a dead preview with no way to recover but a reload — and the URL, if
//     copied, works for anyone until it expires.
//   - exchange the user's JWT for a cookie scoped to this service (see
//     Session), then load a plain URL. The cookie expires on a work-day
//     schedule, and — the part that matters — every request re-checks the
//     artifact's permissions, so revoking a share takes effect immediately
//     instead of when a URL happens to expire.
//
// The token is opaque to the browser and signed with the service's own secret,
// so no platform token is copied into a cookie.
package sign

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Signer mints and verifies session tokens.
type Signer struct {
	secret []byte
	ttl    time.Duration
}

// New returns a signer. A zero ttl defaults to eight hours: long enough for a
// working day, short enough that a forgotten tab does not stay authorized
// indefinitely.
func New(secret string, ttl time.Duration) *Signer {
	if ttl <= 0 {
		ttl = 8 * time.Hour
	}
	return &Signer{secret: []byte(secret), ttl: ttl}
}

// Session returns a token for the user and when it expires.
func (s *Signer) Session(userID string) (string, time.Time) {
	exp := time.Now().Add(s.ttl).Unix()
	payload := base64.RawURLEncoding.EncodeToString([]byte(userID)) + "." + strconv.FormatInt(exp, 10)
	return payload + "." + s.mac(payload), time.Unix(exp, 0).UTC()
}

// VerifySession returns the user a token belongs to, or an error.
func (s *Signer) VerifySession(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("malformed token")
	}
	payload := parts[0] + "." + parts[1]
	if !hmac.Equal([]byte(s.mac(payload)), []byte(parts[2])) {
		return "", fmt.Errorf("bad signature")
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return "", fmt.Errorf("malformed expiry")
	}
	// exp is the first second the token is no longer valid, so equality is
	// already expired.
	if time.Now().Unix() >= exp {
		return "", fmt.Errorf("expired")
	}
	user, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || len(user) == 0 {
		return "", fmt.Errorf("malformed subject")
	}
	return string(user), nil
}

func (s *Signer) mac(payload string) string {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
