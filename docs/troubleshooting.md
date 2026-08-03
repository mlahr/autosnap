# Troubleshooting

## Is The Daemon Running?

```bash
autosnap status
```

`status` is the routine command for checking whether the daemon is active and
what happened on the last check.

To start only when needed:

```bash
autosnap ensure-running
```

`ensure-running` requires a valid `.autosnap.toml` in the current worktree.

## Why Did Hook Installation Refuse?

`autosnap hooks install` refuses to overwrite an existing Git hook or install
through a configured `core.hooksPath`. Inspect the effective state with:

```bash
autosnap hooks status
git config --get core.hooksPath
```

Use `autosnap hooks install --force` only after reviewing the reported path.
Autosnap then backs up and chains existing hooks. It never overwrites an
existing autosnap backup.

`autosnap hooks status` distinguishes these problem states:

- `conflicting (existing hook)`: the hook exists but is not managed by autosnap;
  review it, then use `--force` to preserve and chain it, or remove it and install
  normally;
- `conflicting (orphaned backup)`: an autosnap backup exists without its managed
  hook; restore that backup as the hook or move it aside after review;
- `modified`: a hook has autosnap's management marker but fails its integrity
  check; review and manually restore or remove it because autosnap will not
  overwrite it;
- `error`: autosnap could not inspect or read a hook or backup; correct the
  reported filesystem error before installing.

If `.autosnap.toml` is untracked, installation succeeds with a warning because
new linked worktrees may not contain that configuration file.

## Why Was No Checkpoint Created?

Use logs:

```bash
autosnap logs -n 100
```

Common reasons:

- the configured check command failed;
- the worktree did not become idle long enough;
- the captured tree did not change since the previous checkpoint;
- autosnap is not running in this repository.

## I Edited Config But Behavior Did Not Change

Restart the daemon:

```bash
autosnap restart
```

## The Repository Is Large

If recursive watching is too expensive or hits system limits, use polling:

```toml
[watch]
mode = "poll"
poll_interval = "5s"
```

Then apply the change:

```bash
autosnap restart
```

## A Recovery Command Refused To Run

`pick`, `unpick`, `restore`, and `promote` require a clean worktree and index by
default.

Check Git status:

```bash
git status
```

Commit, stash, or discard unrelated work first. Use `--force` only when you
intentionally want to skip the clean-state precheck.

## Where Are The Installed Docs?

```bash
autosnap docs
man autosnap
```

Packaged installs also place Markdown docs under:

```text
/usr/share/doc/autosnap/
```
