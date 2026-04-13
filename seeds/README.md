---
type: documentation
purpose: seed-examples
---

# Memory Seeds

Эта папка — **примеры формата**, не настоящие mistakes. Они показывают новому пользователю Hypomnema, как выглядит запись в памяти и какие поля имеют смысл заполнять.

## Что с ними делать

**Вариант A — изучить и забыть.** Прочитай 2-3 файла, чтобы понять структуру. Дальше пиши свои mistakes по аналогии — Claude сам предложит формат после первого "запиши это в память".

**Вариант B — скопировать в `mistakes/`.** Если ты сталкивался с подобным багом — перенеси файл в `~/.claude/memory/mistakes/`, убери `seed: true`, обнови `created` и `triggers` под свой случай.

**Вариант C — удалить папку.** Если seeds мешают (срабатывают триггеры, засоряют scoring) — `rm -rf ~/.claude/memory/seeds/`. Никаких зависимостей от них нет.

## Почему `seed: true` важен

Поле `seed: true` во frontmatter говорит scoring-системе **не считать эти файлы за реальный опыт**:
- Не учитываются в bootstrap-счётчике (5 mistakes для выхода из bootstrap-режима — только пользовательские)
- Деприоритезируются в SessionStart injection
- Триггеры всё равно срабатывают в UserPromptSubmit, но с пониженным весом

Это позволяет seed-ам быть полезной справкой без зашумления реальной памяти.

## Что внутри

| Файл | Домен | Универсальность |
|------|-------|-----------------|
| `shell-escape-special-chars.md` | shell | Все |
| `datetime-naive-timezone.md` | data, code | Аналитики, разработчики |
| `file-path-spaces-unquoted.md` | shell | Все |
| `encoding-utf8-vs-cp1251.md` | data | Данные, тексты |
| `git-committed-secrets-history.md` | git | Разработчики |
| `source-url-rot-no-local-copy.md` | research | Исследователи, писатели |
| `api-endpoint-silently-deprecated.md` | api | Разработчики |
