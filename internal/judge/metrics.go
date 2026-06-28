package judge

import (
	"context"
	"sort"
	"time"

	"github.com/suhaanthayyil/entire-judge/internal/brainstore"
	"github.com/suhaanthayyil/entire-judge/internal/gitutil"
)

// computeMetrics derives the deterministic metrics for one brain. hackathonStart
// is optional: a zero time means "no event boundary known", which downgrades the
// timeline to insufficient_brain rather than guessing.
func computeMetrics(ctx context.Context, runner gitutil.CommandRunner, repoDir string, manifest *brainstore.Manifest, brainDir string, hackathonStart time.Time) (Metrics, *gitutil.HistoryCoverage) {
	metrics := Metrics{AgentHistogram: map[string]int{}}

	sessions := manifest.SessionList()
	metrics.Sessions = len(sessions)
	files := map[string]struct{}{}
	var firstSession, lastSession *time.Time
	for i := range sessions {
		session := sessions[i]

		agent := session.Agent
		if agent == "" {
			agent = "unknown"
		}
		metrics.AgentHistogram[agent]++

		// One exported session is one human-prompt-bearing task; CheckpointsCount
		// approximates turns within it (fall back to 1 so an unannotated session
		// still counts as a single turn).
		metrics.HumanPrompts++
		turns := session.CheckpointsCount
		if turns <= 0 {
			turns = 1
		}
		metrics.Turns += turns

		for _, f := range session.FilesTouched {
			files[f] = struct{}{}
		}
		if usage := session.TokenUsage; usage != nil {
			metrics.InputTokens += usage.InputTokens
			metrics.OutputTokens += usage.OutputTokens
			metrics.CacheReadTokens += usage.CacheReadTokens
			metrics.CacheCreationTokens += usage.CacheCreationTokens
		}
		if !session.CreatedAt.IsZero() {
			at := session.CreatedAt.UTC()
			if firstSession == nil || at.Before(*firstSession) {
				v := at
				firstSession = &v
			}
			if lastSession == nil || at.After(*lastSession) {
				v := at
				lastSession = &v
			}
		}
	}
	metrics.FilesTouched = len(files)
	metrics.FirstSessionAt = firstSession
	metrics.LastSessionAt = lastSession
	if firstSession != nil && lastSession != nil {
		metrics.TimeOnTaskMinutes = lastSession.Sub(*firstSession).Minutes()
	}
	metrics.PrimaryAgent = primaryAgent(metrics.AgentHistogram)

	// Durable facts are an optional signal (a brain may have none); a missing
	// store yields zero facts.
	if facts, err := brainstore.LoadFacts(brainDir, manifest.Branch()); err == nil {
		metrics.Facts = len(facts)
	}

	coverage := gitutil.BuildHistoryCoverage(ctx, runner, repoDir, manifest.OldestSessionAt(), manifest.ExportedCheckpointIDs())
	if coverage != nil {
		metrics.TotalCommits = coverage.TotalCommits
		metrics.PreSessionCommits = coverage.PreSessionCommits
		metrics.CoveredCommits = coverage.CoveredCommits
		metrics.MissingSessionCommits = coverage.MissingSessionCommits
		metrics.NoSessionHistory = coverage.NoSessionHistoryCommits
		metrics.MergeCommits = coverage.MergeCommits
		metrics.ExportedCheck = coverage.ExportedCheckpoints
		if coverage.OldestSessionAt != nil {
			v := coverage.OldestSessionAt.UTC()
			metrics.OldestSessionAt = &v
		}
		if at := firstCommitAt(coverage); at != nil {
			metrics.FirstCommitAt = at
			if firstSession != nil {
				metrics.FirstCommitToFirstSession = firstSession.Sub(*at).Minutes()
				metrics.FirstCommitBeforeFirstSess = at.Before(*firstSession)
			}
		}
	}

	category, reason := classifyTimeline(metrics, hackathonStart)
	metrics.TimelineCategory = category
	metrics.TimelineReason = reason
	return metrics, coverage
}

// primaryAgent returns the agent with the most sessions, breaking ties
// alphabetically so the result is deterministic.
func primaryAgent(histogram map[string]int) string {
	best := ""
	bestN := -1
	keys := make([]string, 0, len(histogram))
	for k := range histogram {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if histogram[k] > bestN {
			best = k
			bestN = histogram[k]
		}
	}
	return best
}

// firstCommitAt returns the earliest commit time observable in the coverage's
// uncovered-commit list. Coverage does not expose every commit's timestamp, so
// this is a best-effort floor used only for the descriptive first-commit delta.
func firstCommitAt(coverage *gitutil.HistoryCoverage) *time.Time {
	var earliest *time.Time
	for i := range coverage.UncoveredCommits {
		at := coverage.UncoveredCommits[i].CommittedAt
		if at.IsZero() {
			continue
		}
		at = at.UTC()
		if earliest == nil || at.Before(*earliest) {
			v := at
			earliest = &v
		}
	}
	return earliest
}

// classifyTimeline maps the deterministic coverage + session timing into a
// timeline category. Session-timing placement is the strongest signal and takes
// precedence; the no_session_history commit gate is only a fallback for when
// sessions cannot place the work.
func classifyTimeline(metrics Metrics, hackathonStart time.Time) (string, string) {
	if metrics.Sessions == 0 {
		return TimelineInsufficientBrain, "no exported sessions in the brain"
	}

	oldest := metrics.OldestSessionAt
	if oldest == nil {
		oldest = metrics.FirstSessionAt
	}
	last := metrics.LastSessionAt

	if !hackathonStart.IsZero() && oldest != nil && last != nil {
		start := hackathonStart.UTC()
		switch {
		case last.Before(start):
			return TimelinePredatesEvent, "all captured session activity predates the hackathon start"
		case oldest.Before(start):
			return TimelineMixed, "session history straddles the hackathon start (some work predates the event)"
		default:
			return TimelineCleanStart, "all captured session activity began at or after the hackathon start"
		}
	}

	if metrics.TotalCommits > 0 && metrics.NoSessionHistory == metrics.TotalCommits {
		return TimelineNoSessionHistory, "every commit predates any captured session history"
	}
	if hackathonStart.IsZero() {
		return TimelineInsufficientBrain, "no hackathon start provided; cannot place work relative to the event"
	}
	return TimelineInsufficientBrain, "session timestamps unavailable; cannot place work relative to the event"
}
