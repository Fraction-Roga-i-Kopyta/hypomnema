# v2.0 — Governance-слой поверх native-памяти Claude Code

**Дата:** 2026-05-28
**Статус:** design approved (brainstorming), pending implementation plan
**Scope:** MAJOR — переархитектура: native-память как субстрат, hypomnema как Go-движок governance сверху
**Предыдущая версия:** v1.1.2 (самостоятельный файловый стор + bash-хуки + scoring)

---

## 0. TL;DR

Claude Code обзавёлся **нативной файловой памятью** (default-on, v2.1.59+: `~/.claude/projects/<slug>/memory/`, минимальный frontmatter, `MEMORY.md`-индекс, on-demand чтение моделью). Она коммодитизирует *capture + index + inject* — но **не делает ничего из measurement / decay / ранжированной авто-инъекции**.

v2.0 переводит hypomnema на native как **субстрат хранения** и достраивает сверху ровно то, чего у платформы нет или слабо. Миссия не меняется — усиление возможностей агента там, где у платформы инструментов нет.

Это активация revisit-clause из ADR `file-based-memory.md` («если выходит native memory API — hypomnema становится надстройкой, а не основным стором»), но с уточнением: **не shim, а governance-слой + глобальный стор**, которого у native нет.

---

## 1. Зафиксированные решения (brainstorming)

| Решение | Выбор | Следствие |
|---|---|---|
| **Хранилище** | Native + SQLite-sidecar | Контент только в native-файлах; метаданные hypomnema — в sidecar, ключёванном по файлам. Ноль дублирования, schema-форк исчезает |
| **Миграция** | Полная one-shot + прунинг | 166 файлов → native-формат, sidecar из WAL, мёртвый груз отсекается. Миграция = естественный GC |
| **Реализация** | Go-first, один бинарь + тонкие bash-шимы | Весь рантайм-hot-path в Go; bash 3.2/awk/sed-налог испаряется. Портируем только проверенное, дормантное выкидываем |
| **Идентичность** | hypomnema v2.0, тот же репо | Эволюция, не форк |

### 1.1. Keep / Merge / Drop

**KEEP** (данные подтвердили ценность): ранжированная авто-инъекция (главный дифференциатор — native инжектит только индекс), outcome-измерение (WAL; precision 84%, 465 pos / 8 neg), ref_count + spaced-repetition, decay/lifecycle, dedup на записи, secrets-gate, self-profile.

**MERGE**: два инжект-пайплайна (SessionStart composite-score + UserPromptSubmit substring-triggers с negation-окнами) → **один relevance-ранкер**. Реактивная релевантность сохраняется (572 `trigger-useful` переезжают в ранкер), уходят negation-окна, отдельный awk-пайплайн, ADR `two-scoring-pipelines` + `substring-triggers-with-negation`.

**DROP** (дормантно/коммодитизировано/нишево): TF-IDF body-scoring (cold-start-gated, маргинальный lift; семантику закрывает сама модель on-demand), shadow-FTS retrieval (чисто диагностика, не инжектит), cold-start gates (не нужны после прунинга + упрощённого скоринга), decision-review-triggers (нишево, вернуть позже). FTS5-инфраструктура (sync/query/shadow) ретайрится целиком.

> Спасаем из выкидываемого **Unicode-токенизатор** (`internal/tfidf`) — переезжает в `rank` (кириллица в корпусе критична). Выкидываем TF-IDF-*скоринг*, не токенизацию.

---

## 2. Архитектура

