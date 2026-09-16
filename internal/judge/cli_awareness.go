package judge

import (
	"fmt"
	"sort"
	"strings"

	"github.com/entireio/entire-judge/internal/brainstore"
)

// collectEntireUsage scans each session transcript for Entire skill/CLI/MCP
// signals and folds the tallies into metrics. Missing transcripts are skipped.
func collectEntireUsage(brainDir string, sessions []brainstore.Session, metrics *Metrics) {
	hist := map[string]int{}
	caps := map[string]struct{}{}
	capSessions := map[string]int{}
	subcommands := map[string]int{}
	sessionsWith := 0
	var evidence []string

	for i := range sessions {
		session := sessions[i]
		if session.TranscriptPath == "" {
			continue
		}
		raw, err := brainstore.ReadRelativeFile(brainDir, session.TranscriptPath)
		if err != nil {
			continue
		}
		signals := brainstore.ExtractEntireSignals(raw)
		if len(signals) == 0 {
			continue
		}
		sessionsWith++
		// A capability counts once per session here: reaching for the brain in six
		// sessions is sustained use, whereas six calls inside one session is a single
		// episode. Counting invocations cannot tell those apart.
		seenThisSession := map[string]struct{}{}
		for _, sig := range signals {
			switch sig.Kind {
			case brainstore.EntireSignalSkill:
				metrics.EntireSkillInvocations++
			case brainstore.EntireSignalCLI:
				metrics.EntireCLIInvocations++
			case brainstore.EntireSignalMCP:
				metrics.EntireMCPInvocations++
			}
			key := sig.Kind + ":" + sig.Capability
			hist[key]++
			if sig.Capability != "" {
				caps[sig.Capability] = struct{}{}
				if _, dup := seenThisSession[sig.Capability]; !dup {
					seenThisSession[sig.Capability] = struct{}{}
					capSessions[sig.Capability]++
				}
			}
			if detail := strings.TrimSpace(sig.Detail); detail != "" {
				subcommands[detail]++
			}
			if len(evidence) < 6 {
				anchor := session.SessionID
				if sig.Line > 0 {
					anchor = fmt.Sprintf("%s:L%d", session.SessionID, sig.Line)
				}
				evidence = append(evidence, anchor)
			}
		}
	}

	metrics.EntireSignalSessions = sessionsWith
	metrics.EntireSignalHistogram = hist
	metrics.DistinctEntireCapabilities = sortedKeys(caps)
	metrics.EntireSignalEvidence = evidence
	metrics.EntireCapabilitySessions = capSessions
	metrics.EntireSubcommands = subcommands
	metrics.EntireProblemSignals = deriveProblemSignals(capSessions, sessionsWith)
}

func sortedKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// cliAwarenessLens scores how much the team used Entire's CLI capabilities
// (skill, graph/brain/sem/judge CLI, Entire MCP tools). Deterministic and
// auditable — the score never depends on an LLM.
//
// Rubric (0–5):
//   - 0:    no Entire signals
//   - 1.5:  skill-only or a single CLI/MCP touch
//   - 3.0:  multiple sessions or multi-capability use
//   - 4.5:  sustained multi-capability use across sessions
//   - 5.0:  skill + ≥2 capability families across ≥2 sessions
func cliAwarenessLens(m Metrics) LensResult {
	total := m.EntireSkillInvocations + m.EntireCLIInvocations + m.EntireMCPInvocations
	families := capabilityFamilies(m.DistinctEntireCapabilities)
	nonSkillFamilies := 0
	for _, f := range families {
		if f != brainstore.CapabilitySkill {
			nonSkillFamilies++
		}
	}

	score := 0.0
	verdict := "No Entire skill or CLI usage detected in session transcripts."
	switch {
	case total == 0:
		// score stays 0
	case m.EntireSkillInvocations > 0 && nonSkillFamilies >= 2 && m.EntireSignalSessions >= 2:
		score = 5.0
		verdict = "Sustained Entire CLI awareness: skill plus multiple capability families across sessions."
	case nonSkillFamilies >= 2 && m.EntireSignalSessions >= 2:
		score = 4.5
		verdict = "Multi-capability Entire CLI usage across multiple sessions."
	case m.EntireSignalSessions >= 2 || (total >= 3 && len(families) >= 2) || nonSkillFamilies >= 2:
		score = 3.0
		verdict = "Repeated Entire CLI or multi-capability usage."
	default:
		score = 1.5
		verdict = "Limited Entire skill or CLI touch detected."
	}

	bullets := []string{
		fmt.Sprintf("%d skill · %d CLI · %d MCP invocation(s) across %d session(s).",
			m.EntireSkillInvocations, m.EntireCLIInvocations, m.EntireMCPInvocations, m.EntireSignalSessions),
	}
	if len(m.DistinctEntireCapabilities) > 0 {
		bullets = append(bullets, "Capabilities: "+strings.Join(m.DistinctEntireCapabilities, ", ")+".")
	}
	if len(m.EntireSignalHistogram) > 0 {
		bullets = append(bullets, "Signal mix: "+formatHistogram(m.EntireSignalHistogram)+".")
	}
	// Reach per capability, and the subcommands actually used: the two things a
	// juror asks for after the score. Sorted so the report is reproducible.
	if len(m.EntireCapabilitySessions) > 0 {
		bullets = append(bullets, "Reach: "+formatHistogram(m.EntireCapabilitySessions)+" (sessions per capability).")
	}
	if len(m.EntireSubcommands) > 0 {
		bullets = append(bullets, "Used: "+formatTopN(m.EntireSubcommands, 8)+".")
	}
	for _, sig := range m.EntireProblemSignals {
		bullets = append(bullets, "→ "+sig)
	}
	bullets = append(bullets, "Empty/spam invocations still count as a weak signal; jury may discount via evidence.")

	return LensResult{
		Lens:      LensCLIAwareness,
		Score:     &score,
		Verdict:   verdict,
		Bullets:   bullets,
		Evidence:  append([]string(nil), m.EntireSignalEvidence...),
		Supported: true,
	}
}

