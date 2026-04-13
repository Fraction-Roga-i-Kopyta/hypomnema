# Memory Dashboard — Design Spec

**Дата:** 2026-04-13
**Автор:** akamash + Claude
**Статус:** approved (брейншторм пройден)

## Цель

Консольный дашборд для memory-системы Claude (`~/.claude/memory/`) — единая точка наблюдения и операций. Решает проблему: память выросла до 64+ заметок и 10+ хуков, а инструмента "посмотреть что внутри" нет, всё через `ls`/`cat`/`grep` руками.

## Не цели (явно)

- **Не редактор/браузер заметок.** Read-only. Любая правка контента — через обычный editor, не из дашборда (риск сломать систему теми, кто не понимает механизмов).
- **Не auto-инжект на SessionStart.** Не засорять контекст каждой сессии — дашборд вызывается явно, когда нужен.
- **Не замена хукам.** Хуки работают автономно; дашборд только показывает их состояние и даёт ручной триггер.

## Решение

Standalone TUI на pure bash + ANSI escape codes. Запуск: `~/.claude/bin/memory-dashboard.sh` (с алиасом `memory-dash`). Две вкладки: **Overview** (стат + здоровье памяти) и **Ops** (ручные операции + история хуков).

### Почему так
- **Pure bash, no deps** — соответствует стилю остальных инструментов в `~/.claude/bin/` и `hooks/`. Никакого `pip install`.
- **Standalone TUI** — на запрос, не фон. Не отнимает контекст-окно у сессии.
- **Два таба** — Overview и Ops логически разделены: смотреть и делать.

## UI / Layout

### Tab 1 — Overview

```
+-- Memory Dashboard ---------------------- 2026-04-13 21:32 --+
| [ Overview ]  Ops                                     q: exit|
+--------------------------------------------------------------+
| Storage                         Top Triggered (7d)           |
| -------                         -------------------          |
| Total notes:        64          wrong-root-cause       x12   |
|   mistakes:         14          code-approach           x8   |
|   notes:            10          debugging-approach      x6   |
|   projects:          4          oklch-naming            x3   |
|   feedback:          5                                       |
|   knowledge:         3          Recurring Mistakes           |
|   continuity:        4          ------------------           |
|   strategies:        2          wrong-root-cause      [x5]   |
|   seeds:             0          docker-stale-cache    [x3]   |
|                                                              |
| WAL: 142 entries (47 KB)        Recently Created             |
| Last consolidate:  3d ago       ----------------             |
| Last index regen:  2h ago       nlp-doc-success     (today)  |
|                                 session-types       (3d ago) |
| Active project: akasha                                       |
| Active domains: frontend        Stale (>90d, no refs)        |
|                                 ---------------------        |
|                                 (none)                       |
+-- [Tab] switch  [r] refresh  [q] quit -----------------------+
```

### Tab 2 — Ops

```
+-- Memory Dashboard ---------------------- 2026-04-13 21:32 --+
|   Overview  [ Ops ]                                   q: exit|
+--------------------------------------------------------------+
| Operations                      Last Hook Runs               |
| ----------                      --------------               |
| [1] Regenerate index            session-start    OK   21:30  |
| [2] Run consolidate             user-prompt      OK   21:31  |
| [3] Run dedup (dry-run)         memory-stop      OK   20:44  |
| [4] Backfill ref_count          memory-index     OK   19:12  |
| [5] Enrich frontmatter                                       |
| [6] Score strategies            Recent Errors                |
| [7] Self-profile refresh        -------------                |
| [8] WAL compact                 (none in 24h)                |
|                                                              |
| Health Checks                   Activity (7d)                |
| -------------                   -------------                |
| Frontmatter valid: 64/64 OK     Notes created:    3          |
| Broken md links:   0      OK    Notes referenced: 47         |
| Orphan files:      0      OK    Mistakes added:   1          |
| MEMORY.md sync:    OK           Hook errors:      0          |
+-- [1-8] run  [Tab] switch  [r] refresh  [q] quit ------------+
```

