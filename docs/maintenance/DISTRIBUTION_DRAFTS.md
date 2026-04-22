# Distribution drafts — v0.10.0 launch

Draft messages for the three most plausible channels. Internal doc — not user-facing. Pick one or several; adapt tone.

---

## Why each channel

| Channel | Audience | Expected signal |
|---|---|---|
| **HN Show HN / Launch HN** | Engineers, tinkerers. High bar for novelty, rewards "something I didn't know existed". | Hacker feedback on architecture, maybe 3-10 people try it in first week. Binary lift → long tail on GitHub. |
| **Reddit r/ClaudeAI, r/LocalLLaMA** | Power users of Claude / local LLMs. Tolerant of rough edges, appreciate tool-chain transparency. | 5-20 trial installs expected. Feedback focuses on friction. |
| **Direct outreach** (Twitter/X, personal email, Claude Code Discord) | Known developers who already work with Claude Code agents daily. | Fewer trials (2-5), but deeper engagement — the variance we need most. |

**Recommendation:** start with direct outreach (C) to 2-3 people; a week later, Show HN (A) with their feedback already folded in. Reddit (B) works either way, best as follow-up with a concrete "I did this, it works, here's the repo".

---

## Draft A — Show HN

**Title:**
> Show HN: Hypomnema – file-based long-term memory for Claude Code agents

**Body (~300 words):**

> A few months ago I started noticing my Claude Code sessions kept re-discovering the same mistakes. A subtle sed idiom that breaks on Linux, a Python datetime pitfall, a specific API that silently deprecates — I'd explain it, we'd fix it, and the next session it was gone. So I wrote a hook-based memory system.
>
> It's just a directory under ~/.claude/memory/ with markdown files organised by type (mistakes / strategies / feedback / knowledge / decisions / notes) and an append-only WAL. Four hooks wire it into Claude Code:
>
> - SessionStart injects the top-ranked 12 records into every new session.
> - UserPromptSubmit fires reactively on substring triggers.
> - PreToolUse blocks duplicate mistake files via fuzzy dedup.
> - Stop records what the session learned.
>
> Ranking is composite: keyword-match × 3, plus log₁₀(ref_count), plus a Bayesian effectiveness term over trigger-useful/silent events, plus a spaced-repetition decay over the last 30 days.
>
> It's ~5k lines of bash (hooks + libs) + ~2k lines of Go (non-test). Go is opt-in (`make build`) for the heavy paths: FTS5 shadow retrieval, fuzzy dedup, self-profile aggregation, index sync. Without it, bash fallbacks cover everything except dedup (dedup is Go-only as of v0.10.0). Contract between implementations is pinned in docs/FORMAT.md and docs/hooks-contract.md, with byte-for-byte parity enforced by scripts/parity-check.sh.
>
> A handful of BSD vs GNU portability bugs (sed range semantics, grep escape handling, stat format flags, sqlite3 FTS5 module PATH contamination) got caught during a bash-vs-Go parity test in CI — a nice side effect of the hybrid approach.
>
> Happy to answer questions about the scoring formula, the WAL event taxonomy, or the contract v1.0 surface. Working on analytics-on-Go next.
>
> Repo: [link]

---

## Draft B — Reddit

### r/ClaudeAI post

**Title:**
> Hypomnema: file-based long-term memory for Claude Code (v0.10.0 just shipped)

**Body (~200 words):**

> If you use Claude Code daily you've probably noticed it re-discovers the same bugs across sessions. I wrote a memory system that fixes that — lives under `~/.claude/memory/` as plain markdown, no database, no cloud, no subscription.
>
> The simplest way to think about it: Claude has a little `mistakes/` folder. When you hit a bug and say "remember this", it writes a file. Next session, a hook injects the top 12 most-relevant files into the context. There are also strategies/, feedback/, knowledge/, decisions/ folders.
>
> Cooler bits:
> - **Substring triggers** — "sed issue" matches a mistake file with `triggers: ["sed portability"]`. Negations (`не/без/already/skip`) work.
> - **FTS5 shadow** — even if you never set triggers, a background BM25 pass notices "you asked about X, and there's a note about X that didn't surface — here it is, tune it".
> - **Spaced repetition** — files that get cited over multiple sessions rise; files that surface but never help decay.
> - **Fuzzy dedup** — before writing a new mistake, compare against existing ones via `token_set_ratio` (rapidfuzz semantics, pure-Go port); duplicate → merge.
>
> Install is `git clone && ./install.sh`. Fully reversible with `./uninstall.sh`.
>
> Would love feedback on what's missing. Repo: [link]

### r/LocalLLaMA variant

Same body, different framing lead:
> "For people running Claude Code as part of a longer-term pipeline: here's a memory layer that survives across sessions without external infra."

---

## Draft C — Direct outreach

### Short DM/email — 80 words, for someone you know uses Claude Code

> Just shipped v0.10.0 of hypomnema — the session-memory thing for Claude Code I've been working on. It's now proper: Go pilot for the heavy parts, formal contract document, matrix CI. Would be hugely helpful if you could clone it and use it on one project for a week — I want to see how retrieval metrics look outside my own prompt spectrum. No pressure, but if you do, `make replay` against your real memory dir gives me the summary stats I need. [link]

### Slightly longer — for someone who needs context

> You remember me complaining about Claude forgetting the BSD/GNU sed gotcha for the fifth time? I wrote a memory system that fixes it. It's a directory of markdown files + four Claude Code hooks. Tagged v0.10.0 yesterday — now has a Go binary for the heavy paths, a contract doc that pins the on-disk format, and CI matrix. What I still don't have is usage data from someone whose prompt-spectrum isn't mine. If you have an afternoon, clone it, install, use it on one project for a week, then `make replay` and send me the summary numbers. That's the thing that tells me whether the trigger-matching is doing real work or just looking busy on my corpus. [link]

---

## Post-launch monitoring

After any of the above lands:

- GitHub Stars: useful but weak signal; track daily for the first week.
- Clones: GitHub Insights → Traffic. Clones > stars typically = serious evaluation.
- Issues opened: by far the most valuable signal. Each one is someone who went through install + had enough investment to document a problem.
- PRs: even rarer, even better.
- `shadow-miss` reports from users: if someone does `make replay` on their real memory and sends numbers, that's the exact data we're missing. Ask for it explicitly in follow-ups.

Do NOT:
- Dunk on alternatives (`mem0`, etc.) in the body. Let the reader decide.
- Over-explain the architecture in the first 3 sentences. Lead with user problem, drop architecture at the end.
- Claim "universal" — the README already does that; opening messages should stay narrow on use cases.
