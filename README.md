# autosnap

`autosnap` is a local Git checkpointing CLI that saves passing, test-gated snapshots to Git refs without committing to your active branch.

It is implemented as a minimal Go prototype for the MVP command set:

- `autosnap start`
- `autosnap stop`
- `autosnap status`
- `autosnap list`
- `autosnap show <checkpoint>`

Checkpoints are stored as Git refs under:

```
refs/autosnapshots/<branch>/<timestamp>
```

and are created only when checks pass.

---

## Requirements

- Go 1.22+ (or a working Go toolchain compatible with `go.mod`)
- Git installed and available in `PATH`

---

## Build

From the repo root:

```bash
go build -o autosnap ./cmd/autosnap
```

This creates a local `./autosnap` binary.

---

## Install

Install into your Go bin path:

```bash
go install ./cmd/autosnap
```

Make sure your Go bin directory is on `PATH` (commonly `$(go env GOPATH)/bin`).

## Test

Run the unit test suite (fast) with:

```bash
go test ./...
```

Run the full suite including integration tests with:

```bash
go test -tags=integration ./...
```

If you run `go test` at the repo root, it may report `no Go files` because command code now lives under `cmd/`. Use `go test ./...` to run all packages (including `internal/autosnap`).

---

## Usage

All commands must be run inside a Git worktree.

### Start watcher and checkpoint creation

`autosnap start --check "npm test" --idle 60`

`autosnap start` runs in the background by default and returns immediately.

What happens:

- `autosnap` begins watching the repo.
- Any file change resets the idle timer.
- After `--idle` seconds with no changes, it runs `npm test`.
- If the command passes and the working tree tree hash changed since the last checkpoint, a checkpoint commit is created.
- If the command fails, no checkpoint is created.

Example startup output:

```text
autosnap started in background (pid=12345, log=path/to/repo/.git/autosnap/autosnap.log)
autosnap watching /path/to/repo
branch: feature/foo check: npm test idle: 60s
```

When running detached, all watcher logs go to `autosnap.log` under the autosnap state directory (`.git/autosnap/` in a standard worktree).

You can control what gets snapshotted:

- `--snapshot-mode both` (default): include staged + unstaged changes
- `--snapshot-mode staged`: include staged/index state only
- `--snapshot-mode working`: include working-tree changes and untracked files (ignores staged-only index changes)

To keep the process in the current terminal, pass `--foreground`:

```bash
autosnap start --foreground --check "npm test" --idle 60
```

### Stop background watcher

```bash
autosnap stop
```

Stops the background autosnap process for the current repository.

When an idle check runs:

```text
idle reached, running check...
checkpoint saved: abc1234
```

### Check current state

```bash
autosnap status
```

Output includes:

- repo path
- branch
- last checkpoint timestamp
- last check result
- last failed check (if any)
- pending working-tree changes

### List checkpoints

```bash
autosnap list
```

Lists checkpoints for the current branch, newest-first.

### Show checkpoint

`autosnap show <checkpoint>`

Shows checkpoint metadata and a stat diff for the checkpoint object.

Pass `--full` to show full patch content:

```bash
autosnap show --full <checkpoint>
```

Use `--color` to control syntax highlighting:

- `--color=always` forces ANSI color
- `--color=never` forces plain text
- `--color=auto` (default) enables color only for terminal output

```bash
autosnap show --full --color=always <checkpoint>
```

---

## Notes

- Checkpoints are created in Git refs, not as commits on your current branch.
- No index/working tree resets are performed.
- Unchecked or failed runs are only recorded as status metadata (`status`), not as checkpoint refs.
- Untracked files are included when tracked through the temporary-index path snapshot.
- `.git` and common build/artifact directories are ignored by the watcher.
- Additional ignores from Git's own `.gitignore` are respected by file watching.
