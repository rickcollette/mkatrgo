# atr builds ... local, cross-platform, checks, etc.

SHELL := /bin/sh

APP        := atr
MODULE     := github.com/megalith/mkatrgo
COMMAND    := ./cmd/atr
DIST_DIR   := dist
GO         ?= go
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || printf 'dev')
BUILD_FLAGS := -trimpath
LD_FLAGS    := -s -w -X $(MODULE)/internal/cli.Version=$(VERSION)

GO_SOURCES := $(shell find . -type f -name '*.go' -not -path './$(DIST_DIR)/*')
GO_FILES   := $(GO_SOURCES) go.mod go.sum Makefile

LINUX_AMD64  := $(DIST_DIR)/$(APP)-linux-amd64
WINDOWS_AMD64 := $(DIST_DIR)/$(APP)-windows-amd64.exe
DARWIN_AMD64 := $(DIST_DIR)/$(APP)-darwin-amd64
DARWIN_ARM64 := $(DIST_DIR)/$(APP)-darwin-arm64
ARTIFACTS    := $(LINUX_AMD64) $(WINDOWS_AMD64) $(DARWIN_AMD64) $(DARWIN_ARM64)

.DEFAULT_GOAL := all

.PHONY: all build cross native linux linux-intel windows windows-intel \
	mac-intel mac-arm64 darwin-amd64 darwin-arm64 test race vet fmt fmt-check \
	check install clean version help FORCE

all: build

# all four release builds (intel here means amd64/x86-64).
build cross: $(ARTIFACTS)

linux linux-intel: $(LINUX_AMD64)
windows windows-intel: $(WINDOWS_AMD64)
mac-intel darwin-amd64: $(DARWIN_AMD64)
mac-arm64 darwin-arm64: $(DARWIN_ARM64)

$(DIST_DIR):
	mkdir -p $@

$(LINUX_AMD64): $(GO_FILES) FORCE | $(DIST_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build $(BUILD_FLAGS) -ldflags "$(LD_FLAGS)" -o $@ $(COMMAND)

$(WINDOWS_AMD64): $(GO_FILES) FORCE | $(DIST_DIR)
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GO) build $(BUILD_FLAGS) -ldflags "$(LD_FLAGS)" -o $@ $(COMMAND)

$(DARWIN_AMD64): $(GO_FILES) FORCE | $(DIST_DIR)
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 $(GO) build $(BUILD_FLAGS) -ldflags "$(LD_FLAGS)" -o $@ $(COMMAND)

$(DARWIN_ARM64): $(GO_FILES) FORCE | $(DIST_DIR)
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 $(GO) build $(BUILD_FLAGS) -ldflags "$(LD_FLAGS)" -o $@ $(COMMAND)

# just this machine ... writes ./atr.
native: $(GO_FILES)
	$(GO) build $(BUILD_FLAGS) -ldflags "$(LD_FLAGS)" -o $(APP) $(COMMAND)

test:
	$(GO) test ./...

race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

fmt:
	gofmt -w $(GO_SOURCES)

fmt-check:
	@files="$$(gofmt -l $(GO_SOURCES))"; \
	if [ -n "$$files" ]; then \
		printf 'these need gofmt ...\n%s\n' "$$files"; \
		exit 1; \
	fi

check: fmt-check vet test

install:
	$(GO) install $(BUILD_FLAGS) -ldflags "$(LD_FLAGS)" $(COMMAND)

version:
	@printf '%s\n' "$(VERSION)"

clean:
	$(GO) clean ./...
	$(RM) -r -- $(DIST_DIR)
	$(RM) -- $(APP) $(APP).exe coverage.out coverage.html trace.out \
		cpu.prof mem.prof block.prof mutex.prof
	find . -path './.git' -prune -o -type f \( \
		-name '*.test' -o -name '*.test.exe' -o -name '*.pprof' -o \
		-name '*.coverprofile' \
	\) -exec $(RM) -- {} +

help:
	@printf '%s\n' \
		'make build        all four release binaries (in dist/)' \
		'make linux        linux ... intel/amd64' \
		'make windows      windows ... intel/amd64' \
		'make mac-intel    macos ... intel/amd64' \
		'make mac-arm64    macos ... apple silicon' \
		'make native       ./atr for this machine' \
		'make check        format check + vet + tests' \
		'make race         tests (with the race detector)' \
		'make install      go install' \
		'make clean        remove all project-local build/test artifacts'

# always rebuild these ... otherwise VERSION=whatever can reuse an older binary.
FORCE:
