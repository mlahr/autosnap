.PHONY: test build install

build:
	go build -o autosnap ./cmd/autosnap

install:
	go install ./cmd/autosnap

test:
	go test ./...
