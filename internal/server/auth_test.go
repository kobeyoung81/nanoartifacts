package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gitee.com/kobeyoung81/nanoartifacts/internal/sign"
)

// A top-level tab has no token to present: the session cookie is the credential
// it carries. These tests pin the middleware, not the handlers, so they need no
// store.
func authServer(t *testing.T) (*Server, string) {
	t.Helper()
	signer := sign.New("test-secret", time.Hour)
	token, _ := signer.Session("user-1")
	return &Server{Signer: signer, BusToken: "internal"}, token
}

func cookieRequest(method, token string) *http.Request {
	req := httptest.NewRequest(method, "/api/v1/session", nil)
	if token != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	}
	return req
}

func serveAuth(s *Server, r *http.Request) *httptest.ResponseRecorder {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(userOf(r)))
	})
	rec := httptest.NewRecorder()
	s.auth(next).ServeHTTP(rec, r)
	return rec
}

func TestCookieAuthenticatesReads(t *testing.T) {
	s, token := authServer(t)
	rec := serveAuth(s, cookieRequest(http.MethodGet, token))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "user-1" {
		t.Fatalf("subject = %q", rec.Body.String())
	}
}

func TestCookieWritesRequireSameOrigin(t *testing.T) {
	s, token := authServer(t)

	req := cookieRequest(http.MethodPost, token)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	if rec := serveAuth(s, req); rec.Code != http.StatusForbidden {
		t.Fatalf("cross-site write status = %d, want 403", rec.Code)
	}

	req = cookieRequest(http.MethodPost, token)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	if rec := serveAuth(s, req); rec.Code != http.StatusOK {
		t.Fatalf("same-origin write status = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCookieWriteRejectsForeignOriginHeader(t *testing.T) {
	s, token := authServer(t)
	req := cookieRequest(http.MethodPut, token)
	req.Header.Set("Origin", "https://evil.example")
	if rec := serveAuth(s, req); rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestNoCredentialIsUnauthorized(t *testing.T) {
	s, _ := authServer(t)
	if rec := serveAuth(s, cookieRequest(http.MethodGet, "")); rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestExpiredCookieIsUnauthorized(t *testing.T) {
	signer := sign.New("test-secret", time.Second)
	token, _ := signer.Session("user-1")
	time.Sleep(1100 * time.Millisecond)

	s := &Server{Signer: signer}
	if rec := serveAuth(s, cookieRequest(http.MethodGet, token)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// GET /session is what a tab calls to find out whether it is still authorized.
func TestSessionInfoWithoutStore(t *testing.T) {
	s, token := authServer(t)
	mux := s.Routes()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/session", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, `"user_id":"user-1"`) {
		t.Fatalf("body = %s", body)
	}
}
