package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The bus probes a server with initialize + tools/list before an
// administrator can enable it, and a probe has no agent behind it. Requiring
// the agent header on the whole endpoint made enabling the server fail with
// 400 before a single tool was listed.
func TestMCPProbeNeedsNoAgentIdentity(t *testing.T) {
	s := &Server{BusToken: "shared"}
	mux := s.Routes()

	call := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(body))
		req.Header.Set("Authorization", "Bearer shared")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	rec := call(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("initialize: %d %s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("protocolVersion")) {
		t.Fatalf("initialize body: %s", rec.Body.String())
	}

	rec = call(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("tools/list: %d %s", rec.Code, rec.Body.String())
	}
	var listed struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode tools/list: %v", err)
	}
	// artifact_publish, artifact_list, space_list, tree_get, artifact_get,
	// artifact_rename, artifact_grant, artifact_delete, folder_create,
	// node_move, node_copy, node_delete.
	if len(listed.Result.Tools) != 12 {
		t.Fatalf("tools = %d, want 12", len(listed.Result.Tools))
	}

	// Calling a tool is where identity matters.
	rec = call(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"artifact_list","arguments":{}}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("tools/call status: %d", rec.Code)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("X-Nanowork-Agent-ID is required")) {
		t.Fatalf("tools/call body: %s", rec.Body.String())
	}
}

func TestMCPRejectsAWrongToken(t *testing.T) {
	s := &Server{BusToken: "shared"}
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	req.Header.Set("Authorization", "Bearer wrong")
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}
