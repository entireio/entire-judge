package judge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/entireio/entire-judge/internal/agent"
	"github.com/entireio/entire-judge/internal/brainstore"
	"github.com/entireio/entire-judge/internal/gitutil"
)

const (
	// contextMaxBytes hard-caps the assembled brief handed to a lens agent.
	contextMaxBytes = 96 * 1024
	// excerptMaxBytes bounds the human-prompt excerpt section specifically.
	excerptMaxBytes = 64 * 1024
	// assistantCtxMaxBytes bounds the assistant-turn integrity-context section.
	assistantCtxMaxBytes = 32 * 1024
	// semanticMaxBytes bounds the entire-sem digest section so an unbounded
	// ByKind/ByLanguage/TopFiles join cannot dominate the base and crowd out the
	// integrity section.
	semanticMaxBytes = contextMaxBytes / 2
	// LensTimeout is the default per-lens agent timeout.
	LensTimeout = 5 * time.Minute

	contextMarker = "${SUBMISSION_CONTEXT}"
	metricsMarker = "${METRICS_JSON}"
)

// submissionContext bundles everything a lens needs to score a submission: the
// resolved brain location, the loaded data, the deterministic metrics, and a
// bounded brief assembled from facts + human-prompt excerpts + a timeline
// summary.
type submissionContext struct {
	RepoDir  string
	BrainDir string
	RepoKey  string
	Branch   string

	Manifest *brainstore.Manifest
	Sessions []brainstore.Session
	Facts    []brainstore.FactRecord
	Coverage *gitutil.HistoryCoverage
	Metrics  Metrics
	Semantic *brainstore.SemanticSummary

	Brief string
}

// buildBrief renders the bounded text brief: a deterministic timeline summary, a
// facts section ("kind|paths|text" lines), and human-prompt-only session
// excerpts. The whole thing is hard-capped at contextMaxBytes.
func buildBrief(sc submissionContext) string {
	var b strings.Builder

	b.WriteString("## Deterministic timeline\n\n")
	b.WriteString(timelineSummary(sc.Metrics))
	b.WriteString("\n")

	if len(sc.Facts) > 0 {
		b.WriteString("\n## Durable facts (kind|paths|text)\n\n")
		for _, fact := range sc.Facts {
			line := fmt.Sprintf("%s|%s|%s",
				strings.TrimSpace(fact.Kind),
				strings.Join(fact.Paths, ","),
				strings.Join(strings.Fields(fact.Text), " "),
			)
			b.WriteString(truncateString(line, 600))
			b.WriteString("\n")
			if b.Len() > contextMaxBytes/2 {
				b.WriteString("... (facts truncated)\n")
				break
			}
		}
	}

	if sec := semanticSummarySection(sc.Semantic); sec != "" {
		b.WriteString(truncateString(sec, semanticMaxBytes))
	}

	// The integrity (assistant-context) section carries the load-bearing warning
	// signal, so it must NEVER be the section a tail-truncate sacrifices on a large
	// submission. Build it first, then keep it intact BY CONSTRUCTION: the
	// human-prompt excerpts get only the budget left after the base sections and this
	// section (the existing reserve), and — critically — the already-built base is
	// itself bounded to contextMaxBytes-len(integritySection) before the integrity
	// section is appended. That handles the case the reserve alone cannot: a base of
	// facts + a large semantic layer that on its own exceeds the budget. The final
	// safety truncate below is therefore a no-op for the integrity section.
	var integritySection string
	if ctx := assistantContextExcerpts(sc, assistantCtxMaxBytes); ctx != "" {
		integritySection = "\n## Assistant messages & adjacent human turns (integrity context)\n\n" + ctx
	}

	const reserveMargin = 1024
	excerptBudget := excerptMaxBytes
	if remaining := contextMaxBytes - b.Len() - len(integritySection) - reserveMargin; remaining < excerptBudget {
		excerptBudget = remaining
	}
	if excerptBudget > 0 {
		if excerpts := humanPromptExcerpts(sc, excerptBudget); excerpts != "" {
			b.WriteString("\n## Human prompts (session excerpts)\n\n")
			b.WriteString(excerpts)
		}
	}

	if integritySection != "" {
		// Bound the base so appending the integrity section cannot overflow the cap:
		// truncate the base to contextMaxBytes-len(integritySection) (floor 0) first,
		// THEN append. len(base)+len(integritySection) <= contextMaxBytes by
		// construction, guaranteeing the integrity section survives the final truncate.
		if base := b.String(); len(base)+len(integritySection) > contextMaxBytes {
			baseCap := contextMaxBytes - len(integritySection)
			if baseCap < 0 {
				baseCap = 0
			}
			b.Reset()
			b.WriteString(truncateString(base, baseCap))
		}
		b.WriteString(integritySection)
	}

	out := b.String()
	if len(out) > contextMaxBytes {
		out = truncateString(out, contextMaxBytes)
	}
	return out
}

