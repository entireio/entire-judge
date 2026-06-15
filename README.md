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
entire judge run [path] [--agent claude-code|codex|ollama|command] [--model M] [--effort E] [--started-at RFC3339] [--json] [--plain]
entire judge rank [path] [--agent claude-code|codex|ollama|command] [--model M] [--effort E] [--started-at RFC3339] [--json] [--plain]
entire judge watch [path] [--agent claude-code|codex|ollama|command] [--model M] [--effort E] [--started-at RFC3339]
entire judge version

# All three take a [path]; the verb decides what happens to it.
# `run` scores one submission repo (detailed report); `rank` and `watch` take a
# directory of submission repos (ranked table / interactive view).
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
git clone https://github.com/suhaanthayyil/entire-judge.git
cd entire-judge
mise run build
entire plugin install ./entire-judge --force
```

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
- **idea_plan_execution** — LLM-scored (0–5). The strength of the concept and how
  coherently it was carried from intent to shipped work.
- **effort_consistency** — deterministic. Rewards sustained, multi-session work
  over a single burst.
- **agent_leverage** — descriptive (unscored). Which agents the team leaned on.

The composite weights authenticity (0.30), idea/plan/execution (0.30),
prompting_skill (0.20), and effort_consistency (0.20); `agent_leverage` is
descriptive and excluded. A missing LLM lens is renormalized out of the composite
rather than deflating it.

### Anti-fabrication

The deterministic block always computes. LLM lenses **degrade gracefully** — when
the agent is unavailable, the lens is marked `supported: false` with a warning and
the deterministic lenses are unaffected. Every LLM evidence anchor is validated
against the brain (a real session id or commit hash); unresolvable anchors are
dropped, and a lens with zero valid anchors is excluded from the composite.

### Ranking

`rank` discovers every brain one level under a directory, scores each, and orders
them by composite. Submissions caught by a **hard gate** (predates the event, no
session history, or an insufficient brain) are listed separately and **not**
averaged into the ordered table. A fairness footer states which lenses are
deterministic versus LLM-scored.

## No Egress

`entire-judge` honors the brain's local-only mode. When
`ENTIRE_BRAIN_NO_EGRESS` / `ENTIRE_BRAIN_LOCAL_ONLY` is set, the `codex`,
`claude-code`, and `command` lens agents are rejected (they can send brain context
off-host); use `--agent ollama`, which is pinned to a loopback-only endpoint.

## Output

- `--json` emits the machine-readable report (per-submission or ranking).
- `--plain` emits a rendered text summary with score bars.
- Without either, on a TTY the `run`/`rank`/`watch` commands open an interactive
  browser; on a non-TTY they fall back to the rendered text summary.

## Example

```sh
ENTIRE_REPO_ROOT=/path/to/repo \
ENTIRE_PLUGIN_DATA_DIR=/path/to/plugin/data \
  entire-judge run /path/to/repo --agent codex --plain --started-at 2026-01-05T17:00:00Z
```

```text
Submission: gh/example/project
Brain: /path/to/plugin/data/repos/gh/example/project
Composite: 4.70/5
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
- `internal/cli` — the command tree (`run`, `rank`, `watch`, `version`),
  environment plumbing, and repo-key derivation.
- `internal/judge` — the lens set, metrics math, scoring, composite/hard gates,
  the report schema, and the embedded lens templates.
- `internal/brainstore` — the minimal, self-contained reader for the brain's
  on-disk manifest, transcripts, and facts.
- `internal/gitutil` — git execution and the commit-history coverage classifier.
- `internal/agent` — the lens-agent argv builders and the no-egress-aware runner.
- `internal/tui` — the interactive submission browser.
```