```
┌─ Claude Code harness ──────────────────────────────────────┐
│  native memory: ~/.claude/projects/<slug>/memory/*.md       │
│  • контент (минимальный frontmatter: name/description/type) │
│  • MEMORY.md индекс ── инжектит сам harness                  │
└───────────────▲───────────────────────────▲────────────────┘
       read-only │ (контент)        Write-перехват │ (guard)
┌────────────────┴───────────────────────────┴───────────────┐
│  hypomnema v2 — memoryctl (Go) + тонкие bash-шимы           │
│                                                             │
│  inject ──► additionalContext (ранжированный топ-K)         │
│  close  ──► outcomes → WAL → effectiveness/decay → profile  │
│  guard  ──► secrets-gate + dedup (PreToolUse:Write)         │
│  migrate──► one-shot конверсия + прунинг                    │
│                                                             │
│  WAL (append-only текст) = ПРАВДА событий                   │
│  SQLite sidecar = производная проекция (перестраиваема)     │
│  ~/.claude/memory-global/ = глобальный стор (native-формат) │
└─────────────────────────────────────────────────────────────┘
```

**Граница с harness:** не воюем за `MEMORY.md` — индекс отдаём платформе. Наши топ-K фактов идут отдельным каналом — `additionalContext` из SessionStart-шима. Native = индекс + on-demand чтение; hypomnema = проактивная инъекция того, что модель сама бы не догадалась прочитать.

---

## 3. Компоненты

### 3.1. Командная поверхность

| `memoryctl <verb>` | Событие | Делает |
|---|---|---|
| `inject` | SessionStart + UserPromptSubmit | keywords → ранжирует native-факты → топ-K в `additionalContext` (JSON). Горячий путь |
| `guard` | PreToolUse:Write на memory-путь | secrets-gate + dedup-чек → exit 0/2 |
| `close` | Stop | атрибуция outcomes → WAL → пересчёт effectiveness/decay → self-profile + continuity |
| `migrate` | one-shot вручную | старый стор → native + sidecar + прунинг |
| `doctor` / `profile` | вручную | здоровье / отчёт о точности |

### 3.2. Bash-шимы

Каждый ~10 строк: маршалит env/stdin → аргументы бинаря, релеит stdout/exit. **Ноль логики.** Четыре: `session-start`, `user-prompt-submit`, `pre-tool-write`, `session-stop`.

### 3.3. Go-пакеты (один пакет — одна ответственность)

| Пакет | Ответственность | Зависит от |
|---|---|---|
| `internal/native` | Адаптер native-стора. Единственное место, знающее native-формат: enumerate/parse, резолв project-memory-dir из cwd. Контент **read-only** | — |
| `internal/sidecar` | SQLite-проекция. Единственное место с SQLite: схема, апсерты, ранжир-запросы. Перестраиваема из WAL+frontmatter | wal, native |
| `internal/wal` | Append-only лог событий = источник правды. Текстовый, греппабельный, `four-column-invariant` сохраняется | — |
| `internal/rank` | **Ranker (слитый пайплайн).** ЧИСТАЯ функция: (keywords, candidates+meta, quotas) → топ-K. Комбинирует сигналы `overlap`, `log10(1+ref_count)`, `recency`, `effectiveness` + project/domain-фильтр + квота. **Ни один нулевой сигнал не обнуляет кандидата** (новый native-факт с ref_count=0 инжектабелен). Точная форма (аддитивная/мультипликативная) и веса — калибруются A/B-харнесом (§6.1). Без I/O → юнит-тестируема и A/B-абельна | — |
| `internal/inject` | Оркестратор: keywords → native.List → sidecar.Get → rank.TopK → JSON | native, sidecar, rank |
| `internal/outcome` | Детект+атрибуция исходов на close, эмит WAL | wal, native |
| `internal/decay` | Lifecycle: stale по age/ref/outcomes. По умолчанию **down-rank** (не инжектим), без мутации native-контента; перемещение в archive — только по явному `gc` | sidecar |
| `internal/dedup`+`fuzzy` | Дедуп на записи (fuzzy уже есть) | native |
| `internal/secrets` | Secrets-gate (портируем из bash в Go) | — |
| `internal/migrate` | One-shot конверсия + прунинг | все |
| `internal/profile`/`doctor`/`project`/`domains` | self-profile / здоровье / cwd→slug / домены | sidecar, wal |

### 3.4. Схема sidecar (SQLite — производная; WAL — правда)

