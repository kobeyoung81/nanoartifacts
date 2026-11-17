package server

import (
	"context"
	"encoding/json"
	"testing"

	"gitee.com/kobeyoung81/nanoartifacts/internal/store"
)

// A work's name is shown from nodes.title, not from artifacts.title. These tests
// pin every write path that can change it, because a rename that stops at the
// artifact row is invisible in the library while the tool result says it worked.

type renameResult struct {
	ArtifactID  string `json:"artifact_id"`
	NodeID      string `json:"node_id"`
	Title       string `json:"title"`
	TreeUpdated bool   `json:"tree_updated"`
}

func decodeRename(t *testing.T, text string) renameResult {
	t.Helper()
	var out renameResult
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("decode rename result: %v (%s)", err, text)
	}
	return out
}

func titledArtifact(t *testing.T, s *Server, ctx context.Context, spaceID, title string) (*store.Artifact, *store.Node) {
	t.Helper()
	artifact, _, err := s.Store.Create(ctx, "owner-1", title, "doc", "hash-"+title, "", 8)
	if err != nil {
		t.Fatalf("create artifact: %v", err)
	}
	node, err := s.Store.CreateNode(ctx, spaceID, "", "document", title, "owner-1", artifact.ID)
	if err != nil {
		t.Fatalf("create node: %v", err)
	}
	return artifact, node
}

func TestMCPRenameByArtifactIDRenamesTheTreeRow(t *testing.T) {
	s, _ := uploadServer(t)
	ctx := context.Background()
	space, err := s.Store.CreateSpace(ctx, "owner-1", "改名测试", "members")
	if err != nil {
		t.Fatalf("create space: %v", err)
	}
	artifact, node := titledArtifact(t, s, ctx, space.ID, "旧名")

	text, failed := mcpCall(t, s, "agent-1", "artifact_rename", map[string]any{
		"artifact_id": artifact.ID, "title": "新名",
	})
	if failed {
		t.Fatalf("rename by artifact_id failed: %s", text)
	}
	out := decodeRename(t, text)
	if out.NodeID != node.ID {
		t.Fatalf("node_id = %q, want %q", out.NodeID, node.ID)
	}
	if !out.TreeUpdated {
		t.Fatalf("tree_updated = false, want true")
	}

	got, err := s.Store.Get(ctx, artifact.ID)
	if err != nil {
		t.Fatalf("get artifact: %v", err)
	}
	if got.Title != "新名" {
		t.Fatalf("artifact title = %q, want 新名", got.Title)
	}
	renamed, err := s.Store.NodeByID(ctx, node.ID)
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	if renamed.Title != "新名" {
		t.Fatalf("node title = %q, want 新名 (the library still shows the old name)", renamed.Title)
	}
}

func TestMCPRenameByArtifactIDRefusesAViewer(t *testing.T) {
	s, _ := uploadServer(t)
	ctx := context.Background()
	space, err := s.Store.CreateSpace(ctx, "owner-1", "只读空间", "members")
	if err != nil {
		t.Fatalf("create space: %v", err)
	}
	artifact, node := titledArtifact(t, s, ctx, space.ID, "旧名")
	if err := s.Store.AddGrant(ctx, space.ID, "owner-1", "owner-2", store.RoleViewer); err != nil {
		t.Fatalf("grant viewer: %v", err)
	}

	text, failed := mcpCall(t, s, "agent-2", "artifact_rename", map[string]any{
		"artifact_id": artifact.ID, "title": "新名",
	})
	if !failed {
		t.Fatalf("viewer rename succeeded: %s", text)
	}

	// The refusal must come before either row is written: a half-applied rename
	// is the drift this test exists to prevent.
	got, err := s.Store.Get(ctx, artifact.ID)
	if err != nil {
		t.Fatalf("get artifact: %v", err)
	}
	if got.Title != "旧名" {
		t.Fatalf("artifact title = %q, want 旧名 (viewer was refused, so nothing may change)", got.Title)
	}
	unchanged, err := s.Store.NodeByID(ctx, node.ID)
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	if unchanged.Title != "旧名" {
		t.Fatalf("node title = %q, want 旧名", unchanged.Title)
	}
}

