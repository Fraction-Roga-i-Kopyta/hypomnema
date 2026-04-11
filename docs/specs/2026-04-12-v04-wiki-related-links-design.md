# v0.4 "Вики" — Related Links & Graph

**Дата:** 2026-04-12
**Статус:** approved
**Предыдущая версия:** v0.3 (decay_rate, ref_count, weighted injection)

---

## Цель

Записи памяти усиливают и контекстуализируют друг друга через явные типизированные связи. При injection связанные записи подтягиваются кластером.

---

## 1. `related` поле в frontmatter

Односторонний плоский map. Связь декларирует более конкретная запись, ссылаясь на более общую.

```yaml
related:
  - debugging-approach: reinforces
  - css-child-first-debugging: instance_of
```

**Target** — имя файла без расширения (совпадает со slug в MEMORY.md).

**Типы связей:**

| Тип | Семантика | Пример |
|-----|-----------|--------|
| `reinforces` | Усиливает (два правила про одно) | mistake → feedback |
| `contradicts` | Противоречит (требует разрешения) | старое правило ↔ новое |
| `instance_of` | Частный случай общего правила | конкретная ошибка → общий паттерн |
| `supersedes` | Заменяет устаревшую запись | новая версия → старая |

Связи односторонние. Обратные ссылки строятся на лету при injection (полный скан ~47 файлов — миллисекунды).

---

## 2. Кластерная активация в SessionStart

Второй проход после основного scoring и отбора:

### Алгоритм

1. Собрать все `related` targets из уже отобранных файлов (прямые связи)
2. Обратный скан: пройтись по НЕотобранным файлам — если у них есть `related` на отобранные, они тоже кандидаты
3. Для каждого кандидата: загрузить только если composite score > 0 (тот же scoring что в основном проходе: keyword_hits + TF-IDF + WAL spaced repetition — хотя бы один ненулевой сигнал)
4. Cap: максимум +4 файла сверх MAX_FILES (итого до 26)

### Приоритет при cap

Если кандидатов > 4, порядок приоритета типа связи:

1. `contradicts` (всегда важно видеть конфликт)
2. `reinforces` (усиление контекста)
3. `instance_of` (конкретика)
4. `supersedes` (историческая справка)

При равном типе — сортировка по score descending.

### WAL логирование

Новый event type: `DATE|cluster-load|<filename>|<session-id>` (стандартный WAL формат)

---

## 3. Обработка `contradicts`

### При injection

Если в injection попадают две записи со связью `contradicts`:

- Обе отображаются
- Более свежая (по `referenced` дате) помечается `[ПРИОРИТЕТ]`
- Добавляется предупреждение:
  ```
  ⚠ Конфликт: X contradicts Y — проверь актуальность
  ```

### При `supersedes`

Когда запись A имеет `related: [B: supersedes]`:

- B получает `status: superseded` (не инжектится, не удаляется)
- A — единственный активный вариант
- Установка `status: superseded` — ручная (Claude при обнаружении связи)

---

## 4. Каскадные сигналы

При обновлении файла в `memory/` (перехват Write через существующий хук):

### Механизм

1. Хук проверяет: есть ли другие файлы со связью `instance_of: <обновлённый файл>`
2. Если да — добавляет в `.wal` событие: `cascade-review|<child-file>|parent:<updated-file>`
3. При следующем SessionStart, если child-файл инжектится — добавляет пометку:
   ```
   [REVIEW: parent "debugging-approach" updated 2026-04-12]
   ```

Не автоматическое изменение содержимого, только сигнал для Claude проверить актуальность.

### Срок жизни сигнала

cascade-review в WAL живёт 14 дней. После — считается обработанным (или неактуальным).

---

## 5. Изменения в файлах

| Файл | Изменение |
|------|-----------|
| `hooks/session-start.sh` | +cluster pass после основного scoring, +contradicts formatting в output, +cascade-review пометки из WAL |
| `hooks/memory-outcome.sh` | +cascade detection при Write в `memory/` — скан `instance_of` связей, запись в WAL |
| Memory `.md` файлы | +опциональное `related:` поле в frontmatter |
| `.wal` | Новые event types: `cluster-load`, `cascade-review` |

---

## 6. Начальные связи

Проставить при реализации в существующих файлах:

| Файл | Related | Тип |
|------|---------|-----|
| `mistakes/wrong-root-cause-diagnosis` | `feedback/debugging-approach` | reinforces |
| `mistakes/css-child-first-debugging` | `mistakes/wrong-root-cause-diagnosis` | instance_of |
| `mistakes/css-child-first-debugging` | `strategies/css-layout-debugging` | reinforces |
| `mistakes/jira-deprecated-api-endpoints` | `mistakes/jira-cloud-delete-user-misleading` | reinforces |
| `mistakes/docker-stale-next-cache` | `feedback/code-approach` | instance_of |

---

## 7. Метрика успеха

- При дебаге CSS — автоматически подтягиваются и mistake, и strategy, и feedback как кластер
- `contradicts` пара видна с предупреждением (когда появится)
- Обновление `debugging-approach` → cascade-review сигнал на `wrong-root-cause-diagnosis`
- Injection не раздувается: кластерный cap +4 держит бюджет

---

## 8. Совместимость

- `related` поле опциональное — файлы без него работают как раньше
- Обратная совместимость 100%: v0.3 хуки продолжают работать, cluster pass — дополнение
- Нет новых зависимостей (shell + awk + perl, как и v0.3)
