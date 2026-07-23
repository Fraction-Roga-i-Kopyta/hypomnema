# Hypomnema

[![CI](https://github.com/Fraction-Roga-i-Kopyta/hypomnema/actions/workflows/test.yml/badge.svg?branch=main)](https://github.com/Fraction-Roga-i-Kopyta/hypomnema/actions/workflows/test.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Latest release](https://img.shields.io/github/v/release/Fraction-Roga-i-Kopyta/hypomnema?include_prereleases&sort=semver)](https://github.com/Fraction-Roga-i-Kopyta/hypomnema/releases)

**Governance and ranking layer for Claude Code's native file memory — a Go engine (`memoryctl`) + 6 thin shims. No cloud, no embeddings.** Claude Code now ships native file memory; hypomnema adds ranked auto-injection, effectiveness measurement, decay, and a global store that native lacks.

> **Status:** v2.6.0; native-primary. Requires Claude Code ≥ v2.1.59 (native file memory). v1.x stays on its tag for older Claude Code installs — see [MIGRATION.md](docs/MIGRATION.md) for the v1.x → v2.x upgrade path.
>
> **Platforms:** macOS (primary, daily-driver) and Linux (`ubuntu-latest`), both covered by CI. The core is the Go binary; the shims are ~5-line `sh` marshallers. Windows: WSL only, native unsupported.
>
> _Not affiliated with or endorsed by Anthropic. Hypomnema is an independent project that integrates with Claude Code's public hook API._

## What it looks like

When you open a new Claude Code session in a project, hypomnema injects a `# Memory Context` block before the conversation starts:

```markdown
# Memory Context

## sqlalchemy-metadata-reserved-name
Root cause: `metadata_` mapped to column "metadata" collides with `Base.metadata`
Prevention: Check SQLAlchemy reserved attribute names before mapping

## code-approach
Parameterized queries only. No hardcoded secrets. No debug logging in committed code.

## verification-before-done
Don't claim "done" before running a minimum verification set.
```

Claude reads it as part of its context — no RAG query, no vector database, just a markdown block prepended by the hook. When Claude hits a new bug worth remembering, it writes a native memory file and the next session injects it automatically.

## What hypomnema adds on top of native

Claude Code v2.1.59+ ships native file memory: `~/.claude/projects/<slug>/memory/` holds markdown files, and the harness injects only an auto-generated `MEMORY.md` index. That covers capture and raw listing — but not the parts that make memory actually useful over time.

Hypomnema adds:

| Gap in native | What hypomnema provides |
|---|---|
| Native injects only an index, not ranked content | `memoryctl inject` ranks native facts by relevance (keyword overlap + ref_count + recency + effectiveness) and injects the top-K via `additionalContext` — not just a table of contents |
| No per-session effectiveness signal | WAL captures `trigger-useful`/`trigger-silent` per session; effectiveness feeds back into ranking |
| No decay / lifecycle | `memoryctl close` down-ranks stale facts in the sidecar; nothing is deleted from disk |
| No secrets gate | `memoryctl guard` (PreToolUse:Write\|Edit) blocks credential patterns before they land in a memory file |
| No global store | Native memory is per-project only; hypomnema owns `~/.claude/memory-global/` for facts that travel across every project (language rules, universal debugging patterns) |
| No migration path | `memoryctl migrate` converts a v1.x hypomnema store to native format + sidecar in one shot |

An A/B replay on the maintainer's corpus suggested ranked injection recovers
several times more useful memories per budget slot than a random baseline.
Treat that number as indicative, not proven: the ground-truth labels
(`trigger-useful`) can only exist for facts the ranker itself injected, so the
comparison measures agreement with past injection policy more than retrieval
quality against an unbiased oracle, and the lift scales with the candidate-pool
size. See [`docs/measurements/2026-05-29-v2-ranker-ab.md`](docs/measurements/2026-05-29-v2-ranker-ab.md) for the method and caveats.

## Architecture at a glance

```
┌─ Claude Code harness ───────────────────────────────────────┐
│  native memory: ~/.claude/projects/<slug>/memory/*.md       │
│  • content (minimal frontmatter: name/description/type)     │
│  • MEMORY.md index — injected by harness                    │
└───────────────▲───────────────────────────▲─────────────────┘
       read-only │ (content)       write-intercept │ (guard)
┌────────────────┴───────────────────────────┴────────────────┐
│  hypomnema v2 — memoryctl (Go) + 6 thin sh shims            │
│                                                             │
│  inject ──► additionalContext (ranked top-K)                │
│  close  ──► classify → WAL → effectiveness/decay → profile  │
│  guard  ──► secrets gate (PreToolUse:Write|Edit)            │
│  skill-inject/skill-active ──► per-skill learnings          │
│  migrate──► one-shot v1 conversion + pruning                │
│                                                             │
│  WAL (append-only text) = source of truth for events        │
│  SQLite sidecar = derived projection (rebuildable)          │
│  ~/.claude/memory-global/ = global store (native format)    │
└─────────────────────────────────────────────────────────────┘
```

**Six hook events, six shims** — each ~5 lines, zero logic, pure marshalling:

| Event (matcher) | Shim | What memoryctl does |
|---|---|---|
| `SessionStart` | `session-start.sh` | Detect project + git context + keywords → rank native facts → inject top-K via `additionalContext` |
| `UserPromptSubmit` | `user-prompt-submit.sh` | Re-rank with prompt tokens added → inject anything not yet in session |
| `PreToolUse:Write\|Edit` | `pre-tool-write.sh` | `guard`: secrets scan on memory-path writes → exit 2 on a credential pattern |
| `PreToolUse:Skill` | `skill-active.sh` | Record which skill just activated (marker for the capture path) |
| `PostToolUse:Skill` | `skill-learnings-inject.sh` | `skill-inject`: surface accumulated learnings for the activated skill |
| `Stop` | `session-stop.sh` | `close`: classify the injected set (evidence/citation) → WAL → recompute effectiveness/decay → regenerate `MEMORY.md` + self-profile |

Fuzzy dedup for near-duplicate mistakes is available as the `memoryctl dedup`
verb but is not wired as a hook in v2. The project's native `MEMORY.md` index is
regenerated from native files on close; ranked facts arrive separately through
`additionalContext` — we don't fight the harness for the index channel.

## How injection ranks files

A single relevance ranker (merged from the two v1 pipelines) scores every candidate:

```
score = 3.0 × overlap(keywords, file keywords+name+description+body)
      + 1.0 × log10(1 + ref_count) × effGate   # popularity, GATED by proven usefulness
      + 2.0 × recency                  # 1/(1 + days/30) from last injection (fallback created)
      + 2.0 × effectiveness            # Bayesian (pos+1)/(pos+neg+2) — neutral 0.5 until signal lands
      + 1.0 × project boost            # project-local facts beat global ones on ties

effGate = clamp(2 × effectiveness, 0, 1)       # 1.0 at the prior 0.5; only damps unearned volume
```

Since v2.4.0 the `ref_count` reward is gated by `effGate`, so a fact injected
hundreds of times that rarely proved useful can't coast on volume — the gate is
neutral at the Bayesian prior and only *damps* unearned popularity, never
amplifies. The blend is additive and zero-safe: a brand-new fact with `ref_count=0` and no outcome history is still injectable — it scores on overlap, its frontmatter `created` recency and the neutral prior, not zero. Status filter: `active` and `pinned` only; `stale` is skipped (down-ranked in sidecar, still on disk). Scope filter: only the current project's facts plus the global store inject — other projects' rows never leak in. The result is a flat top-8, capped at 2.5KB per body and 8KB total (Claude Code diverts larger hook payloads to a file the model never sees inline), and each fact injects at most once per session.

Keyword overlap is the primary signal. Keywords come from git context (branch name, changed filenames, recent commit messages, CWD basename) plus prompt tokens, on both events. The old substring-trigger mechanism is gone; the ranker treats all tokens as relevance signal, not imperative triggers.

You do NOT need to set ranks manually. Write good frontmatter (`keywords`, `domains`), let `memoryctl inject` handle the rest.

Injection is push: the ranker sees the user's prompt, not what the agent
hits mid-task. `memoryctl recall` is the pull counterpart — an explicit query
against the same ranker. A recalled fact counts as delivered: it joins the
session's injected set (the close hook classifies it useful/silent), bumps
ref_count/recency, and — if it had gone stale — comes back to life.

## What gets remembered

| `type:` | What |
|------|------|
| **mistake** | Bugs, wrong approaches, repeated errors |
| **strategy** | Proven approaches that worked |
| **feedback** | Behavioral rules (why + how to apply) |
| **knowledge** | Domain facts, API docs, infrastructure |
| **decision** | Architecture & technology choices |
| **note** | Long-form knowledge, references |
| **project** | Project description, stack, known issues |
| **continuity** | "Where we left off" last session |
| **skill-learning** | Usage-learned knowledge bound to a skill (`skill:` field), injected when that skill activates |

The kind is the singular `type:` frontmatter value, not a subdirectory — the
stores are flat. All types compete in one ranked top-8 — there are no per-type
quotas; type balance emerges from relevance. `continuity` and `project` facts
are exempt from staleness rotation.

### Example: a mistake

```markdown
---
type: mistake
name: "api-auth-skipped"
description: "Built API client without reading auth docs first"
created: 2026-04-15
status: pinned
severity: major
recurrence: 1
keywords: [api, auth, planning, rewrite]
domains: [backend, integration]
root-cause: "Built the API client without checking auth flow first."
prevention: "Read auth section of API docs before writing the first request."
---

Read the auth/security section before the first request: required headers,
signing scheme, token lifecycle. The injection renders the **body**, so the
prose lives here — frontmatter is metadata and ranking signal.
```

This gets injected when relevant. After the fifth time, Claude stops doing it.

## How hypomnema compares

| | What it does | Hypomnema's position |
|---|---|---|
| **Claude Code native memory** (v2.1.59+) | Files in `~/.claude/projects/<slug>/memory/`, `MEMORY.md` index injected by harness | Hypomnema ranks those same files and injects the top-K via `additionalContext`; adds global store, effectiveness tracking, decay, secrets gate. Layered on top, not competing. |
| **`CLAUDE.md` files** (Anthropic native) | Static context injected verbatim, in-repo or `~/.claude/CLAUDE.md` | Hypomnema is dynamic: ranking, decay, per-session signal, WAL feedback loop. The two are complementary. |
| **`mem0`** (Python, hosted or self-host) | Vector-embedding memory with a REST API | No embeddings, no service to run — native files are the store. Trade-off: semantic recall beyond keyword overlap is weaker; no multi-language client. |
| **ChatGPT / Cursor memory** | Vendor-managed, opaque, cloud-stored | Local files you can `cat`/`grep`/`git diff`/`rm`. Trade-off: Claude Code specific. |

Sweet spot: daily-driver Claude Code users (v2.1.59+) who want visible, editable, diffable memory with effectiveness feedback. Not a fit for shared/team memory or users working across many AI tools.

## Install

**Requires Claude Code ≥ v2.1.59 (native file memory).**

```bash
git clone https://github.com/Fraction-Roga-i-Kopyta/hypomnema.git
cd hypomnema
make build       # compiles ./bin/memoryctl — the binary is not checked in
./install.sh     # refuses to run if bin/memoryctl is missing
memoryctl doctor # verify: all checks OK
```

`./install.sh`:
- Verifies Claude Code ≥ v2.1.59 and that `settings.json` is valid JSON — before touching anything.
- Symlinks the pre-built `memoryctl` into `~/.claude/bin/`.
- Copies the 6 hook shims into `~/.claude/hooks/v2/` and registers them in `~/.claude/settings.json` (timestamped backup first).
- Creates `~/.claude/memory-global/` for global facts.

**Upgrading from v1.x:** run `memoryctl migrate --dry-run` first — it shows what will be kept, pruned, and routed to global vs. project store. Then `memoryctl migrate --execute` (backs up the old store, does NOT delete it). See [docs/MIGRATION.md](docs/MIGRATION.md).

**Upgrading v2.x:** `cd /path/to/hypomnema && git pull && make build && ./install.sh`

### Requirements

- Claude Code ≥ v2.1.59 (native file memory)
- Go 1.25+ and `make` (to build `memoryctl` — mandatory, not optional)
- `jq` (install.sh edits settings.json)

### Uninstalling

```bash
./uninstall.sh             # remove shims, keep memory
./uninstall.sh --dry-run   # preview
```

Surgically strips hypomnema entries from `~/.claude/settings.json`. Memory files (native + global store) are kept by default.

## Setup

### Where facts live

Routing is by store location, not by frontmatter: files in
`~/.claude/projects/<slug>/memory/` inject only in that project; files in
`~/.claude/memory-global/` inject everywhere. The project slug derives from
`cwd` at runtime (every `/` becomes `-`) — no mapping file needed.
(`projects.json` is only consulted by `memoryctl migrate` when converting a
v1 store.)

### Add memory instructions to CLAUDE.md

```markdown
## Memory
- Bug found → write to native memory file (type: mistake)
- Successful strategy → write to native memory file (type: strategy)
- Before ending session → update the project's continuity file in its native store
```

### Restart Claude Code

The shims activate on next session start.

## Self-profile

`memoryctl close` regenerates `self-profile.md` on every close (Stop fires per turn) from WAL events. Five sections: meta-signals (total sessions, outcome-positive/negative counts, trigger-useful vs trigger-silent), intuition signal (silent-applied/trigger-useful ratio), strengths (top strategies by success_count), weaknesses (top mistakes by recurrence), and calibration (domains by error rate). Never edit manually — it's a pure function of WAL.

Run `memoryctl doctor` for a health snapshot: sidecar drift, WAL anomalies, stale facts due for down-rank, global store coverage.

## Pull retrieval

```
memoryctl recall <query words...> [--k N]
```

Pull-side retrieval: rank current project + global memory against an ad-hoc query; print the best fact's body (2.5KB cap) plus an index of runner-ups with file paths (default 6 results total). Writes a `recall` WAL event for the delivered fact and unions it into the session's injected list. Includes stale facts (marked `[stale]`) — recalling one revives it.

## Lifecycle

`memoryctl close` down-ranks stale facts in the sidecar (no mutation of native content). Staleness counts from the **last injection** (fallback `created`), so facts in active rotation never age out. Explicit archiving ships as `memoryctl retire <slug> [--reason ...] [--superseded-by <ref>]`: the file moves into the store's `.archive/`, and `recall` renders a tombstone redirect (`[retired <date> → successor]`) instead of silently forgetting; `memoryctl revive` undoes it. The full lifecycle is closed by three more verbs: agent-written facts start as `status: candidate` and graduate on their first useful citation; `memoryctl ablate` runs a per-fact holdout that measures whether behavior survives without the injection; `memoryctl promote` (report-only) suggests which facts have outgrown prose memory — a recurring mistake wants a hook/lint/test, an always-useful fact wants CLAUDE.md, an internalized one wants retirement.

| Type | Stale after |
|------|-------------|
| mistakes | 60 days |
| strategies | 90 days |
| knowledge | 90 days |
| decisions | 90 days |
| feedback | 45 days |
| notes | 30 days |
| projects | never |
| continuity | never |

`pinned` facts never decay automatically. Retirement is never age-triggered —
it is an explicit decision: `doctor` hints at stale facts worth retiring, and
`memoryctl promote` names the candidates with evidence.

## Frontmatter schema

Native files carry minimal frontmatter (name/description/type) — that's what the harness requires. Hypomnema-specific metadata (ref_count, effectiveness, status, keywords, domains) lives in the SQLite sidecar, keyed by file slug. The sidecar is a derived projection: delete it and `memoryctl` rebuilds from WAL + native frontmatter scan.

Fields you write in the native file:

```yaml
---
type: mistake            # mistake | strategy | feedback | knowledge | decision | project | continuity | note
name: "short-slug"       # title of the injected block; citation signal for close (the sidecar key is the filename)
description: "one-liner" # shown in the MEMORY.md index
created: YYYY-MM-DD      # recency claim for ranking (wins over WAL history)
status: active           # active | pinned  (stale is sidecar-managed, not hand-set)
keywords: [tag1, tag2]   # explicit relevance signal — feeds keyword overlap alongside name/description/body
domains: [domain1]       # relevance signal today; per-session domain filtering is planned

# Mistakes only
severity: minor | major | critical
recurrence: 0
scope: universal | domain | narrow
root-cause: "..."
prevention: "..."

# Strategies only
success_count: 0

# Feedback / any type
evidence:
  - "phrase the assistant would write when applying this rule"
precision_class: ambient  # excludes from precision denominator (use for language rules, meta-policy)
---
```

`ref_count` and `effectiveness` are sidecar-managed — do not set them by hand. `memoryctl inject` reads them from the sidecar; `memoryctl close` updates them from WAL.

## Subagents

Claude Code subagents don't receive SessionStart injection, and there is no auto-generated context file (`_agent_context.md` was a v1 artefact). Pass the facts a subagent needs inline in its prompt — the orchestrating session has them from its own `# Memory Context` block.

## Design decisions

**Why native files as the store, not a custom directory?** Claude Code's harness already injects `MEMORY.md` for the model — the file format is stable, the harness reads it for free. Owning a separate store meant a schema fork and two systems of record. Native is the substrate; sidecar is the metadata layer.

**Why a Go binary, not bash?** The hot path (inject) needs to parse hundreds of files, run ranking, and return before the hook timeout. Go eliminates the bash 3.2/awk/sed portability tax, is testable with table-driven unit tests, and is benchmarkable. The six shims that remain in bash are ~5 lines each — marshalling only.

**Why a sidecar, not frontmatter?** Native frontmatter is the content contract (harness reads it). Embedding hypomnema metadata there would pollute the harness format and require careful merge logic on every `git pull`. SQLite sidecar is a derivative — the WAL is the truth, the sidecar is a fast query projection that can be rebuilt anytime.

**Why keyword overlap, not embeddings?** At hundreds of files and tens of KB, keyword matching covers the primary injection path. A replay against a random baseline suggests the ranker earns its keep without semantic similarity (with the measurement caveats noted above). No network calls, no hidden costs, no ML dependencies.

**Why a global store?** Native memory is per-project only. Rules like "always respond in Russian" or "list 2-3 root causes before the first fix" apply everywhere. `~/.claude/memory-global/` is hypomnema-owned, injected alongside project-local facts, with the same ranking pipeline.

**Why spaced repetition (ref_count + effectiveness)?** Raw frequency is noisy (a record injected 50 times during a benchmark run isn't 50× more useful). Effectiveness (Bayesian `(pos+1)/(pos+neg+2)`) rewards records that actually help and down-ranks those that don't. The neutral prior means a new fact starts injectable, not suppressed.

**Why not multiple pipelines?** v1 had two: a composite-score SessionStart pipeline and a substring-trigger UserPromptSubmit pipeline. They solved similar problems with different mechanics, creating maintenance overhead and edge-case conflicts. v2 merges them: one ranker, all tokens (git context + prompt) as relevance signal. The old negation-window mechanism disappears.

## FAQ

**Does it work with Cursor / Aider / other tools?**
No. Hypomnema depends on Claude Code's `additionalContext` injection channel and hook events. Other tools don't expose the same API.

**What happens to my v1.x memory?**
`memoryctl migrate` converts it: v1 subdirectories → native files, WAL seeds ref_count/effectiveness, global facts route to `~/.claude/memory-global/`, low-signal files pruned. The old store is backed up, not deleted. Run `--dry-run` first to see the plan.

**Can I run on CC older than v2.1.59?**
No — v2.x requires native memory as the substrate. v1.x stays on its tag and works as before on older CC; there are no backports.

**Can I version-control my memory?**
Yes — native memory files are plain markdown in `~/.claude/projects/<slug>/memory/` and `~/.claude/memory-global/`. `git init` there and commit `*.md`. Runtime state (the WAL and the `.sidecar.db` projection) lives outside the stores, in `~/.claude/memory/`, so there is nothing to gitignore — and the sidecar is rebuildable anyway.

**How much overhead does the hook add?**
`inject` runs well under 1s warm on a ~200-file corpus. Shims are ~10 lines of bash — they're not the bottleneck. `close` is the heavier operation (classification + sidecar reproject + index/profile regen) and runs on every Stop, off the prompt hot path.

**What happens if the sidecar is missing or corrupted?**
`memoryctl` rebuilds it from WAL + native frontmatter scan. During rebuild, the ranker runs in degraded mode (overlap over native files only; ref_count/recency/effectiveness neutral). Injection still happens — degraded, not absent.

## Contributing

Issues are open — bug reports, reproduction recipes, and design questions welcome. Include `memoryctl doctor` output and relevant WAL lines when filing.

Pull requests are **gated by invitation** while the format contract hardens. See [CONTRIBUTING.md](CONTRIBUTING.md) for what can land without prior discussion (docs, seed additions) vs. what needs a paired issue (anything touching `internal/`, hook layer, or format contract).

Report security vulnerabilities privately via GitHub Security Advisories — see [SECURITY.md](SECURITY.md).

Release history: [CHANGELOG.md](CHANGELOG.md).

## License

MIT. See [LICENSE](LICENSE).

---

> ὑπόμνημα — ancient Greek personal notebooks for self-reflection.
> Marcus Aurelius, Seneca, and Epictetus kept them.
> Not memory itself, but the practice that sustains it.
