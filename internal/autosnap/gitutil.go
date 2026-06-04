package autosnap

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type commandResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

func runGitCommand(ctx context.Context, dir string, env map[string]string, args ...string) (commandResult, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if e, ok := err.(*exec.ExitError); ok {
		exitCode = e.ExitCode()
	}

	return commandResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
	}, err
}

func shellCommand(ctx context.Context, dir string, command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.CommandContext(ctx, "cmd", "/C", command)
	}
	return exec.CommandContext(ctx, "sh", "-c", command)
}

func runShellCheck(ctx context.Context, dir string, command string) (time.Duration, int, error) {
	start := time.Now()
	cmd := shellCommand(ctx, dir, command)
	cmd.Dir = dir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = io.MultiWriter(os.Stdout, &stdout)
	cmd.Stderr = io.MultiWriter(os.Stderr, &stderr)

	err := cmd.Run()
	exitCode := 0
	if e, ok := err.(*exec.ExitError); ok {
		exitCode = e.ExitCode()
	} else if err != nil {
		exitCode = 1
	}

	if ctx.Err() != nil {
		exitCode = 1
	}

	_ = stdout.String()
	_ = stderr.String()

	return time.Since(start), exitCode, err
}

func detectRepository(ctx context.Context) (string, string, string, error) {
	result, err := runGitCommand(ctx, ".", nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", "", "", fmt.Errorf("not in a git repository")
	}
	root := strings.TrimSpace(result.Stdout)
	if root == "" {
		return "", "", "", fmt.Errorf("not in a git repository")
	}

	branchResult, err := runGitCommand(ctx, root, nil, "branch", "--show-current")
	if err != nil {
		return "", "", "", err
	}
	branchDisplay := strings.TrimSpace(branchResult.Stdout)

	branchRef := branchDisplay
	if branchRef == "" {
		head, headErr := runGitCommand(ctx, root, nil, "rev-parse", "--short", "HEAD")
		headSHA := strings.TrimSpace(head.Stdout)
		if headErr != nil || headSHA == "" {
			branchRef = "detached"
			branchDisplay = "detached"
		} else {
			branchRef = "detached-" + headSHA
			branchDisplay = "detached@" + headSHA
		}
	}

	return root, branchDisplay, branchRef, nil
}

func gitDir(ctx context.Context, repoRoot string) (string, error) {
	result, err := runGitCommand(ctx, repoRoot, nil, "rev-parse", "--git-dir")
	if err != nil {
		return "", err
	}
	gitDirPath := filepath.Clean(strings.TrimSpace(result.Stdout))
	if !filepath.IsAbs(gitDirPath) {
		gitDirPath = filepath.Join(repoRoot, gitDirPath)
	}

	return filepath.Clean(gitDirPath), nil
}

func stateFilePath(repoRoot string) (string, error) {
	gitDirectory, err := gitDir(context.Background(), repoRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(gitDirectory, "autosnap", "state.json"), nil
}

func isGitIgnored(ctx context.Context, repoRoot string, relPath string) (bool, error) {
	result, err := runGitCommand(ctx, repoRoot, nil, "check-ignore", "-q", "--", relPath)
	if err != nil {
		if result.ExitCode == 1 {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
