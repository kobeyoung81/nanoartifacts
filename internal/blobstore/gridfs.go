// Package blobstore keeps artifact bodies out of the metadata documents.
//
// The implementation is the GridFS layout (`fs.files` + `fs.chunks`, 255 KiB
// chunks, chunk index n) written directly rather than through the driver's
// gridfs package. Two reasons:
//
//   - The file id is the content hash, which is what makes publishing the same
//     bytes twice free: the second publish finds the file and skips the upload.
//   - The reader needs random access for HTTP Range, and the driver's download
//     stream does not promise it. Seeking to a chunk index here is a lookup by
//     {files_id, n}, not a re-read.
package blobstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// ChunkSize matches GridFS's default, so the collections stay readable by
// standard tooling (mongofiles, drivers).
const ChunkSize = 255 * 1024

const (
	filesCollection  = "artifacts_fs.files"
	chunksCollection = "artifacts_fs.chunks"
)

// Store is the narrow surface the server needs; an object store would implement
// the same interface without touching handlers.
type Store interface {
	// Put stores the body under hash unless it is already there. It reports the
	// number of bytes read.
	Put(ctx context.Context, hash string, r io.Reader) (int64, error)
	Exists(ctx context.Context, hash string) (bool, error)
	// Open returns a reader positioned at 0 plus the total length.
	Open(ctx context.Context, hash string) (*Reader, int64, error)
	Delete(ctx context.Context, hash string) error
}

// GridFS implements Store over a MongoDB database.
type GridFS struct{ db *mongo.Database }

// New returns a store over db.
func New(db *mongo.Database) *GridFS { return &GridFS{db: db} }

type fileDoc struct {
	ID         string    `bson:"_id"`
	Length     int64     `bson:"length"`
	ChunkSize  int32     `bson:"chunkSize"`
	UploadDate time.Time `bson:"uploadDate"`
}

type chunkDoc struct {
	ID     string `bson:"_id"`
	FileID string `bson:"files_id"`
	N      int32  `bson:"n"`
	Data   []byte `bson:"data"`
}

// ErrNotFound reports a body that is not stored.
var ErrNotFound = errors.New("blob not found")

// Put writes the body chunk by chunk and records the file document last.
//
// Readers only look for the file document, so an interrupted upload leaves
// chunks that no one can see — the gc pass removes them.
func (g *GridFS) Put(ctx context.Context, hash string, r io.Reader) (int64, error) {
	if hash == "" {
		return 0, fmt.Errorf("hash is required")
	}
	ok, err := g.Exists(ctx, hash)
	if err != nil {
		return 0, err
	}
	if ok {
		// Already stored: drain nothing, report the size on record.
		var f fileDoc
		if err := g.db.Collection(filesCollection).FindOne(ctx, bson.D{{Key: "_id", Value: hash}}).Decode(&f); err != nil {
			return 0, fmt.Errorf("read existing file: %w", err)
		}
		return f.Length, nil
	}

	buf := make([]byte, ChunkSize)
	var (
		total int64
		n     int32
		batch []any
	)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if _, err := g.db.Collection(chunksCollection).InsertMany(ctx, batch); err != nil {
			return fmt.Errorf("insert chunks: %w", err)
		}
		batch = batch[:0]
		return nil
	}

	for {
		read, err := io.ReadFull(r, buf)
		if read > 0 {
			data := make([]byte, read)
			copy(data, buf[:read])
			batch = append(batch, chunkDoc{
				ID:     fmt.Sprintf("%s:%d", hash, n),
				FileID: hash,
				N:      n,
				Data:   data,
			})
			total += int64(read)
			n++
			if len(batch) >= 20 {
				if err := flush(); err != nil {
					return 0, err
				}
			}
		}
		if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return 0, fmt.Errorf("read body: %w", err)
		}
	}
	if err := flush(); err != nil {
		return 0, err
	}

	_, err = g.db.Collection(filesCollection).InsertOne(ctx, fileDoc{
		ID:         hash,
		Length:     total,
		ChunkSize:  ChunkSize,
		UploadDate: time.Now().UTC(),
	})
	if err != nil {
		if isDuplicateKey(err) {
			// A concurrent upload of the same bytes won; both callers asked for
			// the same outcome, so this is success.
			return total, nil
		}
		return 0, fmt.Errorf("insert file: %w", err)
	}
	return total, nil
}

// Exists reports whether a complete file document is present.
func (g *GridFS) Exists(ctx context.Context, hash string) (bool, error) {
	count, err := g.db.Collection(filesCollection).CountDocuments(ctx, bson.D{{Key: "_id", Value: hash}})
	if err != nil {
		return false, fmt.Errorf("check file: %w", err)
	}
	return count > 0, nil
}

