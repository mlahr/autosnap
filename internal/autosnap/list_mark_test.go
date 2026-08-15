package autosnap

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func TestListCommandBranchAndAllScopes(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		currentTimestamp := "20200101T000000Z"
		featureTimestamp := "20210101T000000Z"
		currentRef := createAutosnapTestCommitRef(t, repo, branchRef, currentTimestamp, "current branch checkpoint")
		createAutosnapTestCommitRef(t, repo, "feature/foo", featureTimestamp, "feature branch checkpoint")
		currentDisplayTimestamp := formatCheckpointTimestampForList(currentTimestamp)
		featureDisplayTimestamp := formatCheckpointTimestampForList(featureTimestamp)

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newListCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"list", "--branch", "feature/foo"})

		if err := root.Execute(); err != nil {
			t.Fatalf("branch list failed: %v", err)
		}

		output := buf.String()
		if !strings.Contains(output, featureDisplayTimestamp) || !strings.Contains(output, "feature branch checkpoint") {
			t.Fatalf("expected feature checkpoint in branch list output, got %q", output)
		}
		if strings.Contains(output, featureTimestamp) {
			t.Fatalf("expected branch list output to use formatted timestamp, got %q", output)
		}
		if strings.Contains(output, currentRef) || strings.Contains(output, "current branch checkpoint") {
			t.Fatalf("expected branch list to exclude current checkpoint %s, got %q", currentRef, output)
		}

		buf.Reset()
		root = &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newListCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"list", "--all"})

		if err := root.Execute(); err != nil {
			t.Fatalf("all list failed: %v", err)
		}

		output = buf.String()
		if !strings.Contains(output, branchRef) || !strings.Contains(output, "feature/foo") {
			t.Fatalf("expected all list output to include branch names, got %q", output)
		}
		if !strings.Contains(output, "current branch checkpoint") || !strings.Contains(output, "feature branch checkpoint") {
			t.Fatalf("expected all list output to include both checkpoint summaries, got %q", output)
		}
		if !strings.Contains(output, currentDisplayTimestamp) || !strings.Contains(output, featureDisplayTimestamp) {
			t.Fatalf("expected all list output to include both timestamps, got %q", output)
		}
		if strings.Contains(output, currentTimestamp) || strings.Contains(output, featureTimestamp) {
			t.Fatalf("expected all list output to use formatted timestamps, got %q", output)
		}
	})
}

func TestListCommandListsInclusiveCheckpointRange(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		ref1, _, _ := createCheckpointRangeScenario(t, repo)

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newListCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"list", "first+1..last"})

		if err := root.Execute(); err != nil {
			t.Fatalf("list checkpoint range failed: %v", err)
		}

		output := buf.String()
		if strings.Contains(output, "first") || strings.Contains(output, checkpointRefTimestamp(ref1)) {
			t.Fatalf("expected range list to exclude first checkpoint, got %q", output)
		}
		secondIndex := strings.Index(output, "second")
		thirdIndex := strings.Index(output, "third")
		if secondIndex < 0 || thirdIndex < 0 {
			t.Fatalf("expected range list to include second and third checkpoints, got %q", output)
		}
		if secondIndex > thirdIndex {
			t.Fatalf("expected range list to preserve chronological order, got %q", output)
		}
	})
}

func TestListCommandListsSingleCheckpointSelector(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		createCheckpointRangeScenario(t, repo)

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newListCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"list", "last-1"})

		if err := root.Execute(); err != nil {
			t.Fatalf("list single checkpoint selector failed: %v", err)
		}

		output := buf.String()
		if !strings.Contains(output, "second") {
			t.Fatalf("expected single selector list to include second checkpoint, got %q", output)
		}
		if strings.Contains(output, "first") || strings.Contains(output, "third") {
			t.Fatalf("expected single selector list to include only second checkpoint, got %q", output)
		}
	})
}

