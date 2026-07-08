# hypomnema v2 — Go-only build/test orchestration.

BIN_DIR := ./bin
MEMORYCTL := $(BIN_DIR)/memoryctl
GO_FILES := $(shell find . -name '*.go' -not -path './vendor/*')

# CGO_ENABLED=0 keeps the binary statically linked and trivially cross-
# compilable. modernc.org/sqlite is pure Go, so this works.
GO_BUILD := CGO_ENABLED=0 go build -trimpath -ldflags="-s -w"

# Pinned so CI and local runs agree; bump deliberately.
# v0.7.0 == staticcheck 2026.1 (supports Go 1.25).
STATICCHECK_VERSION := v0.7.0

.PHONY: all build test test-go test-go-cover lint install clean help

all: build test

build: $(MEMORYCTL)

$(MEMORYCTL): $(GO_FILES) go.mod go.sum
	@mkdir -p $(BIN_DIR)
	$(GO_BUILD) -o $(MEMORYCTL) ./cmd/memoryctl
	@echo "Built $(MEMORYCTL) ($(shell du -h $(MEMORYCTL) | cut -f1))"

test: test-go

test-go:
	go test -race ./...

# Go test suite + meaningful subprocess coverage for cmd/memoryctl.
# Plain `go test -cover ./cmd/memoryctl/...` reports 0.0% because the
# tests drive the CLI through `exec.Command`; the parent test binary's
# own statements are nearly trivial. The subprocess writes binary
# coverage data into the per-package GOCOVERDIR (set via TestMain),
# and we ask `go tool covdata textfmt` to convert it to the standard
# textual profile that `go tool cover` understands.
test-go-cover:
	@mkdir -p coverage
	@rm -f coverage/cmd-memoryctl-subprocess.out coverage/test.out
	go test ./... -coverprofile=coverage/test.out
	@# -count=1 defeats the test cache: on a warm cache `go test` reports
	@# (cached) and TestMain never re-runs, so the subprocess coverage profile
	@# (written as a side effect via MEMORYCTL_SUBPROCESS_COVER_OUT) is missing
	@# and the report below silently reads a stale/absent file (review T1).
	@MEMORYCTL_SUBPROCESS_COVER_OUT=$(PWD)/coverage/cmd-memoryctl-subprocess.out \
		go test -count=1 ./cmd/memoryctl/... > /dev/null
	@echo "=== test-binary coverage (in-process) ==="
	@go tool cover -func=coverage/test.out | tail -1
	@echo "=== cmd/memoryctl subprocess coverage ==="
	@go tool cover -func=coverage/cmd-memoryctl-subprocess.out | tail -1
	@echo
	@echo "Profiles in ./coverage/. View HTML with:"
	@echo "  go tool cover -html=coverage/cmd-memoryctl-subprocess.out"

lint:
	go run honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION) ./...

install: build
	@mkdir -p $${HOME}/.claude/bin
	ln -sf "$(abspath $(MEMORYCTL))" $${HOME}/.claude/bin/memoryctl
	@echo "Linked $${HOME}/.claude/bin/memoryctl -> $(abspath $(MEMORYCTL))"
	@echo "Re-run ./install.sh to register v2 hooks in settings.json."

clean:
	rm -f $(MEMORYCTL)

help:
	@echo "Targets:"
	@echo "  build           — compile bin/memoryctl (CGO_ENABLED=0, static)"
	@echo "  test            — go test -race ./..."
	@echo "  test-go         — same as test"
	@echo "  test-go-cover   — Go tests + cmd/memoryctl subprocess coverage report"
	@echo "  lint            — staticcheck $(STATICCHECK_VERSION) across the repo"
	@echo "  install         — symlink bin/memoryctl into ~/.claude/bin/"
	@echo "  clean           — remove bin/memoryctl"
