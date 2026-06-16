package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// recordingRunner records every command it is asked to run and reports success.
type recordingRunner struct{ calls [][]string }

func (r *recordingRunner) Run(ctx context.Context, dir, name string, args ...string) ([]byte, []byte, error) {
	r.calls = append(r.calls, append([]string{dir, name}, args...))
	return nil, nil, nil
}

func TestSubmissionDirName(t *testing.T) {
	cases := map[string]string{
		"https://github.com/owner/repo.git": "owner_repo",
		"https://github.com/owner/repo":     "owner_repo",
		"git@github.com:owner/repo.git":     "owner_repo",
		"https://entire.io/gh/o/r/":         "o_r",
		"solo":                              "solo",
	}
	for in, want := range cases {
		if got := submissionDirName(in); got != want {
			t.Errorf("submissionDirName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestJudgeAddClonesAndFetches(t *testing.T) {
	rr := &recordingRunner{}
	var buf bytes.Buffer
	cmd := NewRootCommand(Options{Version: "test", Runner: rr})
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"add", "https://github.com/team/proj.git", "--dir", t.TempDir(), "--build=false"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("add: %v", err)
	}

	var sawClone, sawFetch bool
	for _, c := range rr.calls {
		if len(c) >= 3 && c[1] == "git" && c[2] == "clone" {
			sawClone = true
		}
		if len(c) >= 3 && c[1] == "git" && c[2] == "fetch" {
			sawFetch = true
		}
	}
	if !sawClone || !sawFetch {
		t.Errorf("expected git clone and git fetch; calls=%v", rr.calls)
	}
	if !strings.Contains(buf.String(), "team_proj") {
		t.Errorf("output missing derived submission name:\n%s", buf.String())
	}
}