// Open returns a seeking reader over the stored body.
func (g *GridFS) Open(ctx context.Context, hash string) (*Reader, int64, error) {
	var f fileDoc
	err := g.db.Collection(filesCollection).FindOne(ctx, bson.D{{Key: "_id", Value: hash}}).Decode(&f)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, 0, ErrNotFound
	}
	if err != nil {
		return nil, 0, fmt.Errorf("read file: %w", err)
	}
	chunkSize := f.ChunkSize
	if chunkSize <= 0 {
		chunkSize = ChunkSize
	}
	return &Reader{db: g.db, hash: hash, size: f.Length, chunkSize: chunkSize, bufN: -1}, f.Length, nil
}

// DistinctFileIDs returns every files_id that has chunks.
func (g *GridFS) DistinctFileIDs(ctx context.Context) ([]string, error) {
	var out []string
	if err := g.db.Collection(chunksCollection).Distinct(ctx, "files_id", bson.D{}).Decode(&out); err != nil {
		return nil, fmt.Errorf("distinct files_id: %w", err)
	}
	return out, nil
}

// FilesBefore lists file documents uploaded before t.
//
// The age threshold is what keeps gc away from an upload that is between its
// chunks and its file document.
func (g *GridFS) FilesBefore(ctx context.Context, t time.Time) ([]string, error) {
	cur, err := g.db.Collection(filesCollection).Find(ctx,
		bson.D{{Key: "uploadDate", Value: bson.D{{Key: "$lt", Value: t}}}})
	if err != nil {
		return nil, fmt.Errorf("list files: %w", err)
	}
	defer func() { _ = cur.Close(ctx) }()

	var out []string
	for cur.Next(ctx) {
		var f fileDoc
		if err := cur.Decode(&f); err != nil {
			return nil, fmt.Errorf("decode file: %w", err)
		}
		out = append(out, f.ID)
	}
	return out, cur.Err()
}

// Delete removes the file document first, so the blob disappears from readers
// before its chunks do.
func (g *GridFS) Delete(ctx context.Context, hash string) error {
	if _, err := g.db.Collection(filesCollection).DeleteOne(ctx, bson.D{{Key: "_id", Value: hash}}); err != nil {
		return fmt.Errorf("delete file: %w", err)
	}
	if _, err := g.db.Collection(chunksCollection).DeleteMany(ctx, bson.D{{Key: "files_id", Value: hash}}); err != nil {
		return fmt.Errorf("delete chunks: %w", err)
	}
	return nil
}

// Reader streams one body and implements io.ReadSeeker for http.ServeContent.
type Reader struct {
	db        *mongo.Database
	hash      string
	size      int64
	chunkSize int32
	pos       int64
	buf       []byte
	bufN      int32
	bufErr    error
}

// Size is the total length.
func (r *Reader) Size() int64 { return r.size }

// Close satisfies io.Closer; the reader holds no pooled resources — each chunk
// is fetched on demand, so there is nothing to release.
func (r *Reader) Close() error { return nil }

// Read implements io.Reader.
func (r *Reader) Read(p []byte) (int, error) {
	if r.pos >= r.size {
		return 0, io.EOF
	}
	if err := r.ensureChunk(); err != nil {
		return 0, err
	}
	offset := r.pos % int64(r.chunkSize)
	if offset >= int64(len(r.buf)) {
		return 0, io.EOF
	}
	n := copy(p, r.buf[offset:])
	r.pos += int64(n)
	return n, nil
}

// Seek implements io.Seeker; anything beyond the end is clamped by Read.
func (r *Reader) Seek(offset int64, whence int) (int64, error) {
	var next int64
	switch whence {
	case io.SeekStart:
		next = offset
	case io.SeekCurrent:
		next = r.pos + offset
	case io.SeekEnd:
		next = r.size + offset
	default:
		return r.pos, fmt.Errorf("invalid whence %d", whence)
	}
	if next < 0 {
		return r.pos, fmt.Errorf("negative position")
	}
	r.pos = next
	r.bufN = -1
	r.buf = nil
	return r.pos, nil
}

func (r *Reader) ensureChunk() error {
	want := int32(r.pos / int64(r.chunkSize))
	if r.bufN == want && r.buf != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var c chunkDoc
	err := r.db.Collection(chunksCollection).FindOne(ctx, bson.D{
		{Key: "files_id", Value: r.hash},
		{Key: "n", Value: want},
	}, options.FindOne()).Decode(&c)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return io.ErrUnexpectedEOF
	}
	if err != nil {
		return fmt.Errorf("read chunk %d: %w", want, err)
	}
	r.buf = c.Data
	r.bufN = want
	return nil
}

func isDuplicateKey(err error) bool {
	var we mongo.WriteException
	if errors.As(err, &we) {
		for _, e := range we.WriteErrors {
			if e.Code == 11000 {
				return true
			}
		}
	}
	return false
}
