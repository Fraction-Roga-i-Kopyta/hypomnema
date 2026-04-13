---
type: mistake
seed: true
created: 2026-04-13
status: active
domains: [git, security]
keywords: [git, secrets, env, history, leak, gitignore, bfg]
severity: critical
recurrence: 0
root-cause: "Удаление файла из current commit не убирает его из истории — закоммиченный .env остаётся доступным через git log/show навсегда; если репо публичный, секрет уже скомпрометирован"
prevention: "1) Добавлять .env в .gitignore ДО первого коммита. 2) Использовать pre-commit хуки (gitleaks, trufflehog). 3) При утечке — немедленно ротировать ключ, потом чистить историю через git-filter-repo"
decay_rate: never
ref_count: 0
triggers:
  - "git secret leak"
  - "committed env file"
scope: domain
---

# Git: закоммиченные секреты в истории

## Симптом
Заметил `.env` в коммите. Удалил файл, закоммитил `git rm .env`. Кажется, что проблема решена — но:
```bash
git log --all --full-history -- .env
# показывает старые коммиты с содержимым
git show <old-commit>:.env
# выводит API ключ
```

## Почему
Git хранит **полную историю**. Удаление файла из current state не трогает blob-объекты в `.git/objects`. Если репо запушен на GitHub/GitLab — секрет уже:
- В истории
- В клонах других разработчиков
- В кешах CI
- Возможно проиндексирован GitHub-search или scrape-ботами

## Fix

**Шаг 1 — НЕМЕДЛЕННО ротировать ключ.** Чистка истории не отменяет утечку. Считай скомпрометированным.

**Шаг 2 — добавить в .gitignore:**
```bash
echo ".env" >> .gitignore
echo ".env.local" >> .gitignore
git rm --cached .env
git commit -m "remove .env from tracking"
```

**Шаг 3 — почистить историю** (если репо приватный или утечка свежая):
```bash
# git-filter-repo (рекомендуется, не filter-branch)
brew install git-filter-repo
git filter-repo --invert-paths --path .env
git push --force origin main   # ВНИМАНИЕ: ломает чужие клоны
```

## Профилактика
- **pre-commit хук** с `gitleaks` или `trufflehog` — блокирует коммит при детекте секретов
- **GitHub Secret Scanning** — включить в settings репозитория, шлёт алерт при пуше известных форматов ключей
- **direnv + .envrc** — секреты в локальном файле, не трекается, автоматически загружается при cd

## Что ВСЕГДА требует ротации
- AWS keys (`AKIA...`)
- GitHub tokens (`ghp_...`, `gho_...`)
- Stripe keys (`sk_live_...`)
- Database connection strings с паролем
- JWT signing secrets
