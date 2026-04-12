# v0.5 Implementation Plan — Context Triggers

**Spec:** `docs/specs/2026-04-12-v05-context-triggers-design.md`
**TDD:** Red → Green → Refactor

---

## Task 1: Тесты для user-prompt-submit.sh

Добавить в `hooks/test-memory-hooks.sh` секцию `--- Prompt triggers ---`.

**Тесты:**
1. `match single trigger` — prompt содержит `"tailwind hsl"` → слаг в output
2. `case insensitive` — `"Tailwind HSL"` матчит `"tailwind hsl"`
3. `backcompat single trigger:` — `trigger: "foo"` тоже работает
4. `no match → empty output` — prompt без совпадений → exit 0, без additionalContext
5. `dedup via /tmp list` — слаг в dedup-file → НЕ в output
6. `cap MAX_TRIGGERED` — 5 совпадений → в output только 4
7. `pinned priority` — pinned выше active при tiebreak
8. `WAL trigger-match logged` — событие в WAL
9. `multi-phrase match` — `triggers: ["a", "b"]`, prompt содержит `b` → match

Запустить тесты, убедиться что FAIL (хука нет).

---

## Task 2: Реализация hooks/user-prompt-submit.sh

Скелет:

```bash
#!/bin/bash
# UserPromptSubmit hook — inject memory files matching prompt triggers (v0.5)
set -o pipefail

MEMORY_DIR="${CLAUDE_MEMORY_DIR:-$HOME/.claude/memory}"
MAX_TRIGGERED=4
TODAY=$(date +%Y-%m-%d)

INPUT=$(cat)
SESSION_ID=$(printf '%s' "$INPUT" | jq -r '.session_id // empty')
PROMPT=$(printf '%s' "$INPUT" | jq -r '.prompt // empty')

[ -z "$SESSION_ID" ] && exit 0
[ -z "$PROMPT" ] && exit 0
[ -d "$MEMORY_DIR" ] || exit 0

LOWER_PROMPT=$(printf '%s' "$PROMPT" | tr '[:upper:]' '[:lower:]')
DEDUP_FILE="/tmp/.claude-injected-${SESSION_ID//\//_}.list"
WAL_FILE="$MEMORY_DIR/.wal"

# Collect candidates
# ... scan memory dirs, extract triggers, match, sort, cap
# Output markdown via jq
```

**Ключевые функции:**
- `parse_triggers FILE` — объединить `trigger:` + `triggers:` в newline-separated list
- `match_prompt PHRASE` — case-insensitive substring check в `$LOWER_PROMPT`
- `sort_candidates` — по полям frontmatter
- `emit_markdown` — построить `## Triggered` секцию, завернуть в hookSpecificOutput JSON

**WAL append:**
```
printf '%s|trigger-match|%s|%s\n' "$TODAY" "$slug" "$SESSION_ID" >> "$WAL_FILE"
```

**Dedup list append:**
Дописать активированные слаги в `$DEDUP_FILE` (для повторных prompt'ов в сессии).

Запустить тесты → должны PASS.

---

## Task 3: Patch session-start.sh — dedup-list

Добавить после основной инъекции (перед `exit 0`):

```bash
# v0.5: write injected slugs for user-prompt-submit dedup
DEDUP_FILE="/tmp/.claude-injected-${SESSION_ID//\//_}.list"
: > "$DEDUP_FILE" 2>/dev/null || true
for _f in "${INJECTED_FILES[@]}"; do
  basename "$_f" .md >> "$DEDUP_FILE" 2>/dev/null || true
done
```

Плюс тест: SessionStart создаёт dedup-файл с ожидаемыми слагами.

---

## Task 4: WAL trigger-match → scoring

В `session-start.sh` найти функцию/блок вычисления `WAL_SCORES`. Добавить обработку event type `trigger-match` с весом как у `outcome-positive`.

Тест: после нескольких `trigger-match` в WAL, SessionStart для нового сессии учитывает эти файлы выше.

---

## Task 5: Registration

### install.sh
Добавить UserPromptSubmit hook в settings.json:

```json
{
  "hooks": {
    "SessionStart": [...],
    "UserPromptSubmit": [
      {
        "hooks": [
          { "type": "command", "command": "$HOOK_DIR/user-prompt-submit.sh" }
        ]
      }
    ]
  }
}
```

### CHANGELOG.md
v0.5 entry: context triggers, frontmatter fields `triggers`/`trigger`, Triggered section.

### README.md
Секция "Triggers" — пример использования.

---

## Task 6: Validation

1. Все тесты PASS
2. Live тест: `echo '{"session_id":"v05-test","prompt":"помоги починить tailwind hsl"}' | bash hooks/user-prompt-submit.sh`
3. Проверить реальный prompt в новой сессии через /exit + restart

---

## Success

- [ ] 9 новых тестов pass
- [ ] Все старые тесты pass (132 + 9 = 141)
- [ ] Bench tests pass (18)
- [ ] Live demo: триггер активирует нужный файл
