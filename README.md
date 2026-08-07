# Entire Judge

`entire-judge` is an Entire CLI plugin that helps a hackathon jury evaluate
submissions using each team's local Entire brain — its exported sessions,
durable facts, and commit history.

It runs deterministic, auditable metrics plus optional LLM "judge lenses" that
produce a score, a verdict, and evidence-anchored bullets per submission, then
ranks a directory of brains into an advisory ordered table. **Every score is
advisory — it informs the jury's judgment, it does not replace it.**

This plugin builds a binary named `entire-judge`, which is invoked through Entire
as:

```sh
entire judge add <repo-url> [--dir DIR] [--entire-binary BIN] [--build] [--checkpoint-limit N]
entire judge run [path] [--agent claude-code|codex|ollama|command] [--model M] [--effort E] [--started-at RFC3339] [--theme NAME] [--json] [--plain]
entire judge rank [path] [--agent claude-code|codex|ollama|command] [--model M] [--effort E] [--started-at RFC3339] [--theme NAME] [--json] [--plain]
entire judge watch [path] [--agent claude-code|codex|ollama|command] [--model M] [--effort E] [--started-at RFC3339] [--theme NAME]
entire judge feedback [path] [--out DIR] [--winners N]   # export per-team markdown (incl. non-winners)
entire judge version

# `run` scores one submission repo; `rank`/`watch` take a directory of repos
# (or a saved board JSON). `feedback` writes one markdown file per team.
```

It is fully self-contained: it reads the brain's on-disk export directly and does
not import any brain-builder internals.

## Install

Requirements:

- Entire CLI
- Git
- Go toolchain

Install the plugin binary with Go, then copy it into Entire's managed plugin
directory:

```sh
go install github.com/suhaanthayyil/entire-judge/cmd/entire-judge@latest
entire plugin install "$(go env GOPATH)/bin/entire-judge" --force
entire judge version
```

## Install From Source

```sh
git clone https://github.com/entireio/entire-judge.git
cd entire-judge
mise run build
entire plugin install ./entire-judge --force
```

## Adding submissions

`entire judge add <repo-url>` readies a submission end to end: it clones the repo
into `--dir` (default `.`), fetches its Entire checkpoint history
(`refs/heads/entire/*`), and builds the brain by shelling out to
`entire brain refresh sessions` (override the binary with `--entire-binary`, or
pass `--build=false` to clone only). Point `rank`/`watch` at the directory of
added submissions to score them all.

```sh
entire judge add https://github.com/team/project --dir ./submissions
entire judge rank ./submissions --started-at <event-start>
```

## entire-graph (the execution signal)

`entire judge` reads the brain directly; it does **not** call entire-graph. But when
a brain was built with the sem provider (`entire brain refresh` records a
`sources.semantic` layer), the judge reads that snapshot straight from the brain
and feeds a "What was built" digest (symbol kinds, capabilities, busiest files)
into the **idea_plan_execution** lens — turning execution scoring from coarse
file/commit counts into a built-vs-planned comparison. Brains without a sem layer
score exactly as before.

## How It Works

`entire-judge` resolves a repository's brain at
`<ENTIRE_PLUGIN_DATA_DIR>/repos/<repo_key>`, where `repo_key` comes from the
brain manifest (or is derived `gh/<owner>/<repo>` from the repo's `origin`
remote). It then reads:

- the export **manifest** (sessions, agents, models, token usage, file lists,
  checkpoint ids),
- each session's **transcript** (to mine the human prompts the team actually
  wrote), and
- the optional **durable facts** store (a brain may have none — that is fine).

From that it computes a deterministic measurement of the submission and runs the
lenses.

### Lenses

- **authenticity** — deterministic. A timeline classifier places the work
  relative to the event start (`--started-at`): `clean_start`, `mixed`,
  `predates_event`, `no_session_history`, or `insufficient_brain`. The score is
  fixed by this rubric; an LLM may add explanatory bullets but can never move the
  score.
- **prompting_skill** — LLM-scored (0–5). How clearly and effectively the team
  directed their agent, read from the human-prompt excerpts.
- **idea_plan_execution** — LLM-scored as three sub-scores — **idea**, **plan**,
  and **execution** (each 0–5, fractional) — averaged into the lens score, so close
  submissions separate instead of clustering on one integer. When the brain carries
  an **entire-graph** layer (see below), the brief includes a "What was built"
  code-structure section so the `execution` sub-score weighs what the team actually
  built (symbol kinds, routes/tools/workflows, busiest files) against what they
  planned. The sub-scores are emitted as the lens `components` array.
