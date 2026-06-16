package brainstore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSemanticSummary(t *testing.T) {
	brain := t.TempDir()
	snapDir := filepath.Join(brain, "semantic", "snapshots", "gen1")
	if err := os.MkdirAll(snapDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ndjson := `{"schema_version":"1.0","provider":"entire-sem","capabilities":["go","routes"]}
{"record_type":"symbol","kind":"function","name":"Handler","file_path":"api/routes.go","language":"go"}
{"record_type":"symbol","kind":"function","name":"Helper","file_path":"api/routes.go","language":"go"}
{"record_type":"symbol","kind":"struct","name":"Server","file_path":"server.go","language":"go"}
{"record_type":"relation","type":"calls","from_id":"a","to_id":"b"}
`
	if err := os.WriteFile(filepath.Join(snapDir, "snapshot.ndjson"), []byte(ndjson), 0o644); err != nil {
		t.Fatal(err)
	}
	src := &SemanticSource{
		Provider:     "entire-sem",
		SnapshotPath: "semantic/snapshots/gen1/snapshot.ndjson",
		Capabilities: []string{"go", "routes"},
	}

	sum, err := LoadSemanticSummary(brain, src)
	if err != nil {
		t.Fatal(err)
	}
	if sum == nil {
		t.Fatal("nil summary for a present snapshot")
	}
	if sum.Symbols != 3 || sum.Relations != 1 || sum.Files != 2 {
		t.Errorf("symbols=%d relations=%d files=%d, want 3/1/2", sum.Symbols, sum.Relations, sum.Files)
	}
	if len(sum.ByKind) == 0 || sum.ByKind[0].Label != "function" || sum.ByKind[0].Count != 2 {
		t.Errorf("ByKind top = %v, want function(2)", sum.ByKind)
	}
	if len(sum.TopFiles) == 0 || sum.TopFiles[0].Label != "api/routes.go" {
		t.Errorf("TopFiles top = %v, want api/routes.go busiest", sum.TopFiles)
	}

	// A traversal path is refused (returns nil, not an error).
	if s, _ := LoadSemanticSummary(brain, &SemanticSource{SnapshotPath: "../../etc/passwd"}); s != nil {
		t.Error("expected nil for a path that escapes the brain dir")
	}
	// A nil source / no sem layer is not an error.
	if s, _ := LoadSemanticSummary(brain, nil); s != nil {
		t.Error("expected nil summary for a nil source")
	}
}

func TestLoadSemanticSummaryFileFallback(t *testing.T) {
	brain := t.TempDir()
	snapDir := filepath.Join(brain, "semantic", "snapshots", "g")
	if err := os.MkdirAll(snapDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A snapshot that enumerates only files (no symbol/relation records): the
	// symbol/relation counts must fall back to the source manifest's totals.
	ndjson := `{"schema_version":"1.0","provider":"entire-sem"}
{"record_type":"file","file_path":"a.go"}
{"record_type":"file","file_path":"b.go"}
`
	if err := os.WriteFile(filepath.Join(snapDir, "snapshot.ndjson"), []byte(ndjson), 0o644); err != nil {
		t.Fatal(err)
	}
	src := &SemanticSource{SnapshotPath: "semantic/snapshots/g/snapshot.ndjson", Symbols: 42, Relations: 7, Files: 9}
	sum, err := LoadSemanticSummary(brain, src)
	if err != nil || sum == nil {
		t.Fatalf("LoadSemanticSummary: sum=%v err=%v", sum, err)
	}
	if sum.Files != 2 {
		t.Errorf("files = %d, want 2 (counted from file records)", sum.Files)
	}
	if sum.Symbols != 42 || sum.Relations != 7 {
		t.Errorf("symbols=%d relations=%d, want 42/7 (fallback to source counts)", sum.Symbols, sum.Relations)
	}
}
