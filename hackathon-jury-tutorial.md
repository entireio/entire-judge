# Hackathon Jury Tutorial: Score Submissions With Entire Brain, Sem, and Judge

This is a practical runbook for a hackathon jury that wants to use Entire to
score submitted repositories. The short version:

1. Install the Entire CLI.
2. Install three plugins: `entire-graph`, `entire-brain`, and `entire-judge`.
3. Clone each submission and fetch its Entire checkpoint history.
4. Build a full brain for each submission.
5. Run `entire judge rank` to produce an advisory jury board.

The main jury-facing tool is `entire judge`. It depends on a local
`entire brain` for each submission, and the brain uses `entire-graph` to add a
semantic "what was built" layer when the semantic provider is installed.

## What Each Piece Does

`entire` is the base CLI. Teams use it during the hackathon so their AI coding
sessions, prompts, checkpoints, and commit links are captured.

`entire-graph` is the semantic provider. It parses the code and emits symbols,
routes, workflow sections, tool handlers, imports, calls, and related local code
facts. The jury does not usually run it directly; `entire brain refresh` invokes
it.

`entire-brain` builds a local, inspectable brain for one repository. It exports
Entire session history, builds local history/docs indexes, and records semantic
snapshots from `entire-graph`.

`entire-judge` reads each submission repo plus its local brain, then produces
deterministic metrics and optional LLM-scored judge lenses. It can score one repo
or rank a directory of submissions in a terminal dashboard.

## Requirements

Install these on the jury machine:

- Git
- Go toolchain with CGO support
- Entire CLI
- Optional: `mise` for source installs
- Optional: `jq` for reading saved JSON reports
- Optional: Claude Code, Codex, or Ollama for the LLM judge lenses

Collect these from the event:

- The hackathon start time in RFC3339 format, for example
  `2026-06-27T09:00:00+02:00`
- Each team's Git repo URL
- Access to each team's Entire checkpoint history

Teams must have used Entire during the event. At minimum, their code repo must
include or expose `refs/heads/entire/*`, usually
`refs/heads/entire/checkpoints/v1`. If a team configured a separate checkpoint
remote, the jury machine needs access to that remote too.

## Install The Entire CLI

Install the stable CLI with the official install script:

```sh
curl -fsSL https://entire.io/install.sh | bash
```

For the nightly channel:

```sh
curl -fsSL https://entire.io/install.sh | bash -s -- --channel nightly
```

The install script resolves to `entireio/cli`, downloads the matching release
archive, verifies its checksum, installs `entire` into `~/.local/bin`, and tells
you if your shell `PATH` needs to be updated.

If this is a first-time install, reload your shell or add the install directory
for the current terminal:

```sh
export PATH="$HOME/.local/bin:$PATH"
```

Verify:

```sh
entire version
entire plugin --help
```

## Install The Plugins

Use the source install path when testing the newest plugin builds. This path
assumes `mise` is installed; if it is not, use the Go install path below once the
repositories are public.

Note: these are the current repository URLs. After the repos move into the
`entireio` GitHub org, replace the URLs with the org versions.

```sh
mkdir -p ~/entire-jury-tools
cd ~/entire-jury-tools

git clone https://github.com/entireio/entire-graph.git
cd entire-graph
mise install
mise run check
mise run build
entire plugin install ./entire-graph --force
entire graph version

cd ..
git clone https://github.com/entireio/entire-brain.git
cd entire-brain
mise install
mise run check
mise run build
entire plugin install ./entire-brain --force
entire brain --help

cd ..
git clone https://github.com/entireio/entire-judge.git
cd entire-judge
mise install
mise run check
mise run build
entire plugin install ./entire-judge --force
entire judge version
```

Check that Entire can see the plugins:

```sh
entire plugin list
entire graph doctor --json
entire brain --help
entire judge rank --help
```

The `entire graph doctor --json` output should include `"provider":"entire-graph"`
and `"no_egress":true`.

## Check The Judge Agent

