// Package judge scores a hackathon submission's Entire brain. It computes
// deterministic, auditable metrics (timeline authenticity, effort, agent mix)
// and runs optional LLM "judge lenses" that produce evidence-anchored,
// advisory color. Every score is advisory — it informs a jury, it does not
// replace it.
package judge

import "time"

const (
	// SchemaVersion is the report schema version emitted in JSON.
	SchemaVersion = 1
	// Disclaimer rides on every report and ranking.
	Disclaimer = "Advisory only: these scores inform the jury's judgment, they do not replace it."

	// Lens names (also the report lens labels).
	LensAuthenticity = "authenticity"
	LensPrompting    = "prompting_skill"
	LensOutcome      = "idea_plan_execution"
	LensEffort       = "effort_consistency"
	LensAgent        = "agent_leverage"

	// Template basenames for the LLM lenses (differ from the lens names).
	templateAuthenticity = "authenticity"
	templatePrompting    = "prompting"
	templateOutcome      = "outcome"
	templateSummary      = "summary"

	// Timeline categories: the deterministic backbone of the authenticity lens.
	TimelineCleanStart        = "clean_start"
	TimelineMixed             = "mixed"
	TimelinePredatesEvent     = "predates_event"
	TimelineNoSessionHistory  = "no_session_history"
	TimelineInsufficientBrain = "insufficient_brain"
)

// Metrics is the deterministic measurement of one submission's brain. Everything
// here is computed from the export manifest, the durable facts, and the git
// history coverage — no LLM is involved, so these numbers are reproducible and
// auditable.
type Metrics struct {
	TimelineCategory string `json:"timeline_category"`
	TimelineReason   string `json:"timeline_reason"`

	Sessions      int `json:"sessions"`
	HumanPrompts  int `json:"human_prompts"`
	Turns         int `json:"turns"`
	FilesTouched  int `json:"files_touched"`
	Facts         int `json:"facts,omitempty"`
	ExportedCheck int `json:"exported_checkpoints"`

	InputTokens         int `json:"input_tokens"`
	OutputTokens        int `json:"output_tokens"`
	CacheReadTokens     int `json:"cache_read_tokens,omitempty"`
	CacheCreationTokens int `json:"cache_creation_tokens,omitempty"`

	TotalCommits          int `json:"total_commits"`
	PreSessionCommits     int `json:"pre_session_commits"`
	CoveredCommits        int `json:"covered_commits"`
	MissingSessionCommits int `json:"missing_session_commits"`
	NoSessionHistory      int `json:"no_session_history_commits"`
	MergeCommits          int `json:"merge_commits"`

	OldestSessionAt   *time.Time `json:"oldest_session_at,omitempty"`
	FirstSessionAt    *time.Time `json:"first_session_at,omitempty"`
	LastSessionAt     *time.Time `json:"last_session_at,omitempty"`
	TimeOnTaskMinutes float64    `json:"time_on_task_minutes"`

	FirstCommitAt              *time.Time `json:"first_commit_at,omitempty"`
	FirstCommitToFirstSession  float64    `json:"first_commit_to_first_session_minutes,omitempty"`
	FirstCommitBeforeFirstSess bool       `json:"first_commit_before_first_session"`

	AgentHistogram map[string]int `json:"agent_histogram,omitempty"`
	PrimaryAgent   string         `json:"primary_agent,omitempty"`

	// Semantic (entire-sem) layer, present only when the brain was built with the
	// sem provider. These describe what the team actually built (code structure),
	// feeding the execution lens; zero/empty when the brain has no sem layer.
	SemanticSymbols      int      `json:"semantic_symbols,omitempty"`
	SemanticRelations    int      `json:"semantic_relations,omitempty"`
	SemanticFiles        int      `json:"semantic_files,omitempty"`
	SemanticCapabilities []string `json:"semantic_capabilities,omitempty"`
}

