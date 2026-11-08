package store

import (
	"strings"
	"testing"
)

func TestParseManifestRejectsUnknownFields(t *testing.T) {
	// A typo in a manifest must fail loudly: a silently ignored "uniqe" would
	// create a non-unique index and the duplicate-content guarantee would be
	// gone without any test noticing.
	_, err := parseManifest(`{"collections":{"a":{"indexes":[{"keys":[["x",1]],"uniqe":true}]}}}`)
	if err == nil {
		t.Fatal("unknown field was accepted")
	}
}

func TestParseManifestConvertsDirections(t *testing.T) {
	spec, err := parseManifest(`{"collections":{"a":{"indexes":[{"keys":[["x",1],["y","hashed"]]}]}}}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	keys := spec.Collections["a"].Indexes[0].keys()
	if len(keys) != 2 {
		t.Fatalf("got %d keys", len(keys))
	}
	if v, ok := keys[0].Value.(int32); !ok || v != 1 {
		t.Fatalf("numeric direction = %#v, want int32(1)", keys[0].Value)
	}
	if v, ok := keys[1].Value.(string); !ok || v != "hashed" {
		t.Fatalf("string direction = %#v", keys[1].Value)
	}
}

func TestParseManifestRejectsEmptyIndex(t *testing.T) {
	if _, err := parseManifest(`{"collections":{"a":{"indexes":[{"name":"x","keys":[]}]}}}`); err == nil {
		t.Fatal("an index with no keys was accepted")
	}
}

func TestRegexpQuote(t *testing.T) {
	got := regexpQuote("a.b*c")
	if !strings.Contains(got, `\.`) || !strings.Contains(got, `\*`) {
		t.Fatalf("regexpQuote(%q) = %q, metacharacters are not escaped", "a.b*c", got)
	}
}
