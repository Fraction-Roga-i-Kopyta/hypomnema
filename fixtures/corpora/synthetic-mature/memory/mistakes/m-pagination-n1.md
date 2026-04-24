---
type: mistake
project: blog-api
created: 2026-04-16
status: active
severity: minor
recurrence: 4
scope: domain
root-cause: "List endpoint fetched related records per row in a loop, producing N+1 queries that killed latency on large pages."
prevention: "Use the ORM's eager-load / prefetch API (select_related / joinedload) on any list endpoint."
keywords: [n+1, orm, pagination, performance]
domains: [backend, performance]
triggers: ["n+1 queries", "pagination slow"]
---

The posts index endpoint loaded `post.author` inside a template loop, issuing one query per post. At 50 posts per page it was 51 queries per request.

Fix: `joinedload(Post.author)` on the query — one query, same data.
