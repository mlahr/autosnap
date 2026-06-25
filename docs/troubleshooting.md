# Troubleshooting

## Is The Daemon Running?

```bash
autosnap status
```

`status` is the routine command for checking whether the daemon is active and
what happened on the last check.

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

`pick`, `restore`, and `promote` require a clean worktree and index by default.

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