// integritySignalForLens is the ANCHOR-SCOPED corroborating signal for a real
// integrity warning: it is true only when a session the integrity lens actually
// CITED (an evidence anchor that resolves to a session) has an ASSISTANT turn
// carrying an integrity keyword. Scoping to the cited evidence — rather than
// scanning every session in the brain — keeps the backstop meaningful in an ML
// hackathon, where benign keyword-ish chatter ("92% on the test-set",
// "train/test split") is everywhere: only a warning inside the LENS's own
// evidence gates the red flag and the Grade-A penalty (see DeriveIntegrityFlag /
// Grades). A commit-only anchor that maps to no session contributes nothing; no
// evidence, no anchor resolving to a session, or no keyword-bearing assistant
// turn in a cited session all yield false.
func integritySignalForLens(brainDir string, sessions []brainstore.Session, integrity LensResult) bool {
	for _, anchor := range integrity.Evidence {
		for i := range sessions {
			if !anchorResolvesToSession(anchor, sessions[i]) {
				continue
			}
			if sessionHasAssistantIntegritySignal(brainDir, sessions[i]) {
				return true
			}
		}
	}
	return false
}

// anchorResolvesToSession reports whether an evidence anchor resolves to THIS
// session, reusing the shared anchorResolves logic with single-session maps (the
// session's id and its RFC3339 CreatedAt timestamp). Commit hashes and file paths
// are intentionally not supplied: neither identifies a session, so a commit-only
// or file-only anchor resolves to no session.
func anchorResolvesToSession(anchor string, session brainstore.Session) bool {
	sessionIDs := map[string]struct{}{}
	if session.SessionID != "" {
		sessionIDs[strings.ToLower(session.SessionID)] = struct{}{}
	}
	sessionTimes := map[string]struct{}{}
	if !session.CreatedAt.IsZero() {
		sessionTimes[strings.ToLower(session.CreatedAt.UTC().Format(time.RFC3339))] = struct{}{}
		sessionTimes[strings.ToLower(session.CreatedAt.UTC().Format(time.RFC3339Nano))] = struct{}{}
	}
	return anchorResolves(anchor, sessionIDs, sessionTimes, nil, nil, "")
}

// sessionHasAssistantIntegritySignal reports whether the session's transcript has
// an ASSISTANT turn carrying an integrity keyword (containsIntegrityKeyword over
// brainstore.ExtractConversationTurns). An absent/unreadable transcript yields false.
func sessionHasAssistantIntegritySignal(brainDir string, session brainstore.Session) bool {
	if session.TranscriptPath == "" {
		return false
	}
	raw, err := brainstore.ReadRelativeFile(brainDir, session.TranscriptPath)
	if err != nil {
		return false
	}
	for _, turn := range brainstore.ExtractConversationTurns(raw) {
		if turn.Role == "assistant" && containsIntegrityKeyword(turn.Text) {
			return true
		}
	}
	return false
}

// semanticSummarySection renders the entire-sem digest — what the team actually
// built (symbol kinds, languages, busiest files, capabilities) — for the brief.
// It is the structural ground truth the execution lens checks the plan against.
// Returns "" when there is no sem layer.
func semanticSummarySection(s *brainstore.SemanticSummary) string {
	if s == nil || s.Symbols == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n## What was built (code structure from entire-sem)\n\n")
	fmt.Fprintf(&b, "%d symbols, %d relations across %d files", s.Symbols, s.Relations, s.Files)
	if len(s.Capabilities) > 0 {
		fmt.Fprintf(&b, "; capabilities: %s", strings.Join(s.Capabilities, ", "))
	}
	b.WriteString("\n")
	if labels := labelCountLine(s.ByKind); labels != "" {
		fmt.Fprintf(&b, "symbol kinds: %s\n", labels)
	}
	if labels := labelCountLine(s.ByLanguage); labels != "" {
		fmt.Fprintf(&b, "languages: %s\n", labels)
	}
	if labels := labelCountLine(s.TopFiles); labels != "" {
		fmt.Fprintf(&b, "busiest files: %s\n", labels)
	}
	return b.String()
}

