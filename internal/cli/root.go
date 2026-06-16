package cli

import (
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"github.com/suhaanthayyil/entire-judge/internal/gitutil"
)

// Options plumbs version, environment, the git runner, and a clock through the
// command tree so tests can inject fakes.
type Options struct {
	Version string
	Env     EntireEnv
	Runner  gitutil.CommandRunner
	Now     func() time.Time
}

// EnvFromOS reads the Entire-supplied environment variables from the process.
func EnvFromOS() EntireEnv {
	return EntireEnv{
		CLIVersion:    os.Getenv(envCLIVersion),
		RepoRoot:      os.Getenv(envRepoRoot),
		PluginDataDir: os.Getenv(envPluginDataDir),
	}
}

// Execute runs the entire-judge root command with the real process environment.
// It mirrors entire-sem's cmd/main entry point: cli.Execute(version, args).
func Execute(version string, args []string) error {
	cmd := NewRootCommand(Options{
		Version: version,
		Env:     EnvFromOS(),
	})
	cmd.SetArgs(args)
	return cmd.Execute()
}

// NewRootCommand builds the entire-judge command tree.
func NewRootCommand(opts Options) *cobra.Command {
	if opts.Version == "" {
		opts.Version = "dev"
	}
	if opts.Runner == nil {
		opts.Runner = gitutil.ExecRunner{}
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}

	cmd := &cobra.Command{
		Use:           "entire-judge",
		Short:         "Score hackathon submissions from each team's Entire brain",
		SilenceUsage:  true,
		SilenceErrors: true,
		Long: `entire-judge is an external-command plugin that helps a hackathon jury
evaluate submissions using each team's local Entire brain (exported sessions,
durable facts, and commit history).

It runs deterministic metrics plus optional LLM "judge lenses" that produce a
score, a verdict, and evidence-anchored bullets per submission, and ranks a
directory of brains into an advisory ordered table. Every score is advisory —
it informs the jury's judgment, it does not replace it.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	cmd.CompletionOptions.DisableDefaultCmd = true

	cmd.AddCommand(newAddCommand(opts))
	cmd.AddCommand(newRunCommand(opts))
	cmd.AddCommand(newRankCommand(opts))
	cmd.AddCommand(newWatchCommand(opts))
	cmd.AddCommand(newVersionCommand(opts.Version))

	return cmd
}

func newVersionCommand(version string) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print plugin version",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := io.WriteString(cmd.OutOrStdout(), version+"\n")
			return err
		},
	}
}

// resolveRepoDir resolves the target repository path: an explicit argument wins,
// then ENTIRE_REPO_ROOT, then the current directory.
func resolveRepoDir(env EntireEnv, args []string) string {
	if len(args) == 1 && args[0] != "" {
		return args[0]
	}
	if env.RepoRoot != "" {
		return env.RepoRoot
	}
	return "."
}

// runFlags carries the shared per-submission flags.
type runFlags struct {
	agent     string
	model     string
	effort    string
	startedAt string
	theme     string
	json      bool
	plain     bool
}

func registerRunFlags(cmd *cobra.Command, flags *runFlags) {
	cmd.Flags().StringVar(&flags.agent, "agent", "claude-code", "Lens agent: claude-code, codex, ollama, or command (no-egress honored)")
	cmd.Flags().StringVar(&flags.model, "model", "", "Override the agent model for the LLM lenses")
	cmd.Flags().StringVar(&flags.effort, "effort", "", "Override the reasoning effort for the LLM lenses (e.g. low)")
	cmd.Flags().StringVar(&flags.startedAt, "started-at", "", "Hackathon start time (RFC3339); enables timeline placement")
	cmd.Flags().StringVar(&flags.theme, "theme", "", "TUI color theme: default, catppuccin, gruvbox, tokyonight (or ENTIRE_JUDGE_THEME)")
	cmd.Flags().BoolVar(&flags.json, "json", false, "Emit machine-readable JSON")
	cmd.Flags().BoolVar(&flags.plain, "plain", false, "Emit the rendered text summary (never the TUI)")
}

// parseHackathonStart parses the optional RFC3339 start time. An empty string
// yields the zero time (no boundary).
func parseHackathonStart(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, errors.New("invalid_started_at: --started-at must be RFC3339: " + err.Error())
	}
	return t.UTC(), nil
}

// outputIsTTY reports whether the writer is an interactive terminal.
func outputIsTTY(w io.Writer) bool {
	file, ok := w.(*os.File)
	if !ok {
		return false
	}
	return isatty.IsTerminal(file.Fd())
}

func commandOutputIsTTY(cmd *cobra.Command) bool {
	return outputIsTTY(cmd.OutOrStdout())
}
