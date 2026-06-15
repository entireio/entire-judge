package judge

import (
	"fmt"
	"strings"
)

// authenticityLens computes the deterministic authenticity score from the
// timeline rubric: predates_event caps low, clean_start floors high, mixed sits
// in the middle, and degenerate categories score low. The LLM only adds bullets —
// it never moves this score.
func authenticityLens(m Metrics) LensResult {
	var score float64
	verdict := ""
	switch m.TimelineCategory {
	case TimelineCleanStart:
		score = 4.5
		verdict = "Work authentically began at or after the event start."
	case TimelineMixed:
		score = 3.0
		verdict = "Work straddles the event start; some history predates it."
	case TimelinePredatesEvent:
		score = 1.5
		verdict = "Captured work predates the event start."
	case TimelineNoSessionHistory:
		score = 0.5
		verdict = "Commits exist but no session history backs them."
	default: // insufficient_brain
		score = 0
		verdict = "Insufficient brain to assess authenticity."
	}
	return LensResult{
		Lens:    LensAuthenticity,
		Score:   &score,
		Verdict: verdict,
		Bullets: []string{
			fmt.Sprintf("Timeline category: %s (%s).", m.TimelineCategory, m.TimelineReason),
			fmt.Sprintf("%d sessions, %d commits (%d pre-session, %d covered).", m.Sessions, m.TotalCommits, m.PreSessionCommits, m.CoveredCommits),
		},
		Supported: true,
	}
}

// effortLens computes the deterministic effort/consistency score from session
// spread, turn count, and time on task. It rewards sustained, multi-session work
// over a single burst.
func effortLens(m Metrics) LensResult {
	score := 0.0
	switch {
	case m.Sessions >= 8:
		score += 2.0
	case m.Sessions >= 4:
		score += 1.5
	case m.Sessions >= 2:
		score += 1.0
	case m.Sessions == 1:
		score += 0.5
	}
	switch {
	case m.Turns >= 40:
		score += 1.5
	case m.Turns >= 15:
		score += 1.0
	case m.Turns >= 5:
		score += 0.5
	}
	switch {
	case m.TimeOnTaskMinutes >= 8*60:
		score += 1.5
	case m.TimeOnTaskMinutes >= 2*60:
		score += 1.0
	case m.TimeOnTaskMinutes >= 30:
		score += 0.5
	}
	if m.FilesTouched >= 10 {
		score += 0.5
	}
	if score > 5 {
		score = 5
	}
	return LensResult{
		Lens:    LensEffort,
		Score:   &score,
		Verdict: fmt.Sprintf("%d sessions over %.0f minutes, %d turns, %d files touched.", m.Sessions, m.TimeOnTaskMinutes, m.Turns, m.FilesTouched),
		Bullets: []string{
			fmt.Sprintf("Token usage: %d input, %d output.", m.InputTokens, m.OutputTokens),
			fmt.Sprintf("Work spread across %d session(s).", m.Sessions),
		},
		Supported: true,
	}
}

// agentLeverageLens is a descriptive, unscored lens reporting which agents the
// team leaned on.
func agentLeverageLens(m Metrics) LensResult {
	hist := sortedAgentHistogram(m.AgentHistogram)
	verdict := "No agent attribution in the brain."
	if m.PrimaryAgent != "" {
		verdict = fmt.Sprintf("Primary agent: %s.", m.PrimaryAgent)
	}
	bullets := []string{}
	if len(hist) > 0 {
		bullets = append(bullets, "Agent mix: "+strings.Join(hist, ", ")+".")
	}
	return LensResult{
		Lens:      LensAgent,
		Verdict:   verdict,
		Bullets:   bullets,
		Supported: m.PrimaryAgent != "" && m.PrimaryAgent != "unknown",
	}
}

// compositeAndFlags computes the weighted composite over the four scored lenses
// (agent_leverage excluded) and surfaces hard-gate flags. Degenerate timeline
// categories are flagged so the jury sees them rather than a silently averaged
// number.
func compositeAndFlags(lenses []LensResult, m Metrics) (*float64, []string) {
	// Only evidence-backed lens scores count toward the composite. An LLM lens
	// that returned a score but whose evidence anchors did not resolve against
	// the brain (Supported == false) is excluded rather than allowed to move the
	// composite on unverifiable grounds — otherwise a team is penalized for the
	// brain not capturing its prompts (e.g. an agent that exposes no prompt text)
	// instead of for weak work. The deterministic lenses are always supported.
	scores := map[string]float64{}
	scored := map[string]bool{}
	for _, lens := range lenses {
		if lens.Score == nil {
			continue
		}
		scored[lens.Lens] = true
		if lens.Supported {
			scores[lens.Lens] = *lens.Score
		}
	}
	var flags []string
	switch m.TimelineCategory {
	case TimelinePredatesEvent:
		flags = append(flags, "predates_event")
	case TimelineNoSessionHistory:
		flags = append(flags, "no_session_history")
	case TimelineInsufficientBrain:
		flags = append(flags, "insufficient_brain")
	}

	auth, hasAuth := scores[LensAuthenticity]
	outcome, hasOutcome := scores[LensOutcome]
	prompting, hasPrompting := scores[LensPrompting]
	effort, hasEffort := scores[LensEffort]
	if !hasAuth || !hasEffort {
		// Authenticity and effort are deterministic and always present; their
		// absence means the brain was too thin to score at all.
		return nil, flags
	}

	composite := auth * weightAuthenticity
	weightUsed := weightAuthenticity
	composite += effort * weightEffort
	weightUsed += weightEffort
	if hasOutcome {
		composite += outcome * weightOutcome
		weightUsed += weightOutcome
	} else if scored[LensOutcome] {
		flags = append(flags, "idea_plan_execution_unsupported")
	} else {
		flags = append(flags, "idea_plan_execution_unscored")
	}
	if hasPrompting {
		composite += prompting * weightPrompting
		weightUsed += weightPrompting
	} else if scored[LensPrompting] {
		flags = append(flags, "prompting_skill_unsupported")
	} else {
		flags = append(flags, "prompting_skill_unscored")
	}
	// Renormalize so a missing LLM lens does not deflate the composite below what
	// the present lenses justify.
	if weightUsed > 0 {
		composite = composite / weightUsed
	}
	return &composite, flags
}

// HardGateReason returns a non-empty exclusion reason for a degenerate timeline
// category, or "" when the submission is rankable.
func HardGateReason(category string) string {
	switch category {
	case TimelinePredatesEvent:
		return "captured work predates the event start"
	case TimelineNoSessionHistory:
		return "commits exist but no session history backs them"
	case TimelineInsufficientBrain:
		return "insufficient brain to assess"
	}
	return ""
}
