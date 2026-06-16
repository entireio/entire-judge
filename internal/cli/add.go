package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

type addFlags struct {
	dir             string
	entireBinary    string
	build           bool
	checkpointLimit int
}

func newAddCommand(opts Options) *cobra.Command {
	var flags addFlags
	cmd := &cobra.Command{
		Use:   "add <repo-url>",
		Short: "Clone a submission repo, fetch its Entire history, and build its brain",
		Long: `add readies a submission for judging: it clones the repository, fetches its
Entire checkpoint history (refs/heads/entire/*), and builds the brain by shelling
out to ` + "`entire brain refresh sessions`" + ` (configurable via --entire-binary).
Afterward the submission is scorable with ` + "`entire judge run`" + ` / ` + "`rank`" + `.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runJudgeAdd(cmd, opts, flags, args[0])
		},
	}
	cmd.Flags().StringVar(&flags.dir, "dir", ".", "Directory to clone the submission into")
	cmd.Flags().StringVar(&flags.entireBinary, "entire-binary", "entire", "Entire CLI binary used to build the brain (`<bin> brain refresh sessions`)")
	cmd.Flags().BoolVar(&flags.build, "build", true, "Build the brain after cloning (use --build=false to clone only)")
	cmd.Flags().IntVar(&flags.checkpointLimit, "checkpoint-limit", 0, "Limit checkpoints scanned when building the brain (0 = no limit)")
	return cmd
}

func runJudgeAdd(cmd *cobra.Command, opts Options, flags addFlags, repoURL string) error {
	ctx := cmd.Context()
	out := cmd.OutOrStdout()
	errOut := cmd.ErrOrStderr()

	name := submissionDirName(repoURL)
	if name == "" {
		return fmt.Errorf("invalid_repo_url: cannot derive a directory name from %q", repoURL)
	}
	dest := filepath.Join(flags.dir, name)

	// 1. Clone (skip if the checkout already exists).
	if isGitCheckout(dest) {
		fmt.Fprintf(out, "clone: %s already present, skipping\n", dest)
	} else {
		fmt.Fprintf(out, "clone: %s -> %s\n", repoURL, dest)
		if _, stderr, err := opts.Runner.Run(ctx, "", "git", "clone", "--quiet", repoURL, dest); err != nil {
			return fmt.Errorf("clone_failed: %v: %s", err, strings.TrimSpace(string(stderr)))
		}
	}

	// 2. Fetch the Entire checkpoint history (best-effort: a repo may have none).
	if _, stderr, err := opts.Runner.Run(ctx, dest, "git", "fetch", "--quiet", "origin", "refs/heads/entire/*:refs/heads/entire/*"); err != nil {
		fmt.Fprintf(errOut, "warning: could not fetch Entire history (%v): %s\n", err, strings.TrimSpace(string(stderr)))
	}

	// 3. Build the brain.
	if !flags.build {
		fmt.Fprintf(out, "added %s at %s (clone only; build with `%s brain refresh sessions`)\n", name, dest, flags.entireBinary)
		return nil
	}
	absDest, _ := filepath.Abs(dest)
	if err := buildSubmissionBrain(ctx, flags, absDest, opts.Env.PluginDataDir); err != nil {
		fmt.Fprintf(errOut, "warning: brain build failed (%v)\nbuild it manually:\n  ENTIRE_REPO_ROOT=%s %s brain refresh sessions\n", err, absDest, flags.entireBinary)
		return nil
	}
	fmt.Fprintf(out, "added %s at %s — brain built. Score it with `entire judge run %s` (or `rank %s`).\n", name, dest, dest, flags.dir)
	return nil
}

// buildSubmissionBrain shells out to the Entire CLI to build the submission's
// brain with ENTIRE_REPO_ROOT pointed at the checkout, exactly as
// `entire brain refresh sessions` expects. It inherits the process env (so the
// host-provided ENTIRE_PLUGIN_DATA_DIR carries through) and overrides the repo
// root and, when known, the plugin data dir.
func buildSubmissionBrain(ctx context.Context, flags addFlags, repoDir, dataDir string) error {
	// The host CLI exposes the builder as `entire brain refresh sessions`; the
	// standalone brain binary exposes it directly as `entire-brain refresh
	// sessions`. Detect which by the binary's base name so both work.
	args := []string{"brain", "refresh", "sessions"}
	if base := filepath.Base(flags.entireBinary); base == "entire-brain" || strings.HasPrefix(base, "entire-brain") {
		args = []string{"refresh", "sessions"}
	}
	if flags.checkpointLimit > 0 {
		args = append(args, "--checkpoint-limit", strconv.Itoa(flags.checkpointLimit))
	}
	c := exec.CommandContext(ctx, flags.entireBinary, args...)
	c.Dir = repoDir
	c.Env = append(os.Environ(), "ENTIRE_REPO_ROOT="+repoDir)
	if dataDir != "" {
		c.Env = append(c.Env, "ENTIRE_PLUGIN_DATA_DIR="+dataDir)
	}
	if combined, err := c.CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(combined)))
	}
	return nil
}

// submissionDirName derives a local directory name from a repo URL, preferring
// "<owner>_<repo>" (matching the judge-demo layout). It handles https and
// scp-like (git@host:owner/repo) URLs and strips a trailing .git.
func submissionDirName(repoURL string) string {
	u := strings.TrimSuffix(strings.TrimSpace(repoURL), "/")
	u = strings.TrimSuffix(u, ".git")
	if i := strings.Index(u, "://"); i >= 0 {
		rest := u[i+3:]
		if s := strings.IndexByte(rest, '/'); s >= 0 {
			u = rest[s+1:] // drop host
		} else {
			u = rest
		}
	} else if i := strings.LastIndex(u, ":"); i >= 0 {
		u = u[i+1:] // scp-like git@host:owner/repo
	}
	parts := make([]string, 0, 3)
	for _, p := range strings.Split(u, "/") {
		if p != "" {
			parts = append(parts, p)
		}
	}
	switch {
	case len(parts) >= 2:
		return parts[len(parts)-2] + "_" + parts[len(parts)-1]
	case len(parts) == 1:
		return parts[0]
	default:
		return ""
	}
}
