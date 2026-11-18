package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"gitee.com/kobeyoung81/nanoartifacts/internal/blobstore"
	"gitee.com/kobeyoung81/nanoartifacts/internal/store"
)

// maxBody caps a publish request. The plan allows 16 MiB bodies; JSON adds a
// little framing, so the limit sits slightly above it and the real check is the
// per-user quota and the kind/HTML validation.
const maxBody = 20 << 20

type versionJSON struct {
	Seq           int    `json:"seq"`
	ContentHash   string `json:"content_hash"`
	SizeBytes     int64  `json:"size_bytes"`
	Note          string `json:"note,omitempty"`
	CreatedBy     string `json:"created_by"`
	CreatedByName string `json:"created_by_name,omitempty"`
	CreatedAt     string `json:"created_at"`
}

// displayName falls back to the id when no resolver is wired (tests, migrate).
func (s *Server) displayName(ctx context.Context, userID string) string {
	if s.Identity == nil {
		return userID
	}
	return s.Identity.DisplayName(ctx, userID)
}

func (s *Server) artifactJSON(r *http.Request, a *store.Artifact) map[string]any {
	return map[string]any{
		"id":              a.ID,
		"owner_user_id":   a.OwnerUserID,
		"owner_name":      s.displayName(r.Context(), a.OwnerUserID),
		"title":           a.Title,
		"kind":            a.Kind,
		"current_version": a.CurrentVersion,
		"visibility":      a.Visibility,
		"size_bytes":      a.SizeBytes,
		"created_at":      nowRFC3339(a.CreatedAt),
		"updated_at":      nowRFC3339(a.UpdatedAt),
	}
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	userID := userOf(r)
	scope := store.ScopeOwned
	if r.URL.Query().Get("scope") == "shared" {
		scope = store.ScopeShared
	}

	// Visibility now comes from space membership, so the list is "what I can
	// read" rather than "what I own"; scope is kept for older callers.
	items, err := s.Store.ListVisibleArtifacts(r.Context(), userID)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	out := make([]map[string]any, 0, len(items))
	for i := range items {
		out = append(out, s.artifactJSON(r, &items[i]))
	}

	used, err := s.Store.QuotaUsed(r.Context(), userID)
	if err != nil {
		slog.Warn("quota lookup failed", "error", err)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"artifacts": out,
		"scope":     string(scope),
		"quota":     map[string]any{"used": used, "limit": s.Quota},
	})
}

type publishRequest struct {
	ArtifactID string `json:"artifact_id"`
	SpaceID    string `json:"space_id"`
	NodeID     string `json:"node_id"`
	ParentID   string `json:"parent_id"`
	Title      string `json:"title"`
	Kind       string `json:"kind"`
	HTML       string `json:"html"`
	Note       string `json:"note"`
}

func (s *Server) handlePublish(w http.ResponseWriter, r *http.Request) {
	userID := userOf(r)
	var req publishRequest
	if err := decodeLimited(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.HTML) == "" {
		writeError(w, http.StatusBadRequest, "html is required")
		return
	}

	a, v, err := s.publish(r.Context(), userID, req)
	if err != nil {
		var over *quotaExceeded
		if errors.As(err, &over) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{
				"error": "quota exceeded", "used": over.used, "limit": over.limit, "needed": over.needed,
			})
			return
		}
		writeStoreError(w, err)
		return
	}
	status := http.StatusCreated
	if req.ArtifactID != "" {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{"artifact": s.artifactJSON(r, a), "version": versionOf(v)})
}

func (s *Server) handleDetail(w http.ResponseWriter, r *http.Request) {
	userID := userOf(r)
	a, err := s.Store.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	ok, err := s.Store.CanViewArtifact(r.Context(), a, userID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !ok {
		// A caller without access learns nothing about existence.
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	versions, err := s.Store.Versions(r.Context(), a.ID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	outVersions := make([]versionJSON, 0, len(versions))
	for i := range versions {
		v := versionOf(&versions[i])
		v.CreatedByName = s.displayName(r.Context(), versions[i].CreatedBy)
		outVersions = append(outVersions, v)
	}

	grants, err := s.Store.Grants(r.Context(), a.ID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	outGrants := make([]map[string]string, 0, len(grants))
	for _, g := range grants {
		outGrants = append(outGrants, map[string]string{
			"user_id": g.UserID, "name": s.displayName(r.Context(), g.UserID), "role": g.Role,
		})
	}

	// Where the work sits, so an old ?work= link can highlight its place in the
	// tree without a second lookup.
	spaceID, nodeID := "", ""
	if node, err := s.Store.NodeForArtifact(r.Context(), a.ID); err == nil {
		spaceID, nodeID = node.SpaceID, node.ID
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"artifact":   s.artifactJSON(r, a),
		"versions":   outVersions,
		"grants":     outGrants,
		"space_id":   spaceID,
		"node_id":    nodeID,
		"can_manage": a.OwnerUserID == userID,
	})
}

// handleRaw streams one version. It is the only unauthenticated-by-header route,
// so everything it returns is pinned down: the signature binds user, artifact,
// version and expiry, and the response headers keep the document in an opaque
// origin even when someone pastes the URL into the address bar.
// handleRaw streams one version. It is the one route an iframe can reach, so
// it accepts the session cookie (or a bearer token) and re-checks the grant on
// every load: a revoked share stops working immediately, not when a URL expires.
//
// The response headers keep the document in an opaque origin even when someone
// pastes the URL into the address bar.
func (s *Server) handleRaw(w http.ResponseWriter, r *http.Request) {
	artifactID := r.PathValue("id")
	seq, err := seqOf(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid seq")
		return
	}

	artifact, err := s.Store.Get(r.Context(), artifactID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	ok, err := s.Store.CanViewArtifact(r.Context(), artifact, userOf(r))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	v, err := s.Store.VersionAt(r.Context(), artifactID, seq)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	reader, _, err := s.Blobs.Open(r.Context(), v.ContentHash)
	if err != nil {
		if errors.Is(err, blobstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "content unavailable")
			return
		}
		slog.Error("open blob", "error", err)
		writeError(w, http.StatusInternalServerError, "content unavailable")
		return
	}
	defer func() { _ = reader.Close() }()

	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", "sandbox allow-scripts")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Robots-Tag", "noindex")
	h.Set("Cache-Control", "private, max-age=60")
	h.Set("Referrer-Policy", "no-referrer")
	if r.URL.Query().Get("download") == "1" {
		// filename* keeps non-ASCII titles intact; the plain form would need
		// escaping the browser does not agree on.
		h.Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(filenameFor(artifact.Title)))
	}
	// ServeContent handles Range through the reader's Seek.
	http.ServeContent(w, r, v.ContentHash+".html", v.CreatedAt, reader)
}

// handleSessionInfo tells a top-level tab who it is. It is the first call the
// SPA makes when it has no token to present (a new tab, a pasted link).
func (s *Server) handleSessionInfo(w http.ResponseWriter, r *http.Request) {
	userID := userOf(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"user_id":      userID,
		"display_name": s.displayName(r.Context(), userID),
	})
}