func labelCountLine(counts []brainstore.LabelCount) string {
	parts := make([]string, 0, len(counts))
	for _, c := range counts {
		parts = append(parts, fmt.Sprintf("%s(%d)", c.Label, c.Count))
	}
	return strings.Join(parts, ", ")
}

// timelineSummary renders the deterministic metrics as a compact factual summary
// for the brief. It states the category and the load-bearing counts so a lens can
// explain — but not invent — the verdict.
func timelineSummary(m Metrics) string {
	var b strings.Builder
	fmt.Fprintf(&b, "category: %s (%s)\n", m.TimelineCategory, m.TimelineReason)
	fmt.Fprintf(&b, "sessions: %d, human prompts: %d, turns: %d, files touched: %d, facts: %d\n",
		m.Sessions, m.HumanPrompts, m.Turns, m.FilesTouched, m.Facts)
	fmt.Fprintf(&b, "commits: total %d, pre-session %d, covered %d, no-checkpoint %d, no-session-history %d, merges %d\n",
		m.TotalCommits, m.PreSessionCommits, m.CoveredCommits, m.MissingSessionCommits, m.NoSessionHistory, m.MergeCommits)
	fmt.Fprintf(&b, "tokens: input %d, output %d, cache-read %d, cache-creation %d\n",
		m.InputTokens, m.OutputTokens, m.CacheReadTokens, m.CacheCreationTokens)
	if m.FirstSessionAt != nil {
		fmt.Fprintf(&b, "first session: %s\n", m.FirstSessionAt.UTC().Format(time.RFC3339))
	}
	if m.LastSessionAt != nil {
		fmt.Fprintf(&b, "last session: %s\n", m.LastSessionAt.UTC().Format(time.RFC3339))
	}
	fmt.Fprintf(&b, "time on task: %.1f minutes\n", m.TimeOnTaskMinutes)
	if m.PrimaryAgent != "" {
		fmt.Fprintf(&b, "primary agent: %s\n", m.PrimaryAgent)
	}
	return b.String()
}