func TestMarkCommandLabelsListShowAndJSONOutput(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		createAutosnapTestCommitRef(t, repo, branchRef, "20260101T000000Z", "first checkpoint")
		createAutosnapTestCommitRef(t, repo, branchRef, "20260101T000001Z", "second checkpoint")
		createAutosnapTestCommitRef(t, repo, branchRef, "20260101T000002Z", "third checkpoint")

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newMarkCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"mark", "--review", "last-1"})
		if err := root.Execute(); err != nil {
			t.Fatalf("mark review failed: %v", err)
		}

		buf.Reset()
		root = &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newMarkCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"mark", "--bad", "--reason", "regression", "last"})
		if err := root.Execute(); err != nil {
			t.Fatalf("mark bad failed: %v", err)
		}

		buf.Reset()
		root = &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newListCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"list"})
		if err := root.Execute(); err != nil {
			t.Fatalf("list failed: %v", err)
		}
		output := buf.String()
		if !strings.Contains(output, "first checkpoint") || strings.Contains(output, "[unmarked]") || !strings.Contains(output, "[review] second checkpoint") || !strings.Contains(output, "[bad] third checkpoint") {
			t.Fatalf("expected list output to include compact review/bad labels, got %q", output)
		}
		if strings.Contains(output, "regression") {
			t.Fatalf("expected list text output to omit bad reason, got %q", output)
		}

		buf.Reset()
		root = &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newListCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"list", "--format", "json"})
		if err := root.Execute(); err != nil {
			t.Fatalf("json list failed: %v", err)
		}
		var rows []map[string]any
		if err := json.Unmarshal(buf.Bytes(), &rows); err != nil {
			t.Fatalf("decode json list failed: %v output=%q", err, buf.String())
		}
		if len(rows) != 3 {
			t.Fatalf("expected 3 json rows, got %d", len(rows))
		}
		if rows[0]["mark"] != "unmarked" {
			t.Fatalf("expected first checkpoint mark=unmarked, got %+v", rows[0])
		}
		if rows[1]["mark"] != "review" {
			t.Fatalf("expected second checkpoint mark=review, got %+v", rows[1])
		}
		if _, ok := rows[1]["reason"]; ok {
			t.Fatalf("expected review checkpoint to omit reason, got %+v", rows[1])
		}
		if rows[2]["mark"] != "bad" || rows[2]["reason"] != "regression" {
			t.Fatalf("expected third checkpoint reason in json, got %+v", rows[2])
		}

		buf.Reset()
		root = &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newListCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"list", "--format", "jsonl"})
		if err := root.Execute(); err != nil {
			t.Fatalf("jsonl list failed: %v", err)
		}
		if !strings.Contains(buf.String(), `"mark":"review"`) {
			t.Fatalf("expected jsonl list to include review mark, got %q", buf.String())
		}

		buf.Reset()
		root = &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newPendingCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"pending", "--explain"})
		if err := root.Execute(); err != nil {
			t.Fatalf("pending explain failed: %v", err)
		}
		if !strings.Contains(buf.String(), "[review] second checkpoint") {
			t.Fatalf("expected pending output to include review mark, got %q", buf.String())
		}

		buf.Reset()
		root = &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newShowCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"show", "last-1", "--name-only"})
		if err := root.Execute(); err != nil {
			t.Fatalf("show review failed: %v", err)
		}
		if !strings.Contains(buf.String(), "mark: review") {
			t.Fatalf("expected show output to include review mark, got %q", buf.String())
		}

		buf.Reset()
		root = &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newShowCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"show", "last", "--name-only"})
		if err := root.Execute(); err != nil {
			t.Fatalf("show failed: %v", err)
		}
		output = buf.String()
		if !strings.Contains(output, "mark: bad") || !strings.Contains(output, "mark reason: regression") {
			t.Fatalf("expected show output to include bad mark and reason, got %q", output)
		}
	})
}

func TestMarkCommandSupportsArbitraryLabelAndReason(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		createCheckpointRangeScenario(t, repo)

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newMarkCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"mark", "--label", "needs-review", "last", "--reason", "inspect renderer"})
		if err := root.Execute(); err != nil {
			t.Fatalf("mark arbitrary label failed: %v", err)
		}

		buf.Reset()
		root = &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newListCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"list", "--format", "json"})
		if err := root.Execute(); err != nil {
			t.Fatalf("list json failed: %v", err)
		}
		var rows []map[string]any
		if err := json.Unmarshal(buf.Bytes(), &rows); err != nil {
			t.Fatalf("decode list json failed: %v", err)
		}
		last := rows[len(rows)-1]
		if last["mark"] != "needs-review" || last["reason"] != "inspect renderer" {
			t.Fatalf("expected arbitrary mark and reason, got %+v", last)
		}

		buf.Reset()
		root = &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newListCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"list"})
		if err := root.Execute(); err != nil {
			t.Fatalf("list text failed: %v", err)
		}
		if !strings.Contains(buf.String(), "[needs-review]") || strings.Contains(buf.String(), ansiYellow) {
			t.Fatalf("expected uncolored arbitrary mark in text output, got %q", buf.String())
		}
	})
}

