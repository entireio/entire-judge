package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestExtractRepoURLs(t *testing.T) {
	cases := map[string][]string{
		"https://github.com/team/proj":                    {"https://github.com/team/proj"},
		"https://github.com/team/proj.git":                {"https://github.com/team/proj"},
		"https://github.com/team/proj/tree/main/web":      {"https://github.com/team/proj"},
		"github.com/Team/Proj":                            {"https://github.com/Team/Proj"},
		"https://proj.vercel.app, https://gitlab.com/a/b": {"https://gitlab.com/a/b"},
		"front: github.com/a/web back: github.com/a/api":  {"https://github.com/a/web", "https://github.com/a/api"},
		"https://github.com/orgs/team/people":             nil,
		"https://github.com/team":                         nil,
		"https://team.github.io/proj":                     nil,
		"https://notgithub.com/a/b":                       nil,
		"see https://github.com/team/proj. thanks":        {"https://github.com/team/proj"},
		"https://github.com/a/b.git.":                     {"https://github.com/a/b"},
		"https://gitlab.com/grp/sub/repo":                 {"https://gitlab.com/grp/sub/repo"},
		"https://gitlab.com/grp/sub/repo/-/tree/main":     {"https://gitlab.com/grp/sub/repo"},
		"https://gitlab.com/grp/repo/blob/main/x.go":      {"https://gitlab.com/grp/repo"},
	}
	for in, want := range cases {
		if got := extractRepoURLs(in); !reflect.DeepEqual(got, want) {
			t.Errorf("extractRepoURLs(%q) = %v, want %v", in, got, want)
		}
	}
}

// A trimmed-down Devpost submissions export: repo links live in the free-form
// "Try it out" column alongside demo sites, one team has two repos, one team
// submitted twice, and one team never linked a repo.
const devpostCSV = "\ufeff" + `Project Title,Submission Url,Project Status,"""Try it out"" Links",Video Demo Link,Submitter Email
Plant Pal,https://devpost.com/software/plant-pal,Submitted (Gallery/Visible),"https://plantpal.vercel.app, https://github.com/greenteam/plant-pal",https://youtu.be/x,a@example.com
Bus Buddy,https://devpost.com/software/bus-buddy,Submitted (Gallery/Visible),"https://github.com/bb/web, https://github.com/bb/api/tree/main",,b@example.com
Plant Pal (resubmit),https://devpost.com/software/plant-pal-2,Draft,https://github.com/GreenTeam/plant-pal.git,,a@example.com
Mystery Box,https://devpost.com/software/mystery-box,Submitted (Gallery/Visible),https://mystery.example.com,,c@example.com
,,,,,
`

func TestParseSubmissionCSVDevpost(t *testing.T) {
	src, err := parseSubmissionCSV(strings.NewReader(strings.TrimPrefix(devpostCSV, "\ufeff")), "")
	if err != nil {
		t.Fatal(err)
	}
	got := dedupeURLs(src.urls)
	want := []string{
		"https://github.com/greenteam/plant-pal",
		"https://github.com/bb/web",
		"https://github.com/bb/api",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("urls = %v, want %v", got, want)
	}
	if len(src.missing) != 1 || !strings.Contains(src.missing[0], "Mystery Box") {
		t.Errorf("missing = %v, want the Mystery Box row", src.missing)
	}
	for _, u := range src.urls {
		if strings.Contains(u, "example.com") || strings.Contains(u, "@") {
			t.Errorf("non-repo data leaked into urls: %q", u)
		}
	}
}

func TestParseSubmissionCSVColumn(t *testing.T) {
	csv := "Project Title,Repo,Notes\nA,https://github.com/a/one,also see https://github.com/a/fork\n"
	src, err := parseSubmissionCSV(strings.NewReader(csv), "repo")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"https://github.com/a/one"}; !reflect.DeepEqual(src.urls, want) {
		t.Errorf("urls = %v, want %v", src.urls, want)
	}
	if _, err := parseSubmissionCSV(strings.NewReader(csv), "nope"); err == nil {
		t.Error("expected an error for an unknown --column")
	}
}

func TestParseSubmissionList(t *testing.T) {
	list := "# finalists\nhttps://github.com/a/one\n\ngit@git.example.edu:team/two.git\n  https://github.com/a/one/  \n"
	got := dedupeURLs(parseSubmissionList(strings.NewReader(list)).urls)
	want := []string{"https://github.com/a/one", "git@git.example.edu:team/two.git"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("urls = %v, want %v", got, want)
	}
}

// A list keeps clone URLs that carry their own transport or credentials, and
// reports lines that are not URLs instead of cloning them. That covers a
// Devpost export saved as .txt and a line that would be parsed as a git option.
func TestParseSubmissionListKeepsTransportAndRejectsJunk(t *testing.T) {
	list := strings.Join([]string{
		"ssh://git@github.com/a/private.git",
		"git@github.com:a/scp.git",
		"https://user@github.com/a/cred",
		"https://github.com/a/deep/tree/main",
		"Project Title,Submission Url,Try it out Links",
		"Mystery Box,https://devpost.com/software/mystery-box,https://mystery.example.com",
		"--upload-pack=touch /tmp/pwned",
		"-oProxyCommand=evil",
	}, "\n")
	src := parseSubmissionList(strings.NewReader(list))
	want := []string{
		"ssh://git@github.com/a/private.git",
		"git@github.com:a/scp.git",
		"https://user@github.com/a/cred",
		"https://github.com/a/deep",
	}
	if !reflect.DeepEqual(src.urls, want) {
		t.Errorf("urls = %v, want %v", src.urls, want)
	}
	if len(src.missing) != 4 {
		t.Errorf("missing = %v, want the header, the no-repo row, and both option-like lines", src.missing)
	}
}

