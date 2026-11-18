// Package server exposes nanoartifacts over HTTP: the REST surface the library
// UI uses and the MCP endpoint agents publish through.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"gitee.com/kobeyoung81/nanoartifacts/internal/auth"
	"gitee.com/kobeyoung81/nanoartifacts/internal/blobstore"
	"gitee.com/kobeyoung81/nanoartifacts/internal/identity"
	"gitee.com/kobeyoung81/nanoartifacts/internal/metrics"
	"gitee.com/kobeyoung81/nanoartifacts/internal/sign"
	"gitee.com/kobeyoung81/nanoartifacts/internal/store"
)

// Server holds the service's collaborators.
type Server struct {
	Store    *store.Store
	Blobs    blobstore.Store
	Signer   *sign.Signer
	Users    *auth.Validator
	Identity *identity.Resolver
	Quota    int64
	BusToken string
	Version  string
	// WebDir is the built SPA. Empty disables static serving, which is what the
	// tests and the migrate/gc commands want.
	WebDir string
	// SessionTTL is how long a preview cookie stays valid.
	SessionTTL time.Duration
	// CookiePath is the external path the cookie is scoped to. It must match
	// what the browser sees (/nanoartifacts/api), not what this process sees
	// (/api), because nginx strips the prefix.
	CookiePath string
}

// Routes returns the mux.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": s.Version})
	})

	// Operational numbers, anonymous: a scraper is infrastructure, and this
	// route sits outside the per-route auth below.
	mux.Handle("GET /metrics", metrics.Handler())

	// Spaces and their trees. The space is the permission boundary: every
	// route below resolves a role first and answers 404 to non-members.
	mux.Handle("GET /api/v1/spaces", s.auth(http.HandlerFunc(s.handleListSpaces)))
	mux.Handle("POST /api/v1/spaces", s.auth(http.HandlerFunc(s.handleCreateSpace)))
	mux.Handle("GET /api/v1/spaces/{id}", s.auth(http.HandlerFunc(s.handleGetSpace)))
	mux.Handle("PATCH /api/v1/spaces/{id}", s.auth(http.HandlerFunc(s.handleUpdateSpace)))
	mux.Handle("DELETE /api/v1/spaces/{id}", s.auth(http.HandlerFunc(s.handleDeleteSpace)))
	mux.Handle("GET /api/v1/spaces/{id}/tree", s.auth(http.HandlerFunc(s.handleSpaceTree)))
	mux.Handle("POST /api/v1/spaces/{id}/nodes", s.auth(http.HandlerFunc(s.handleCreateNode)))
	mux.Handle("GET /api/v1/spaces/{id}/grants", s.auth(http.HandlerFunc(s.handleListGrants)))
	mux.Handle("POST /api/v1/spaces/{id}/grants", s.auth(http.HandlerFunc(s.handleAddGrant)))
	mux.Handle("DELETE /api/v1/spaces/{id}/grants/{userID}", s.auth(http.HandlerFunc(s.handleRemoveGrant)))
	mux.Handle("PATCH /api/v1/nodes/{id}", s.auth(http.HandlerFunc(s.handleUpdateNode)))
	mux.Handle("POST /api/v1/nodes/{id}/move", s.auth(http.HandlerFunc(s.handleMoveNode)))
	mux.Handle("POST /api/v1/nodes/{id}/copy", s.auth(http.HandlerFunc(s.handleCopyNode)))
	mux.Handle("DELETE /api/v1/nodes/{id}", s.auth(http.HandlerFunc(s.handleDeleteNode)))

	// User surface. Every handler behind auth runs as the JWT subject or the
	// session cookie's subject.
	mux.Handle("GET /api/v1/artifacts", s.auth(http.HandlerFunc(s.handleList)))
	mux.Handle("POST /api/v1/artifacts", s.auth(http.HandlerFunc(s.handlePublish)))
	mux.Handle("GET /api/v1/artifacts/{id}", s.auth(http.HandlerFunc(s.handleDetail)))
	// Who am I: what a top-level tab calls when it has no token to present.
	mux.Handle("GET /api/v1/session", s.auth(http.HandlerFunc(s.handleSessionInfo)))
	// Minting a session stays bearer-only: a cookie that could renew itself
	// would never end.
	mux.Handle("POST /api/v1/session", s.bearerAuth(http.HandlerFunc(s.handleSession)))
	mux.Handle("POST /api/v1/artifacts/{id}/rollback", s.auth(http.HandlerFunc(s.handleRollback)))
	mux.Handle("POST /api/v1/artifacts/{id}/grants", s.auth(http.HandlerFunc(s.handleGrant)))
	mux.Handle("DELETE /api/v1/artifacts/{id}/grants/{userID}", s.auth(http.HandlerFunc(s.handleRevoke)))
	mux.Handle("DELETE /api/v1/artifacts/{id}", s.auth(http.HandlerFunc(s.handleDelete)))
	mux.Handle("GET /api/v1/users", s.auth(http.HandlerFunc(s.handleFindUser)))
	mux.Handle("GET /api/v1/me", s.auth(http.HandlerFunc(s.handleMe)))

	// Content: the preview frame sends the cookie, curl may send the token.
	mux.Handle("GET /api/v1/artifacts/{id}/versions/{seq}/raw", s.auth(http.HandlerFunc(s.handleRaw)))

	// Agent surface.
	mux.Handle("POST /mcp", s.agentAuth(http.HandlerFunc(s.handleMCP)))
	// The byte path beside it: a body the runtime read from the agent's
	// workspace, and the stream an agent downloads one from. Both act on behalf
	// of an agent, so both require the agent header (see agentOf).
	mux.Handle("POST /mcp/blobs", s.agentAuth(http.HandlerFunc(s.handleBlobUpload)))
	mux.Handle("GET /mcp/artifacts/{id}/versions/{seq}/raw", s.agentAuth(http.HandlerFunc(s.handleAgentRaw)))

	// The SPA last: its pattern is the least specific.
	if s.WebDir != "" {
		mux.Handle("/", s.staticHandler())
	}

	return metrics.Middleware(mux)
}