```sql
CREATE TABLE memory (              -- одна строка на native-файл
  slug         TEXT PRIMARY KEY,   -- путь относительно memory-dir, напр. 'feedback_x.md'
  content_sha  TEXT,               -- детект rename/edit → переслинковка
  type TEXT, name TEXT, description TEXT,   -- type здесь = ТОЧНЫЙ (8 типов), не грубый native
  project TEXT, domains TEXT,       -- derived (origin/cwd, инференс)
  created TEXT, last_injected TEXT,
  ref_count INTEGER DEFAULT 0,
  status TEXT DEFAULT 'active',     -- active|pinned|stale (lifecycle, sidecar-owned)
  effectiveness REAL                -- материализованный (pos+1)/(pos+neg+2)
);
CREATE TABLE keyword (slug TEXT, term TEXT, weight REAL);   -- derived из name/description/body
CREATE TABLE outcome (slug TEXT, ts TEXT, kind TEXT);       -- positive|negative|useful|silent
CREATE TABLE meta    (k TEXT PRIMARY KEY, v TEXT);          -- schema_version, last_wal_offset
```

Связь WAL↔SQLite — тот же принцип, что у текущего `index.db`: удалил sidecar → перестроится из WAL + скан native-frontmatter. Ключ — `slug`; `content_sha` ловит переименования; doctor флагует орфанов.

**Тип-маппинг:** native-схема знает 4 типа (`user|feedback|project|reference`) — грубее наших 8. Native-frontmatter = грубый тип; `sidecar.type` = точный (mistake/strategy/knowledge/decision/note → `reference` в native), и именно он рулит квотами (3+3 mistakes, cap-6 feedback…).

---

## 4. Поток данных

### 4.1. Жизненный цикл сессии

**SessionStart** (`inject --event=session-start`)
1. `project.Detect(cwd)` + `domains.Infer(git)` + keywords (ветка, diff-имена, коммиты, basename).
2. `native.List()`; если sidecar дрейфанул (content_sha/mtime) → `sidecar.Reproject()`.
3. `rank.TopK(keywords, candidates, quotas)` — фильтр project/domain/status (stale пропускаем), квоты по `sidecar.type`.
4. → `additionalContext` JSON. WAL: `inject|slug|session`. Записать injected-set сессии (для атрибуции).

**UserPromptSubmit** (`inject --event=prompt --prompt=…`)
- Тот же ранкер, keywords += токены промпта (реактивность взамен substring-triggers). Ре-ранк, инжектим топ-K, которых ещё не было в сессии. Negation-окна уходят (следствие MERGE): ранкер трактует слова как сигнал релевантности, не императив-триггер.

**PreToolUse:Write на memory-путь** (`guard`)
- `secrets.Scan` → кредл вне фенса, len≥8 → **exit 2** + stderr; override `HYPOMNEMA_ALLOW_SECRETS=1` / `.secretsignore`.
- `dedup.Check` → fuzzy-похоже → **warn (exit 0)**, не блок.

**Stop** (`close`)
1. `outcome.Detect(session)` → атрибуция на injected-set → WAL (`outcome-pos/neg`, `trigger-useful/silent`, `session-close`).
2. `sidecar.Reproject()` инкрементально (ref_count, effectiveness, last_injected).
3. `decay.Recompute()` → stale в sidecar (down-rank, без перемещения).
4. `profile.Regenerate()` → self-profile.md.
5. Continuity → один авто-управляемый native-файл (100% derived из git+сессии, явно помеченный) — единственное исключение из «не пишем native-контент».

### 4.2. Глобальный стор (структурный элемент, которого нет у native)

Native-память — **только per-project**, глобального стора нет. У hypomnema есть `project: global` (универсальные правила: `language-always-russian`, `wrong-root-cause-diagnosis`). Решение: глобальные факты живут в **hypomnema-owned native-формат-дире** `~/.claude/memory-global/` (путь конфигурируем), `inject` объединяет global + project-native в рантайме, sidecar трекает оба. Это буквально «строим, где native слаб».