> Mockups намеренно ASCII-only (без emoji/box-drawing) для предсказуемой ширины в markdown-рендерах. **В реальной имплементации** разрешено использовать Unicode box-drawing (`┌─┐│└┘├┤`) и эмодзи (📊🔥⚠️🆕💤⚙️📋❌🩺📈) — терминалы их корректно отображают. Главное правило: каждая ячейка содержимого должна укладываться в фиксированную ширину колонки (37 символов left, 37 right) с учётом того, что эмодзи занимают 2 cell-а.

## Архитектура

Один скрипт `memory-dashboard.sh` с модульной структурой функций:

```
memory-dashboard.sh
├── main()                       — event loop, keyboard handling
├── render_frame()               — рамка, заголовок, табы, футер
├── render_overview_tab()        — рендер первой вкладки
│   ├── stats_storage()          — подсчёт заметок по категориям
│   ├── stats_wal()              — WAL size, last consolidate
│   ├── stats_top_triggered()    — парсинг WAL за 7d
│   ├── stats_recurring()        — frontmatter recurrence
│   ├── stats_recent()           — sort by created
│   ├── stats_stale()            — referenced > 90d AND ref_count == 0
│   └── stats_active()           — read _agent_context.md
├── render_ops_tab()             — рендер второй вкладки
│   ├── ops_menu()               — список операций
│   ├── ops_hook_history()       — последние запуски (timestamps файлов)
│   ├── ops_recent_errors()      — поиск ошибок в логах хуков
│   ├── ops_health_checks()      — frontmatter / links / orphans / sync
│   └── ops_activity()           — стата за 7d из WAL
└── ops_run(N)                   — выполнить операцию N (1-8)
```

### Источники данных

| Что | Откуда |
|---|---|
| Список заметок | `find ~/.claude/memory/{mistakes,notes,...}/*.md` |
| Frontmatter | `awk` парсинг по `---` блокам |
| WAL события | `~/.claude/memory/.wal/` (структура изучается на этапе T1) |
| Active project/domains | `~/.claude/memory/_agent_context.md` |
| Hook last run | `stat -f %m` по timestamp последней модификации логов хуков |
| Hook errors | поиск `ERROR`/`error`/`failed` в логах (если есть; иначе блок убирается) |
| Existing scripts | `~/.claude/bin/memory-*.sh` и `~/.claude/hooks/memory-*.sh` |

### Wiring операций (Ops menu)

| # | Команда |
|---|---------|
| 1 | `~/.claude/hooks/regen-memory-index.sh` |
| 2 | `~/.claude/bin/memory-consolidate.sh` |
| 3 | `python3 ~/.claude/bin/memory-dedup.py --dry-run` |
| 4 | `~/.claude/bin/memory-backfill-refcount.sh` |
| 5 | `~/.claude/bin/memory-enrich-frontmatter.sh` |
| 6 | `~/.claude/bin/memory-strategy-score.sh` |
| 7 | `~/.claude/bin/memory-self-profile.sh` |
| 8 | `~/.claude/hooks/wal-compact.sh` |

При запуске операции — TUI временно выходит из alt-screen, показывает stdout скрипта, после `Press any key` возвращается на вкладку Ops.

## Технические детали

- **Шебанг:** `#!/bin/bash` (совместимо с macOS bash 3.2 — без assoc arrays, без `mapfile`)
- **Alt-screen:** `tput smcup` / `rmcup` для сохранения терминала после выхода
- **Cursor:** `tput civis` / `cnorm` (скрыть/показать)
- **Reading keys:** `read -rsn1` для одиночных клавиш, `read -t N` для refresh-таймера
- **Цвета:** ANSI `\033[XXm` через `tput setaf`/`setab` (портабельнее)
- **Размеры:** `tput cols` / `tput lines` — graceful fallback если терминал < 80×24 (предупреждение и выход)
- **Refresh:** ручной (`r`), без auto-tick (избегаем мерцания и нагрузки на WAL парсинг)

## Обработка ошибок