// humanPromptExcerpts extracts the human turns from each session's transcript and
// concatenates them under a byte budget. A session whose transcript is unreadable
// is silently skipped — the brief degrades, it does not fail.
func humanPromptExcerpts(sc submissionContext, maxBytes int) string {
	var b strings.Builder
	for i := range sc.Sessions {
		session := sc.Sessions[i]
		if session.TranscriptPath == "" {
			continue
		}
		raw, err := brainstore.ReadRelativeFile(sc.BrainDir, session.TranscriptPath)
		if err != nil {
			continue
		}
		prompts := brainstore.ExtractHumanPrompts(raw)
		if len(prompts) == 0 {
			continue
		}
		header := fmt.Sprintf("### session %s", session.SessionID)
		if session.Branch != "" {
			header += " (" + session.Branch + ")"
		}
		b.WriteString(header)
		b.WriteString("\n")
		for _, p := range prompts {
			line := "- " + truncateString(strings.Join(strings.Fields(p), " "), 500)
			if b.Len()+len(line) > maxBytes {
				b.WriteString("- ... (excerpts truncated)\n")
				return b.String()
			}
			b.WriteString(line)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// integrityKeywords are phrases that denote an actual integrity or validity
// VIOLATION — test-set contamination, train/test leakage, evaluating on / training
// on the test data, overfitting the holdout, cheating, memorized/seen examples,
// fabricated/plagiarized results, rules violations, or hardcoded benchmark answers.
// They corroborate the integrity red flag via integritySignalForLens — which scopes
// the scan to the SESSIONS THE LENS CITED — AND prioritize which assistant-turn
// windows survive truncation.
//
// This list is an ADVISORY BACKSTOP only, scoped to the cited session's transcript;
// it never gates a verdict on its own. The LLM integrity-lens score plus a validated
// evidence anchor are the PRIMARY gates (see DeriveIntegrityFlag / Grades) — the
// keyword scan merely corroborates. As a case-insensitive substring matcher it has an
// irreducible false-positive/false-negative floor, so it is precision-tuned rather
// than exhaustive: entries are specific phrases, and bare high-collision stems that
// fire on ordinary ML/coding chatter are deliberately EXCLUDED even at some recall
// cost — "leak" (vs "leakage"), "invalid" (vs "invalid results"), "evaluat", "cheat"
// (vs "cheating", which does not match "cheat sheet"), "trained on" (vs "trained on
// the test" — a backbone "trained on ImageNet" is benign), "overfit" (vs "holdout"),
// "leaderboard", "violates the", "integrity", "test-set" (bare — appears in benign
// "92% on the test-set"), "training data", "ground truth". Substrings, matched
// case-insensitively.
var integrityKeywords = []string{
	"contaminated with", "test set samples",
	"leakage", "train/test", "train / test", "trained on the test",
	"same data",
	"evaluating on the training", "results are invalid", "invalid results",
	"illegal for the competition", "against the rules", "competition rules",
	"hardcoded the", "hard-coded the",
	"fabricat", "plagiari", "disqualif",
	"held-out test", "held out test", "holdout", "hold-out",
	"memoriz", "saw these", "cheating",
}

func containsIntegrityKeyword(s string) bool {
	lower := strings.ToLower(s)
	for _, k := range integrityKeywords {
		if strings.Contains(lower, k) {
			return true
		}
	}
	return false
}

// assistantContextExcerpts renders the ASSISTANT turns (where an integrity warning
// would live) each with its adjacent human turns (whether the team addressed or
// overrode it), grouped per session with a session-id + timestamp anchor so the
// integrity lens can cite evidence. Windows containing a near-warning keyword are
// emitted first, so when the section is truncated to maxBytes the load-bearing
// signal is kept. A session whose transcript is unreadable is silently skipped.
func assistantContextExcerpts(sc submissionContext, maxBytes int) string {
	type window struct {
		text    string
		flagged bool
	}
	var windows []window
	for i := range sc.Sessions {
		session := sc.Sessions[i]
		if session.TranscriptPath == "" {
			continue
		}
		raw, err := brainstore.ReadRelativeFile(sc.BrainDir, session.TranscriptPath)
		if err != nil {
			continue
		}
		turns := brainstore.ExtractConversationTurns(raw)
		header := "### session " + session.SessionID
		if !session.CreatedAt.IsZero() {
			header += " @" + session.CreatedAt.UTC().Format(time.RFC3339)
		}
		for j := range turns {
			if turns[j].Role != "assistant" {
				continue
			}
			var w strings.Builder
			w.WriteString(header)
			w.WriteString("\n")
			if j > 0 && turns[j-1].Role == "user" {
				w.WriteString("- [human] " + flattenTurn(turns[j-1].Text, 400) + "\n")
			}
			w.WriteString("- [assistant] " + flattenTurn(turns[j].Text, 900) + "\n")
			if j+1 < len(turns) && turns[j+1].Role == "user" {
				w.WriteString("- [human→] " + flattenTurn(turns[j+1].Text, 400) + "\n")
			}
			windows = append(windows, window{text: w.String(), flagged: containsIntegrityKeyword(turns[j].Text)})
		}
	}
	if len(windows) == 0 {
		return ""
	}
	var b strings.Builder
	// Two passes: near-warning windows first, then the rest, so truncation drops
	// ordinary chatter before it drops a warning.
	for _, wantFlagged := range []bool{true, false} {
		for _, w := range windows {
			if w.flagged != wantFlagged {
				continue
			}
			if b.Len()+len(w.text) > maxBytes {
				b.WriteString("... (assistant context truncated)\n")
				return b.String()
			}
			b.WriteString(w.text)
		}
	}
	return b.String()
}

// flattenTurn collapses a turn's whitespace onto one line and caps its length.
func flattenTurn(text string, max int) string {
	return truncateString(strings.Join(strings.Fields(text), " "), max)
}

// loadTemplate loads the system prompt for a named lens template, stripping its
// YAML frontmatter (the agent runners pass the prompt as a positional arg, so a
// leading "---" would be parsed as a flag).
func loadTemplate(templateName string) (string, error) {
	name := "templates/entire-judge-" + templateName + ".md"
	data, err := templatesFS.ReadFile(name)
	if err != nil {
		return "", fmt.Errorf("read judge template %s: %w", templateName, err)
	}
	return stripTemplateFrontmatter(string(data)), nil
}

func stripTemplateFrontmatter(content string) string {
	if !strings.HasPrefix(content, "---\n") && !strings.HasPrefix(content, "---\r\n") {
		return content
	}
	lines := strings.Split(content, "\n")
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return strings.TrimLeft(strings.Join(lines[i+1:], "\n"), "\r\n")
		}
	}
	return content
}

// runLens runs one LLM-backed lens: it loads the lens template, substitutes the
// metrics JSON marker, builds the agent argv (honoring no-egress), and runs the
// agent with the assembled brief on stdin. The lens output is parsed leniently —
// a parse failure becomes a warning on the result, never a hard error.
func runLens(ctx context.Context, sc submissionContext, lensName, templateName, agentName, model, effort string, run agent.Runner, timeout time.Duration) (LensResult, error) {
	result := LensResult{Lens: lensName}

	prompt, err := loadTemplate(templateName)
	if err != nil {
		return result, err
	}

	metricsJSON, err := json.MarshalIndent(sc.Metrics, "", "  ")
	if err != nil {
		return result, err
	}
	if strings.Contains(prompt, metricsMarker) {
		prompt = strings.ReplaceAll(prompt, metricsMarker, string(metricsJSON))
	}

	content := sc.Brief
	if strings.Contains(prompt, contextMarker) {
		prompt = strings.ReplaceAll(prompt, contextMarker, sc.Brief)
		content = string(metricsJSON)
	}

	args, err := agent.CommandArgs(agentName, nil, prompt)
	if err != nil {
		return result, err
	}
	args = agent.InjectModel(args, agentName, model)
	args = agent.InjectEffort(args, agentName, effort)

	if timeout <= 0 {
		timeout = LensTimeout
	}
	out, err := run(ctx, sc.RepoDir, args, []byte(content), timeout)
	if err != nil {
		result.Warnings = append(result.Warnings, fmt.Sprintf("lens agent failed: %v", err))
		return result, nil
	}
	parsed := parseLensOutput(out, lensName)
	parsed.Lens = lensName
	return parsed, nil
}

// lensRaw is the on-the-wire shape a lens agent is asked to emit. A lens may emit
// a single overall `score`, or a set of named sub-scores (idea/plan/execution for
// the outcome lens) whose mean becomes the overall score.
type lensRaw struct {
	Score     *float64 `json:"score"`
	Idea      *float64 `json:"idea"`
	Plan      *float64 `json:"plan"`
	Execution *float64 `json:"execution"`
	Verdict   string   `json:"verdict"`
	Bullets   []string `json:"bullets"`
	Evidence  []string `json:"evidence"`
}

// clampScore confines a lens score to the 0-5 range.
func clampScore(s float64) float64 {
	if s < 0 {
		return 0
	}
	if s > 5 {
		return 5
	}
	return s
}

// parseLensOutput is the LENIENT parser for a lens agent's stdout. It strips
// ```json fences, extracts the outer {...} object, unmarshals it, clamps the
// score to 0-5, and dedupes bullets. On any failure it returns a result carrying
// a warning and the raw output — it never hard-fails. lensName selects the
// scoring shape: the outcome lens averages idea/plan/execution sub-scores; the
// others use a single `score`.
func parseLensOutput(out, lensName string) LensResult {
	result := LensResult{Raw: out}
	jsonText, ok := extractOuterJSONObject(stripJSONFences(out))
	if !ok {
		result.Warnings = append(result.Warnings, "lens output contained no JSON object")
		return result
	}
	var raw lensRaw
	if err := json.Unmarshal([]byte(jsonText), &raw); err != nil {
		result.Warnings = append(result.Warnings, fmt.Sprintf("lens output not valid JSON: %v", err))
		return result
	}
	// Sub-scores (idea/plan/execution) apply only to the outcome lens: when present
	// the overall score is their mean and each is recorded as a component, keeping
	// the outcome grade fractional and showing the jury what drove it. Other lenses
	// use the single `score` (and any stray sub-score keys are ignored).
	if lensName == LensOutcome {
		subs := []struct {
			name string
			val  *float64
		}{{"idea", raw.Idea}, {"plan", raw.Plan}, {"execution", raw.Execution}}
		var sum float64
		var n int
		for _, s := range subs {
			if s.val == nil {
				continue
			}
			v := clampScore(*s.val)
			result.Components = append(result.Components, LensComponent{Name: s.name, Score: v})
			sum += v
			n++
		}
		if n > 0 {
			mean := sum / float64(n)
			result.Score = &mean
		}
	}
	if result.Score == nil && raw.Score != nil {
		s := clampScore(*raw.Score)
		result.Score = &s
	}
	// A scored LLM lens that parsed but produced no usable score is surfaced as a
	// warning so the run is marked degraded rather than silently unscored.
	if result.Score == nil && (lensName == LensOutcome || lensName == LensPrompting || lensName == LensIntegrity) {
		result.Warnings = append(result.Warnings, "lens output contained no usable score")
	}
	result.Verdict = strings.TrimSpace(raw.Verdict)
	result.Bullets = dedupeStrings(raw.Bullets)
	result.Evidence = dedupeStrings(raw.Evidence)
	return result
}

// stripJSONFences removes a leading/embedded ```json fence wrapper so the object
// extractor sees bare JSON. It is tolerant: content without fences is returned
// unchanged.
func stripJSONFences(s string) string {
	s = strings.TrimSpace(s)
	if !strings.Contains(s, "```") {
		return s
	}
	if idx := strings.Index(s, "```"); idx >= 0 {
		rest := s[idx+3:]
		if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
			lang := strings.TrimSpace(rest[:nl])
			if lang == "json" || lang == "" || !strings.Contains(lang, "{") {
				rest = rest[nl+1:]
			}
		}
		if closeIdx := strings.Index(rest, "```"); closeIdx >= 0 {
			rest = rest[:closeIdx]
		}
		return strings.TrimSpace(rest)
	}
	return s
}

