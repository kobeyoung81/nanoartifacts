package config

import (
	"strings"
	"testing"
)

// requiredExcept fills everything Load refuses to start without, then blanks the
// variable named. Blank rather than unset: the loader wraps the environment and a
// leftover value from the deployment must not make a test pass by accident.
func requiredExcept(t *testing.T, blank string) {
	t.Helper()
	values := map[string]string{
		"NANOARTIFACTS_MONGO_URI":      "mongodb://example/nanosec",
		"NANOARTIFACTS_SESSION_SECRET": "secret",
		"NANOSEC_BUS_INTERNAL_TOKEN":   "token",
		"NANOAUTH_JWKS_URL":            "http://nanoauth:8081/auth/v1/.well-known/jwks",
		"NANOSEC_PORTAL_URL":           "http://nanoportal:80",
		"NANOAUTH_INTERNAL_URL":        "http://nanoauth:8081",
	}
	for name, value := range values {
		if name == blank {
			value = ""
		}
		t.Setenv(name, value)
	}
	// The bus base URL is not a substitute for either owner: agents and accounts
	// are the portal's and nanoauth's, and pointing this service at the bus is the
	// outage these requirements exist to prevent.
	t.Setenv("NANOSEC_BUS_URL", "http://nanobus:9000")
}

func TestLoadRequiresThePortalForAgents(t *testing.T) {
	requiredExcept(t, "NANOSEC_PORTAL_URL")

	_, err := Load()
	if err == nil {
		t.Fatal("a missing portal URL must be a startup error")
	}
	if !strings.Contains(err.Error(), "NANOSEC_PORTAL_URL") {
		t.Fatalf("err = %v, want it to name NANOSEC_PORTAL_URL", err)
	}
}

func TestLoadRequiresNanoauthForAccounts(t *testing.T) {
	requiredExcept(t, "NANOAUTH_INTERNAL_URL")

	_, err := Load()
	if err == nil {
		t.Fatal("a missing nanoauth URL must be a startup error")
	}
	if !strings.Contains(err.Error(), "NANOAUTH_INTERNAL_URL") {
		t.Fatalf("err = %v, want it to name NANOAUTH_INTERNAL_URL", err)
	}
}

func TestLoadReadsBothIdentityOwners(t *testing.T) {
	requiredExcept(t, "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.PortalURL != "http://nanoportal:80" {
		t.Fatalf("PortalURL = %q", cfg.PortalURL)
	}
	if cfg.AuthURL != "http://nanoauth:8081" {
		t.Fatalf("AuthURL = %q", cfg.AuthURL)
	}
}