`entire judge` can always run the deterministic lenses (including
`cli_awareness`), but the `prompting_skill`, `idea_plan_execution`, `integrity`,
and generated `summary` fields need a judge agent. If you plan to use Claude Code,
make sure the same terminal that runs `entire judge` can see a logged-in `claude`
binary:

```sh
export PATH="$HOME/.local/bin:$PATH"
claude --version
claude --print --no-session-persistence \
  --setting-sources user \
  --strict-mcp-config \
  --mcp-config '{"mcpServers":{}}' \
  --disable-slash-commands \
  --permission-mode dontAsk \
  --tools '' \
  --system-prompt 'Reply only with OK.' <<'EOF'
hello
EOF
```

The final line should be `OK`. If it says `Not logged in`, run `claude` once in
that terminal and complete `/login`.

For local-only judging, verify Ollama before the event:

```sh
ollama --version
ollama list
```

Pick an installed coding or instruction model for the `--model` flag.

Alternative Go install path, once the repositories are public and the jury
machine has access to them:

```sh
go install github.com/entireio/entire-graph/cmd/entire-graph@latest
entire plugin install "$(go env GOPATH)/bin/entire-graph" --force

go install github.com/ashtom/entire-brain/cmd/entire-brain@latest
entire plugin install "$(go env GOPATH)/bin/entire-brain" --force

go install github.com/suhaanthayyil/entire-judge/cmd/entire-judge@latest
entire plugin install "$(go env GOPATH)/bin/entire-judge" --force
```

## Prepare A Jury Workspace

Create one working directory for the event:

```sh
mkdir -p ~/paris-hackathon-jury/submissions
mkdir -p ~/paris-hackathon-jury/reports
cd ~/paris-hackathon-jury

export EVENT_START="2026-06-27T09:00:00+02:00"
export SUBMISSIONS="$PWD/submissions"
export REPORTS="$PWD/reports"
```

Use the actual event start time. `--started-at` is important because the
authenticity lens uses it to classify whether work started cleanly during the
hackathon, straddled the start, or predates the event.

Keep real Git checkout directories directly under `$SUBMISSIONS`. Do not put
symlinks there; `entire judge rank "$SUBMISSIONS"` discovers the directory
itself if it is a Git checkout, or immediate child directories that are Git
checkouts.

## Add Submissions

For each team repo, run:

```sh
entire judge add https://github.com/team-a/project.git \
  --dir "$SUBMISSIONS" \
  --build=false
```

Repeat for every team. `entire judge add` clones the repository and fetches
`refs/heads/entire/*` from `origin`.

This tutorial uses `--build=false` on purpose. The built-in `judge add` build
step creates the session export, but the best scoring path is to run a full
`entire brain refresh` afterward so the brain also includes the semantic layer
from `entire-graph`.

If you already cloned submissions yourself, fetch checkpoint history manually:

```sh
cd "$SUBMISSIONS/team-a_project"
git fetch origin '+refs/heads/entire/*:refs/heads/entire/*'
```

If that fetch fails, stop and get access to the checkpoint remote before
scoring. A submission with no exported Entire session history can still be
inspected by humans, but `entire judge rank` will exclude it from the ordered
table.

## Build The Brains

Run a full brain refresh inside each submission repo:

```sh
failed=0
for repo in "$SUBMISSIONS"/*; do
  [ -d "$repo/.git" ] || continue
  echo "Building brain for $repo"
  if ! (
    cd "$repo"
    entire brain refresh --agent none
  ); then
    echo "Brain refresh failed for $repo" >&2
    failed=1
  fi
done

[ "$failed" -eq 0 ] || {
  echo "One or more brains failed to build. Fix checkpoint access before scoring." >&2
  exit 1
}
```

`--agent none` keeps brain building deterministic and token-free. The refresh
still exports sessions, builds local history/docs indexes, and runs
`entire graph` to add semantic context when the provider is installed.
If this step fails with missing session history, do not continue to ranking; fetch
the team's Entire checkpoint refs or checkpoint remote first.

