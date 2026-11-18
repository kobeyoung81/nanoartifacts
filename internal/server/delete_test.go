package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"gitee.com/kobeyoung81/nanoartifacts/internal/store"
)

// The tree tools are what the library's own UI does: create a folder, move a
// row, copy it, delete an empty row. These tests drive them through MCP, which
// is how agents reach them, and check the work rows that move with them.

func decodeTool(t *testing.T, text string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("decode tool result: %v (%s)", err, text)
	}
	return out
}

func publishedNodeID(t *testing.T, s *Server, ctx context.Context, spaceID, artifactID string) string {
	t.Helper()
	nodes, err := s.Store.ListNodes(ctx, spaceID)
	if err != nil {
		t.Fatalf("list nodes: %v", err)
	}
	for _, node := range nodes {
		if node.ArtifactID == artifactID {
			return node.ID
		}
	}
	t.Fatalf("no row shows artifact %s", artifactID)
	return ""
}

func TestMCPTreeWritesAndDeletes(t *testing.T) {
	s, _ := uploadServer(t)
	ctx := context.Background()
	space, err := s.Store.CreateSpace(ctx, "owner-1", "树操作", "members")
	if err != nil {
		t.Fatalf("create space: %v", err)
	}

	text, failed := mcpCall(t, s, "agent-1", "folder_create", map[string]any{
		"space_id": space.ID, "title": "验证目录",
	})
	if failed {
		t.Fatalf("folder_create failed: %s", text)
	}
	folderID, _ := decodeTool(t, text)["node_id"].(string)
	if folderID == "" {
		t.Fatalf("folder_create returned no node_id: %s", text)
	}

	text, failed = mcpCall(t, s, "agent-1", "artifact_publish", map[string]any{
		"space_id": space.ID, "parent_id": folderID, "title": "文档",
		"kind": "doc", "html": "<!doctype html><html><body>one</body></html>",
	})
	if failed {
		t.Fatalf("publish failed: %s", text)
	}
	artifactID, _ := decodeTool(t, text)["artifact_id"].(string)
	docNode := publishedNodeID(t, s, ctx, space.ID, artifactID)

	// Move the document to the space root.
	text, failed = mcpCall(t, s, "agent-1", "node_move", map[string]any{
		"node_id": docNode, "parent_id": "",
	})
	if failed {
		t.Fatalf("node_move failed: %s", text)
	}
	moved, err := s.Store.NodeByID(ctx, docNode)
	if err != nil {
		t.Fatalf("reload moved node: %v", err)
	}
	if moved.ParentID != "" {
		t.Fatalf("parent = %q, want the root", moved.ParentID)
	}

	// Copy it, then delete the copy.
	text, failed = mcpCall(t, s, "agent-1", "node_copy", map[string]any{"node_id": docNode})
	if failed {
		t.Fatalf("node_copy failed: %s", text)
	}
	copied := decodeTool(t, text)
	copyNode, _ := copied["node_id"].(string)
	copyArtifact, _ := copied["artifact_id"].(string)
	if copyNode == "" || copyNode == docNode || copyArtifact == "" || copyArtifact == artifactID {
		t.Fatalf("copy did not produce its own row and work: %s", text)
	}
	text, failed = mcpCall(t, s, "agent-1", "node_delete", map[string]any{"node_id": copyNode})
	if failed {
		t.Fatalf("delete copy failed: %s", text)
	}
	if _, err := s.Store.Get(ctx, copyArtifact); err == nil {
		t.Fatal("copy's work survived the row delete")
	}

	// The folder is empty now, so it may go; re-create one with a row inside to
	// check the refusal first.
	if _, failed = mcpCall(t, s, "agent-1", "folder_create", map[string]any{
		"space_id": space.ID, "title": "非空目录",
	}); failed {
		t.Fatal("second folder_create failed")
	}
	nodes, _ := s.Store.ListNodes(ctx, space.ID)
	var nonEmpty string
	for _, node := range nodes {
		if node.Title == "非空目录" {
			nonEmpty = node.ID
		}
	}
	if _, failed = mcpCall(t, s, "agent-1", "node_move", map[string]any{
		"node_id": docNode, "parent_id": nonEmpty,
	}); failed {
		t.Fatal("move into the new folder failed")
	}
	text, failed = mcpCall(t, s, "agent-1", "node_delete", map[string]any{"node_id": nonEmpty})
	if !failed || !strings.Contains(text, "folder is not empty") {
		t.Fatalf("deleting a non-empty folder = %q (failed=%v), want the refusal", text, failed)
	}
	if _, err := s.Store.NodeByID(ctx, nonEmpty); err != nil {
		t.Fatalf("non-empty folder was removed: %v", err)
	}

	// Empty it: delete the document (its work goes too, nobody else shows it),
	// then the folder.
	if _, failed = mcpCall(t, s, "agent-1", "node_delete", map[string]any{"node_id": docNode}); failed {
		t.Fatal("delete document failed")
	}
	if _, err := s.Store.Get(ctx, artifactID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("document's work survived: %v", err)
	}
	if _, failed = mcpCall(t, s, "agent-1", "node_delete", map[string]any{"node_id": nonEmpty}); failed {
		t.Fatal("delete empty folder failed")
	}
	if _, err := s.Store.NodeByID(ctx, nonEmpty); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("empty folder survived: %v", err)
	}
}