func TestCheckpointMarksLoadsMarkedAndUnmarkedCheckpoints(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		createAutosnapTestCommitRef(t, repo, branchRef, "20260101T000000Z", "first checkpoint")
		badRef := createAutosnapTestCommitRef(t, repo, branchRef, "20260101T000001Z", "second checkpoint")
		goodRef := createAutosnapTestCommitRef(t, repo, branchRef, "20260101T000002Z", "third checkpoint")
		reviewRef := createAutosnapTestCommitRef(t, repo, branchRef, "20260101T000003Z", "fourth checkpoint")

		if err := markCheckpointBad(context.Background(), repo, checkpointRefInfo{Ref: badRef, FullCommit: runGitOutput(t, repo, "rev-parse", badRef)}, "regression", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
			t.Fatalf("mark bad failed: %v", err)
		}
		if err := markCheckpointGood(context.Background(), repo, checkpointRefInfo{Ref: goodRef, FullCommit: runGitOutput(t, repo, "rev-parse", goodRef)}, time.Date(2026, 1, 1, 0, 0, 1, 0, time.UTC)); err != nil {
			t.Fatalf("mark good failed: %v", err)
		}
		if err := markCheckpointReview(context.Background(), repo, checkpointRefInfo{Ref: reviewRef, FullCommit: runGitOutput(t, repo, "rev-parse", reviewRef)}, time.Date(2026, 1, 1, 0, 0, 2, 0, time.UTC)); err != nil {
			t.Fatalf("mark review failed: %v", err)
		}

		refs, err := listCheckpointRefsForBranch(context.Background(), repo, branchRef)
		if err != nil {
			t.Fatalf("listCheckpointRefsForBranch failed: %v", err)
		}
		checkpoints, err := listCheckpointsFromRefs(context.Background(), repo, refs)
		if err != nil {
			t.Fatalf("listCheckpointsFromRefs failed: %v", err)
		}
		marks, err := checkpointMarks(context.Background(), repo, checkpoints)
		if err != nil {
			t.Fatalf("checkpointMarks failed: %v", err)
		}
		if marks[refs[0].Ref].Mark != checkpointMarkStateUnmarked {
			t.Fatalf("expected first checkpoint to be unmarked, got %+v", marks[refs[0].Ref])
		}
		if marks[badRef].Mark != checkpointMarkStateBad || marks[badRef].Reason != "regression" {
			t.Fatalf("expected bad checkpoint mark with reason, got %+v", marks[badRef])
		}
		if marks[goodRef].Mark != checkpointMarkStateGood {
			t.Fatalf("expected good checkpoint mark, got %+v", marks[goodRef])
		}
		if marks[reviewRef].Mark != checkpointMarkStateReview || marks[reviewRef].Reason != "" {
			t.Fatalf("expected review checkpoint mark without reason, got %+v", marks[reviewRef])
		}
	})
}

func TestCheckpointMarksAppliesCommitMarkToDuplicateCheckpointRefs(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		firstRef := createAutosnapTestCommitRef(t, repo, branchRef, "20260101T000000Z", "first checkpoint")
		commit := runGitOutput(t, repo, "rev-parse", firstRef)
		secondRef := snapshotRef(branchRef, "20260101T000001Z")
		runGit(t, repo, "update-ref", secondRef, commit)
		addGitNote(t, repo, checkpointMarkNoteRef, commit, `{"mark":"review"}`)

		refs, err := listCheckpointRefsForBranch(context.Background(), repo, branchRef)
		if err != nil {
			t.Fatalf("listCheckpointRefsForBranch failed: %v", err)
		}
		checkpoints, err := listCheckpointsFromRefs(context.Background(), repo, refs)
		if err != nil {
			t.Fatalf("listCheckpointsFromRefs failed: %v", err)
		}
		marks, err := checkpointMarks(context.Background(), repo, checkpoints)
		if err != nil {
			t.Fatalf("checkpointMarks failed: %v", err)
		}
		if marks[firstRef].Mark != checkpointMarkStateReview || marks[secondRef].Mark != checkpointMarkStateReview {
			t.Fatalf("expected both duplicate refs to use commit mark, got first=%+v second=%+v", marks[firstRef], marks[secondRef])
		}
		if marks[firstRef].Reason != "" || marks[secondRef].Reason != "" {
			t.Fatalf("expected both duplicate review refs to omit reason, got first=%+v second=%+v", marks[firstRef], marks[secondRef])
		}
	})
}

