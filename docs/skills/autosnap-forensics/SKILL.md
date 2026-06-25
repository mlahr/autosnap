---
name: autosnap-forensics
description: Use when investigating existing autosnap checkpoints to identify when a bug, regression, unwanted behavior, or bad implementation decision was introduced during a long-running coding session.
---

# autosnap Forensics

Use autosnap checkpoints as a forensic timeline for long-running coding sessions.

This skill is for finding where a behavior, regression, bug, or bad decision was
introduced, especially when the current work is one large uncommitted or already
committed changeset.

The goal is to identify the smallest useful checkpoint or checkpoint interval
that introduced the issue, then diagnose and propose remediation. Do not
implement fixes or recovery actions as part of this skill.

## Core Questions

Use this skill to answer questions such as:

- When did this behavior first appear?
- Which checkpoint introduced this regression?
- Which changed paths are most relevant to the issue?
- What changed between the last known good checkpoint and the first known bad checkpoint?
- What is the smallest useful checkpoint interval that isolates the bad change?
- Which prior checkpoint represents a useful known-good state?

## Capabilities

List the checkpoint timeline:

```bash
autosnap list
```

Inspect changed paths for a checkpoint:

```bash
autosnap show --name-only <checkpoint>
```

Inspect changed paths across an inclusive autosnap checkpoint interval:

```bash
autosnap show --name-only <start>..<end>
```

Inspect checkpoint patch details:

```bash
autosnap show <checkpoint>
```

Inspect the net patch across an inclusive autosnap checkpoint interval:

```bash
autosnap show <start>..<end>
```

Inspect pending checkpoint state only when the user's question involves pending
checkpoint state:

```bash
autosnap pending --explain
```

## Checkpoint Selection

Use checkpoint selectors to traverse the autosnap timeline:

```text
last
last-N
first
first+N
refs/autosnapshots/<branch>/<timestamp>
<checkpoint-commit-hash>
```

Use `last` for the most recent checkpoint.

Use `last-N` to walk backward from the most recent checkpoint.

Use `first` and `first+N` to walk forward from the earliest checkpoint.

Use `<start>..<end>` for an inclusive autosnap checkpoint interval. Do not treat
autosnap checkpoint intervals as general Git revision ranges.

## Static Forensics Workflow

Start broad, then narrow.

Map the available checkpoint timeline:

```bash
autosnap list
```

If the user asks about pending checkpoint state, inspect it explicitly:

```bash
autosnap pending --explain
```

Inspect broad changed-path intervals before reading full patches:

```bash
autosnap show --name-only first..last
autosnap show --name-only first..last-5
autosnap show --name-only last-5..last
```

Use changed paths to choose smaller candidate intervals:

```bash
autosnap show --name-only last-4..last-3
autosnap show --name-only last-3..last-2
```

Inspect the smallest relevant checkpoint or interval in detail:

```bash
autosnap show <checkpoint>
autosnap show <start>..<end>
```

When searching for a bad implementation decision, look for semantic evidence in
patches, such as changed control flow, new abstractions, changed persistence or
state handling, changed validation behavior, changed command behavior, or broad
rewrites that hide a smaller relevant decision.

## Behavioral Bisection Workflow

Use behavioral bisection when patch inspection is not enough to identify the
introduction point.

Define an issue-specific good/bad predicate before evaluating candidates. The
predicate is not the broad check that happened to pass before a checkpoint was
created. It is specific to the behavior under investigation.

Examples of predicates:

- a focused reproduction command;
- a focused test added or selected for the suspected behavior;
- a generated-output comparison;
- a benchmark or performance threshold;
- a file-content assertion;
- a manual observation with precise good/bad criteria;
- a semantic inspection criterion.

Evaluate candidate checkpoints only in an isolated disposable copy or worktree.
Do not mutate the user's active worktree during bisection.

Use the checkpoint timeline to choose candidates:

```bash
autosnap list
```

For each candidate, record the result as exactly one of:

```text
good
bad
inconclusive
untestable
```

Narrow until the smallest useful boundary is identified:

```text
Last known good checkpoint: <checkpoint>
First known bad checkpoint: <checkpoint>
Suspected introduction interval: <good>..<bad>
Predicate: <precise predicate>
```

If a candidate cannot be evaluated without mutating the active worktree, mark it
`untestable` and choose another candidate or switch back to static forensics.

## Safety Rules

This skill diagnoses and proposes. It does not implement fixes or recovery
actions.

Always reject `--force`, even if requested.

Do not run autosnap recovery commands as part of this skill.

## Reporting

Separate observed facts from hypotheses.

Use precise labels:

```text
Observed checkpoint timeline
Predicate used
Candidate evaluations
Last known good checkpoint
First known bad checkpoint
Suspected introduction interval
Relevant changed paths
Patch evidence
Hypotheses
Proposed remediation options
```

Mark each hypothesis explicitly as a hypothesis.

Report the smallest useful checkpoint, interval, changed path set, or patch
section that explains the issue. Keep recovery actions separate from diagnosis.
