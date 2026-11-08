package store

import (
	"context"
	"testing"
	"time"
)

func TestTakeUploadConsumesOnce(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	up := &Upload{
		ID:          "handle-1",
		OwnerUserID: "alice",
		ContentHash: "hash",
		SizeBytes:   10,
		CreatedAt:   time.Now().UTC(),
		ExpiresAt:   time.Now().UTC().Add(UploadTTL),
	}
	if err := st.CreateUpload(ctx, up); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := st.TakeUpload(ctx, "handle-1", "alice")
	if err != nil {
		t.Fatalf("take: %v", err)
	}
	if got.ContentHash != "hash" || got.SizeBytes != 10 {
		t.Fatalf("upload = %+v", got)
	}

	if _, err := st.TakeUpload(ctx, "handle-1", "alice"); err != ErrNotFound {
		t.Fatalf("second take = %v, want ErrNotFound", err)
	}
}

func TestTakeUploadIsOwnerScoped(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	up := &Upload{
		ID:          "handle-2",
		OwnerUserID: "alice",
		ContentHash: "hash",
		SizeBytes:   10,
		CreatedAt:   time.Now().UTC(),
		ExpiresAt:   time.Now().UTC().Add(UploadTTL),
	}
	if err := st.CreateUpload(ctx, up); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := st.TakeUpload(ctx, "handle-2", "bob"); err != ErrNotFound {
		t.Fatalf("other owner = %v, want ErrNotFound", err)
	}
	// The failed attempt must not have consumed the handle.
	if _, err := st.TakeUpload(ctx, "handle-2", "alice"); err != nil {
		t.Fatalf("owner take after a stranger's attempt: %v", err)
	}
}

func TestTakeUploadRefusesAnExpiredHandle(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	up := &Upload{
		ID:          "handle-3",
		OwnerUserID: "alice",
		ContentHash: "hash",
		SizeBytes:   10,
		CreatedAt:   time.Now().UTC().Add(-time.Hour),
		ExpiresAt:   time.Now().UTC().Add(-time.Minute),
	}
	if err := st.CreateUpload(ctx, up); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := st.TakeUpload(ctx, "handle-3", "alice"); err != ErrNotFound {
		t.Fatalf("expired take = %v, want ErrNotFound", err)
	}
}

func TestDeleteExpiredUploadsKeepsLiveHandles(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	live := &Upload{ID: "live", OwnerUserID: "alice", ContentHash: "a", SizeBytes: 1,
		CreatedAt: now, ExpiresAt: now.Add(UploadTTL)}
	dead := &Upload{ID: "dead", OwnerUserID: "alice", ContentHash: "b", SizeBytes: 1,
		CreatedAt: now.Add(-time.Hour), ExpiresAt: now.Add(-time.Minute)}
	for _, u := range []*Upload{live, dead} {
		if err := st.CreateUpload(ctx, u); err != nil {
			t.Fatalf("create %s: %v", u.ID, err)
		}
	}

	removed, err := st.DeleteExpiredUploads(ctx, now)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	// The live handle still works after the sweep.
	if _, err := st.TakeUpload(ctx, "live", "alice"); err != nil {
		t.Fatalf("live handle: %v", err)
	}
}