func TestCheckpointMarksRejectsInvalidMarkPayload(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		ref := createAutosnapTestCommitRef(t, repo, branchRef, "20260101T000000Z", "first checkpoint")
		addGitNote(t, repo, checkpointMarkNoteRef, ref, `{not json`)

		refs, err := listCheckpointRefsForBranch(context.Background(), repo, branchRef)
		if err != nil {
			t.Fatalf("listCheckpointRefsForBranch failed: %v", err)
		}
		checkpoints, err := listCheckpointsFromRefs(context.Background(), repo, refs)
		if err != nil {
			t.Fatalf("listCheckpointsFromRefs failed: %v", err)
		}
		_, err = checkpointMarks(context.Background(), repo, checkpoints)
		if err == nil || !strings.Contains(err.Error(), "invalid checkpoint mark") {
			t.Fatalf("expected invalid checkpoint mark error, got %v", err)
		}
	})
}

func TestMarkCommandMarksRangeAndReplacesWithReview(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		createCheckpointRangeScenario(t, repo)

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newMarkCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"mark", "--bad", "first+1..last"})
		if err := root.Execute(); err != nil {
			t.Fatalf("mark range bad failed: %v", err)
		}

		buf.Reset()
		root = &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newListCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"list"})
		if err := root.Execute(); err != nil {
			t.Fatalf("list failed: %v", err)
		}
		output := buf.String()
		if !strings.Contains(output, "first") || strings.Contains(output, "[unmarked]") || !strings.Contains(output, "[bad] second") || !strings.Contains(output, "[bad] third") {
			t.Fatalf("expected range mark labels in list output, got %q", output)
		}

		buf.Reset()
		root = &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newMarkCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"mark", "--review", "last"})
		if err := root.Execute(); err != nil {
			t.Fatalf("mark review failed: %v", err)
		}

		buf.Reset()
		root = &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newListCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"list"})
		if err := root.Execute(); err != nil {
			t.Fatalf("list after review failed: %v", err)
		}
		output = buf.String()
		if !strings.Contains(output, "[bad] second") || !strings.Contains(output, "[review] third") {
			t.Fatalf("expected review mark to replace bad mark on last checkpoint, got %q", output)
		}

		buf.Reset()
		root = &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newMarkCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"mark", "--unmark", "last"})
		if err := root.Execute(); err != nil {
			t.Fatalf("unmark failed: %v", err)
		}

		buf.Reset()
		root = &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newListCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"list"})
		if err := root.Execute(); err != nil {
			t.Fatalf("list after unmark failed: %v", err)
		}
		output = buf.String()
		if !strings.Contains(output, "[bad] second") || !strings.Contains(output, "third") || strings.Contains(output, "[unmarked]") {
			t.Fatalf("expected unmark to clear last checkpoint, got %q", output)
		}
	})
}

func TestListCommandListsRangeForBranchScope(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		createAutosnapTestCommitRef(t, repo, branchRef, "20260101T000000Z", "current branch checkpoint")
		createAutosnapTestCommitRef(t, repo, "feature/foo", "20260101T000000Z", "feature first")
		createAutosnapTestCommitRef(t, repo, "feature/foo", "20260101T000001Z", "feature second")
		createAutosnapTestCommitRef(t, repo, "feature/foo", "20260101T000002Z", "feature third")

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newListCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"list", "--branch", "feature/foo", "first+1..last"})

		if err := root.Execute(); err != nil {
			t.Fatalf("list branch checkpoint range failed: %v", err)
		}

		output := buf.String()
		if strings.Contains(output, "current branch checkpoint") || strings.Contains(output, "feature first") {
			t.Fatalf("expected branch range list to exclude out-of-range checkpoints, got %q", output)
		}
		if !strings.Contains(output, "feature second") || !strings.Contains(output, "feature third") {
			t.Fatalf("expected branch range list to include selected feature checkpoints, got %q", output)
		}
	})
}

func TestListCommandSinceDuration(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		oldTimestamp := time.Now().UTC().Add(-10 * 24 * time.Hour).Format("20060102T150405Z")
		newTimestamp := time.Now().UTC().Add(-1 * time.Hour).Format("20060102T150405Z")
		createAutosnapTestCommitRef(t, repo, branchRef, oldTimestamp, "old list since duration")
		createAutosnapTestCommitRef(t, repo, branchRef, newTimestamp, "new list since duration")

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newListCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"list", "--since", "7d"})
		if err := root.Execute(); err != nil {
			t.Fatalf("list since duration command failed: %v", err)
		}

		output := buf.String()
		if strings.Contains(output, "old list since duration") {
			t.Fatalf("expected old checkpoint to be excluded by since duration, got %q", output)
		}
		if !strings.Contains(output, "new list since duration") {
			t.Fatalf("expected new checkpoint in since duration output, got %q", output)
		}
	})
}

