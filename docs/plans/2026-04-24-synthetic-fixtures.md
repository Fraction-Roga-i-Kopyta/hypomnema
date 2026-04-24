# Synthetic test fixtures — спека v0.14

## Контекст

После round 2 external review (`docs/audits/2026-04-24-v0.13-review-response-round-2.md`) встал вопрос: как калибровать P1.0 (Cold-start scoring ADR) и P1.1 (outcome-positive expansion) на реальных данных, не экспонируя приватный контент?

**Что не подошло:**

1. **Коммит external-ref-corpus snapshot** (43 файла + WAL от живого пользователя). Проблемы:
   - Residual leakage risk (README snapshot'а сам требует manual scan перед публикацией).
   - Imitation risk: новый пользователь может принять перекошенный onboarding-корпус за канон.
   - Bus factor внешнего ревьюера: git-история не стирается.
   - **Нет транскриптов сессий** в snapshot'е — P1.1 (evidence-match в сессиях) калибровать невозможно.

2. **Private submodule.** Усложняет CI, внешние контрибьюторы не могут прогнать полный test suite.

**Выбрано: synthetic fixtures + локальный cross-reference с external-ref-corpus.**

Ключевой инсайт: synthetic fixtures могут включать **и memory-файлы, и synthetic transcripts** (jsonl), то есть покрывают P1.1 evidence-match — чего в real-snapshot'е нет. External reference corpus остаётся у мейнтейнера локально как «gut check» против synthetic'а.

## Принципы дизайна

### 1. SPEC до кода — fixture-first

`README.md` каждой fixture с **expected outputs** пишется ДО того, как трогается соответствующий код (cold-start gating, outcome-positive expansion и т.п.). Потом код. Потом сравнение.

Если spec разошёлся с реальностью — **останавливаемся и понимаем почему**, не корректируем fixture задним числом. Тавтологический паттерн (fixture подгоняется под код, код подгоняется под fixture) ломается ровно здесь.

### 2. Одна fixture — одно утверждение

Каждая fixture заявляет **одну конкретную вещь**:
- `synthetic-cold-start` → «Bayesian + TF-IDF должны быть dormant».
- `synthetic-mature` → «все 5 компонентов должны быть active».
- `synthetic-feedback-heavy` → «P1.1 эмитит outcome-positive для non-mistake файлов».

Без смешивания claim'ов в одном корпусе. Если хочется проверить «что будет, если»: отдельная fixture.

### 3. Expected output frozen as snapshot

Каждая fixture хранит **expected.json** — замороженный результат прогона (doctor output, self-profile metrics, scoring sample для test-промпта). Тест = `diff actual.json expected.json`. Любое изменение скоринг-формулы → snapshot обновляется **осознанно**, в одном коммите с изменением кода, с явным «expected changed because X».

Альтернатива (fuzzy assertions типа «Bayesian active если outcome ≥ 5») хуже: спецификация растворяется в условиях теста, а не в опорном файле.

### 4. Cross-reference с реальным корпусом — локально, протокол

External reference corpus **не commit'ится**. Но мейнтейнер прогоняет его локально раз в калибровку (перед merge ADR cold-start `proposed → active`, перед merge P1.1). Результат:
- Если synthetic cold-start и external-ref-corpus дают одинаковый dormant-verdict — synthetic достаточно реалистичен.
- Если расходится — synthetic упрощает что-то, чего реальность не упрощает. **Триггер добавить новый synthetic-профиль** или скорректировать один из существующих.

Результат cross-reference фиксируется в `docs/fixtures/real-corpus-notes.md` как **текст** (не данные): дата, версия hypomnema, synthetic verdict, real verdict, совпало/разошлось, действие.

### 5. Изоляция имён

Synthetic fixture'ы используют **generic example-project naming**: `todo-app`, `blog-api`, `data-pipeline`, `weather-cli`. Это:
- Не пересекается с anonymized external-ref-corpus slugs (`project-alpha`..`zeta`).
- Сразу читается как «не чей-то реальный проект».
- Stable — если в будущем external-ref-corpus slug переименуют, synthetic не затронет.

### 6. Без секретов, без реальных API-endpoint'ов, без реальных имён

Даже в роли examples. URL — `https://api.example.com`, email — `user@example.com`, даты — `2026-01-01`. Это **ещё один уровень защиты** от случайного использования чужих данных в synthetic.

---

## Структура одной fixture

```
fixtures/corpora/<profile-name>/
├── README.md                 # что моделирует, expected verdicts, scenario boundaries
├── expected.json             # frozen snapshot: doctor output, self-profile metrics, scoring sample
├── memory/
│   ├── .wal                  # events log подогнанный под профиль
│   ├── .config.sh            # CLAUDE_MEMORY_DIR=… и ENV overrides
│   ├── .stopwords            # (optional, пустой если не нужен)
│   ├── feedback/             # md файлы
│   ├── mistakes/
│   ├── knowledge/
│   ├── strategies/
│   ├── notes/
│   ├── continuity/
│   └── projects/
└── transcripts/              # optional — нужен для P1.1 evidence-match tests
    ├── session-001.jsonl
    ├── session-002.jsonl
    └── ...
```

**Важно:** `.wal` — plain-text файл 4-колоночного формата (`date|event|slug|session-id`), генерируется либо вручную (для простых fixtures), либо через `scripts/gen-fixture.sh` (для параметризованных).

`transcripts/*.jsonl` — синтетические сессии формата Claude Code: user/assistant messages в Anthropic-стандарте. Только те поля, что нужны для evidence-match (текст сообщений + session_id). Minimal viable.

---

## Пять профилей

### 1. `synthetic-cold-start` — блокирует P1.0 калибровку

**Purpose:** доказать, что на корпусе без outcome-трафика и без populated tfidf-индекса:
- Bayesian eff компонент скрыт (dormant).
- TF-IDF body-match компонент скрыт (dormant).
- Doctor печатает `active: 3/5` с явным dormant-listing.
- `measurable precision` — honest baseline от keyword + project + recency.

**Structure:**
- `memory/feedback/*.md` — 10 файлов с `keywords:`, `triggers:`, без `evidence:` (нечему match'иться).
- `memory/knowledge/*.md` — 5 файлов.
- `memory/mistakes/` — **пусто**.
- `memory/strategies/` — **пусто**.
- `.wal` — 50 событий за 14 дней: 40 `inject`, 10 `trigger-silent`, 0 `outcome-positive`, 0 `outcome-negative`, 0 `clean-session`.
- `transcripts/` — не нужны (P1.1 не тестируем).

**Expected (expected.json):**
```json
{
  "doctor": {
    "scoring_components_active": "3/5",
    "dormant": ["Bayesian (need ≥5 outcomes in 14d, have 0)",
                "TF-IDF (vocab <100)"]
  },
  "self_profile": {
    "total_sessions": 0,
    "outcome_positive": 0,
    "measurable_precision_pct": null
  },
  "scoring_sample": {
    "prompt": "debugging async timeout",
    "top_3_slugs": ["synth-async-timeout", "synth-debug-tips", "synth-pytest-fixture"],
    "bayesian_for_top": 1.0,
    "tfidf_for_top": 0.0
  }
}
```

**Why this matters:** если после имплементации Bayesian возвращает 0.5 вместо 1.0 — тест падает. Если doctor не печатает dormant-строку — падает. Если TF-IDF неожиданно набирает vocab — падает (защита от случайного включения индексации).

### 2. `synthetic-mature` — baseline для precision

**Purpose:** доказать обратную сторону cold-start: когда данных достаточно, все 5 компонентов активны, scoring работает как описано в CLAUDE.md.

**Structure:**
- `memory/mistakes/*.md` — 15 файлов с `severity`, `recurrence`, `keywords`.
- `memory/feedback/*.md` — 10 файлов с `evidence:`.
- `memory/knowledge/*.md` — 5 файлов.
- `memory/strategies/*.md` — 5 файлов с `trigger:` и `success_count`.
- `.wal` — 200+ событий: 120 `inject`, 30 `trigger-useful`, 30 `outcome-positive` (распределены по mistake-slug'ам), 15 `outcome-negative`, 5 `strategy-used`, 20 `clean-session`.
- `transcripts/` — 10 синтетических сессий с mixed evidence-match.

**Expected:**
```json
{
  "doctor": { "scoring_components_active": "5/5", "dormant": [] },
  "self_profile": {
    "total_sessions": 20,
    "outcome_positive": 30,
    "measurable_precision_pct_min": 40
  },
  "scoring_sample": {
    "prompt": "<representative prompt matching synth-mistake-01 triggers>",
    "top_1_slug": "synth-mistake-01",
    "bayesian_for_top_min": 0.60,
    "tfidf_for_top_min": 0.05
  }
}
```

**Note on `_min` fields:** у scoring может быть некоторый jitter из-за WAL-ordering. `_min` означает «не меньше», не «ровно». Это единственная fuzzy assertion в профиле — всё остальное exact. Причина: Bayesian формула содержит `log₁₀(ref_count)`, который зависит от истории инжекций.

### 3. `synthetic-feedback-heavy` — блокирует P1.1 validation

**Purpose:** доказать, что после P1.1 расширения `outcome-positive` emission работает **для feedback-файлов, не только mistakes**.

**Structure:**
- `memory/feedback/*.md` — 15 файлов, **все с `evidence:`**.
- `memory/mistakes/` — пусто.
- `memory/knowledge/` — 3 файла без evidence.
- `.wal` — в начале: 30 inject, 0 outcome-positive (до P1.1). В конце (после P1.1 прогона): должны появиться outcome-positive для feedback-слагов.
- `transcripts/session-*.jsonl` — **5 сессий**, где явно встречаются evidence-фразы из feedback'ов. Один транскрипт per slug гарантированно; один транскрипт без evidence-match (control).

**Expected (PRE-P1.1):**
```json
{
  "outcome_positive_events": 0,
  "outcome_positive_for_feedback": 0,
  "measurable_precision_pct": 0
}
```

**Expected (POST-P1.1):**
```json
{
  "outcome_positive_events": 4,
  "outcome_positive_for_feedback": 4,
  "measurable_precision_pct_min": 25,
  "feedback_slugs_with_outcome": [
    "synth-fb-01-simplest-first",
    "synth-fb-02-verify-first",
    "synth-fb-03-no-mock-db",
    "synth-fb-04-parameterized-sql"
  ]
}
```

**Critical assertion:** `outcome_positive_for_feedback` должен быть ровно тем количеством, сколько транскриптов содержат evidence-фразы. Не больше (false positive в evidence-miner), не меньше (broken regex).

**Control sub-case:** пятый транскрипт БЕЗ evidence-match → `outcome_positive` НЕ должен эмитится. Защита от «после P1.1 всё подряд считается positive».

### 4. `synthetic-multilingual` — блокирует P1.3 (TF-IDF skip-rule)

**Purpose:** после снятия tfidf skip-rule проверить, что индекс корректно покрывает кириллицу, иврит, CJK — не только латиницу.

**Structure:**
- `memory/knowledge/api-russian.md` — текст на русском с `keywords: [api, ключи, запросы]`.
- `memory/knowledge/hebrew-notes.md` — текст на иврите, `keywords: []` (проверка пустого массива + non-Latin body).
- `memory/notes/chinese-terms.md` — CJK body, `keywords` присутствуют.
- `memory/feedback/english-baseline.md` — контроль (английский + keywords).
- `.wal` — минимальный, только `inject` события.
- `transcripts/` — не нужны.

**Expected:**
```json
{
  "tfidf_index_size_bytes_min": 5000,
  "tfidf_unique_terms_min": 50,
  "tfidf_index_contains_cyrillic_term": true,
  "tfidf_index_contains_hebrew_term": true,
  "tfidf_index_contains_cjk_term": true,
  "scoring_sample": {
    "russian_prompt": "ключи API запросы",
    "top_1_slug": "api-russian"
  }
}
```

**Why Go binding:** в `internal/tfidf/tfidf.go` уже rune-семантика. Bash-fallback (awk regex) кириллицу режет. Тест должен прогоняться через Go-path и **падать**, если из-за неправильного wiring'а фикстура попала в bash-fallback.

### 5. `synthetic-edge-cases` — блокирует P1.5, P1.7

**Purpose:** robustness-тесты на frontmatter-quirks, corrupt WAL, retro closing-set logic.

**Structure:**
- `memory/feedback/empty-status.md` — файл со **пустым `status:`** (в корпусе external-ref-corpus таких 3). Doctor должен WARN.
- `memory/feedback/no-triggers.md` — файл без `triggers:` (после round 2: WARN, не FAIL).
- `memory/feedback/corrupt-yaml.md` — невалидный YAML в frontmatter (отсутствует `---` в конце). Parser должен skip, journal warn.
- `memory/knowledge/duplicate-slug-a.md` + `memory/notes/duplicate-slug-a.md` — один slug в двух dir'ах. Doctor WARN или FAIL.
- `.wal` с:
  - Строки с `NF != 4` (битая строка из-за лишнего `|`).
  - Сессия с `inject + session-metrics` (без `clean-session`) — должна считаться «закрытой» после soft-close расширения closing-set.
  - Сессия с `inject` без любого closing event — должна корректно стать `trigger-silent-retro` после retro-прогона.
  - Сессия с `inject + outcome-new` — soft close (новая mistake зафиксирована).

**Expected:**
```json
{
  "doctor": {
    "warns": ["empty status in feedback/empty-status.md",
              "feedback/no-triggers.md missing triggers (optional)",
              "duplicate slug: duplicate-slug-a"],
    "fails": []
  },
  "retro": {
    "trigger_silent_retro_emitted_for": ["session-003"],
    "trigger_silent_retro_NOT_emitted_for": ["session-001", "session-002", "session-004"]
  },
  "wal_corrupt_lines_skipped": 1
}
```

**Critical:** `session-002` содержит `session-metrics` но не `clean-session` — retro должен её считать закрытой (soft-close semantics). `session-001` содержит `outcome-new` — тоже soft-close. Это **прямой тест** P1.5.

---

## Генератор `scripts/gen-fixture.sh`

Не пишется вместе с первыми профилями. Сначала — 2-3 fixture ручками (чтобы понять шаблон), потом — generator для параметризации.

**MVP scope generator'а:**
```bash
./scripts/gen-fixture.sh \
  --name synthetic-vivid-scenario \
  --feedback-count 12 \
  --feedback-with-evidence 8 \
  --mistakes 5 \
  --outcomes-positive 15 \
  --outcomes-negative 3 \
  --strategy-count 2 \
  --days-of-wal 14 \
  --transcripts 3 \
  --transcripts-with-evidence-match 2 \
  --locale en
```

Выплёвывает `fixtures/corpora/synthetic-vivid-scenario/` с полной структурой. Expected.json генерирует **шаблон** (scaffolding), но конкретные expected значения проставляются вручную — **иначе мы генерируем и expected, и actual из одного источника**, возвращаемся к тавтологическому тесту.

**Generator не включает:**
- Многомерные сценарии (generator = плоские корпусы; сложная комбинаторика — ручками).
- Verification самого generator'а (его тестирует только применение к выходу — если fixture'ы работают, generator тоже).

---

## Test harness — как прогоняется

### Новый `make` target

```makefile
test-fixtures: build
	@for fixture in fixtures/corpora/synthetic-*/; do \
		name=$$(basename $$fixture); \
		echo "== $$name =="; \
		CLAUDE_MEMORY_DIR=$$fixture/memory \
			./hooks/test-fixture-snapshot.sh $$fixture; \
	done
```

### `hooks/test-fixture-snapshot.sh` (новый)

```bash
#!/usr/bin/env bash
# Usage: test-fixture-snapshot.sh <fixture-dir>
set -euo pipefail
FIXTURE="$1"
MEMORY="$FIXTURE/memory"
EXPECTED="$FIXTURE/expected.json"

actual=$(
  jq -n \
    --argjson doctor      "$(memoryctl doctor --json)" \
    --argjson self_profile "$(cat "$MEMORY/self-profile.md" | yq -p md)" \
    --argjson scoring_sample "$(run_scoring_sample "$FIXTURE")" \
    '{doctor: $doctor, self_profile: $self_profile, scoring_sample: $scoring_sample}'
)

diff <(jq -S . <<< "$actual") <(jq -S . "$EXPECTED") \
  || { echo "FIXTURE MISMATCH: $FIXTURE"; exit 1; }
```

**Ключ:** diff-based snapshot comparison. Любое расхождение между actual и expected — fail. Update expected → **явный commit** с сообщением `test(fixtures): update synthetic-X expected after feature Y`.

### Интеграция в CI

В `.github/workflows/test.yml` добавляется шаг после `Run Go unit tests`:

```yaml
- name: Run synthetic fixture snapshot tests
  run: make test-fixtures
```

---

## Cross-reference protocol с external-ref-corpus

**Когда:** перед merge ADR cold-start (`proposed → active`), перед merge P1.1, перед каждым v0.x tag'ом, где trogatsya scoring/retro.

**Процесс (документирован в `docs/fixtures/real-corpus-notes.md`):**

1. Прогнать `make test-fixtures` → все зелёные.
2. Прогнать тот же test harness вручную на external-ref-corpus:
   ```bash
   CLAUDE_MEMORY_DIR=~/external-ref-corpus/memory ./hooks/test-fixture-snapshot.sh ~/external-ref-corpus/
   ```
3. Сравнить verdicts:
   - Cold-start verdict (active/dormant): synthetic cold-start → dormant. external-ref-corpus → dormant. **Совпало** ✓.
   - Precision diff pre/post P1.1: synthetic feedback-heavy → 0 → 25%. external-ref-corpus → [записать замер]. Если дельты расходятся на >10 п.п. — investigate.
4. Записать в `docs/fixtures/real-corpus-notes.md`:
   ```markdown
   ## 2026-04-26 · ADR cold-start calibration

   hypomnema version: v0.14.0-rc1
   synthetic: cold-start dormant, mature active, feedback-heavy precision 0 → 28% post-P1.1.
   external-ref-corpus: cold-start dormant ✓ matched. Post-P1.1 precision shift 0.5% → 3.2% (transcripts incomplete, replayed only session-005 + session-012).
   conclusion: cold-start threshold correct. P1.1 gain lower на реальном — либо evidence-phrases в synthetic более «чистые», либо real transcripts имеют noise.
   action: добавить synthetic-noisy-transcripts профиль (edge case "evidence-phrase in unrelated context") в v0.14.
   ```

Это **метод**, не ритуал. Если расхождения нет 3 раза подряд — можно снизить частоту прогона.

---

## Maintenance protocol

### Добавить новую fixture
1. Написать `README.md` + `expected.json` (SPEC-first).
2. Собрать `memory/` и `.wal` вручную или через generator.
3. Прогнать `make test-fixtures` — fail expected (код ещё не написан).
4. Написать код.
5. `make test-fixtures` — green.
6. Commit: `test(fixtures): add synthetic-<name> for <purpose>`.

### Обновить expected после изменения scoring
1. Изменить код скоринга.
2. Прогнать `make test-fixtures` — fail (expected расходится).
3. **Глазами** сверить actual vs expected. Понять, **соответствует ли расхождение намеченному изменению**.
4. Если да — обновить expected (`make update-fixtures` helper, запускает те же команды и перезаписывает expected.json).
5. Commit: `test(fixtures): expected updated for <feature> — <2-sentence explanation of why scoring shifted>`.
6. В commit message обязательно указать, **какая fixture поменялась и чего именно ждали**.

### Удалить obsolete fixture
1. В `docs/fixtures/obsolete-fixtures.md` — одна строка: «synthetic-X удалён из-за Y на date Z».
2. `git rm -r fixtures/corpora/synthetic-X/`.
3. Убрать из Makefile target'а если был hardcoded.

---

## Rollout phases

### Phase 1 (блокирует P1.0 merge)
- `synthetic-cold-start` — ручками, ~2 часа.
- `synthetic-mature` — ручками, ~3 часа.
- `hooks/test-fixture-snapshot.sh` + Makefile target + CI step.

### Phase 2 (блокирует P1.1 merge)
- `synthetic-feedback-heavy` с transcripts — ~3 часа.
- **Тут же**: первый cross-reference с external-ref-corpus, запись в real-corpus-notes.md.

### Phase 3 (блокирует P1.3, P1.5, P1.7 merge)
- `synthetic-multilingual` — ~2 часа.
- `synthetic-edge-cases` — ~3 часа.

### Phase 4 (после v0.14, не блокирующая)
- `scripts/gen-fixture.sh` — параметризация.
- `synthetic-noisy-transcripts` или другие профили, открытые в cross-reference.

**Общий бюджет:** ~15 часов на полный набор + generator. Phases 1-3 блокируют v0.14 merge, phase 4 — follow-up.

---

## Что это решает vs не решает

### Решает
- **Privacy:** нет приватного контента вообще. Публичный репо без paranoia.
- **Reproducibility:** любой контрибьютор может прогнать `make test-fixtures`.
- **Regression:** snapshot-based diff ловит любое изменение scoring outputs.
- **SPEC-first:** fixture как формальный контракт на каждый scoring-scenario.
- **P1.1 calibration:** synthetic transcripts закрывают пробел, которого в external-ref-corpus нет.
- **Multilingual coverage:** отдельный профиль — не «как-нибудь там».
- **Edge-case coverage:** отдельный профиль с corrupt data, duplicate slugs, empty status.

### Не решает
- **Surprise-value живого корпуса.** Реальные пользователи пишут файлы в неожиданных формах, которые я (и 5 агентов) не предусмотрим в synthetic. Компенсируется cross-reference с external-ref-corpus.
- **Масштаб.** Synthetic fixtures маленькие (10-30 файлов). Live performance на 500-file корпусе тестирует `hooks/bench-memory.sh`, не эти fixtures.
- **Team scenarios.** Все synthetic — single-user. Для multi-user адопшена нужен отдельный профиль, когда/если появится реальный team-кейс.

---

## Связь с другими документами

- **Основной план v0.14** — `docs/plans/2026-04-24-v0.13-review-followup.md`. Обновляется: `fixtures/corpora/` становится synthetic-first, external-ref-corpus упоминается как «local-only calibration reference».
- **Round 2 ответ внешнему ревьюеру** — `docs/audits/2026-04-24-v0.13-response-round-2.md`. Обновляется секцией «Snapshot — не коммитим, используем локально; в репо идёт synthetic».
- **Cold-start ADR** — `docs/decisions/cold-start-scoring.md`. Tests: `fixtures/corpora/synthetic-cold-start/` + `synthetic-mature/`.
- **CI** — `.github/workflows/test.yml`. Добавляется шаг `make test-fixtures`.
- **Generator** — `scripts/gen-fixture.sh` (создаётся в phase 4).

## Open questions для synthetic design

1. **Транскрипты формата Claude Code.** Нужно подсмотреть реальный `.jsonl` из `~/.claude/projects/<project>/*.jsonl` и сделать минимальный subset. Если поля меняются в будущих версиях Claude Code — synthetic ломается. Lock-in.
2. **`_min` vs exact.** Какие поля expected допускают `_min`? Bayesian зависит от `log₁₀(ref_count)` который зависит от WAL mtime. TF-IDF — от порядка build'а. Решение: документировать каждый fuzzy-field в `expected.json` как `{"value_min": X, "_fuzzy_reason": "..."}` вместо просто числа. Иначе readers не поймут, почему one field exact а другое fuzzy.
3. **Generator deterministic?** `gen-fixture.sh` должен быть **идемпотентен** (с одним seed → одинаковый output). Иначе regeneration меняет fixture, и expected.json нужно апдейтить каждый прогон. Рандомизация через `--seed N`, default seed фиксирован.
4. **Где хранить shared fragments?** Одни и те же evidence-фразы, slug-имена, ключевые слова могут встречаться в нескольких fixtures. Дублировать (простота) vs shared `fixtures/corpora/_shared/` (DRY). Склоняюсь к дублированию — fixture должна быть self-contained, читаться без навигации в shared.
