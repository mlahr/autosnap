# autosnap

`autosnap` is a local Git checkpointing CLI that saves passing, test-gated snapshots to Git refs without committing to your active branch.

It is implemented as a minimal Go prototype for the MVP command set:

- `autosnap start`
- `autosnap status`
- `autosnap list`

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
go build -o autosnap .
```

This creates a local `./autosnap` binary.

---

## Install

Install into your Go bin path:

```bash
go install .
```

Make sure your Go bin directory is on `PATH` (commonly `$(go env GOPATH)/bin`).

---

## Usage

All commands must be run inside a Git worktree.

### Start watcher and checkpoint creation

```bash
autosnap start --check "npm test" --idle 60
```

What happens:

- `autosnap` begins watching the repo.
- Any file change resets the idle timer.
- After `--idle` seconds with no changes, it runs `npm test`.
- If the command passes and the working tree tree hash changed since the last checkpoint, a checkpoint commit is created.
- If the command fails, no checkpoint is created.

Example startup output:

```text
autosnap watching /path/to/repo
branch: feature/foo check: npm test idle: 60s
```

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

---

## Notes

- Checkpoints are created in Git refs, not as commits on your current branch.
- No index/working tree resets are performed.
- Unchecked or failed runs are only recorded as status metadata (`status`), not as checkpoint refs.
- Untracked files are included when tracked through the temporary-index path snapshot.
- `.git` and common build/artifact directories are ignored by the watcher.