- **Отсутствующий скрипт ops** (например, `memory-consolidate.sh` ещё не существует) — в Ops tab рядом с пунктом показывается `(missing)`, запуск блокируется
- **Нет WAL директории** — блоки "Top triggered"/"Activity" показывают "—" (не падаем)
- **Нет `_agent_context.md`** — Active project/domains: "—"
- **Битый frontmatter** в заметке — Health check показывает счётчик, заметка пропускается, не падаем
- **Терминал слишком узкий** (<80 cols) — выход с сообщением "Need terminal ≥ 80×24"

## Тестирование

Pure bash без unit-test инфраструктуры. Ручная верификация:
1. Запуск на текущем состоянии памяти → визуальный осмотр обеих вкладок
2. Переключение Tab/1/2 → правильная вкладка активна
3. Запуск каждой из 8 операций → корректный выход и возврат
4. Ресайз терминала → graceful (или фиксированный layout, выбор на этапе T6)
5. `q` → чистый выход, alt-screen восстановлен, cursor виден

## Скоуп

**MVP (этот спек):**
- Overview tab со всеми блоками выше
- Ops tab с 8 операциями + history + health checks
- Pure bash, alt-screen, ручной refresh

**Не входит (на будущее):**
- Auto-refresh с конфигурируемым интервалом
- Графики активности (sparklines)
- Search/filter заметок
- Просмотр содержимого заметки (даже read-only — отказались осознанно)
- Webhook-уведомления о recurring mistakes
- Темы оформления

## Examples (для onboarding на чистой установке hypomnema)

Эта секция нужна агенту, открывшему hypomnema-репу впервые, чтобы понять **как** дашборд читает память и **что** именно показывает. Дашборд работает поверх соглашений memory-системы — если они нарушены, парсеры покажут "—" или health checks загорятся красным.

### E1. Структура `~/.claude/memory/` (что дашборд сканирует)

```
~/.claude/memory/
├── MEMORY.md                  # индекс (читается клиентом, не дашбордом)
├── _agent_context.md          # активный проект + домены (Overview block)
├── self-profile.md            # саморефлексия Claude (не сканируется)
├── user-profile.md            # профиль пользователя (не сканируется)
├── projects.json              # реестр проектов
├── projects-domains.json      # маппинг проект -> домены
├── .wal/                      # write-ahead log (Top Triggered, Activity)
│   └── 2026-04-13.jsonl       # пример: события за день
├── continuity/                # текущее состояние работы по проектам
│   └── akasha.md
├── decisions/                 # архитектурные решения
├── feedback/                  # правила взаимодействия от пользователя
│   └── code-approach.md
├── journal/                   # хронологические записи по дням
│   └── 2026-04-13.md
├── knowledge/                 # фактические знания
├── mistakes/                  # антипаттерны (с recurrence)
│   └── wrong-root-cause-diagnosis.md
├── notes/                     # свободные заметки
├── projects/                  # описание проектов
│   └── akasha.md
├── seeds/                     # затравки для будущих заметок
└── strategies/                # успешные стратегии (с success_count)
    └── multi-agent-analysis.md
```

Дашборд **сканирует только** директории-категории (10 шт.) для подсчёта `Total notes` и подкатегорий. Остальное — игнорируется.

### E2. Пример frontmatter (что парсит дашборд)

**Mistake** (для блока "Recurring Mistakes"):

```markdown
---
type: mistake
project: akasha
created: 2026-04-05
referenced: 2026-04-13
status: active
severity: major          # critical | major | minor
recurrence: 5            # сколько раз повторилась
ref_count: 8
keywords: [debug, hypothesis, root-cause]
domains: [debugging]
root-cause: "Claude jumps to first hypothesis..."
prevention: "List 2-3 possible causes before first fix"
---
```

Дашборд использует: `recurrence` (для блока Recurring), `created` (для Recently Created), `referenced` + `ref_count` (для Stale), `severity` (для приоритета).

**Note** (минимум):

```markdown
---
name: NLP-doc success pattern
description: Public NLP methodology — successful systematization
type: note
project: akasha
created: 2026-04-13
referenced: 2026-04-13
status: active
ref_count: 1
---
```

Дашборд требует **обязательно**: `type`, `created`. Опционально для расширенных блоков: `referenced`, `ref_count`, `severity`, `recurrence`. Health check `Frontmatter valid` падает если нет `type` или `created`.

