# autosnap unpick

## autosnap unpick

Remove a checkpoint's incremental patch

### Synopsis

Remove the same patch displayed by autosnap show <checkpoint-or-range>.

A range A..B removes the net patch from the diff base of A through B. Ranges
are inclusive autosnap checkpoint intervals, not general Git revision ranges.

The checkpoint argument can be an explicit autosnap ref, a checkpoint commit hash,
or one of these current-branch history selectors:

  first
  first+N
  last
  last-N

Examples: first+1 selects the second checkpoint. last-1 selects the checkpoint
immediately before the latest checkpoint.

```
autosnap unpick <checkpoint-or-range> [flags]
```

### Examples

```
autosnap unpick last
autosnap unpick last-1
autosnap unpick first+2
autosnap unpick first+2..last
autosnap unpick refs/autosnapshots/main/20260605T120000Z
```

### Options

```
      --conflict string   Conflict resolution policy: manual, base, head (default "manual")
      --force             Skip the clean worktree/index precheck
  -h, --help              help for unpick
```

### SEE ALSO

* [autosnap](autosnap.md)	 - Local checkpointing for Git worktrees

