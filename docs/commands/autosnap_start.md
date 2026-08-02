# autosnap start

## autosnap start

Start the autosnap daemon and checkpoint on idle passing checks

```
autosnap start [flags]
```

### Options

```
      --check string                     Shell command to run after idle
      --commit-mode string               Commit target: checkpoint, direct, sync (default "checkpoint")
      --foreground                       Run autosnap in the current terminal
  -h, --help                             help for start
      --idle int                         Seconds without changes before running the check (default 60)
      --log-max-bytes int                Maximum autosnap daemon log size in bytes (default 10485760)
      --msg-source-cmd string            Shell command that returns the checkpoint commit message (multiline supported)
      --note-command string              Shell command that returns the checkpoint git note content
      --note-ref string                  Git notes ref for checkpoint notes
      --poll-interval duration           Polling interval for poll or auto watch mode (default 5s)
      --post-checkpoint-command string   Shell command to run after creating a checkpoint
      --snapshot-mode string             Snapshot source: both, staged, working (default "both")
      --watch-mode string                Watch strategy: recursive, poll, auto (default "recursive")
```

### SEE ALSO

* [autosnap](autosnap.md)	 - Local checkpointing for Git worktrees