### 4.3. Миграция (`migrate`, one-shot)

1. Читаем старый стор (9 dirs).
2. **Прунинг:** drop если `ref_count==0 && status!=pinned && нет outcome-событий && created старше 30 дней`; stale (по type-порогам) → backup; остальное — keep.
3. **Конверсия типов:** native = грубый тип, `sidecar.type` = точный.
4. Пишем native-файлы, маршрутизируя по `project:` (global → memory-global, иначе → project-dir).
5. Сидим sidecar+WAL из старого WAL (маппинг old-slug→new-slug, seed ref_count/effectiveness/created).
6. Верификация: parity-чек, doctor, отчёт (migrated/pruned/global-vs-project).
7. Старый стор → backup (не `rm`).

---

## 5. Обработка ошибок

**Управляющий инвариант** (сохраняем ADR `silent-fail-policy`): хук никогда не ломает сессию. Любая внутренняя ошибка `inject`/`close` → **exit 0** + stderr + `wal-error`. Единственный путь exit≠0 — `guard` при *обнаруженном* секрете.

| Сбой | Поведение |
|---|---|
| Sidecar отсутствует/битый/schema-mismatch | Rebuild из WAL+native; не успели/упал → **degraded ranker** (overlap+recency, ref_count/effectiveness нейтральны). Инъекция всё равно идёт |
| Sidecar дрейфанул (content_sha≠) | Reproject; при edit — ре-токенизация keywords, **ref_count/outcomes сохраняются** (идентичность факта переживает правку) |
| Native-файл удалён | Орфан-строка не инжектится; `status=deleted`, outcome-история **не трётся** |
| Native-файл переименован | `content_sha` переслинковывает slug; неоднозначно → новая строка |
| Native написал новый факт | Кандидат с нейтральными метаданными, инжектабелен сразу; `guard`/`close` заводит строку |
| Превышен time-budget SessionStart | Дедлайн в `rank` (мягкий 800 мс) — отдаём best-so-far/пусто, exit 0 |
| WAL lock/запись упала | Best-effort; stderr, продолжаем |
| Параллельные сессии | SQLite WAL-mode + busy_timeout; текстовый WAL — flock; injected-set по session_id |
| Парс native-файла упал / неизвестные поля | Скип файла (лог); неизвестные поля игнорим (forward-compat) |

**`guard` fail-policy:** при ошибке *самого сканера* (не обнаружения) — **fail-open** (exit 0 + громкий stderr). Обоснование: консистентно с silent-fail; secrets-gate — best-effort defense, не жёсткая граница.

**Миграция:** idempotent, `--dry-run`, backup-not-delete, parity-verify перед ретайром.

---

## 6. Тестирование

| Уровень | Что |
|---|---|
| Unit: чистый `rank` | Table-driven + property: монотонность, квоты, stale-исключение, project/domain-фильтр. Здесь верифицируется «точнее» |
| `sidecar` проекция | Детерминизм rebuild==инкремент; round-trip WAL→проекция; recovery после порчи DB |
| `native` адаптер | Фикстуры: missing/unknown поля, CRLF, кириллица, rename/delete; стабильность content_sha |
| Миграция | dry-run-план на реальном корпусе, parity-gate (см. ниже), идемпотентность |
| Integration / hook-contract | Шимы кормим реальным hook-JSON; silent-fail (форс-ошибка → exit 0); guard exit 2 / fail-open |
| Регрессия (golden) | Снапшот `additionalContext` JSON для фикс-корпуса → golden |
| Perf-gate | `go test -bench`: inject на ~200 файлах < 1s warm, регресс-толеранс 1.2× |
| Concurrency | Параллельные inject+close → нет corruption/deadlock |

### 6.1. A/B-харнес нулевой гипотезы (то, что упускали — теперь first-class)
`rank` чистый → реплеим исторический WAL+корпус, сравниваем **ranked топ-K vs dump-all / топ-N-без-ранжирования** на прокси полезности (содержал ли injected-set файлы, получившие `trigger-useful` в той сессии). Отвечает данными: окупается ли ранжирование. Артефакт релиза, не afterthought.