// extractOuterJSONObject returns the substring from the first '{' to its matching
// '}', tracking string literals so a brace inside a JSON string does not throw
// off the balance. ok=false when no balanced object is present.
func extractOuterJSONObject(s string) (string, bool) {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return "", false
	}
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1], true
			}
		}
	}
	return "", false
}

// dedupeStrings trims and removes empty/duplicate entries while preserving the
// first-seen order.
func dedupeStrings(in []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// validateLensEvidence filters the lens's evidence to anchors that resolve against
// the brain, records dropped anchors as warnings, and sets Supported when at
// least one anchor resolves.
func validateLensEvidence(result *LensResult, sc submissionContext) {
	kept, dropped := validateEvidenceAnchors(sc, result.Evidence)
	result.Evidence = kept
	for _, d := range dropped {
		result.Warnings = append(result.Warnings, "dropped unresolvable evidence anchor: "+truncateString(d, 120))
	}
	result.Supported = len(kept) > 0
}

// validateEvidenceAnchors filters an evidence list to anchors that resolve against
// the brain/repo: a commit hash that appears in the coverage, a session id or
// session timestamp that exists in the manifest, or a safe repo-relative file
// path from the submission. Unresolvable anchors are dropped (and reported).
func validateEvidenceAnchors(sc submissionContext, evidence []string) (kept, dropped []string) {
	sessionIDs := map[string]struct{}{}
	sessionTimes := map[string]struct{}{}
	for i := range sc.Sessions {
		sessionIDs[strings.ToLower(sc.Sessions[i].SessionID)] = struct{}{}
		if !sc.Sessions[i].CreatedAt.IsZero() {
			sessionTimes[strings.ToLower(sc.Sessions[i].CreatedAt.UTC().Format(time.RFC3339))] = struct{}{}
			sessionTimes[strings.ToLower(sc.Sessions[i].CreatedAt.UTC().Format(time.RFC3339Nano))] = struct{}{}
		}
	}
	commitHashes := map[string]struct{}{}
	if sc.Coverage != nil {
		for i := range sc.Coverage.UncoveredCommits {
			commitHashes[strings.ToLower(sc.Coverage.UncoveredCommits[i].Hash)] = struct{}{}
		}
	}
	filePaths := knownEvidenceFiles(sc)
	for _, anchor := range evidence {
		if anchorResolves(anchor, sessionIDs, sessionTimes, commitHashes, filePaths, sc.RepoDir) {
			kept = append(kept, anchor)
		} else {
			dropped = append(dropped, anchor)
		}
	}
	return kept, dropped
}

func knownEvidenceFiles(sc submissionContext) map[string]struct{} {
	files := map[string]struct{}{}
	for _, session := range sc.Sessions {
		for _, file := range session.FilesTouched {
			addEvidenceFile(files, file)
		}
	}
	if sc.Semantic != nil {
		for _, file := range sc.Semantic.TopFiles {
			addEvidenceFile(files, file.Label)
		}
	}
	return files
}

func addEvidenceFile(files map[string]struct{}, file string) {
	if clean, ok := cleanRepoEvidencePath(file); ok {
		files[strings.ToLower(clean)] = struct{}{}
	}
}

// anchorResolves reports whether an evidence string references a known session id
// a known session timestamp, a known commit hash, or a safe repo-relative file.
// It tokenizes the anchor so an anchor like "commit abc1234: fixed the bug" still
// matches the bare hash, and matches a commit hash by prefix (agents routinely
// cite short hashes).
func anchorResolves(anchor string, sessionIDs, sessionTimes, commitHashes, filePaths map[string]struct{}, repoDir string) bool {
	lower := strings.ToLower(anchor)
	trimmed := strings.TrimSpace(lower)
	if _, ok := sessionIDs[trimmed]; ok {
		return true
	}
	if _, ok := sessionTimes[trimmed]; ok {
		return true
	}
	for id := range sessionIDs {
		if len(id) >= 7 && strings.Contains(lower, id) {
			return true
		}
	}
	if fileAnchorResolves(anchor, filePaths, repoDir) {
		return true
	}
	for timestamp := range sessionTimes {
		if strings.Contains(lower, timestamp) {
			return true
		}
	}
	for _, tok := range strings.FieldsFunc(lower, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	}) {
		if _, ok := sessionIDs[tok]; ok {
			return true
		}
		if len(tok) >= 7 {
			for hash := range commitHashes {
				if strings.HasPrefix(hash, tok) || strings.HasPrefix(tok, hash) {
					return true
				}
			}
		}
	}
	return false
}

