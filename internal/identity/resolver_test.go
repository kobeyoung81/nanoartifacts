package identity

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// The two fakes stand in for the owners. Each records the request URIs it was
// asked for, so a test can pin the contract (which owner, which path) instead of
// only the result — the failure this package just had was a wrong path that a
// fixture had invented along with the implementation.
type fakeOwner struct {
	*httptest.Server
	mu    sync.Mutex
	asked []string
}

func newFakeOwner(t *testing.T, handler http.HandlerFunc) *fakeOwner {
	t.Helper()
	f := &fakeOwner{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.asked = append(f.asked, r.URL.RequestURI())
		f.mu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeOwner) asks() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.asked...)
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

// portalAgent answers the shape the portal's internal agent read returns.
func portalAgent(userID string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"agent": map[string]string{"id": "agent-1", "user_id": userID},
		})
	}
}

func noCalls(t *testing.T, what string) http.HandlerFunc {
	return func(http.ResponseWriter, *http.Request) {
		t.Errorf("%s was asked something it does not own", what)
	}
}

func TestOwnerOfAgentAsksThePortal(t *testing.T) {
	portal := newFakeOwner(t, portalAgent("owner-1"))
	auth := newFakeOwner(t, noCalls(t, "nanoauth"))
	r := New(Options{PortalURL: portal.URL, AuthURL: auth.URL, Token: "tok"})

	owner, err := r.OwnerOfAgent(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("owner: %v", err)
	}
	if owner != "owner-1" {
		t.Fatalf("owner = %q, want owner-1", owner)
	}

	asked := portal.asks()
	if len(asked) != 1 {
		t.Fatalf("portal asked %d time(s), want 1: %v", len(asked), asked)
	}
	if asked[0] != "/internal/v1/agents/agent-1" {
		t.Fatalf("asked %q, want the portal's internal agent read", asked[0])
	}
	if got := len(auth.asks()); got != 0 {
		t.Fatalf("nanoauth was asked %d time(s) about an agent, want 0", got)
	}
}

func TestOwnerOfAgentSendsTheSharedToken(t *testing.T) {
	var bearer string
	portal := newFakeOwner(t, func(w http.ResponseWriter, r *http.Request) {
		bearer = r.Header.Get("Authorization")
		portalAgent("owner-1")(w, r)
	})
	r := New(Options{PortalURL: portal.URL, AuthURL: "http://127.0.0.1:1", Token: "tok"})

	if _, err := r.OwnerOfAgent(context.Background(), "agent-1"); err != nil {
		t.Fatalf("owner: %v", err)
	}
	if bearer != "Bearer tok" {
		t.Fatalf("Authorization = %q, want the shared token", bearer)
	}
}

func TestOwnerOfAgentNotFoundIsNotAPlatformFailure(t *testing.T) {
	portal := newFakeOwner(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent not found"})
	})
	r := New(Options{PortalURL: portal.URL, AuthURL: "http://127.0.0.1:1", Token: "tok"})

	_, err := r.OwnerOfAgent(context.Background(), "ghost")
	if !errors.Is(err, ErrUnknownAgent) {
		t.Fatalf("err = %v, want ErrUnknownAgent", err)
	}
}

func TestOwnerOfAgentReportsAnUnreachableOwnerAsAnError(t *testing.T) {
	portal := newFakeOwner(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "boom"})
	})
	r := New(Options{PortalURL: portal.URL, AuthURL: "http://127.0.0.1:1", Token: "tok"})

	_, err := r.OwnerOfAgent(context.Background(), "agent-1")
	if err == nil {
		t.Fatal("a broken portal must not read as success")
	}
	if errors.Is(err, ErrUnknownAgent) {
		t.Fatalf("err = %v: a broken owner is not an unknown agent", err)
	}
	if !strings.Contains(err.Error(), "HTTP 500") || !strings.Contains(err.Error(), "portal") {
		t.Fatalf("err = %v, want it to name the owner and the status", err)
	}
}

func TestOwnerOfAgentIsCached(t *testing.T) {
	portal := newFakeOwner(t, portalAgent("owner-1"))
	r := New(Options{PortalURL: portal.URL, AuthURL: "http://127.0.0.1:1", Token: "tok"})

	for i := 0; i < 3; i++ {
		if _, err := r.OwnerOfAgent(context.Background(), "agent-1"); err != nil {
			t.Fatalf("owner %d: %v", i, err)
		}
	}
	if got := len(portal.asks()); got != 1 {
		t.Fatalf("portal asked %d times, want 1: %v", got, portal.asks())
	}
}

