package judge

import (
	"strings"
	"testing"

	"github.com/entireio/entire-judge/internal/brainstore"
)

// Brain reached for across many sessions is sustained context-carrying; the same
// capability touched once is setup. The distinction is the whole point of the
// per-session counting, so it is pinned here.
func TestDeriveProblemSignals_BrainReuseVsSetup(t *testing.T) {
	sustained := deriveProblemSignals(map[string]int{brainstore.CapabilityBrain: 5}, 6)
	if len(sustained) == 0 || !strings.Contains(sustained[0], "carried context forward") {
		t.Fatalf("5-session brain use should read as carried forward, got %v", sustained)
	}
	setup := deriveProblemSignals(map[string]int{brainstore.CapabilityBrain: 1}, 6)
	if len(setup) == 0 || !strings.Contains(setup[0], "setup, not reuse") {
		t.Fatalf("single-session brain use should read as setup, got %v", setup)
	}
}

func TestDeriveProblemSignals_NavigationAndBreadth(t *testing.T) {
	got := deriveProblemSignals(map[string]int{
		brainstore.CapabilityGraph: 3,
		brainstore.CapabilityBrain: 2,
		brainstore.CapabilityJudge: 1,
	}, 4)
	joined := strings.Join(got, " | ")
	for _, want := range []string{"code navigation", "breadth: 3 capability families"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %q", want, joined)
		}
	}
}

// Skill invocations with no capability behind them must not read as breadth.
func TestDeriveProblemSignals_SkillOnlyIsNotBreadth(t *testing.T) {
	got := deriveProblemSignals(map[string]int{brainstore.CapabilitySkill: 4}, 4)
	joined := strings.Join(got, " | ")
	if !strings.Contains(joined, "no capability behind them") {
		t.Errorf("skill-only should be called out, got %q", joined)
	}
}

func TestDeriveProblemSignals_EmptyIsNil(t *testing.T) {
	if got := deriveProblemSignals(nil, 0); got != nil {
		t.Errorf("no signals should derive nothing, got %v", got)
	}
	if got := deriveProblemSignals(map[string]int{brainstore.CapabilityGraph: 1}, 0); got != nil {
		t.Errorf("zero sessions should derive nothing, got %v", got)
	}
}

// formatTopN must be deterministic: equal counts sort by name, and the cap holds.
func TestFormatTopN_StableAndCapped(t *testing.T) {
	h := map[string]int{"graph": 9, "brain": 9, "judge": 2, "plugin": 1}
	got := formatTopN(h, 3)
	if got != "brain×9, graph×9, judge×2" {
		t.Errorf("unstable or uncapped output: %q", got)
	}
}