func fileAnchorResolves(anchor string, filePaths map[string]struct{}, repoDir string) bool {
	clean, ok := cleanRepoEvidencePath(anchor)
	if !ok {
		return false
	}
	if _, ok := filePaths[strings.ToLower(clean)]; ok {
		return true
	}
	if repoDir == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(repoDir, filepath.FromSlash(clean)))
	return err == nil && !info.IsDir()
}

func cleanRepoEvidencePath(anchor string) (string, bool) {
	candidate := strings.TrimSpace(anchor)
	candidate = strings.Trim(candidate, "`'\"")
	candidate = strings.TrimPrefix(candidate, "file:")
	if candidate == "" {
		return "", false
	}
	clean := filepath.Clean(filepath.FromSlash(candidate))
	if filepath.IsAbs(clean) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(clean), true
}

// sortedAgentHistogram renders an agent histogram as a deterministic
// "agent×count" slice, most-used first then alphabetical.
func sortedAgentHistogram(histogram map[string]int) []string {
	type pair struct {
		agent string
		count int
	}
	pairs := make([]pair, 0, len(histogram))
	for a, c := range histogram {
		pairs = append(pairs, pair{a, c})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].count != pairs[j].count {
			return pairs[i].count > pairs[j].count
		}
		return pairs[i].agent < pairs[j].agent
	})
	out := make([]string, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, fmt.Sprintf("%s×%d", p.agent, p.count))
	}
	return out
}

// truncateString cuts value to at most max bytes on a rune boundary, appending
// an ellipsis when it truncates.
func truncateString(value string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(value) <= max {
		return value
	}
	limit := max
	suffix := ""
	if max > 3 {
		limit = max - 3
		suffix = "..."
	}
	cut := limit
	for cut > 0 && !utf8RuneStart(value[cut]) {
		cut--
	}
	return value[:cut] + suffix
}

func utf8RuneStart(b byte) bool { return b&0xC0 != 0x80 }
