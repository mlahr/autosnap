# autosnap checkpoint

## autosnap checkpoint

Create a checkpoint immediately

```
autosnap checkpoint [flags]
```

### Options

```
      --check string            Shell command to run before checkpointing
      --commit-mode string      Commit target: checkpoint, direct, sync (default "checkpoint")
  -h, --help                    help for checkpoint
      --msg-source-cmd string   Shell command that returns the checkpoint commit message (multiline supported)
      --snapshot-mode string    Snapshot source: both, staged, working (default "both")
      --timeout duration        Maximum time to wait for another checkpoint operation to finish (0 waits indefinitely)
```

### SEE ALSO

* [autosnap](autosnap.md)	 - Local checkpointing for Git worktrees

