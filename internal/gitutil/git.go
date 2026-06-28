// Package gitutil runs git and turns its commit log into the history-coverage
// tally the judge's authenticity lens depends on. It mirrors entire-sem's exec
// style (an injectable CommandRunner so tests need no real repo) and reimplements
// the brain's seed-coverage classifier with a self-contained data model.
package gitutil

import (
	"bytes"
	"context"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

const (
	checkpointTrailerKey  = "Entire-Checkpoint"
	gitLogRecordSeparator = "\x1e"
	gitLogFieldSeparator  = "\x00"
)

var checkpointTrailerRegex = regexp.MustCompile(checkpointTrailerKey + `:\s*([a-f0-9]{12})(?:\s|$)`)

// CommandRunner runs external commands. Tests replace it so coverage behavior
// can be verified without a real git checkout.
type CommandRunner interface {
	Run(ctx context.Context, dir, name string, args ...string) ([]byte, []byte, error)
}

// ExecRunner is the production CommandRunner backed by os/exec.
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, dir, name string, args ...string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

// Commit is one parsed commit from `git log`, annotated with its coverage class
// relative to the brain's session history.
type Commit struct {
	Hash        string    `json:"hash"`
	CommittedAt time.Time `json:"committed_at"`
	AuthorName  string    `json:"author_name,omitempty"`
	AuthorEmail string    `json:"author_email,omitempty"`
	Subject     string    `json:"subject,omitempty"`
	Coverage    string    `json:"coverage"`
	Checkpoints []string  `json:"checkpoints,omitempty"`
	Merge       bool      `json:"merge,omitempty"`
	Parents     []string  `json:"parents,omitempty"`
	BodyExcerpt string    `json:"body_excerpt,omitempty"`
}

// HistoryCoverage is the tally of how a repo's commit history relates to its
// captured session history. It is the deterministic backbone of the authenticity
// lens.
type HistoryCoverage struct {
	TotalCommits            int        `json:"total_commits"`
	PreSessionCommits       int        `json:"pre_session_commits"`
	CoveredCommits          int        `json:"covered_commits"`
	MissingSessionCommits   int        `json:"missing_session_commits"`
	NoSessionHistoryCommits int        `json:"no_session_history_commits"`
	MergeCommits            int        `json:"merge_commits"`
	OldestSessionAt         *time.Time `json:"oldest_session_at,omitempty"`
	ExportedCheckpoints     int        `json:"exported_checkpoints"`
	UncoveredCommits        []Commit   `json:"uncovered_commits,omitempty"`
	GeneratedFrom           string     `json:"generated_from"`
}

// BuildHistoryCoverage runs `git log --reverse` in repoDir and classifies every
// commit against the brain's oldest session timestamp and exported checkpoint
// set. A git failure yields an empty (zero-commit) coverage rather than an
// error, so a non-repo target degrades gracefully.
func BuildHistoryCoverage(ctx context.Context, runner CommandRunner, repoDir string, oldestSession *time.Time, exportedCheckpoints map[string]struct{}) *HistoryCoverage {
	coverage := &HistoryCoverage{
		OldestSessionAt:     oldestSession,
		ExportedCheckpoints: len(exportedCheckpoints),
		GeneratedFrom:       "git log",
	}
	stdout, _, err := runner.Run(ctx, repoDir, "git", "log", "--reverse", "--format=%H%x00%P%x00%aI%x00%an%x00%ae%x00%B%x1e")
	if err != nil {
		return coverage
	}
	commits := ParseGitLog(stdout)
	for i := range commits {
		commit := commits[i]
		classifyCommitCoverage(&commit, oldestSession)
		coverage.TotalCommits++
		if commit.Merge {
			coverage.MergeCommits++
		}
		switch commit.Coverage {
		case "pre_session":
			coverage.PreSessionCommits++
		case "covered":
			coverage.CoveredCommits++
		case "missing_session":
			coverage.MissingSessionCommits++
			coverage.UncoveredCommits = append(coverage.UncoveredCommits, commit)
		case "no_session_history":
			coverage.NoSessionHistoryCommits++
			coverage.UncoveredCommits = append(coverage.UncoveredCommits, commit)
		}
	}
	return coverage
}

// ParseGitLog parses the record-separated log produced by the
// %H%x00%P%x00%aI%x00%an%x00%ae%x00%B%x1e format string.
func ParseGitLog(data []byte) []Commit {
	var commits []Commit
	for _, raw := range strings.Split(string(data), gitLogRecordSeparator) {
		raw = strings.Trim(raw, "\n")
		if strings.TrimSpace(raw) == "" {
			continue
		}
		fields := strings.SplitN(raw, gitLogFieldSeparator, 6)
		if len(fields) < 6 {
			continue
		}
		committedAt, err := time.Parse(time.RFC3339, strings.TrimSpace(fields[2]))
		if err != nil {
			continue
		}
		body := strings.TrimSpace(fields[5])
		parents := strings.Fields(strings.TrimSpace(fields[1]))
		commits = append(commits, Commit{
			Hash:        strings.TrimSpace(fields[0]),
			Parents:     parents,
			CommittedAt: committedAt.UTC(),
			AuthorName:  strings.TrimSpace(fields[3]),
			AuthorEmail: strings.TrimSpace(fields[4]),
			Subject:     firstCommitSubject(body),
			Checkpoints: uniqueStrings(checkpointTrailers(body)),
			Merge:       len(parents) > 1,
			BodyExcerpt: commitBodyExcerpt(body),
		})
	}
	return commits
}

// classifyCommitCoverage labels a commit by how it relates to the brain's session
// history. A commit carrying an Entire checkpoint trailer was made under an Entire
// session, so it is covered — the brain need not have exported that exact
// checkpoint (it records only one latest-checkpoint id per session, not every
// intermediate one, so matching on the exported set alone would mislabel genuine
// in-session commits as uncovered).
func classifyCommitCoverage(commit *Commit, oldestSession *time.Time) {
	if oldestSession == nil {
		commit.Coverage = "no_session_history"
		return
	}
	if commit.CommittedAt.Before(*oldestSession) {
		commit.Coverage = "pre_session"
		return
	}
	if len(commit.Checkpoints) > 0 {
		commit.Coverage = "covered"
		return
	}
	commit.Coverage = "missing_session"
}

func checkpointTrailers(body string) []string {
	var checkpoints []string
	for _, match := range checkpointTrailerRegex.FindAllStringSubmatch(body, -1) {
		if len(match) > 1 {
			checkpoints = append(checkpoints, match[1])
		}
	}
	return checkpoints
}

func firstCommitSubject(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func commitBodyExcerpt(body string) string {
	var lines []string
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, checkpointTrailerKey+":") {
			continue
		}
		lines = append(lines, line)
		if len(strings.Join(lines, " ")) >= 240 {
			break
		}
	}
	excerpt := strings.Join(lines, " ")
	if len(excerpt) > 240 {
		excerpt = excerpt[:240]
	}
	return excerpt
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, v := range values {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// RemoteOriginURL returns the origin remote URL, or "" when there is none.
func RemoteOriginURL(ctx context.Context, runner CommandRunner, repoDir string) string {
	stdout, _, err := runner.Run(ctx, repoDir, "git", "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(stdout))
}
