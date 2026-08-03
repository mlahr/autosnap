# Quick Start

Run autosnap from inside the Git worktree you want to protect.

## 1. Create Config

```bash
autosnap config init
```

This creates `.autosnap.toml` in the repository root. autosnap is configured per
Git worktree; running it in one repository does not enable it in every
repository.

## 2. Edit The Check Command

The important setting is `check`. It is the command autosnap runs before saving
a checkpoint.

Example:

```toml
check = "make test"
idle_seconds = 60
snapshot_mode = "both"
commit_mode = "checkpoint"
msg_source_cmd = ""
note_command = ""
note_ref = ""
post_checkpoint_command = ""
log_max_bytes = 10485760

[watch]
mode = "recursive"
poll_interval = "5s"
```

Use `make test` only as a simple example. Replace it with the command that means
"this worktree is good enough to preserve" for your project.

Usually you adapt:

- `check`: your validation command.
- `idle_seconds`: how long autosnap waits after the last file change before it
  runs the check.
- `watch.mode`: use `poll` or `auto` for repositories where recursive watching
  is too expensive.

Keep these defaults unless you have a specific reason to change them:

- `snapshot_mode = "both"`
- `commit_mode = "checkpoint"`

## 3. Start The Daemon Automatically

Install repository-local Git hooks when autosnap should start automatically for
this worktree and future linked worktrees:

```bash
autosnap hooks install
```

The command validates `.autosnap.toml`, installs `post-checkout` and
`pre-commit`, and starts autosnap for the current worktree. Git invokes
`post-checkout` after populating a new linked worktree, so a tracked
`.autosnap.toml` is available before startup. An untracked config works in the
current worktree, but autosnap warns that future worktrees may not contain it.

Existing hooks and a configured `core.hooksPath` are left untouched unless you
explicitly pass `--force`. In that mode, autosnap backs up and chains existing
hooks. Autosnap startup failures from an installed hook are warnings and do not
fail the Git operation.

Check or remove the installation with:

```bash
autosnap hooks status
autosnap hooks uninstall
```

Uninstalling hooks does not stop daemons that are already running.

## 4. Start The Daemon Manually

```bash
autosnap start
```

The daemon runs in the background for the current repository. When files change,
autosnap waits for the worktree to become idle, runs the configured check, and
saves a checkpoint if the check passes and the captured tree changed.

`ensure-running` is the idempotent equivalent for scripts and editor
integrations:

```bash
autosnap ensure-running
```

It starts autosnap from `.autosnap.toml` only when the current worktree daemon
is not already active.

## 5. Check Status

```bash
autosnap status
```

Use this to confirm whether the daemon is running and what happened on the last
check.

## 6. Apply Config Changes

After editing `.autosnap.toml`, restart the daemon:

```bash
autosnap restart
```

`restart` requires a running daemon. It validates the current configuration,
stops the daemon, and starts it again. Configuration flags supplied to the
original `autosnap start` remain overrides; all other settings are reloaded from
`.autosnap.toml`. To change the preserved flags, run `autosnap stop` followed by
a new `autosnap start` command. Detailed restart progress is appended to the
daemon log and is visible with `autosnap logs`.

## 7. Troubleshoot With Logs

```bash
autosnap logs
autosnap logs -n 100
autosnap logs -n 100 -f
```

Use logs when a checkpoint was not created or when you need to see daemon output.
