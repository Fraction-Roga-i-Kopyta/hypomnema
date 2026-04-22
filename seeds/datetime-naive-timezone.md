---
type: mistake
seed: true
created: 2026-04-13
status: active
domains: [code, data]
keywords: [datetime, timezone, naive, utc, pytz, tz-aware]
severity: major
recurrence: 0
root-cause: "datetime.now() without a timezone returns a naive object. Comparing it against an aware datetime either raises TypeError or — worse — gets silently interpreted in the server's local zone"
prevention: "Always use datetime.now(tz=timezone.utc) or datetime.now(ZoneInfo('Europe/...')). Store UTC only in the database; convert at display time"
decay_rate: never
ref_count: 0
triggers:
  - "datetime timezone"
  - "naive datetime"
scope: domain
---

# Datetime: naive vs aware and time zones

## Symptom
A report "for yesterday" shows data from two days. A cron job fires an hour early after DST transitions. Date comparison crashes:
```python
TypeError: can't compare offset-naive and offset-aware datetimes
```

## Why
Python distinguishes two kinds of datetime:
- **Naive** (`datetime.now()`) — no zone, "just a number". What it means depends on who reads it.
- **Aware** (`datetime.now(tz=...)`) — has a zone, unambiguous.

A naive datetime written on a UTC server and read in Moscow silently shifts by 3 hours, with no error.

## Fix
```python
from datetime import datetime, timezone
from zoneinfo import ZoneInfo  # Python 3.9+

# Always set a zone explicitly
now_utc = datetime.now(tz=timezone.utc)
now_msk = datetime.now(ZoneInfo("Europe/Moscow"))

# Store UTC in the DB, convert on display
db_value = some_aware_dt.astimezone(timezone.utc)
display = db_value.astimezone(ZoneInfo("Europe/Moscow"))
```

## Where it bites
- Analytics: daily reports with midnight cutoffs.
- Cron jobs on servers in different regions.
- APIs that return `created_at` without a zone — clients guess.
- SQL: Postgres `TIMESTAMP WITHOUT TIME ZONE` behaves as naive.
