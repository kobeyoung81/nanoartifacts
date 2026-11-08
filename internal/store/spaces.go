package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	spacesCollection      = "artifacts_spaces"
	spaceGrantsCollection = "artifacts_space_grants"
	nodesCollection       = "artifacts_nodes"
)

// Roles a space member can hold. The owner is not a grant row: it is the
// space's owner_user_id, so transferring a space is a single field update.
const (
	RoleOwner  = "owner"
	RoleEditor = "editor"
	RoleViewer = "viewer"
)

// Space is the permission boundary: a personal workspace or a team library.
type Space struct {
	ID          string     `bson:"_id" json:"id"`
	Name        string     `bson:"name" json:"name"`
	Kind        string     `bson:"kind" json:"kind"`
	OwnerUserID string     `bson:"owner_user_id" json:"owner_user_id"`
	Visibility  string     `bson:"visibility" json:"visibility"`
	CreatedAt   time.Time  `bson:"created_at" json:"created_at"`
	UpdatedAt   time.Time  `bson:"updated_at" json:"updated_at"`
	DeletedAt   *time.Time `bson:"deleted_at,omitempty" json:"-"`
}

// SpaceMembership is a space plus the caller's role in it.
type SpaceMembership struct {
	Space
	Role string `bson:"-" json:"role"`
}

// SpaceGrant is one member's role row.
type SpaceGrant struct {
	ID        string    `bson:"_id" json:"id"`
	SpaceID   string    `bson:"space_id" json:"space_id"`
	UserID    string    `bson:"user_id" json:"user_id"`
	Role      string    `bson:"role" json:"role"`
	CreatedAt time.Time `bson:"created_at" json:"created_at"`
}

func (s *Store) spaces() *mongo.Collection      { return s.db.Collection(spacesCollection) }
func (s *Store) spaceGrants() *mongo.Collection { return s.db.Collection(spaceGrantsCollection) }

// PersonalSpace returns the user's personal space, creating it on first use.
//
// It is created lazily rather than at sign-up: accounts exist before they ever
// open the library, and a space created for every account would be dead weight.
func (s *Store) PersonalSpace(ctx context.Context, userID string) (*Space, error) {
	if strings.TrimSpace(userID) == "" {
		return nil, fmt.Errorf("%w: user is required", ErrInvalid)
	}
	var space Space
	err := s.spaces().FindOne(ctx, bson.D{
		{Key: "owner_user_id", Value: userID},
		{Key: "kind", Value: "personal"},
		{Key: "deleted_at", Value: nil},
	}).Decode(&space)
	if err == nil {
		return &space, nil
	}
	if !errors.Is(err, mongo.ErrNoDocuments) {
		return nil, fmt.Errorf("get personal space: %w", err)
	}

	now := time.Now().UTC()
	space = Space{
		ID:          newID(),
		Name:        "我的空间",
		Kind:        "personal",
		OwnerUserID: userID,
		Visibility:  "private",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if _, err := s.spaces().InsertOne(ctx, space); err != nil {
		if isDuplicateKey(err) {
			// A concurrent request created it; read theirs.
			return s.PersonalSpace(ctx, userID)
		}
		return nil, fmt.Errorf("create personal space: %w", err)
	}
	return &space, nil
}

// CreateSpace creates a team space. Personal spaces are only made by
// PersonalSpace.
func (s *Store) CreateSpace(ctx context.Context, ownerID, name, visibility string) (*Space, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("%w: name is required", ErrInvalid)
	}
	if visibility != "private" && visibility != "members" {
		visibility = "private"
	}
	now := time.Now().UTC()
	space := Space{
		ID:          newID(),
		Name:        name,
		Kind:        "team",
		OwnerUserID: ownerID,
		Visibility:  visibility,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if _, err := s.spaces().InsertOne(ctx, space); err != nil {
		return nil, fmt.Errorf("create space: %w", err)
	}
	return &space, nil
}

// GetSpace loads a live space.
func (s *Store) GetSpace(ctx context.Context, id string) (*Space, error) {
	var space Space
	err := s.spaces().FindOne(ctx, bson.D{{Key: "_id", Value: id}, {Key: "deleted_at", Value: nil}}).Decode(&space)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get space: %w", err)
	}
	return &space, nil
}

