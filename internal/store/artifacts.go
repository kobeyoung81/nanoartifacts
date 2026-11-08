package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	artifactsCollection = "artifacts"
	versionsCollection  = "artifacts_versions"
	grantsCollection    = "artifacts_grants"
)

// Kinds this service accepts. The set mirrors nanowork's container kinds plus
// plain HTML, and the extension/suffix pairing is the platform's invariant, not
// something this service re-derives.
var validKinds = map[string]bool{"doc": true, "ppt": true, "ui": true, "pro": true, "html": true}

var (
	// ErrNotFound is returned for an unknown or deleted artifact. Handlers turn
	// it into 404 even when the caller lacks permission, so an id probe cannot
	// distinguish "does not exist" from "not yours".
	ErrNotFound = errors.New("artifact not found")
	// ErrForbidden means the artifact exists but the caller may not touch it.
	ErrForbidden = errors.New("forbidden")
	// ErrInvalid means the request itself is wrong (unknown kind, empty title).
	ErrInvalid = errors.New("invalid request")
)

// Artifact is one published work.
type Artifact struct {
	ID             string     `bson:"_id" json:"id"`
	OwnerUserID    string     `bson:"owner_user_id" json:"owner_user_id"`
	OwnerName      string     `bson:"-" json:"owner_name,omitempty"`
	Title          string     `bson:"title" json:"title"`
	Kind           string     `bson:"kind" json:"kind"`
	CurrentVersion int        `bson:"current_version" json:"current_version"`
	Visibility     string     `bson:"visibility" json:"visibility"`
	SizeBytes      int64      `bson:"size_bytes" json:"size_bytes"`
	CreatedAt      time.Time  `bson:"created_at" json:"created_at"`
	UpdatedAt      time.Time  `bson:"updated_at" json:"updated_at"`
	DeletedAt      *time.Time `bson:"deleted_at,omitempty" json:"-"`
}

// Version is one immutable snapshot. Seq is per artifact and only ever grows.
type Version struct {
	ID           string    `bson:"_id" json:"id"`
	ArtifactID   string    `bson:"artifact_id" json:"artifact_id"`
	Seq          int       `bson:"seq" json:"seq"`
	ContentHash  string    `bson:"content_hash" json:"content_hash"`
	SizeBytes    int64     `bson:"size_bytes" json:"size_bytes"`
	Note         string    `bson:"note,omitempty" json:"note,omitempty"`
	SourceCommit string    `bson:"source_commit,omitempty" json:"source_commit,omitempty"`
	CreatedBy    string    `bson:"created_by" json:"created_by"`
	CreatedAt    time.Time `bson:"created_at" json:"created_at"`
}

// Grant is a viewer permission for one user.
type Grant struct {
	ID         string    `bson:"_id" json:"id"`
	ArtifactID string    `bson:"artifact_id" json:"artifact_id"`
	UserID     string    `bson:"user_id" json:"user_id"`
	Role       string    `bson:"role" json:"role"`
	CreatedAt  time.Time `bson:"created_at" json:"created_at"`
}

