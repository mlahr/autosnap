package autosnap

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/spf13/cobra"
)

const defaultLogFollowInterval = 500 * time.Millisecond

func newLogsCommand() *cobra.Command {
	var (
		tailLines int
		follow    bool
	)

	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Show autosnap daemon logs",
		Args:  cobra.ExactArgs(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			repoRoot, _, _, err := detectRepository(context.Background())
			if err != nil {
				return err
			}

			logPath, err := backgroundLogPath(repoRoot)
			if err != nil {
				return err
			}

			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
			defer stop()

			offset, err := writeLogTail(cmd.OutOrStdout(), logPath, tailLines)
			if err != nil {
				return err
			}
			if !follow {
				return nil
			}

			return followLog(ctx, cmd.OutOrStdout(), logPath, offset, defaultLogFollowInterval)
		},
	}

	cmd.Flags().IntVarP(&tailLines, "tail", "n", -1, "Number of log lines to show from the end (default all)")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Follow log output")

	return cmd
}

func writeLogTail(out io.Writer, logPath string, tailLines int) (int64, error) {
	raw, err := os.ReadFile(logPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, fmt.Errorf("autosnap log not found at %s", logPath)
		}
		return 0, fmt.Errorf("read autosnap log: %w", err)
	}

	if tailLines != 0 {
		toWrite := raw
		if tailLines > 0 {
			lines := bytes.SplitAfter(raw, []byte("\n"))
			if len(lines) > 0 && len(lines[len(lines)-1]) == 0 {
				lines = lines[:len(lines)-1]
			}
			if len(lines) > tailLines {
				toWrite = bytes.Join(lines[len(lines)-tailLines:], nil)
			}
		}
		if len(toWrite) > 0 {
			if _, err := out.Write(toWrite); err != nil {
				return 0, err
			}
		}
	}

	return int64(len(raw)), nil
}

func followLog(ctx context.Context, out io.Writer, logPath string, offset int64, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			nextOffset, err := writeLogAppend(out, logPath, offset)
			if err != nil {
				return err
			}
			offset = nextOffset
		}
	}
}

func writeLogAppend(out io.Writer, logPath string, offset int64) (int64, error) {
	stat, err := os.Stat(logPath)
	if err != nil {
		return offset, fmt.Errorf("stat autosnap log: %w", err)
	}

	size := stat.Size()
	if size < offset {
		offset = 0
	}
	if size == offset {
		return offset, nil
	}

	file, err := os.Open(logPath)
	if err != nil {
		return offset, fmt.Errorf("open autosnap log: %w", err)
	}
	defer file.Close()

	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return offset, fmt.Errorf("seek autosnap log: %w", err)
	}
	n, err := io.Copy(out, file)
	if err != nil {
		return offset, fmt.Errorf("read autosnap log append: %w", err)
	}
	return offset + n, nil
}
