# Handover: Automatic Git Checkpoint Tool

## Product idea

Build a CLI tool that automatically creates local, test-gated development checkpoints from a Git worktree.

The tool should not replace intentional Git commits. Instead, it should continuously preserve useful recovery points while a developer or coding agent is working.

Core framing:

> Automatic local checkpoints for the last known-good state of your worktree.

This is especially useful for AI-assisted coding workflows where tools like Codex, Claude Code, or other agents make many edits quickly and can leave the repository in a broken or partially changed state.

## Problem

Manual Git commits require intent. Developers decide when a logical unit of work is ready and write a meaningful commit message.

But during active development, especially with coding agents, many intermediate states are useful even if they are not good commits:

- a version that still compiled
- a version before a large refactor went wrong
- a version before an agent made destructive edits
- a version where tests passed
- a version that can later be turned into a clean commit

Currently, developers often rely on manual commits, stashes, IDE local history, or nothing at all.

## Desired behavior

The tool should watch a Git worktree for file changes.

When the worktree becomes idle for a configured amount of time, it should run a configured check command.

If the check passes and the diff changed since the previous checkpoint, it should create a local checkpoint.

The checkpoint should not pollute the normal branch history by default.

## Important product distinction

Do not implement this as "auto-commit directly to the current branch" by default.

That would create noisy Git history and bad commits.

Instead, implement it as:

> local autosnapshots that can later be restored, inspected, squashed, or promoted into real commits.

## MVP scope

Build a simple CLI with these commands:

bash autosnap start autosnap status autosnap list autosnap restore <checkpoint> autosnap promote <checkpoint> 

Optional later:

bash autosnap diff <checkpoint> autosnap prune autosnap config autosnap squash 

## MVP workflow

1. User runs:

bash autosnap start --check "npm test" --idle 60 

2. Tool verifies current directory is inside a Git repository.

3. Tool watches the worktree for file changes.

4. When no changes happen for 60 seconds, tool runs:

bash npm test 

5. If the command exits with code 0:
   - check if the Git diff changed since the last saved checkpoint
   - if no meaningful diff changed, do nothing
   - if diff changed, save a checkpoint

6. If the command fails:
   - do not save a passing checkpoint
   - optionally record the failed check in status/logs, but do not create a checkpoint in MVP unless explicitly configured

7. Checkpoints can be listed and restored.

## Checkpoint storage strategy

Prefer hidden Git refs or a dedicated local branch namespace.

Recommended approach:

text refs/autosnapshots/<branch>/<timestamp> 

Alternative simpler MVP approach:

text autosnap/<branch> 

The current branch should remain clean and should not receive autosave commits directly.

Each checkpoint can be represented as a Git commit created from the current worktree state.

Commit message format:

text autosnap: passing checkpoint 2026-06-04 13:22:10  branch: feature/foo check: npm test idle_seconds: 60 base: <HEAD sha> 

Keep metadata machine-readable if practical.

## Git behavior requirements

The tool must be conservative.

It should not destroy user work.

Rules:

- never run git reset --hard without explicit user command
- never force-push
- never modify remote branches
- never commit directly to the active branch unless the user explicitly asks
- never include ignored files
- respect .gitignore
- handle untracked files if they are not ignored
- avoid checkpointing secrets if possible, but MVP can rely on Git ignore rules

## File watching behavior

Use a cross-platform file watcher.

Ignore:

text .git/ node_modules/ target/ build/ dist/ out/ .gradle/ .idea/ .vscode/ .DS_Store 

Allow ignore patterns to be configured later.

Debounce logic:

- any file change resets the idle timer
- after idle_seconds without changes, run the check
- while check is running, do not start another check
- if changes happen during a check, mark the check result stale and schedule another idle cycle

## Check command

The user should be able to provide any shell command.

Examples:

bash autosnap start --check "npm test" autosnap start --check "npm run typecheck" autosnap start --check "./gradlew test" autosnap start --check "cargo test" autosnap start --check "mvn test" 

MVP should support one command.

Later versions can support multiple checks.

## Config file

Support a project-local config file later.

Possible file:

toml # .autosnap.toml  idle_seconds = 60 check = "npm test"  [watch] ignore = [   "node_modules/",   "dist/",   "build/",   "target/" ] 

For MVP, command-line flags are enough.

## CLI behavior

### autosnap start

Starts the watcher.

Example:

bash autosnap start --check "npm test" --idle 60 

Output should be minimal and clear:

text autosnap watching /path/to/repo branch: feature/foo check: npm test idle: 60s  changed: src/foo.ts idle reached, running check... check passed checkpoint saved: 2026-06-04 13:22:10 

### autosnap status

Shows current state:

text repo: /path/to/repo branch: feature/foo last checkpoint: 2026-06-04 13:22:10 last check: passed last check duration: 8.4s pending changes: yes 

### autosnap list

Shows checkpoints:

text 2026-06-04 13:22:10  passing  npm test  abc1234 2026-06-04 13:10:02  passing  npm test  def5678 

### autosnap restore <checkpoint>

Restores a checkpoint.

This must be explicit and safe.

Before restoring:
- detect uncommitted work
- if current worktree has changes, refuse unless --force or create a safety checkpoint first

Preferred behavior:

text current worktree has changes created safety checkpoint: 2026-06-04 13:40:12 restored checkpoint: 2026-06-04 13:22:10 

