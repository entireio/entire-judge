package judge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/suhaanthayyil/entire-judge/internal/agent"
	"github.com/suhaanthayyil/entire-judge/internal/brainstore"
	"github.com/suhaanthayyil/entire-judge/internal/gitutil"
)

// Params carries the per-submission run parameters shared across lenses.
type Params struct {
	Agent          string
	Model          string
	Effort         string
	HackathonStart time.Time
}

// Submit runs the full per-submission pipeline: assemble context, compute the
// deterministic lenses, run the LLM lenses, validate evidence, and assemble the
// report. It never hard-fails on missing optional data (no facts, no
// transcripts): those just shrink the brief and degrade the LLM lenses.
func Submit(ctx context.Context, runner gitutil.CommandRunner, run agent.Runner, repoDir, brainDir, repoKey string, params Params, now time.Time) (*RunReport, error) {
	now = now.UTC()

	sc, err := assembleSubmissionContext(ctx, runner, repoDir, brainDir, repoKey, params.HackathonStart)
	if err != nil {
		return nil, err
	}

	report := &RunReport{
		SchemaVersion: SchemaVersion,
		Kind:          "entire_judge_submission",
		Advisory:      true,
		Disclaimer:    Disclaimer,
		GeneratedAt:   now,
		SubmissionID:  SubmissionIDFromKey(repoKey),
		RepoDir:       repoDir,
		BrainPath:     brainDir,
		Deterministic: sc.Metrics,
		Run: RunMetadata{
			Agent:             params.Agent,
			Model:             params.Model,
			Effort:            params.Effort,
			PromptFingerprint: PromptFingerprint(params.Agent, params.Model, params.Effort),
			LLMStatus:         "ok",
		},
	}
	if !params.HackathonStart.IsZero() {
		v := params.HackathonStart.UTC()
		report.Run.HackathonStartedAt = &v
	}

	var llmFailures int

	// 1. authenticity — DETERMINISTIC score from the timeline rubric; the LLM only
	//    adds explanatory bullets and may never set the verdict/score.
	auth := authenticityLens(sc.Metrics)
	if llmAuth, ferr := runLens(ctx, sc, LensAuthenticity, templateAuthenticity, params.Agent, params.Model, params.Effort, run, LensTimeout); ferr == nil {
		mergeLLMBullets(&auth, llmAuth, sc)
	} else {
		llmFailures++
		auth.Warnings = append(auth.Warnings, "authenticity explanation unavailable: "+ferr.Error())
	}
	report.Lenses = append(report.Lenses, auth)

	// 2. prompting_skill — LLM-scored.
	report.Lenses = append(report.Lenses, runScoredLens(ctx, sc, LensPrompting, templatePrompting, params, run, &llmFailures))

	// 3. idea_plan_execution — LLM-scored.
	report.Lenses = append(report.Lenses, runScoredLens(ctx, sc, LensOutcome, templateOutcome, params, run, &llmFailures))

	// 4. effort_consistency — DETERMINISTIC.
	report.Lenses = append(report.Lenses, effortLens(sc.Metrics))

	// 5. agent_leverage — descriptive category, unscored.
	report.Lenses = append(report.Lenses, agentLeverageLens(sc.Metrics))

	if llmFailures > 0 {
		report.Run.LLMStatus = fmt.Sprintf("degraded: %d lens(es) had no LLM output", llmFailures)
		report.Warnings = append(report.Warnings, "one or more LLM lenses produced no usable output; deterministic lenses are unaffected")
	}

	// 6. summary — a short overview (what they built, how it went). Narrative
	//    only, never scored; on LLM failure, store the deterministic fallback so
	//    JSON consumers get the same non-empty summary as the text/TUI renderers.
	if summary, serr := runSummary(ctx, sc, params, run); serr == nil && summary != "" {
		report.Summary = summary
	} else {
		report.Summary = ComposeSummary(report)
	}

	OrderLenses(report.Lenses)
	report.Composite, report.Flags = compositeAndFlags(report.Lenses, sc.Metrics)
	report.GradeProcess, report.GradeSolution, _ = Grades(report.Lenses)
	return report, nil
}

