export GOWORK := off
export CGO_ENABLED := 0
export GOFLAGS := -mod=vendor

VERSION := $(shell sed -n 's/^const Version = "\(.*\)"/\1/p' internal/buildinfo/version.go)
ARCHITECTURES := riscv64 arm64 amd64
GO ?= go

.PHONY: help build test check release package clean
.DEFAULT_GOAL := help

help:
	@echo 'make build    Build the installer for this machine'
	@echo 'make test     Run Go tests and vet (including bootstrap tests)'
	@echo 'make check    Check formatting, shell syntax, tests and vet'
	@echo 'make release  Build Linux riscv64, arm64 and amd64 binaries'
	@echo 'make package  Create dist/v$(VERSION)/ release assets'
	@echo 'make clean    Remove dist/'

build:
	$(GO) build -trimpath -ldflags='-s -w' -o dist/onekvm-installer ./cmd/onekvm-installer
test:
	$(GO) test -tags=onekvm_test ./...
	$(GO) vet -tags=onekvm_test ./...
check:
	@test -z "$$(gofmt -l cmd internal)" || { gofmt -l cmd internal; exit 1; }
	sh -n onekvm.sh
	sh -n scripts/package.sh
	$(MAKE) test
release:
	@set -e; for architecture in $(ARCHITECTURES); do \
		echo "Building Linux $$architecture"; \
		GOOS=linux GOARCH=$$architecture $(GO) build -trimpath -ldflags='-s -w' \
			-o dist/onekvm-installer-linux-$$architecture ./cmd/onekvm-installer; \
	done
package: release
	sh scripts/package.sh "$(VERSION)"
clean:
	rm -rf dist