### autosnap promote <checkpoint>

Promotes a checkpoint into a normal Git commit on the current branch.

This should either:
- cherry-pick the checkpoint commit, or
- apply the diff and create a normal commit

The user should be able to edit the commit message later. MVP can use a generated message.

Example:

bash autosnap promote 2026-06-04T13:22:10 

Result:

text created commit on current branch: abc1234 implement parser cleanup 

For MVP, generated message is acceptable:

text checkpoint promoted from autosnap 2026-06-04 13:22:10 

## Suggested implementation language

Good options:

1. Rust
   - best for single binary distribution
   - good filesystem watcher crates
   - good CLI ecosystem
   - slightly more implementation overhead

2. Go
   - also good for single binary distribution
   - simpler implementation
   - good enough for MVP

3. Node.js
   - fastest for MVP
   - easy watcher and shell command handling
   - less ideal for robust Git tooling distribution

Recommendation: use Go or Rust if this should become a real CLI product. Use Node.js only for quick prototype.

## Suggested libraries

For Go:

- cobra for CLI
- fsnotify for file watching
- go-git or shell out to system git

Prefer shelling out to system Git for MVP. It avoids subtle compatibility issues.

For Rust:

- clap for CLI
- notify for file watching
- shell out to system git

Again, shell out to Git for MVP.

## Git implementation outline

To create a checkpoint without committing to current branch:

1. Detect current branch:

bash git branch --show-current 

2. Detect current HEAD:

bash git rev-parse HEAD 

3. Detect dirty state:

bash git status --porcelain 

4. Create a temporary index or stash-like commit.

Simpler MVP path:

- create an autosnap branch if missing
- save current branch name and HEAD
- create a temporary commit from current worktree
- move hidden ref to that commit
- restore user branch state

But this is risky if implemented naïvely.

Safer approach:

Use Git plumbing with a temporary index.

High-level:

bash git add -A tree=$(git write-tree) parent=$(git rev-parse HEAD) commit=$(echo "$message" | git commit-tree "$tree" -p "$parent") git update-ref "refs/autosnapshots/<branch>/<timestamp>" "$commit" 

Important: using git add -A mutates the user's index. Avoid that if possible.

Better:

Use a temporary index:

bash GIT_INDEX_FILE=.git/autosnap-index git read-tree HEAD GIT_INDEX_FILE=.git/autosnap-index git add -A tree=$(GIT_INDEX_FILE=.git/autosnap-index git write-tree) commit=$(echo "$message" | git commit-tree "$tree" -p HEAD) git update-ref "refs/autosnapshots/<branch>/<timestamp>" "$commit" rm .git/autosnap-index 

This avoids touching the user's staging area.

This is important.

## MVP acceptance criteria

The MVP is acceptable when:

- running autosnap start --check "<cmd>" --idle <seconds> watches a repo
- changes trigger an idle timer
- the check command runs only after the idle period
- passing checks create checkpoints
- failing checks do not create checkpoints
- repeated unchanged worktree states do not create duplicate checkpoints
- user staging area is not modified
- checkpoints are stored outside normal branch history
- autosnap list shows checkpoints
- autosnap restore can restore a checkpoint safely
- autosnap promote can turn a checkpoint into a normal commit

## Non-goals for MVP

Do not build these initially:

- UI
- cloud sync
- remote backup
- AI commit message generation
- semantic diff grouping
- multi-repo support
- GitHub integration
- automatic pushing
- team workflow
- pre-commit integration
- complex config inheritance

## Later product ideas

Useful next features:

1. Last known-good command

bash autosnap restore last-good 

2. AI coding agent integration

Expose a simple CLI that agents can call:

bash autosnap mark "before refactor" autosnap restore last-good autosnap diff last-good 

3. Checkpoint timeline

Show all passing and failing states in chronological order.

4. Semantic checkpoint summaries

Generate summaries like:

text - changed parser error handling - updated tests - removed unused DTO 

5. Commit builder

Let the user select a range of checkpoints and produce one clean commit.

6. Branch protection

Automatically checkpoint before risky commands:

bash autosnap guard -- claude-code autosnap guard -- codex autosnap guard -- npm run migrate 

7. IDE extension

Show timeline and restore buttons inside editor.

## Main risks

### Risk 1: Polluting Git history

Avoid by storing checkpoints outside the current branch.

### Risk 2: Damaging worktree or index

Use a temporary Git index. Do not mutate the user's staging area.

### Risk 3: Too many checkpoints

Avoid duplicates by comparing tree hashes. Add retention later.

### Risk 4: Slow checks

Make checks configurable. Later support quick checks and full checks.

### Risk 5: Misleading "passing" state

Store the exact check command and exit code. A passing checkpoint only means the configured check passed.

## Naming ideas

Working names:

- autosnap
- git-sentinel
- git-guard
- worktree-checkpoint
- lastgood
- snapgit

Recommended internal name for now:

text autosnap 

## First coding task

Build a CLI prototype with:

bash autosnap start --check "echo ok" --idle 5 

Then verify:

1. create a test Git repository
2. edit a file
3. wait 5 seconds
4. check command runs
5. checkpoint ref is created
6. checkpoint appears in autosnap list
7. user index remains unchanged

Focus on correctness and safety before UX.