- **integrity** — LLM-scored (0–5, where a *low* score is bad). Detects when the
  coding assistant **explicitly warned** about a substantive integrity or validity
  problem — test-set contamination, train/test leakage, evaluating on training
  data, cheating, fabricated or hardcoded results, plagiarism, or a stated rules
  violation — and the team **proceeded without addressing it**. Ordinary
  code-review nits and style suggestions are **not** integrity concerns. Evidence
  bullets are anchored to real session ids, timestamps, or commit hashes like the
  other LLM lenses. A low score raises a prominent advisory **red flag** (see
  Output).
- **effort_consistency** — deterministic. Rewards sustained, multi-session work
  over a single burst.
- **cli_awareness** — deterministic (0–5). Mines session transcripts for Entire
  skill invocations (`Skill` tool / `/entire`), `entire …` shell commands
  (graph/brain/sem/judge/…), and Entire MCP tools. Higher scores reward sustained,
  multi-capability CLI use — an advisory incentive for Entire CLI capability
  awareness. Empty/spam touches still count as a weak signal; the jury can discount
  via the evidence bullets.
- **agent_leverage** — descriptive (unscored). Which agents the team leaned on.

The headline score is built like a multi-component score (technical + presentation
in judged sports): two component grades, each 0–5, then their equal-weighted mean.
**Grade A (process)** is the mean of the supported process lenses — `authenticity`,
`prompting_skill`, `effort_consistency`, `cli_awareness`, and — **penalty-only** —
`integrity`. `integrity` feeds Grade A *only* when it is a genuine flagged concern
(the same condition that raises the red flag: a real assistant warning,
evidence-supported, score ≤ 2.0), so it can drag the grade down but a clean
`integrity` read never inflates it. **Grade B (solution)** is the
`idea_plan_execution` lens. The **composite** is the equal-weighted mean of
whichever of {Grade A, Grade B} are present — so a submission scored with no judge
agent still gets a Combined equal to its Process grade. A lens with no resolvable
evidence is dropped from its grade rather than counted as zero; `agent_leverage` is
descriptive and never scored.

### Summary

Each submission also carries a short, evidence-grounded **summary** — a 2–3
sentence overview of what the team built and how it went, written by the same
agent that drives the lenses. When no agent is available (no-egress, or the agent
is down) it falls back to a deterministic summary composed from the metrics and
lens verdicts, so the summary is never blank.

### Anti-fabrication

The deterministic block always computes. LLM lenses **degrade gracefully** — when
the agent is unavailable, the lens is marked `supported: false` with a warning and
the deterministic lenses are unaffected. Every LLM evidence anchor is validated
against the brain (a real session id, session timestamp, commit hash, or a
repo-relative file path present in the submission); unresolvable anchors are
dropped, and a lens with zero valid anchors is excluded from the composite.

The **integrity red flag** is guarded the same way: it fires only when the
`integrity` lens is evidence-supported (≥1 validated anchor), scores ≤ 2.0, **and**
the brain actually contains an assistant turn carrying an integrity-warning
keyword. A hallucinated low score alone cannot brand a clean team.

### Ranking

`rank` discovers every brain one level under a directory, scores each, and orders
them by composite. Submissions caught by a **hard gate** (predates the event, no
session history, or an insufficient brain) are listed separately and **not**
averaged into the ordered table. A fairness footer states which lenses are
deterministic versus LLM-scored. An **integrity red flag** is different: it is
**advisory only**. A flagged submission still appears in the ranked table and is
never auto-excluded — the jury decides. This is unlike the hard gates
(`predates_event`, `no_session_history`, insufficient brain), which *are* removed
from the ordering.

## No Egress

`entire-judge` honors the brain's local-only mode. When
`ENTIRE_BRAIN_NO_EGRESS` / `ENTIRE_BRAIN_LOCAL_ONLY` is set, the `codex`,
`claude-code`, and `command` lens agents are rejected (they can send brain context
off-host); use `--agent ollama`, which is pinned to a loopback-only endpoint.

## Output

- `--json` emits the machine-readable report (per-submission or ranking),
  including each lens's `components` (the outcome lens's idea/plan/execution
  sub-scores) and the `grade_process` / `grade_solution` / `composite` totals.
  When the integrity red flag fires it also carries an `integrity_flag` boolean and
  an `integrity_reason` string.
- `--plain` emits a rendered text summary with score bars (and the summary line);
  the per-submission report shows Grade A / Grade B / Combined and the outcome
  lens's idea/plan/execution sub-scores. A fired integrity flag adds a
  `⚠ INTEGRITY FLAG: <reason> @<anchor>` line.
- Without either, on a TTY the `run`/`rank`/`watch` commands open the interactive
  **dashboard**; on a non-TTY they fall back to the rendered text summary.

### Dashboard

The dashboard is a sidebar + detail layout: a ranked submission table on the left
and a per-repo **page** on the right with three sections — **Score** (the
**Combined** total with its **Grade A / Grade B** components and per-lens bars,
including the outcome lens's idea/plan/execution sub-scores), **Summary**, and
**Findings** (the deterministic metrics plus each lens's bullets and evidence
anchors). When the integrity red flag fires, the detail view shows a prominent
**banner** with its reason. Hard-gated submissions live in a separate **Excluded**
tab so the jury sees them with their gate reason; an integrity-flagged submission
is *not* excluded — it stays in the ranked table with its banner.

- Navigate: `↑/↓` or `j/k` move the selection (the page follows); `enter`/`→`
  focuses the page to scroll it; `esc`/`←` returns to the list.
- `tab` switches the Ranked / Excluded sections; `/` filters submissions by id;
  `?` toggles full help; `q` quits.
- `--theme` (or `ENTIRE_JUDGE_THEME`) selects a color theme: `default`,
  `catppuccin`, `gruvbox`, or `tokyonight`.

`rank`/`watch` run the LLM lenses for every submission *before* the dashboard
opens (progress is printed to stderr while scoring). To avoid re-scoring on every
launch, **score once and browse instantly**:

```sh
entire judge rank ./submissions --started-at <t> --json > board.json   # score once
entire judge watch board.json                                          # opens instantly, no agent
entire judge feedback board.json --out ./feedback --winners 3          # markdown for every team
```

`watch`/`rank` accept either a directory of repos (score live) or a saved
`--json` report file (load and open immediately).

### Feedback for every team (including non-winners)

`entire judge feedback` writes one `feedback-<slug>.md` file per ranked **and**
excluded submission. Prefer feeding it a saved `rank --json` board so no LLM
calls are re-run — each file reuses that report's Summary, lens verdicts/bullets,
evidence anchors, and the `cli_awareness` breakdown. `--winners N` only changes
the greeting ("Congratulations" vs "Keep building"); **all teams still get a
file**.

## Example

```sh
ENTIRE_REPO_ROOT=/path/to/repo \
ENTIRE_PLUGIN_DATA_DIR=/path/to/plugin/data \
  entire-judge run /path/to/repo --agent codex --plain --started-at 2026-01-05T17:00:00Z
```

```text
Submission: gh/example/project
Brain: /path/to/plugin/data/repos/gh/example/project
Combined: 4.75/5  (Grade A process 4.75 · Grade B solution n/a)
Flags: idea_plan_execution_unscored, prompting_skill_unscored
Agent: codex  LLM: degraded: 2 lens(es) had no LLM output

█████ 4.5/5 authenticity
  Work authentically began at or after the event start.
  - Timeline category: clean_start (all captured session activity began at or after the hackathon start).
  - 225 sessions, 4966 commits (12 pre-session, 124 covered).

█████ 5.0/5 effort_consistency
  225 sessions over 187594 minutes, 449 turns, 346 files touched.

Advisory only: these scores inform the jury's judgment, they do not replace it.
```

## Layout

- `cmd/entire-judge` — the binary entry point.
- `internal/cli` — the command tree (`run`, `rank`, `watch`, `feedback`, `version`),
  environment plumbing, and repo-key derivation.
- `internal/judge` — the lens set (incl. deterministic `cli_awareness`), metrics
  math, scoring, composite/hard gates, the report schema, and the embedded lens
  templates.
- `internal/brainstore` — the minimal, self-contained reader for the brain's
  on-disk manifest, transcripts (incl. Entire skill/CLI signal extraction), and
  facts.
- `internal/gitutil` — git execution and the commit-history coverage classifier.
- `internal/agent` — the lens-agent argv builders and the no-egress-aware runner.
- `internal/tui` — the interactive dashboard (ranked table, per-repo page,
  sections, filter, help, and the theme registry).
```
