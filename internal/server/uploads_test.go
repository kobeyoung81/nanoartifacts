package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"gitee.com/kobeyoung81/nanoartifacts/internal/blobstore"
	"gitee.com/kobeyoung81/nanoartifacts/internal/identity"
	"gitee.com/kobeyoung81/nanoartifacts/internal/store"
)

// uploadServer wires a real store and a fake portal, so an agent header resolves
// to an owner exactly as it does in production. The bytes path is what these
// tests exercise: upload, claim the handle, publish, download.
//
// The fake answers the portal's internal agent read, which is where that row
// lives. It used to answer a bus path instead, so the fixture and the
// implementation drifted away from the platform together and every test passed
// while the service could not resolve a single agent.
func uploadServer(t *testing.T) (*Server, string) {
	t.Helper()
	uri := os.Getenv("NANOARTIFACTS_MONGO_URI")
	if uri == "" {
		t.Skip("NANOARTIFACTS_MONGO_URI not set")
	}
	parsed, err := url.Parse(uri)
	if err != nil {
		t.Fatalf("parse uri: %v", err)
	}
	var suffix [6]byte
	_, _ = rand.Read(suffix[:])
	parsed.Path = "/nanoartifacts_upload_test_" + hex.EncodeToString(suffix[:])

	st, err := store.Open(context.Background(), parsed.String())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })

	portal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/internal/v1/agents/")
		owner := map[string]string{"agent-1": "owner-1", "agent-2": "owner-2"}[id]
		if owner == "" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"agent": map[string]string{"id": id, "user_id": owner},
		})
	}))
	t.Cleanup(portal.Close)

	// Accounts are nanoauth's; these tests publish, they never look one up.
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Errorf("nanoauth was asked about an account during an upload test")
	}))
	t.Cleanup(auth.Close)

	return &Server{
		Store:    st,
		Blobs:    blobstore.New(st.DB()),
		Identity: identity.New(identity.Options{PortalURL: portal.URL, AuthURL: auth.URL, Token: "bus-token"}),
		BusToken: "internal",
	}, portal.URL
}

func uploadBody(t *testing.T, s *Server, agent, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp/blobs", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer internal")
	req.Header.Set("X-Nanowork-Agent-ID", agent)
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)

	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// mcpCall invokes one tool and returns its text result and whether it failed.
func mcpCall(t *testing.T, s *Server, agent, name string, args map[string]any) (string, bool) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": name, "arguments": args},
	})
	if err != nil {
		t.Fatalf("marshal call: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer internal")
	req.Header.Set("X-Nanowork-Agent-ID", agent)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)

	var out struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode call: %v (%s)", err, rec.Body.String())
	}
	text := ""
	if len(out.Result.Content) > 0 {
		text = out.Result.Content[0].Text
	}
	return text, out.Result.IsError
}

func TestUploadThenPublishByHandle(t *testing.T) {
	s, _ := uploadServer(t)
	page := "<!doctype html><html><body><h1>hand-off</h1></body></html>"

	code, out := uploadBody(t, s, "agent-1", page)
	if code != http.StatusCreated {
		t.Fatalf("upload: %d %v", code, out)
	}
	uri, _ := out["uri"].(string)
	if !strings.HasPrefix(uri, "nanoartifacts://upload/") {
		t.Fatalf("uri = %q", uri)
	}

	text, failed := mcpCall(t, s, "agent-1", "artifact_publish", map[string]any{
		"html_file": uri, "title": "hand-off", "kind": "doc",
	})
	if failed {
		t.Fatalf("publish failed: %s", text)
	}
	var published struct {
		ArtifactID string `json:"artifact_id"`
		SizeBytes  int64  `json:"size_bytes"`
	}
	if err := json.Unmarshal([]byte(text), &published); err != nil {
		t.Fatalf("decode publish: %v (%s)", err, text)
	}
	if published.SizeBytes != int64(len(page)) {
		t.Fatalf("size = %d, want %d", published.SizeBytes, len(page))
	}

	// The bytes are what was uploaded, reached through the agent-only raw route.
	req := httptest.NewRequest(http.MethodGet, "/mcp/artifacts/"+published.ArtifactID+"/versions/1/raw", nil)
	req.Header.Set("Authorization", "Bearer internal")
	req.Header.Set("X-Nanowork-Agent-ID", "agent-1")
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("raw: %d %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != page {
		t.Fatalf("raw body = %q", rec.Body.String())
	}
}

func TestPublishRejectsARawPath(t *testing.T) {
	s, _ := uploadServer(t)
	text, failed := mcpCall(t, s, "agent-1", "artifact_publish", map[string]any{
		"html_file": "artifacts/page.html", "title": "page",
	})
	if !failed {
		t.Fatalf("a path the client never uploaded must not publish: %s", text)
	}
	if !strings.Contains(text, "not uploaded by the client") {
		t.Fatalf("error text = %q", text)
	}
}

