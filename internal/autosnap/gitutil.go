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

func gitCommandError(err error, result commandResult) error {
	if err == nil {
		return nil
	}

	detail := strings.TrimSpace(result.Stderr)
	if detail == "" {
		detail = strings.TrimSpace(result.Stdout)
	}
	if detail == "" {
		return err
	}

	return fmt.Errorf("%w: %s", err, detail)
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

func runGitCommandWithInput(ctx context.Context, dir string, env map[string]string, input string, args ...string) (commandResult, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Stdin = strings.NewReader(input)

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
	stdoutSink := io.MultiWriter(os.Stdout, &stdout)
	stderrSink := io.MultiWriter(os.Stderr, &stderr)
	if logTimestampsEnabled {
		stdoutSink = io.MultiWriter(withTimestampWriter(os.Stdout), &stdout)
		stderrSink = io.MultiWriter(withTimestampWriter(os.Stderr), &stderr)
	}

	cmd.Stdout = stdoutSink
	cmd.Stderr = stderrSink

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

func runShellOutput(ctx context.Context, dir string, command string) (string, int, error) {
	return runShellOutputEnv(ctx, dir, command, nil)
}

func runShellOutputEnv(ctx context.Context, dir string, command string, env map[string]string) (string, int, error) {
	return runShellOutputEnvLabel(ctx, dir, "msg-source-cmd", command, env)
}

func runShellOutputEnvLabel(ctx context.Context, dir string, label string, command string, env map[string]string) (string, int, error) {
	cmd := shellCommand(ctx, dir, command)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	var stdout, stderr bytes.Buffer
	stdoutSink := io.MultiWriter(os.Stdout, &stdout)
	stderrSink := io.MultiWriter(os.Stderr, &stderr)
	if logTimestampsEnabled {
		stdoutSink = io.MultiWriter(withTimestampWriter(os.Stdout), &stdout)
		stderrSink = io.MultiWriter(withTimestampWriter(os.Stderr), &stderr)
	}
	cmd.Stdout = stdoutSink
	cmd.Stderr = stderrSink

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

	logSourceCommandOutput(label, command, stdout.String(), stderr.String(), exitCode)

	if err != nil && stderr.Len() > 0 {
		if stderrText := strings.TrimSpace(stderr.String()); stderrText != "" {
			err = fmt.Errorf("%w (%s)", err, stderrText)
		}
	}

	return stdout.String(), exitCode, err
}

func logSourceCommandOutput(label, command, stdoutText, stderrText string, exitCode int) {
	logf("running %s: %q (exit=%d)\n", label, command, exitCode)

	logShellOutputLines(label, "stdout", stdoutText)
	logShellOutputLines(label, "stderr", stderrText)

	if strings.TrimSpace(stdoutText) == "" && strings.TrimSpace(stderrText) == "" {
		logf("%s produced no output\n", label)
	}
}

func logShellOutputLines(commandLabel, stream, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}

	lines := strings.Split(text, "\n")
	for _, line := range lines {
		logf("%s %s: %s\n", commandLabel, streamPrefix(stream), strings.TrimSpace(line))
	}
}

func streamPrefix(stream string) string {
	switch stream {
	case "stdout":
		return "[stdout]"
	case "stderr":
		return "[stderr]"
	default:
		return "[output]"
	}
}

var detectRepositoryStartDir = func() string { return "." }

const detachedHeadShortLength = 7

func detachedBranchIdentity(head string) (string, string) {
	shortHead := strings.TrimSpace(head)
	if shortHead == "" {
		return "detached", "detached"
	}
	if len(shortHead) > detachedHeadShortLength {
		shortHead = shortHead[:detachedHeadShortLength]
	}
	return "detached@" + shortHead, "detached-" + shortHead
}

func detectRepository(ctx context.Context) (string, string, string, error) {
	result, err := runGitCommand(ctx, detectRepositoryStartDir(), nil, "rev-parse", "--show-toplevel")
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
		head, headErr := runGitCommand(ctx, root, nil, "rev-parse", "HEAD")
		headSHA := strings.TrimSpace(head.Stdout)
		if headErr != nil || headSHA == "" {
			branchRef = "detached"
			branchDisplay = "detached"
		} else {
			branchDisplay, branchRef = detachedBranchIdentity(headSHA)
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

func gitIgnoredPaths(ctx context.Context, repoRoot string, relPaths []string) (map[string]bool, error) {
	ignored := make(map[string]bool)
	if len(relPaths) == 0 {
		return ignored, nil
	}

	input := strings.Join(relPaths, "\x00") + "\x00"
	result, err := runGitCommandWithInput(ctx, repoRoot, nil, input, "check-ignore", "--no-index", "--stdin", "-z")
	if err != nil {
		if result.ExitCode == 1 {
			return ignored, nil
		}
		return nil, err
	}

	for _, relPath := range strings.Split(result.Stdout, "\x00") {
		if relPath != "" {
			ignored[filepath.ToSlash(relPath)] = true
		}
	}
	return ignored, nil
}
