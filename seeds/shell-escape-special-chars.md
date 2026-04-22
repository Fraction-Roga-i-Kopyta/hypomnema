---
type: mistake
seed: true
created: 2026-04-13
status: active
domains: [shell]
keywords: [bash, shell, escape, dollar, exclamation, quote, password]
severity: minor
recurrence: 0
root-cause: "Special characters ($, !, backticks, $()) get interpreted inside double quotes before the string reaches the command"
prevention: "Use single quotes for strings containing special characters; for bcrypt hashes or passwords, use a heredoc with a quoted EOF delimiter to suppress interpolation"
decay_rate: never
ref_count: 0
triggers:
  - "shell escape"
  - "bash dollar sign"
scope: domain
---

# Shell escape: special characters inside quotes

## Symptom
The command fails with a cryptic error, or runs with a mangled string:
```bash
echo "P@ssw0rd$abc!"   # → P@ssw0rd!   ($abc is empty, ! can trigger history expansion)
```

## Why
Double quotes in bash **do** interpret:
- `$VAR` — variable expansion.
- `$(...)` and `` `...` `` — command substitution.
- `\n`, `\t`, etc. in some contexts.
- `!` — history expansion in interactive shells.

## Fix
Single quotes interpret nothing:
```bash
echo 'P@ssw0rd$abc!'   # → P@ssw0rd$abc!
```

For long strings or passwords, use a heredoc with a quoted delimiter:
```bash
cat <<'EOF' > /tmp/secret
P@ssw0rd$abc!`anything`
EOF
```

## When it really matters
- Bcrypt hashes (`$2b$12$...` — `$12` gets interpreted as a variable).
- `curl -u user:pass` credentials.
- JSON payloads with `$` (jq queries, MongoDB operators).
