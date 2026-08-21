package autosnap

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

func newShowCommand() *cobra.Command {
	var (
		full      bool
		gitDiff   bool
		nameOnly  bool
		colorMode string
	)

	cmd := &cobra.Command{
		Use:   "show [checkpoint-or-range]",
		Short: "Show checkpoint details",
		Long: strings.TrimSpace(`Show checkpoint metadata and the diff for a checkpoint.

With no checkpoint argument, show defaults to the last checkpoint on the current branch.

A range A..B shows the net patch from the diff base of A through B. Ranges are
inclusive autosnap checkpoint intervals, not general Git revision ranges.

The checkpoint argument can be an explicit autosnap ref, a checkpoint commit hash,
or one of these current-branch history selectors:

  first
  first+N
  last
  last-N

Examples: first+1 selects the second checkpoint. last-1 selects the checkpoint
immediately before the latest checkpoint.`),
		Example: strings.TrimSpace(`autosnap show
autosnap show last
autosnap show last-1
autosnap show first+1
autosnap show first+1..last
autosnap show --name-only refs/autosnapshots/main/20260605T120000Z`),
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			ctx := context.Background()
			repoRoot, _, branchRef, err := detectRepository(ctx)
			if err != nil {
				return err
			}

			checkpointArg := "last"
			if len(args) == 1 {
				checkpointArg = args[0]
			}

			_, _, ranged, err := splitCheckpointRangeArg(checkpointArg)
			if err != nil {
				return err
			}
			patchRange, err := resolveCheckpointPatchRange(ctx, repoRoot, branchRef, checkpointArg)
			if err != nil {
				return err
			}
			meta := patchRange.End

			if !gitDiff {
				if ranged {
					fmt.Fprintf(out, "checkpoint range: %s..%s\n", patchRange.Start.Ref, patchRange.End.Ref)
					fmt.Fprintf(out, "start checkpoint: %s\n", patchRange.Start.Ref)
					fmt.Fprintf(out, "end checkpoint: %s\n", patchRange.End.Ref)
					fmt.Fprintf(out, "end commit: %s\n", meta.Commit)
					fmt.Fprintf(out, "end timestamp: %s\n", formatCheckpointTimestamp(meta.Timestamp))
				} else {
					fmt.Fprintf(out, "checkpoint: %s\n", meta.Ref)
					fmt.Fprintf(out, "commit: %s\n", meta.Commit)
					fmt.Fprintf(out, "timestamp: %s\n", formatCheckpointTimestamp(meta.Timestamp))
				}

				message, err := getCommitMessage(ctx, repoRoot, meta.Ref)
				if err == nil && message != "" {
					status, checkCmd := parseCheckpointMessage(message)
					statusLabel := "status"
					checkLabel := "check"
					if ranged {
						statusLabel = "end status"
						checkLabel = "end check"
					}
					fmt.Fprintf(out, "%s: %s\n", statusLabel, status)
					if checkCmd != "" {
						fmt.Fprintf(out, "%s: %s\n", checkLabel, checkCmd)
					}
				}
				mark, err := readCheckpointMark(ctx, repoRoot, meta.Ref)
				if err != nil {
					return err
				}
				markLabel := "mark"
				if ranged {
					markLabel = "end mark"
				}
				if mark.Mark == "" {
					mark.Mark = checkpointMarkStateUnmarked
				}
				fmt.Fprintf(out, "%s: %s\n", markLabel, mark.Mark)
				if strings.TrimSpace(mark.Reason) != "" {
					fmt.Fprintf(out, "%s reason: %s\n", markLabel, strings.TrimSpace(mark.Reason))
				}
			}

			colorArg, err := normalizeShowColorArg(colorMode, out)
			if err != nil {
				return err
			}

			showArgs := []string{colorArg}
			if nameOnly {
				showArgs = append(showArgs, "--name-only")
			}
			showArgs = append(showArgs, patchRange.Base, meta.Commit)

			showResult, err := runGitCommand(ctx, repoRoot, nil, append([]string{"diff"}, showArgs...)...)
			if err != nil {
				return fmt.Errorf("failed to show checkpoint %q: %w", meta.Ref, err)
			}
			if strings.TrimSpace(showResult.Stdout) != "" {
				fmt.Fprint(out, showResult.Stdout)
				if !strings.HasSuffix(showResult.Stdout, "\n") {
					fmt.Fprintln(out)
				}
			}
			if strings.TrimSpace(showResult.Stderr) != "" {
				fmt.Fprint(out, showResult.Stderr)
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&full, "full", false, "Show full checkpoint diff (default)")
	cmd.Flags().BoolVar(&gitDiff, "git-diff", false, "Show only Git diff output, without autosnap metadata")
	cmd.Flags().BoolVar(&nameOnly, "name-only", false, "Show only changed file names")
	cmd.Flags().StringVar(&colorMode, "color", "auto", "Color output: auto, always, never")
	return cmd
}

func normalizeShowColorArg(mode string, outWriter io.Writer) (string, error) {
	switch mode {
	case "auto":
		if isTerminalWriter(outWriter) {
			return "--color=always", nil
		}
		return "--no-color", nil
	case "always":
		return "--color=always", nil
	case "never":
		return "--no-color", nil
	default:
		return "", fmt.Errorf("invalid --color value %q (expected auto, always, never)", mode)
	}
}

func resolveShowDiffBase(ctx context.Context, repoRoot, branchRef, targetRef, targetCommit string) (string, error) {
	targetBranch := branchRef
	if parsedBranch := branchFromCheckpointRef(targetRef); parsedBranch != "" {
		targetBranch = parsedBranch
	}

	checkpoints, err := listCheckpointRefsForBranch(ctx, repoRoot, targetBranch)
	if err != nil {
		return "", err
	}

	var previousRef string
	for _, checkpoint := range checkpoints {
		if checkpoint.Ref == targetRef {
			if previousRef != "" {
				return previousRef, nil
			}
			break
		}
		previousRef = checkpoint.Ref
	}

	parent, err := getCommitParent(ctx, repoRoot, targetCommit)
	if err != nil {
		return "", err
	}
	return parent, nil
}

func isTerminalWriter(writer io.Writer) bool {
	file, ok := writer.(*os.File)
	if !ok {
		return false
	}

	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
