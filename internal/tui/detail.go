package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/suhaanthayyil/entire-judge/internal/judge"
)

// lensComponent labels which component grade each scored lens feeds (Grade A
// process vs Grade B solution), shown next to the lens in the Score section.
var lensComponent = map[string]string{
	judge.LensAuthenticity: "A · process",
	judge.LensPrompting:    "A · process",
	judge.LensEffort:       "A · process",
	judge.LensCLIAwareness: "A · process",
	judge.LensIntegrity:    "A · process",
	judge.LensOutcome:      "B · solution",
}

// renderDetail renders one submission's page: Header, Score, Summary, Findings.
// width is the content width inside the detail viewport.
func renderDetail(th Theme, r *judge.RunReport, meta judge.RunMetadata, excluded bool, rank, width int) string {
	if width < 20 {
		width = 20
	}
	wrap := lipgloss.NewStyle().Width(width).Foreground(th.Text)
	var b strings.Builder

	// ---- Header ----
	title := ""
	if rank > 0 {
		rc := lipgloss.NewStyle().Foreground(th.rankColor(rank)).Bold(true)
		if m := medal(rank); m != "" {
			title += rc.Render(m+" #"+fmt.Sprint(rank)) + " "
		} else {
			title += rc.Render("#"+fmt.Sprint(rank)) + " "
		}
	}
	title += th.titleStyle().Render(r.SubmissionID)
	b.WriteString(title)
	b.WriteString("\n")

	// Clickable links (cmd/ctrl-click in supporting terminals). Rendered raw —
	// no width-constrained style — because OSC 8 escapes are zero-width.
	if owner, repo := ownerRepo(r.SubmissionID); owner != "" {
		gh := osc8("https://github.com/"+owner+"/"+repo, th.linkStyle().Render("GitHub ↗"))
		eio := osc8("https://entire.io/gh/"+owner+"/"+repo+"/commits", th.linkStyle().Render("entire.io ↗"))
		b.WriteString(gh + "   " + eio + "\n")
	}

	b.WriteString(th.dimStyle().Render(truncate(r.RepoDir, width)))
	b.WriteString("\n")
	meta1 := fmt.Sprintf("agent %s", meta.Agent)
	if meta.Model != "" {
		meta1 += " (" + meta.Model + ")"
	}
	meta1 += " · LLM " + r.Run.LLMStatus
	if r.Run.HackathonStartedAt != nil {
		meta1 += " · start " + r.Run.HackathonStartedAt.Format("2006-01-02 15:04Z")
	}
	b.WriteString(th.dimStyle().Render(truncate(meta1, width)))
	b.WriteString("\n")
	b.WriteString(th.dimStyle().Render("↑/↓ scroll · esc/← back · g GitHub · e entire.io"))
	b.WriteString("\n\n")

	if excluded {
		reason := judge.HardGateReason(r.Deterministic.TimelineCategory)
		if reason == "" {
			reason = "held out of the ranking"
		}
		b.WriteString(lipgloss.NewStyle().Foreground(th.Bad).Bold(true).Render("EXCLUDED — hard gate"))
		b.WriteString("\n")
		b.WriteString(wrap.Render(th.dimStyle().Render(reason)))
		b.WriteString("\n\n")
	} else {
		// The two component grades and their combined total (process =
		// authenticity+prompting+effort, solution = idea_plan_execution; combined =
		// the equal-weighted mean). These are computed once when the report is built
		// (or re-derived on load by the CLI) and read here, so the list and the
		// detail page always show the same number.
		process, solution, combined := r.GradeProcess, r.GradeSolution, r.Composite
		gradeStr := func(g *float64) string {
			if g == nil {
				return "n/a"
			}
			return fmt.Sprintf("%.2f", *g)
		}
		combColor := th.Dim
		combVal := "n/a"
		if combined != nil {
			combVal = fmt.Sprintf("%.2f / 5", *combined)
			combColor = th.scoreColor(*combined)
		}
		b.WriteString(lipgloss.NewStyle().Foreground(combColor).Bold(true).Render("Combined  " + combVal))
		b.WriteString("\n")
		b.WriteString(th.dimStyle().Render("Grade A · process  ") + gradeStr(process) +
			th.dimStyle().Render("    Grade B · solution  ") + gradeStr(solution))
		b.WriteString("\n\n")

		// Integrity red-flag banner: the assistant warned about a substantive
		// integrity/validity problem and the team proceeded without addressing it.
		// Surfaced prominently; the submission still ranks (the jury decides).
		if r.IntegrityFlag {
			banner := lipgloss.NewStyle().Foreground(th.Bad).Bold(true).Render("⚠ INTEGRITY FLAG")
			b.WriteString(banner)
			b.WriteString("\n")
			if reason := strings.TrimSpace(r.IntegrityReason); reason != "" {
				b.WriteString(wrap.Render(lipgloss.NewStyle().Foreground(th.Bad).Render(reason)))
				b.WriteString("\n")
			}
			b.WriteString("\n")
		}
	}

	// ---- Score ----
	b.WriteString(sectionHeader(th, "Score"))
	b.WriteString("\n")
	for i := range r.Lenses {
		lens := r.Lenses[i]
		// The outcome lens shows as its idea/plan/execution components — each its
		// own bar, aligned with the Grade A lens bars and tagged (B · solution),
		// rather than a single aggregate bar (the aggregate is the Combined header).
		if lens.Lens == judge.LensOutcome && len(lens.Components) > 0 {
			tag := th.dimStyle().Render("(" + lensComponent[judge.LensOutcome] + ")")
			for _, c := range lens.Components {
				cv := c.Score
				b.WriteString("  " + coloredScoreBar(th, &cv) + "  " + c.Name + "  " + tag + "\n")
			}
			continue
		}
		label := lens.Lens + "  " + th.dimStyle().Render("("+lensComponent[lens.Lens]+")")
		if lens.Score != nil {
			b.WriteString("  " + coloredScoreBar(th, lens.Score) + "  " + label + "\n")
		} else {
			// descriptive (agent_leverage)
			desc := lens.Verdict
			if desc == "" {
				desc = "—"
			}
			b.WriteString("  " + th.dimStyle().Render("·····") + "       " + lens.Lens + ": " + th.dimStyle().Render(desc) + "\n")
		}
	}
	if len(r.Flags) > 0 {
		b.WriteString("  " + th.flagStyle().Render("flags: "+strings.Join(r.Flags, ", ")) + "\n")
	}
	b.WriteString("\n")

	// ---- Summary ----
	b.WriteString(sectionHeader(th, "Summary"))
	b.WriteString("\n")
	b.WriteString(wrap.Render(th.textStyle().Render(judge.SummaryText(r))))
	b.WriteString("\n\n")

	// ---- Findings ----
	b.WriteString(sectionHeader(th, "Findings"))
	b.WriteString("\n")
	b.WriteString(findingsBlock(th, r, width))

	return b.String()
}

