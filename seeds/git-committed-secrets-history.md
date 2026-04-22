---
type: mistake
seed: true
created: 2026-04-13
status: active
domains: [git, security]
keywords: [git, secrets, env, history, leak, gitignore, bfg]
severity: critical
recurrence: 0
root-cause: "Removing a file from the current commit does not remove it from history — a committed .env stays reachable via git log/show forever. If the repo is public, the secret is already compromised"
prevention: "1) Add .env to .gitignore BEFORE the first commit. 2) Use pre-commit hooks (gitleaks, trufflehog). 3) On leak — rotate the key immediately, then rewrite history with git-filter-repo"
decay_rate: never
ref_count: 0
triggers:
  - "git secret leak"
  - "committed env file"
  - "committed .env"
  - ".env from history"
  - "remove from history"
  - "remove from git history"
  - "bfg"
  - "git-filter-repo"
scope: domain
---

# Git: secrets committed to history

## Symptom
You spot `.env` in a commit. You delete the file and commit `git rm .env`. Looks solved — but:
```bash
git log --all --full-history -- .env
# shows old commits with the content
git show <old-commit>:.env
# prints the API key
```

## Why
Git stores **the full history**. Removing a file from the current state doesn't touch the blob objects in `.git/objects`. If the repo is pushed to GitHub/GitLab, the secret already lives:
- In history.
- In clones on other developers' machines.
- In CI caches.
- Possibly in search engines or scraper databases.

## Fix

**Step 1 — rotate the key IMMEDIATELY.** History cleanup does not undo the leak. Treat the key as compromised.

**Step 2 — add to .gitignore:**
```bash
echo ".env" >> .gitignore
echo ".env.local" >> .gitignore
git rm --cached .env
git commit -m "remove .env from tracking"
```

**Step 3 — rewrite history** (if the repo is private or the leak is fresh):
```bash
# git-filter-repo (preferred; do NOT use filter-branch)
brew install git-filter-repo
git filter-repo --invert-paths --path .env
git push --force origin main   # WARNING: breaks other clones
```

## Prevention
- **Pre-commit hook** with `gitleaks` or `trufflehog` — blocks the commit when a known secret pattern is detected.
- **GitHub Secret Scanning** — enable in repo settings; alerts on push when a known key format appears.
- **direnv + .envrc** — secrets stay in a local file, never tracked, loaded automatically on `cd`.

## Always require rotation
- AWS keys (`AKIA...`).
- GitHub tokens (`ghp_...`, `gho_...`).
- Stripe keys (`sk_live_...`).
- Database connection strings containing a password.
- JWT signing secrets.
