# Varsynth

Varsynth is a Go CLI for running a bug-fix synthesis pipeline against a local
git repository.

The current implementation supports:

- offline issue ingestion from a normalized `issue.json`
- repository/bootstrap context generation
- candidate worktree creation for four repair lenses
- stub-backed candidate execution
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

## Expected Output

Dry-run output writes only:

- `out/demo/context.json`

Normal output writes:

- `out/demo/context.json`
- `out/demo/candidates/defensive.json`
- `out/demo/candidates/minimalist.json`
- `out/demo/candidates/architect.json`
- `out/demo/candidates/performance.json`
- `out/demo/report.json`

If `--preserve-worktrees` is used, candidate worktrees are also kept under:

- `out/demo/worktrees/`

## Current Limitation

The candidate runner is still stub-backed.

That means:

- the pipeline executes end to end
- candidate artifacts and `report.json` are written
- validation runs in each candidate worktree
- candidate diffs are usually empty because no real code-generation backend is
  wired in yet