// Create inserts a new artifact and its first version.
func (s *Store) Create(ctx context.Context, owner, title, kind, hash, note string, size int64) (*Artifact, *Version, error) {
	if !validKinds[kind] {
		return nil, nil, fmt.Errorf("%w: unknown kind %q", ErrInvalid, kind)
	}
	if strings.TrimSpace(title) == "" {
		return nil, nil, fmt.Errorf("%w: title is required", ErrInvalid)
	}
	if hash == "" || size <= 0 {
		return nil, nil, fmt.Errorf("%w: content is required", ErrInvalid)
	}

	now := time.Now().UTC()
	a := &Artifact{
		ID:             newID(),
		OwnerUserID:    owner,
		Title:          strings.TrimSpace(title),
		Kind:           kind,
		CurrentVersion: 1,
		Visibility:     "private",
		SizeBytes:      size,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if _, err := s.db.Collection(artifactsCollection).InsertOne(ctx, a); err != nil {
		return nil, nil, fmt.Errorf("insert artifact: %w", err)
	}

	v := &Version{
		ID:          newID(),
		ArtifactID:  a.ID,
		Seq:         1,
		ContentHash: hash,
		SizeBytes:   size,
		Note:        note,
		CreatedBy:   owner,
		CreatedAt:   now,
	}
	if _, err := s.db.Collection(versionsCollection).InsertOne(ctx, v); err != nil {
		// The artifact row without a version is unreadable; remove it rather
		// than leave a tombstone that every list would have to filter out.
		_, _ = s.db.Collection(artifactsCollection).DeleteOne(ctx, bson.D{{Key: "_id", Value: a.ID}})
		return nil, nil, fmt.Errorf("insert version: %w", err)
	}
	return a, v, nil
}

// Publish appends a version, or returns the existing one when the content hash
// is unchanged. Republishing identical content is therefore idempotent: it
// moves the current pointer and creates no new row.
func (s *Store) Publish(ctx context.Context, artifactID, userID, hash, note string, size int64) (*Version, *Artifact, error) {
	a, err := s.Get(ctx, artifactID)
	if err != nil {
		return nil, nil, err
	}
	if a.OwnerUserID != userID {
		return nil, nil, ErrForbidden
	}
	if hash == "" || size <= 0 {
		return nil, nil, fmt.Errorf("%w: content is required", ErrInvalid)
	}

	var existing Version
	err = s.db.Collection(versionsCollection).FindOne(ctx, bson.D{
		{Key: "artifact_id", Value: artifactID},
		{Key: "content_hash", Value: hash},
	}).Decode(&existing)
	if err == nil {
		updated, err := s.setCurrent(ctx, artifactID, existing.Seq, size)
		return &existing, updated, err
	}
	if !errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil, fmt.Errorf("look up version: %w", err)
	}

	now := time.Now().UTC()
	update := bson.D{
		{Key: "$inc", Value: bson.D{{Key: "current_version", Value: 1}}},
		{Key: "$set", Value: bson.D{
			{Key: "updated_at", Value: now},
			{Key: "size_bytes", Value: size},
		}},
	}
	opts := options.FindOneAndUpdate().SetReturnDocument(options.After)
	var updated Artifact
	if err := s.db.Collection(artifactsCollection).FindOneAndUpdate(ctx, bson.D{{Key: "_id", Value: artifactID}}, update, opts).Decode(&updated); err != nil {
		return nil, nil, fmt.Errorf("advance version: %w", err)
	}

	v := &Version{
		ID:          newID(),
		ArtifactID:  artifactID,
		Seq:         updated.CurrentVersion,
		ContentHash: hash,
		SizeBytes:   size,
		Note:        note,
		CreatedBy:   userID,
		CreatedAt:   now,
	}
	if _, err := s.db.Collection(versionsCollection).InsertOne(ctx, v); err != nil {
		if isDuplicateKey(err) {
			// A concurrent publish won the unique (artifact, hash) index; the
			// version it wrote is the one this call wanted, so report it and
			// leave the pointer where the winner put it.
			var winner Version
			if findErr := s.db.Collection(versionsCollection).FindOne(ctx, bson.D{
				{Key: "artifact_id", Value: artifactID},
				{Key: "content_hash", Value: hash},
			}).Decode(&winner); findErr == nil {
				return &winner, &updated, nil
			}
		}
		return nil, nil, fmt.Errorf("insert version: %w", err)
	}
	return v, &updated, nil
}

func (s *Store) setCurrent(ctx context.Context, artifactID string, seq int, size int64) (*Artifact, error) {
	update := bson.D{{Key: "$set", Value: bson.D{
		{Key: "current_version", Value: seq},
		{Key: "size_bytes", Value: size},
		{Key: "updated_at", Value: time.Now().UTC()},
	}}}
	opts := options.FindOneAndUpdate().SetReturnDocument(options.After)
	var a Artifact
	if err := s.db.Collection(artifactsCollection).FindOneAndUpdate(ctx, bson.D{{Key: "_id", Value: artifactID}}, update, opts).Decode(&a); err != nil {
		return nil, fmt.Errorf("set current version: %w", err)
	}
	return &a, nil
}

// Get returns a live artifact.
func (s *Store) Get(ctx context.Context, artifactID string) (*Artifact, error) {
	var a Artifact
	err := s.db.Collection(artifactsCollection).FindOne(ctx, bson.D{
		{Key: "_id", Value: artifactID},
		{Key: "deleted_at", Value: nil},
	}).Decode(&a)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get artifact: %w", err)
	}
	return &a, nil
}

// Versions lists every version, newest first.
func (s *Store) Versions(ctx context.Context, artifactID string) ([]Version, error) {
	cur, err := s.db.Collection(versionsCollection).Find(ctx,
		bson.D{{Key: "artifact_id", Value: artifactID}},
		options.Find().SetSort(bson.D{{Key: "seq", Value: -1}}))
	if err != nil {
		return nil, fmt.Errorf("list versions: %w", err)
	}
	defer func() { _ = cur.Close(ctx) }()

	var out []Version
	if err := cur.All(ctx, &out); err != nil {
		return nil, fmt.Errorf("decode versions: %w", err)
	}
	return out, nil
}

