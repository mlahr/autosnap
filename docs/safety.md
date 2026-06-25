# Safety Model

In the default `commit_mode = "checkpoint"` mode, autosnap is conservative.

It does not:

- commit to your active branch;
- push to a remote;
- overwrite work during normal daemon operation;
- replace your normal Git commit workflow.

It does:

- run your configured check command after the worktree has been idle;
- create a local checkpoint when that check passes;
- store checkpoint refs under `refs/autosnapshots/<branch>/<timestamp>`;
- keep daemon state and logs under the repository's Git directory.

## Local Refs

A checkpoint is stored as a Git ref like this:

```text
refs/autosnapshots/main/20260625T120000Z
```

That ref points to a checkpoint commit object. You do not need to use Git plumbing
commands to work with it; autosnap commands such as `list`, `show`, `pending`,
`pick`, `restore`, `promote`, and `prune` are the normal interface.

Advanced users can inspect autosnap refs with plain Git commands, but the user
documentation does not rely on that.

## Commands That Change The Worktree Or Branch

Some explicit commands are meant to modify the worktree or branch:

- `autosnap pick <checkpoint-or-range>` applies a checkpoint patch.
- `autosnap unpick <checkpoint-or-range>` removes a checkpoint patch.
- `autosnap restore <checkpoint>` applies checkpoint changes into the worktree
  and index without moving `HEAD`.
- `autosnap promote <checkpoint>` creates a normal commit on the active branch.

These commands refuse to run on a dirty worktree and index unless you pass
`--force`.

## Direct And Sync Modes

`commit_mode = "direct"` and `commit_mode = "sync"` intentionally differ from
the default. They commit directly to the active branch. `sync` also pulls with
rebase and pushes.

Use those modes only when direct branch commits are the behavior you want.
