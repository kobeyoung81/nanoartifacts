package server

import (
	"context"
	"encoding/json"
	"net/http"

	"gitee.com/kobeyoung81/nanoartifacts/internal/store"
)

// requireRole resolves the caller's role in a space, writing the error response
// when they have none. Non-members get 404, never 403: the existence of a space
// is itself information.
func (s *Server) requireRole(w http.ResponseWriter, r *http.Request, spaceID string) (string, bool) {
	role, err := s.Store.Role(r.Context(), spaceID, userOf(r))
	if err != nil {
		writeStoreError(w, err)
		return "", false
	}
	if role == "" {
		writeError(w, http.StatusNotFound, "not found")
		return "", false
	}
	return role, true
}

func (s *Server) handleListSpaces(w http.ResponseWriter, r *http.Request) {
	spaces, err := s.Store.ListSpaces(r.Context(), userOf(r))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(spaces))
	for _, m := range spaces {
		out = append(out, map[string]any{
			"id":         m.ID,
			"name":       m.Name,
			"kind":       m.Kind,
			"visibility": m.Visibility,
			"role":       m.Role,
			"created_at": nowRFC3339(m.CreatedAt),
			"updated_at": nowRFC3339(m.UpdatedAt),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"spaces": out})
}

func (s *Server) handleCreateSpace(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name       string `json:"name"`
		Visibility string `json:"visibility"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	space, err := s.Store.CreateSpace(r.Context(), userOf(r), body.Name, body.Visibility)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"space": spaceJSON(space, store.RoleOwner)})
}

func spaceJSON(space *store.Space, role string) map[string]any {
	return map[string]any{
		"id":            space.ID,
		"name":          space.Name,
		"kind":          space.Kind,
		"visibility":    space.Visibility,
		"owner_user_id": space.OwnerUserID,
		"role":          role,
		"created_at":    nowRFC3339(space.CreatedAt),
		"updated_at":    nowRFC3339(space.UpdatedAt),
	}
}

func (s *Server) handleGetSpace(w http.ResponseWriter, r *http.Request) {
	spaceID := r.PathValue("id")
	role, ok := s.requireRole(w, r, spaceID)
	if !ok {
		return
	}
	space, err := s.Store.GetSpace(r.Context(), spaceID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	grants, err := s.Store.ListGrants(r.Context(), spaceID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	members := make([]map[string]string, 0, len(grants)+1)
	members = append(members, map[string]string{
		"user_id": space.OwnerUserID,
		"name":    s.displayName(r.Context(), space.OwnerUserID),
		"role":    store.RoleOwner,
	})
	for _, g := range grants {
		members = append(members, map[string]string{
			"user_id": g.UserID,
			"name":    s.displayName(r.Context(), g.UserID),
			"role":    g.Role,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"space": spaceJSON(space, role), "members": members,
	})
}

func (s *Server) handleUpdateSpace(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name       *string `json:"name"`
		Visibility *string `json:"visibility"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	space, err := s.Store.UpdateSpace(r.Context(), r.PathValue("id"), userOf(r), body.Name, body.Visibility)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"space": spaceJSON(space, store.RoleOwner)})
}

