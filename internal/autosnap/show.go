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
		colorMode string
	)

	cmd := &cobra.Command{
		Use:   "show <checkpoint>",
		Short: "Show checkpoint details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			ctx := context.Background()
			repoRoot, _, branchRef, err := detectRepository(ctx)
			if err != nil {
				return err
			}

			meta, err := resolveCheckpointRefMetadata(ctx, repoRoot, branchRef, args[0])
			if err != nil {
				return err
			}

			fmt.Fprintf(out, "checkpoint: %s\n", meta.Ref)
			fmt.Fprintf(out, "commit: %s\n", meta.Commit)
			fmt.Fprintf(out, "timestamp: %s\n", meta.Timestamp)

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
			if full {
				showArgs = append(showArgs, diffBase, meta.Commit)
			} else {
				showArgs = append(showArgs, "--stat", diffBase, meta.Commit)
			}

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

	cmd.Flags().BoolVar(&full, "full", false, "Show full checkpoint diff")
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