// handleSession exchanges a JWT for the cookie the preview frame carries.
//
// The cookie is scoped to this service's API path and HttpOnly: the page's own
// JavaScript (and the artifact, which runs in an opaque origin) cannot read it.
// It is only ever used to read content — there is no write guarded by it — so a
// leak would expose what the user could already open.
func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	token, expires := s.Signer.Session(userOf(r))
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     s.CookiePath,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		// Secure only when the browser reached us over TLS; on a plain-http dev
		// stack a Secure cookie would be dropped silently.
		Secure:  r.Header.Get("X-Forwarded-Proto") == "https",
		Expires: expires,
		MaxAge:  int(time.Until(expires).Seconds()),
	})
	writeJSON(w, http.StatusOK, map[string]any{"expires_at": nowRFC3339(expires)})
}

func (s *Server) handleRollback(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Seq int `json:"seq"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Seq <= 0 {
		writeError(w, http.StatusBadRequest, "seq is required")
		return
	}
	a, err := s.Store.Rollback(r.Context(), r.PathValue("id"), userOf(r), body.Seq)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"artifact": s.artifactJSON(r, a)})
}

func (s *Server) handleGrant(w http.ResponseWriter, r *http.Request) {
	var body struct {
		UserID string `json:"user_id"`
		Email  string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	target := strings.TrimSpace(body.UserID)
	if target == "" && strings.TrimSpace(body.Email) != "" {
		user, err := s.Identity.FindByEmail(r.Context(), body.Email)
		if err != nil {
			writeStoreError(w, fmt.Errorf("%w: %v", store.ErrInvalid, err))
			return
		}
		if user == nil {
			writeError(w, http.StatusNotFound, "no account with that email")
			return
		}
		target = user.ID
	}
	if err := s.Store.Grant(r.Context(), r.PathValue("id"), userOf(r), target); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRevoke(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.Revoke(r.Context(), r.PathValue("id"), userOf(r), r.PathValue("userID")); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	if _, err := s.Store.DeleteWork(r.Context(), r.PathValue("id"), userOf(r)); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleFindUser(w http.ResponseWriter, r *http.Request) {
	// The member picker passes q and gets a short list; everything else (MCP,
	// older callers) passes an exact address and gets one account.
	if q := r.URL.Query().Get("q"); q != "" {
		users, err := s.Identity.SearchUsers(r.Context(), q)
		if err != nil {
			writeStoreError(w, fmt.Errorf("%w: %v", store.ErrInvalid, err))
			return
		}
		out := make([]map[string]any, 0, len(users))
		for _, user := range users {
			out = append(out, map[string]any{
				"user_id":      user.ID,
				"display_name": user.DisplayName,
				"email":        user.Email,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"users": out})
		return
	}
	email := r.URL.Query().Get("email")
	user, err := s.Identity.FindByEmail(r.Context(), email)
	if err != nil {
		writeStoreError(w, fmt.Errorf("%w: %v", store.ErrInvalid, err))
		return
	}
	if user == nil {
		writeError(w, http.StatusNotFound, "no account with that email")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	userID := userOf(r)
	writeJSON(w, http.StatusOK, map[string]any{"user_id": userID, "display_name": s.displayName(r.Context(), userID)})
}

// filenameFor turns a title into something safe for a Content-Disposition
// header: no quotes, no path separators, no control characters.
func filenameFor(title string) string {
	cleaned := strings.Map(func(r rune) rune {
		switch r {
		case '"', '\\', '/', '\n', '\r', '\t':
			return '-'
		}
		if r < 0x20 {
			return -1
		}
		return r
	}, strings.TrimSpace(title))
	if cleaned == "" {
		cleaned = "artifact"
	}
	if len(cleaned) > 80 {
		cleaned = cleaned[:80]
	}
	return cleaned + ".html"
}
