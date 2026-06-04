.PHONY: all build install test test-unit test-integration test-all

all: build

build:
	go build -o autosnap ./cmd/autosnap

install:
	go install ./cmd/autosnap

test-unit:
	go test ./...

test-integration:
	go test -tags=integration ./...

test: test-unit

test-all: 
	$(MAKE) test-unit
	$(MAKE) test-integration