### 6.2. Протокол миграции (риск на реальных данных)
`migrate --dry-run` → ручной ревью плана → execute с backup → **parity-gate ≥70% overlap** (inject на мигрированном ∩ inject старого hypomnema на тех же keywords) → идемпотентность.

### 6.3. Тестовый налог, который исчезает
Уходят: bash/Go parity-gate (Go = импл), BSD-vs-GNU sed/awk/stat-пробы, awk-TSV edge-кейсы. `test-memory-hooks.sh` (3858 строк) схлопывается в Go-тесты. CI: `go test ./...` + bench + migrate-dry-run, матрица macOS+Linux без cross-platform-шаманства.

---

## 7. Upgrade & distribution (open-repo)

v2.0 — MAJOR с разрывом субстрата. Репо публичный → переход обязан обслужить и существующих v1.x-юзеров, и новых.

### 7.1. Гейт совместимости (native — жёсткое требование)
install/upgrade проверяет: `claude --version` ≥ 2.1.59 **И** auto-memory не выключена (`autoMemoryEnabled` ≠ false, env `CLAUDE_CODE_DISABLE_AUTO_MEMORY` не задан). Не совпало → **отказ с actionable-сообщением**: «обнови Claude Code / включи auto-memory / останься на hypomnema v1.x». Legacy self-store режима НЕТ. v1.x остаётся тегнутой и работающей as-is (без бэкпортов).

### 7.2. Апгрейд существующего v1.x-юзера (guided, не silent)
1. **Детект v1:** наличие `~/.claude/memory/` с v1-frontmatter + старые хуки в settings.json.
2. `memoryctl migrate --dry-run` → показать план (keep/prune/global-vs-project routing) → **явный confirm**. Флаг `--auto` для non-interactive.
3. **Backup:** старый стор → `~/.claude/memory.v1-backup-<date>/`; блок хуков settings.json сохраняется отдельно.
4. **Конверсия + seed** sidecar/WAL (§4.3).
5. **Хирургическая правка settings.json:** убрать v1-регистрации (session-start / user-prompt-submit / memory-dedup / …), добавить 4 тонких шима, **не трогая чужие настройки** (парсим JSON, правим только hypomnema-owned записи). Закрывает известную боль «хуки на диске, но не зарегистрированы».
6. **Верификация:** doctor + parity-gate ≥70%. Печать сводки (migrated / pruned / routed).

### 7.3. Rollback
`uninstall.sh` / `memoryctl migrate --rollback`: восстановить v1-backup стор + блок хуков, перерегистрировать v1-хуки. Backup-not-delete гарантирует обратимость.

### 7.4. Новый юзер (fresh v2)
Нет v1-стора. install: гейт (§7.1) → Go-бинарь + 4 шима → регистрация шимов → пустой sidecar + глобальный стор-дир → конверсия `seeds/` в native-формат и раскладка. `seeds/` теперь поставляются в native-формате.

### 7.5. Доки, артефакты и релиз
`docs/MIGRATION.md` +раздел «v1.x → v2.0» (гейт, шаги, rollback). `README.md` — переписать ложную премису «starts every session from zero» (native стартует не с нуля) на framing «governance-over-native». **`CLAUDE.md` (и проектная памятка) — переписать под v2:** native+sidecar модель, новые команды/шимы, удалённые фичи (TF-IDF/shadow/cold-start/два пайплайна), глобальный стор. Обновить `install.sh` / `uninstall.sh` / `Makefile` / `CHANGELOG.md`. **Релиз:** тег `v2.0.0` + GitHub release через skill `/release` (полный test-gate + CHANGELOG + GH release notes). v1.x остаётся на своём теге/ветке.

---

## 8. Влияние на ADR

