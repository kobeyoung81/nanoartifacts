// Package config reads nanoartifacts' settings from the environment.
//
// Values the service cannot invent are required: a missing signing secret or
// database URI is a startup error, not a default. A service that quietly ran
// without NANOARTIFACTS_URL_SECRET would hand out links that never expire.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config is the resolved environment.
type Config struct {
	Addr          string // listen address, e.g. :8080
	MongoURI      string // its own database; the only database this service touches
	SessionSecret string // HMAC key for the preview-session cookie
	QuotaBytes    int64  // per-user ceiling, counted after content dedupe
	BusURL        string // identity lookups (agent -> owner, user display names)
	BusToken      string // shared internal token for the bus's REST surface
	JWKSURL       string // nanoauth's keys, served through the bus
	WebDir        string // built SPA; empty means API only
	// CookiePath scopes the preview cookie. The default is "/" because the
	// service is reached at different paths depending on deployment (directly
	// at /api behind nginx, or /nanoartifacts/api through the portal); a path that
	// does not match silently produces 401s. Deployments that care can tighten
	// it to the external API path.
	CookiePath string
}

// Load reads and validates the environment.
func Load() (*Config, error) {
	c := &Config{
		Addr:       optional("NANOARTIFACTS_ADDR", ":8080"),
		WebDir:     optional("NANOARTIFACTS_WEB_DIR", "web/dist"),
		CookiePath: optional("NANOARTIFACTS_COOKIE_PATH", "/"),
		QuotaBytes: 2 << 30, // 2 GiB
	}

	var missing []string
	for _, field := range []struct {
		name string
		dst  *string
	}{
		{"NANOARTIFACTS_MONGO_URI", &c.MongoURI},
		{"NANOARTIFACTS_SESSION_SECRET", &c.SessionSecret},
		{"NANOSEC_BUS_URL", &c.BusURL},
		{"NANOSEC_BUS_INTERNAL_TOKEN", &c.BusToken},
		{"NANOAUTH_JWKS_URL", &c.JWKSURL},
	} {
		value := strings.TrimSpace(os.Getenv(field.name))
		if value == "" {
			missing = append(missing, field.name)
			continue
		}
		*field.dst = value
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required environment: %s", strings.Join(missing, ", "))
	}

	if raw := strings.TrimSpace(os.Getenv("NANOARTIFACTS_QUOTA_BYTES")); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("NANOARTIFACTS_QUOTA_BYTES must be a positive integer, got %q", raw)
		}
		c.QuotaBytes = n
	}

	return c, nil
}

func optional(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}