func TestMCPArtifactDeleteRemovesTheTreeRow(t *testing.T) {
	s, _ := uploadServer(t)
	ctx := context.Background()
	space, err := s.Store.CreateSpace(ctx, "owner-1", "删除作品", "members")
	if err != nil {
		t.Fatalf("create space: %v", err)
	}
	text, failed := mcpCall(t, s, "agent-1", "artifact_publish", map[string]any{
		"space_id": space.ID, "title": "要删的文档", "kind": "doc",
		"html": "<!doctype html><html><body>gone</body></html>",
	})
	if failed {
		t.Fatalf("publish failed: %s", text)
	}
	artifactID, _ := decodeTool(t, text)["artifact_id"].(string)
	nodeID := publishedNodeID(t, s, ctx, space.ID, artifactID)

	text, failed = mcpCall(t, s, "agent-1", "artifact_delete", map[string]any{"artifact_id": artifactID})
	if failed {
		t.Fatalf("artifact_delete failed: %s", text)
	}
	out := decodeTool(t, text)
	if out["nodes_deleted"] != float64(1) {
		t.Fatalf("nodes_deleted = %v, want 1 (%s)", out["nodes_deleted"], text)
	}
	if _, err := s.Store.NodeByID(ctx, nodeID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("row survived the work delete: %v", err)
	}
	nodes, err := s.Store.ListNodes(ctx, space.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(nodes) != 0 {
		t.Fatalf("%d rows remain after deleting the work", len(nodes))
	}
}

func TestDeleteNodeEndpointRefusesANonEmptyFolder(t *testing.T) {
	s, ownerToken, _ := moveServer(t)
	ctx := context.Background()
	space, err := s.Store.CreateSpace(ctx, "owner", "非空目录", "members")
	if err != nil {
		t.Fatalf("create space: %v", err)
	}
	root, err := s.Store.CreateNode(ctx, space.ID, "", "folder", "根", "owner", "")
	if err != nil {
		t.Fatalf("create folder: %v", err)
	}
	if _, err := s.Store.CreateNode(ctx, space.ID, root.ID, "document", "子页", "owner", ""); err != nil {
		t.Fatalf("create child: %v", err)
	}

	rec := nodeRequest(t, s, http.MethodDelete, ownerToken, "/api/v1/nodes/"+root.ID, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("delete non-empty folder = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "folder is not empty") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestMCPTreeWritesRefuseAViewer(t *testing.T) {
	s, _ := uploadServer(t)
	ctx := context.Background()
	space, err := s.Store.CreateSpace(ctx, "owner-1", "只读空间", "members")
	if err != nil {
		t.Fatalf("create space: %v", err)
	}
	if err := s.Store.AddGrant(ctx, space.ID, "owner-1", "owner-2", store.RoleViewer); err != nil {
		t.Fatalf("grant viewer: %v", err)
	}

	if _, failed := mcpCall(t, s, "agent-2", "folder_create", map[string]any{
		"space_id": space.ID, "title": "不该建出来",
	}); !failed {
		t.Fatal("a viewer created a folder")
	}
	folder, err := s.Store.CreateNode(ctx, space.ID, "", "folder", "已有", "owner-1", "")
	if err != nil {
		t.Fatalf("create folder: %v", err)
	}
	if _, failed := mcpCall(t, s, "agent-2", "node_delete", map[string]any{"node_id": folder.ID}); !failed {
		t.Fatal("a viewer deleted a folder")
	}
}
