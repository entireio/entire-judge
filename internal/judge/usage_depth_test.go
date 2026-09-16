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

// The whole Entire CLI must map to a family — session/trail/setup/insight used to
// collapse into "other", which hid most of what teams did with it.
func TestCapabilityRows_CoversWholeCLI(t *testing.T) {
	m := Metrics{
		EntireCapabilitySessions: map[string]int{
			"graph": 6, "brain": 5, "session": 4, "trail": 2, "insight": 1,
		},
		EntireSignalHistogram: map[string]int{
			"cli:graph": 14, "cli:brain": 7, "cli:session": 9, "cli:trail": 3, "cli:insight": 1,
		},
	}
	rows := capabilityRows(m)
	if len(rows) != 5 {
		t.Fatalf("want a row per capability, got %d", len(rows))
	}
	if rows[0].Name != "graph" || rows[0].Sessions != 6 {
		t.Errorf("rows must be ordered by reach, got %+v", rows[0])
	}
	if rows[0].Calls != 14 {
		t.Errorf("calls should fold in from the histogram, got %d", rows[0].Calls)
	}
	if len(rows[0].Bar) <= len(rows[len(rows)-1].Bar) {
		t.Error("bar length must scale with sessions reached")
	}
	// 5 families, none of them skill -> top of the ramp.
	if rows[0].Level != 3 {
		t.Errorf("five families should be the broadest level, got %d", rows[0].Level)
	}
}

// The ramp is what turns the chart orange for narrow use and green for broad.
func TestBreadthLevel_Ramp(t *testing.T) {
	for _, tc := range []struct{ families, want int }{{0, 0}, {1, 0}, {2, 1}, {3, 2}, {4, 3}, {9, 3}} {
		if got := breadthLevel(tc.families); got != tc.want {
			t.Errorf("breadthLevel(%d) = %d, want %d", tc.families, got, tc.want)
		}
	}
}

func TestCapabilityRows_SkillOnlyIsNarrow(t *testing.T) {
	m := Metrics{EntireCapabilitySessions: map[string]int{"skill": 4}}
	rows := capabilityRows(m)
	if len(rows) != 1 || rows[0].Level != 0 {
		t.Errorf("skill alone is not breadth, got %+v", rows)
	}
}
