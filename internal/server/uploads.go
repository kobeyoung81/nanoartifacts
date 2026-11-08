package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"gitee.com/kobeyoung81/nanoartifacts/internal/blobstore"
	"gitee.com/kobeyoung81/nanoartifacts/internal/store"
)

// maxUploadBody caps a hand-off. It matches the 16 MiB publish ceiling the tool
// description states, plus a little room so a file that is exactly at the limit
// is not refused over its trailing newline.
const maxUploadBody = 17 << 20

// uploadURIPrefix is what the runtime substitutes for a workspace path before
// it calls a tool. The server accepts nothing else in html_file: a raw path would
// mean the client never uploaded, and the server cannot read the agent's disk.
const uploadURIPrefix = "nanoartifacts://upload/"

// agentOf resolves the calling agent and the user it belongs to.
//
// Both routes below act on behalf of one agent, so unlike the JSON-RPC entry
// point (which the bus probes with initialize/tools/list before any agent
// exists) they refuse a request that names none.
func (s *Server) agentOf(w http.ResponseWriter, r *http.Request) (string, bool) {
	agentID := strings.TrimSpace(r.Header.Get("X-Nanowork-Agent-ID"))
	if agentID == "" {
		writeError(w, http.StatusBadRequest, "X-Nanowork-Agent-ID is required")
		return "", false
	}
	if s.Identity == nil {
		writeError(w, http.StatusInternalServerError, "identity is not configured")
		return "", false
	}
	owner, err := s.Identity.OwnerOfAgent(r.Context(), agentID)
	if err != nil {
		slog.Warn("mcp identity lookup failed", "agent", agentID, "error", err)
		writeError(w, http.StatusForbidden, "unknown agent")
		return "", false
	}
	return owner, true
}

// handleBlobUpload accepts a body the runtime read from the agent's workspace.
//
// This is the half of a publish that must never travel through a model's
// context: the bytes go straight from the agent container to the blob store,
// and what comes back is a short-lived handle the model passes to
// artifact_publish instead of the document.
func (s *Server) handleBlobUpload(w http.ResponseWriter, r *http.Request) {
	owner, ok := s.agentOf(w, r)
	if !ok {
		return
	}

	body := http.MaxBytesReader(w, r.Body, maxUploadBody)
	tmp, err := os.CreateTemp("", "nanoartifacts-upload-")
	if err != nil {
		slog.Error("create upload spool", "error", err)
		writeError(w, http.StatusInternalServerError, "cannot buffer the upload")
		return
	}
	name := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(name)
	}()

	// Hash while copying: the body may be 16 MiB, and reading it twice (once to
	// hash, once to store) would either double the memory or the disk traffic.
	sum := sha256.New()
	size, err := io.Copy(io.MultiWriter(tmp, sum), body)
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge,
				fmt.Sprintf("body must be at most %d bytes", maxUploadBody))
			return
		}
		writeError(w, http.StatusBadRequest, "cannot read the upload")
		return
	}
	if size == 0 {
		writeError(w, http.StatusBadRequest, "the uploaded file is empty")
		return
	}
	hash := hex.EncodeToString(sum.Sum(nil))

	// Quota counts each distinct body once, so content the owner already holds
	// (published or waiting) must not be refused — a retried publish would
	// otherwise fail against a quota the first attempt already paid for.
	duplicate, err := s.Store.HasHash(r.Context(), owner, hash)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !duplicate {
		pending, err := s.Store.HasPendingUpload(r.Context(), owner, hash)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		duplicate = pending
	}
	if !duplicate && s.Quota > 0 {
		used, err := s.Store.QuotaUsed(r.Context(), owner)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		if used+size > s.Quota {
			over := &quotaExceeded{used: used, limit: s.Quota, needed: size}
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{
				"error": "quota exceeded", "used": over.used, "limit": over.limit, "needed": over.needed,
			})
			return
		}
	}

	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		slog.Error("rewind upload spool", "error", err)
		writeError(w, http.StatusInternalServerError, "cannot buffer the upload")
		return
	}
	if _, err := s.Blobs.Put(r.Context(), hash, tmp); err != nil {
		slog.Error("store uploaded content", "error", err)
		writeError(w, http.StatusInternalServerError, "cannot store the content")
		return
	}

	now := time.Now().UTC()
	up := &store.Upload{
		ID:          newUploadID(),
		OwnerUserID: owner,
		ContentHash: hash,
		SizeBytes:   size,
		CreatedAt:   now,
		ExpiresAt:   now.Add(store.UploadTTL),
	}
	if err := s.Store.CreateUpload(r.Context(), up); err != nil {
		slog.Error("record upload", "error", err)
		writeError(w, http.StatusInternalServerError, "cannot record the upload")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"upload_id":    up.ID,
		"uri":          uploadURIPrefix + up.ID,
		"content_hash": hash,
		"size_bytes":   size,
		"expires_at":   nowRFC3339(up.ExpiresAt),
	})
}

// handleAgentRaw streams one version to an agent.
//
// The user-facing raw route authenticates a JWT or the preview cookie; an agent
// has neither, only the internal token and its own id, so downloads that should
// land in a workspace come through here instead.
func (s *Server) handleAgentRaw(w http.ResponseWriter, r *http.Request) {
	owner, ok := s.agentOf(w, r)
	if !ok {
		return
	}
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
	visible, err := s.Store.CanViewArtifact(r.Context(), artifact, owner)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !visible {
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
	h.Set("X-Content-SHA256", v.ContentHash)
	// Prompt-cacheable would be wrong here: this response is an agent reading
	// bytes it is about to write to disk, not a shared asset.
	h.Set("Cache-Control", "private, no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	// ServeContent handles Range through the reader's Seek, so a 16 MiB body
	// resumes instead of restarting.
	http.ServeContent(w, r, v.ContentHash+".html", v.CreatedAt, reader)
}

// newUploadID mints a handle. It is random, not derived from the content: the
// blob store deduplicates globally, so a hash would let any caller name bytes it
// never uploaded.
func newUploadID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand does not fail in practice; a fixed prefix would collide,
		// so this is the one place where panicking is the honest outcome.
		panic(fmt.Sprintf("mint upload id: %v", err))
	}
	return hex.EncodeToString(b[:])
}
