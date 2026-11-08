package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Uploads are bodies an agent has handed over but not published yet.
//
// The point is that the document never travels as a tool argument: an agent
// writes the file in its workspace, the runtime posts the bytes here, and the
// publish call names the handle below instead of the HTML. The handle is minted
// by this service (never a content hash — bodies are deduplicated globally, so a
// hash would let a caller reference bytes it never uploaded), it belongs to one
// owner, it expires, and it can be consumed once.
const uploadsCollection = "uploads"

// UploadTTL is how long a body waits for the publish call that claims it.
const UploadTTL = 15 * time.Minute

// Upload is one pending body.
type Upload struct {
	ID          string     `bson:"_id"`
	OwnerUserID string     `bson:"owner_user_id"`
	ContentHash string     `bson:"content_hash"`
	SizeBytes   int64      `bson:"size_bytes"`
	CreatedAt   time.Time  `bson:"created_at"`
	ExpiresAt   time.Time  `bson:"expires_at"`
	ConsumedAt  *time.Time `bson:"consumed_at,omitempty"`
}

// CreateUpload records a handle for a body that is already in the blob store.
func (s *Store) CreateUpload(ctx context.Context, u *Upload) error {
	if u.ID == "" || u.OwnerUserID == "" || u.ContentHash == "" {
		return fmt.Errorf("%w: upload needs an id, an owner and a hash", ErrInvalid)
	}
	if _, err := s.db.Collection(uploadsCollection).InsertOne(ctx, u); err != nil {
		return fmt.Errorf("record upload: %w", err)
	}
	return nil
}

// TakeUpload consumes a handle.
//
// The conditions live in the update filter, not in Go: "mine, unconsumed,
// unexpired" is decided by the database in one round trip, so two concurrent
// publishes cannot both claim the same body. A miss is ErrNotFound for all three
// reasons — telling a caller which one applies would leak handles it does not own.
func (s *Store) TakeUpload(ctx context.Context, id, owner string) (*Upload, error) {
	now := time.Now().UTC()
	var out Upload
	err := s.db.Collection(uploadsCollection).FindOneAndUpdate(ctx,
		bson.D{
			{Key: "_id", Value: id},
			{Key: "owner_user_id", Value: owner},
			{Key: "consumed_at", Value: nil},
			{Key: "expires_at", Value: bson.D{{Key: "$gt", Value: now}}},
		},
		bson.D{{Key: "$set", Value: bson.D{{Key: "consumed_at", Value: now}}}},
		options.FindOneAndUpdate().SetReturnDocument(options.Before),
	).Decode(&out)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("take upload: %w", err)
	}
	return &out, nil
}

// HasPendingUpload reports whether the owner already has these exact bytes
// waiting. Quota is charged after dedupe, so an upload of content the owner
// already holds must not be refused — and a retried publish must not pay twice.
func (s *Store) HasPendingUpload(ctx context.Context, owner, hash string) (bool, error) {
	count, err := s.db.Collection(uploadsCollection).CountDocuments(ctx, bson.D{
		{Key: "owner_user_id", Value: owner},
		{Key: "content_hash", Value: hash},
		{Key: "consumed_at", Value: nil},
		{Key: "expires_at", Value: bson.D{{Key: "$gt", Value: time.Now().UTC()}}},
	})
	if err != nil {
		return false, fmt.Errorf("count uploads: %w", err)
	}
	return count > 0, nil
}

// DeleteExpiredUploads removes handles past their deadline. The gc command calls
// it; publish does not need it because TakeUpload checks the deadline itself.
func (s *Store) DeleteExpiredUploads(ctx context.Context, now time.Time) (int64, error) {
	res, err := s.db.Collection(uploadsCollection).DeleteMany(ctx, bson.D{
		{Key: "expires_at", Value: bson.D{{Key: "$lt", Value: now}}},
	})
	if err != nil {
		return 0, fmt.Errorf("delete expired uploads: %w", err)
	}
	return res.DeletedCount, nil
}
