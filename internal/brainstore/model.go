// Package brainstore reads an Entire brain export from disk with a minimal,
// self-contained data model. It deliberately defines only the fields the judge
// needs from the brain's on-disk JSON, so the judge plugin never imports the
// brain builder's internals.
package brainstore

import "time"

// SchemaVersion is the brain manifest schema this reader targets. A brain with
// a different schema still parses (the reader is permissive about extra/missing
// fields) — this constant only seeds a synthesized manifest when none exists.
const SchemaVersion = 3

// TokenUsage mirrors the manifest's per-session token accounting.
type TokenUsage struct {
	InputTokens         int `json:"input_tokens"`
	OutputTokens        int `json:"output_tokens"`
	CacheReadTokens     int `json:"cache_read_tokens,omitempty"`
	CacheCreationTokens int `json:"cache_creation_tokens,omitempty"`
}

// Summary mirrors the manifest's per-session intent/outcome summary.
type Summary struct {
	Intent  string `json:"intent,omitempty"`
	Outcome string `json:"outcome,omitempty"`
}

// Session is one exported session in the brain. Only fields the judge reads are
// modeled; unknown JSON fields are ignored on unmarshal.
type Session struct {
	SessionID        string      `json:"session_id"`
	Branch           string      `json:"branch,omitempty"`
	Agent            string      `json:"agent,omitempty"`
	Model            string      `json:"model,omitempty"`
	LatestCheckpoint string      `json:"latest_checkpoint_id,omitempty"`
	CreatedAt        time.Time   `json:"created_at"`
	FilesTouched     []string    `json:"files_touched,omitempty"`
	TokenUsage       *TokenUsage `json:"token_usage,omitempty"`
	Summary          *Summary    `json:"summary,omitempty"`
	CheckpointsCount int         `json:"checkpoints_count,omitempty"`
	TranscriptPath   string      `json:"transcript_path,omitempty"`
}

// SessionSource is the structured session source inside manifest.sources.
type SessionSource struct {
	GeneratedAt        time.Time  `json:"generated_at"`
	DefaultBranch      string     `json:"default_branch,omitempty"`
	CheckpointsScanned int        `json:"checkpoints_scanned,omitempty"`
	OldestSessionAt    *time.Time `json:"oldest_session_at,omitempty"`
	Sessions           []Session  `json:"sessions"`
}

// SemanticSource is the brain's optional entire-sem layer (built by
// `entire brain refresh` when the sem provider is available). It records the
// code-structure snapshot the judge can read to see what a team actually built.
type SemanticSource struct {
	GeneratedAt  time.Time `json:"generated_at"`
	Provider     string    `json:"provider,omitempty"`
	Commit       string    `json:"commit,omitempty"`
	SnapshotPath string    `json:"snapshot_path,omitempty"`
	Symbols      int       `json:"symbols"`
	Relations    int       `json:"relations"`
	Files        int       `json:"files,omitempty"`
	Capabilities []string  `json:"capabilities,omitempty"`
}

// Sources is the brain's per-source manifest container. The judge needs the
// session source and the optional semantic (entire-sem) source; other sources
// (seed, history, facts, docs) are ignored.
type Sources struct {
	Sessions *SessionSource  `json:"sessions,omitempty"`
	Semantic *SemanticSource `json:"semantic,omitempty"`
}

// Manifest is the brain export manifest. The judge prefers the structured
// session source (Sources.Sessions) over the flat Sessions field, mirroring the
// brain builder's own precedence.
type Manifest struct {
	SchemaVersion int       `json:"schema_version"`
	GeneratedAt   time.Time `json:"generated_at"`
	RepoKey       string    `json:"repo_key,omitempty"`
	DefaultBranch string    `json:"default_branch,omitempty"`
	Sessions      []Session `json:"sessions,omitempty"`
	Sources       *Sources  `json:"sources,omitempty"`
}

// SessionList returns the authoritative session list for the brain, preferring
// the structured session source over the flat manifest field.
func (m *Manifest) SessionList() []Session {
	if m == nil {
		return nil
	}
	if m.Sources != nil && m.Sources.Sessions != nil && m.Sources.Sessions.Sessions != nil {
		return m.Sources.Sessions.Sessions
	}
	return m.Sessions
}

// OldestSessionAt returns the structured source's oldest-session timestamp when
// present, else the earliest CreatedAt across the session list.
func (m *Manifest) OldestSessionAt() *time.Time {
	if m == nil {
		return nil
	}
	if m.Sources != nil && m.Sources.Sessions != nil && m.Sources.Sessions.OldestSessionAt != nil {
		v := m.Sources.Sessions.OldestSessionAt.UTC()
		return &v
	}
	var oldest time.Time
	for _, s := range m.SessionList() {
		if s.CreatedAt.IsZero() {
			continue
		}
		if oldest.IsZero() || s.CreatedAt.Before(oldest) {
			oldest = s.CreatedAt.UTC()
		}
	}
	if oldest.IsZero() {
		return nil
	}
	return &oldest
}

// ExportedCheckpointIDs returns the set of latest-checkpoint ids referenced by
// the brain's sessions. It is the input the git-history coverage classifier uses
// to decide whether a commit's checkpoint trailer was actually exported.
func (m *Manifest) ExportedCheckpointIDs() map[string]struct{} {
	ids := map[string]struct{}{}
	for _, s := range m.SessionList() {
		if s.LatestCheckpoint != "" {
			ids[s.LatestCheckpoint] = struct{}{}
		}
	}
	return ids
}

// FactProvenance cites the source a fact was derived from.
type FactProvenance struct {
	SessionID  string `json:"session_id,omitempty"`
	Commit     string `json:"commit,omitempty"`
	Transcript string `json:"transcript,omitempty"`
	Line       int    `json:"line,omitempty"`
}

// FactRecord is one durable statement in the brain's facts store. Only the
// fields the judge reads are modeled.
type FactRecord struct {
	ID         string           `json:"id"`
	Paths      []string         `json:"paths"`
	Kind       string           `json:"kind,omitempty"`
	Text       string           `json:"text"`
	Provenance []FactProvenance `json:"provenance,omitempty"`
}
