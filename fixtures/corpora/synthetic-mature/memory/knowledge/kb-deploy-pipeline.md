---
type: knowledge
project: data-pipeline
created: 2026-04-13
status: active
description: data-pipeline deployment runs tests then applies migrations before rolling the application container.
keywords: [deploy, ci, migrations, containers]
domains: [devops]
---

The CI stage sequence is: lint, unit tests, integration tests, migration dry-run, build image. Deploy stage applies migrations against the target database first, then performs a rolling update of the application pods.

A migration failure aborts the deploy before any code switches. Rollback is a manual re-run of the previous image tag plus a separate migration down.

Smoke tests run against the new version before the rollout completes.