// VersionAt loads one version by seq.
func (s *Store) VersionAt(ctx context.Context, artifactID string, seq int) (*Version, error) {
	var v Version
	err := s.db.Collection(versionsCollection).FindOne(ctx, bson.D{
		{Key: "artifact_id", Value: artifactID},
		{Key: "seq", Value: seq},
	}).Decode(&v)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get version: %w", err)
	}
	return &v, nil
}

// Rollback points the artifact at an older version without deleting anything.
func (s *Store) Rollback(ctx context.Context, artifactID, userID string, seq int) (*Artifact, error) {
	a, err := s.Get(ctx, artifactID)
	if err != nil {
		return nil, err
	}
	if a.OwnerUserID != userID {
		return nil, ErrForbidden
	}
	v, err := s.VersionAt(ctx, artifactID, seq)
	if err != nil {
		return nil, err
	}
	return s.setCurrent(ctx, artifactID, v.Seq, v.SizeBytes)
}

// Grant grants viewer access. Only the owner may do it.
func (s *Store) Grant(ctx context.Context, artifactID, ownerID, targetID string) error {
	a, err := s.Get(ctx, artifactID)
	if err != nil {
		return err
	}
	if a.OwnerUserID != ownerID {
		return ErrForbidden
	}
	if strings.TrimSpace(targetID) == "" || targetID == ownerID {
		return fmt.Errorf("%w: target user is required", ErrInvalid)
	}
	_, err = s.db.Collection(grantsCollection).InsertOne(ctx, &Grant{
		ID:         newID(),
		ArtifactID: artifactID,
		UserID:     targetID,
		Role:       "viewer",
		CreatedAt:  time.Now().UTC(),
	})
	if err != nil && !isDuplicateKey(err) {
		return fmt.Errorf("grant: %w", err)
	}
	return nil
}

// Revoke removes a grant.
func (s *Store) Revoke(ctx context.Context, artifactID, ownerID, targetID string) error {
	a, err := s.Get(ctx, artifactID)
	if err != nil {
		return err
	}
	if a.OwnerUserID != ownerID {
		return ErrForbidden
	}
	_, err = s.db.Collection(grantsCollection).DeleteOne(ctx, bson.D{
		{Key: "artifact_id", Value: artifactID},
		{Key: "user_id", Value: targetID},
	})
	return err
}

// Grants lists the viewers of one artifact.
func (s *Store) Grants(ctx context.Context, artifactID string) ([]Grant, error) {
	cur, err := s.db.Collection(grantsCollection).Find(ctx,
		bson.D{{Key: "artifact_id", Value: artifactID}},
		options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}}))
	if err != nil {
		return nil, fmt.Errorf("list grants: %w", err)
	}
	defer func() { _ = cur.Close(ctx) }()

	var out []Grant
	if err := cur.All(ctx, &out); err != nil {
		return nil, fmt.Errorf("decode grants: %w", err)
	}
	return out, nil
}

// CanView reports whether the user may read the artifact.
//
// A grant row is the only way in besides ownership; there is no "public"
// visibility in this service, which is what keeps sharing inside the platform.
func (s *Store) CanView(ctx context.Context, a *Artifact, userID string) (bool, error) {
	if a.OwnerUserID == userID {
		return true, nil
	}
	count, err := s.db.Collection(grantsCollection).CountDocuments(ctx, bson.D{
		{Key: "artifact_id", Value: a.ID},
		{Key: "user_id", Value: userID},
	})
	if err != nil {
		return false, fmt.Errorf("check grant: %w", err)
	}
	return count > 0, nil
}