func TestListCommandSinceCheckpointCommitUsesCheckpointTimestamp(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		createAutosnapTestCommitRef(t, repo, branchRef, "20220101T000000Z", "old list since checkpoint")
		middleRef := createAutosnapTestCommitRef(t, repo, branchRef, "20220102T000000Z", "middle list since checkpoint")
		createAutosnapTestCommitRef(t, repo, branchRef, "20220103T000000Z", "new list since checkpoint")
		middleCommit := runGitOutput(t, repo, "rev-parse", middleRef)

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newListCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"list", "--since", middleCommit})
		if err := root.Execute(); err != nil {
			t.Fatalf("list since checkpoint command failed: %v", err)
		}

		output := buf.String()
		if strings.Contains(output, "old list since checkpoint") {
			t.Fatalf("expected older checkpoint to be excluded by checkpoint cutoff, got %q", output)
		}
		if !strings.Contains(output, "middle list since checkpoint") || !strings.Contains(output, "new list since checkpoint") {
			t.Fatalf("expected cutoff checkpoint and newer checkpoint, got %q", output)
		}
	})
}

func TestListCommandRejectsAllScopeWithRange(t *testing.T) {
	t.Parallel()
	root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
	root.AddCommand(newListCommand())
	root.SetArgs([]string{"list", "--all", "first..last"})

	err := root.Execute()
	if err == nil {
		t.Fatalf("expected list --all with range to fail")
	}
	if !strings.Contains(err.Error(), "checkpoint ranges only for current branch or --branch") {
		t.Fatalf("expected all scope range error, got %v", err)
	}
}

func TestListCommandRejectsMalformedRange(t *testing.T) {
	root := NewRootCommand()
	root.SetArgs([]string{"list", "first..last..last"})

	err := root.Execute()
	if err == nil {
		t.Fatalf("expected malformed checkpoint range to fail")
	}
	if !strings.Contains(err.Error(), "invalid checkpoint range") {
		t.Fatalf("expected invalid checkpoint range error, got %v", err)
	}
}

func TestListCommandJSONLOutput(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		ref := createAutosnapTestCommitRef(t, repo, branchRef, "20260101T000000Z", "jsonl checkpoint")

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newListCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"list", "--format", "jsonl"})

		if err := root.Execute(); err != nil {
			t.Fatalf("list jsonl command failed: %v", err)
		}

		rows := parseJSONLRows(t, buf.String())
		if len(rows) != 1 {
			t.Fatalf("expected one JSONL row, got %d: %q", len(rows), buf.String())
		}
		row := rows[0]
		if row["ref"] != ref || row["branch"] != branchRef || row["summary"] != "jsonl checkpoint" {
			t.Fatalf("unexpected JSONL row: %#v", row)
		}
		if row["timestamp"] != formatCheckpointTimestampForList("20260101T000000Z") {
			t.Fatalf("expected formatted timestamp, got %#v", row["timestamp"])
		}
	})
}

func TestListCommandShowsWorktreeMatchMarkers(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		createAutosnapTestCommitRef(t, repo, branchRef, "20260101T000000Z", "head checkpoint")
		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("staged\n"), 0o644); err != nil {
			t.Fatalf("write staged file failed: %v", err)
		}
		runGit(t, repo, "add", "file.txt")
		indexTree := runGitOutput(t, repo, "write-tree")
		createAutosnapTestCommitRefFromTree(t, repo, branchRef, "20260101T000001Z", indexTree, "index checkpoint")

		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("worktree\n"), 0o644); err != nil {
			t.Fatalf("write worktree file failed: %v", err)
		}
		gitDirectory, err := gitDir(context.Background(), repo)
		if err != nil {
			t.Fatalf("gitDir failed: %v", err)
		}
		worktreeTree, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
		if err != nil {
			t.Fatalf("compute worktree tree failed: %v", err)
		}
		createAutosnapTestCommitRefFromTree(t, repo, branchRef, "20260101T000002Z", worktreeTree, "worktree checkpoint")

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newListCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"list"})
		if err := root.Execute(); err != nil {
			t.Fatalf("list command failed: %v", err)
		}

		output := buf.String()
		if !strings.Contains(output, "**  worktree checkpoint") {
			t.Fatalf("expected worktree match marker in list output, got %q", output)
		}
		if !strings.Contains(output, "*   index checkpoint") {
			t.Fatalf("expected index match marker in list output, got %q", output)
		}
	})
}

