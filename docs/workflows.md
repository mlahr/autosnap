# Workflows

## Normal Manual Commit Workflow

The usual workflow is:

```bash
autosnap start
# work normally
git status
git add ...
git commit
autosnap pending --explain
```

You continue to decide what belongs in normal Git commits. autosnap preserves
passing checkpoints along the way.

To make startup automatic, run `autosnap hooks install` once. Its
`post-checkout` hook starts autosnap after Git creates or checks out a worktree,
and its `pre-commit` hook retries startup if the daemon stopped later. Hook
startup errors are warnings; any pre-existing hook chained during a forced
installation retains its original exit status.

After you manually commit work, use:

```bash
autosnap pending --explain
```

The status `exact` means the checkpoint tree matches the current branch tip
exactly. In the normal flow, the latest relevant checkpoint showing as `exact`
means your manual commit has integrated that checkpoint's tree.

Other statuses can indicate checkpoints that still contain unapplied, conflicting,
or older variant changes.

## Inspect Checkpoints

```bash
autosnap list
autosnap show last
autosnap show --name-only last
```

Use `list` to see checkpoints and `show` to inspect one checkpoint's metadata and
patch.

Checkpoint selectors include:

- `last`
- `last-N`
- `first`
- `first+N`
- an explicit `refs/autosnapshots/...` ref
- a checkpoint commit hash

Timestamp-only selectors are not accepted.

## Continue On A New Branch

When you want to keep working from the current branch state but continue on a new
branch with the same autosnap checkpoint timeline, use:

```bash
autosnap branch create Branch-B
```

This creates and checks out `Branch-B`, then copies the current branch's
autosnap checkpoint refs into `refs/autosnapshots/Branch-B/`. It copies refs, not
checkpoint commit objects or patches. To create the Git branch without copying
checkpoint refs, use:

```bash
autosnap branch create Branch-B --no-copy-checkpoints
```

If you already created the Git branch, copy checkpoints explicitly:

```bash
autosnap branch copy --from Branch-A --to Branch-B
```

## Salvage Selected Changes

Sometimes a checkpoint passed the configured check but still contains a change
you later decide you do not want. In that case, first get your current worktree
clean, then inspect the checkpoint range:

```bash
git status
autosnap list
autosnap show last-1
```

To apply one checkpoint's incremental patch:

```bash
autosnap pick last-1
```

To apply or remove a net checkpoint interval, use an inclusive autosnap
checkpoint range:

```bash
autosnap pick first+1..last
autosnap unpick first+1..last --force
```

To turn a useful checkpoint into a normal branch commit:

```bash
autosnap promote last-1
```

`pick`, `unpick`, and `promote` are secondary recovery tools. The primary path is
still to make normal Git commits yourself as work becomes ready.

## Restore A Checkpoint

```bash
autosnap restore last
```

`restore` applies checkpoint changes into the worktree and index without moving
`HEAD`. It is useful for recovery, but it is not the normal way to integrate
routine work.

By default, `restore` refuses to run unless the worktree and index are clean.
