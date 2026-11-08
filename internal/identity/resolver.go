// Package identity answers "who is this" without owning an identity table.
//
// Agents present their own id (injected by nanosec as X-Nanowork-Agent-ID), and
// this service asks the bus who that agent belongs to. Users are identified by
// the JWT subject; only display names are fetched, for the library UI.
package identity

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Resolver caches bus lookups.
type Resolver struct {
	baseURL string
	token   string
	client  *http.Client

	mu     sync.Mutex
	agents map[string]cached
	users  map[string]cached
	ttl    time.Duration
}

type cached struct {
	value string
	at    time.Time
}

// New returns a resolver against the bus's REST surface.
func New(baseURL, token string) *Resolver {
	return &Resolver{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		client:  &http.Client{Timeout: 10 * time.Second},
		agents:  map[string]cached{},
		users:   map[string]cached{},
		ttl:     5 * time.Minute,
	}
}

// User is the part of an account this service shows.
type User struct {
	ID          string `json:"user_id"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
}

// OwnerOfAgent maps an agent id to its owning user.
func (r *Resolver) OwnerOfAgent(ctx context.Context, agentID string) (string, error) {
	if agentID == "" {
		return "", fmt.Errorf("agent id is required")
	}
	r.mu.Lock()
	if c, ok := r.agents[agentID]; ok && time.Since(c.at) < r.ttl {
		r.mu.Unlock()
		return c.value, nil
	}
	r.mu.Unlock()

	var info struct {
		UserID string `json:"user_id"`
	}
	if err := r.get(ctx, "/api/v1/agents/"+url.PathEscape(agentID), &info); err != nil {
		return "", fmt.Errorf("resolve agent owner: %w", err)
	}
	if info.UserID == "" {
		return "", fmt.Errorf("agent %s has no owner", agentID)
	}
	r.mu.Lock()
	r.agents[agentID] = cached{value: info.UserID, at: time.Now()}
	r.mu.Unlock()
	return info.UserID, nil
}

// DisplayName returns a human name, or the id when the bus cannot answer.
func (r *Resolver) DisplayName(ctx context.Context, userID string) string {
	if userID == "" {
		return ""
	}
	r.mu.Lock()
	if c, ok := r.users[userID]; ok && time.Since(c.at) < r.ttl {
		r.mu.Unlock()
		return c.value
	}
	r.mu.Unlock()

	var user User
	if err := r.get(ctx, "/api/v1/user-accounts/"+url.PathEscape(userID), &user); err != nil {
		return userID
	}
	name := user.DisplayName
	if name == "" {
		name = userID
	}
	r.mu.Lock()
	r.users[userID] = cached{value: name, at: time.Now()}
	r.mu.Unlock()
	return name
}

// FindByEmail is what the share dialog searches with. The bus matches on the
// address only; there is no fuzzy name search there, and inventing one here
// would mean caching the whole user table.
func (r *Resolver) FindByEmail(ctx context.Context, email string) (*User, error) {
	email = strings.TrimSpace(email)
	if email == "" {
		return nil, fmt.Errorf("email is required")
	}
	var out struct {
		Users []User `json:"users"`
	}
	if err := r.get(ctx, "/api/v1/user-accounts?email="+url.QueryEscape(email), &out); err != nil {
		return nil, err
	}
	if len(out.Users) == 0 {
		return nil, nil
	}
	return &out.Users[0], nil
}

// SearchUsers is the member picker's directory. It searches by fragment (a
// couple of name or address characters), never by exact address, and the bus
// caps the list. An empty fragment returns nothing: asking the bus for "everyone"
// to render a dropdown is both slow and a needless copy of the user table.
func (r *Resolver) SearchUsers(ctx context.Context, q string) ([]User, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, nil
	}
	var out struct {
		Users []User `json:"users"`
	}
	path := "/api/v1/user-accounts?q=" + url.QueryEscape(q) + "&limit=10"
	if err := r.get(ctx, path, &out); err != nil {
		return nil, err
	}
	return out.Users, nil
}

func (r *Resolver) get(ctx context.Context, path string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+r.token)
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bus %s: HTTP %d", path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(dst)
}
