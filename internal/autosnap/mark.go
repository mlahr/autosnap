package autosnap

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newMarkCommand() *cobra.Command {
	var (
		bad    bool
		good   bool
		review bool
		unmark bool
		label  string
		reason string
	)

	cmd := &cobra.Command{
		Use:   "mark (--label LABEL|--bad|--good|--review|--unmark) <checkpoint-or-range>",
		Short: "Mark checkpoints with an arbitrary label",
		Long: strings.TrimSpace(`Mark one checkpoint or an inclusive checkpoint range with an arbitrary label.

A range A..B marks checkpoints from A through B, inclusive. Ranges are
inclusive autosnap checkpoint intervals, not general Git revision ranges.

Labels must be 1-32 characters matching [A-Za-z0-9][A-Za-z0-9_-]*. The label
unmarked is reserved. Use --reason with --label or --bad to record why a
checkpoint was marked.

The checkpoint argument can be an explicit autosnap ref, a checkpoint commit hash,
or one of these current-branch history selectors:

  first
  first+N
  last
  last-N`),
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("mark requires exactly one checkpoint-or-range argument\n\nExamples:\n  autosnap mark --label review last\n  autosnap mark --bad last")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			ctx := context.Background()
			modeCount := 0
			labelProvided := cmd.Flags().Changed("label")
			if labelProvided {
				modeCount++
			}
			if bad {
				modeCount++
			}
			if good {
				modeCount++
			}
			if review {
				modeCount++
			}
			if unmark {
				modeCount++
			}
			if modeCount != 1 {
				return fmt.Errorf("mark requires exactly one of --label, --bad, --good, --review, or --unmark")
			}
			if labelProvided {
				var err error
				label, err = validateCheckpointMarkLabel(label)
				if err != nil {
					return err
				}
			}
			if !bad && !labelProvided && strings.TrimSpace(reason) != "" {
				return fmt.Errorf("--reason can only be used with --label or --bad")
			}

			repoRoot, _, branchRef, err := detectRepository(ctx)
			if err != nil {
				return err
			}
			refs, err := listCheckpointRefsForRange(ctx, repoRoot, branchRef, args[0])
			if err != nil {
				return err
			}
			if len(refs) == 0 {
				return fmt.Errorf("checkpoint not found: %q", args[0])
			}

			for _, ref := range refs {
				switch {
				case labelProvided:
					if err := writeCheckpointMark(ctx, repoRoot, ref, label, reason, time.Now().UTC()); err != nil {
						return fmt.Errorf("failed to mark checkpoint %q: %w", ref.Ref, err)
					}
				case bad:
					if err := markCheckpointBad(ctx, repoRoot, ref, reason, time.Now().UTC()); err != nil {
						return fmt.Errorf("failed to mark checkpoint %q bad: %w", ref.Ref, err)
					}
				case good:
					if err := markCheckpointGood(ctx, repoRoot, ref, time.Now().UTC()); err != nil {
						return fmt.Errorf("failed to mark checkpoint %q good: %w", ref.Ref, err)
					}
				case review:
					if err := markCheckpointReview(ctx, repoRoot, ref, time.Now().UTC()); err != nil {
						return fmt.Errorf("failed to mark checkpoint %q review: %w", ref.Ref, err)
					}
				case unmark:
					if err := unmarkCheckpoint(ctx, repoRoot, ref); err != nil {
						return fmt.Errorf("failed to unmark checkpoint %q: %w", ref.Ref, err)
					}
				}
			}

			state := "unmarked"
			switch {
			case labelProvided:
				state = label
			case bad:
				state = "bad"
			case good:
				state = "good"
			case review:
				state = "review"
			}
			if len(refs) == 1 {
				fmt.Fprintf(out, "checkpoint marked %s: %s %s\n", state, path.Base(refs[0].Ref), refs[0].Commit)
			} else {
				fmt.Fprintf(out, "checkpoint range marked %s: %s..%s (%d checkpoints)\n", state, path.Base(refs[0].Ref), path.Base(refs[len(refs)-1].Ref), len(refs))
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&bad, "bad", false, "Mark selected checkpoints bad")
	cmd.Flags().BoolVar(&good, "good", false, "Mark selected checkpoints good")
	cmd.Flags().BoolVar(&review, "review", false, "Mark selected checkpoints for review")
	cmd.Flags().BoolVar(&unmark, "unmark", false, "Remove explicit review, good, or bad marks from selected checkpoints")
	cmd.Flags().StringVar(&label, "label", "", "Arbitrary mark label")
	cmd.Flags().StringVar(&reason, "reason", "", "Human-readable reason for the mark")
	return cmd
}
