---
type: mistake
project: blog-api
created: 2026-04-14
status: active
severity: major
recurrence: 3
scope: domain
root-cause: "CORS wildcard origin combined with credentials allowed cross-site cookie leaks."
prevention: "Explicit allow-list of origins when credentials are enabled; never combine * with allow-credentials."
keywords: [cors, security, headers, cookies]
domains: [backend, security]
triggers: ["cors with credentials", "wildcard origin credentials"]
---

The API returned `Access-Control-Allow-Origin: *` together with `Access-Control-Allow-Credentials: true`. Browsers reject that combo silently in newer versions, but older ones exposed auth cookies to any origin.

Fix: explicit allow-list keyed on environment, plus a smoke test that refuses to deploy if wildcard + credentials ever coexist.