func TestListCommandJSONLIncludesWorktreeMatch(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		createAutosnapTestCommitRef(t, repo, branchRef, "20260101T000000Z", "head checkpoint")
		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("staged\n"), 0o644); err != nil {
			t.Fatalf("write staged file failed: %v", err)
		}
		runGit(t, repo, "add", "file.txt")
		indexTree := runGitOutput(t, repo, "write-tree")
		indexRef := createAutosnapTestCommitRefFromTree(t, repo, branchRef, "20260101T000001Z", indexTree, "index checkpoint")

		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("worktree\n"), 0o644); err != nil {
			t.Fatalf("write worktree file failed: %v", err)
		}
		gitDirectory, err := gitDir(context.Background(), repo)
		if err != nil {
			t.Fatalf("gitDir failed: %v", err)
		}
		worktreeTree, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
		if err != nil {
			t.Fatalf("compute worktree tree failed: %v", err)
		}
		worktreeRef := createAutosnapTestCommitRefFromTree(t, repo, branchRef, "20260101T000002Z", worktreeTree, "worktree checkpoint")

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newListCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"list", "--format", "jsonl"})
		if err := root.Execute(); err != nil {
			t.Fatalf("list jsonl command failed: %v", err)
		}

		byRef := jsonRowsByRef(parseJSONLRows(t, buf.String()))
		if byRef[worktreeRef]["worktreeMatch"] != string(checkpointWorktreeMatchWorktree) {
			t.Fatalf("expected worktree JSON marker, got %#v", byRef[worktreeRef])
		}
		if byRef[indexRef]["worktreeMatch"] != string(checkpointWorktreeMatchIndex) {
			t.Fatalf("expected index JSON marker, got %#v", byRef[indexRef])
		}
	})
}

func TestListCommandJSONLOutputIncludesDecodedJSONNote(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		ref := createAutosnapTestCommitRef(t, repo, branchRef, "20260101T000000Z", "noted checkpoint")
		addGitNote(t, repo, "refs/notes/diffcog", ref, `{"risk":"low","files":["a.go"]}`)

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newListCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"list", "--format", "jsonl", "--notes-json", "--note-ref", "refs/notes/diffcog"})

		if err := root.Execute(); err != nil {
			t.Fatalf("list jsonl notes command failed: %v", err)
		}

		rows := parseJSONLRows(t, buf.String())
		if len(rows) != 1 {
			t.Fatalf("expected one JSONL row, got %d: %q", len(rows), buf.String())
		}
		note, ok := rows[0]["note"].(map[string]any)
		if !ok {
			t.Fatalf("expected decoded note object, got %#v", rows[0]["note"])
		}
		if note["risk"] != "low" {
			t.Fatalf("expected decoded note risk, got %#v", note)
		}
		files, ok := note["files"].([]any)
		if !ok || len(files) != 1 || files[0] != "a.go" {
			t.Fatalf("expected decoded note files, got %#v", note["files"])
		}
		if rows[0]["noteRef"] != "refs/notes/diffcog" {
			t.Fatalf("expected noteRef in row, got %#v", rows[0])
		}
	})
}

func TestListCommandJSONLNotesUseConfiguredNoteRef(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		if err := os.WriteFile(autosnapConfigPath(repo), []byte("note_ref = \"refs/notes/configured\"\n"), 0o644); err != nil {
			t.Fatalf("write config failed: %v", err)
		}
		ref := createAutosnapTestCommitRef(t, repo, branchRef, "20260101T000000Z", "configured note checkpoint")
		addGitNote(t, repo, "refs/notes/configured", ref, `{"source":"config"}`)

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newListCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"list", "--format", "jsonl", "--notes-json"})

		if err := root.Execute(); err != nil {
			t.Fatalf("list configured note ref failed: %v", err)
		}

		rows := parseJSONLRows(t, buf.String())
		if len(rows) != 1 {
			t.Fatalf("expected one JSONL row, got %d: %q", len(rows), buf.String())
		}
		if rows[0]["noteRef"] != "refs/notes/configured" {
			t.Fatalf("expected configured noteRef, got %#v", rows[0])
		}
	})
}

