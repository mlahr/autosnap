# autosnap pick

## autosnap pick

Apply a checkpoint's incremental patch

### Synopsis

Apply the same patch displayed by autosnap show <checkpoint>.

The checkpoint argument can be an explicit autosnap ref, a checkpoint commit hash,
or one of these current-branch history selectors:

  first
  first+N
  last
  last-N

Examples: first+1 selects the second checkpoint. last-1 selects the checkpoint
immediately before the latest checkpoint.

```
autosnap pick <checkpoint> [flags]
```

### Examples

```
autosnap pick last
autosnap pick last-1
autosnap pick first+2
autosnap pick refs/autosnapshots/main/20260605T120000Z
```

### Options

```
      --conflict string   Conflict resolution policy: manual, checkpoint, head (default "manual")
      --force             Skip the clean worktree/index precheck
  -h, --help              help for pick
```

### SEE ALSO

* [autosnap](autosnap.md)	 - Local checkpointing for Git worktrees