func TestPublishRejectsAnotherOwnersHandle(t *testing.T) {
	s, _ := uploadServer(t)
	code, out := uploadBody(t, s, "agent-1", "<html>mine</html>")
	if code != http.StatusCreated {
		t.Fatalf("upload: %d %v", code, out)
	}
	text, failed := mcpCall(t, s, "agent-2", "artifact_publish", map[string]any{
		"html_file": out["uri"], "title": "stolen",
	})
	if !failed {
		t.Fatalf("another owner's handle must not publish: %s", text)
	}
}

func TestUploadHandleIsSingleUse(t *testing.T) {
	s, _ := uploadServer(t)
	code, out := uploadBody(t, s, "agent-1", "<html>once</html>")
	if code != http.StatusCreated {
		t.Fatalf("upload: %d %v", code, out)
	}
	if text, failed := mcpCall(t, s, "agent-1", "artifact_publish", map[string]any{
		"html_file": out["uri"], "title": "first", "kind": "doc",
	}); failed {
		t.Fatalf("first publish failed: %s", text)
	}
	if text, failed := mcpCall(t, s, "agent-1", "artifact_publish", map[string]any{
		"html_file": out["uri"], "title": "second",
	}); !failed {
		t.Fatalf("a consumed handle must not publish twice: %s", text)
	}
}

func TestExpiredUploadHandleIsRefused(t *testing.T) {
	s, _ := uploadServer(t)
	ctx := context.Background()
	up := &store.Upload{
		ID:          "expired-handle",
		OwnerUserID: "owner-1",
		ContentHash: hashOf("<html>old</html>"),
		SizeBytes:   15,
		CreatedAt:   time.Now().UTC().Add(-time.Hour),
		ExpiresAt:   time.Now().UTC().Add(-time.Minute),
	}
	if err := s.Store.CreateUpload(ctx, up); err != nil {
		t.Fatalf("create upload: %v", err)
	}
	text, failed := mcpCall(t, s, "agent-1", "artifact_publish", map[string]any{
		"html_file": uploadURIPrefix + up.ID, "title": "old",
	})
	if !failed {
		t.Fatalf("an expired handle must not publish: %s", text)
	}
}

func TestArtifactGetDownloadToReturnsADescriptor(t *testing.T) {
	s, _ := uploadServer(t)
	page := "<html><body>download me</body></html>"
	code, out := uploadBody(t, s, "agent-1", page)
	if code != http.StatusCreated {
		t.Fatalf("upload: %d %v", code, out)
	}
	text, failed := mcpCall(t, s, "agent-1", "artifact_publish", map[string]any{
		"html_file": out["uri"], "title": "downloadable", "kind": "doc",
	})
	if failed {
		t.Fatalf("publish failed: %s", text)
	}
	var published struct {
		ArtifactID string `json:"artifact_id"`
	}
	if err := json.Unmarshal([]byte(text), &published); err != nil {
		t.Fatalf("decode publish: %v", err)
	}

	text, failed = mcpCall(t, s, "agent-1", "artifact_get", map[string]any{
		"artifact_id": published.ArtifactID, "download_to": "artifacts/page.html",
	})
	if failed {
		t.Fatalf("get failed: %s", text)
	}
	if strings.Contains(text, "<html>") {
		t.Fatalf("download_to must not inline the body: %s", text)
	}
	var got struct {
		Download struct {
			RawPath     string `json:"raw_path"`
			SizeBytes   int64  `json:"size_bytes"`
			ContentHash string `json:"content_hash"`
		} `json:"download"`
	}
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("decode get: %v (%s)", err, text)
	}
	if got.Download.RawPath == "" || got.Download.SizeBytes != int64(len(page)) {
		t.Fatalf("descriptor = %+v", got.Download)
	}
	if got.Download.ContentHash != hashOf(page) {
		t.Fatalf("content_hash = %q", got.Download.ContentHash)
	}
}

func TestInlineHTMLStillPublishes(t *testing.T) {
	s, _ := uploadServer(t)
	text, failed := mcpCall(t, s, "agent-1", "artifact_publish", map[string]any{
		"html": "<html><body>inline</body></html>", "title": "inline", "kind": "doc",
	})
	if failed {
		t.Fatalf("inline publish failed: %s", text)
	}
	if !strings.Contains(text, "artifact_id") {
		t.Fatalf("result = %s", text)
	}
}

func TestPublishNeedsSomeContent(t *testing.T) {
	s, _ := uploadServer(t)
	text, failed := mcpCall(t, s, "agent-1", "artifact_publish", map[string]any{"title": "empty"})
	if !failed || !strings.Contains(text, "html_file is required") {
		t.Fatalf("failed=%v text=%q", failed, text)
	}
}

func TestUploadNeedsAnAgent(t *testing.T) {
	s, _ := uploadServer(t)
	req := httptest.NewRequest(http.MethodPost, "/mcp/blobs", strings.NewReader("<html></html>"))
	req.Header.Set("Authorization", "Bearer internal")
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/mcp/blobs", strings.NewReader("<html></html>"))
	req.Header.Set("Authorization", "Bearer wrong")
	req.Header.Set("X-Nanowork-Agent-ID", "agent-1")
	rec = httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestUploadRejectsAnEmptyBody(t *testing.T) {
	s, _ := uploadServer(t)
	code, out := uploadBody(t, s, "agent-1", "")
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%v)", code, out)
	}
}