| ADR | Судьба в v2 |
|---|---|
| `file-based-memory` | **Эволюция** — revisit-clause активирован; hypomnema = governance-слой + глобальный стор (не «shim») |
| `bash-go-gradual-port` | **Перевёрнут** — Go primary, bash = тонкие шимы. Новый ADR-replacement |
| `two-scoring-pipelines` | **Superseded** — один ранкер |
| `substring-triggers-with-negation` | **Superseded** — relevance-ранкер, negation убран |
| `fts5-shadow-retrieval` | **Superseded** — FTS5 ретайрится |
| `cold-start-scoring` | **Retired** — gates убраны |
| `scoring-weights` | **Пересмотрен** — новая формула ранкера |
| `quota-pool` | **Kept** — квоты на `sidecar.type` |
| `wal-four-column-invariant`, `silent-fail-policy`, `precision-class-ambient`, `intuition-milestone` | **Kept** |
| decision-review-triggers (фича) | **Retired** (ADR-доки остаются, фича вернётся позже) |

**Новые ADR (писать в имплементацию):** `native-primary-sidecar` (модель хранилища), `go-primary-bash-shims` (флип bash-go).

---

## 9. Не-цели (YAGNI)

- **Кросс-агентная портируемость** — осознанно отложена (выбран native-primary, Claude-специфичный). Чистые интерфейсы `native`-адаптера оставляют дверь открытой, но не строим под это.
- **decision-review-triggers, shadow-диагностика, TF-IDF** — не возвращаем в v2.
- **Облачная синхронизация** — вне scope (как и у native).
- **Legacy self-store режим для старого CC** — осознанно отклонён (§7.1). Бэкпорты v2-фич в v1.x — нет; v1.x замораживается на своём теге.

---

## 10. Параметры для калибровки (явные дефолты, тюнятся данными)

| Параметр | Дефолт | Где тюнить |
|---|---|---|
| Прунинг-порог | ref_count==0 & !pinned & 0 outcomes & >30д | §4.3, по результату dry-run |
| inject soft-deadline | 800 мс | §5, perf-gate |
| migration parity-gate | ≥70% overlap | §6.2, после A/B |
| глобальный стор путь | `~/.claude/memory-global/` | §4.2, конфиг |
| формула ранкера (форма + веса) | комбинация `overlap`, `log10(1+ref_count)`, `recency`, `effectiveness`; zero-safe | §3.3, A/B-харнес калибрует |

---

## 11. Success criteria

- [ ] `migrate --dry-run` на реальном корпусе даёт ревьюабельный план; execute сохраняет backup, идемпотентен
- [ ] После миграции `inject` пересекается ≥70% со старым поведением на репрезентативных keywords
- [ ] `inject` < 1s warm на ~200 файлах; degraded-ranker при убитом sidecar всё равно инжектит
- [ ] `guard` блокирует подложенный секрет (exit 2), fail-open при краше сканера
- [ ] Глобальные факты (`language-always-russian`) инжектятся в произвольном проекте
- [ ] A/B-харнес выдаёт вердикт: ранжирование vs dump-all на историческом WAL
- [ ] `go test ./...` зелёный на macOS+Linux; bash-шимы проходят hook-contract тесты
- [ ] self-profile регенерится; outcome-петля пишет в WAL как раньше
- [ ] Старый bash-плумбинг (awk-парсинг, FTS5, cold-start, два пайплайна) удалён, не закомментирован
- [ ] Гейт совместимости отказывает на CC < 2.1.59 / выключенной auto-memory с actionable-сообщением
- [ ] Апгрейд v1.x: dry-run → confirm → backup → конверсия → хирургическая правка settings.json; doctor зелёный
- [ ] Rollback восстанавливает v1-состояние из backup
- [ ] Fresh-install нового юзера поднимает v2 с native-seeds
- [ ] README больше не утверждает «starts every session from zero»; MIGRATION.md покрывает v1.x → v2.0
- [ ] README + CLAUDE.md описывают v2 (native+sidecar), не v1; CHANGELOG обновлён; GitHub-релиз `v2.0.0` опубликован через `/release`
```

