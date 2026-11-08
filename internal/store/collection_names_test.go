package store

import (
	"encoding/json"
	"strings"
	"testing"

	"gitee.com/kobeyoung81/nanoartifacts/migrations"
)

// The prefix is the writer boundary: one collection, one writer, and the name
// says which module that is. The root collection is the documented exception —
// it is named after the module itself.
func TestCollectionNamesCarryTheirModulePrefix(t *testing.T) {
	owned := []string{
		artifactsCollection,
		versionsCollection,
		grantsCollection,
		spacesCollection,
		spaceGrantsCollection,
		nodesCollection,
		uploadsCollection,
		migrationsCollection,
	}
	for _, name := range owned {
		switch {
		case name == artifactsCollection:
			if name != "artifacts" {
				t.Errorf("root collection = %q, want artifacts", name)
			}
		case name == migrationsCollection:
			if !strings.HasPrefix(name, "sys_") {
				t.Errorf("%q does not start with sys_", name)
			}
		default:
			if !strings.HasPrefix(name, "artifacts_") {
				t.Errorf("%q does not start with artifacts_", name)
			}
		}
	}
}

// The manifests create the indexes, so a collection renamed only in Go leaves
// production without them — and nothing fails at startup.
func TestMigrationManifestsUsePrefixedCollections(t *testing.T) {
	entries, err := migrations.FS.ReadDir(".")
	if err != nil {
		t.Fatalf("read manifests: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		body, err := migrations.FS.ReadFile(entry.Name())
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		var manifest struct {
			Collections map[string]json.RawMessage `json:"collections"`
		}
		if err := json.Unmarshal(body, &manifest); err != nil {
			t.Fatalf("%s: parse: %v", entry.Name(), err)
		}
		for collection := range manifest.Collections {
			if collection != artifactsCollection && !strings.HasPrefix(collection, "artifacts_") {
				t.Errorf("%s: collection %q has no module prefix", entry.Name(), collection)
			}
		}
	}
}
