# Repository Guidelines

## Project Structure & Module Organization

`autosnap` is a Go CLI for local Git checkpointing. The executable entry point lives in `cmd/autosnap/main.go`; command implementations and supporting logic live in `internal/autosnap/`. Tests are currently colocated in `internal/autosnap/main_test.go`, with integration behavior gated by build tags. Project metadata and automation live in `go.mod`, `Makefile`, `.github/workflows/`, and `.goreleaser.yml`. Runtime user configuration examples use `.autosnap.toml`; do not commit local state from `.git/autosnap/`.

## Build, Test, and Development Commands

- `make build`: builds the CLI as `./autosnap` using `go build -o autosnap ./cmd/autosnap`.
- `make install`: installs the command with `go install ./cmd/autosnap`.
- `make test` or `make test-unit`: runs the fast test suite with `go test ./...`.
- `make test-integration`: runs integration tests with `go test -tags=integration ./...`.
- `make test-all`: runs both unit and integration tests; this is what CI executes.

Run commands from the repository root. Use `go test ./...`, not bare `go test`, because the main package is under `cmd/`.

## Coding Style & Naming Conventions

Use standard Go style: tabs via `gofmt`, concise package names, and exported identifiers only when needed outside a package. Keep command constructors named `new<Command>Command` and root wiring in `internal/autosnap/root.go`. Prefer small helpers with explicit errors over hidden global state. File names should be lowercase and descriptive, such as `snapshot.go`, `run_state.go`, or `config_command.go`.

Before submitting changes, run:

```bash
gofmt -w cmd internal
go test ./...
```

## Testing Guidelines

Use Go's standard `testing` package. Name tests `Test<Behavior>` and prefer table-driven tests for parsing, formatting, and command output cases. Integration tests must call `requireIntegration(t)` and run only with the `integration` build tag. Add focused tests for new command behavior, Git ref handling, config parsing, and daemon state transitions.

## Commit & Pull Request Guidelines

Recent history uses Conventional Commit-style messages, for example `feat(autosnap): add logs command for viewing daemon logs` and `ci(github): add CI workflow for Go tests`. Follow `type(scope): summary` with an imperative, specific summary.

Pull requests should include a short description, test results, and any user-visible command or output changes. Link related issues when applicable. Include screenshots only for terminal output changes where visual formatting matters.

## Security & Configuration Tips

Treat `--check` and `--msg-source-cmd` as shell commands supplied by the user. Avoid logging secrets from command output or environment variables. Keep generated binaries, logs, and local autosnap state out of commits.

## Agent coordination with `agentcom`

Run `agentcom instructions` for the current repository coordination contract.

Every agent must register its current task in `agentcom` before starting work,
using an existing task when one is available or creating one otherwise. 

Agents must register themselves with a specific stable descriptive name.
Generic names such as `codex` are not sufficient.
