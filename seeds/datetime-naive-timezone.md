---
type: mistake
seed: true
created: 2026-04-13
status: active
domains: [code, data]
keywords: [datetime, timezone, naive, utc, pytz, tz-aware]
severity: major
recurrence: 0
root-cause: "datetime.now() без timezone возвращает naive объект, который сравнивается с aware datetime через TypeError или (хуже) интерпретируется в локальной зоне сервера"
prevention: "Всегда использовать datetime.now(tz=timezone.utc) или datetime.now(ZoneInfo('Europe/Moscow')); хранить в БД только UTC"
decay_rate: never
ref_count: 0
triggers:
  - "datetime timezone"
  - "naive datetime"
scope: domain
---

# Datetime: naive vs aware и таймзоны

## Симптом
Отчёт за "вчера" показывает данные за два дня. Cron срабатывает на час раньше после перехода на летнее время. Сравнение дат падает:
```python
TypeError: can't compare offset-naive and offset-aware datetimes
```

## Почему
Python различает два типа datetime:
- **Naive** (`datetime.now()`) — без зоны, "просто число". Что оно значит — зависит от того, кто читает.
- **Aware** (`datetime.now(tz=...)`) — с зоной, однозначно.

Naive datetime, сохранённый на сервере в UTC и прочитанный в Москве — сдвинется на 3 часа без предупреждения.

## Fix
```python
from datetime import datetime, timezone
from zoneinfo import ZoneInfo  # Python 3.9+

# Всегда явно указывать зону
now_utc = datetime.now(tz=timezone.utc)
now_msk = datetime.now(ZoneInfo("Europe/Moscow"))

# В БД хранить UTC, конвертировать на отображение
db_value = some_aware_dt.astimezone(timezone.utc)
display = db_value.astimezone(ZoneInfo("Europe/Moscow"))
```

## Где ловится
- Аналитика: отчёты "за день" с границей в полночь
- Cron-задачи на серверах в разных регионах
- API, которое отдаёт `created_at` без зоны — клиент гадает, что это
- SQL: `TIMESTAMP WITHOUT TIME ZONE` в Postgres ведёт себя как naive