// findingsBlock renders the deterministic metrics, then each lens's verdict,
// bullets, and evidence anchors, then any warnings.
func findingsBlock(th Theme, r *judge.RunReport, width int) string {
	wrap := lipgloss.NewStyle().Width(width).Foreground(th.Text)
	m := r.Deterministic
	var b strings.Builder

	metric := func(label, value string) {
		b.WriteString("  " + th.dimStyle().Render(label+": ") + value + "\n")
	}
	metric("timeline", fmt.Sprintf("%s (%s)", m.TimelineCategory, m.TimelineReason))
	metric("activity", fmt.Sprintf("%d sessions · %d turns · %d human prompts · %d files · %d facts",
		m.Sessions, m.Turns, m.HumanPrompts, m.FilesTouched, m.Facts))
	metric("effort", fmt.Sprintf("%s on task · %d in / %d out tokens",
		humanMinutes(m.TimeOnTaskMinutes), m.InputTokens, m.OutputTokens))
	// The coverage buckets partition TotalCommits, so they reconcile to the total;
	// "covered" is every commit made under an Entire session (carries a checkpoint
	// trailer). Merges are an overlapping tally, shown parenthetically.
	commitsLine := fmt.Sprintf("%d total · %d pre-session · %d covered · %d no-checkpoint",
		m.TotalCommits, m.PreSessionCommits, m.CoveredCommits, m.MissingSessionCommits)
	if m.NoSessionHistory > 0 {
		commitsLine += fmt.Sprintf(" · %d no-session-history", m.NoSessionHistory)
	}
	if m.MergeCommits > 0 {
		commitsLine += fmt.Sprintf(" (%d merges)", m.MergeCommits)
	}
	metric("commits", commitsLine)
	if m.FirstCommitAt != nil && m.FirstSessionAt != nil {
		rel := "after"
		if m.FirstCommitBeforeFirstSess {
			rel = "before"
		}
		metric("first commit", fmt.Sprintf("%.0f min %s first session", absFloat(m.FirstCommitToFirstSession), rel))
	}
	if agents := agentMix(m); agents != "" {
		metric("agents", agents)
	}
	if m.SemanticSymbols > 0 {
		built := fmt.Sprintf("%d symbols across %d files", m.SemanticSymbols, m.SemanticFiles)
		if len(m.SemanticCapabilities) > 0 {
			built += " · " + strings.Join(m.SemanticCapabilities, ", ")
		}
		metric("built (sem)", built)
	}
	entireTotal := m.EntireSkillInvocations + m.EntireCLIInvocations + m.EntireMCPInvocations
	if entireTotal > 0 || m.EntireSignalSessions > 0 {
		line := fmt.Sprintf("%d skill · %d CLI · %d MCP across %d session(s)",
			m.EntireSkillInvocations, m.EntireCLIInvocations, m.EntireMCPInvocations, m.EntireSignalSessions)
		if len(m.DistinctEntireCapabilities) > 0 {
			line += " · " + strings.Join(m.DistinctEntireCapabilities, ", ")
		}
		metric("entire CLI", line)
	}
	b.WriteString("\n")

	for i := range r.Lenses {
		lens := r.Lenses[i]
		if lens.Verdict == "" && len(lens.Bullets) == 0 && len(lens.Evidence) == 0 {
			continue
		}
		b.WriteString("  " + th.titleStyle().Render(lens.Lens) + "\n")
		if lens.Verdict != "" {
			b.WriteString(wrap.Render("    " + th.textStyle().Render(lens.Verdict)))
			b.WriteString("\n")
		}
		for _, bullet := range lens.Bullets {
			b.WriteString(wrap.Render("    • " + bullet))
			b.WriteString("\n")
		}
		for _, e := range lens.Evidence {
			b.WriteString("    " + th.dimStyle().Render("↳ "+truncate(e, width-6)) + "\n")
		}
		if !lens.Supported && lens.Score != nil {
			b.WriteString("    " + th.flagStyle().Render("(unsupported: no evidence anchor resolved)") + "\n")
		}
		b.WriteString("\n")
	}

	for _, w := range r.Warnings {
		b.WriteString("  " + th.flagStyle().Render("⚠ "+w) + "\n")
	}
	return b.String()
}

