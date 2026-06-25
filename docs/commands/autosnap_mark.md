# autosnap mark

## autosnap mark

Mark checkpoints unmarked, good, or bad

### Synopsis

Mark one checkpoint or an inclusive checkpoint range unmarked, good, or bad.

A range A..B marks checkpoints from A through B, inclusive. Ranges are
inclusive autosnap checkpoint intervals, not general Git revision ranges.

The checkpoint argument can be an explicit autosnap ref, a checkpoint commit hash,
or one of these current-branch history selectors:

  first
  first+N
  last
  last-N

```
autosnap mark (--bad|--good|--unmark) <checkpoint-or-range> [flags]
```

### Options

```
      --bad             Mark selected checkpoints bad
      --good            Mark selected checkpoints good
  -h, --help            help for mark
      --reason string   Human-readable reason for a bad mark
      --unmark          Remove explicit good or bad marks from selected checkpoints
```

### SEE ALSO

* [autosnap](autosnap.md)	 - Local checkpointing for Git worktrees

