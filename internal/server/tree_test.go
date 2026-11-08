package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A freshly created space has no nodes. The payload must carry [] and not null:
// the browser resolves the selected node with nodes.find on first render, and
// null there is what blanked the page when a space was created and opened.
func TestSpaceTreeOfAnEmptySpaceIsAnArray(t *testing.T) {
	s, ownerToken, _ := moveServer(t)
	space, err := s.Store.CreateSpace(context.Background(), "owner", "空空间", "members")
	if err != nil {
		t.Fatalf("create space: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/spaces/"+space.ID+"/tree", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: ownerToken})
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Nodes json.RawMessage `json:"nodes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(body.Nodes) != "[]" {
		t.Fatalf("nodes = %s, want []", body.Nodes)
	}
}