func TestRepublishWithATitleRenamesTheWorkAndItsNode(t *testing.T) {
	s, _ := uploadServer(t)
	ctx := context.Background()
	space, err := s.Store.CreateSpace(ctx, "owner-1", "重发改名", "members")
	if err != nil {
		t.Fatalf("create space: %v", err)
	}
	artifact, node := titledArtifact(t, s, ctx, space.ID, "旧名")

	text, failed := mcpCall(t, s, "agent-1", "artifact_publish", map[string]any{
		"artifact_id": artifact.ID,
		"title":       "新名",
		"html":        "<!doctype html><html><body>second version</body></html>",
	})
	if failed {
		t.Fatalf("republish failed: %s", text)
	}
	var published struct {
		Seq   int    `json:"seq"`
		Title string `json:"title"`
	}
	if err := json.Unmarshal([]byte(text), &published); err != nil {
		t.Fatalf("decode publish: %v (%s)", err, text)
	}
	if published.Seq != 2 {
		t.Fatalf("seq = %d, want 2", published.Seq)
	}
	if published.Title != "新名" {
		t.Fatalf("response title = %q, want 新名", published.Title)
	}

	got, err := s.Store.Get(ctx, artifact.ID)
	if err != nil {
		t.Fatalf("get artifact: %v", err)
	}
	if got.Title != "新名" {
		t.Fatalf("artifact title = %q, want 新名", got.Title)
	}
	renamed, err := s.Store.NodeByID(ctx, node.ID)
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	if renamed.Title != "新名" {
		t.Fatalf("node title = %q, want 新名", renamed.Title)
	}
}

func TestPublishIntoAnEmptyNodeTakesThePublishedTitle(t *testing.T) {
	s, _ := uploadServer(t)
	ctx := context.Background()
	space, err := s.Store.CreateSpace(ctx, "owner-1", "空节点", "members")
	if err != nil {
		t.Fatalf("create space: %v", err)
	}
	node, err := s.Store.CreateNode(ctx, space.ID, "", "document", "草稿", "owner-1", "")
	if err != nil {
		t.Fatalf("create node: %v", err)
	}

	text, failed := mcpCall(t, s, "agent-1", "artifact_publish", map[string]any{
		"node_id": node.ID,
		"title":   "正式名",
		"kind":    "doc",
		"html":    "<!doctype html><html><body>filled</body></html>",
	})
	if failed {
		t.Fatalf("publish into empty node failed: %s", text)
	}
	var published struct {
		ArtifactID string `json:"artifact_id"`
	}
	if err := json.Unmarshal([]byte(text), &published); err != nil {
		t.Fatalf("decode publish: %v (%s)", err, text)
	}

	got, err := s.Store.Get(ctx, published.ArtifactID)
	if err != nil {
		t.Fatalf("get artifact: %v", err)
	}
	if got.Title != "正式名" {
		t.Fatalf("artifact title = %q, want 正式名", got.Title)
	}
	filled, err := s.Store.NodeByID(ctx, node.ID)
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	if filled.ArtifactID != published.ArtifactID {
		t.Fatalf("node artifact_id = %q, want %q", filled.ArtifactID, published.ArtifactID)
	}
	if filled.Title != "正式名" {
		t.Fatalf("node title = %q, want 正式名", filled.Title)
	}
}

// One work can be filed in more than one tree; a rename has to reach all of them,
// not just the first row NodeForArtifact happens to find.
func TestRenameWorkMovesEveryNodeOfTheWork(t *testing.T) {
	s, _ := uploadServer(t)
	ctx := context.Background()
	space, err := s.Store.CreateSpace(ctx, "owner-1", "双节点", "members")
	if err != nil {
		t.Fatalf("create space: %v", err)
	}
	artifact, first := titledArtifact(t, s, ctx, space.ID, "旧名")
	second, err := s.Store.CreateNode(ctx, space.ID, "", "document", "旧名", "owner-1", artifact.ID)
	if err != nil {
		t.Fatalf("create second node: %v", err)
	}

	moved, err := s.Store.RenameWork(ctx, artifact.ID, "新名")
	if err != nil {
		t.Fatalf("rename work: %v", err)
	}
	if moved != 2 {
		t.Fatalf("moved = %d, want 2", moved)
	}
	for _, id := range []string{first.ID, second.ID} {
		node, err := s.Store.NodeByID(ctx, id)
		if err != nil {
			t.Fatalf("get node %s: %v", id, err)
		}
		if node.Title != "新名" {
			t.Fatalf("node %s title = %q, want 新名", id, node.Title)
		}
	}
}
