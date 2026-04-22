---
type: mistake
seed: true
created: 2026-04-13
status: active
domains: [shell]
keywords: [path, spaces, quote, macos, file, directory]
severity: minor
recurrence: 0
root-cause: "The shell word-splits on whitespace, so the path 'My Documents/file.txt' without quotes becomes two arguments: 'My' and 'Documents/file.txt'"
prevention: "Always wrap paths in double quotes, especially in scripts and copy/move commands"
decay_rate: never
ref_count: 0
triggers:
  - "path spaces"
  - "unquoted path"
  - "path with spaces"
  - "quote a path"
  - "quote path"
  - "word splitting"
  - "spaces in path"
scope: domain
---

# Paths with spaces, unquoted

## Symptom
```bash
cp /Users/me/My Documents/file.txt /tmp/
# cp: cannot stat '/Users/me/My': No such file or directory
# cp: cannot stat 'Documents/file.txt': No such file or directory
```

Common on macOS, where `~/Documents`, `~/Desktop`, `~/Library/Application Support` all contain spaces.

## Why
The shell word-splits the command line **before** the command sees it. `cp` receives three arguments instead of two: `My`, `Documents/file.txt`, `/tmp/`.

## Fix
Always quote:
```bash
cp "/Users/me/My Documents/file.txt" /tmp/
```

In variables:
```bash
SRC="$HOME/My Documents/file.txt"
cp "$SRC" /tmp/    # Quote at declaration AND at use
```

In scripts with `find`:
```bash
find "$DIR" -name "*.txt" -exec cp "{}" /tmp/ \;
```

## Gotchas
- Tab-completion in zsh/bash escapes spaces (`\ `) automatically, but the escape is lost on copy-paste.
- `$@` vs `"$@"` in scripts: the first word-splits arguments, the second preserves them.
- `IFS` (Internal Field Separator) can be reconfigured, but that's rarely the right solution.