// assembleSubmissionContext loads the brain, computes metrics, and builds the
// bounded brief.
func assembleSubmissionContext(ctx context.Context, runner gitutil.CommandRunner, repoDir, brainDir, repoKey string, hackathonStart time.Time) (submissionContext, error) {
	sc := submissionContext{RepoDir: repoDir, BrainDir: brainDir, RepoKey: repoKey}

	manifest, err := brainstore.LoadManifest(brainDir)
	if err != nil {
		return sc, err
	}
	sc.Manifest = manifest
	sc.Sessions = manifest.SessionList()
	sc.Branch = manifest.Branch()
	if facts, ferr := brainstore.LoadFacts(brainDir, sc.Branch); ferr == nil {
		sc.Facts = facts
	}

	metrics, coverage := computeMetrics(ctx, runner, repoDir, manifest, brainDir, hackathonStart)

	// Optional entire-sem layer: what the team actually built (code structure).
	// Absent for brains built without the sem provider; the lenses degrade.
	if manifest.Sources != nil && manifest.Sources.Semantic != nil {
		if summary, serr := brainstore.LoadSemanticSummary(brainDir, manifest.Sources.Semantic); serr == nil && summary != nil {
			sc.Semantic = summary
			metrics.SemanticSymbols = summary.Symbols
			metrics.SemanticRelations = summary.Relations
			metrics.SemanticFiles = summary.Files
			metrics.SemanticCapabilities = summary.Capabilities
		}
	}

	sc.Metrics = metrics
	sc.Coverage = coverage

	sc.Brief = buildBrief(sc)
	return sc, nil
}

// runScoredLens runs an LLM-scored lens by template name (the template basename
// differs from the lens name), validates its evidence, and returns the result. A
// lens with zero valid anchors is marked unsupported.
func runScoredLens(ctx context.Context, sc submissionContext, lensName, templateName string, params Params, run agent.Runner, llmFailures *int) LensResult {
	result, err := runLens(ctx, sc, lensName, templateName, params.Agent, params.Model, params.Effort, run, LensTimeout)
	result.Lens = lensName
	if err != nil {
		*llmFailures++
		result.Warnings = append(result.Warnings, "lens unavailable: "+err.Error())
		return result
	}
	if result.Score == nil && len(result.Warnings) > 0 {
		*llmFailures++
	}
	validateLensEvidence(&result, sc)
	return result
}

// mergeLLMBullets folds the LLM authenticity explanation into the deterministic
// authenticity result WITHOUT letting it change the score or verdict. Only
// evidence-validated bullets/anchors survive.
func mergeLLMBullets(det *LensResult, llm LensResult, sc submissionContext) {
	validateLensEvidence(&llm, sc)
	det.Bullets = dedupeStrings(append(det.Bullets, llm.Bullets...))
	det.Evidence = dedupeStrings(append(det.Evidence, llm.Evidence...))
	det.Warnings = append(det.Warnings, llm.Warnings...)
	if len(det.Evidence) > 0 {
		det.Supported = true
	}
}

// PromptFingerprint hashes the lens templates together with the run parameters so
// a report records exactly which prompts produced it. A template read failure is
// folded into the hash as an empty body rather than failing.
func PromptFingerprint(agentName, model, effort string) string {
	h := sha256.New()
	for _, name := range []string{templateAuthenticity, templatePrompting, templateOutcome} {
		body, _ := loadTemplate(name)
		fmt.Fprintf(h, "%s\x00%s\x00", name, body)
	}
	fmt.Fprintf(h, "agent=%s\x00model=%s\x00effort=%s", agentName, model, effort)
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// SubmissionIDFromKey derives a "gh/<owner>/<repo>" submission id from a repo
// storage key. Keys are stored slash-joined; the trailing host/owner/repo shape
// is kept and prefixed with "gh/" when missing so the id is stable and readable.
func SubmissionIDFromKey(key string) string {
	key = strings.Trim(filepath.ToSlash(key), "/")
	if key == "" {
		return "gh/unknown/unknown"
	}
	parts := strings.Split(key, "/")
	if len(parts) >= 3 {
		tail := parts[len(parts)-3:]
		return "gh/" + strings.Join(tail[len(tail)-2:], "/")
	}
	if len(parts) == 2 {
		return "gh/" + strings.Join(parts, "/")
	}
	return "gh/unknown/" + parts[len(parts)-1]
}
