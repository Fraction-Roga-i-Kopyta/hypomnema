---
type: mistake
seed: true
created: 2026-04-13
status: active
domains: [research, writing]
keywords: [source, url, link rot, archive, citation, research]
severity: major
recurrence: 0
root-cause: "URL без локальной копии — не источник, а обещание источника. Сайты редизайнятся, статьи удаляются, домены умирают; через 1-3 года значительная часть ссылок становится 404"
prevention: "При сохранении ссылки — сразу же сохранять локальную копию (PDF/screenshot/markdown) ИЛИ архивировать через web.archive.org. Хранить и URL, и дату доступа, и снапшот"
decay_rate: never
ref_count: 0
triggers:
  - "link rot"
  - "источник недоступен"
scope: domain
---

# Link rot: URL без локальной копии

## Симптом
Через год возвращаешься к своей заметке/статье/исследованию. Ключевая ссылка ведёт на:
- 404 Not Found
- Редирект на главную сайта (статья перенесена/удалена)
- Совершенно другой контент (домен перепродан)
- Paywall, которого раньше не было
- Cloudflare-блок по геолокации

## Почему
- **Половина ссылок умирает за 5-7 лет** (исследования по link rot в академии).
- Новостные сайты убирают статьи через год.
- Блоги мигрируют на другие платформы и теряют permalinks.
- Twitter/X удалил доступ к старым постам без логина.
- Reddit потерял часть старых тредов после API-изменений 2023.

## Fix

**Минимум** — дата доступа в цитировании:
```markdown
[Статья про X](https://example.com/article) (доступ: 2026-04-13)
```

**Базово** — архив через Wayback Machine:
```bash
curl -s "https://web.archive.org/save/https://example.com/article"
# или: https://archive.ph/https://example.com/article
```
И в заметке хранить и оригинал, и архивную ссылку.

**Надёжно** — локальная копия:
```bash
# Markdown через pandoc или readability
curl -s URL | pandoc -f html -t markdown -o saved.md

# Полная страница с ассетами
monolith URL -o saved.html

# PDF через headless Chrome
chromium --headless --disable-gpu --print-to-pdf=saved.pdf URL
```

**Систематично** — Zotero/Obsidian Web Clipper/Readwise: при сохранении ссылки автоматически тянут локальную копию.

## Где особенно критично
- Академические работы и эссе (рецензент проверяет источники)
- Юридические/медицинские референсы
- Дизайн-references (Pinterest-доски умирают целиком)
- Технические how-to, привязанные к конкретной версии тула
