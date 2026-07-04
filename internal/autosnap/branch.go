package autosnap

import (
	"context"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/spf13/cobra"
)

func newBranchCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "branch",
		Short: "Create Git branches with autosnap checkpoints",
		Long: strings.TrimSpace(`Create Git branches and copy autosnap checkpoint refs between branch namespaces.

Autosnap checkpoints are stored as refs under refs/autosnapshots/<branch>/.
Branch commands copy those refs. They do not duplicate checkpoint commit
objects, apply checkpoint patches, or push anything to a remote.`),
	}

	cmd.AddCommand(newBranchCreateCommand())
	cmd.AddCommand(newBranchCopyCommand())
	return cmd
}

func newBranchCreateCommand() *cobra.Command {
	var (
		noCopy    bool
		overwrite bool
	)

	cmd := &cobra.Command{
		Use:   "create <branch>",
		Short: "Create and check out a Git branch with checkpoint refs",
		Long: strings.TrimSpace(`Create and check out a Git branch, then copy autosnap checkpoint refs
from the previously checked-out branch into the new branch namespace.

This is equivalent to git checkout -b <branch> plus copying refs from
refs/autosnapshots/<source>/ to refs/autosnapshots/<branch>/.`),
		Example: strings.TrimSpace(`autosnap branch create feature/next
autosnap branch create feature/next --no-copy-checkpoints
autosnap branch create feature/next --overwrite`),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			ctx := context.Background()
			targetBranch := strings.TrimSpace(args[0])
			if err := validateGitBranchName(ctx, targetBranch); err != nil {
				return err
			}

			repoRoot, _, sourceBranch, err := detectRepository(ctx)
			if err != nil {
				return err
			}
			if sourceBranch == "" || strings.HasPrefix(sourceBranch, "detached") {
				return fmt.Errorf("branch create requires a checked-out source branch")
			}

			var copyPlan []checkpointRefCopy
			if !noCopy {
				copyPlan, err = prepareCheckpointRefCopyPlan(ctx, repoRoot, sourceBranch, targetBranch, overwrite)
				if err != nil {
					return err
				}
			}

			result, err := runGitCommand(ctx, repoRoot, nil, "checkout", "-b", targetBranch)
			if err != nil {
				return gitCommandError(err, result)
			}
			fmt.Fprintf(out, "created and checked out branch %s\n", targetBranch)

			if noCopy {
				return nil
			}

			count, err := applyCheckpointRefCopyPlan(ctx, repoRoot, copyPlan)
			if err != nil {
				return err
			}
			printCheckpointRefCopyResult(out, count, sourceBranch, targetBranch)
			return nil
		},
	}

	cmd.Flags().BoolVar(&noCopy, "no-copy-checkpoints", false, "Create the Git branch without copying checkpoint refs")
	cmd.Flags().BoolVar(&overwrite, "overwrite", false, "Replace colliding target checkpoint refs")
	return cmd
}

func newBranchCopyCommand() *cobra.Command {
	var (
		from      string
		to        string
		overwrite bool
	)

	cmd := &cobra.Command{
		Use:   "copy",
		Short: "Copy checkpoint refs between autosnap branch namespaces",
		Long: strings.TrimSpace(`Copy autosnap checkpoint refs from one branch namespace to another.

The target Git branch must already exist. This command does not check out or
create Git branches.`),
		Example: strings.TrimSpace(`autosnap branch copy --from main --to feature/next
autosnap branch copy --from main --to feature/next --overwrite`),
		Args: cobra.ExactArgs(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			ctx := context.Background()
			sourceBranch := strings.TrimSpace(from)
			targetBranch := strings.TrimSpace(to)
			if sourceBranch == "" {
				return fmt.Errorf("branch copy requires --from")
			}
			if targetBranch == "" {
				return fmt.Errorf("branch copy requires --to")
			}
			if err := validateGitBranchName(ctx, sourceBranch); err != nil {
				return fmt.Errorf("invalid --from: %w", err)
			}
			if err := validateGitBranchName(ctx, targetBranch); err != nil {
				return fmt.Errorf("invalid --to: %w", err)
			}

			repoRoot, _, _, err := detectRepository(ctx)
			if err != nil {
				return err
			}
			if exists, err := gitBranchExists(ctx, repoRoot, targetBranch); err != nil {
				return err
			} else if !exists {
				return fmt.Errorf("target Git branch does not exist: %s", targetBranch)
			}

			count, err := copyCheckpointRefsBetweenBranches(ctx, repoRoot, sourceBranch, targetBranch, overwrite)
			if err != nil {
				return err
			}
			printCheckpointRefCopyResult(out, count, sourceBranch, targetBranch)
			return nil
		},
	}

	cmd.Flags().StringVar(&from, "from", "", "Source autosnap branch namespace")
	cmd.Flags().StringVar(&to, "to", "", "Target autosnap branch namespace")
	cmd.Flags().BoolVar(&overwrite, "overwrite", false, "Replace colliding target checkpoint refs")
	return cmd
}

