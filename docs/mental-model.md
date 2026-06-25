# Mental Model

autosnap is a local safety net between intentional Git commits.

You still make normal commits yourself when you decide the work is ready.
autosnap keeps passing intermediate checkpoints so you can inspect or recover
states from the work that happened between those commits.

This is useful during ordinary manual development and during coding-agent-heavy
sessions. You do not need to decide in advance which intermediate state might be
useful later; if the configured check passes, autosnap can preserve it.

The default mode is deliberately separate from branch history:

- Your active branch remains yours to commit intentionally.
- Checkpoints are local Git refs.
- You can inspect, pick, restore, promote, or prune checkpoints later.

Use `autosnap status` for routine visibility. Use `autosnap logs` when something
needs troubleshooting.
