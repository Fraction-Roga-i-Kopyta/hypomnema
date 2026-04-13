---
type: mistake
seed: true
created: 2026-04-13
status: active
domains: [api]
keywords: [api, deprecated, endpoint, silent, 200, 204, status, atlassian, side-effect]
severity: major
recurrence: 0
root-cause: "Вендоры (Atlassian, Google, Stripe) часто отключают endpoint, оставляя возврат 200/204 из соображений совместимости — но действие не выполняется; внешний код видит 'успех' и не алертит"
prevention: "После любого write-запроса к стороннему API делать read-проверку результата. Регулярно сверять используемые endpoints с changelog/deprecation-страницей вендора"
decay_rate: never
ref_count: 0
triggers:
  - "api deprecated"
  - "endpoint 200 not working"
scope: domain
---

# API: endpoint молча deprecated

## Симптом
Скрипт удаления пользователей возвращает 204 No Content на каждый вызов. Через неделю выясняется: ни один пользователь не удалён. Логи показывают чистые "успехи".

Реальные кейсы:
- Atlassian Cloud `DELETE /rest/api/3/user` — возвращает 204, но пользователь остаётся в org
- Google Sheets API v3 — продолжал отвечать после deprecation, но изменения не применялись
- Stripe API без version header — старое поведение для совместимости, новые поля игнорируются

## Почему
Вендоры боятся ломать клиентов:
- HTTP 4xx/5xx → клиент алертит → жалоба в support
- HTTP 2xx без side-effect → "тихая деградация", клиент не замечает месяцами

Это **сознательная стратегия** для уменьшения нагрузки на support, особенно у крупных вендоров.

## Fix

**Read-after-write проверка:**
```python
def delete_user(user_id):
    resp = requests.delete(f"{API}/users/{user_id}")
    assert resp.status_code in (200, 204), f"unexpected status: {resp.status_code}"

    # КРИТИЧНО: проверить, что действие реально произошло
    check = requests.get(f"{API}/users/{user_id}")
    if check.status_code != 404:
        raise Exception(f"User {user_id} still exists after delete (status {check.status_code})")
```

**Версионирование явно:**
```python
headers = {
    "Stripe-Version": "2024-11-20",
    "X-Atlassian-Token": "no-check",
    "Accept": "application/json; version=3",
}
```

**Мониторинг deprecation headers:**
```python
if "Sunset" in resp.headers or "Deprecation" in resp.headers:
    log.warning(f"endpoint deprecated: {resp.headers}")
```

## Профилактика
- Подписаться на changelog вендора (RSS/email) для используемых API
- Регулярно (раз в квартал) сверять список endpoints в коде с deprecation-страницей
- В CI добавить smoke-тест: write → read → assert effect
- Для критичных операций — проверять не только status, но и body/state

## Что особенно подвержено
- Lifecycle endpoints (delete user/org/project)
- Permission/role изменения
- Webhook регистрация
- Любые async-операции (queue submission "успешна", но job не запустился)
