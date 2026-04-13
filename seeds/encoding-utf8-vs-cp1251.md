---
type: mistake
seed: true
created: 2026-04-13
status: active
domains: [data]
keywords: [encoding, utf8, cp1251, windows-1251, csv, excel, кириллица]
severity: minor
recurrence: 0
root-cause: "Excel на Windows сохраняет CSV в cp1251 (Windows-1251), pandas и большинство современных тулов читают как utf-8 — кириллица превращается в мусор или UnicodeDecodeError"
prevention: "При чтении кириллических CSV из Excel — pd.read_csv(file, encoding='cp1251'); при сохранении — to_csv(..., encoding='utf-8-sig') чтобы Excel корректно открыл"
decay_rate: never
ref_count: 0
triggers:
  - "encoding cp1251"
  - "кириллица csv"
scope: domain
---

# Encoding: UTF-8 vs CP1251 в CSV

## Симптом
```python
df = pd.read_csv("data.csv")
# UnicodeDecodeError: 'utf-8' codec can't decode byte 0xcf in position 0
```

Или хуже — читается без ошибок, но кириллица превращается в `Ðåñ°ðð°ð¼` или `?????`.

## Почему
- Excel на Windows по умолчанию сохраняет CSV в **cp1251** (Windows-1251) для русской локали
- macOS-Excel может использовать **MacRoman** или **utf-8** в зависимости от версии
- Современные инструменты (pandas, awk, jq) ожидают **utf-8** по умолчанию

## Fix

**При чтении** — указать encoding:
```python
df = pd.read_csv("data.csv", encoding="cp1251")
# Или попробовать варианты:
for enc in ["utf-8", "cp1251", "utf-8-sig", "latin-1"]:
    try:
        df = pd.read_csv("data.csv", encoding=enc)
        break
    except UnicodeDecodeError:
        continue
```

**При сохранении для Excel** — utf-8 with BOM:
```python
df.to_csv("out.csv", encoding="utf-8-sig", index=False)
# utf-8-sig добавляет BOM (\ufeff), Excel по нему опознаёт кодировку
```

**Командная строка** — `iconv`:
```bash
iconv -f cp1251 -t utf-8 input.csv > output.csv
```

## Где ловится
- Аналитики, получающие выгрузки от бухгалтерии/отдела продаж
- Импорт legacy-данных из 1С, БОСС-Кадровик, других русских ERP
- Парсинг публичных датасетов (часто Росстат, ФНС в cp1251)
