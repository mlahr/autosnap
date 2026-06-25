# autosnap show

## autosnap show

Show checkpoint details

### Synopsis

Show checkpoint metadata and the diff for a checkpoint.

The checkpoint argument can be an explicit autosnap ref, a checkpoint commit hash,
or one of these current-branch history selectors:

  first
  first+N
  last
  last-N

Examples: first+1 selects the second checkpoint. last-1 selects the checkpoint
immediately before the latest checkpoint.

```
autosnap show <checkpoint> [flags]
```

### Examples

```
autosnap show last
autosnap show last-1
autosnap show first+1
autosnap show --name-only refs/autosnapshots/main/20260605T120000Z
```

### Options

```
      --color string   Color output: auto, always, never (default "auto")
      --full           Show full checkpoint diff (default)
  -h, --help           help for show
      --name-only      Show only changed file names
```

### SEE ALSO

* [autosnap](autosnap.md)	 - Local checkpointing for Git worktrees