// DuplicateArtifact creates a second work that points at the same body. The blob
// store is content-addressed, so the bytes are already there: only the artifact
// row and its single version row are new. The quota counts distinct content hashes
// (see QuotaUsed), so a copy does not charge the new owner twice.
//
// Only the current version travels. A copy is a new document that starts at v1;
// carrying the history would make every copy as heavy as its source.
func (s *Store) DuplicateArtifact(ctx context.Context, artifactID, newOwner, title string) (*Artifact, error) {
	src, err := s.Get(ctx, artifactID)
	if err != nil {
		return nil, err
	}
	current, err := s.VersionAt(ctx, artifactID, src.CurrentVersion)
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(title)
	if trimmed == "" {
		trimmed = src.Title
	}

	now := time.Now().UTC()
	a := &Artifact{
		ID:             newID(),
		OwnerUserID:    newOwner,
		Title:          trimmed,
		Kind:           src.Kind,
		CurrentVersion: 1,
		Visibility:     "private",
		SizeBytes:      current.SizeBytes,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if _, err := s.db.Collection(artifactsCollection).InsertOne(ctx, a); err != nil {
		return nil, fmt.Errorf("insert artifact: %w", err)
	}
	v := &Version{
		ID:          newID(),
		ArtifactID:  a.ID,
		Seq:         1,
		ContentHash: current.ContentHash,
		SizeBytes:   current.SizeBytes,
		Note:        fmt.Sprintf("copy of %s#%d", src.ID, current.Seq),
		CreatedBy:   newOwner,
		CreatedAt:   now,
	}
	if _, err := s.db.Collection(versionsCollection).InsertOne(ctx, v); err != nil {
		_, _ = s.db.Collection(artifactsCollection).DeleteOne(ctx, bson.D{{Key: "_id", Value: a.ID}})
		return nil, fmt.Errorf("insert version: %w", err)
	}
	return a, nil
}

// RenameArtifact changes the title shown on the work itself. The tree keeps its
// own title (nodes.title); one request writes both, so a rename never leaves the
// document header disagreeing with the tree.
func (s *Store) RenameArtifact(ctx context.Context, artifactID, title string) error {
	trimmed := strings.TrimSpace(title)
	if trimmed == "" {
		return fmt.Errorf("%w: title is required", ErrInvalid)
	}
	result, err := s.db.Collection(artifactsCollection).UpdateOne(ctx,
		bson.D{{Key: "_id", Value: artifactID}, {Key: "deleted_at", Value: nil}},
		bson.D{{Key: "$set", Value: bson.D{
			{Key: "title", Value: trimmed},
			{Key: "updated_at", Value: time.Now().UTC()},
		}}})
	if err != nil {
		return fmt.Errorf("rename artifact: %w", err)
	}
	if result.MatchedCount == 0 {
		return ErrNotFound
	}
	return nil
}

// SoftDelete hides the artifact from every list and lookup. The rows stay so an
// operator can restore a mistaken delete; the UI does not offer one.
func (s *Store) SoftDelete(ctx context.Context, artifactID, userID string) error {
	a, err := s.Get(ctx, artifactID)
	if err != nil {
		return err
	}
	if a.OwnerUserID != userID {
		return ErrForbidden
	}
	now := time.Now().UTC()
	_, err = s.db.Collection(artifactsCollection).UpdateOne(ctx,
		bson.D{{Key: "_id", Value: artifactID}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "deleted_at", Value: now}}}})
	return err
}

// ListScope selects which half of the library a list request wants.
type ListScope string

const (
	ScopeOwned  ListScope = "owned"
	ScopeShared ListScope = "shared"
)

// List returns the artifacts visible in one scope, newest first.
func (s *Store) List(ctx context.Context, userID string, scope ListScope, query, kind string) ([]Artifact, error) {
	filter := bson.D{{Key: "deleted_at", Value: nil}}
	switch scope {
	case ScopeShared:
		grants, err := s.grantedIDs(ctx, userID)
		if err != nil {
			return nil, err
		}
		if len(grants) == 0 {
			return []Artifact{}, nil
		}
		filter = append(filter, bson.E{Key: "_id", Value: bson.D{{Key: "$in", Value: grants}}})
	default:
		filter = append(filter, bson.E{Key: "owner_user_id", Value: userID})
	}
	if kind != "" {
		filter = append(filter, bson.E{Key: "kind", Value: kind})
	}
	if q := strings.TrimSpace(query); q != "" {
		filter = append(filter, bson.E{Key: "title", Value: bson.D{{Key: "$regex", Value: regexpQuote(q)}, {Key: "$options", Value: "i"}}})
	}

	cur, err := s.db.Collection(artifactsCollection).Find(ctx, filter,
		options.Find().SetSort(bson.D{{Key: "updated_at", Value: -1}}).SetLimit(200))
	if err != nil {
		return nil, fmt.Errorf("list artifacts: %w", err)
	}
	defer func() { _ = cur.Close(ctx) }()

	out := []Artifact{}
	if err := cur.All(ctx, &out); err != nil {
		return nil, fmt.Errorf("decode artifacts: %w", err)
	}
	return out, nil
}

func (s *Store) grantedIDs(ctx context.Context, userID string) ([]string, error) {
	cur, err := s.db.Collection(grantsCollection).Find(ctx, bson.D{{Key: "user_id", Value: userID}})
	if err != nil {
		return nil, fmt.Errorf("list grants: %w", err)
	}
	defer func() { _ = cur.Close(ctx) }()

	var ids []string
	for cur.Next(ctx) {
		var g Grant
		if err := cur.Decode(&g); err != nil {
			return nil, fmt.Errorf("decode grant: %w", err)
		}
		ids = append(ids, g.ArtifactID)
	}
	return ids, cur.Err()
}

