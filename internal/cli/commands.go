package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/suhaanthayyil/entire-judge/internal/agent"
	"github.com/suhaanthayyil/entire-judge/internal/brainstore"
	"github.com/suhaanthayyil/entire-judge/internal/judge"
	"github.com/suhaanthayyil/entire-judge/internal/tui"
)

// ---- judge run ----

func newRunCommand(opts Options) *cobra.Command {
	var flags runFlags
	cmd := &cobra.Command{
		Use:   "run [repo]",
		Short: "Score a single submission repo with the judge lenses",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runJudgeRun(cmd, opts, flags, resolveRepoDir(opts.Env, args))
		},
	}
	registerRunFlags(cmd, &flags)
	return cmd
}

func runJudgeRun(cmd *cobra.Command, opts Options, flags runFlags, repoDir string) error {
	if err := agent.RejectForNoEgress(flags.agent); err != nil {
		return err
	}
	hackStart, err := parseHackathonStart(flags.startedAt)
	if err != nil {
		return err
	}

	storage := resolveBrainStorage(cmd.Context(), opts.Runner, opts.Env, repoDir)
	if !brainstore.Exists(storage.BrainDir) {
		return fmt.Errorf("brain_missing: no exported brain at %s; run `entire brain refresh` first", storage.BrainDir)
	}

	run := agent.DefaultRunner(flags.agent)
	params := judge.Params{Agent: flags.agent, Model: flags.model, Effort: flags.effort, HackathonStart: hackStart}
	report, err := judge.Submit(cmd.Context(), opts.Runner, run, repoDir, storage.BrainDir, storage.Key, params, opts.Now())
	if err != nil {
		return err
	}

	if flags.json {
		return writeJSON(cmd, report)
	}
	if flags.plain || !commandOutputIsTTY(cmd) {
		printRunReport(cmd, report)
		return nil
	}
	return runTUI(cmd, []judge.RunReport{*report}, report.Run)
}

// ---- judge rank ----

func newRankCommand(opts Options) *cobra.Command {
	var flags runFlags
	cmd := &cobra.Command{
		Use:   "rank [dir]",
		Short: "Rank a directory of submission repos into an advisory table",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			return runJudgeRank(cmd, opts, flags, dir)
		},
	}
	registerRunFlags(cmd, &flags)
	return cmd
}

