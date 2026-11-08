package blobstore

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/url"
	"os"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// The store's value is in what MongoDB does — chunk documents, index lookups —
// so these tests need a real database and skip without one.
func testGridFS(t *testing.T) *GridFS {
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
	u.Path = "/nanoartifacts_blob_" + hex.EncodeToString(b[:])

	client, err := mongo.Connect(options.Client().ApplyURI(u.String()))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	db := client.Database(strings.TrimPrefix(u.Path, "/"))
	t.Cleanup(func() {
		_ = db.Drop(context.Background())
		_ = client.Disconnect(context.Background())
	})
	return New(db)
}

func TestPutOpenAndSeek(t *testing.T) {
	g := testGridFS(t)
	ctx := context.Background()

	// Three chunks plus a tail, so the seek test crosses a boundary.
	body := make([]byte, ChunkSize*2+1234)
	if _, err := rand.Read(body); err != nil {
		t.Fatalf("rand: %v", err)
	}
	hash := hashOf(body)

	size, err := g.Put(ctx, hash, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if size != int64(len(body)) {
		t.Fatalf("size = %d, want %d", size, len(body))
	}

	reader, length, err := g.Open(ctx, hash)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = reader.Close() }()
	if length != int64(len(body)) {
		t.Fatalf("length = %d", length)
	}
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatal("round trip changed the bytes")
	}

	// Random access is what http.ServeContent's Range support depends on.
	offset := int64(ChunkSize + 10)
	if _, err := reader.Seek(offset, io.SeekStart); err != nil {
		t.Fatalf("seek: %v", err)
	}
	window := make([]byte, 64)
	read, err := reader.Read(window)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("read after seek: %v", err)
	}
	if !bytes.Equal(window[:read], body[offset:offset+int64(read)]) {
		t.Fatal("seek returned the wrong window")
	}
}

func TestPutIsIdempotentPerHash(t *testing.T) {
	g := testGridFS(t)
	ctx := context.Background()
	body := []byte("same bytes")
	hash := hashOf(body)

	if _, err := g.Put(ctx, hash, bytes.NewReader(body)); err != nil {
		t.Fatalf("first put: %v", err)
	}
	if _, err := g.Put(ctx, hash, bytes.NewReader(body)); err != nil {
		t.Fatalf("second put: %v", err)
	}

	count, err := g.db.Collection(filesCollection).CountDocuments(ctx, bson.D{{Key: "_id", Value: hash}})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("file documents = %d, want 1", count)
	}
	chunks, err := g.db.Collection(chunksCollection).CountDocuments(ctx, bson.D{{Key: "files_id", Value: hash}})
	if err != nil {
		t.Fatalf("count chunks: %v", err)
	}
	if chunks != 1 {
		t.Fatalf("chunks = %d, want 1", chunks)
	}
}

func TestOrphanChunksAreDiscoverable(t *testing.T) {
	g := testGridFS(t)
	ctx := context.Background()

	// An upload interrupted between chunks and the file document.
	if _, err := g.db.Collection(chunksCollection).InsertOne(ctx, chunkDoc{
		ID: "orphan:0", FileID: "orphan", N: 0, Data: []byte("half"),
	}); err != nil {
		t.Fatalf("insert chunk: %v", err)
	}

	ids, err := g.DistinctFileIDs(ctx)
	if err != nil {
		t.Fatalf("distinct: %v", err)
	}
	if len(ids) != 1 || ids[0] != "orphan" {
		t.Fatalf("ids = %v", ids)
	}
	if ok, _ := g.Exists(ctx, "orphan"); ok {
		t.Fatal("a chunk without a file document reads as existing")
	}
	if err := g.Delete(ctx, "orphan"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if left, _ := g.db.Collection(chunksCollection).CountDocuments(ctx, bson.D{{Key: "files_id", Value: "orphan"}}); left != 0 {
		t.Fatalf("%d chunks survived the delete", left)
	}
}

func TestOpenMissingBody(t *testing.T) {
	g := testGridFS(t)
	if _, _, err := g.Open(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func hashOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
