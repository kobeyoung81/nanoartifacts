package store

import (
	"context"
	"errors"
	"sort"
	"testing"
)

// The tree's drag-and-drop. CreateNode and UpdateNode can only append, so a drop
// between two rows has to go through MoveNode — and the order it leaves behind
// is what the UI renders, which is why these tests read the tree back rather
// than trusting the returned position.

func childrenOf(t *testing.T, st *Store, spaceID, parentID string) []Node {
	t.Helper()
	nodes, err := st.ListNodes(context.Background(), spaceID)
	if err != nil {
		t.Fatalf("list nodes: %v", err)
	}
	var out []Node
	for _, node := range nodes {
		if node.ParentID == parentID {
			out = append(out, node)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Position != out[j].Position {
			return out[i].Position < out[j].Position
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func titles(nodes []Node) []string {
	out := make([]string, 0, len(nodes))
	for _, node := range nodes {
		out = append(out, node.Title)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestMoveNode_BeforeAndAfter(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	space, err := st.PersonalSpace(ctx, "u1")
	if err != nil {
		t.Fatalf("personal space: %v", err)
	}
	for _, title := range []string{"一", "二", "三"} {
		if _, err := st.CreateNode(ctx, space.ID, "", "folder", title, "u1", ""); err != nil {
			t.Fatalf("create %s: %v", title, err)
		}
	}
	roots := childrenOf(t, st, space.ID, "")
	if got := titles(roots); !equalStrings(got, []string{"一", "二", "三"}) {
		t.Fatalf("initial order = %v", got)
	}

	// 三 to the front.
	if _, err := st.MoveNode(ctx, roots[2].ID, "", "", roots[0].ID); err != nil {
		t.Fatalf("move to front: %v", err)
	}
	if got := titles(childrenOf(t, st, space.ID, "")); !equalStrings(got, []string{"三", "一", "二"}) {
		t.Fatalf("after move to front = %v", got)
	}

	// 三 to the end (empty before_id).
	if _, err := st.MoveNode(ctx, roots[2].ID, "", "", ""); err != nil {
		t.Fatalf("move to end: %v", err)
	}
	if got := titles(childrenOf(t, st, space.ID, "")); !equalStrings(got, []string{"一", "二", "三"}) {
		t.Fatalf("after move to end = %v", got)
	}
}

func TestMoveNode_ReparentsAndRenumbers(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	space, err := st.PersonalSpace(ctx, "u1")
	if err != nil {
		t.Fatalf("personal space: %v", err)
	}
	folder, err := st.CreateNode(ctx, space.ID, "", "folder", "需求", "u1", "")
	if err != nil {
		t.Fatalf("create folder: %v", err)
	}
	a, err := st.CreateNode(ctx, space.ID, folder.ID, "document", "A", "u1", "")
	if err != nil {
		t.Fatalf("create a: %v", err)
	}
	b, err := st.CreateNode(ctx, space.ID, folder.ID, "document", "B", "u1", "")
	if err != nil {
		t.Fatalf("create b: %v", err)
	}
	loose, err := st.CreateNode(ctx, space.ID, "", "document", "散件", "u1", "")
	if err != nil {
		t.Fatalf("create loose: %v", err)
	}

	// Drop the loose document between A and B.
	if _, err := st.MoveNode(ctx, loose.ID, "", folder.ID, b.ID); err != nil {
		t.Fatalf("move into folder: %v", err)
	}
	kids := childrenOf(t, st, space.ID, folder.ID)
	if got := titles(kids); !equalStrings(got, []string{"A", "散件", "B"}) {
		t.Fatalf("folder order = %v", got)
	}
	for i, node := range kids {
		if node.Position != i {
			t.Fatalf("position %d = %d, want %d", i, node.Position, i)
		}
	}
	if got := childrenOf(t, st, space.ID, ""); len(got) != 1 || got[0].ID != folder.ID {
		t.Fatalf("root should hold only the folder, got %v", titles(got))
	}
	_ = a

	// A before_id from another parent is refused rather than silently ignored.
	if _, err := st.MoveNode(ctx, b.ID, "", folder.ID, folder.ID); err == nil {
		t.Fatal("expected an error when before_id is not a sibling")
	}
}

func TestMoveNode_RefusesOwnSubtree(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	space, err := st.PersonalSpace(ctx, "u1")
	if err != nil {
		t.Fatalf("personal space: %v", err)
	}
	outer, err := st.CreateNode(ctx, space.ID, "", "folder", "外", "u1", "")
	if err != nil {
		t.Fatalf("create outer: %v", err)
	}
	inner, err := st.CreateNode(ctx, space.ID, outer.ID, "folder", "内", "u1", "")
	if err != nil {
		t.Fatalf("create inner: %v", err)
	}

	if _, err := st.MoveNode(ctx, outer.ID, "", inner.ID, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("moving a folder into its own child = %v, want ErrInvalid", err)
	}
	// It is still where it was.
	if got := childrenOf(t, st, space.ID, ""); len(got) != 1 || got[0].ID != outer.ID {
		t.Fatalf("tree changed after a refused move: %v", titles(got))
	}
}

func TestMoveNode_RefusesNonFolderParent(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	space, err := st.PersonalSpace(ctx, "u1")
	if err != nil {
		t.Fatalf("personal space: %v", err)
	}
	doc, err := st.CreateNode(ctx, space.ID, "", "document", "文档", "u1", "")
	if err != nil {
		t.Fatalf("create doc: %v", err)
	}
	other, err := st.CreateNode(ctx, space.ID, "", "folder", "目录", "u1", "")
	if err != nil {
		t.Fatalf("create folder: %v", err)
	}
	if _, err := st.MoveNode(ctx, other.ID, "", doc.ID, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("moving under a document = %v, want ErrInvalid", err)
	}
}

// A brand-new space has no nodes. The JSON has to be [] rather than null, or the
// workbench calls .find on null the moment you open it.
func TestListNodes_EmptySpaceIsAnEmptySlice(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	space, err := st.PersonalSpace(ctx, "u1")
	if err != nil {
		t.Fatalf("personal space: %v", err)
	}
	nodes, err := st.ListNodes(ctx, space.ID)
	if err != nil {
		t.Fatalf("list nodes: %v", err)
	}
	if nodes == nil {
		t.Fatal("expected an empty slice, got nil")
	}
	if len(nodes) != 0 {
		t.Fatalf("expected no nodes, got %d", len(nodes))
	}
}

// Crossing spaces moves the whole subtree: a folder that arrived without its
// children would be an empty shell pointing at nodes the space cannot see.
func TestMoveNode_AcrossSpacesTakesTheSubtree(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	home, err := st.PersonalSpace(ctx, "u1")
	if err != nil {
		t.Fatalf("personal space: %v", err)
	}
	team, err := st.CreateSpace(ctx, "u1", "团队", "members")
	if err != nil {
		t.Fatalf("create space: %v", err)
	}
	folder, err := st.CreateNode(ctx, home.ID, "", "folder", "外", "u1", "")
	if err != nil {
		t.Fatalf("create folder: %v", err)
	}
	child, err := st.CreateNode(ctx, home.ID, folder.ID, "folder", "内", "u1", "")
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	if _, err := st.MoveNode(ctx, folder.ID, team.ID, "", ""); err != nil {
		t.Fatalf("move across spaces: %v", err)
	}

	inTeam, err := st.ListNodes(ctx, team.ID)
	if err != nil {
		t.Fatalf("list team: %v", err)
	}
	if len(inTeam) != 2 {
		t.Fatalf("team should hold the folder and its child, got %d", len(inTeam))
	}
	for _, node := range inTeam {
		if node.SpaceID != team.ID {
			t.Fatalf("node %s still points at %s", node.ID, node.SpaceID)
		}
	}
	if moved, err := st.NodeByID(ctx, child.ID); err != nil || moved.SpaceID != team.ID {
		t.Fatalf("child did not move: %+v (%v)", moved, err)
	}
}

func TestCopySubtree_KeepsShapeAndSharesContent(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	source, err := st.PersonalSpace(ctx, "u1")
	if err != nil {
		t.Fatalf("personal space: %v", err)
	}
	team, err := st.CreateSpace(ctx, "u1", "团队", "members")
	if err != nil {
		t.Fatalf("create space: %v", err)
	}

	artifact, _, err := st.Create(ctx, "u1", "手册", "doc", "hash-abc", "", 12)
	if err != nil {
		t.Fatalf("create artifact: %v", err)
	}
	folder, err := st.CreateNode(ctx, source.ID, "", "folder", "资料", "u1", "")
	if err != nil {
		t.Fatalf("create folder: %v", err)
	}
	if _, err := st.CreateNode(ctx, source.ID, folder.ID, "document", "手册", "u1", artifact.ID); err != nil {
		t.Fatalf("create doc: %v", err)
	}

	created, err := st.CopySubtree(ctx, folder.ID, team.ID, "", "u1")
	if err != nil {
		t.Fatalf("copy subtree: %v", err)
	}
	if created.SpaceID != team.ID || created.Kind != "folder" {
		t.Fatalf("copied root = %+v", created)
	}
	kids := childrenOf(t, st, team.ID, created.ID)
	if len(kids) != 1 || kids[0].Kind != "document" || kids[0].ArtifactID == "" {
		t.Fatalf("copied children = %+v", kids)
	}
	if kids[0].ArtifactID == artifact.ID {
		t.Fatal("the copy reuses the source artifact; it must get its own row")
	}

	copied, err := st.Get(ctx, kids[0].ArtifactID)
	if err != nil {
		t.Fatalf("get copied artifact: %v", err)
	}
	if copied.CurrentVersion != 1 || copied.SizeBytes != artifact.SizeBytes {
		t.Fatalf("copied artifact = %+v", copied)
	}
	versions, err := st.Versions(ctx, copied.ID)
	if err != nil || len(versions) != 1 {
		t.Fatalf("copied versions = %+v (%v)", versions, err)
	}
	if versions[0].ContentHash != "hash-abc" {
		t.Fatalf("copy changed the content hash: %s", versions[0].ContentHash)
	}
}