// ListSpaces returns the personal space plus every team space the user belongs
// to, each with the caller's role.
func (s *Store) ListSpaces(ctx context.Context, userID string) ([]SpaceMembership, error) {
	if _, err := s.PersonalSpace(ctx, userID); err != nil {
		return nil, err
	}

	cur, err := s.spaceGrants().Find(ctx, bson.D{{Key: "user_id", Value: userID}})
	if err != nil {
		return nil, fmt.Errorf("list memberships: %w", err)
	}
	roles := map[string]string{}
	var ids []string
	for cur.Next(ctx) {
		var g SpaceGrant
		if err := cur.Decode(&g); err != nil {
			_ = cur.Close(ctx)
			return nil, fmt.Errorf("decode membership: %w", err)
		}
		roles[g.SpaceID] = g.Role
		ids = append(ids, g.SpaceID)
	}
	_ = cur.Close(ctx)
	if err := cur.Err(); err != nil {
		return nil, err
	}

	filter := bson.D{{Key: "deleted_at", Value: nil}}
	if len(ids) > 0 {
		filter = append(filter, bson.E{Key: "$or", Value: bson.A{
			bson.D{{Key: "owner_user_id", Value: userID}},
			bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: ids}}}},
		}})
	} else {
		filter = append(filter, bson.E{Key: "owner_user_id", Value: userID})
	}

	spaces, err := s.spaces().Find(ctx, filter, options.Find().SetSort(bson.D{
		{Key: "kind", Value: 1}, {Key: "name", Value: 1},
	}))
	if err != nil {
		return nil, fmt.Errorf("list spaces: %w", err)
	}
	defer func() { _ = spaces.Close(ctx) }()

	out := []SpaceMembership{}
	for spaces.Next(ctx) {
		var space Space
		if err := spaces.Decode(&space); err != nil {
			return nil, fmt.Errorf("decode space: %w", err)
		}
		role := roles[space.ID]
		if space.OwnerUserID == userID {
			role = RoleOwner
		}
		if role == "" {
			continue
		}
		out = append(out, SpaceMembership{Space: space, Role: role})
	}
	return out, spaces.Err()
}

