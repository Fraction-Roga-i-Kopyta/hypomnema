---
type: knowledge
project: blog-api
created: 2026-04-12
status: active
description: blog-api persists posts, comments, users, and audit events in Postgres with a single migration chain.
keywords: [postgres, schema, migrations, audit]
domains: [backend, database]
---

Core tables: users, posts, comments, tags, post_tags (join), audit_events. Foreign keys cascade on delete for posts → comments but restrict on users → posts.

Audit events are append-only with a partial index on recent rows. Migrations live in alembic and always pair up/down.

Every schema change requires a migration PR reviewed separately from application code.
