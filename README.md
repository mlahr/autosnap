# autosnap

`autosnap` is a local Git checkpointing CLI that saves passing, test-gated snapshots to Git refs without committing to your active branch.

It is implemented as a minimal Go prototype for the MVP command set:

- `autosnap start`
- `autosnap stop`
- `autosnap status`
- `autosnap list`
- `autosnap show <checkpoint>`
- `autosnap restore <checkpoint>`
- `autosnap promote <checkpoint>`
- `autosnap prune`

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

`autosnap start --check "npm test" --msg-source-cmd "printf \"checkpoint: $(date -u +%Y-%m-%dT%H:%M:%SZ)\\ncontext: autosnap\""`

When set, `--msg-source-cmd` replaces the checkpoint commit message with command output.
Multiline output is supported; leading and trailing whitespace is trimmed.
If the command fails or emits no output, autosnap falls back to the generated message.

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

By default, autosnap recursively watches the repository with filesystem events. Large repositories can use polling or automatic fallback:

```bash
autosnap start --check "make build" --idle 30 --watch-mode poll --poll-interval 5s
autosnap start --check "make build" --idle 30 --watch-mode auto
```

Watch modes:

- `--watch-mode recursive` (default): recursively watch directories with filesystem events
- `--watch-mode poll`: poll Git working-tree status without recursive filesystem watches
- `--watch-mode auto`: try recursive watching, then fall back to polling if the watcher hits the open-file limit

Polling uses `--poll-interval` (`5s` by default).

To exclude large paths from triggering autosnap, add a repo-root `.autosnapignore` file:

```gitignore
tmp/
examples/corpus-pdfs-failed/
src/test/fixtures/font-swap-visual-layout-snapshots/
```

`.autosnapignore` is watch-only: ignored paths do not trigger idle checks, but they are still included in checkpoint snapshots if another watched change causes a checkpoint. Git ignored paths are also skipped by the watcher.

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

Lists checkpoints for the current branch, oldest-first.

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

### Restore checkpoint changes

`autosnap restore <checkpoint>`

	Applies the checkpoint diff back into the working tree and index without moving `HEAD`.
By default, restore refuses to run unless the worktree and index are clean.
When changes overlap, restore attempts a three-way apply and may leave conflict markers for manual resolution.

Pass `--force` to skip the clean-state precheck and let Git apply the patch normally:

```bash
autosnap restore --force <checkpoint>
```

### Promote checkpoint to a branch commit

`autosnap promote <checkpoint>`

Creates a normal commit on the current branch using the checkpoint tree and commit message.
By default, promote refuses to run unless the worktree and index are clean.

Pass `--force` to skip the clean-state precheck:

```bash
autosnap promote --force <checkpoint>
```

### Prune old checkpoints

`autosnap prune` previews checkpoint refs that match the current branch and a retention policy.
Pass `--apply` to delete the matching refs.

Scope flags:

- `--current-branch` (default)
- `--branch <name>`
- `--all-branches`

Retention policy flags:

- `--keep N`: keep the newest N checkpoints per branch and prune older checkpoints
- `--older-than <duration>`: prune checkpoints older than a duration such as `24h` or `7d`

Examples:

```bash
autosnap prune --current-branch --keep 20
autosnap prune --all-branches --older-than 30d --apply
```

---

## Notes

- Checkpoints are created in Git refs, not as commits on your current branch.
- No index/working tree resets are performed.
- Unchecked or failed runs are only recorded as status metadata (`status`), not as checkpoint refs.
- Untracked files are included when tracked through the temporary-index path snapshot.
- `.git` and common build/artifact directories are ignored by the watcher.
- Additional ignores from Git's own `.gitignore` are respected by file watching.
- `.autosnapignore` can exclude large paths from watcher triggers without excluding them from checkpoints.
