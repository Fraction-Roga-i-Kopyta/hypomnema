# v0.5 "Контекстные триггеры" — Prompt-based Activation

**Дата:** 2026-04-12
**Статус:** approved
**Предыдущая версия:** v0.4 (related links, cluster activation, contradicts, cascade)

---

## Цель

Записи памяти, помеченные `triggers`/`trigger`, активируются при совпадении фразы в первом сообщении пользователя. Это дополняет CWD/git-based scoring мгновенной реакцией на явный контекст задачи.

**Не заменяет SessionStart-инъекцию** — работает поверх неё через отдельный hook.

---

## 1. Источник сигнала

**UserPromptSubmit hook** (Claude Code event). Срабатывает на каждый prompt пользователя. Первый prompt в сессии — основной источник триггеров; последующие prompt'ы тоже обрабатываются (триггеры не "сгорают").

**Почему не SessionStart:** при SessionStart нет сообщения пользователя. CWD/git уже покрывается существующими `keywords:`.

---

## 2. Frontmatter — поля `triggers` и `trigger`

### Новое: `triggers` (массив)

```yaml
triggers:
  - "css layout"
  - "tailwind hsl"
  - "flex gap"
```

### Старое: `trigger` (single phrase, обратная совместимость)

```yaml
trigger: "CSS layout баг"
```

Оба поля поддерживаются одновременно. Парсер объединяет их в единый список фраз.

### Что считается фразой

- **Plain string**, case-insensitive substring match в `prompt.lower()`
- **Без regex** на старте (простота, отладка, безопасность)
- **Любые символы, кроме `:`** (yaml collision) и кавычек (yaml syntax)
- **Рекомендуемая длина:** 2-5 слов. Слишком короткие (`"css"`) дадут false positives; слишком длинные — промахи.

---

## 3. Матчинг

```python
# Pseudocode
lower_prompt = prompt.lower()
for file in scan(mistakes, feedback, strategies, knowledge, notes):
    phrases = file.triggers + [file.trigger] if present
    for phrase in phrases:
        if phrase.lower() in lower_prompt:
            candidates.append((file, phrase))
            break  # один match на файл достаточно
```

**Приоритеты сортировки кандидатов:**
1. `status: pinned` выше `status: active`
2. Больший `severity` (major > minor) для mistakes
3. Больший `recurrence` для mistakes
4. Больший `ref_count` (популярность)

**Cap:** `MAX_TRIGGERED=4` — сверху обрезается, нижние кандидаты игнорируются.

---

## 4. Dedup с SessionStart

### Файл состояния

`/tmp/.claude-injected-${SESSION_ID}.list` — плоский список слагов, по одному на строку. Пишется в SessionStart.

### Поведение при отсутствии файла

Если файл удалён (например, `/tmp` очищен) — dedup пропускается. Worst case: дубликат в контексте. Это приемлемо (не data loss).

### Дедуп между промптами

Внутри одной сессии, если триггер уже активировал файл → не повторять. Расширяем dedup-файл: user-prompt-submit дописывает активированные слаги.

---

## 5. Output format

```markdown
## Triggered (from prompt)

### tailwind-hsl-channels-in-inline-styles (matched: "tailwind hsl")
<body без frontmatter>

### css-layout-debugging (matched: "css layout")
<body без frontmatter>
```

Пустой блок при отсутствии матчей → exit 0 без output (не добавлять пустые секции).

---

## 6. WAL event

```
YYYY-MM-DD|trigger-match|<slug>|<session_id>
```

### Использование в будущем scoring

В SessionStart-е `WAL_SCORES` учитывает различные события. v0.5 добавляет `trigger-match` как **positive signal** — файл, активированный триггером, вероятно будет полезен и в будущих сессиях.

**Вес:** такой же как `outcome-positive` (наивысший positive сигнал). Формула scoring — см. session-start.sh, функция `compute_wal_scores`.

---

## 7. Изменения в файлах

| Файл | Изменение |
|------|-----------|
| `hooks/user-prompt-submit.sh` | **НОВЫЙ.** Парсинг prompt, матчинг, output, WAL |
| `hooks/session-start.sh` | +запись dedup-list в `/tmp/.claude-injected-${SESSION_ID}.list`; +учёт `trigger-match` в WAL_SCORES |
| `hooks/test-memory-hooks.sh` | +smoke tests для триггеров |
| `install.sh` | +регистрация UserPromptSubmit hook в settings.json |
| `templates/settings.json` (если есть) | +UserPromptSubmit |
| `CHANGELOG.md` | v0.5 entry |
| `README.md` | Документация `triggers:` поля |

---

## 8. Рекомендации по использованию

**Где имеет смысл ставить `triggers`:**
- Mistakes с характерной фразой-симптомом (`"tailwind hsl"`, `"docker .next stale"`)
- Strategies с явным триггером задачи (`"CSS layout баг"`, уже есть)
- Knowledge с доменной терминологией (`"atlassian user lifecycle"`)

**Где не нужно:**
- Feedback (общие правила, должны применяться всегда)
- Notes (справочные, активируются keyword-scoring'ом)

---

## 9. Ограничения v0.5

1. **Только plain substring** — regex откладывается на v0.5.1 при необходимости
2. **Первое ключевое слово = полный prompt** — не парсим multi-message threads
3. **Нет learning-loop** — если файл активирован триггером, но бесполезен, система не штрафует (только positive сигнал)
4. **Нет UI отладки** — диагностика через WAL + ручной прогон хука

---

## 10. Success criteria

- [ ] User пишет `"помоги починить tailwind hsl"` → `tailwind-hsl-channels-in-inline-styles` в Triggered секции
- [ ] Case-insensitive: `"Tailwind HSL"` тоже матчит
- [ ] Dedup: если файл уже в SessionStart — не дублируется
- [ ] Cap 4 работает при большом количестве матчей
- [ ] WAL event записан с `trigger-match|slug|session_id`
- [ ] Старое `trigger:` поле всё ещё работает (обратная совместимость)
- [ ] 100% existing tests PASS + новые smoke tests PASS