func validateGitBranchName(ctx context.Context, branch string) error {
	if strings.TrimSpace(branch) == "" {
		return fmt.Errorf("branch name is required")
	}
	result, err := runGitCommand(ctx, ".", nil, "check-ref-format", "--branch", branch)
	if err != nil {
		return fmt.Errorf("invalid branch name %q: %w", branch, gitCommandError(err, result))
	}
	return nil
}

func gitBranchExists(ctx context.Context, repoRoot, branch string) (bool, error) {
	result, err := runGitCommand(ctx, repoRoot, nil, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	if err == nil {
		return true, nil
	}
	if result.ExitCode == 1 {
		return false, nil
	}
	return false, gitCommandError(err, result)
}

func copyCheckpointRefsBetweenBranches(ctx context.Context, repoRoot, sourceBranch, targetBranch string, overwrite bool) (int, error) {
	plan, err := prepareCheckpointRefCopyPlan(ctx, repoRoot, sourceBranch, targetBranch, overwrite)
	if err != nil {
		return 0, err
	}
	return applyCheckpointRefCopyPlan(ctx, repoRoot, plan)
}

type checkpointRefCopy struct {
	TargetRef string
	Commit    string
}

func prepareCheckpointRefCopyPlan(ctx context.Context, repoRoot, sourceBranch, targetBranch string, overwrite bool) ([]checkpointRefCopy, error) {
	sourceRefs, err := listCheckpointRefsForBranch(ctx, repoRoot, sourceBranch)
	if err != nil {
		return nil, err
	}
	if len(sourceRefs) == 0 {
		return nil, nil
	}

	plan := make([]checkpointRefCopy, 0, len(sourceRefs))
	for _, sourceRef := range sourceRefs {
		commit := strings.TrimSpace(sourceRef.FullCommit)
		if commit == "" {
			commit = strings.TrimSpace(sourceRef.Commit)
		}
		if commit == "" {
			return nil, fmt.Errorf("checkpoint ref %s has no commit", sourceRef.Ref)
		}
		plan = append(plan, checkpointRefCopy{
			TargetRef: targetCheckpointRefForSource(sourceRef.Ref, targetBranch),
			Commit:    commit,
		})
	}

	if !overwrite {
		targetRefs := make([]string, 0, len(plan))
		for _, refCopy := range plan {
			targetRefs = append(targetRefs, refCopy.TargetRef)
		}
		collisions, err := existingRefs(ctx, repoRoot, targetRefs)
		if err != nil {
			return nil, err
		}
		if len(collisions) > 0 {
			return nil, checkpointRefCollisionError{targetBranch: targetBranch, refs: collisions}
		}
	}

	return plan, nil
}

func applyCheckpointRefCopyPlan(ctx context.Context, repoRoot string, plan []checkpointRefCopy) (int, error) {
	for _, refCopy := range plan {
		result, err := runGitCommand(ctx, repoRoot, nil, "update-ref", refCopy.TargetRef, refCopy.Commit)
		if err != nil {
			return 0, gitCommandError(err, result)
		}
	}
	return len(plan), nil
}

func targetCheckpointRefForSource(sourceRef, targetBranch string) string {
	return snapshotRef(targetBranch, path.Base(sourceRef))
}

func existingRefs(ctx context.Context, repoRoot string, refs []string) ([]string, error) {
	var existing []string
	for _, ref := range refs {
		result, err := runGitCommand(ctx, repoRoot, nil, "show-ref", "--verify", "--quiet", ref)
		if err == nil {
			existing = append(existing, ref)
			continue
		}
		if result.ExitCode == 1 {
			continue
		}
		return nil, gitCommandError(err, result)
	}
	return existing, nil
}

type checkpointRefCollisionError struct {
	targetBranch string
	refs         []string
}

func (e checkpointRefCollisionError) Error() string {
	return fmt.Sprintf("target branch %s already has %d checkpoint ref(s); use --overwrite to replace colliding refs", e.targetBranch, len(e.refs))
}

func printCheckpointRefCopyResult(out io.Writer, count int, sourceBranch, targetBranch string) {
	if count == 0 {
		fmt.Fprintf(out, "no checkpoints for branch %s\n", sourceBranch)
		return
	}
	fmt.Fprintf(out, "copied %d checkpoint ref(s) from %s to %s\n", count, sourceBranch, targetBranch)
}
