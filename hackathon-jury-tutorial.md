# Hackathon Jury Tutorial: Score Submissions With Entire Brain, Sem, and Judge

This is a practical runbook for a hackathon jury that wants to use Entire to
score submitted repositories. The short version:

1. Install the Entire CLI.
2. Install three plugins: `entire-sem`, `entire-brain`, and `entire-judge`.
3. Clone each submission and fetch its Entire checkpoint history.
4. Build a full brain for each submission.
5. Run `entire judge rank` to produce an advisory jury board.

The main jury-facing tool is `entire judge`. It depends on a local
`entire brain` for each submission, and the brain uses `entire-sem` to add a
semantic "what was built" layer when the semantic provider is installed.

## What Each Piece Does

`entire` is the base CLI. Teams use it during the hackathon so their AI coding
sessions, prompts, checkpoints, and commit links are captured.

`entire-sem` is the semantic provider. It parses the code and emits symbols,
routes, workflow sections, tool handlers, imports, calls, and related local code
facts. The jury does not usually run it directly; `entire brain refresh` invokes
it.

`entire-brain` builds a local, inspectable brain for one repository. It exports
Entire session history, builds local history/docs indexes, and records semantic
snapshots from `entire-sem`.

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

git clone https://github.com/suhaanthayyil/entire-sem.git
cd entire-sem
mise install
mise run check
mise run build
entire plugin install ./entire-sem --force
entire sem version

cd ..
git clone https://github.com/ashtom/entire-brain.git
cd entire-brain
mise install
mise run check
mise run build
entire plugin install ./entire-brain --force
entire brain --help

cd ..
git clone https://github.com/suhaanthayyil/entire-judge.git
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
entire sem doctor --json
entire brain --help
entire judge rank --help
```

The `entire sem doctor --json` output should include `"provider":"entire-sem"`
and `"no_egress":true`.

Alternative Go install path, once the repositories are public and the jury
machine has access to them:

```sh
go install github.com/suhaanthayyil/entire-sem/cmd/entire-sem@latest
entire plugin install "$(go env GOPATH)/bin/entire-sem" --force

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
from `entire-sem`.

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
`entire sem` to add semantic context when the provider is installed.
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

If the semantic section is missing, verify `entire-sem` is installed:

```sh
entire sem version
entire sem doctor --json
cd "$SUBMISSIONS/team-a_project"
entire brain refresh --force --agent none
```

Large submissions can take minutes to refresh, especially if they have thousands
of checkpoints or many tracked source files. Build brains before live demos, then
save `rank --json` once and browse the saved board.

## Optional: Athens Sample Data

The Athens hackathon submissions are useful for a setup smoke test before a new
event. They are normal GitHub repos with Entire checkpoint refs:

```sh
ATHENS_REPOS=(
  https://github.com/omincron/pricemind-mvp.git
  https://github.com/entireio/cli.git
  https://github.com/danielkotsi/pantryPal.git
  https://github.com/nikolasgkou/gate-tpa.git
  https://github.com/Manolisgeo/agentbuilder.git
  https://github.com/galactica-labs/project-atlas.git
  https://github.com/Cavramoudis/msquared.git
  https://github.com/iraklisp98/NoteAI.git
  https://github.com/socratesomiliadis/syntheci-shipping.git
  https://github.com/Graffalo92/thalaios.git
  https://github.com/itsmichellecotter-cmyk/corporate-twin.git
)

for repo_url in "${ATHENS_REPOS[@]}"; do
  entire judge add "$repo_url" --dir "$SUBMISSIONS" --build=false
done
```

Then run the normal brain build, preflight, and rank steps below. The
`entireio/cli` sample is much larger than the others; use one smaller sample
first if you only want a quick plugin smoke test.

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

- `deterministic.semantic_symbols` is greater than zero when `entire-sem`
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

Open the saved board in the interactive dashboard without re-scoring:

```sh
entire judge watch "$REPORTS/board.json"
```

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

The main lenses are:

- `authenticity`: deterministic timeline classification using `--started-at`
- `prompting_skill`: LLM-scored from human prompt excerpts
- `idea_plan_execution`: LLM-scored; stronger when the brain has `entire-sem`
  semantic context showing what was actually built
- `effort_consistency`: deterministic session/turn/file activity metrics
- `agent_leverage`: descriptive, not included in the composite score

Submissions can be hard-gated into the Excluded tab if they predate the event,
lack session history, or have an insufficient brain. Excluded submissions should
still be reviewed by jurors, but they are not averaged into the ordered ranking.

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

After judging:

1. Save `reports/board.json`.
2. Save any single-submission JSON reports used for decisions.
3. Do not publish raw reports unless the event policy allows sharing prompts,
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
entire sem version
entire sem doctor --json
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