func TestListCommandJSONOutputIncludesRawNoteString(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		ref := createAutosnapTestCommitRef(t, repo, branchRef, "20260101T000000Z", "raw note checkpoint")
		addGitNote(t, repo, "refs/notes/diffcog", ref, `{"risk":"low"}`)

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newListCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"list", "--format", "json", "--notes", "--note-ref", "refs/notes/diffcog"})

		if err := root.Execute(); err != nil {
			t.Fatalf("list json raw notes command failed: %v", err)
		}

		rows := parseJSONRows(t, buf.String())
		if len(rows) != 1 {
			t.Fatalf("expected one JSON row, got %d: %q", len(rows), buf.String())
		}
		if rows[0]["note"] != `{"risk":"low"}` {
			t.Fatalf("expected raw note string, got %#v", rows[0]["note"])
		}
	})
}

func TestListCommandTextOutputIncludesRawNoteString(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		ref := createAutosnapTestCommitRef(t, repo, branchRef, "20260101T000000Z", "text note checkpoint")
		addGitNote(t, repo, "refs/notes/diffcog", ref, `{"risk":"text"}`)

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newListCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"list", "--notes", "--note-ref", "refs/notes/diffcog"})

		if err := root.Execute(); err != nil {
			t.Fatalf("list text notes command failed: %v", err)
		}

		output := buf.String()
		if strings.Contains(strings.TrimSpace(output), "{") && strings.HasPrefix(strings.TrimSpace(output), "{") {
			t.Fatalf("expected text output, got JSON-looking output %q", output)
		}
		if !strings.Contains(output, "note_ref: refs/notes/diffcog") || !strings.Contains(output, `note: {"risk":"text"}`) {
			t.Fatalf("expected raw text note output, got %q", output)
		}
	})
}

func TestListCommandJSONLNotesReportMissingAndInvalidNotes(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		missingRef := createAutosnapTestCommitRef(t, repo, branchRef, "20260101T000000Z", "missing note checkpoint")
		invalidRef := createAutosnapTestCommitRef(t, repo, branchRef, "20260101T000001Z", "invalid note checkpoint")
		addGitNote(t, repo, "refs/notes/diffcog", invalidRef, `{not json`)

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newListCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"list", "--format", "jsonl", "--notes-json", "--note-ref", "refs/notes/diffcog"})

		if err := root.Execute(); err != nil {
			t.Fatalf("list missing/invalid notes failed: %v", err)
		}

		rows := parseJSONLRows(t, buf.String())
		if len(rows) != 2 {
			t.Fatalf("expected two JSONL rows, got %d: %q", len(rows), buf.String())
		}
		byRef := jsonRowsByRef(rows)
		if byRef[missingRef]["note"] != nil {
			t.Fatalf("expected missing note to be null, got %#v", byRef[missingRef])
		}
		if _, ok := byRef[missingRef]["noteError"]; ok {
			t.Fatalf("expected missing note not to set noteError, got %#v", byRef[missingRef])
		}
		if byRef[invalidRef]["note"] != nil {
			t.Fatalf("expected invalid note to be null, got %#v", byRef[invalidRef])
		}
		if noteError, ok := byRef[invalidRef]["noteError"].(string); !ok || !strings.Contains(noteError, "invalid JSON note") {
			t.Fatalf("expected invalid noteError, got %#v", byRef[invalidRef])
		}
	})
}

func TestListCommandJSONLNotesValidateFlags(t *testing.T) {
	t.Parallel()
	root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
	root.AddCommand(newListCommand())
	root.SetArgs([]string{"list", "--format", "text", "--notes-json", "--note-ref", "refs/notes/diffcog"})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "--format json or jsonl") {
		t.Fatalf("expected notes-json format error, got %v", err)
	}

	root = &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
	root.AddCommand(newListCommand())
	root.SetArgs([]string{"list", "--notes"})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "--note-ref") {
		t.Fatalf("expected missing note ref error, got %v", err)
	}

	root = &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
	root.AddCommand(newListCommand())
	root.SetArgs([]string{"list", "--format", "json", "--notes", "--notes-json", "--note-ref", "refs/notes/diffcog"})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected mutually exclusive notes error, got %v", err)
	}
}

func TestListCommandRejectsMultipleScopes(t *testing.T) {
	root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
	root.AddCommand(newListCommand())
	root.SetArgs([]string{"list", "--branch", "feature/foo", "--all"})

	err := root.Execute()
	if err == nil {
		t.Fatalf("expected list to fail")
	}
	if !strings.Contains(err.Error(), "at most one scope") {
		t.Fatalf("expected scope error, got %v", err)
	}
}

