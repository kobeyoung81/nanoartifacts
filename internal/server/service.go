package server

import (
	"context"
	"fmt"
	"strings"

	"gitee.com/kobeyoung81/nanoartifacts/internal/store"
)

// quotaExceeded carries the numbers the UI shows ("used X of Y"), which a plain
// 413 would throw away.
type quotaExceeded struct {
	used, limit, needed int64
}

func (e *quotaExceeded) Error() string {
	return fmt.Sprintf("quota exceeded: used %d of %d, needs %d more", e.used, e.limit, e.needed)
}

// destination is where a publish lands, resolved before any bytes are touched.
type destination struct {
	spaceID    string
	nodeID     string
	parentID   string
	artifactID string
}

// publish is the write path for an inline body, shared by REST and MCP.
//
// Order matters: the blob is written before the version row, so a crash between
// the two leaves an unreferenced body that gc collects, never a version whose
// content cannot be read.
func (s *Server) publish(ctx context.Context, owner string, req publishRequest) (*store.Artifact, *store.Version, error) {
	if strings.TrimSpace(req.HTML) == "" {
		return nil, nil, fmt.Errorf("%w: html is required", store.ErrInvalid)
	}
	hash := hashOf(req.HTML)
	size := int64(len(req.HTML))

	dest, err := s.resolveDestination(ctx, owner, req)
	if err != nil {
		return nil, nil, err
	}

	duplicate, err := s.Store.HasHash(ctx, owner, hash)
	if err != nil {
		return nil, nil, err
	}
	used, err := s.Store.QuotaUsed(ctx, owner)
	if err != nil {
		return nil, nil, err
	}
	if !duplicate && s.Quota > 0 && used+size > s.Quota {
		return nil, nil, &quotaExceeded{used: used, limit: s.Quota, needed: size}
	}

	if _, err := s.Blobs.Put(ctx, hash, strings.NewReader(req.HTML)); err != nil {
		return nil, nil, fmt.Errorf("store content: %w", err)
	}

	return s.finishPublish(ctx, owner, dest, req, hash, size)
}

// publishUpload is the same write path for a body that arrived through the
// upload hand-off.
//
// The bytes are already in the blob store and already counted against the
// owner's quota by the upload endpoint (which charges a body once, after
// dedupe), so this adds only the metadata — and does not touch the blob, which
// must not be re-read or re-written just to publish it.
func (s *Server) publishUpload(ctx context.Context, owner string, req publishRequest, up *store.Upload) (*store.Artifact, *store.Version, error) {
	dest, err := s.resolveDestination(ctx, owner, req)
	if err != nil {
		return nil, nil, err
	}
	return s.finishPublish(ctx, owner, dest, req, up.ContentHash, up.SizeBytes)
}

// resolveDestination validates where a publish lands: publishing into a space
// the caller cannot edit must fail without leaving a blob behind.
func (s *Server) resolveDestination(ctx context.Context, owner string, req publishRequest) (destination, error) {
	dest := destination{
		spaceID:    req.SpaceID,
		nodeID:     req.NodeID,
		parentID:   req.ParentID,
		artifactID: req.ArtifactID,
	}
	if dest.nodeID != "" {
		node, err := s.Store.NodeByID(ctx, dest.nodeID)
		if err != nil {
			return dest, err
		}
		if node.Kind == "folder" {
			return dest, fmt.Errorf("%w: a folder cannot hold content", store.ErrInvalid)
		}
		role, err := s.Store.Role(ctx, node.SpaceID, owner)
		if err != nil {
			return dest, err
		}
		if role == "" {
			return dest, store.ErrNotFound
		}
		if role != store.RoleOwner && role != store.RoleEditor {
			return dest, store.ErrForbidden
		}
		dest.spaceID = node.SpaceID
		if dest.artifactID == "" {
			dest.artifactID = node.ArtifactID
		}
		return dest, nil
	}

	if dest.spaceID == "" {
		personal, err := s.Store.PersonalSpace(ctx, owner)
		if err != nil {
			return dest, err
		}
		dest.spaceID = personal.ID
	}
	role, err := s.Store.Role(ctx, dest.spaceID, owner)
	if err != nil {
		return dest, err
	}
	if role == "" {
		return dest, store.ErrNotFound
	}
	if role != store.RoleOwner && role != store.RoleEditor {
		return dest, store.ErrForbidden
	}
	return dest, nil
}

// finishPublish writes the version and the tree row for bytes that are already
// in the blob store.
func (s *Server) finishPublish(ctx context.Context, owner string, dest destination, req publishRequest, hash string, size int64) (*store.Artifact, *store.Version, error) {
	if dest.artifactID != "" {
		version, artifact, err := s.Store.Publish(ctx, dest.artifactID, owner, hash, req.Note, size)
		if err == nil {
			s.Store.TouchNode(ctx, dest.nodeID)
		}
		return artifact, version, err
	}

	artifact, version, err := s.Store.Create(ctx, owner, req.Title, req.Kind, hash, req.Note, size)
	if err != nil {
		return nil, nil, err
	}

	if dest.nodeID != "" {
		if err := s.Store.AttachArtifact(ctx, dest.nodeID, artifact.ID); err != nil {
			return artifact, version, err
		}
		return artifact, version, nil
	}

	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = artifact.Title
	}
	if _, err := s.Store.CreateNode(ctx, dest.spaceID, dest.parentID, nodeKindFor(artifact.Kind), title, owner, artifact.ID); err != nil {
		return artifact, version, err
	}
	return artifact, version, nil
}

// nodeKindFor maps an artifact kind to the tree node that holds it.
func nodeKindFor(kind string) string {
	if kind == "ui" || kind == "pro" {
		return "app"
	}
	return "document"
}
