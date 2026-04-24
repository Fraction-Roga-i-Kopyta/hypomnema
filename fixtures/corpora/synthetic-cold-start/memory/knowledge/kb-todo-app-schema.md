---
type: knowledge
project: todo-app
created: 2026-04-10
status: active
description: todo-app persists state as an append-only event log at /data/events.log.
keywords: [todo-app, event-sourcing]
---

Events are JSONL. Never rewrite history; always append.
