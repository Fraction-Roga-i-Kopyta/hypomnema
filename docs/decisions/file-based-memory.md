---
type: decision
project: global
created: 2026-04-10
status: active
description: On-disk markdown + YAML frontmatter is the single source of truth for memory content; no database, no embeddings, no cloud.
keywords: [storage, format, markdown, yaml, offline]
domains: [architecture]
---

## What

Memory records are plain markdown files with YAML frontmatter under
`~/.claude/memory/{mistakes,feedback,knowledge,strategies,decisions,
notes,projects,continuity}/*.md`. No database stores the primary
content; no embedding model is invoked; no network call leaves the
machine. Derivative indices (`.wal`, `index.db`, `.tfidf-index`,
`self-profile.md`) are regenerable projections over these files.

## Why

Three forcing functions, all upstream of any performance concern:

1. **Transparency for the author.** The user needs to `cat`, `grep`,
   `git diff`, `rm` any memory file without tooling. An opaque store
   would let the system become an agent the user doesn't trust.
2. **Portability.** Memory is useful only if it survives machine
   moves and tool migrations. `git push` to a private remote is the
   entire sync story; any alternative reintroduces migration work.
3. **LLM write-path ergonomics.** Claude can author a markdown file
   through its existing Write tool without new infrastructure. A
   database or vector store would need a separate authoring path.

## Trade-off accepted

- No transactions. Every scan is an O(N) `find + awk` walk.
- No semantic recall — only keyword, substring, and FTS5 trigram.
  Queries that would need "find me files that mean X" are not
  served.
- No schema enforcement at write time. A malformed frontmatter lands
  on disk; the read-side emits `schema-error` and skips the file.

## When to revisit

Corpus size grows past ~10 000 files and SessionStart exceeds the
15-second hook timeout under warm cache on the author's machine. At
that point the O(N) scan becomes the bottleneck and a structured
index (SQLite row store over frontmatter, not just FTS5) becomes the
smallest intervention.

Alternatively: a native Claude Code memory API ships that covers the
same transparency + portability surface. At that point hypomnema is a
compatibility shim, not the primary store.