Verify a submission brain:

```sh
cd "$SUBMISSIONS/team-a_project"
entire brain status
entire brain status --json > "$REPORTS/team-a.status.json"
```

In the status output, look for:

- Sessions/export data present
- Semantic section present
- No unsafe semantic freshness warning
- Any parse warnings or blind spots that jurors should know about

If the semantic section is missing, verify `entire-graph` is installed:

```sh
entire graph version
entire graph doctor --json
cd "$SUBMISSIONS/team-a_project"
entire brain refresh --force --agent none
```

Large submissions can take minutes to refresh, especially if they have thousands
of checkpoints or many tracked source files. Build brains before live demos, then
save `rank --json` once and browse the saved board.

## Preflight One Submission

Before scoring the whole event, prove the full stack works on one repo:

```sh
cd "$SUBMISSIONS/team-a_project"

entire brain refresh --agent none
entire brain status --json > "$REPORTS/team-a.status.json"

entire judge run "$PWD" \
  --started-at "$EVENT_START" \
  --agent claude-code \
  --json > "$REPORTS/team-a.judge.json"
```

If `jq` is installed, check the saved JSON directly:

```sh
jq -e '.kind == "entire_judge_submission"
  and .brain_path != ""
  and (.summary | length > 0)' \
  "$REPORTS/team-a.judge.json" >/dev/null
```

Open the saved JSON and also check:

- `deterministic.semantic_symbols` is greater than zero when `entire-graph`
  indexed the repo

If `entire brain refresh` fails with missing session history, fix checkpoint
access before scoring. If refresh succeeds but the judge report says
`insufficient_brain` or `no_session_history`, the tool is working, but the
submission does not have enough exported Entire history for an ordered score.

## Score One Submission

Use `run` when you want a detailed report for one team:

```sh
entire judge run "$SUBMISSIONS/team-a_project" \
  --started-at "$EVENT_START" \
  --agent claude-code \
  --plain
```

To save JSON:

```sh
entire judge run "$SUBMISSIONS/team-a_project" \
  --started-at "$EVENT_START" \
  --agent claude-code \
  --json > "$REPORTS/team-a.judge.json"
```

`--agent claude-code` is the default lens agent. You can also use:

```sh
entire judge run "$SUBMISSIONS/team-a_project" \
  --started-at "$EVENT_START" \
  --agent codex \
  --plain
```

For local-only judging with Ollama:

```sh
ollama pull llama3.1

ENTIRE_BRAIN_NO_EGRESS=1 entire judge run "$SUBMISSIONS/team-a_project" \
  --started-at "$EVENT_START" \
  --agent ollama \
  --model llama3.1 \
  --plain
```

## Rank All Submissions

Score every submission and print a plain ranking:

```sh
entire judge rank "$SUBMISSIONS" \
  --started-at "$EVENT_START" \
  --agent claude-code \
  --plain
```

Save the full board once:

```sh
entire judge rank "$SUBMISSIONS" \
  --started-at "$EVENT_START" \
  --agent claude-code \
  --json > "$REPORTS/board.json"
```

Scoring is LLM-latency-bound, so `--jobs N` (default 4) scores N submissions
concurrently and cuts wall-clock close to linearly — useful for a large field.
Raise it if your agent's rate limits allow; the saved board is identical to a
sequential run.

```sh
entire judge rank "$SUBMISSIONS" \
  --started-at "$EVENT_START" \
  --agent claude-code \
  --jobs 4 \
  --json > "$REPORTS/board.json"
```

Open the saved board in the interactive dashboard without re-scoring:

```sh
entire judge watch "$REPORTS/board.json"
```

Export per-team feedback markdown for **every** submission (winners and
non-winners). Prefer the saved board so no LLM calls are re-run:

```sh
entire judge feedback "$REPORTS/board.json" --out "$REPORTS/feedback" --winners 3
```

