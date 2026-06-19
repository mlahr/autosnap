package autosnap

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

func newShowCommand() *cobra.Command {
	var (
		full      bool
		nameOnly  bool
		colorMode string
	)

	cmd := &cobra.Command{
		Use:   "show <checkpoint>",
		Short: "Show checkpoint details",
		Long: strings.TrimSpace(`Show checkpoint metadata and the diff for a checkpoint.

The checkpoint argument can be an explicit autosnap ref, a checkpoint commit hash,
or one of these current-branch history selectors:

  first
  first+N
  last
  last-N

Examples: first+1 selects the second checkpoint. last-1 selects the checkpoint
immediately before the latest checkpoint.`),
		Example: strings.TrimSpace(`autosnap show last
autosnap show last-1
autosnap show first+1
autosnap show --name-only refs/autosnapshots/main/20260605T120000Z`),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			ctx := context.Background()
			repoRoot, _, branchRef, err := detectRepository(ctx)
			if err != nil {
				return err
			}

			meta, err := resolveShowCheckpointRefMetadata(ctx, repoRoot, branchRef, args[0])
			if err != nil {
				return err
			}

			fmt.Fprintf(out, "checkpoint: %s\n", meta.Ref)
			fmt.Fprintf(out, "commit: %s\n", meta.Commit)
			fmt.Fprintf(out, "timestamp: %s\n", formatCheckpointTimestamp(meta.Timestamp))

			message, err := getCommitMessage(ctx, repoRoot, meta.Ref)
			if err == nil && message != "" {
				status, checkCmd := parseCheckpointMessage(message)
				fmt.Fprintf(out, "status: %s\n", status)
				if checkCmd != "" {
					fmt.Fprintf(out, "check: %s\n", checkCmd)
				}
			}

			colorArg, err := normalizeShowColorArg(colorMode, out)
			if err != nil {
				return err
			}

			diffBase, err := resolveShowDiffBase(ctx, repoRoot, branchRef, meta.Ref, meta.Commit)
			if err != nil {
				return fmt.Errorf("failed to resolve show diff base for checkpoint %q: %w", meta.Ref, err)
			}

			showArgs := []string{colorArg}
			if nameOnly {
				showArgs = append(showArgs, "--name-only")
			}
			showArgs = append(showArgs, diffBase, meta.Commit)

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
	cmd.Flags().BoolVar(&nameOnly, "name-only", false, "Show only changed file names")
	cmd.Flags().StringVar(&colorMode, "color", "auto", "Color output: auto, always, never")
	return cmd
}

func resolveShowCheckpointRefMetadata(ctx context.Context, repoRoot, branchRef, arg string) (checkpointRefInfo, error) {
	ref, matched, err := resolveShowHistorySelectorRef(ctx, repoRoot, branchRef, arg)
	if err != nil {
		if matched {
			return checkpointRefInfo{}, err
		}
		return resolveCheckpointRefMetadata(ctx, repoRoot, branchRef, arg)
	}

	if matched {
		entries, err := listCheckpointRefsForBranch(ctx, repoRoot, branchRef)
		if err != nil {
			return checkpointRefInfo{}, err
		}

		for _, entry := range entries {
			if entry.Ref == ref {
				entry.Timestamp = checkpointRefTimestamp(ref)
				return entry, nil
			}
		}

		return resolveCheckpointRefMetadata(ctx, repoRoot, branchRef, ref)
	}

	return resolveCheckpointRefMetadata(ctx, repoRoot, branchRef, arg)
}

type showHistorySelectorError struct {
	err error
}

func (e showHistorySelectorError) Error() string {
	return e.err.Error()
}

func resolveShowHistorySelectorRef(ctx context.Context, repoRoot, branchRef, arg string) (string, bool, error) {
	selector := strings.TrimSpace(arg)
	if selector == "" {
		return "", true, showHistorySelectorError{err: fmt.Errorf("checkpoint identifier is required")}
	}

	if selector == "first" {
		resolved, err := resolveShowHistoryRefAtOffset(ctx, repoRoot, branchRef, 0, true)
		return resolved, true, err
	}
	if selector == "last" {
		resolved, err := resolveShowHistoryRefAtOffset(ctx, repoRoot, branchRef, 0, false)
		return resolved, true, err
	}

	if strings.HasPrefix(selector, "first+") {
		offsetText := strings.TrimPrefix(selector, "first+")
		offset, err := strconv.Atoi(offsetText)
		if err != nil {
			return "", true, showHistorySelectorError{err: fmt.Errorf("invalid show selector %q", arg)}
		}
		if offset < 1 {
			return "", true, showHistorySelectorError{err: fmt.Errorf("show selector %q requires a positive offset", arg)}
		}
		resolved, err := resolveShowHistoryRefAtOffset(ctx, repoRoot, branchRef, offset, true)
		return resolved, true, err
	}

	if strings.HasPrefix(selector, "last-") {
		offsetText := strings.TrimPrefix(selector, "last-")
		offset, err := strconv.Atoi(offsetText)
		if err != nil {
			return "", true, showHistorySelectorError{err: fmt.Errorf("invalid show selector %q", arg)}
		}
		if offset < 1 {
			return "", true, showHistorySelectorError{err: fmt.Errorf("show selector %q requires a positive offset", arg)}
		}
		resolved, err := resolveShowHistoryRefAtOffset(ctx, repoRoot, branchRef, offset, false)
		return resolved, true, err
	}

	return "", false, nil
}

func resolveShowHistoryRefAtOffset(ctx context.Context, repoRoot, branchRef string, offset int, fromFirst bool) (string, error) {
	checkpoints, err := listCheckpointRefsForBranch(ctx, repoRoot, branchRef)
	if err != nil {
		return "", err
	}
	if len(checkpoints) == 0 {
		return "", showHistorySelectorError{err: fmt.Errorf("no checkpoints for current branch")}
	}

	if fromFirst {
		if offset < 0 || offset >= len(checkpoints) {
			return "", showHistorySelectorError{err: fmt.Errorf("show selector out of range")}
		}
		return checkpoints[offset].Ref, nil
	}

	if offset > len(checkpoints) {
		return "", showHistorySelectorError{err: fmt.Errorf("show selector out of range")}
	}

	idx := len(checkpoints) - 1 - offset
	if idx < 0 {
		return "", showHistorySelectorError{err: fmt.Errorf("show selector out of range")}
	}
	return checkpoints[idx].Ref, nil
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
