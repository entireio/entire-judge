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
