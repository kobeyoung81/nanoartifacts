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

// BackfillStats reports what a space backfill did (or would do, in a dry run).
type BackfillStats struct {
	ArtifactsScanned int
	SpacesCreated    int
	FoldersCreated   int
	NodesCreated     int
	LegacyGrantsKept int
}

// BackfillSpaces gives every existing artifact a home: the owner's personal
// space, under an "未分类" folder, with one node per artifact.
//
// Idempotent by construction: a node is created only when the artifact has
// none, and the personal space / folder are looked up before being made. A
// second run therefore reports zero work.
//
// Per-artifact grants are deliberately NOT converted into space membership: a
// personal space is not shareable, and turning ten per-document shares into
// access to someone's whole personal space would widen access rather than
// preserve it. They stay as read-only exceptions (see CanViewArtifact).
func (s *Store) BackfillSpaces(ctx context.Context, dryRun bool) (BackfillStats, error) {
	var stats BackfillStats

	cur, err := s.db.Collection(artifactsCollection).Find(ctx,
		bson.D{{Key: "deleted_at", Value: nil}},
		options.Find().SetProjection(bson.D{
			{Key: "_id", Value: 1}, {Key: "owner_user_id", Value: 1},
			{Key: "title", Value: 1}, {Key: "kind", Value: 1},
		}).SetSort(bson.D{{Key: "created_at", Value: 1}}))
	if err != nil {
		return stats, fmt.Errorf("list artifacts: %w", err)
	}
	defer func() { _ = cur.Close(ctx) }()

	type artifactRow struct {
		ID          string `bson:"_id"`
		OwnerUserID string `bson:"owner_user_id"`
		Title       string `bson:"title"`
		Kind        string `bson:"kind"`
	}
	var rows []artifactRow
	if err := cur.All(ctx, &rows); err != nil {
		return stats, fmt.Errorf("decode artifacts: %w", err)
	}
	stats.ArtifactsScanned = len(rows)

	spaceFor := map[string]string{}  // owner -> personal space id
	folderFor := map[string]string{} // owner -> 未分类 folder id

	for _, row := range rows {
		spaceID, ok := spaceFor[row.OwnerUserID]
		if !ok {
			var (
				created bool
				err     error
			)
			spaceID, created, err = s.ensurePersonalSpace(ctx, row.OwnerUserID, dryRun)
			if err != nil {
				return stats, err
			}
			if created {
				stats.SpacesCreated++
			}
			spaceFor[row.OwnerUserID] = spaceID
		}
		if spaceID == "" {
			// Dry run: the space does not exist yet, so nothing below it can be
			// counted precisely; report one would-be node and move on.
			stats.NodesCreated++
			continue
		}

		folderID, ok := folderFor[row.OwnerUserID]
		if !ok {
			var created bool
			folderID, created, err = s.ensureUncategorised(ctx, spaceID, row.OwnerUserID, dryRun)
			if err != nil {
				return stats, err
			}
			if created {
				stats.FoldersCreated++
			}
			folderFor[row.OwnerUserID] = folderID
		}

		existing, err := s.NodeForArtifact(ctx, row.ID)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return stats, err
		}
		if existing != nil {
			continue
		}
		stats.NodesCreated++
		if dryRun {
			continue
		}
		if _, err := s.CreateNode(ctx, spaceID, folderID, nodeKindForArtifact(row.Kind), row.Title, row.OwnerUserID, row.ID); err != nil {
			return stats, fmt.Errorf("place artifact %s: %w", row.ID, err)
		}
	}

	legacy, err := s.db.Collection(grantsCollection).CountDocuments(ctx, bson.D{})
	if err != nil {
		return stats, fmt.Errorf("count legacy grants: %w", err)
	}
	stats.LegacyGrantsKept = int(legacy)
	return stats, nil
}

// ensurePersonalSpace returns the owner's personal space id, creating it unless
// this is a dry run. An empty id in a dry run means "would be created".
func (s *Store) ensurePersonalSpace(ctx context.Context, ownerID string, dryRun bool) (string, bool, error) {
	var space Space
	err := s.spaces().FindOne(ctx, bson.D{
		{Key: "owner_user_id", Value: ownerID},
		{Key: "kind", Value: "personal"},
		{Key: "deleted_at", Value: nil},
	}).Decode(&space)
	if err == nil {
		return space.ID, false, nil
	}
	if !errors.Is(err, mongo.ErrNoDocuments) {
		return "", false, fmt.Errorf("find personal space: %w", err)
	}
	if dryRun {
		return "", true, nil
	}
	created, err := s.PersonalSpace(ctx, ownerID)
	if err != nil {
		return "", false, err
	}
	return created.ID, true, nil
}

// ensureUncategorised returns the space's "未分类" folder, creating it at the
// root when missing.
func (s *Store) ensureUncategorised(ctx context.Context, spaceID, actor string, dryRun bool) (string, bool, error) {
	var folder Node
	err := s.db.Collection(nodesCollection).FindOne(ctx, bson.D{
		{Key: "space_id", Value: spaceID},
		{Key: "parent_id", Value: ""},
		{Key: "kind", Value: "folder"},
		{Key: "title", Value: "未分类"},
		{Key: "deleted_at", Value: nil},
	}).Decode(&folder)
	if err == nil {
		return folder.ID, false, nil
	}
	if !errors.Is(err, mongo.ErrNoDocuments) {
		return "", false, fmt.Errorf("find folder: %w", err)
	}
	if dryRun {
		return "", true, nil
	}
	node, err := s.CreateNode(ctx, spaceID, "", "folder", "未分类", actor, "")
	if err != nil {
		return "", false, err
	}
	return node.ID, true, nil
}

// CanViewArtifact is the read rule for a work: its space grants access, or the
// caller still holds a legacy per-artifact grant from before spaces existed.
func (s *Store) CanViewArtifact(ctx context.Context, a *Artifact, userID string) (bool, error) {
	if a.OwnerUserID == userID {
		return true, nil
	}
	node, err := s.NodeForArtifact(ctx, a.ID)
	if err == nil {
		role, err := s.Role(ctx, node.SpaceID, userID)
		if err != nil {
			return false, err
		}
		if role != "" {
			return true, nil
		}
	} else if !errors.Is(err, ErrNotFound) {
		return false, err
	}
	count, err := s.db.Collection(grantsCollection).CountDocuments(ctx, bson.D{
		{Key: "artifact_id", Value: a.ID},
		{Key: "user_id", Value: userID},
	})
	if err != nil {
		return false, fmt.Errorf("check legacy grant: %w", err)
	}
	return count > 0, nil
}

// TouchNode updates a node's timestamp; used when a new version is published.
func (s *Store) TouchNode(ctx context.Context, nodeID string) {
	if nodeID == "" {
		return
	}
	_, _ = s.db.Collection(nodesCollection).UpdateOne(ctx,
		bson.D{{Key: "_id", Value: nodeID}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "updated_at", Value: time.Now().UTC()}}}})
}
