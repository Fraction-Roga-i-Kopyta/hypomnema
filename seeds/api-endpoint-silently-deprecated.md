---
type: mistake
seed: true
created: 2026-04-13
status: active
domains: [api]
keywords: [api, deprecated, endpoint, silent, 200, 204, status, atlassian, side-effect]
severity: major
recurrence: 0
root-cause: "Vendors (Atlassian, Google, Stripe) often disable an endpoint while keeping 200/204 responses for backwards compatibility — the action no longer happens but the client code sees 'success' and never alerts"
prevention: "After any write request to a third-party API, do a read-check that the change actually happened. Regularly reconcile the endpoints you use against the vendor's changelog / deprecation page"
decay_rate: never
ref_count: 0
triggers:
  - "api deprecated"
  - "endpoint 200 not working"
scope: domain
---

# API: endpoint silently deprecated

## Symptom
The user-deletion script returns 204 No Content on every call. A week later you discover: not a single user was actually deleted. The logs show clean "successes".

Real cases:
- Atlassian Cloud `DELETE /rest/api/3/user` — returns 204, but the user is still in the org.
- Google Sheets API v3 — kept responding after deprecation, but changes weren't applied.
- Stripe API without a version header — old behaviour for compatibility; new fields get ignored.

## Why
Vendors are afraid of breaking clients:
- HTTP 4xx/5xx → client alerts → support ticket.
- HTTP 2xx without side effect → "silent degradation", client doesn't notice for months.

This is a **deliberate strategy** to reduce support load, especially at larger vendors.

## Fix

**Read-after-write verification:**
```python
def delete_user(user_id):
    resp = requests.delete(f"{API}/users/{user_id}")
    assert resp.status_code in (200, 204), f"unexpected status: {resp.status_code}"

    # CRITICAL: confirm the action actually took effect
    check = requests.get(f"{API}/users/{user_id}")
    if check.status_code != 404:
        raise Exception(f"User {user_id} still exists after delete (status {check.status_code})")
```

**Explicit versioning:**
```python
headers = {
    "Stripe-Version": "2024-11-20",
    "X-Atlassian-Token": "no-check",
    "Accept": "application/json; version=3",
}
```

**Watch for deprecation headers:**
```python
if "Sunset" in resp.headers or "Deprecation" in resp.headers:
    log.warning(f"endpoint deprecated: {resp.headers}")
```

## Prevention
- Subscribe to the vendor's changelog (RSS/email) for every API you depend on.
- Once a quarter, reconcile your code's endpoint list with the vendor's deprecation page.
- In CI, add a smoke test: write → read → assert effect.
- For critical operations, check not just status but the resulting body/state.

## Especially at risk
- Lifecycle endpoints (delete user/org/project).
- Permission / role changes.
- Webhook registration.
- Any async operation (queue submission "succeeds", but the job never runs).
