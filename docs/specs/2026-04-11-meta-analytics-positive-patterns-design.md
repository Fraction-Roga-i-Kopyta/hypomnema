# Meta-analytics & Positive Patterns

Дизайн двух связанных фич для hypomnema: мета-аналитика эффективности injection и автоматизация захвата позитивных паттернов.

## Мотивация

Текущая система заточена на негативную обратную связь: ошибки детектятся автоматически (error-detect, outcome-negative), а успехи — нет. Scoring слепой — spaced repetition без сигнала качества это просто частотный счётчик. Strategies/ почти пуст (2 записи) потому что нет триггера для записи успехов.

Цель: дать scoring реальный quality signal и построить pipeline для автоматического накопления позитивных паттернов.

## Новые WAL-события

| Событие | Когда | Формат |
|---------|-------|--------|
| `outcome-positive` | Stop: mistake injected и не повторился | `date\|outcome-positive\|filename\|session_id` |
| `session-metrics` | Stop: метрики сессии | `date\|session-metrics\|domains\|error_count:N,tool_calls:N,duration:Ns` |
| `clean-session` | Stop: 0 errors в сессии | `date\|clean-session\|domains\|session_id` |
| `strategy-used` | Stop: strategy injected + clean session в её домене | `date\|strategy-used\|filename\|session_id` |
| `strategy-gap` | Stop: clean session без injected strategies | `date\|strategy-gap\|domains\|session_id` |

WAL остаётся append-only TSV с `|` разделителем. wal-compact.sh: outcome/metrics события не агрегируются в inject-agg (сохраняются as-is). session-metrics старше 30 дней агрегируются до monthly summary.

## Outcome detection (session-stop.sh)

### Outcome-positive для mistakes

После error detection блока:
1. Из WAL взять все `inject` события текущей сессии
2. Отфильтровать те, что соответствуют файлам из `mistakes/`
3. Исключить те, для которых есть `outcome-negative` в этой сессии
4. **Domain filter:** outcome-positive только если domains сессии пересекаются с domains mistake (предотвращает false positive когда mistake инжектирован но задача была в другом домене)
5. Оставшиеся → записать `outcome-positive` в WAL

Условия: сессия длиннее MIN_SESSION_SECONDS (120с), транскрипт существует и не пуст.

### Session metrics

Из данных, уже доступных в Stop hook:
- `error_count` — из error detection блока (переиспользуем)
- `tool_calls` — `grep -c '"tool_use"' transcript`
- `duration` — `NOW - MARKER_TIME` (уже вычисляется)

Одна строка на сессию в WAL.

### Clean session

`error_count == 0 && duration > 120s` → `clean-session` event с domains.

### Strategy-used

Если clean session + injected strategies (из WAL текущей сессии) → `strategy-used` для каждой injected strategy. Proxy для причинности, не proof.

### Strategy-gap

Если clean session + нет injected strategies + domains не пусты → `strategy-gap`. Копит сигнал для analytics.

## Scoring formula v2 (session-start.sh)

### Текущая формула WAL-компоненты

```
wal_score = spread × decay(recency) × (1 - negative_ratio)
negative_ratio = outcome-negative / total_injects
```

Проблема: запись с 0 positive и 0 negative получает множитель 1.0 (максимум). Необоснованный оптимизм.

### Новая формула

```
effectiveness = (positive_count + 1) / (positive_count + negative_count + 2)
wal_score = spread × decay(recency) × effectiveness
```

Bayesian smoothing (Laplace):
- 0 данных → effectiveness = 0.5 (нейтральный)
- Больше positive → сдвиг к 1.0
- Больше negative → сдвиг к 0.0
- Не требует минимального порога данных

### Strategy bonus

```
strategy_bonus = min(strategy_used_count × 2, 6)
```

Strategies с подтверждённым использованием (`strategy-used` в WAL) получают до +6 к score. Ставит проверенную стратегию выше непроверенного knowledge.

### Noise penalty

Если `.analytics-report` существует и свежий (< 7 дней) — записи из списка noise candidates получают `-3` к score. Не удаление — понижение приоритета. Penalty снимается при следующем analytics run если запись начнёт генерировать outcome-positive.

### Что не меняется

keyword_hits, project_boost, tfidf_bonus — работают на уровне релевантности контексту. WAL-компонента — quality signal. Ортогональны.

## Strategies pipeline

Трёхступенчатая воронка для автоматического накопления позитивных паттернов.

### Ступень 1: Автодетекция кандидатов (Stop hook)

- Clean session + injected strategies → `strategy-used` (подтверждение)
- Clean session без strategies + domains не пусты → `strategy-gap` (сигнал для analytics)

### Ступень 2: Напоминание

**PreCompact** (существующий): добавить строку про strategies в напоминание.

```
Если задача решена с первого подхода — запиши в strategies/:
trigger, steps (3-5), outcome.
```

**Stop hook**: если clean session + duration > 600s → усиленное напоминание про strategies.

### Ступень 3: Шаблон strategies/ файла

