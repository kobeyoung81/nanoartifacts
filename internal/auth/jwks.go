// Package auth validates the platform's user tokens.
//
// It verifies the same RS256 tokens the portal does, against the same JWKS that
// the bus serves. There is no second user database here and no trusted header:
// a user is whoever signed the token, nothing else.
package auth

import (
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Validator caches nanoauth's public keys.
type Validator struct {
	jwksURL string
	token   string // shared internal token; the bus requires it for internal reads
	client  *http.Client

	mu        sync.RWMutex
	keys      map[string]*rsa.PublicKey
	fetchedAt time.Time
	ttl       time.Duration
}

// New returns a validator. token may be empty for a JWKS that needs no auth.
func New(jwksURL, token string) *Validator {
	return &Validator{
		jwksURL: jwksURL,
		token:   token,
		client:  &http.Client{Timeout: 10 * time.Second},
		keys:    map[string]*rsa.PublicKey{},
		ttl:     5 * time.Minute,
	}
}

// UserID verifies the token and returns its subject.
func (v *Validator) UserID(raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("missing token")
	}
	claims := jwt.MapClaims{}
	_, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		if kid == "" {
			return nil, fmt.Errorf("token has no kid")
		}
		return v.key(kid)
	}, jwt.WithValidMethods([]string{"RS256"}))
	if err != nil {
		return "", fmt.Errorf("invalid token: %w", err)
	}
	sub, _ := claims["sub"].(string)
	if sub == "" {
		return "", fmt.Errorf("token has no subject")
	}
	return sub, nil
}

func (v *Validator) key(kid string) (*rsa.PublicKey, error) {
	v.mu.RLock()
	if k, ok := v.keys[kid]; ok && time.Since(v.fetchedAt) < v.ttl {
		v.mu.RUnlock()
		return k, nil
	}
	v.mu.RUnlock()

	v.mu.Lock()
	defer v.mu.Unlock()
	if k, ok := v.keys[kid]; ok && time.Since(v.fetchedAt) < v.ttl {
		return k, nil
	}
	if err := v.refresh(); err != nil {
		return nil, err
	}
	if k, ok := v.keys[kid]; ok {
		return k, nil
	}
	return nil, fmt.Errorf("unknown key %q", kid)
}

func (v *Validator) refresh() error {
	req, err := http.NewRequest(http.MethodGet, v.jwksURL, nil)
	if err != nil {
		return fmt.Errorf("jwks request: %w", err)
	}
	if v.token != "" {
		req.Header.Set("Authorization", "Bearer "+v.token)
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch jwks: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch jwks: HTTP %d", resp.StatusCode)
	}

	var doc struct {
		Keys []struct {
			KID string `json:"kid"`
			KTY string `json:"kty"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return fmt.Errorf("decode jwks: %w", err)
	}

	keys := make(map[string]*rsa.PublicKey, len(doc.Keys))
	for _, k := range doc.Keys {
		if k.KTY != "RSA" || k.KID == "" {
			continue
		}
		nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil {
			continue
		}
		eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil {
			continue
		}
		keys[k.KID] = &rsa.PublicKey{
			N: new(big.Int).SetBytes(nBytes),
			E: int(new(big.Int).SetBytes(eBytes).Int64()),
		}
	}
	if len(keys) == 0 {
		return fmt.Errorf("jwks returned no usable keys")
	}
	v.keys = keys
	v.fetchedAt = time.Now()
	return nil
}
