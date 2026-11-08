package store

import (
	"context"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// The artifacts service and the bus now keep their migration bookkeeping in
// one shared collection, so every record has to name its owner. Without the
// service prefix a manifest of the same filename in the other service would
// look applied here — and both sides name their files with a numeric prefix,
// so a collision is the normal case rather than a freak one.
func TestMigrationsAreScopedToArtifacts(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	foreign := bson.D{
		{Key: "_id", Value: "bus:0001_artifacts.json"},
		{Key: "service", Value: "bus"},
	}
	if _, err := st.DB().Collection(migrationsCollection).InsertOne(ctx, foreign); err != nil {
		t.Fatalf("insert foreign migration record: %v", err)
	}

	status, err := st.MigrationStatus(manifests(t))
	if err != nil {
		t.Fatalf("migration status: %v", err)
	}
	if len(status) == 0 {
		t.Fatal("no manifests reported")
	}
	for _, m := range status {
		if !m.Applied {
			t.Errorf("%s reported pending: the foreign record was read as this service's", m.Name)
		}
	}

	own, err := st.DB().Collection(migrationsCollection).CountDocuments(ctx,
		bson.D{{Key: "service", Value: "artifacts"}})
	if err != nil {
		t.Fatalf("count artifacts records: %v", err)
	}
	if own == 0 {
		t.Fatal("no artifacts records: migrations are not being recorded")
	}

	var record bson.M
	if err := st.DB().Collection(migrationsCollection).FindOne(ctx,
		bson.D{{Key: "service", Value: "artifacts"}}).Decode(&record); err != nil {
		t.Fatalf("read an artifacts record: %v", err)
	}
	id, _ := record["_id"].(string)
	if !strings.HasPrefix(id, "artifacts:") {
		t.Fatalf("record id = %q, want an artifacts: prefix", id)
	}
}