func (s *Server) handleDeleteSpace(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.DeleteSpace(r.Context(), r.PathValue("id"), userOf(r)); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSpaceTree(w http.ResponseWriter, r *http.Request) {
	spaceID := r.PathValue("id")
	role, ok := s.requireRole(w, r, spaceID)
	if !ok {
		return
	}
	space, err := s.Store.GetSpace(r.Context(), spaceID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	nodes, err := s.Store.ListNodes(r.Context(), spaceID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	out := nodes
	if out == nil {
		out = []store.Node{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"space": spaceJSON(space, role),
		"nodes": out,
	})
}

// handleMoveNode is the tree's drag-and-drop: "put this under that, before this
// one". It is separate from PATCH because a patch can only append.
func (s *Server) handleMoveNode(w http.ResponseWriter, r *http.Request) {
	node, _, err := s.nodeForWrite(r.Context(), r.PathValue("id"), userOf(r))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	var body struct {
		SpaceID  string `json:"space_id"`
		ParentID string `json:"parent_id"`
		BeforeID string `json:"before_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	// Moving into another space needs write access there as well as here.
	if body.SpaceID != "" && body.SpaceID != node.SpaceID {
		role, ok := s.requireRole(w, r, body.SpaceID)
		if !ok {
			return
		}
		if role != store.RoleOwner && role != store.RoleEditor {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
	}
	moved, err := s.Store.MoveNode(r.Context(), node.ID, body.SpaceID, body.ParentID, body.BeforeID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"node": moved})
}

// handleCopyNode duplicates a node (and its subtree) into a space the caller can
// write to. The source needs write access too: copying is a read, but reading a
// tree you cannot edit and pasting it elsewhere is still a share.
func (s *Server) handleCopyNode(w http.ResponseWriter, r *http.Request) {
	node, _, err := s.nodeForWrite(r.Context(), r.PathValue("id"), userOf(r))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	var body struct {
		SpaceID  string `json:"space_id"`
		ParentID string `json:"parent_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	target := body.SpaceID
	if target == "" {
		target = node.SpaceID
	}
	if target != node.SpaceID {
		role, ok := s.requireRole(w, r, target)
		if !ok {
			return
		}
		if role != store.RoleOwner && role != store.RoleEditor {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
	}
	created, err := s.Store.CopySubtree(r.Context(), node.ID, target, body.ParentID, userOf(r))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"node": created})
}

func (s *Server) handleListGrants(w http.ResponseWriter, r *http.Request) {
	spaceID := r.PathValue("id")
	if _, ok := s.requireRole(w, r, spaceID); !ok {
		return
	}
	grants, err := s.Store.ListGrants(r.Context(), spaceID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	out := make([]map[string]string, 0, len(grants))
	for _, g := range grants {
		out = append(out, map[string]string{
			"user_id": g.UserID, "name": s.displayName(r.Context(), g.UserID), "role": g.Role,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": out})
}

func (s *Server) handleAddGrant(w http.ResponseWriter, r *http.Request) {
	var body struct {
		UserID string `json:"user_id"`
		Email  string `json:"email"`
		Role   string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	target := body.UserID
	if target == "" && body.Email != "" {
		user, err := s.Identity.FindByEmail(r.Context(), body.Email)
		if err != nil {
			writeError(w, http.StatusBadRequest, "could not look up that address")
			return
		}
		if user == nil {
			writeError(w, http.StatusNotFound, "no account with that email")
			return
		}
		target = user.ID
	}
	if err := s.Store.AddGrant(r.Context(), r.PathValue("id"), userOf(r), target, body.Role); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRemoveGrant(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.RemoveGrant(r.Context(), r.PathValue("id"), userOf(r), r.PathValue("userID")); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// nodeForWrite resolves a node and checks the caller may change the tree.
func (s *Server) nodeForWrite(ctx context.Context, nodeID, userID string) (*store.Node, bool, error) {
	node, err := s.Store.NodeByID(ctx, nodeID)
	if err != nil {
		return nil, false, err
	}
	role, err := s.Store.Role(ctx, node.SpaceID, userID)
	if err != nil {
		return nil, false, err
	}
	if role == "" {
		return nil, false, store.ErrNotFound
	}
	if role != store.RoleOwner && role != store.RoleEditor {
		return nil, false, store.ErrForbidden
	}
	return node, true, nil
}

func (s *Server) handleCreateNode(w http.ResponseWriter, r *http.Request) {
	spaceID := r.PathValue("id")
	role, ok := s.requireRole(w, r, spaceID)
	if !ok {
		return
	}
	if role != store.RoleOwner && role != store.RoleEditor {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	var body struct {
		ParentID string `json:"parent_id"`
		Kind     string `json:"kind"`
		Title    string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	node, err := s.Store.CreateNode(r.Context(), spaceID, body.ParentID, body.Kind, body.Title, userOf(r), "")
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"node": node})
}

func (s *Server) handleUpdateNode(w http.ResponseWriter, r *http.Request) {
	node, _, err := s.nodeForWrite(r.Context(), r.PathValue("id"), userOf(r))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	var body struct {
		Title    *string `json:"title"`
		ParentID *string `json:"parent_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	updated, err := s.Store.UpdateNode(r.Context(), node.ID, body.Title, body.ParentID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	// A document's title lives in two places: the tree row and the artifact the
	// body belongs to. Renaming from the tree has to move both or the header
	// keeps the old name.
	if body.Title != nil && node.ArtifactID != "" && updated.Title != node.Title {
		if _, err := s.Store.RenameWork(r.Context(), node.ArtifactID, updated.Title); err != nil {
			writeStoreError(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"node": updated})
}

func (s *Server) handleDeleteNode(w http.ResponseWriter, r *http.Request) {
	node, _, err := s.nodeForWrite(r.Context(), r.PathValue("id"), userOf(r))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if _, err := s.Store.DeleteNode(r.Context(), node.ID, userOf(r)); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
