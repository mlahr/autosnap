.PHONY: help all build install test test-unit test-integration test-all clean

help:
	@printf '%s\n' 'Usage: make <target>'
	@printf '\n%s\n' 'Targets:'
	@printf '  %-18s %s\n' 'help' 'Show this help message.'
	@printf '  %-18s %s\n' 'all' 'Build the autosnap binary.'
	@printf '  %-18s %s\n' 'build' 'Build ./autosnap from ./cmd/autosnap.'
	@printf '  %-18s %s\n' 'install' 'Build, then install autosnap with go install.'
	@printf '  %-18s %s\n' 'test' 'Run the fast unit test suite.'
	@printf '  %-18s %s\n' 'test-unit' 'Run go test ./...'
	@printf '  %-18s %s\n' 'test-integration' 'Run integration tests with the integration build tag.'
	@printf '  %-18s %s\n' 'test-all' 'Run unit tests, then integration tests.'
	@printf '  %-18s %s\n' 'clean' 'Remove generated binaries, coverage files, test files, and temp directories.'

all: build

build:
	go build -o autosnap ./cmd/autosnap

install:
	$(MAKE) build
	go install ./cmd/autosnap

test-unit:
	go test ./...

test-integration:
	go test -tags=integration ./...

test: test-unit

test-all: 
	$(MAKE) test-unit
	$(MAKE) test-integration

clean:
	rm -rf autosnap *.exe coverage.out coverage.html *.test *.out bin tmp
