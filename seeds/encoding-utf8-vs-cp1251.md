---
type: mistake
seed: true
created: 2026-04-13
status: active
domains: [data]
keywords: [encoding, utf8, cp1251, windows-1251, csv, excel, cyrillic]
severity: minor
recurrence: 0
root-cause: "Excel on Windows saves CSV in cp1251 (Windows-1251) for Cyrillic locales. pandas and most modern tools read as utf-8 by default — non-Latin text either decodes as garbage or raises UnicodeDecodeError"
prevention: "When reading Cyrillic CSV from Excel, pass encoding='cp1251'. When writing for Excel, use to_csv(..., encoding='utf-8-sig') so Excel detects the BOM and opens it correctly"
decay_rate: never
ref_count: 0
triggers:
  - "encoding cp1251"
  - "cyrillic csv"
  - "unicodedecodeerror"
scope: domain
---

# Encoding: UTF-8 vs CP1251 in CSV

## Symptom
```python
df = pd.read_csv("data.csv")
# UnicodeDecodeError: 'utf-8' codec can't decode byte 0xcf in position 0
```

Or worse — no error, but Cyrillic text turns into `Ðåñ°ðð°ð¼` or `?????`.

## Why
- Excel on Windows defaults to **cp1251** (Windows-1251) for CSV in Russian locales.
- Excel on macOS might use **MacRoman** or **utf-8** depending on version.
- Modern tools (pandas, awk, jq) expect **utf-8** by default.

## Fix

**On read** — specify the encoding:
```python
df = pd.read_csv("data.csv", encoding="cp1251")
# Or try a set of common variants:
for enc in ["utf-8", "cp1251", "utf-8-sig", "latin-1"]:
    try:
        df = pd.read_csv("data.csv", encoding=enc)
        break
    except UnicodeDecodeError:
        continue
```

**On write for Excel** — utf-8 with BOM:
```python
df.to_csv("out.csv", encoding="utf-8-sig", index=False)
# utf-8-sig prepends ﻿; Excel detects it and picks the right codec
```

**Command line** — `iconv`:
```bash
iconv -f cp1251 -t utf-8 input.csv > output.csv
```

## Where it shows up
- Analysts receiving exports from accounting / sales.
- Importing legacy data from ERPs on Windows (1C, BOSS-Kadrovik, and similar).
- Parsing public datasets that ship cp1251 (some government agencies).
