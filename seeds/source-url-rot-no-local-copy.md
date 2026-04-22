---
type: mistake
seed: true
created: 2026-04-13
status: active
domains: [research, writing]
keywords: [source, url, link rot, archive, citation, research]
severity: major
recurrence: 0
root-cause: "A URL without a local copy is not a source — it's a promise of a source. Sites are redesigned, articles get deleted, domains die; within 1–3 years a meaningful share of links are 404"
prevention: "When you save a link, save a local copy at the same time (PDF / screenshot / markdown) OR archive via web.archive.org. Store the URL, the access date, and the snapshot together"
decay_rate: never
ref_count: 0
triggers:
  - "link rot"
  - "source unavailable"
scope: domain
---

# Link rot: URL without a local copy

## Symptom
A year later, you come back to your note / article / research. The key link leads to:
- 404 Not Found.
- Redirect to the site's homepage (article moved or deleted).
- Completely different content (domain sold).
- A paywall that wasn't there before.
- A Cloudflare geo-block.

## Why
- **Roughly half of links die within 5–7 years** (see academic research on link rot).
- News sites pull articles after about a year.
- Blogs migrate platforms and lose permalinks.
- Twitter/X removed access to old posts without login.
- Reddit lost part of its old threads after the 2023 API changes.

## Fix

**Minimum** — record the access date in the citation:
```markdown
[Article on X](https://example.com/article) (accessed: 2026-04-13)
```

**Baseline** — archive via Wayback Machine:
```bash
curl -s "https://web.archive.org/save/https://example.com/article"
# or: https://archive.ph/https://example.com/article
```
Store both the original and the archive URL in the note.

**Robust** — a local copy:
```bash
# Markdown via pandoc or readability
curl -s URL | pandoc -f html -t markdown -o saved.md

# Full page with assets
monolith URL -o saved.html

# PDF via headless Chrome
chromium --headless --disable-gpu --print-to-pdf=saved.pdf URL
```

**Systematic** — Zotero / Obsidian Web Clipper / Readwise: they pull a local copy automatically whenever you save a URL.

## Especially critical for
- Academic work and essays (reviewers verify sources).
- Legal or medical references.
- Design references (Pinterest boards die wholesale).
- Technical how-tos tied to a specific tool version.
