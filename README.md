# autosnap

`autosnap` is a local Git checkpointing CLI for preserving known-good worktree
states while you work. It is a safety net between intentional Git commits, not a
replacement for normal commits.

By default, autosnap stores passing checkpoints as local Git refs under
`refs/autosnapshots/`. It does not commit to your active branch, does not push,
and does not overwrite your work during normal daemon operation.

## Quick Start

Run autosnap from inside a Git worktree:

```bash
autosnap config init
```

Edit `.autosnap.toml` and set the check command for your project. For example:

```toml
check = "make test"
idle_seconds = 60
snapshot_mode = "both"
commit_mode = "checkpoint"
msg_source_cmd = ""
note_command = ""
note_ref = ""
```

Start the daemon:

```bash
autosnap start
autosnap status
```

After editing `.autosnap.toml`, restart the daemon:

```bash
autosnap restart
```

See [docs/quick-start.md](docs/quick-start.md) for the full first-run flow.

## Installation

On Debian-based Linux amd64 or arm64 systems, install the latest released
Debian package:

```bash
curl -fsSL https://raw.githubusercontent.com/mlahr/autosnap/main/install.sh | bash
```

The installer downloads the latest GitHub Release `.deb`, verifies it against
the release `checksums.txt`, and installs it with `apt-get`.

Install from source:

```bash
make install
```

## Documentation

Packaged Linux installs include manual pages and Markdown documentation:

```bash
man autosnap
autosnap docs
```

Repository documentation:

- [Quick start](docs/quick-start.md)
- [Mental model](docs/mental-model.md)
- [Safety model](docs/safety.md)
- [Workflows](docs/workflows.md)
- [Configuration](docs/configuration.md)
- [Troubleshooting](docs/troubleshooting.md)
- [Command reference](docs/commands/autosnap.md)

## Agent Skill

This repository includes an autosnap forensics agent skill at
[docs/skills/autosnap-forensics/SKILL.md](docs/skills/autosnap-forensics/SKILL.md).
The skill helps agents use existing autosnap checkpoints to find when a bug,
regression, unwanted behavior, or bad implementation decision was introduced
during a long-running coding session.

To install it for a local Codex-compatible skill loader:

```bash
mkdir -p ~/.codex/skills/autosnap-forensics
cp docs/skills/autosnap-forensics/SKILL.md ~/.codex/skills/autosnap-forensics/SKILL.md
```

## Build And Test

Requirements:

- Go 1.22+
- Git in `PATH`

Build:

```bash
make build
```

Install source-built documentation:

```bash
make docs
sudo make install-docs
```

By default, source docs install under `/usr/local/share/man/man1/` and
`/usr/local/share/doc/autosnap/`. Override `PREFIX`, `MANDIR`, `DOCDIR`, or
`DESTDIR` when packaging or installing somewhere else.

Run tests:

```bash
make test
make test-integration
make test-all
```

Regenerate documentation:

```bash
make docs
```

## Release

Releases are built by GitHub Actions with GoReleaser on version tags:

```bash
git tag v0.1.0
git push origin v0.1.0
```

The release workflow builds Linux tarballs and `.deb` packages for amd64 and
arm64.
