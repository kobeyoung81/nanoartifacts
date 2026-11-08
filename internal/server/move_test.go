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
	"testing"
	"time"

	"gitee.com/kobeyoung81/nanoartifacts/internal/sign"
	"gitee.com/kobeyoung81/nanoartifacts/internal/store"
)

// moveServer wires a real store and two credentials: the space owner and a
// viewer. The handler under test is thin (parse, check role, delegate), so what
// matters here is the role gate and the shape of the response — the ordering
// itself is covered in internal/store.
func moveServer(t *testing.T) (*Server, string, string) {
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
	parsed.Path = "/nanoartifacts_server_test_" + hex.EncodeToString(suffix[:])

	st, err := store.Open(context.Background(), parsed.String())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })

	signer := sign.New("test-secret", time.Hour)
	ownerToken, _ := signer.Session("owner")
	viewerToken, _ := signer.Session("viewer")
	return &Server{Store: st, Signer: signer, BusToken: "internal"}, ownerToken, viewerToken
}

func moveRequest(t *testing.T, s *Server, token, nodeID string, body any) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/nodes/"+nodeID+"/move", bytes.NewReader(encoded))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)
	return rec
}

func TestMoveNodeEndpoint(t *testing.T) {
	s, ownerToken, viewerToken := moveServer(t)
	ctx := context.Background()

	space, err := s.Store.CreateSpace(ctx, "owner", "拖拽测试", "members")
	if err != nil {
		t.Fatalf("create space: %v", err)
	}
	first, err := s.Store.CreateNode(ctx, space.ID, "", "folder", "一", "owner", "")
	if err != nil {
		t.Fatalf("create 一: %v", err)
	}
	second, err := s.Store.CreateNode(ctx, space.ID, "", "folder", "二", "owner", "")
	if err != nil {
		t.Fatalf("create 二: %v", err)
	}

	// The owner may drop 二 in front of 一.
	rec := moveRequest(t, s, ownerToken, second.ID, map[string]string{"parent_id": "", "before_id": first.ID})
	if rec.Code != http.StatusOK {
		t.Fatalf("owner move = %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Node struct {
			ID       string `json:"id"`
			Position int    `json:"position"`
		} `json:"node"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Node.ID != second.ID || body.Node.Position != 0 {
		t.Fatalf("moved node = %+v, want id %s at position 0", body.Node, second.ID)
	}

	// A viewer may not.
	if err := s.Store.AddGrant(ctx, space.ID, "owner", "viewer", store.RoleViewer); err != nil {
		t.Fatalf("grant viewer: %v", err)
	}
	rec = moveRequest(t, s, viewerToken, first.ID, map[string]string{"parent_id": "", "before_id": second.ID})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer move = %d, want 403: %s", rec.Code, rec.Body.String())
	}
}

func nodeRequest(t *testing.T, s *Server, method, token, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		reader = bytes.NewReader(encoded)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)
	return rec
}

// A document's title lives in two rows. Renaming from the tree has to move both,
// otherwise the tree says one thing and the document header another.
func TestRenameNodeAlsoRenamesTheArtifact(t *testing.T) {
	s, ownerToken, _ := moveServer(t)
	ctx := context.Background()
	space, err := s.Store.CreateSpace(ctx, "owner", "重命名", "members")
	if err != nil {
		t.Fatalf("create space: %v", err)
	}
	artifact, _, err := s.Store.Create(ctx, "owner", "旧名", "doc", "hash-rename", "", 8)
	if err != nil {
		t.Fatalf("create artifact: %v", err)
	}
	node, err := s.Store.CreateNode(ctx, space.ID, "", "document", "旧名", "owner", artifact.ID)
	if err != nil {
		t.Fatalf("create node: %v", err)
	}

	rec := nodeRequest(t, s, http.MethodPatch, ownerToken, "/api/v1/nodes/"+node.ID, map[string]string{"title": "新名"})
	if rec.Code != http.StatusOK {
		t.Fatalf("rename = %d: %s", rec.Code, rec.Body.String())
	}
	got, err := s.Store.Get(ctx, artifact.ID)
	if err != nil {
		t.Fatalf("get artifact: %v", err)
	}
	if got.Title != "新名" {
		t.Fatalf("artifact title = %q, want 新名", got.Title)
	}
}

func TestCopyNodeEndpoint(t *testing.T) {
	s, ownerToken, viewerToken := moveServer(t)
	ctx := context.Background()
	source, err := s.Store.CreateSpace(ctx, "owner", "源", "members")
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	target, err := s.Store.CreateSpace(ctx, "owner", "目标", "members")
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	artifact, _, err := s.Store.Create(ctx, "owner", "手册", "doc", "hash-copy", "", 9)
	if err != nil {
		t.Fatalf("create artifact: %v", err)
	}
	node, err := s.Store.CreateNode(ctx, source.ID, "", "document", "手册", "owner", artifact.ID)
	if err != nil {
		t.Fatalf("create node: %v", err)
	}

	rec := nodeRequest(t, s, http.MethodPost, ownerToken, "/api/v1/nodes/"+node.ID+"/copy",
		map[string]string{"space_id": target.ID, "parent_id": ""})
	if rec.Code != http.StatusCreated {
		t.Fatalf("copy = %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Node struct {
			ID      string `json:"id"`
			SpaceID string `json:"space_id"`
		} `json:"node"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Node.SpaceID != target.ID || body.Node.ID == node.ID {
		t.Fatalf("copied node = %+v", body.Node)
	}

	// A viewer in the source space cannot lift content into another space.
	if err := s.Store.AddGrant(ctx, source.ID, "owner", "viewer", store.RoleViewer); err != nil {
		t.Fatalf("grant: %v", err)
	}
	rec = nodeRequest(t, s, http.MethodPost, viewerToken, "/api/v1/nodes/"+node.ID+"/copy",
		map[string]string{"space_id": target.ID})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer copy = %d, want 403: %s", rec.Code, rec.Body.String())
	}
}
