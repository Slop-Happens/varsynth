# Varsynth

Varsynth is a Go CLI for running a bug-fix synthesis pipeline against a local
git repository.

The current implementation supports:

- offline issue ingestion from a normalized `issue.json`
- repository/bootstrap context generation
- candidate worktree creation for four repair lenses
- stub-backed candidate execution
- opt-in Codex-backed candidate execution
- prompt artifact generation for each repair lens
- validation command execution per candidate
- JSON artifact generation

## Requirements

- Go 1.25+
- `git`

## Demo Target

This repo is currently set up to run against the local demo repository at:

`/Users/dominikhobel/Desktop/Test`

That repo contains:

- intentionally faulty Go code
- a matching normalized issue file at `/Users/dominikhobel/Desktop/Test/issue.json`

## Run

From the `varsynth` repo root:

```sh
go run ./cmd/varsynth \
  --repo /Users/dominikhobel/Desktop/Test \
  --issue-file /Users/dominikhobel/Desktop/Test/issue.json \
  --test-command "go test ./..." \
  --out ./out/demo
```

## Dry Run

To run only the bootstrap/context stage:

```sh
go run ./cmd/varsynth \
  --repo /Users/dominikhobel/Desktop/Test \
  --issue-file /Users/dominikhobel/Desktop/Test/issue.json \
  --test-command "go test ./..." \
  --out ./out/demo \
  --dry-run
```

## Preserve Worktrees

By default, candidate worktrees are cleaned up after the run.

To keep them for inspection:

```sh
go run ./cmd/varsynth \
  --repo /Users/dominikhobel/Desktop/Test \
  --issue-file /Users/dominikhobel/Desktop/Test/issue.json \
  --test-command "go test ./..." \
  --out ./out/demo \
  --preserve-worktrees
```

## Codex Agent

Varsynth defaults to the offline stub agent so demos and tests do not require
live model access.

To use the Codex CLI backend for each candidate worktree:

```sh
go run ./cmd/varsynth \
  --repo /Users/dominikhobel/Desktop/Test \
  --issue-file /Users/dominikhobel/Desktop/Test/issue.json \
  --test-command "go test ./..." \
  --out ./out/demo \
  --agent codex
```

Optional Codex settings:

- `--codex-command` overrides the Codex CLI executable path.
- `--codex-model` passes a model override to `codex exec`.
- `--codex-full-auto=false` disables Codex full-auto mode and uses an
  explicit `workspace-write` sandbox instead.
- `--agent-timeout` sets a per-candidate agent timeout, for example `10m`.
- `--agent-stream=false` disables live colored Codex output streaming.
- `--agent-concurrency` caps concurrent agent runs. The default `0` keeps all
  lenses running concurrently for demo speed.
- `--agent-retries` retries transient agent/backend failures before recording a
  failed candidate artifact.
- `--agent-retry-delay` sets the base retry delay, for example `2s`.

By default, `--agent codex` invokes `codex exec --full-auto` for each candidate
worktree. This avoids interactive approvals while keeping execution sandboxed to
the worktree. Commands that need network access or writes outside the worktree
should still fail instead of blocking for manual approval. Codex-backed runs also
pass a JSON output schema so final responses populate rationale, root-cause,
changed-summary, validation-notes, and confidence fields.

## Expected Output

Dry-run output writes only:

- `out/demo/context.json`

Normal output writes:

- `out/demo/context.json`
- `out/demo/prompts/defensive.md`
- `out/demo/prompts/minimalist.md`
- `out/demo/prompts/architect.md`
- `out/demo/prompts/performance.md`
- `out/demo/candidates/defensive.json`
- `out/demo/candidates/minimalist.json`
- `out/demo/candidates/architect.json`
- `out/demo/candidates/performance.json`
- `out/demo/agents/<lens>/stdout.log`
- `out/demo/agents/<lens>/stderr.log`
- `out/demo/agents/<lens>/final_response.json`
- `out/demo/run_events.jsonl`
- `out/demo/report.json`

If `--preserve-worktrees` is used, candidate worktrees are also kept under:

- `out/demo/worktrees/`

## Stub Mode

The default stub mode executes the full pipeline without modifying candidate
worktrees. Candidate diffs are usually empty in this mode. Use `--agent codex`
to run prompt-driven generation inside each isolated worktree.
