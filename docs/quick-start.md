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

## 3. Start The Daemon

```bash
autosnap start
```

The daemon runs in the background for the current repository. When files change,
autosnap waits for the worktree to become idle, runs the configured check, and
saves a checkpoint if the check passes and the captured tree changed.

## 4. Check Status

```bash
autosnap status
```

Use this to confirm whether the daemon is running and what happened on the last
check.

## 5. Apply Config Changes

After editing `.autosnap.toml`, restart the daemon:

```bash
autosnap restart
```

`restart` is the normal way to apply config changes. If a daemon is already
running, it stops and starts again with the updated configuration.

## 6. Troubleshoot With Logs

```bash
autosnap logs
autosnap logs -n 100
autosnap logs -n 100 -f
```

Use logs when a checkpoint was not created or when you need to see daemon output.
