# autosnap pending

## autosnap pending

List checkpoints after the latest checkpoint matching branch tip

```
autosnap pending [flags]
```

### Options

```
      --all               List pending checkpoints for all branches
      --branch string     List pending checkpoints for a specific branch
      --debug             Show progress diagnostics on stderr
      --explain           Show integration status for all scanned checkpoints
      --format string     Output format: text, json, jsonl (default "text")
  -h, --help              help for pending
      --limit int         Maximum number of newest checkpoints to scan (0 means unlimited)
      --note-ref string   Git notes ref for checkpoint notes
      --notes             Include checkpoint git notes as text
      --notes-json        Include checkpoint git notes decoded as JSON (requires --format json or jsonl)
      --since string      Scan checkpoints since a duration or commit ID
```

### SEE ALSO

* [autosnap](autosnap.md)	 - Local checkpointing for Git worktrees

