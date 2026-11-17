package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gitee.com/kobeyoung81/nanoartifacts/internal/identity"
)

// These two tests need no database: the byte path settles identity before it
// touches the store, which is the point — an agent that cannot be resolved never
// reaches a write.

func agentHeaderRequest(t *testing.T, s *Server) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp/blobs", strings.NewReader("<html></html>"))
	req.Header.Set("Authorization", "Bearer internal")
	req.Header.Set("X-Nanowork-Agent-ID", "agent-1")
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)
	return rec
}

func TestBlobUploadAnswers403ForAnUnknownAgent(t *testing.T) {
	portal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(portal.Close)
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("nanoauth was asked about an account while resolving an agent")
	}))
	t.Cleanup(auth.Close)

	s := &Server{
		Identity: identity.New(identity.Options{PortalURL: portal.URL, AuthURL: auth.URL, Token: "t"}),
		BusToken: "internal",
	}
	rec := agentHeaderRequest(t, s)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "unknown agent") {
		t.Fatalf("body = %s, want it to name the unknown agent", rec.Body.String())
	}
}

func TestBlobUploadReportsAnUnreachablePortalAsItsOwnFailure(t *testing.T) {
	portal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(portal.Close)
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(auth.Close)

	s := &Server{
		Identity: identity.New(identity.Options{PortalURL: portal.URL, AuthURL: auth.URL, Token: "t"}),
		BusToken: "internal",
	}
	rec := agentHeaderRequest(t, s)
	// A broken owner must not read as "your agent does not exist": that sentence
	// is what kept this outage looking like a caller's mistake.
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "agent directory unavailable") {
		t.Fatalf("body = %s, want the platform-failure wording", rec.Body.String())
	}
}
