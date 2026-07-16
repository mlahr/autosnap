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
note_command = ""
note_ref = ""
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

`note_command` and `note_ref` are empty by default. Set both to attach command
output as a Git note on each checkpoint commit, for example under
`refs/notes/diffcog`. `note_command` receives `AUTOSNAP_DIFF_BASE`,
`AUTOSNAP_PREVIOUS_CHECKPOINT_REF`, `AUTOSNAP_BRANCH_REF`, `AUTOSNAP_HEAD`, and
the note-only `AUTOSNAP_CHECKPOINT_COMMIT`.

## Applying Changes

After editing `.autosnap.toml`, run:

```bash
autosnap restart
```

`restart` is the normal way to apply config edits to a running daemon. Its
configuration precedence is: flags supplied to the original `autosnap start`,
then the current `.autosnap.toml`, then built-in defaults. `restart` does not
accept configuration flags. To change a flag override, run `autosnap stop` and
then `autosnap start` with the new flags. `restart` appends its configuration
source, preserved flag names, validated non-command settings, and process
lifecycle steps to the daemon log.
