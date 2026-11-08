// Command nanoartifacts serves the platform's work library: metadata over REST,
// bodies over a signed URL, publication over MCP.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"gitee.com/kobeyoung81/nanoartifacts/internal/auth"
	"gitee.com/kobeyoung81/nanoartifacts/internal/blobstore"
	"gitee.com/kobeyoung81/nanoartifacts/internal/config"
	"gitee.com/kobeyoung81/nanoartifacts/internal/identity"
	"gitee.com/kobeyoung81/nanoartifacts/internal/server"
	"gitee.com/kobeyoung81/nanoartifacts/internal/sign"
	"gitee.com/kobeyoung81/nanoartifacts/internal/store"
	"gitee.com/kobeyoung81/nanoartifacts/migrations"
)

// version is stamped at build time.
var version = "dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "serve":
		must(serve())
	case "migrate":
		if len(os.Args) < 3 || os.Args[2] != "up" {
			fmt.Fprintln(os.Stderr, "usage: nanoartifacts migrate up")
			os.Exit(2)
		}
		must(withStore(func(ctx context.Context, st *store.Store) error {
			ran, err := st.Migrate(loadManifests())
			for _, name := range ran {
				slog.Info("applied", "migration", name)
			}
			if err != nil {
				return err
			}
			fmt.Printf("applied %d migration(s)\n", len(ran))
			return nil
		}))
	case "backfill":
		if len(os.Args) < 3 || os.Args[2] != "spaces" {
			fmt.Fprintln(os.Stderr, "usage: nanoartifacts backfill spaces [--dry-run]")
			os.Exit(2)
		}
		dryRun := len(os.Args) > 3 && os.Args[3] == "--dry-run"
		must(withStore(func(ctx context.Context, st *store.Store) error {
			stats, err := st.BackfillSpaces(ctx, dryRun)
			if err != nil {
				return err
			}
			fmt.Printf("backfill spaces (dry-run=%v): scanned=%d spaces=%d folders=%d nodes=%d legacy_grants=%d\n",
				dryRun, stats.ArtifactsScanned, stats.SpacesCreated, stats.FoldersCreated, stats.NodesCreated, stats.LegacyGrantsKept)
			return nil
		}))
	case "gc":
		must(withStore(func(ctx context.Context, st *store.Store) error { return runGC(ctx, st) }))
	case "version":
		fmt.Println(version)
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: nanoartifacts <command>

  serve          run the HTTP and MCP server
  migrate up     apply pending MongoDB manifests
  backfill spaces [--dry-run]  give existing artifacts a space and a node
  gc             remove orphan chunks and unreferenced bodies
  version        print the build version`)
}

func must(err error) {
	if err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func serve() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx := context.Background()

	st, err := store.Open(ctx, cfg.MongoURI)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close(context.Background()) }()

	// Migrations run at start-up: this service owns its database, so there is no
	// init container to do it and a missing index must not be a runtime surprise.
	if _, err := st.Migrate(loadManifests()); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	// Idempotent: every artifact gets a personal space and a node. Running it on
	// every start means a deployment cannot leave the UI looking at an empty
	// library, and a second run costs one scan.
	if stats, err := st.BackfillSpaces(ctx, false); err != nil {
		slog.Warn("space backfill failed", "error", err)
	} else if stats.NodesCreated > 0 || stats.SpacesCreated > 0 {
		slog.Info("space backfill", "spaces", stats.SpacesCreated, "folders", stats.FoldersCreated, "nodes", stats.NodesCreated)
	}

	srv := &server.Server{
		Store:    st,
		Blobs:    blobstore.New(st.DB()),
		Signer:   sign.New(cfg.SessionSecret, 8*time.Hour),
		Users:    auth.New(cfg.JWKSURL, cfg.BusToken),
		Identity: identity.New(cfg.BusURL, cfg.BusToken),
		Quota:    cfg.QuotaBytes,
		BusToken: cfg.BusToken,
		Version:  version,
		WebDir:   cfg.WebDir,
		// A working day; the cookie is reissued every time a work is opened.
		SessionTTL: 8 * time.Hour,
		CookiePath: cfg.CookiePath,
	}

	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
		// A 16 MiB publish over a slow link needs room; the body itself is
		// capped by maxBody in the handlers.
		ReadTimeout:  2 * time.Minute,
		WriteTimeout: 2 * time.Minute,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	errCh := make(chan error, 1)
	go func() {
		slog.Info("nanoartifacts listening", "addr", cfg.Addr, "version", version)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-stop:
		slog.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	}
}

// runGC removes three kinds of leftovers:
//
//   - chunks whose file document never appeared (an upload interrupted between
//     chunks and the file record);
//   - bodies older than an hour that no version references (a publish whose
//     metadata write failed, or a blob only ever written by a crashed process);
//   - upload handles past their deadline.
//
// The one-hour threshold keeps it away from uploads in flight.
func runGC(ctx context.Context, st *store.Store) error {
	blobs := blobstore.New(st.DB())

	ids, err := blobs.DistinctFileIDs(ctx)
	if err != nil {
		return err
	}
	var orphanChunks int
	for _, id := range ids {
		ok, err := blobs.Exists(ctx, id)
		if err != nil {
			return err
		}
		if ok {
			continue
		}
		if err := blobs.Delete(ctx, id); err != nil {
			return err
		}
		orphanChunks++
	}

	referenced, err := st.ReferencedHashes(ctx)
	if err != nil {
		return err
	}
	files, err := blobs.FilesBefore(ctx, time.Now().Add(-time.Hour))
	if err != nil {
		return err
	}
	var unreferenced int
	for _, hash := range files {
		if referenced[hash] {
			continue
		}
		if err := blobs.Delete(ctx, hash); err != nil {
			return err
		}
		unreferenced++
	}

	fmt.Printf("gc: removed %d orphan chunk set(s), %d unreferenced body(ies)\n", orphanChunks, unreferenced)

	// Handles past their deadline are dead weight: without this they would
	// accumulate one row per abandoned publish forever. Their bodies are already
	// covered above — an upload nobody published is unreferenced and older than
	// the hour, so it is collected in the same pass.
	expired, err := st.DeleteExpiredUploads(ctx, time.Now().UTC())
	if err != nil {
		return err
	}
	if expired > 0 {
		fmt.Printf("gc: removed %d expired upload handle(s)\n", expired)
	}
	return nil
}

func withStore(fn func(context.Context, *store.Store) error) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx := context.Background()
	st, err := store.Open(ctx, cfg.MongoURI)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close(context.Background()) }()
	return fn(ctx, st)
}

func loadManifests() map[string]string {
	entries, err := migrations.FS.ReadDir(".")
	if err != nil {
		return nil
	}
	out := make(map[string]string, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		body, err := migrations.FS.ReadFile(e.Name())
		if err != nil {
			continue
		}
		out[e.Name()] = string(body)
	}
	return out
}