// ListVisibleArtifacts returns every work the caller can read: their own, the
// ones in spaces they belong to, and any legacy per-artifact grant.
func (s *Store) ListVisibleArtifacts(ctx context.Context, userID string) ([]Artifact, error) {
	seen := map[string]bool{}
	var out []Artifact
	add := func(items []Artifact) {
		for _, a := range items {
			if seen[a.ID] {
				continue
			}
			seen[a.ID] = true
			out = append(out, a)
		}
	}

	owned, err := s.List(ctx, userID, ScopeOwned, "", "")
	if err != nil {
		return nil, err
	}
	add(owned)

	spaces, err := s.ListSpaces(ctx, userID)
	if err != nil {
		return nil, err
	}
	if len(spaces) > 0 {
		spaceIDs := make([]string, 0, len(spaces))
		for _, m := range spaces {
			spaceIDs = append(spaceIDs, m.ID)
		}
		nodeCur, err := s.db.Collection(nodesCollection).Find(ctx, bson.D{
			{Key: "space_id", Value: bson.D{{Key: "$in", Value: spaceIDs}}},
			{Key: "artifact_id", Value: bson.D{{Key: "$ne", Value: ""}}},
			{Key: "deleted_at", Value: nil},
		})
		if err != nil {
			return nil, fmt.Errorf("list space nodes: %w", err)
		}
		var artifactIDs []string
		for nodeCur.Next(ctx) {
			var n Node
			if err := nodeCur.Decode(&n); err != nil {
				_ = nodeCur.Close(ctx)
				return nil, fmt.Errorf("decode node: %w", err)
			}
			artifactIDs = append(artifactIDs, n.ArtifactID)
		}
		_ = nodeCur.Close(ctx)
		if err := nodeCur.Err(); err != nil {
			return nil, err
		}
		if len(artifactIDs) > 0 {
			cur, err := s.db.Collection(artifactsCollection).Find(ctx, bson.D{
				{Key: "_id", Value: bson.D{{Key: "$in", Value: artifactIDs}}},
				{Key: "deleted_at", Value: nil},
			}, options.Find().SetSort(bson.D{{Key: "updated_at", Value: -1}}))
			if err != nil {
				return nil, fmt.Errorf("list space artifacts: %w", err)
			}
			var items []Artifact
			if err := cur.All(ctx, &items); err != nil {
				return nil, fmt.Errorf("decode artifacts: %w", err)
			}
			add(items)
		}
	}

	grantCur, err := s.db.Collection(grantsCollection).Find(ctx, bson.D{{Key: "user_id", Value: userID}})
	if err != nil {
		return nil, fmt.Errorf("list legacy grants: %w", err)
	}
	var grantedIDs []string
	for grantCur.Next(ctx) {
		var g Grant
		if err := grantCur.Decode(&g); err != nil {
			_ = grantCur.Close(ctx)
			return nil, fmt.Errorf("decode grant: %w", err)
		}
		grantedIDs = append(grantedIDs, g.ArtifactID)
	}
	_ = grantCur.Close(ctx)
	if err := grantCur.Err(); err != nil {
		return nil, err
	}
	if len(grantedIDs) > 0 {
		cur, err := s.db.Collection(artifactsCollection).Find(ctx, bson.D{
			{Key: "_id", Value: bson.D{{Key: "$in", Value: grantedIDs}}},
			{Key: "deleted_at", Value: nil},
		})
		if err != nil {
			return nil, fmt.Errorf("list granted artifacts: %w", err)
		}
		var items []Artifact
		if err := cur.All(ctx, &items); err != nil {
			return nil, fmt.Errorf("decode granted: %w", err)
		}
		add(items)
	}

	return out, nil
}

// Role reports the caller's role, or "" when they have none.
func (s *Store) Role(ctx context.Context, spaceID, userID string) (string, error) {
	var space Space
	err := s.spaces().FindOne(ctx, bson.D{{Key: "_id", Value: spaceID}, {Key: "deleted_at", Value: nil}}).Decode(&space)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get space: %w", err)
	}
	if space.OwnerUserID == userID {
		return RoleOwner, nil
	}
	var grant SpaceGrant
	err = s.spaceGrants().FindOne(ctx, bson.D{
		{Key: "space_id", Value: spaceID},
		{Key: "user_id", Value: userID},
	}).Decode(&grant)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get role: %w", err)
	}
	return grant.Role, nil
}

// canWrite reports whether the role may change the tree or publish into it.
func canWrite(role string) bool {
	return role == RoleOwner || role == RoleEditor
}

