package judge

import (
	"fmt"
	"sort"
	"strings"
)

// lensDisplayRank orders lenses for display and JSON: the Grade A (process)
// lenses first — authenticity, prompting_skill, effort_consistency,
// cli_awareness, integrity — then the Grade B (solution) lens
// idea_plan_execution, then descriptive lenses.
var lensDisplayRank = map[string]int{
	LensAuthenticity: 0,
	LensPrompting:    1,
	LensEffort:       2,
	LensCLIAwareness: 3,
	LensIntegrity:    4,
	LensOutcome:      5,
	LensAgent:        6,
}

func lensRankOf(name string) int {
	if r, ok := lensDisplayRank[name]; ok {
		return r
	}
	return 99
}

// OrderLenses sorts a submission's lenses in place into the canonical display
// order, so every surface (JSON, --plain, dashboard) presents the process-grade
// lenses, then the solution-grade lens, then descriptive lenses.
func OrderLenses(lenses []LensResult) {
	sort.SliceStable(lenses, func(i, j int) bool {
		return lensRankOf(lenses[i].Lens) < lensRankOf(lenses[j].Lens)
	})
}

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

// compositeAndFlags computes the weighted composite over the scored lenses
// (authenticity, prompting_skill, effort_consistency, cli_awareness, integrity,
// idea_plan_execution; agent_leverage excluded) and surfaces hard-gate flags.
// Degenerate timeline categories are flagged so the jury sees them rather than a
// silently averaged number.
func compositeAndFlags(lenses []LensResult, m Metrics, integritySignal bool) (*float64, []string) {
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

	_, hasOutcome := scores[LensOutcome]
	_, hasPrompting := scores[LensPrompting]
	if !hasOutcome {
		if scored[LensOutcome] {
			flags = append(flags, "idea_plan_execution_unsupported")
		} else {
			flags = append(flags, "idea_plan_execution_unscored")
		}
	}
	if !hasPrompting {
		if scored[LensPrompting] {
			flags = append(flags, "prompting_skill_unsupported")
		} else {
			flags = append(flags, "prompting_skill_unscored")
		}
	}
	// The composite is the combined total of the two component grades, the single
	// source of truth for the score (Grades returns nil when no supported lens fed
	// a grade — i.e. a brain too thin to score).
	_, _, combined := Grades(lenses, integritySignal)
	return combined, flags
}

// processLenses are the "how they worked" component (Grade A): the deterministic
// timeline/effort/cli-awareness signals, the LLM prompting-skill read, and —
// PENALTY-ONLY — the LLM integrity read. Integrity feeds Grade A only when it is
// a genuine, evidence-backed concern (integrityIsPenalty: supported, scored <=
// IntegrityFlagThreshold, and a real assistant warning in the brain); a clean or
// hallucinated integrity read is excluded entirely so it can neither inflate nor
// falsely drag the grade. The solution component (Grade B) is idea_plan_execution.
var processLenses = []string{LensAuthenticity, LensPrompting, LensEffort, LensCLIAwareness, LensIntegrity}

// IntegrityFlagThreshold is the integrity score at or below which a validated
// integrity lens raises the red flag on a submission.
const IntegrityFlagThreshold = 2.0

// integrityIsPenalty is the SINGLE source of truth for "the integrity lens is a
// genuine, evidence-backed concern": a REAL assistant integrity warning exists in
// the brain (integritySignal), AND the integrity lens is present, evidence-supported
// (>=1 validated anchor), and scored at or below IntegrityFlagThreshold. Both
// DeriveIntegrityFlag (whether the red flag fires) and Grades (whether integrity
// feeds the Grade A mean) consult this predicate, so the two can never drift:
// integrity counts toward Grade A exactly when the red flag fires. A missing signal,
// or a missing/unsupported/high-scoring integrity lens, yields false.
func integrityIsPenalty(lenses []LensResult, integritySignal bool) bool {
	if !integritySignal {
		return false
	}
	for i := range lenses {
		l := lenses[i]
		if l.Lens != LensIntegrity {
			continue
		}
		return l.Score != nil && l.Supported && *l.Score <= IntegrityFlagThreshold
	}
	return false
}

