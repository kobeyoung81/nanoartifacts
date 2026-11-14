// Package store owns nanoartifacts' MongoDB database: metadata in three
// collections, blobs in a GridFS-compatible pair (see internal/blobstore).
//
// Nothing else in the platform opens this database. The bus owns its own and
// the two are joined by HTTP, not by a shared connection — the architecture note
// in the repository root says "one owner per database", and this service is the
// owner of its own.
package store

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"

	"gitee.com/kobeyoung81/nanoartifacts/internal/metrics"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Store is the handle the rest of the service uses.
type Store struct {
	client *mongo.Client
	db     *mongo.Database
}

// Open connects and verifies the connection before returning.
//
// The database name is taken from the URI path on purpose: a deployment that
// forgets it would otherwise land in "test" and look healthy.
func Open(ctx context.Context, uri string) (*Store, error) {
	name, err := databaseFromURI(uri)
	if err != nil {
		return nil, err
	}

	connectCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	// One monitor per client: it feeds the module's database numbers (command
	// counts and latency) without every call site having to time itself.
	client, err := mongo.Connect(options.Client().
		ApplyURI(uri).
		SetMonitor(metrics.Monitor()))
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	if err := client.Ping(connectCtx, nil); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, fmt.Errorf("ping %s: %w", name, err)
	}
	return &Store{client: client, db: client.Database(name)}, nil
}

// Close releases the connection pool.
func (s *Store) Close(ctx context.Context) error { return s.client.Disconnect(ctx) }

// DB exposes the database for the blobstore.
func (s *Store) DB() *mongo.Database { return s.db }

// databaseFromURI reads the path component of a mongodb:// URI.
func databaseFromURI(uri string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", fmt.Errorf("parse mongo uri: %w", err)
	}
	name := strings.Trim(strings.TrimPrefix(u.Path, "/"), " ")
	if name == "" {
		return "", fmt.Errorf("mongo uri must name a database (mongodb://host:port/<db>)")
	}
	return name, nil
}
