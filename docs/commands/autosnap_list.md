# autosnap list

## autosnap list

List checkpoints

### Synopsis

List checkpoints.

A range A..B lists checkpoints from A through B, inclusive. Ranges are
inclusive autosnap checkpoint intervals, not general Git revision ranges.

The checkpoint argument can be an explicit autosnap ref, a checkpoint commit hash,
or one of these current-branch history selectors:

  first
  first+N
  last
  last-N

```
autosnap list [checkpoint-or-range] [flags]
```

### Options

```
      --all               List checkpoints for all branches
      --branch string     List checkpoints for a specific branch
      --format string     Output format: text, json, jsonl (default "text")
  -h, --help              help for list
      --note-ref string   Git notes ref for checkpoint notes
      --notes             Include checkpoint git notes as text
      --notes-json        Include checkpoint git notes decoded as JSON (requires --format json or jsonl)
      --since string      List checkpoints since a duration or commit ID
```

### SEE ALSO

* [autosnap](autosnap.md)	 - Local checkpointing for Git worktrees

