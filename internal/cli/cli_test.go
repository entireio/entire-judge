package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/entireio/entire-judge/internal/gitutil"
)

type fakeRunner struct {
	origin string
	err    bool
}

func (f fakeRunner) Run(ctx context.Context, dir, name string, args ...string) ([]byte, []byte, error) {
	if f.err {
		return nil, nil, context.Canceled
	}
	if name == "git" && len(args) >= 2 && args[0] == "remote" && args[1] == "get-url" {
		return []byte(f.origin + "\n"), nil, nil
	}
	return nil, nil, context.Canceled
}

func TestRepoKeyFromRemote(t *testing.T) {
	cases := map[string]string{
		"https://github.com/team/project.git": "gh/team/project",
		"git@github.com:team/project.git":     "gh/team/project",
		"ssh://git@github.com/team/project":   "gh/team/project",
		"https://gitlab.com/group/sub/repo":   "gitlab.com/group/sub/repo",
	}
	for remote, want := range cases {
		got, ok := repoKeyFromRemote(remote)
		if !ok {
			t.Errorf("repoKeyFromRemote(%q) not ok", remote)
			continue
		}
		if got != want {
			t.Errorf("repoKeyFromRemote(%q) = %q, want %q", remote, got, want)
		}
	}
}

func TestDeriveRepoKeyFallsBackToLocal(t *testing.T) {
	key := deriveRepoKey(context.Background(), fakeRunner{err: true}, "/tmp/my-cool-repo")
	if !strings.HasPrefix(key, "local/") {
		t.Errorf("expected local fallback key, got %q", key)
	}
}

func TestDeriveRepoKeyFromOrigin(t *testing.T) {
	key := deriveRepoKey(context.Background(), fakeRunner{origin: "https://github.com/team/project.git"}, "/tmp/repo")
	if key != "gh/team/project" {
		t.Errorf("derived key = %q, want gh/team/project", key)
	}
}

func TestVersionCommand(t *testing.T) {
	var buf bytes.Buffer
	cmd := NewRootCommand(Options{Version: "1.2.3", Runner: gitutil.ExecRunner{}})
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"version"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("version: %v", err)
	}
	if strings.TrimSpace(buf.String()) != "1.2.3" {
		t.Errorf("version output = %q, want 1.2.3", buf.String())
	}
}

func TestRunRejectsMissingBrain(t *testing.T) {
	var buf bytes.Buffer
	cmd := NewRootCommand(Options{Version: "dev", Env: EntireEnv{PluginDataDir: t.TempDir()}, Runner: fakeRunner{err: true}})
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"run", t.TempDir(), "--plain"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "brain_missing") {
		t.Errorf("expected brain_missing error, got %v", err)
	}
}

func TestParseHackathonStart(t *testing.T) {
	if _, err := parseHackathonStart(""); err != nil {
		t.Errorf("empty start should not error: %v", err)
	}
	if _, err := parseHackathonStart("2026-01-05T17:00:00Z"); err != nil {
		t.Errorf("valid RFC3339 should parse: %v", err)
	}
	if _, err := parseHackathonStart("not-a-time"); err == nil {
		t.Errorf("invalid start should error")
	}
}
