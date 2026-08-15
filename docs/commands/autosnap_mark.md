# autosnap mark

## autosnap mark

Mark checkpoints with an arbitrary label

### Synopsis

Mark one checkpoint or an inclusive checkpoint range with an arbitrary label.

A range A..B marks checkpoints from A through B, inclusive. Ranges are
inclusive autosnap checkpoint intervals, not general Git revision ranges.

Labels must be 1-32 characters matching [A-Za-z0-9][A-Za-z0-9_-]*. The label
unmarked is reserved. Use --reason with --label or --bad to record why a
checkpoint was marked.

The checkpoint argument can be an explicit autosnap ref, a checkpoint commit hash,
or one of these current-branch history selectors:

  first
  first+N
  last
  last-N

```
autosnap mark (--label LABEL|--bad|--good|--review|--unmark) <checkpoint-or-range> [flags]
```

### Options

```
      --bad             Mark selected checkpoints bad
      --good            Mark selected checkpoints good
  -h, --help            help for mark
      --label string    Arbitrary mark label
      --reason string   Human-readable reason for the mark
      --review          Mark selected checkpoints for review
      --unmark          Remove explicit review, good, or bad marks from selected checkpoints
```

### SEE ALSO

* [autosnap](autosnap.md)	 - Local checkpointing for Git worktrees