func sectionHeader(th Theme, title string) string {
	return th.titleStyle().Render("▌ " + title)
}

// ownerRepo splits a "gh/<owner>/<repo>" submission id into owner and repo for
// building GitHub / entire.io URLs. It returns empty strings for an id it cannot
// parse, so callers can skip the links.
func ownerRepo(submissionID string) (owner, repo string) {
	id := strings.TrimPrefix(submissionID, "gh/")
	parts := strings.Split(id, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", ""
	}
	return parts[0], parts[1]
}

// coloredScoreBar renders a themed "█████ 4.5" bar colored by the score band.
func coloredScoreBar(th Theme, score *float64) string {
	const cells = 5
	if score == nil {
		return th.dimStyle().Render(strings.Repeat("░", cells) + "  -")
	}
	filled := int(*score + 0.5)
	if filled < 0 {
		filled = 0
	}
	if filled > cells {
		filled = cells
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", cells-filled)
	styled := lipgloss.NewStyle().Foreground(th.scoreColor(*score)).Render(bar)
	return styled + fmt.Sprintf("  %.1f", *score)
}

// agentMix renders the agent histogram as "Codex×14, Claude Code×3", most-used
// first then alphabetical, with the primary agent leading implicitly.
func agentMix(m judge.Metrics) string {
	if len(m.AgentHistogram) == 0 {
		return m.PrimaryAgent
	}
	type pair struct {
		agent string
		count int
	}
	pairs := make([]pair, 0, len(m.AgentHistogram))
	for a, c := range m.AgentHistogram {
		pairs = append(pairs, pair{a, c})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].count != pairs[j].count {
			return pairs[i].count > pairs[j].count
		}
		return pairs[i].agent < pairs[j].agent
	})
	parts := make([]string, 0, len(pairs))
	for _, p := range pairs {
		parts = append(parts, fmt.Sprintf("%s×%d", p.agent, p.count))
	}
	return strings.Join(parts, ", ")
}

func humanMinutes(mins float64) string {
	if mins < 90 {
		return fmt.Sprintf("%.0f min", mins)
	}
	return fmt.Sprintf("%.1f h", mins/60)
}

func absFloat(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
