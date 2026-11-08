package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"strings"
	"testing"

	"gitee.com/kobeyoung81/nanoartifacts/migrations"
)

// These tests need a real MongoDB: the behaviour under test (unique indexes,
// the atomic version counter, aggregation) is the database's, not Go's. Without
// NANOARTIFACTS_MONGO_URI they skip rather than pretend to pass.
func testStore(t *testing.T) *Store {
	t.Helper()
	uri := os.Getenv("NANOARTIFACTS_MONGO_URI")
	if uri == "" {
		t.Skip("NANOARTIFACTS_MONGO_URI not set")
	}
	u, err := url.Parse(uri)
	if err != nil {
		t.Fatalf("parse uri: %v", err)
	}
	var b [6]byte
	_, _ = rand.Read(b[:])
	u.Path = "/nanoartifacts_test_" + hex.EncodeToString(b[:])

	st, err := Open(context.Background(), u.String())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		_ = st.DB().Drop(context.Background())
		_ = st.Close(context.Background())
	})

	if _, err := st.Migrate(manifests(t)); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return st
}

func manifests(t *testing.T) map[string]string {
	t.Helper()
	entries, err := migrations.FS.ReadDir(".")
	if err != nil {
		t.Fatalf("read manifests: %v", err)
	}
	out := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		body, err := migrations.FS.ReadFile(e.Name())
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		out[e.Name()] = string(body)
	}
	if len(out) == 0 {
		t.Fatal("no manifests embedded")
	}
	return out
}

func TestCreatePublishAndRollback(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	a, v1, err := st.Create(ctx, "u1", "Quarterly review", "doc", "hash-1", "first", 100)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if a.CurrentVersion != 1 || v1.Seq != 1 {
		t.Fatalf("first version: artifact %d, version %d", a.CurrentVersion, v1.Seq)
	}

	v2, a2, err := st.Publish(ctx, a.ID, "u1", "hash-2", "second", 200)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if v2.Seq != 2 || a2.CurrentVersion != 2 {
		t.Fatalf("second version: version %d, current %d", v2.Seq, a2.CurrentVersion)
	}

	// The same bytes again: no new row, but the pointer still ends up there.
	v3, a3, err := st.Publish(ctx, a.ID, "u1", "hash-2", "same bytes", 200)
	if err != nil {
		t.Fatalf("republish: %v", err)
	}
	if v3.Seq != 2 || a3.CurrentVersion != 2 {
		t.Fatalf("republish created a version: %d/%d", v3.Seq, a3.CurrentVersion)
	}
	versions, err := st.Versions(ctx, a.ID)
	if err != nil {
		t.Fatalf("versions: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("got %d versions, want 2", len(versions))
	}

	rolled, err := st.Rollback(ctx, a.ID, "u1", 1)
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if rolled.CurrentVersion != 1 {
		t.Fatalf("rollback left current at %d", rolled.CurrentVersion)
	}
	if versions, _ := st.Versions(ctx, a.ID); len(versions) != 2 {
		t.Fatalf("rollback deleted history: %d versions", len(versions))
	}
}

func TestPublishOnlyByOwner(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	a, _, err := st.Create(ctx, "owner", "T", "doc", "h1", "", 10)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, _, err := st.Publish(ctx, a.ID, "stranger", "h2", "", 10); err != ErrForbidden {
		t.Fatalf("a non-owner published: %v", err)
	}
}

func TestVisibilityFollowsGrants(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	a, _, err := st.Create(ctx, "owner", "T", "doc", "h1", "", 10)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if ok, _ := st.CanView(ctx, a, "stranger"); ok {
		t.Fatal("a stranger could view without a grant")
	}
	if err := st.Grant(ctx, a.ID, "owner", "stranger"); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if ok, _ := st.CanView(ctx, a, "stranger"); !ok {
		t.Fatal("the grant did not take effect")
	}
	if err := st.Revoke(ctx, a.ID, "owner", "stranger"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if ok, _ := st.CanView(ctx, a, "stranger"); ok {
		t.Fatal("the revoked grant still applies")
	}
}

func TestQuotaCountsDistinctContentOnce(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	if _, _, err := st.Create(ctx, "u1", "A", "doc", "same", "", 100); err != nil {
		t.Fatalf("create A: %v", err)
	}
	if _, _, err := st.Create(ctx, "u1", "B", "doc", "same", "", 100); err != nil {
		t.Fatalf("create B: %v", err)
	}
	used, err := st.QuotaUsed(ctx, "u1")
	if err != nil {
		t.Fatalf("quota: %v", err)
	}
	if used != 100 {
		t.Fatalf("used = %d, want 100 (one stored body)", used)
	}
}

func TestSoftDeleteHidesEverywhere(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	a, _, err := st.Create(ctx, "u1", "T", "doc", "h1", "", 10)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := st.SoftDelete(ctx, a.ID, "u1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := st.Get(ctx, a.ID); err != ErrNotFound {
		t.Fatalf("get after delete: %v", err)
	}
	items, err := st.List(ctx, "u1", ScopeOwned, "", "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("a deleted artifact is still listed")
	}
}

func TestListSharedScopes(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	a, _, err := st.Create(ctx, "owner", "Shared", "doc", "h1", "", 10)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := st.Grant(ctx, a.ID, "owner", "viewer"); err != nil {
		t.Fatalf("grant: %v", err)
	}

	shared, err := st.List(ctx, "viewer", ScopeShared, "", "")
	if err != nil {
		t.Fatalf("list shared: %v", err)
	}
	if len(shared) != 1 || shared[0].ID != a.ID {
		t.Fatalf("shared list = %+v", shared)
	}
	owned, err := st.List(ctx, "viewer", ScopeOwned, "", "")
	if err != nil {
		t.Fatalf("list owned: %v", err)
	}
	if len(owned) != 0 {
		t.Fatalf("someone else's artifact appears in owned: %+v", owned)
	}
}

func TestCreateRejectsUnknownKind(t *testing.T) {
	st := testStore(t)
	_, _, err := st.Create(context.Background(), "u1", "T", "xls", "h", "", 1)
	if err == nil || !strings.Contains(err.Error(), "unknown kind") {
		t.Fatalf("err = %v", err)
	}
}

func TestQuotaIgnoresOtherUsers(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	if _, _, err := st.Create(ctx, "u1", "A", "doc", "h1", "", 100); err != nil {
		t.Fatalf("create: %v", err)
	}
	if used, _ := st.QuotaUsed(ctx, "u2"); used != 0 {
		t.Fatalf("u2 used = %d", used)
	}
}