// DeriveIntegrityFlag reports whether a submission's integrity lens raises the red
// flag. It fires under exactly the penalty condition (integrityIsPenalty): a REAL
// integrity warning exists in the brain (integritySignal — an assistant transcript
// turn carrying an integrity keyword) AND the lens is present, evidence-supported
// (>=1 validated anchor), and scored at or below IntegrityFlagThreshold — the
// signature of the assistant warning about a substantive integrity/validity problem
// the team then proceeded past. The signal gate prevents a false positive: an LLM
// that hallucinates a low score and cites any real session id would otherwise flag a
// clean team. It returns the reason (the lens verdict, else its first bullet) with
// the first evidence anchor appended, for prominent surfacing. When the penalty
// condition does not hold it yields (false, "").
func DeriveIntegrityFlag(lenses []LensResult, integritySignal bool) (bool, string) {
	if !integrityIsPenalty(lenses, integritySignal) {
		return false, ""
	}
	for i := range lenses {
		l := lenses[i]
		if l.Lens != LensIntegrity {
			continue
		}
		reason := strings.TrimSpace(l.Verdict)
		if reason == "" && len(l.Bullets) > 0 {
			reason = strings.TrimSpace(l.Bullets[0])
		}
		if reason == "" {
			reason = "assistant raised an unaddressed integrity concern"
		}
		if len(l.Evidence) > 0 {
			reason += " @" + strings.TrimSpace(l.Evidence[0])
		}
		return true, reason
	}
	return false, ""
}

// Grades computes the two component grades and their combined total from a
// submission's scored lenses, in the spirit of a multi-component score (technical
// + presentation): Grade A (process) is the mean of the supported process lenses
// (authenticity, prompting_skill, effort_consistency, and — PENALTY-ONLY —
// integrity); Grade B (solution) is the supported idea_plan_execution score.
// Combined is the mean of whichever grades are present, so the two components count
// equally regardless of how many lenses feed each. A nil grade means no supported
// lens fed it.
//
// Integrity is folded into the Grade A mean ONLY when it meets the penalty condition
// (integrityIsPenalty(lenses, integritySignal): supported, scored <=
// IntegrityFlagThreshold, and a real assistant warning present) — i.e. exactly when
// the integrity red flag fires. A clean/high, unsupported, or unsignalled integrity
// read is EXCLUDED from the mean entirely (not zeroed), so it can neither inflate
// Grade A nor let a hallucinated low score drag it down; Grade A is therefore
// invariant to whether a clean team's integrity anchor happened to resolve.
func Grades(lenses []LensResult, integritySignal bool) (process, solution, combined *float64) {
	integrityPenalty := integrityIsPenalty(lenses, integritySignal)
	supported := map[string]float64{}
	for _, l := range lenses {
		if l.Score != nil && l.Supported {
			supported[l.Lens] = *l.Score
		}
	}
	var psum float64
	var pn int
	for _, name := range processLenses {
		// Integrity contributes to Grade A only as a genuine, evidence-backed
		// penalty; otherwise it is excluded so it never moves the process grade.
		if name == LensIntegrity && !integrityPenalty {
			continue
		}
		if v, ok := supported[name]; ok {
			psum += v
			pn++
		}
	}
	if pn > 0 {
		p := psum / float64(pn)
		process = &p
	}
	if v, ok := supported[LensOutcome]; ok {
		s := v
		solution = &s
	}
	var csum float64
	var cn int
	if process != nil {
		csum += *process
		cn++
	}
	if solution != nil {
		csum += *solution
		cn++
	}
	if cn > 0 {
		c := csum / float64(cn)
		combined = &c
	}
	return process, solution, combined
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
