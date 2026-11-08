package store

import (
	"context"
	"testing"
)

func TestPersonalSpaceIsCreatedOnce(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	first, err := st.PersonalSpace(ctx, "u1")
	if err != nil {
		t.Fatalf("personal space: %v", err)
	}
	second, err := st.PersonalSpace(ctx, "u1")
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("personal space is not stable: %s vs %s", first.ID, second.ID)
	}
	if first.Kind != "personal" || first.Visibility != "private" {
		t.Fatalf("shape = %+v", first)
	}
}

func TestSpaceMembershipAndRoles(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	space, err := st.CreateSpace(ctx, "owner", "产品知识库", "members")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := st.AddGrant(ctx, space.ID, "owner", "reader", RoleViewer); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if err := st.AddGrant(ctx, space.ID, "owner", "writer", RoleEditor); err != nil {
		t.Fatalf("grant: %v", err)
	}

	for user, want := range map[string]string{
		"owner": RoleOwner, "reader": RoleViewer, "writer": RoleEditor, "stranger": "",
	} {
		got, err := st.Role(ctx, space.ID, user)
		if err != nil {
			t.Fatalf("role %s: %v", user, err)
		}
		if got != want {
			t.Fatalf("role of %s = %q, want %q", user, got, want)
		}
	}

	mine, err := st.ListSpaces(ctx, "reader")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var found bool
	for _, m := range mine {
		if m.ID == space.ID {
			found = true
			if m.Role != RoleViewer {
				t.Fatalf("listed role = %q", m.Role)
			}
		}
	}
	if !found {
		t.Fatal("a granted space is missing from the list")
	}

	if err := st.RemoveGrant(ctx, space.ID, "owner", "reader"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if role, _ := st.Role(ctx, space.ID, "reader"); role != "" {
		t.Fatalf("role after removal = %q", role)
	}
}

func TestPersonalSpaceCannotBeDeleted(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	personal, err := st.PersonalSpace(ctx, "u1")
	if err != nil {
		t.Fatalf("personal: %v", err)
	}
	if err := st.DeleteSpace(ctx, personal.ID, "u1"); err == nil {
		t.Fatal("a personal space was deleted")
	}
}

func TestTreeCreateMoveAndCycle(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	space, err := st.CreateSpace(ctx, "u1", "Team", "members")
	if err != nil {
		t.Fatalf("space: %v", err)
	}

	docs, err := st.CreateNode(ctx, space.ID, "", "folder", "文档", "u1", "")
	if err != nil {
		t.Fatalf("folder: %v", err)
	}
	sub, err := st.CreateNode(ctx, space.ID, docs.ID, "folder", "季度", "u1", "")
	if err != nil {
		t.Fatalf("sub folder: %v", err)
	}
	page, err := st.CreateNode(ctx, space.ID, sub.ID, "document", "复盘", "u1", "art-1")
	if err != nil {
		t.Fatalf("document: %v", err)
	}

	// Moving the parent under its own child would orphan the whole subtree.
	if _, err := st.UpdateNode(ctx, docs.ID, nil, &page.ID); err == nil {
		t.Fatal("a folder was moved into its own descendant")
	}

	if _, err := st.UpdateNode(ctx, page.ID, nil, &docs.ID); err != nil {
		t.Fatalf("valid move: %v", err)
	}
	moved, err := st.NodeByID(ctx, page.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if moved.ParentID != docs.ID {
		t.Fatalf("parent = %q", moved.ParentID)
	}
}

func TestDeleteNodeRemovesSubtree(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	space, _ := st.CreateSpace(ctx, "u1", "Team", "members")
	root, _ := st.CreateNode(ctx, space.ID, "", "folder", "根", "u1", "")
	child, _ := st.CreateNode(ctx, space.ID, root.ID, "document", "子页", "u1", "art-1")

	if err := st.DeleteNode(ctx, root.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := st.NodeByID(ctx, child.ID); err != ErrNotFound {
		t.Fatalf("child survived: %v", err)
	}
	nodes, _ := st.ListNodes(ctx, space.ID)
	if len(nodes) != 0 {
		t.Fatalf("%d nodes remain", len(nodes))
	}
}

func TestBackfillIsIdempotent(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	if _, _, err := st.Create(ctx, "u1", "A", "doc", "h-a", "", 10); err != nil {
		t.Fatalf("create A: %v", err)
	}
	if _, _, err := st.Create(ctx, "u1", "B", "ui", "h-b", "", 10); err != nil {
		t.Fatalf("create B: %v", err)
	}

	first, err := st.BackfillSpaces(ctx, false)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if first.NodesCreated != 2 || first.SpacesCreated != 1 || first.FoldersCreated != 1 {
		t.Fatalf("first run = %+v", first)
	}

	second, err := st.BackfillSpaces(ctx, false)
	if err != nil {
		t.Fatalf("second backfill: %v", err)
	}
	if second.NodesCreated != 0 || second.SpacesCreated != 0 {
		t.Fatalf("second run created work: %+v", second)
	}

	nodes, _ := st.ListNodes(ctx, mustPersonal(t, st, "u1").ID)
	// The folder is a node too: 1 folder + 2 artifacts.
	if len(nodes) != 3 {
		t.Fatalf("nodes = %d, want 3", len(nodes))
	}
	kinds := map[string]bool{}
	for _, n := range nodes {
		if n.ArtifactID != "" {
			kinds[n.Kind] = true
		}
	}
	if !kinds["document"] || !kinds["app"] {
		t.Fatalf("artifact node kinds = %v", kinds)
	}
}

func TestBackfillDryRunWritesNothing(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	if _, _, err := st.Create(ctx, "u1", "A", "doc", "h-a", "", 10); err != nil {
		t.Fatalf("create: %v", err)
	}
	stats, err := st.BackfillSpaces(ctx, true)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if stats.NodesCreated == 0 {
		t.Fatal("dry run reported no work")
	}
	var space Space
	if err := st.spaces().FindOne(ctx, map[string]any{}).Decode(&space); err == nil {
		t.Fatal("dry run created a space")
	}
}

func TestCanViewArtifactFollowsSpace(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	space, _ := st.CreateSpace(ctx, "owner", "Team", "members")
	artifact, _, err := st.Create(ctx, "owner", "Doc", "doc", "h1", "", 10)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := st.CreateNode(ctx, space.ID, "", "document", "Doc", "owner", artifact.ID); err != nil {
		t.Fatalf("node: %v", err)
	}

	if ok, _ := st.CanViewArtifact(ctx, artifact, "stranger"); ok {
		t.Fatal("a stranger could read via the space")
	}
	if err := st.AddGrant(ctx, space.ID, "owner", "member", RoleViewer); err != nil {
		t.Fatalf("grant: %v", err)
	}
	ok, err := st.CanViewArtifact(ctx, artifact, "member")
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !ok {
		t.Fatal("a member could not read the work")
	}
}

func mustPersonal(t *testing.T, st *Store, userID string) *Space {
	t.Helper()
	space, err := st.PersonalSpace(context.Background(), userID)
	if err != nil {
		t.Fatalf("personal space: %v", err)
	}
	return space
}