func capabilityFamilies(caps []string) []string {
	return append([]string(nil), caps...)
}

func formatHistogram(hist map[string]int) string {
	keys := make([]string, 0, len(hist))
	for k := range hist {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s×%d", k, hist[k]))
	}
	return strings.Join(parts, ", ")
}

// deriveProblemSignals reads the capability-by-session pattern and names the
// developer problem it points at. These are the follow-up questions jurors ask
// after seeing a cli_awareness score — "did they use Entire to carry context
// between sessions?", "did they use it to find their way around the code?" —
// answered from the pattern rather than from an LLM.
//
// Each reading is advisory and deliberately conservative: it says where to look,
// never that the team definitely solved that problem. A juror confirms against
// the evidence anchors.
func deriveProblemSignals(capSessions map[string]int, totalSessions int) []string {
	if len(capSessions) == 0 || totalSessions == 0 {
		return nil
	}
	var out []string

	// Context continuity: the brain reached for again and again, across sessions,
	// is the signature of carrying context forward (a forgetful agent, or picking
	// up someone else's thread). Once is just setup.
	if n := capSessions[brainstore.CapabilityBrain]; n >= 2 {
		out = append(out, fmt.Sprintf(
			"context continuity: brain used in %d of %d session(s) — carried context forward, not just set up once",
			n, totalSessions))
	} else if n == 1 {
		out = append(out, "context continuity: brain touched in a single session — looks like setup, not reuse")
	}

	// Code navigation: graph or sem use points at finding one's way around code
	// rather than re-reading it.
	nav := capSessions[brainstore.CapabilityGraph] + capSessions[brainstore.CapabilitySem]
	if nav >= 2 {
		out = append(out, fmt.Sprintf(
			"code navigation: graph/sem used in %d session(s) — located code instead of re-reading it", nav))
	} else if nav == 1 {
		out = append(out, "code navigation: one graph/sem session — tried it, did not lean on it")
	}

	// Breadth: how much of the toolkit they actually reached for.
	families := 0
	for capability, n := range capSessions {
		if n > 0 && capability != brainstore.CapabilitySkill {
			families++
		}
	}
	switch {
	case families >= 3:
		out = append(out, fmt.Sprintf("breadth: %d capability families used — explored the toolkit", families))
	case families == 0:
		out = append(out, "breadth: skill invocations only, no capability behind them")
	}
	return out
}

// formatTopN renders the n highest-count entries of a histogram, ties broken by
// name so the output is stable across runs.
func formatTopN(h map[string]int, n int) string {
	type kv struct {
		k string
		v int
	}
	items := make([]kv, 0, len(h))
	for k, v := range h {
		items = append(items, kv{k, v})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].v != items[j].v {
			return items[i].v > items[j].v
		}
		return items[i].k < items[j].k
	})
	if len(items) > n {
		items = items[:n]
	}
	parts := make([]string, 0, len(items))
	for _, it := range items {
		parts = append(parts, fmt.Sprintf("%s×%d", it.k, it.v))
	}
	return strings.Join(parts, ", ")
}