// UpdateSpace renames a space or changes its visibility. Owner only.
func (s *Store) UpdateSpace(ctx context.Context, spaceID, userID string, name, visibility *string) (*Space, error) {
	space, err := s.GetSpace(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	if space.OwnerUserID != userID {
		return nil, ErrForbidden
	}
	set := bson.D{{Key: "updated_at", Value: time.Now().UTC()}}
	if name != nil {
		trimmed := strings.TrimSpace(*name)
		if trimmed == "" {
			return nil, fmt.Errorf("%w: name is required", ErrInvalid)
		}
		set = append(set, bson.E{Key: "name", Value: trimmed})
	}
	if visibility != nil {
		if *visibility != "private" && *visibility != "members" {
			return nil, fmt.Errorf("%w: unknown visibility", ErrInvalid)
		}
		if space.Kind == "personal" {
			return nil, fmt.Errorf("%w: a personal space is always private", ErrInvalid)
		}
		set = append(set, bson.E{Key: "visibility", Value: *visibility})
	}
	opts := options.FindOneAndUpdate().SetReturnDocument(options.After)
	var updated Space
	if err := s.spaces().FindOneAndUpdate(ctx,
		bson.D{{Key: "_id", Value: spaceID}}, bson.D{{Key: "$set", Value: set}}, opts).Decode(&updated); err != nil {
		return nil, fmt.Errorf("update space: %w", err)
	}
	return &updated, nil
}

// DeleteSpace soft-deletes a team space and its nodes. Personal spaces cannot
// be deleted: they are the place every artifact falls back to.
func (s *Store) DeleteSpace(ctx context.Context, spaceID, userID string) error {
	space, err := s.GetSpace(ctx, spaceID)
	if err != nil {
		return err
	}
	if space.OwnerUserID != userID {
		return ErrForbidden
	}
	if space.Kind != "team" {
		return fmt.Errorf("%w: a personal space cannot be deleted", ErrInvalid)
	}
	now := time.Now().UTC()
	if _, err := s.spaces().UpdateOne(ctx,
		bson.D{{Key: "_id", Value: spaceID}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "deleted_at", Value: now}}}}); err != nil {
		return fmt.Errorf("delete space: %w", err)
	}
	_, err = s.db.Collection(nodesCollection).UpdateMany(ctx,
		bson.D{{Key: "space_id", Value: spaceID}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "deleted_at", Value: now}}}})
	return err
}

// ListGrants lists members (owner excluded).
func (s *Store) ListGrants(ctx context.Context, spaceID string) ([]SpaceGrant, error) {
	cur, err := s.spaceGrants().Find(ctx,
		bson.D{{Key: "space_id", Value: spaceID}},
		options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}}))
	if err != nil {
		return nil, fmt.Errorf("list grants: %w", err)
	}
	defer func() { _ = cur.Close(ctx) }()
	var out []SpaceGrant
	if err := cur.All(ctx, &out); err != nil {
		return nil, fmt.Errorf("decode grants: %w", err)
	}
	return out, nil
}

// AddGrant adds or re-roles a member. Owner only.
func (s *Store) AddGrant(ctx context.Context, spaceID, actorID, targetID, role string) error {
	space, err := s.GetSpace(ctx, spaceID)
	if err != nil {
		return err
	}
	if space.OwnerUserID != actorID {
		return ErrForbidden
	}
	if role != RoleEditor && role != RoleViewer {
		return fmt.Errorf("%w: role must be editor or viewer", ErrInvalid)
	}
	if strings.TrimSpace(targetID) == "" || targetID == actorID {
		return fmt.Errorf("%w: target user is required", ErrInvalid)
	}
	_, err = s.spaceGrants().UpdateOne(ctx,
		bson.D{{Key: "space_id", Value: spaceID}, {Key: "user_id", Value: targetID}},
		bson.D{
			{Key: "$set", Value: bson.D{{Key: "role", Value: role}}},
			{Key: "$setOnInsert", Value: bson.D{
				{Key: "_id", Value: newID()},
				{Key: "space_id", Value: spaceID},
				{Key: "user_id", Value: targetID},
				{Key: "created_at", Value: time.Now().UTC()},
			}},
		},
		options.UpdateOne().SetUpsert(true))
	if err != nil {
		return fmt.Errorf("add grant: %w", err)
	}
	return nil
}

// RemoveGrant removes a member. Owner only; the owner is not a grant row.
func (s *Store) RemoveGrant(ctx context.Context, spaceID, actorID, targetID string) error {
	space, err := s.GetSpace(ctx, spaceID)
	if err != nil {
		return err
	}
	if space.OwnerUserID != actorID {
		return ErrForbidden
	}
	_, err = s.spaceGrants().DeleteOne(ctx, bson.D{
		{Key: "space_id", Value: spaceID},
		{Key: "user_id", Value: targetID},
	})
	return err
}
