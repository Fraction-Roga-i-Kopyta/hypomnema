# v2.0 Phase 7 — Cutover + release Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development for the doc/code tasks. The two GATED steps (§C, §D) require explicit maintainer confirmation — do NOT execute them autonomously.

**Goal:** Flip the repo from the v1 bash system to v2 (native + Go), document the change, and release v2.0.0.

**Architecture:** Phase 7 has four parts. **A (docs)** and **B (ADRs)** are additive/safe — executed now. **C (install cutover + v1 removal)** flips install.sh to register the v2 shims and deletes ~9700 LOC of the v1 bash plumbing — it changes install behaviour and destroys existing code, so it is **maintainer-gated**. **D (release)** is the public, irreversible `git push` + `gh release` — **maintainer-gated**.

---

## A. Docs (SAFE — execute now)

### A1. README reframe
- The opening premise is now false: *"Claude Code starts every session from zero; hypomnema fixes that."* Claude Code now ships native memory. Reframe to: hypomnema is a **governance + ranking layer on top of Claude Code's native memory** — it adds ranked auto-injection, effectiveness measurement, decay, and a global store that native lacks.
- Update the one-liner ("markdown, YAML, **shell hooks**" → "native-memory + a Go governance engine"), the Status block (→ v2.0, native-primary), and the architecture section to describe native+sidecar+ranker+close+guard.
- Keep CI/license/release badges.

### A2. CLAUDE.md (project agent protocol) reframe
- Rewrite the "What hypomnema does" + "Memory layout" + "How injection ranks files" sections for v2: native files are the store; the sidecar (SQLite) holds metadata; `memoryctl` (Go) does inject/close/guard/migrate; the ranker is the single relevance pipeline; decay marks stale; global store at `memory-global`.
- Note the dropped features (TF-IDF, shadow-FTS, cold-start gates, two pipelines, substring-triggers) and the new ones (`memoryctl ab`, native-primary).
- Preserve the writing-protocol guidance (when to write mistake/strategy/feedback/etc.).

### A3. docs/MIGRATION.md — "v1.x → v2.0"
- The compatibility gate (CC v2.1.59+; if older → stay on v1.x tag).
- The guided upgrade: `memoryctl migrate --dry-run` (review the plan, esp. the global-fallback warning — add projects to `projects.json` first) → `--execute` (backs up to `memory.v1-backup-<date>`) → re-run `./install.sh` to register v2 shims → `--rollback` if needed.
- What changes: store moves to native dirs + `memory-global`; metadata to the sidecar; bash hooks → 4 thin shims.

### A4. CHANGELOG.md — v2.0.0 entry
- MAJOR: repositioned onto Claude Code native memory; Go-first engine + thin shims; merged injection pipeline; dropped TF-IDF/shadow/cold-start/two-pipelines/substring-triggers; added global store, `memoryctl ab` A/B harness, migration. Supersedes/flips 6 ADRs. Links the A/B measurement (`docs/measurements/2026-05-29-v2-ranker-ab.md`: ranking beats random 8–20×).

## B. ADRs (SAFE — execute now)

### B1. `docs/decisions/native-primary-sidecar.md` (new, status: active)
What/Why/Trade-off/When-to-revisit for: native memory is the content store; hypomnema is the governance+ranking layer; metadata in a rebuildable SQLite sidecar; global store fills native's per-project-only gap.

### B2. `docs/decisions/go-primary-bash-shims.md` (new, status: active) — flips `bash-go-gradual-port`
Go is now the primary implementation; bash is reduced to ~10-line `exec` shims. Records the reversal of `bash-go-gradual-port.md` and why (perf, JSON-safety, the awk/3.2 maintenance tax).

### B3. Supersede headers on flipped ADRs
Add a `superseded-by:` / status note to: `two-scoring-pipelines`, `substring-triggers-with-negation`, `fts5-shadow-retrieval`, `cold-start-scoring`, `scoring-weights` (revised), `bash-go-gradual-port` (flipped). One-line each, pointing at the v2 spec.

---

## C. Install cutover + v1 removal (⚠️ MAINTAINER-GATED — do NOT auto-execute)

### C1. install.sh / uninstall.sh
- Add the CC-version gate (`claude --version` ≥ 2.1.59; else refuse with the actionable message).
- Register the 4 v2 shims (`hooks/v2/*.sh`) via `register_hook` (SessionStart→session-start.sh, UserPromptSubmit→user-prompt-submit.sh, PreToolUse:Write→pre-tool-write.sh, Stop→session-stop.sh), REPLACING the v1 hook registrations.
- Symlink `memoryctl`; offer `memoryctl migrate --dry-run`.
- uninstall.sh removes the v2 shim registrations.

### C2. Remove v1 plumbing (~9700 LOC)
Delete the v1 bash system superseded by the Go engine: `hooks/*.sh` (session-start, user-prompt-submit, session-stop, memory-*, wal-*), `hooks/lib/`, the bin/*.sh shell utilities subsumed by memoryctl, `internal/tfidf` (scoring; tokenizer already salvaged), `internal/fts` (shadow), `internal/evidence`, `internal/decisions` (review-triggers feature), and the v1 bash test suite (`test-memory-hooks.sh`, `bench-memory.sh`, parity/bench/replay scripts). Update the `Makefile` (`test` = `go test -race ./...`; drop test-hooks/parity/bench-gate/replay/test-uninstalled). Update CI `.github/workflows/test.yml` to the Go-only gate.

**Why gated:** this destroys ~9700 lines of the maintainer's existing, working code. It is reversible on the branch (git), but the maintainer must review the exact deletion set first. v1.x remains available on its release tag for users on older Claude Code.

## D. Release (⚠️ MAINTAINER-GATED — outward/irreversible)

`/release v2.0.0` (or manual): full test gate → version bump → CHANGELOG finalize → annotated tag `v2.0.0` → `git push` → `gh release`. **The push + GH release are the only outward, irreversible actions in the entire v2 effort and require explicit maintainer go-ahead at execution time.** Merge the `v2-native-memory` branch to `main` as part of this (the maintainer chose "integrate at cutover").

---

## Phase 7 Definition of Done

- [ ] (A) README + CLAUDE.md describe v2 (native+sidecar+ranker), not v1; no "starts every session from zero".
- [ ] (A) MIGRATION.md covers the guided v1.x→v2.0 upgrade; CHANGELOG has the v2.0.0 entry.
- [ ] (B) New ADRs `native-primary-sidecar` + `go-primary-bash-shims`; 6 flipped ADRs carry supersede notes.
- [ ] (C, gated) install.sh registers v2 shims + gate; v1 plumbing removed; Makefile/CI Go-only. **After maintainer review.**
- [ ] (D, gated) v2.0.0 tagged + released; branch merged to main. **After maintainer go-ahead.**

## Self-Review
- A/B are additive (new/edited docs) — no behaviour or code-deletion risk; safe to execute now.
- C/D are the destructive + outward steps; explicitly gated per the maintainer-confirmation discipline (delete-existing-work + publish-public-release). v1 survives on its tag.