// QuotaUsed sums the distinct content the user owns. Two versions with the same
// hash — or the same file published under two titles — count once, because the
// blob is stored once.
func (s *Store) QuotaUsed(ctx context.Context, userID string) (int64, error) {
	cur, err := s.db.Collection(artifactsCollection).Find(ctx, bson.D{
		{Key: "owner_user_id", Value: userID},
		{Key: "deleted_at", Value: nil},
	})
	if err != nil {
		return 0, fmt.Errorf("list owned: %w", err)
	}
	var ids []string
	for cur.Next(ctx) {
		var a Artifact
		if err := cur.Decode(&a); err != nil {
			_ = cur.Close(ctx)
			return 0, fmt.Errorf("decode artifact: %w", err)
		}
		ids = append(ids, a.ID)
	}
	_ = cur.Close(ctx)
	if err := cur.Err(); err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}

	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: bson.D{{Key: "artifact_id", Value: bson.D{{Key: "$in", Value: ids}}}}}},
		{{Key: "$group", Value: bson.D{
			{Key: "_id", Value: "$content_hash"},
			{Key: "size", Value: bson.D{{Key: "$max", Value: "$size_bytes"}}},
		}}},
		{{Key: "$group", Value: bson.D{
			{Key: "_id", Value: nil},
			{Key: "total", Value: bson.D{{Key: "$sum", Value: "$size"}}},
		}}},
	}
	agg, err := s.db.Collection(versionsCollection).Aggregate(ctx, pipeline)
	if err != nil {
		return 0, fmt.Errorf("sum usage: %w", err)
	}
	defer func() { _ = agg.Close(ctx) }()

	var out struct {
		Total int64 `bson:"total"`
	}
	if agg.Next(ctx) {
		if err := agg.Decode(&out); err != nil {
			return 0, fmt.Errorf("decode usage: %w", err)
		}
	}
	return out.Total, agg.Err()
}

// HasHash reports whether the user already owns a version with this content.
// The quota check uses it so republishing the same bytes never charges twice.
func (s *Store) HasHash(ctx context.Context, userID, hash string) (bool, error) {
	cur, err := s.db.Collection(artifactsCollection).Find(ctx, bson.D{
		{Key: "owner_user_id", Value: userID},
		{Key: "deleted_at", Value: nil},
	}, options.Find().SetProjection(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		return false, fmt.Errorf("list owned: %w", err)
	}
	var ids []string
	for cur.Next(ctx) {
		var row struct {
			ID string `bson:"_id"`
		}
		if err := cur.Decode(&row); err != nil {
			_ = cur.Close(ctx)
			return false, fmt.Errorf("decode artifact: %w", err)
		}
		ids = append(ids, row.ID)
	}
	_ = cur.Close(ctx)
	if err := cur.Err(); err != nil {
		return false, err
	}
	if len(ids) == 0 {
		return false, nil
	}
	count, err := s.db.Collection(versionsCollection).CountDocuments(ctx, bson.D{
		{Key: "artifact_id", Value: bson.D{{Key: "$in", Value: ids}}},
		{Key: "content_hash", Value: hash},
	})
	if err != nil {
		return false, fmt.Errorf("check hash: %w", err)
	}
	return count > 0, nil
}

// ReferencedHashes lists every content hash a version points at, including
// versions of soft-deleted artifacts: a deleted work can be restored by an
// operator, so its body must not be collected underneath it.
func (s *Store) ReferencedHashes(ctx context.Context) (map[string]bool, error) {
	var values []string
	if err := s.db.Collection(versionsCollection).Distinct(ctx, "content_hash", bson.D{}).Decode(&values); err != nil {
		return nil, fmt.Errorf("distinct hashes: %w", err)
	}
	out := make(map[string]bool, len(values))
	for _, h := range values {
		out[h] = true
	}
	return out, nil
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// regexpQuote escapes a user-supplied search string so it cannot act as a
// pattern: a title search is a literal substring match, not a regex surface.
func regexpQuote(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\\', '.', '+', '*', '?', '(', ')', '|', '[', ']', '{', '}', '^', '$':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
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
	var bwe mongo.BulkWriteException
	if errors.As(err, &bwe) {
		for _, e := range bwe.WriteErrors {
			if e.Code == 11000 {
				return true
			}
		}
	}
	return false
}