// LensComponent is a named sub-score of a lens (e.g. the outcome lens's idea,
// plan, and execution), each 0-5. A lens whose Score is the mean of several
// sub-components records them here so the jury can see what drove the grade.
type LensComponent struct {
	Name  string  `json:"name"`
	Score float64 `json:"score"`
}

// LensResult is one lens's verdict on a submission. Score is a pointer so an
// unscored (descriptive) lens can omit it. Supported records whether any LLM
// evidence anchor resolved against the brain; an unsupported lens is advisory
// color only. Components, when present, are the sub-scores Score is the mean of.
type LensResult struct {
	Lens       string          `json:"lens"`
	Score      *float64        `json:"score,omitempty"`
	Components []LensComponent `json:"components,omitempty"`
	Verdict    string          `json:"verdict,omitempty"`
	Bullets    []string        `json:"bullets,omitempty"`
	Evidence   []string        `json:"evidence,omitempty"`
	Supported  bool            `json:"supported"`
	Warnings   []string        `json:"warnings,omitempty"`
	Raw        string          `json:"raw,omitempty"`
}

// RunMetadata records how a report was produced so a result is reproducible.
type RunMetadata struct {
	Agent              string     `json:"agent"`
	Model              string     `json:"model,omitempty"`
	Effort             string     `json:"effort,omitempty"`
	PromptFingerprint  string     `json:"prompt_fingerprint"`
	HackathonStartedAt *time.Time `json:"hackathon_started_at,omitempty"`
	LLMStatus          string     `json:"llm_status"`
}

// RunReport is the per-submission report.
type RunReport struct {
	SchemaVersion int         `json:"schema_version"`
	Kind          string      `json:"kind"`
	Advisory      bool        `json:"advisory"`
	Disclaimer    string      `json:"disclaimer"`
	GeneratedAt   time.Time   `json:"generated_at"`
	SubmissionID  string      `json:"submission_id"`
	RepoDir       string      `json:"repo_dir"`
	BrainPath     string      `json:"brain_path"`
	Run           RunMetadata `json:"run"`
	Deterministic Metrics     `json:"deterministic"`
	// GradeProcess (A) and GradeSolution (B) are the two component grades;
	// Composite is their combined total (their equal-weighted mean). See Grades.
	GradeProcess  *float64     `json:"grade_process,omitempty"`
	GradeSolution *float64     `json:"grade_solution,omitempty"`
	Composite     *float64     `json:"composite,omitempty"`
	Flags         []string     `json:"flags,omitempty"`
	Summary       string       `json:"summary,omitempty"`
	Lenses        []LensResult `json:"lenses"`
	Warnings      []string     `json:"warnings,omitempty"`
}

// RankReport ranks a directory of submissions.
type RankReport struct {
	SchemaVersion int            `json:"schema_version"`
	Kind          string         `json:"kind"`
	Advisory      bool           `json:"advisory"`
	Disclaimer    string         `json:"disclaimer"`
	GeneratedAt   time.Time      `json:"generated_at"`
	Run           RunMetadata    `json:"run"`
	Submissions   []RankEntry    `json:"submissions"`
	Excluded      []RankExcluded `json:"excluded,omitempty"`
	FairnessNotes []string       `json:"fairness_notes,omitempty"`
	Warnings      []string       `json:"warnings,omitempty"`
}

// RankEntry is one ranked submission.
type RankEntry struct {
	Rank         int        `json:"rank"`
	SubmissionID string     `json:"submission_id"`
	BrainPath    string     `json:"brain_path"`
	Composite    *float64   `json:"composite,omitempty"`
	Flags        []string   `json:"flags,omitempty"`
	Report       *RunReport `json:"report,omitempty"`
}

// RankExcluded records a submission held out of the ordered table by a hard gate.
type RankExcluded struct {
	SubmissionID string     `json:"submission_id"`
	BrainPath    string     `json:"brain_path"`
	Reason       string     `json:"reason"`
	Report       *RunReport `json:"report,omitempty"`
}