### E3. Пример WAL entry (источник Top Triggered и Activity)

WAL хранит JSONL, одна строка = одно событие. Минимальная схема:

```jsonl
{"ts":"2026-04-13T21:30:00Z","event":"trigger","note":"wrong-root-cause-diagnosis","trigger":"debugging"}
{"ts":"2026-04-13T21:31:15Z","event":"create","note":"nlp-doc-success-pattern","category":"notes"}
{"ts":"2026-04-13T21:32:02Z","event":"reference","note":"akasha","category":"projects"}
{"ts":"2026-04-13T21:33:40Z","event":"trigger","note":"code-approach","trigger":"sql"}
```

Дашборд агрегирует:
- **Top Triggered (7d):** `event=trigger`, GROUP BY `note`, ORDER BY count DESC LIMIT 4
- **Activity (7d):**
  - `Notes created` = COUNT(`event=create`)
  - `Notes referenced` = COUNT(`event=reference`)
  - `Mistakes added` = COUNT(`event=create AND category=mistakes`)

Если `.wal/` отсутствует или пуст — оба блока показывают `—`. Это валидное состояние для свежей установки.

### E4. Пример `_agent_context.md` (источник Active project/domains)

```markdown
# Memory Context

**Active project:** akasha
**Active domains:** frontend, debugging

## Active Mistakes
...
```

Дашборд парсит только две bold-строки `Active project:` и `Active domains:`. Если файла нет — поля показывают `—`.

### E5. Скриншот: пустая память (свежая установка hypomnema)

Что увидит агент при первом запуске `memory-dash` после `git clone hypomnema`:

```
+-- Memory Dashboard ---------------------- 2026-04-13 12:00 --+
| [ Overview ]  Ops                                     q: exit|
+--------------------------------------------------------------+
| Storage                         Top Triggered (7d)           |
| -------                         -------------------          |
| Total notes:         0          (no WAL data)                |
|   mistakes:          0                                       |
|   notes:             0          Recurring Mistakes           |
|   projects:          0          ------------------           |
|   ...                           (none)                       |
|                                                              |
| WAL: -                          Recently Created             |
| Last consolidate:  -            ----------------             |
| Last index regen:  -            (empty)                      |
|                                                              |
| Active project: -               Stale (>90d, no refs)        |
| Active domains: -               ---------------------        |
|                                 (none)                       |
+-- [Tab] switch  [r] refresh  [q] quit -----------------------+
```

Это **валидное** состояние — ничего не сломано, просто памяти ещё нет. Health checks (Tab 2) тоже все зелёные при пустой памяти.

### E6. Сценарии использования

**Сценарий "утренняя проверка":**
1. Открыть терминал, `memory-dash`
2. Tab Overview — глянуть Recurring Mistakes (есть ли что повторяющееся)
3. Tab Ops — посмотреть Last Hook Runs (хуки работали ночью?), Recent Errors
4. `q` — выход

**Сценарий "после большой сессии":**
1. `memory-dash`, Tab Ops
2. `2` — Run consolidate (свернуть похожие заметки)
3. `3` — Run dedup --dry-run (посмотреть кандидатов на дедуп)
4. `1` — Regenerate index (обновить MEMORY.md)
5. `q`

**Сценарий "что-то странное с памятью":**
1. `memory-dash`, Tab Ops
2. Глянуть Health Checks — есть ли красное (broken links / orphans / sync mismatch)
3. Recent Errors — что упало в хуках
4. Если сломан frontmatter — `5` (Enrich frontmatter) для авто-починки

## Open questions (решаются на этапе имплементации)

1. Структура WAL — какие именно события и в каком формате логируются. Изучить на T1 перед написанием парсеров.
2. Где хуки пишут логи (если пишут) — иначе блок "Last Hook Runs" / "Recent Errors" заменяем на "—" или убираем.
3. macOS-specific `stat -f %m` vs Linux `stat -c %Y` — нужна обёртка `stat_mtime()` для портабельности (или фиксируем macOS only — это локальная утилита автора).