func TestDisplayNameAsksAuthAndFallsBackToTheID(t *testing.T) {
	auth := newFakeOwner(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/internal/v1/accounts/u1":
			writeJSON(w, http.StatusOK, map[string]string{"user_id": "u1", "display_name": "Alice"})
		case "/internal/v1/accounts/gone":
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		default:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "boom"})
		}
	})
	portal := newFakeOwner(t, noCalls(t, "the portal"))
	r := New(Options{PortalURL: portal.URL, AuthURL: auth.URL, Token: "tok"})
	ctx := context.Background()

	if name := r.DisplayName(ctx, "u1"); name != "Alice" {
		t.Fatalf("display name = %q, want Alice", name)
	}
	// A name is decoration: a missing account and a broken owner both answer with
	// the id rather than failing the page.
	if name := r.DisplayName(ctx, "gone"); name != "gone" {
		t.Fatalf("missing account = %q, want the id", name)
	}
	if name := r.DisplayName(ctx, "unwell"); name != "unwell" {
		t.Fatalf("broken owner = %q, want the id", name)
	}
	asked := auth.asks()
	if len(asked) != 3 {
		t.Fatalf("nanoauth asked %d times, want 3: %v", len(asked), asked)
	}
	if asked[0] != "/internal/v1/accounts/u1" {
		t.Fatalf("asked %q, want nanoauth's internal account read", asked[0])
	}
}

func TestDisplayNameCachesTheNotFoundFallback(t *testing.T) {
	auth := newFakeOwner(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
	})
	r := New(Options{PortalURL: "http://127.0.0.1:1", AuthURL: auth.URL, Token: "tok"})

	for i := 0; i < 3; i++ {
		if name := r.DisplayName(context.Background(), "gone"); name != "gone" {
			t.Fatalf("call %d = %q, want the id", i, name)
		}
	}
	if got := len(auth.asks()); got != 1 {
		t.Fatalf("a deleted account cost %d lookups, want 1: %v", got, auth.asks())
	}
}

func TestFindByEmailDecodesOneAccountAndTreats404AsNone(t *testing.T) {
	auth := newFakeOwner(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("email") == "nobody@example.test" {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{
			"user_id": "u1", "display_name": "Alice", "email": "alice@example.test",
		})
	})
	r := New(Options{PortalURL: "http://127.0.0.1:1", AuthURL: auth.URL, Token: "tok"})
	ctx := context.Background()

	user, err := r.FindByEmail(ctx, "alice@example.test")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if user == nil || user.ID != "u1" || user.DisplayName != "Alice" {
		t.Fatalf("user = %+v, want the account nanoauth returned", user)
	}
	// The whole URI, not a fragment: the address lookup moved owners once already,
	// and a fragment assertion would accept the old service's path.
	if asked := auth.asks(); asked[0] != "/internal/v1/accounts?email=alice%40example.test" {
		t.Fatalf("asked %q, want nanoauth's exact-address lookup", asked[0])
	}

	missing, err := r.FindByEmail(ctx, "nobody@example.test")
	if err != nil {
		t.Fatalf("missing address must not be an error: %v", err)
	}
	if missing != nil {
		t.Fatalf("missing = %+v, want nil", missing)
	}
}

func TestSearchUsersDecodesTheAccountList(t *testing.T) {
	auth := newFakeOwner(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"accounts": []map[string]string{
				{"user_id": "u1", "display_name": "Alice", "email": "alice@example.test"},
				{"user_id": "u2", "display_name": "Alicia", "email": "alicia@example.test"},
			},
		})
	})
	r := New(Options{PortalURL: "http://127.0.0.1:1", AuthURL: auth.URL, Token: "tok"})
	ctx := context.Background()

	users, err := r.SearchUsers(ctx, "ali")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(users) != 2 || users[1].ID != "u2" {
		t.Fatalf("users = %+v, want the two summaries", users)
	}
	asked := auth.asks()
	if len(asked) != 1 {
		t.Fatalf("nanoauth asked %d times, want 1", len(asked))
	}
	if asked[0] != "/internal/v1/accounts?q=ali&limit=10" {
		t.Fatalf("asked %q, want nanoauth's search with the cap", asked[0])
	}

	// An empty fragment must not become "list everyone".
	if users, err := r.SearchUsers(ctx, "   "); err != nil || users != nil {
		t.Fatalf("empty fragment = %v, %v; want no results and no error", users, err)
	}
	if got := len(auth.asks()); got != 1 {
		t.Fatalf("an empty fragment cost a request: %v", auth.asks())
	}
}

func TestAnUnconfiguredOwnerFailsLoudly(t *testing.T) {
	r := New(Options{PortalURL: "", AuthURL: "", Token: "tok"})
	if _, err := r.OwnerOfAgent(context.Background(), "agent-1"); err == nil ||
		!strings.Contains(err.Error(), "not configured") {
		t.Fatalf("err = %v, want an unconfigured-owner error", err)
	}
	if _, err := r.FindByEmail(context.Background(), "a@b.c"); err == nil {
		t.Fatal("an unconfigured nanoauth must not read as 'no such account'")
	}
}