`--winners N` only changes the greeting header; every ranked and excluded team
still gets a `feedback-<slug>.md` file with summary, lens notes, evidence, and
the `cli_awareness` breakdown.

`rank`, `run`, and `watch` open the TUI only when stdout is an interactive
terminal. Use `--json` for scripts and `--plain` for copyable text output.

Useful dashboard keys:

- `j` / `k` or arrow keys: move between submissions
- `enter` or right arrow: focus the detail page
- `esc` or left arrow: return to the list
- `tab`: switch between Ranked and Excluded
- `/`: filter submissions
- `?`: help
- `q`: quit

## How To Read The Scores

The board is advisory. It helps the jury inspect evidence faster; it does not
replace human judgment.

### Two component grades and a combined total

The headline score is built like a multi-component score (think technical +
presentation in judged sports): two component grades, each 0–5, then their
equal-weighted mean as the **Combined** total that orders the board.

- **Grade A — Process** (how they worked): the mean of the supported process
  lenses — `authenticity`, `prompting_skill`, `effort_consistency`,
  `cli_awareness`, and — **penalty-only** — `integrity`. `integrity` counts toward
  this grade *only* when it is a genuine flagged concern (real assistant warning,
  evidence-supported, score ≤ 2.0), so it can drag the grade down but a clean read
  never raises it. (A lens with no resolvable evidence is dropped from the mean,
  not counted as zero.)
- **Grade B — Solution** (what they built): the `idea_plan_execution` lens.
- **Combined** = the equal-weighted mean of whichever grades are present; if no
  judge agent is available (no-egress or the agent is down), Grade B (Solution) is
  `n/a` and Combined falls back to the Process grade alone. This is the `composite`
  field in `--json`, the `Score` column in the dashboard list, and the `Combined`
  line on each detail page (which also shows Grade A and Grade B).

The lenses behind the grades:

- `authenticity` (Process): deterministic timeline classification using
  `--started-at`.
- `prompting_skill` (Process): LLM-scored from human prompt excerpts.
- `effort_consistency` (Process): deterministic session/turn/file activity.
- `cli_awareness` (Process): deterministic (0–5). Counts Entire skill invocations
  (`Skill` tool / `/entire`), `entire …` shell commands (graph/brain/sem/judge/…),
  and Entire MCP tools mined from session transcripts. Higher scores reward
  sustained multi-capability CLI use — an advisory incentive for Entire CLI
  capability awareness. Spam/empty touches still register as a weak signal; the
  jury can discount via evidence bullets.
- `integrity` (Process): LLM-scored (0–5, low is bad). Detects when the assistant
  explicitly warned about a substantive integrity or validity problem — test-set
  contamination, train/test leakage, evaluating on training data, cheating,
  fabricated or hardcoded results, plagiarism, or a stated rules violation — and
  the team proceeded without addressing it. Ordinary code-review nits are not
  integrity concerns. A low, evidence-backed score raises an advisory red flag (see
  below).
- `idea_plan_execution` (Solution): LLM-scored as three sub-scores — `idea`,
  `plan`, and `execution` — averaged into the lens score. Three fractional
  sub-components (each 0–5) instead of one whole number let close submissions
  separate rather than clustering on the same integer. The `execution` sub-score
  weighs the `entire-graph` semantic layer ("what was built") heavily. idea, plan,
  and execution appear as their own `(B · solution)` bars on the detail page and in
  `judge run --plain`, and as the lens `components` array in `--json`.
- `agent_leverage`: descriptive, not scored and not part of any grade.

### Excluded submissions

A submission is hard-gated into the **Excluded** tab — and kept off the ordered
board entirely — when its timeline predates the event, has no session history, or
the brain is insufficient (e.g. a team that did not set up Entire correctly). Its
lens scores, including the LLM solution grade computed from its code, are still
recorded and viewable on its detail page, but it is not ranked and not averaged
into the ordering. Jurors should still review excluded entries by hand.

### Integrity red flags