```yaml
---
type: strategy
project: global|project-name
created: YYYY-MM-DD
status: active
domains: [domain1, domain2]
keywords: [keyword1, keyword2]
trigger: "описание ситуации когда применять"
success_count: 1
---

# Название стратегии

## Steps
1. ...
2. ...
3. ...

## Outcome
Что даёт этот подход.
```

`success_count` во frontmatter — начальное значение при создании. Реальные использования трекаются через `strategy-used` в WAL. Analytics может сверять.

## Batch analytics (memory-analytics.sh)

Отдельный скрипт, не хук. Анализирует WAL за 30 дней.

### Per-file effectiveness

```
Для каждого файла в WAL:
  inject_count = count(inject | inject-agg)
  positive = count(outcome-positive)
  negative = count(outcome-negative)
  effectiveness = (positive + 1) / (positive + negative + 2)

Категории:
  winners:  effectiveness > 0.7 && inject_count >= 3
  noise:    effectiveness < 0.3 && inject_count >= 5
  unproven: inject_count >= 5 && positive == 0 && negative == 0
```

### Strategy coverage

```
Для каждого домена:
  clean_sessions = count(clean-session где domain ∈ domains)
  error_sessions = count(sessions с error_count > 0 и domain ∈ domains)
  strategy_count = count(файлов в strategies/ с этим domain)

  gap: clean_sessions > 3 && strategy_count == 0
```

### Session trends

```
  avg_error_rate за 30 дней
  clean_ratio = clean_sessions / total_sessions
  trend = последние 15д vs предыдущие 15д
```

### Выход: .analytics-report

```
date: YYYY-MM-DD
sessions: N
clean_ratio: 0.XX
trend: improving|stable|declining

## Winners
- filename (eff: X.XX, injects: N)

## Noise candidates
- filename (eff: X.XX, injects: N)

## Strategy gaps
- domain: N clean sessions, 0 strategies

## Unproven
- filename (injects: N, 0 outcomes either way)
```

### Injection

SessionStart: если `.analytics-report` существует и свежий (< 7 дней) — инжектировать только `## Noise candidates` как hint. Noise candidates получают `-3` penalty в scoring.

### Запуск

Через Stop hook: если `.analytics-report` отсутствует или старше 7 дней → `memory-analytics.sh &` в фоне. Не блокирует завершение сессии.

## Изменения в существующих файлах

### session-start.sh (~28 строк изменений)
- Scoring: заменить `(1 - negative_ratio)` на Bayesian effectiveness — подсчёт `outcome-positive` в существующем awk-проходе по WAL
- Strategy bonus: в collect_scored для strategies/ — `strategy-used` count из WAL
- Noise penalty: прочитать noise candidates из `.analytics-report`, применить `-3`

### session-stop.sh (~53 строки новых)
- Outcome-positive detection (после error detection блока)
- Session metrics (error_count, tool_calls, duration)
- Clean session event
- Strategy-used / strategy-gap events
- Analytics trigger
- Strategies reminder (clean + duration > 600s)

### memory-precompact.sh (~2 строки)
- Добавить строку про strategies в напоминание

### wal-compact.sh (~15 строк)
- Не агрегировать outcome/metrics события
- session-metrics старше 30 дней → monthly summary

### Новые файлы
- `hooks/memory-analytics.sh` (~80 строк)
- Обновить `hooks/test-memory-hooks.sh` — тесты для новых событий

### Не затрагиваются
- memory-dedup.sh
- memory-error-detect.sh
- memory-index.sh
- memory-outcome.sh

## Порядок реализации

**Фаза 1: WAL events (foundation)**
1. session-stop.sh — outcome-positive, session-metrics, clean-session
2. wal-compact.sh — обработка новых событий
3. Тесты

**Фаза 2: Scoring v2 + strategies pipeline**
4. session-start.sh — Bayesian effectiveness, strategy bonus
5. session-stop.sh — strategy-used, strategy-gap, strategies reminder
6. memory-precompact.sh — strategies prompt
7. Тесты

**Фаза 3: Analytics**
8. memory-analytics.sh — batch analysis
9. session-start.sh — noise penalty из .analytics-report
10. session-stop.sh — analytics trigger
11. Тесты

## Обратная совместимость

Все изменения аддитивные. Старый WAL без новых событий → effectiveness = 0.5 (Bayesian default). Система работает с первого дня, улучшается по мере накопления данных.

## Риски

| Риск | Вероятность | Митигация |
|------|-------------|-----------|
| Outcome-positive false positives (mistake injected, задача в другом домене) | Средняя | Domain filter: outcome-positive только при пересечении domains |
| WAL растёт быстрее (session-metrics) | Низкая | 1 строка/сессию, compact агрегирует. 5 сессий/день = 150 строк/месяц |
| Analytics report stale но инжектируется | Низкая | 7-дневный TTL + авто-rebuild в Stop hook |
| Strategies reminder annoying | Средняя | Порог: duration > 600s + clean session |