func TestListCheckpointsFromRefsIncludesFailedMetadata(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		validRef := createAutosnapTestCommitRef(t, repo, branchRef, "20260101T000000Z", "visible checkpoint")
		missingRef := snapshotRef(branchRef, "20990101T000000Z-missing")

		checkpoints, err := listCheckpointsFromRefs(context.Background(), repo, []checkpointRefInfo{
			{
				Ref:       validRef,
				Commit:    "deadbeef",
				Timestamp: "20260101T000000Z",
				Branch:    branchRef,
			},
			{
				Ref:       missingRef,
				Commit:    "cafebabe",
				Timestamp: "20260102T000000Z",
				Branch:    branchRef,
			},
		})
		if err != nil {
			t.Fatalf("listCheckpointsFromRefs failed: %v", err)
		}
		if len(checkpoints) != 2 {
			t.Fatalf("expected both checkpoints to be returned, got %d", len(checkpoints))
		}

		summaries := map[string]string{}
		for _, checkpoint := range checkpoints {
			summaries[checkpoint.Ref] = checkpoint.Summary
		}
		if summaries[validRef] == failedCommitMetadataSummary {
			t.Fatalf("expected valid ref %s not to fail", validRef)
		}
		if summaries[missingRef] != failedCommitMetadataSummary {
			t.Fatalf("expected missing ref %s to fail metadata read, got %q", missingRef, summaries[missingRef])
		}
	})
}

func TestListCheckpointsFromRefsUsesFirstContentLine(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		ref := createAutosnapTestCommitRef(t, repo, branchRef, "20260101T000000Z", "feat(logging): implement structured logging\n \nbody line")
		checkpoints, err := listCheckpointsFromRefs(context.Background(), repo, []checkpointRefInfo{
			{
				Ref:       ref,
				Commit:    "deadbeef",
				Timestamp: "20260101T000000Z",
				Branch:    branchRef,
			},
		})
		if err != nil {
			t.Fatalf("listCheckpointsFromRefs failed: %v", err)
		}
		if len(checkpoints) != 1 {
			t.Fatalf("expected one checkpoint, got %d", len(checkpoints))
		}
		if got, want := checkpoints[0].Summary, "feat(logging): implement structured logging"; got != want {
			t.Fatalf("expected summary %q, got %q", want, got)
		}
	})
}

func TestCheckpointTreeLinesUsesCachedTrees(t *testing.T) {
	t.Parallel()
	treeLines, err := checkpointTreeLines(context.Background(), "", []checkpointInfo{
		{Ref: "refs/autosnapshots/main/20260101T000000Z", Tree: "1111111111111111111111111111111111111111"},
		{Ref: "refs/autosnapshots/main/20260101T000001Z", Tree: "2222222222222222222222222222222222222222"},
	})
	if err != nil {
		t.Fatalf("checkpointTreeLines failed: %v", err)
	}
	if len(treeLines) != 2 || treeLines[0] != "1111111111111111111111111111111111111111" || treeLines[1] != "2222222222222222222222222222222222222222" {
		t.Fatalf("expected cached tree lines in order, got %#v", treeLines)
	}
}

func TestCheckpointTreeLinesResolvesOnlyMissingTreesInOrder(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		firstRef := createAutosnapTestCommitRef(t, repo, branchRef, "20260101T000000Z", "first checkpoint")
		if err := os.WriteFile(filepath.Join(repo, "mixed-cache.txt"), []byte("changed\n"), 0o644); err != nil {
			t.Fatalf("write file failed: %v", err)
		}
		runGit(t, repo, "add", "mixed-cache.txt")
		secondRef := createAutosnapTestCommitRefFromIndex(t, repo, branchRef, "20260101T000001Z", "second checkpoint")
		firstTree := runGitOutput(t, repo, "rev-parse", firstRef+"^{tree}")
		secondTree := runGitOutput(t, repo, "rev-parse", secondRef+"^{tree}")

		treeLines, err := checkpointTreeLines(context.Background(), repo, []checkpointInfo{
			{Ref: firstRef, Tree: firstTree},
			{Ref: secondRef},
		})
		if err != nil {
			t.Fatalf("checkpointTreeLines failed: %v", err)
		}
		if len(treeLines) != 2 || treeLines[0] != firstTree || treeLines[1] != secondTree {
			t.Fatalf("expected mixed cached/resolved tree lines in order, got %#v want [%s %s]", treeLines, firstTree, secondTree)
		}
	})
}