func TestJudgeAddRejectsOptionLikeURL(t *testing.T) {
	rr := &recordingRunner{}
	cmd := NewRootCommand(Options{Version: "test", Runner: rr})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"add", "--dir", t.TempDir(), "--build=false", "--", "--upload-pack=touch /tmp/pwned"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "invalid_repo_url") {
		t.Fatalf("expected invalid_repo_url, got %v", err)
	}
	if len(rr.calls) != 0 {
		t.Errorf("git must not run for an option-like URL, got %v", rr.calls)
	}
}

func TestJudgeAddPassesEndOfOptionsToClone(t *testing.T) {
	rr := &recordingRunner{}
	cmd := NewRootCommand(Options{Version: "test", Runner: rr})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"add", "https://github.com/a/b", "--dir", t.TempDir(), "--build=false"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, c := range rr.calls {
		if len(c) >= 3 && c[1] == "git" && c[2] == "clone" {
			if c[len(c)-3] != "--" {
				t.Errorf("clone args = %v, want -- before the URL", c)
			}
			return
		}
	}
	t.Fatal("no clone call recorded")
}

func TestJudgeAddBatchFailsOnDirCollision(t *testing.T) {
	rr := &recordingRunner{}
	var out, errOut bytes.Buffer
	cmd := NewRootCommand(Options{Version: "test", Runner: rr})
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"add", "https://github.com/a/b", "https://gitlab.com/a/b", "--dir", t.TempDir(), "--build=false"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "1 submission(s) failed") {
		t.Fatalf("expected the second repo to fail, got %v", err)
	}
	if !strings.Contains(errOut.String(), "dir_collision") {
		t.Errorf("expected a dir_collision error:\n%s", errOut.String())
	}
}

func TestJudgeAddColumnRequiresFrom(t *testing.T) {
	cmd := NewRootCommand(Options{Version: "test", Runner: &recordingRunner{}})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"add", "https://github.com/a/b", "--column", "Repo"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "invalid_flags") {
		t.Fatalf("expected invalid_flags, got %v", err)
	}
}

func TestJudgeAddFromDevpostCSV(t *testing.T) {
	dir := t.TempDir()
	csvPath := filepath.Join(dir, "projects.csv")
	if err := os.WriteFile(csvPath, []byte(devpostCSV), 0o644); err != nil {
		t.Fatal(err)
	}
	rr := &recordingRunner{}
	var out, errOut bytes.Buffer
	cmd := NewRootCommand(Options{Version: "test", Runner: rr})
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"add", "--from", csvPath, "--dir", filepath.Join(dir, "subs"), "--build=false"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("add --from: %v\n%s", err, errOut.String())
	}
	var clones []string
	for _, c := range rr.calls {
		if len(c) >= 3 && c[1] == "git" && c[2] == "clone" {
			clones = append(clones, c[len(c)-2])
		}
	}
	if len(clones) != 3 {
		t.Errorf("expected 3 clones, got %v", clones)
	}
	if !strings.Contains(out.String(), "added 3 of 3 submissions") {
		t.Errorf("missing summary:\n%s", out.String())
	}
	if !strings.Contains(errOut.String(), "Mystery Box") {
		t.Errorf("expected a warning for the team with no repo link:\n%s", errOut.String())
	}
}

// failingCloneRunner fails `git clone` for one repo and succeeds otherwise.
type failingCloneRunner struct {
	recordingRunner
	failOn string
}

func (r *failingCloneRunner) Run(ctx context.Context, dir, name string, args ...string) ([]byte, []byte, error) {
	r.recordingRunner.Run(ctx, dir, name, args...)
	if name == "git" && len(args) > 0 && args[0] == "clone" {
		for _, a := range args {
			if a == r.failOn {
				return nil, []byte("repository not found"), errors.New("exit status 128")
			}
		}
	}
	return nil, nil, nil
}

func TestJudgeAddBatchContinuesPastFailures(t *testing.T) {
	rr := &failingCloneRunner{failOn: "https://github.com/a/private"}
	var out, errOut bytes.Buffer
	cmd := NewRootCommand(Options{Version: "test", Runner: rr})
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"add", "https://github.com/a/private", "https://github.com/a/public", "--dir", t.TempDir(), "--build=false"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "1 submission(s) failed") {
		t.Fatalf("expected a summary error for the failed repo, got %v", err)
	}
	if !strings.Contains(out.String(), "added 1 of 2 submissions") {
		t.Errorf("the public repo should still be added:\n%s", out.String())
	}
}

func TestJudgeAddRequiresInput(t *testing.T) {
	cmd := NewRootCommand(Options{Version: "test", Runner: &recordingRunner{}})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"add"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "no_submissions") {
		t.Fatalf("expected no_submissions error, got %v", err)
	}
}
