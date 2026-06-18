.PHONY: all build install test test-unit test-integration test-all clean

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
