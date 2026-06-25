# Configuration

autosnap reads `.autosnap.toml` from the repository root.

Create it with:

```bash
autosnap config init
```

Show the resolved configuration with:

```bash
autosnap config show
```

## Example

```toml
check = "make test"
idle_seconds = 60
snapshot_mode = "both"
commit_mode = "checkpoint"
msg_source_cmd = ""
log_max_bytes = 10485760

[watch]
mode = "recursive"
poll_interval = "5s"
```

## Settings To Adapt

`check` is the command that must pass before autosnap saves a checkpoint. Replace
`make test` with your project's validation command.

`idle_seconds` controls how long autosnap waits after the last file change before
running the check.

`watch.mode` controls how autosnap detects changes:

- `recursive`: use recursive filesystem watching.
- `poll`: poll Git status and relevant dirty-file content.
- `auto`: try recursive watching, then fall back to polling if recursive watching
  cannot be used.

Use `poll_interval` with `poll` or `auto`.

## Defaults To Keep

`snapshot_mode = "both"` captures staged, unstaged, and untracked applicable
changes.

`commit_mode = "checkpoint"` stores passing checkpoints under local autosnap refs
instead of committing to the active branch.

## Applying Changes

After editing `.autosnap.toml`, run:

```bash
autosnap restart
```

`restart` is the normal way to apply config edits to a running daemon.
