# autosnap prune

## autosnap prune

Prune old autosnap checkpoints

```
autosnap prune [flags]
```

### Options

```
      --all-branches        Prune checkpoints for all branches
      --apply               Delete matching checkpoint refs
      --branch string       Prune checkpoints for a specific branch
      --current-branch      Prune checkpoints for the current branch
  -h, --help                help for prune
      --keep int            Keep the newest N checkpoints per branch (default -1)
      --older-than string   Prune checkpoints older than a duration such as 24h or 7d
```

### SEE ALSO

* [autosnap](autosnap.md)	 - Local checkpointing for Git worktrees

