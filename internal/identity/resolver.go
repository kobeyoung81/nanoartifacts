// Package identity answers "who is this" without owning an identity table.
//
// Agents present their own id (injected by nanosec as X-Nanowork-Agent-ID), and
// this service asks the module that owns the row. There are two owners, so there
// are two questions and two base URLs:
//
//   - the portal keeps the agent rows, so it answers "whose agent is this";
//   - nanoauth keeps the accounts, so it answers display names and address
//     lookups.
//
// Neither question is a database read from here: this service holds no identity
// table, and the owners expose these reads on their internal surfaces.
package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"gitee.com/kobeyoung81/nanoartifacts/internal/metrics"
)

// ErrUnknownAgent reports that the portal does not know an agent id. That is a
// caller's mistake (a stale container or a stale tab), not a platform failure,
// and the handlers answer it differently from an owner they cannot reach.
var ErrUnknownAgent = errors.New("unknown agent")

// Lookup kinds, used as the metric label. They name the question rather than the
// service, so a dashboard survives an owner moving.
const (
	lookupAgentOwner  = "agent_owner"
	lookupDisplayName = "display_name"
	lookupFindByEmail = "find_by_email"
	lookupSearch      = "search"
)

// Options says where the two owners live.
type Options struct {
	// PortalURL is the portal's internal surface; it owns the agent rows.
	PortalURL string
	// AuthURL is nanoauth's internal surface; it owns the accounts.
	AuthURL string
	// Token is the deployment's shared internal token. Both owners refuse an
	// unauthenticated call.
	Token string
}

// Resolver caches owner lookups.
type Resolver struct {
	portalURL string
	authURL   string
	token     string
	client    *http.Client

	mu     sync.Mutex
	agents map[string]cached
	users  map[string]cached
	ttl    time.Duration
}

type cached struct {
	value string
	at    time.Time
}

// New returns a resolver over the two internal surfaces.
func New(opts Options) *Resolver {
	return &Resolver{
		portalURL: strings.TrimRight(strings.TrimSpace(opts.PortalURL), "/"),
		authURL:   strings.TrimRight(strings.TrimSpace(opts.AuthURL), "/"),
		token:     opts.Token,
		client:    &http.Client{Timeout: 10 * time.Second},
		agents:    map[string]cached{},
		users:     map[string]cached{},
		ttl:       5 * time.Minute,
	}
}

// User is the part of an account this service shows. Both answers nanoauth
// gives — one account, and the summaries a search returns — decode into it.
type User struct {
	ID          string `json:"user_id"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
}

// OwnerOfAgent maps an agent id to its owning user. ErrUnknownAgent is wrapped
// when the portal simply does not have that row.
func (r *Resolver) OwnerOfAgent(ctx context.Context, agentID string) (string, error) {
	if agentID == "" {
		return "", fmt.Errorf("agent id is required")
	}
	if owner, ok := r.cachedValue(r.agents, agentID); ok {
		return owner, nil
	}

	var payload struct {
		Agent struct {
			UserID string `json:"user_id"`
		} `json:"agent"`
	}
	status, err := r.get(ctx, r.portalURL, "portal",
		"/internal/v1/agents/"+url.PathEscape(agentID), &payload)
	if err != nil {
		if status == http.StatusNotFound {
			count(lookupAgentOwner, "not_found")
			return "", fmt.Errorf("%w: %s", ErrUnknownAgent, agentID)
		}
		count(lookupAgentOwner, "error")
		return "", fmt.Errorf("resolve agent owner: %w", err)
	}
	if payload.Agent.UserID == "" {
		count(lookupAgentOwner, "error")
		return "", fmt.Errorf("portal returned agent %s without an owner", agentID)
	}
	r.remember(r.agents, agentID, payload.Agent.UserID)
	count(lookupAgentOwner, "ok")
	return payload.Agent.UserID, nil
}

// DisplayName returns a human name, or the id when the account cannot be read.
//
// A name is decoration: an unreachable owner must not fail a page, so every
// failure falls back to the id. A "no such account" answer is cached with that
// fallback — a deleted account should not cost one lookup per render — while a
// transport failure is not cached, because the owner may only be briefly away.
// The metric keeps both visible instead of letting them hide behind the id.
func (r *Resolver) DisplayName(ctx context.Context, userID string) string {
	if userID == "" {
		return ""
	}
	if name, ok := r.cachedValue(r.users, userID); ok {
		return name
	}

	var account User
	status, err := r.get(ctx, r.authURL, "nanoauth",
		"/internal/v1/accounts/"+url.PathEscape(userID), &account)
	if err != nil {
		if status == http.StatusNotFound {
			count(lookupDisplayName, "not_found")
			r.remember(r.users, userID, userID)
		} else {
			count(lookupDisplayName, "error")
		}
		return userID
	}
	name := account.DisplayName
	if name == "" {
		name = userID
	}
	r.remember(r.users, userID, name)
	count(lookupDisplayName, "ok")
	return name
}

// FindByEmail is what the share dialog searches with: nanoauth answers with the
// account itself, and 404 for an address nobody owns. A nil user without an
// error means "no such account".
func (r *Resolver) FindByEmail(ctx context.Context, email string) (*User, error) {
	email = strings.TrimSpace(email)
	if email == "" {
		return nil, fmt.Errorf("email is required")
	}

	var account User
	status, err := r.get(ctx, r.authURL, "nanoauth",
		"/internal/v1/accounts?email="+url.QueryEscape(email), &account)
	if err != nil {
		if status == http.StatusNotFound {
			count(lookupFindByEmail, "not_found")
			return nil, nil
		}
		count(lookupFindByEmail, "error")
		return nil, err
	}
	if account.ID == "" {
		count(lookupFindByEmail, "not_found")
		return nil, nil
	}
	count(lookupFindByEmail, "ok")
	return &account, nil
}

// SearchUsers is the member picker's directory. It searches by fragment (a
// couple of name or address characters), never by exact address, and nanoauth
// caps the list. An empty fragment returns nothing: asking for "everyone" to
// render a dropdown is both slow and a needless copy of the user table.
func (r *Resolver) SearchUsers(ctx context.Context, q string) ([]User, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, nil
	}

	var out struct {
		Accounts []User `json:"accounts"`
	}
	if _, err := r.get(ctx, r.authURL, "nanoauth",
		"/internal/v1/accounts?q="+url.QueryEscape(q)+"&limit=10", &out); err != nil {
		count(lookupSearch, "error")
		return nil, err
	}
	count(lookupSearch, "ok")
	return out.Accounts, nil
}

// get asks one owner a question and reports the HTTP status, so a caller can
// tell "no such row" apart from "the owner is unwell". 0 means the request never
// produced a status (unconfigured owner, transport failure).
func (r *Resolver) get(ctx context.Context, base, owner, path string, dst any) (int, error) {
	if base == "" {
		return 0, fmt.Errorf("the %s is not configured", owner)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+r.token)
	resp, err := r.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, fmt.Errorf("%s %s: HTTP %d", owner, path, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		return resp.StatusCode, fmt.Errorf("decode %s %s: %w", owner, path, err)
	}
	return resp.StatusCode, nil
}

func count(kind, result string) {
	metrics.IdentityLookups.WithLabelValues(kind, result).Inc()
}

func (r *Resolver) cachedValue(m map[string]cached, key string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := m[key]; ok && time.Since(c.at) < r.ttl {
		return c.value, true
	}
	return "", false
}

func (r *Resolver) remember(m map[string]cached, key, value string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	m[key] = cached{value: value, at: time.Now()}
}
