PREFIX ?= /usr/local
DESTDIR ?=
MANDIR ?= $(PREFIX)/share/man
DOCDIR ?= $(PREFIX)/share/doc/autosnap
INSTALL ?= install

.PHONY: help all fmt build install install-docs docs test test-unit test-integration test-docs test-all clean

help:
	@printf '%s\n' 'Usage: make <target>'
	@printf '%s\n' 'Variables: PREFIX=/usr/local DESTDIR= MANDIR=$$(PREFIX)/share/man DOCDIR=$$(PREFIX)/share/doc/autosnap'
	@printf '\n%s\n' 'Targets:'
	@printf '  %-18s %s\n' 'help' 'Show this help message.'
	@printf '  %-18s %s\n' 'all' 'Format, test, build, and generate docs.'
	@printf '  %-18s %s\n' 'fmt' 'Format Go source with gofmt.'
	@printf '  %-18s %s\n' 'build' 'Build ./autosnap from ./cmd/autosnap.'
	@printf '  %-18s %s\n' 'install' 'Build, then install autosnap with go install.'
	@printf '  %-18s %s\n' 'install-docs' 'Install man pages and Markdown docs from source.'
	@printf '  %-18s %s\n' 'docs' 'Regenerate command docs and Debian documentation payloads.'
	@printf '  %-18s %s\n' 'test' 'Run the fast unit test suite.'
	@printf '  %-18s %s\n' 'test-unit' 'Run go test ./...'
	@printf '  %-18s %s\n' 'test-integration' 'Run integration tests with the integration build tag.'
	@printf '  %-18s %s\n' 'test-docs' 'Verify generated docs are current.'
	@printf '  %-18s %s\n' 'test-all' 'Run unit tests, then integration tests.'
	@printf '  %-18s %s\n' 'clean' 'Remove generated binaries, coverage files, test files, and temp directories.'

all: fmt test-all build docs

fmt:
	gofmt -w cmd internal tools

build:
	go build -o autosnap ./cmd/autosnap

install:
	$(MAKE) build
	go install ./cmd/autosnap

docs:
	go run ./tools/gen-docs

install-docs: docs
	$(INSTALL) -d "$(DESTDIR)$(MANDIR)/man1"
	$(INSTALL) -m 0644 docs/man/autosnap.1 "$(DESTDIR)$(MANDIR)/man1/autosnap.1"
	$(INSTALL) -m 0644 docs/man/generated/*.1 "$(DESTDIR)$(MANDIR)/man1/"
	$(INSTALL) -d "$(DESTDIR)$(DOCDIR)"
	$(INSTALL) -m 0644 docs/*.md "$(DESTDIR)$(DOCDIR)/"

test-unit:
	go test ./...

test-integration:
	go test -tags=integration ./...

test: test-unit

test-docs:
	$(MAKE) docs
	git diff --exit-code -- docs/commands docs/man packaging/deb

test-all: 
	$(MAKE) test-unit
	$(MAKE) test-integration

clean:
	rm -rf autosnap *.exe coverage.out coverage.html *.test *.out bin tmp
