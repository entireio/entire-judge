package gitutil

import (
	"context"
	"strings"
	"testing"
	"time"
)

type fakeRunner struct{ gitLog string }

func (f fakeRunner) Run(ctx context.Context, dir, name string, args ...string) ([]byte, []byte, error) {
	if name == "git" && len(args) >= 1 && args[0] == "log" {
		return []byte(f.gitLog), nil, nil
	}
	return nil, nil, context.Canceled
}

func record(hash, parents, committedAt, body string) string {
	return strings.Join([]string{hash, parents, committedAt, "dev", "dev@x.com", body}, "\x00") + "\x1e"
}

func TestParseGitLog(t *testing.T) {
	at := time.Date(2026, 6, 4, 10, 0, 0, 0, time.UTC).Format(time.RFC3339)
	data := record("aaaa111", "", at, "first commit\n\nEntire-Checkpoint: 0123456789ab")
	commits := ParseGitLog([]byte(data))
	if len(commits) != 1 {
		t.Fatalf("commits = %d, want 1", len(commits))
	}
	c := commits[0]
	if c.Hash != "aaaa111" {
		t.Errorf("hash = %q", c.Hash)
	}
	if c.Subject != "first commit" {
		t.Errorf("subject = %q", c.Subject)
	}
	if len(c.Checkpoints) != 1 || c.Checkpoints[0] != "0123456789ab" {
		t.Errorf("checkpoints = %v", c.Checkpoints)
	}
}

func TestBuildHistoryCoverageCoverageClasses(t *testing.T) {
	oldest := time.Date(2026, 6, 4, 9, 0, 0, 0, time.UTC)
	preAt := oldest.Add(-time.Hour).Format(time.RFC3339)
	coveredAt := oldest.Add(time.Hour).Format(time.RFC3339)
	missingAt := oldest.Add(2 * time.Hour).Format(time.RFC3339)

	data := record("pre0001", "", preAt, "pre-session work") +
		record("cov0001", "", coveredAt, "session work\n\nEntire-Checkpoint: aaaaaaaaaaaa") +
		record("cov0002", "", coveredAt, "more session work (intermediate checkpoint)\n\nEntire-Checkpoint: bbbbbbbbbbbb") +
		record("mis0001", "", missingAt, "no checkpoint trailer")
	runner := fakeRunner{gitLog: data}

	// A commit carrying any Entire checkpoint trailer is covered, whether or not
	// that exact checkpoint is the session's exported latest-checkpoint id.
	exported := map[string]struct{}{"aaaaaaaaaaaa": {}}
	cov := BuildHistoryCoverage(context.Background(), runner, "/repo", &oldest, exported)
	if cov.TotalCommits != 4 {
		t.Errorf("total = %d, want 4", cov.TotalCommits)
	}
	if cov.PreSessionCommits != 1 {
		t.Errorf("pre-session = %d, want 1", cov.PreSessionCommits)
	}
	if cov.CoveredCommits != 2 {
		t.Errorf("covered = %d, want 2 (both checkpointed commits, including the intermediate one)", cov.CoveredCommits)
	}
	if cov.MissingSessionCommits != 1 {
		t.Errorf("missing-session = %d, want 1", cov.MissingSessionCommits)
	}
	// The coverage buckets must partition TotalCommits exactly (merges are an
	// overlapping tally, not part of the partition).
	sum := cov.PreSessionCommits + cov.CoveredCommits + cov.MissingSessionCommits + cov.NoSessionHistoryCommits
	if sum != cov.TotalCommits {
		t.Errorf("coverage buckets sum to %d, want %d (must partition total)", sum, cov.TotalCommits)
	}
}

func TestBuildHistoryCoverageNoSessions(t *testing.T) {
	at := time.Date(2026, 6, 4, 10, 0, 0, 0, time.UTC).Format(time.RFC3339)
	runner := fakeRunner{gitLog: record("c1", "", at, "commit") + record("c2", "", at, "commit")}
	cov := BuildHistoryCoverage(context.Background(), runner, "/repo", nil, nil)
	if cov.NoSessionHistoryCommits != 2 || cov.TotalCommits != 2 {
		t.Errorf("no-session-history = %d, total = %d, want 2/2", cov.NoSessionHistoryCommits, cov.TotalCommits)
	}
}

func TestBuildHistoryCoverageGitFailureIsEmpty(t *testing.T) {
	// A runner that always errors stands in for a non-repo target.
	failing := fakeRunner{}
	cov := BuildHistoryCoverage(context.Background(), errRunner{failing}, "/repo", nil, nil)
	if cov.TotalCommits != 0 {
		t.Errorf("total = %d, want 0 on git failure", cov.TotalCommits)
	}
}

type errRunner struct{ fakeRunner }

func (errRunner) Run(ctx context.Context, dir, name string, args ...string) ([]byte, []byte, error) {
	return nil, nil, context.Canceled
}
