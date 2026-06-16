package judge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/suhaanthayyil/entire-judge/internal/agent"
)

// runSummary asks the lens agent for a short, evidence-grounded overview of the
// submission (what the team built and how it went). It reuses the lens plumbing
// — the summary template as the system prompt, the brief on stdin — and parses
// the lenient {"summary":"..."} shape. It is narrative color, not a score; on any
// failure it returns an error and the caller falls back to ComposeSummary.
func runSummary(ctx context.Context, sc submissionContext, params Params, run agent.Runner) (string, error) {
	prompt, err := loadTemplate(templateSummary)
	if err != nil {
		return "", err
	}

	metricsJSON, err := json.MarshalIndent(sc.Metrics, "", "  ")
	if err != nil {
		return "", err
	}
	if strings.Contains(prompt, metricsMarker) {
		prompt = strings.ReplaceAll(prompt, metricsMarker, string(metricsJSON))
	}
	content := sc.Brief
	if strings.Contains(prompt, contextMarker) {
		prompt = strings.ReplaceAll(prompt, contextMarker, sc.Brief)
		content = string(metricsJSON)
	}

	args, err := agent.CommandArgs(params.Agent, nil, prompt)
	if err != nil {
		return "", err
	}
	args = agent.InjectModel(args, params.Agent, params.Model)
	args = agent.InjectEffort(args, params.Agent, params.Effort)

	out, err := run(ctx, sc.RepoDir, args, []byte(content), LensTimeout)
	if err != nil {
		return "", err
	}
	return parseSummaryOutput(out), nil
}

// summaryRaw is the on-the-wire shape the summary agent is asked to emit.
type summaryRaw struct {
	Summary string `json:"summary"`
}

// parseSummaryOutput leniently extracts the summary text: strip code fences,
// pull the outer {...}, unmarshal, and trim. An empty result signals the caller
// to fall back to the composed summary.
func parseSummaryOutput(out string) string {
	jsonText, ok := extractOuterJSONObject(stripJSONFences(out))
	if !ok {
		return ""
	}
	var raw summaryRaw
	if err := json.Unmarshal([]byte(jsonText), &raw); err != nil {
		return ""
	}
	return strings.Join(strings.Fields(raw.Summary), " ")
}

// ComposeSummary builds a deterministic one-line overview from the report's
// computed fields. It is the fallback whenever the LLM summary is unavailable
// (no-egress, agent down, or unparsable), so the Summary section is never blank.
func ComposeSummary(report *RunReport) string {
	m := report.Deterministic
	var parts []string

	switch m.TimelineCategory {
	case TimelineCleanStart:
		parts = append(parts, "Clean start")
	case TimelineMixed:
		parts = append(parts, "Mixed timeline (some work predates the event)")
	case TimelinePredatesEvent:
		parts = append(parts, "Work predates the event")
	case TimelineNoSessionHistory:
		parts = append(parts, "Commits without session history")
	case TimelineInsufficientBrain:
		parts = append(parts, "Insufficient brain to assess")
	}

	// Prefer the idea/plan/execution verdict, then prompting, as the narrative core.
	if v := lensVerdict(report, LensOutcome); v != "" {
		parts = append(parts, trimSentence(v))
	} else if v := lensVerdict(report, LensPrompting); v != "" {
		parts = append(parts, trimSentence(v))
	}

	stats := fmt.Sprintf("%d sessions, %d files touched", m.Sessions, m.FilesTouched)
	if m.TimeOnTaskMinutes >= 1 {
		stats += fmt.Sprintf(", %s on task", humanizeMinutes(m.TimeOnTaskMinutes))
	}
	if m.PrimaryAgent != "" {
		stats += fmt.Sprintf(" (primarily %s)", m.PrimaryAgent)
	}
	parts = append(parts, stats)

	return strings.Join(parts, ". ") + "."
}

// SummaryText returns the LLM summary when present, else the composed fallback.
func SummaryText(report *RunReport) string {
	if strings.TrimSpace(report.Summary) != "" {
		return report.Summary
	}
	return ComposeSummary(report)
}

func lensVerdict(report *RunReport, lens string) string {
	for i := range report.Lenses {
		if report.Lenses[i].Lens == lens {
			return strings.TrimSpace(report.Lenses[i].Verdict)
		}
	}
	return ""
}

// trimSentence lowercases the leading character of a verdict so it reads as a
// clause inside the composed sentence, and strips a trailing period.
func trimSentence(s string) string {
	s = strings.TrimRight(strings.TrimSpace(s), ".")
	return s
}

func humanizeMinutes(mins float64) string {
	if mins < 90 {
		return fmt.Sprintf("%.0f min", mins)
	}
	return fmt.Sprintf("%.1f h", (time.Duration(mins) * time.Minute).Hours())
}