The `integrity` lens can raise an advisory **red flag** — a banner on the detail
page, an `integrity_flag` / `integrity_reason` pair in `--json`, and a
`⚠ INTEGRITY FLAG: <reason> @<anchor>` line in `--plain`. The flag fires only when
the lens is evidence-supported (at least one validated anchor), scores ≤ 2.0, and
the brain actually contains an assistant turn with an integrity-warning keyword, so
a hallucinated low score cannot brand a clean team.

A red flag is **advisory, not a gate**. Unlike the timeline/history exclusions
above, a flagged submission stays in the ranked table with its normal score; it is
never auto-excluded. The jury decides what to do with it.

For example, one team's assistant warned mid-session that their retrieval and
evaluation pool was contaminated with test-set samples, so any accuracy numbers
would be invalid, and the team proceeded to demo those numbers anyway. The
`integrity` lens scores low, anchors the warning to the session where it appeared,
and raises the red flag — but the submission still ranks, and jurors weigh the flag
against the rest of the evidence.

## Recommended Event Workflow

Before demos:

1. Install and verify the three plugins.
2. Create the jury workspace.
3. Add each repo with `entire judge add --build=false`.
4. Build every brain with `entire brain refresh --agent none`.
5. Run `entire brain status` on suspicious repos.

During judging:

1. Run `entire judge rank --json > board.json` once.
2. Open the board with `entire judge watch board.json`.
3. Use the Ranked tab for the advisory ordering.
4. Use the Excluded tab for submissions with timeline/history problems.
5. Open individual reports for close calls with `entire judge run --plain`.
6. Export shareable notes with `entire judge feedback board.json --out feedback`.

After judging:

1. Save `reports/board.json`.
2. Save any single-submission JSON reports used for decisions.
3. Share `feedback/*.md` with teams (including non-winners) when the event policy
   allows.
4. Do not publish raw reports unless the event policy allows sharing prompts,
   transcripts, repository metadata, and semantic code facts.

## Troubleshooting

`brain_missing: no exported brain`

Run a full refresh inside that submission:

```sh
cd "$SUBMISSIONS/team-a_project"
entire brain refresh --agent none
```

No session history or the submission is excluded for missing history

The team may not have used Entire, or the checkpoint refs were not pushed or
not fetched. Try:

```sh
cd "$SUBMISSIONS/team-a_project"
git branch -a | grep 'entire/'
git fetch origin '+refs/heads/entire/*:refs/heads/entire/*'
entire brain refresh --agent none
```

Semantic data is missing

Check the provider and rebuild:

```sh
entire graph version
entire graph doctor --json
cd "$SUBMISSIONS/team-a_project"
entire brain refresh --force --agent none
entire brain status
```

Brain refresh complains about a dirty worktree

Submission checkouts should be clean before scoring:

```sh
cd "$SUBMISSIONS/team-a_project"
git status --short
```

Resolve or document any local edits before rebuilding the brain.

The LLM lenses fail or time out

The deterministic parts still run. If hosted agents are unavailable, use a local
Ollama model:

```sh
ENTIRE_BRAIN_NO_EGRESS=1 entire judge rank "$SUBMISSIONS" \
  --started-at "$EVENT_START" \
  --agent ollama \
  --model llama3.1 \
  --json > "$REPORTS/board.local.json"
```

The ranking takes a long time

`rank` runs LLM lenses for every submission before the dashboard opens. Save JSON
once, then browse the saved board:

```sh
entire judge rank "$SUBMISSIONS" --started-at "$EVENT_START" --json > "$REPORTS/board.json"
entire judge watch "$REPORTS/board.json"
```

## Team Setup Snippet

If teams ask how to make their repos judgeable during the hackathon, give them:

```sh
cd your-project
entire enable --agent claude-code

# Work normally with your agent and commit your code.
git push origin HEAD

# Make sure checkpoint history is available to the jury.
git push origin 'refs/heads/entire/*:refs/heads/entire/*'
```

If the team uses a separate checkpoint remote, make sure Peyton/Cole/the jury
account has access to that remote before scoring starts.