func runJudgeRank(cmd *cobra.Command, opts Options, flags runFlags, dir string) error {
	if err := agent.RejectForNoEgress(flags.agent); err != nil {
		return err
	}
	hackStart, err := parseHackathonStart(flags.startedAt)
	if err != nil {
		return err
	}
	checkouts, err := discoverSubmissionCheckouts(dir)
	if err != nil {
		return err
	}
	if len(checkouts) == 0 {
		return fmt.Errorf("no_submissions: no git checkouts found under %s (point rank at a directory of submission repos)", dir)
	}

	run := agent.DefaultRunner(flags.agent)
	params := judge.Params{Agent: flags.agent, Model: flags.model, Effort: flags.effort, HackathonStart: hackStart}

	report := &judge.RankReport{
		SchemaVersion: judge.SchemaVersion,
		Kind:          "entire_judge_ranking",
		Advisory:      true,
		Disclaimer:    judge.Disclaimer,
		GeneratedAt:   opts.Now().UTC(),
		Run: judge.RunMetadata{
			Agent:             flags.agent,
			Model:             flags.model,
			Effort:            flags.effort,
			PromptFingerprint: judge.PromptFingerprint(flags.agent, flags.model, flags.effort),
			LLMStatus:         "ok",
		},
	}
	if !hackStart.IsZero() {
		v := hackStart.UTC()
		report.Run.HackathonStartedAt = &v
	}

	var reports []judge.RunReport
	for _, checkout := range checkouts {
		// Resolve each submission's brain exactly as `run` does, so the
		// commit-timeline lens reads the real checkout (not a brain-dir parent).
		storage := resolveBrainStorage(cmd.Context(), opts.Runner, opts.Env, checkout)
		if !brainstore.Exists(storage.BrainDir) {
			report.Warnings = append(report.Warnings, fmt.Sprintf("%s: no exported brain (run `entire brain refresh` first)", filepath.Base(checkout)))
			continue
		}
		sub, serr := judge.Submit(cmd.Context(), opts.Runner, run, checkout, storage.BrainDir, storage.Key, params, opts.Now())
		if serr != nil {
			report.Warnings = append(report.Warnings, fmt.Sprintf("%s: %v", filepath.Base(checkout), serr))
			continue
		}
		// Hard gates: degenerate timelines are excluded from the ordered table
		// rather than silently averaged into it.
		if reason := judge.HardGateReason(sub.Deterministic.TimelineCategory); reason != "" {
			report.Excluded = append(report.Excluded, judge.RankExcluded{
				SubmissionID: sub.SubmissionID,
				BrainPath:    sub.BrainPath,
				Reason:       reason,
			})
			continue
		}
		reports = append(reports, *sub)
	}

	sort.SliceStable(reports, func(i, j int) bool {
		return compositeOf(reports[i]) > compositeOf(reports[j])
	})
	for i := range reports {
		r := reports[i]
		report.Submissions = append(report.Submissions, judge.RankEntry{
			Rank:         i + 1,
			SubmissionID: r.SubmissionID,
			BrainPath:    r.BrainPath,
			Composite:    r.Composite,
			Flags:        r.Flags,
			Report:       &reports[i],
		})
	}

	report.FairnessNotes = []string{
		judge.Disclaimer,
		"Authenticity and effort are deterministic; prompting and idea/plan/execution are LLM-scored and may vary by agent/model.",
		"Submissions that predate the event or lack session history are excluded from the ordered table, not averaged into it.",
	}

	if flags.json {
		return writeJSON(cmd, report)
	}
	if flags.plain || !commandOutputIsTTY(cmd) {
		printRankReport(cmd, report)
		return nil
	}
	return runTUI(cmd, reports, report.Run)
}

// ---- judge watch ----

func newWatchCommand(opts Options) *cobra.Command {
	var flags runFlags
	cmd := &cobra.Command{
		Use:   "watch [dir]",
		Short: "Browse ranked submissions in an interactive view",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			// watch is rank with the TUI forced on a TTY; --json/--plain still
			// short-circuit to non-interactive output.
			return runJudgeRank(cmd, opts, flags, dir)
		},
	}
	registerRunFlags(cmd, &flags)
	return cmd
}

// ---- discovery + helpers ----

func compositeOf(r judge.RunReport) float64 {
	if r.Composite == nil {
		return -1
	}
	return *r.Composite
}

// isGitCheckout reports whether dir is a git working tree (a .git directory or,
// for worktrees/submodules, a .git file).
func isGitCheckout(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// discoverSubmissionCheckouts finds the submission repositories to rank under dir.
// A submission is a git checkout, so the commit-timeline lens has a real repo to
// read and each brain resolves the same way `run` resolves it. If dir is itself a
// checkout it is the sole submission; otherwise each immediate subdirectory that
// is a checkout is a submission.
func discoverSubmissionCheckouts(dir string) ([]string, error) {
	if isGitCheckout(dir) {
		abs, _ := filepath.Abs(dir)
		return []string{abs}, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("rank_dir_missing: %s does not exist", dir)
		}
		return nil, err
	}
	var dirs []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		candidate := filepath.Join(dir, entry.Name())
		if isGitCheckout(candidate) {
			abs, _ := filepath.Abs(candidate)
			dirs = append(dirs, abs)
		}
	}
	sort.Strings(dirs)
	return dirs, nil
}

func writeJSON(cmd *cobra.Command, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), string(encoded))
	return err
}

// ---- text rendering ----

func printRunReport(cmd *cobra.Command, report *judge.RunReport) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Submission: %s\n", report.SubmissionID)
	fmt.Fprintf(out, "Brain: %s\n", report.BrainPath)
	if report.Composite != nil {
		fmt.Fprintf(out, "Composite: %.2f/5\n", *report.Composite)
	} else {
		fmt.Fprintf(out, "Composite: n/a\n")
	}
	if len(report.Flags) > 0 {
		fmt.Fprintf(out, "Flags: %s\n", strings.Join(report.Flags, ", "))
	}
	fmt.Fprintf(out, "Agent: %s", report.Run.Agent)
	if report.Run.Model != "" {
		fmt.Fprintf(out, " (%s)", report.Run.Model)
	}
	fmt.Fprintf(out, "  LLM: %s\n\n", report.Run.LLMStatus)

	for _, lens := range report.Lenses {
		fmt.Fprintf(out, "%s %s\n", tui.ScoreBar(lens.Score), lens.Lens)
		if lens.Verdict != "" {
			fmt.Fprintf(out, "  %s\n", lens.Verdict)
		}
		for _, b := range lens.Bullets {
			fmt.Fprintf(out, "  - %s\n", b)
		}
		for _, e := range lens.Evidence {
			fmt.Fprintf(out, "  [evidence] %s\n", e)
		}
		if !lens.Supported && lens.Score != nil {
			fmt.Fprintf(out, "  (unsupported: no valid evidence anchors)\n")
		}
		fmt.Fprintln(out)
	}
	for _, w := range report.Warnings {
		fmt.Fprintf(out, "warning: %s\n", w)
	}
	fmt.Fprintf(out, "\n%s\n", report.Disclaimer)
}

func printRankReport(cmd *cobra.Command, report *judge.RankReport) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Ranking %d submission(s)\n\n", len(report.Submissions))
	fmt.Fprintf(out, "%-4s %-32s %-10s %s\n", "rank", "submission", "composite", "flags")
	for _, entry := range report.Submissions {
		composite := "n/a"
		if entry.Composite != nil {
			composite = fmt.Sprintf("%.2f", *entry.Composite)
		}
		fmt.Fprintf(out, "%-4d %-32s %-10s %s\n", entry.Rank, truncateString(entry.SubmissionID, 32), composite, strings.Join(entry.Flags, ","))
	}
	if len(report.Excluded) > 0 {
		fmt.Fprintf(out, "\nExcluded (hard gate):\n")
		for _, ex := range report.Excluded {
			fmt.Fprintf(out, "  %-32s %s\n", truncateString(ex.SubmissionID, 32), ex.Reason)
		}
	}
	if len(report.FairnessNotes) > 0 {
		fmt.Fprintf(out, "\nFairness:\n")
		for _, note := range report.FairnessNotes {
			fmt.Fprintf(out, "  - %s\n", note)
		}
	}
	for _, w := range report.Warnings {
		fmt.Fprintf(out, "warning: %s\n", w)
	}
}

func truncateString(value string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(value) <= max {
		return value
	}
	if max <= 3 {
		return value[:max]
	}
	return value[:max-3] + "..."
}

// ---- TUI ----

// runTUI launches the interactive submission browser. On a non-TTY it falls back
// to the rendered text summary so the command is never silently inert.
func runTUI(cmd *cobra.Command, reports []judge.RunReport, meta judge.RunMetadata) error {
	if !commandOutputIsTTY(cmd) {
		return fallbackText(cmd, reports)
	}
	model := tui.NewModel(reports, meta)
	program := tea.NewProgram(model, tea.WithAltScreen(), tea.WithOutput(cmd.OutOrStdout()))
	_, err := program.Run()
	return err
}

func fallbackText(cmd *cobra.Command, reports []judge.RunReport) error {
	if len(reports) == 1 {
		printRunReport(cmd, &reports[0])
		return nil
	}
	for i := range reports {
		printRunReport(cmd, &reports[i])
		fmt.Fprintln(cmd.OutOrStdout(), strings.Repeat("-", 60))
	}
	return nil
}