type ctxKey string

const userKey ctxKey = "user"

// auth accepts either credential the platform can present: the user's JWT, or
// the session cookie a top-level tab carries.
//
// The bearer token is tried first so a caller that has one never depends on a
// cookie. Cookie-authenticated writes additionally require the request to come
// from this origin: SameSite=Lax already keeps the cookie off cross-site posts,
// and this is the second gate behind it.
func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID, viaCookie := s.identity(r)
		if userID == "" {
			slog.Warn("rejecting user request", "path", r.URL.Path, "error", "no valid credential")
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if viaCookie && isWrite(r.Method) && !sameOrigin(r) {
			slog.Warn("refusing cookie-authenticated write from another origin", "path", r.URL.Path)
			writeError(w, http.StatusForbidden, "cross-origin request refused")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey, userID)))
	})
}

// bearerAuth is for the routes that must not be reachable with a cookie.
func (s *Server) bearerAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Users == nil {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		userID, err := s.Users.UserID(bearer(r))
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey, userID)))
	})
}

// identity resolves the caller, reporting whether the cookie was the source.
func (s *Server) identity(r *http.Request) (string, bool) {
	if s.Users != nil {
		if subject, err := s.Users.UserID(bearer(r)); err == nil && subject != "" {
			return subject, false
		}
	}
	if cookie, err := r.Cookie(sessionCookie); err == nil && cookie.Value != "" {
		if subject, err := s.Signer.VerifySession(cookie.Value); err == nil {
			return subject, true
		}
	}
	return "", false
}

func isWrite(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
}

// sameOrigin accepts a browser request from this origin, and a non-browser
// caller that sent no Origin at all (curl, tests). A browser cannot omit
// Origin on a cross-site write, which is what makes the absence safe here.
func sameOrigin(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" {
		return site == "same-origin"
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

// sessionCookie is the name of the session credential. The prefix is the
// service's, not the platform's: it must be impossible to confuse with a portal
// cookie.
const sessionCookie = "nw_artifacts"

// agentAuth accepts the shared internal token.
//
// The agent header is deliberately not required here: the bus probes a server
// (initialize + tools/list) before an administrator can enable it, and a probe
// has no agent behind it. The header is required where it means something — on
// tools/call, which is the only method that acts as an agent.
func (s *Server) agentAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.BusToken == "" || bearer(r) != s.BusToken {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

func userOf(r *http.Request) string {
	if v, ok := r.Context().Value(userKey).(string); ok {
		return v
	}
	return ""
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if body != nil {
		_ = json.NewEncoder(w).Encode(body)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// writeStoreError maps the store's sentinels onto status codes. A missing
// artifact and someone else's artifact both become 404: the difference is
// information the caller has not earned.
func writeStoreError(w http.ResponseWriter, err error) {
	switch {
	case err == nil:
		return
	case isNotFound(err):
		writeError(w, http.StatusNotFound, "not found")
	case isForbidden(err):
		writeError(w, http.StatusForbidden, "forbidden")
	case isInvalid(err):
		writeError(w, http.StatusBadRequest, "invalid request")
	case isNotEmpty(err):
		// 409 rather than 400: the request is well formed, the folder is not empty.
		writeError(w, http.StatusConflict, "folder is not empty")
	default:
		slog.Error("request failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

func isNotFound(err error) bool  { return errors.Is(err, store.ErrNotFound) }
func isForbidden(err error) bool { return errors.Is(err, store.ErrForbidden) }
func isInvalid(err error) bool   { return errors.Is(err, store.ErrInvalid) }
func isNotEmpty(err error) bool  { return errors.Is(err, store.ErrNotEmpty) }

// seqOf parses a version number from the path.
func seqOf(r *http.Request) (int, error) {
	return strconv.Atoi(r.PathValue("seq"))
}

func nowRFC3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }
