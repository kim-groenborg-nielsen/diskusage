# Makefile for common tasks

.PHONY: test build goreleaser-snapshot goreleaser-release

test:
	go test ./...

build:
	go build -v -o diskusage ./...

# Create a local snapshot artifacts (no publish)
goreleaser-snapshot:
	goreleaser --snapshot --skip-publish --rm-dist

# Run goreleaser release locally (requires GITHUB_TOKEN and proper git tag)
goreleaser-release:
	goreleaser release --rm-dist

