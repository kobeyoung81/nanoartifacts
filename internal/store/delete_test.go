package store

import (
	"context"
	"errors"
	"testing"
)

// Deleting a work and deleting a row are two halves of one action: the library
// lists works through nodes, so a delete that stops at either row leaves
// something the user cannot reconcile.

func TestDeleteWorkRemovesTheRowAndItsNodes(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	space, _ := st.CreateSpace(ctx, "u1", "Team", "members")
	artifact, _, err := st.Create(ctx, "u1", "文档", "doc", "hash-delete-work", "", 8)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	node, err := st.CreateNode(ctx, space.ID, "", "document", "文档", "u1", artifact.ID)
	if err != nil {
		t.Fatalf("create node: %v", err)
	}

	result, err := st.DeleteWork(ctx, artifact.ID, "u1")
	if err != nil {
		t.Fatalf("delete work: %v", err)
	}
	if result.NodesDeleted != 1 {
		t.Fatalf("nodes deleted = %d, want 1", result.NodesDeleted)
	}
	if _, err := st.Get(ctx, artifact.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("artifact still readable: %v", err)
	}
	if _, err := st.NodeByID(ctx, node.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("node survived: %v", err)
	}
	nodes, err := st.ListNodes(ctx, space.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(nodes) != 0 {
		t.Fatalf("%d nodes remain", len(nodes))
	}
}

func TestDeleteWorkRefusesSomeoneElsesWork(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	artifact, _, err := st.Create(ctx, "u1", "文档", "doc", "hash-delete-owner", "", 8)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := st.DeleteWork(ctx, artifact.ID, "u2"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("delete by a stranger = %v, want ErrForbidden", err)
	}
	if _, err := st.Get(ctx, artifact.ID); err != nil {
		t.Fatalf("artifact was removed by a stranger: %v", err)
	}
}

func TestDeleteNodeRemovesTheWorkItOwns(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	space, _ := st.CreateSpace(ctx, "u1", "Team", "members")
	artifact, _, err := st.Create(ctx, "u1", "文档", "doc", "hash-node-owner", "", 8)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	node, err := st.CreateNode(ctx, space.ID, "", "document", "文档", "u1", artifact.ID)
	if err != nil {
		t.Fatalf("create node: %v", err)
	}

	result, err := st.DeleteNode(ctx, node.ID, "u1")
	if err != nil {
		t.Fatalf("delete node: %v", err)
	}
	if result.ArtifactsDeleted != 1 || result.ArtifactsKept != 0 {
		t.Fatalf("result = %+v, want one artifact deleted", result)
	}
	if _, err := st.Get(ctx, artifact.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("artifact survived the row delete: %v", err)
	}
}

// A space editor removes a member's row, not the member's work: the work stays
// in the owner's library, and the result says so.
func TestDeleteNodeKeepsAWorkSomeoneElseOwns(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	space, _ := st.CreateSpace(ctx, "u1", "Team", "members")
	artifact, _, err := st.Create(ctx, "u2", "别人的文档", "doc", "hash-node-owner-2", "", 8)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	node, err := st.CreateNode(ctx, space.ID, "", "document", "别人的文档", "u1", artifact.ID)
	if err != nil {
		t.Fatalf("create node: %v", err)
	}

	result, err := st.DeleteNode(ctx, node.ID, "u1")
	if err != nil {
		t.Fatalf("delete node: %v", err)
	}
	if result.ArtifactsKept != 1 || result.ArtifactsDeleted != 0 {
		t.Fatalf("result = %+v, want one artifact kept", result)
	}
	if _, err := st.Get(ctx, artifact.ID); err != nil {
		t.Fatalf("another user's work was removed: %v", err)
	}
}

func TestDeleteNodeKeepsAWorkAnotherRowStillShows(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	space, _ := st.CreateSpace(ctx, "u1", "Team", "members")
	artifact, _, err := st.Create(ctx, "u1", "文档", "doc", "hash-node-shared", "", 8)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	first, err := st.CreateNode(ctx, space.ID, "", "document", "文档", "u1", artifact.ID)
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	second, err := st.CreateNode(ctx, space.ID, "", "document", "文档", "u1", artifact.ID)
	if err != nil {
		t.Fatalf("create second: %v", err)
	}

	result, err := st.DeleteNode(ctx, first.ID, "u1")
	if err != nil {
		t.Fatalf("delete first: %v", err)
	}
	if result.ArtifactsKept != 1 {
		t.Fatalf("result = %+v, want the work kept for the other row", result)
	}
	if _, err := st.Get(ctx, artifact.ID); err != nil {
		t.Fatalf("work was removed while a row still shows it: %v", err)
	}

	if _, err := st.DeleteNode(ctx, second.ID, "u1"); err != nil {
		t.Fatalf("delete second: %v", err)
	}
	if _, err := st.Get(ctx, artifact.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("work survived the last row: %v", err)
	}
}

// A row whose work was deleted out of band must not be offered: opening it would
// 404. This is the safety net behind the write paths.
func TestListNodesHidesRowsWhoseWorkIsGone(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	space, _ := st.CreateSpace(ctx, "u1", "Team", "members")
	live, _, err := st.Create(ctx, "u1", "在", "doc", "hash-list-live", "", 8)
	if err != nil {
		t.Fatalf("create live: %v", err)
	}
	gone, _, err := st.Create(ctx, "u1", "没了", "doc", "hash-list-gone", "", 8)
	if err != nil {
		t.Fatalf("create gone: %v", err)
	}
	if err := st.SoftDelete(ctx, gone.ID, "u1"); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	if _, err := st.CreateNode(ctx, space.ID, "", "document", "在", "u1", live.ID); err != nil {
		t.Fatalf("node live: %v", err)
	}
	if _, err := st.CreateNode(ctx, space.ID, "", "document", "没了", "u1", gone.ID); err != nil {
		t.Fatalf("node gone: %v", err)
	}
	if _, err := st.CreateNode(ctx, space.ID, "", "document", "从未发布", "u1", "no-such-artifact"); err != nil {
		t.Fatalf("node dangling: %v", err)
	}

	nodes, err := st.ListNodes(ctx, space.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(nodes) != 1 || nodes[0].ArtifactID != live.ID {
		t.Fatalf("nodes = %+v, want only the live work's row", nodes)
	}
}
