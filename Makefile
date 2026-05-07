# hypomnema — top-level orchestration for the Go side of the project.
# Bash hooks remain self-contained (see install.sh); this Makefile only
# covers Go build/test/install.

# NB: bin/ also holds committed shell scripts (consolidate.sh, memory-fts-*.sh,
# memory-dedup.py, memory-self-profile.sh, memory-strategy-score.sh). Never
# `rm -rf bin/` — only the Go binary is ours to clean here.
BIN_DIR := ./bin
MEMORYCTL := $(BIN_DIR)/memoryctl
GO_FILES := $(shell find . -name '*.go' -not -path './vendor/*')

# CGO_ENABLED=0 keeps the binary statically linked and trivially cross-
# compilable. modernc.org/sqlite is pure Go, so this works.
GO_BUILD := CGO_ENABLED=0 go build -trimpath -ldflags="-s -w"

.PHONY: all build test test-go test-go-cover test-hooks test-fixtures parity replay install clean help

all: build test

build: $(MEMORYCTL)

$(MEMORYCTL): $(GO_FILES) go.mod go.sum
	@mkdir -p $(BIN_DIR)
	$(GO_BUILD) -o $(MEMORYCTL) ./cmd/memoryctl
	@echo "Built $(MEMORYCTL) ($(shell du -h $(MEMORYCTL) | cut -f1))"

test: test-go test-hooks

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
	@MEMORYCTL_SUBPROCESS_COVER_OUT=$(PWD)/coverage/cmd-memoryctl-subprocess.out \
		go test ./cmd/memoryctl/... > /dev/null
	@echo "=== test-binary coverage (in-process) ==="
	@go tool cover -func=coverage/test.out | tail -1
	@echo "=== cmd/memoryctl subprocess coverage ==="
	@go tool cover -func=coverage/cmd-memoryctl-subprocess.out | tail -1
	@echo
	@echo "Profiles in ./coverage/. View HTML with:"
	@echo "  go tool cover -html=coverage/cmd-memoryctl-subprocess.out"

test-hooks:
	bash hooks/test-memory-hooks.sh

# Snapshot tests against synthetic corpora in fixtures/corpora/. Each
# fixture has an expected.json written SPEC-first; the harness diffs
# actual doctor / wal / corpus state against it.
test-fixtures: build
	@any_fail=0; \
	for d in fixtures/corpora/synthetic-*/; do \
		[ -d "$$d" ] || continue; \
		MEMORYCTL="$(abspath $(MEMORYCTL))" bash hooks/test-fixture-snapshot.sh "$$d" || any_fail=1; \
	done; \
	exit $$any_fail

# Parity check: same fixture, same prompt, bash vs Go shadow. Ensures the
# Go pilot stays drop-in compatible with the reference implementation.
parity: build
	@bash scripts/parity-check.sh

# Batch-replay a corpus of synthetic prompts through UserPromptSubmit and
# report aggregate retrieval metrics (trigger-match, shadow-miss). Use
# this to measure retrieval quality against a hand-crafted prompt spread
# instead of waiting for real sessions to accumulate.
replay: build
	@bash scripts/replay-runner.sh

install: build
	@mkdir -p $${HOME}/.claude/bin
	ln -sf "$(abspath $(MEMORYCTL))" $${HOME}/.claude/bin/memoryctl
	@echo "Linked $${HOME}/.claude/bin/memoryctl -> $(abspath $(MEMORYCTL))"
	@echo "Re-run ./install.sh to pick up memoryctl in settings.json (optional)."

clean:
	rm -f $(MEMORYCTL)

help:
	@echo "Targets:"
	@echo "  build       — compile bin/memoryctl (CGO_ENABLED=0, static)"
	@echo "  test        — go test + bash hooks/test-memory-hooks.sh"
	@echo "  test-go     — just the Go test suite"
	@echo "  test-go-cover — Go tests + cmd/memoryctl subprocess coverage report"
	@echo "  test-hooks  — just the bash hooks smoke test"
	@echo "  test-fixtures — snapshot tests against fixtures/corpora/synthetic-*"
	@echo "  parity      — bash vs Go shadow pass comparison"
	@echo "  replay      — synthetic corpus replay, retrieval metrics"
	@echo "  install     — symlink bin/memoryctl into ~/.claude/bin/"
	@echo "  clean       — remove bin/memoryctl"
