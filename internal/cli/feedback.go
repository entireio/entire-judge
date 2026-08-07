package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/suhaanthayyil/entire-judge/internal/agent"
	"github.com/suhaanthayyil/entire-judge/internal/brainstore"
	"github.com/suhaanthayyil/entire-judge/internal/judge"
)

type feedbackFlags struct {
	runFlags
	outDir  string
	winners int
}

func newFeedbackCommand(opts Options) *cobra.Command {
	var flags feedbackFlags
	cmd := &cobra.Command{
		Use:   "feedback [path]",
		Short: "Export per-team AI judge feedback markdown (winners and non-winners)",
		Long: `Write one feedback-<slug>.md file per submission from a saved rank JSON
board, a saved run JSON report, or a directory of submission repos (scored live).

Every team gets a file — including hard-gated / excluded submissions and teams
outside the top N. --winners N only changes the greeting header ("Congratulations"
vs "Keep building"); it does not omit anyone.

When path is a saved board from ` + "`rank --json`" + `, no LLM calls are made —
feedback is rendered from the embedded reports.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "."
			if len(args) == 1 {
				path = args[0]
			}
			return runJudgeFeedback(cmd, opts, flags, path)
		},
	}
	registerRunFlags(cmd, &flags.runFlags)
	cmd.Flags().StringVar(&flags.outDir, "out", "./feedback", "Directory to write feedback-*.md files into")
	cmd.Flags().IntVar(&flags.winners, "winners", 3, "Top N ranked teams get a congratulations header (all teams still get a file)")
	return cmd
}

func runJudgeFeedback(cmd *cobra.Command, opts Options, flags feedbackFlags, path string) error {
	if flags.winners < 0 {
		return fmt.Errorf("invalid_winners: --winners must be >= 0")
	}
	outDir := flags.outDir
	if strings.TrimSpace(outDir) == "" {
		outDir = "./feedback"
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("feedback_out_dir: %w", err)
	}

	// Saved board / single report: no re-scoring.
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		return writeFeedbackFromSaved(cmd, path, outDir, flags.winners)
	}

	// Live score a submissions directory (same path as rank).
	if err := agent.RejectForNoEgress(flags.agent); err != nil {
		return err
	}
	hackStart, err := parseHackathonStart(flags.startedAt)
	if err != nil {
		return err
	}
	checkouts, err := discoverSubmissionCheckouts(path)
	if err != nil {
		return err
	}
	if len(checkouts) == 0 {
		return fmt.Errorf("no_submissions: no git checkouts found under %s", path)
	}

	run := agent.DefaultRunner(flags.agent)
	params := judge.Params{Agent: flags.agent, Model: flags.model, Effort: flags.effort, HackathonStart: hackStart}

	var ranked, excluded []judge.RunReport
	results := scoreCheckouts(checkouts, flags.jobs, func(checkout string) scoreResult {
		name := filepath.Base(checkout)
		storage := resolveBrainStorage(cmd.Context(), opts.Runner, opts.Env, checkout)
		if !brainstore.Exists(storage.BrainDir) {
			return scoreResult{name: name, warning: fmt.Sprintf("%s: no exported brain", name)}
		}
		sub, serr := judge.Submit(cmd.Context(), opts.Runner, run, checkout, storage.BrainDir, storage.Key, params, opts.Now())
		if serr != nil {
			return scoreResult{name: name, warning: fmt.Sprintf("%s: %v", name, serr)}
		}
		return scoreResult{name: name, report: sub}
	}, func(done, total int, name string) {
		fmt.Fprintf(cmd.ErrOrStderr(), "scored %d/%d: %s\n", done, total, name)
	})

	var warnings []string
	for i := range results {
		res := results[i]
		if res.warning != "" {
			warnings = append(warnings, res.warning)
			continue
		}
		sub := res.report
		if reason := judge.HardGateReason(sub.Deterministic.TimelineCategory); reason != "" {
			excluded = append(excluded, *sub)
			continue
		}
		ranked = append(ranked, *sub)
	}
	sortReportsByComposite(ranked)

	n, err := writeFeedbackFiles(outDir, ranked, excluded, flags.winners)
	if err != nil {
		return err
	}
	for _, w := range warnings {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", w)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "wrote %d feedback file(s) to %s\n", n, outDir)
	return nil
}

func writeFeedbackFromSaved(cmd *cobra.Command, path, outDir string, winners int) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read_saved_board: %w", err)
	}

	var rank judge.RankReport
	if err := json.Unmarshal(data, &rank); err == nil && rank.Kind == "entire_judge_ranking" {
		var ranked, excluded []judge.RunReport
		for _, e := range rank.Submissions {
			if e.Report != nil {
				ranked = append(ranked, *e.Report)
			}
		}
		for _, e := range rank.Excluded {
			if e.Report != nil {
				excluded = append(excluded, *e.Report)
			}
		}
		if len(ranked) == 0 && len(excluded) == 0 {
			return fmt.Errorf("saved_board_empty: %s has no embedded submission reports", path)
		}
		applyGrades(ranked)
		applyGrades(excluded)
		sortReportsByComposite(ranked)
		n, werr := writeFeedbackFiles(outDir, ranked, excluded, winners)
		if werr != nil {
			return werr
		}
		fmt.Fprintf(cmd.OutOrStdout(), "wrote %d feedback file(s) to %s\n", n, outDir)
		return nil
	}

	var single judge.RunReport
	if err := json.Unmarshal(data, &single); err == nil && single.Kind == "entire_judge_submission" {
		single.GradeProcess, single.GradeSolution, single.Composite = judge.Grades(single.Lenses, single.IntegritySignal)
		n, werr := writeFeedbackFiles(outDir, []judge.RunReport{single}, nil, winners)
		if werr != nil {
			return werr
		}
		fmt.Fprintf(cmd.OutOrStdout(), "wrote %d feedback file(s) to %s\n", n, outDir)
		return nil
	}
	return fmt.Errorf("not_a_saved_board: %s is not a saved entire-judge report", path)
}

func sortReportsByComposite(reports []judge.RunReport) {
	sort.SliceStable(reports, func(i, j int) bool {
		return compositeOf(reports[i]) > compositeOf(reports[j])
	})
}

func writeFeedbackFiles(outDir string, ranked, excluded []judge.RunReport, winners int) (int, error) {
	n := 0
	for i := range ranked {
		rank := i + 1
		body := renderFeedbackMarkdown(&ranked[i], rank, false, "", winners)
		path := filepath.Join(outDir, feedbackFilename(ranked[i].SubmissionID))
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			return n, err
		}
		n++
	}
	for i := range excluded {
		reason := judge.HardGateReason(excluded[i].Deterministic.TimelineCategory)
		body := renderFeedbackMarkdown(&excluded[i], 0, true, reason, winners)
		path := filepath.Join(outDir, feedbackFilename(excluded[i].SubmissionID))
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

var nonSlug = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func feedbackFilename(submissionID string) string {
	slug := strings.TrimPrefix(submissionID, "gh/")
	slug = strings.ReplaceAll(slug, "/", "-")
	slug = nonSlug.ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = "unknown"
	}
	return "feedback-" + slug + ".md"
}

// renderFeedbackMarkdown builds the shareable per-team feedback note from an
// already-scored report. No LLM call — summary/lenses come from the report.
func renderFeedbackMarkdown(r *judge.RunReport, rank int, excluded bool, gateReason string, winners int) string {
	var b strings.Builder
	isWinner := !excluded && rank > 0 && winners > 0 && rank <= winners
	if isWinner {
		b.WriteString("# Congratulations — Entire Judge Feedback\n\n")
	} else {
		b.WriteString("# Keep building — Entire Judge Feedback\n\n")
	}
	b.WriteString(fmt.Sprintf("**Submission:** `%s`\n\n", r.SubmissionID))
	if excluded {
		if gateReason == "" {
			gateReason = "held out of the ranking"
		}
		b.WriteString(fmt.Sprintf("**Status:** excluded from the ranked table (%s)\n\n", gateReason))
	} else if rank > 0 {
		b.WriteString(fmt.Sprintf("**Rank:** #%d\n\n", rank))
	}

	g := func(v *float64) string {
		if v == nil {
			return "n/a"
		}
		return fmt.Sprintf("%.2f", *v)
	}
	if r.Composite != nil {
		b.WriteString(fmt.Sprintf("**Combined:** %s/5 (Grade A process %s · Grade B solution %s)\n\n",
			g(r.Composite), g(r.GradeProcess), g(r.GradeSolution)))
	} else {
		b.WriteString("**Combined:** n/a\n\n")
	}
	if r.IntegrityFlag {
		b.WriteString(fmt.Sprintf("**Integrity flag:** %s\n\n", r.IntegrityReason))
	}

	b.WriteString("## Summary\n\n")
	b.WriteString(judge.SummaryText(r))
	b.WriteString("\n\n")

	// CLI awareness breakdown (business objective: Entire CLI capability awareness).
	m := r.Deterministic
	b.WriteString("## Entire CLI awareness\n\n")
	total := m.EntireSkillInvocations + m.EntireCLIInvocations + m.EntireMCPInvocations
	if total == 0 && m.EntireSignalSessions == 0 {
		b.WriteString("No Entire skill / CLI / MCP usage was detected in session transcripts. ")
		b.WriteString("Using `entire graph`, `entire brain`, `entire sem`, or the Entire agent skill during the event can improve this score.\n\n")
	} else {
		b.WriteString(fmt.Sprintf("- Skill invocations: %d\n", m.EntireSkillInvocations))
		b.WriteString(fmt.Sprintf("- CLI invocations: %d\n", m.EntireCLIInvocations))
		b.WriteString(fmt.Sprintf("- MCP invocations: %d\n", m.EntireMCPInvocations))
		b.WriteString(fmt.Sprintf("- Sessions with signals: %d\n", m.EntireSignalSessions))
		if len(m.DistinctEntireCapabilities) > 0 {
			b.WriteString(fmt.Sprintf("- Capabilities: %s\n", strings.Join(m.DistinctEntireCapabilities, ", ")))
		}
		b.WriteString("\n")
	}

	b.WriteString("## Lens feedback\n\n")
	for i := range r.Lenses {
		lens := r.Lenses[i]
		score := "unscored"
		if lens.Score != nil {
			score = fmt.Sprintf("%.2f/5", *lens.Score)
		}
		b.WriteString(fmt.Sprintf("### %s (%s)\n\n", lens.Lens, score))
		if lens.Verdict != "" {
			b.WriteString(lens.Verdict + "\n\n")
		}
		for _, bullet := range lens.Bullets {
			b.WriteString("- " + bullet + "\n")
		}
		if len(lens.Bullets) > 0 {
			b.WriteString("\n")
		}
		for _, ev := range lens.Evidence {
			b.WriteString(fmt.Sprintf("  - evidence: `%s`\n", ev))
		}
		if len(lens.Evidence) > 0 {
			b.WriteString("\n")
		}
		if !lens.Supported && lens.Score != nil {
			b.WriteString("_Unsupported: no valid evidence anchors._\n\n")
		}
	}

	b.WriteString("---\n\n")
	b.WriteString(r.Disclaimer + "\n")
	return b.String()
}
