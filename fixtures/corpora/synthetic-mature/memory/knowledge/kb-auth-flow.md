---
type: knowledge
project: blog-api
created: 2026-04-11
status: active
description: blog-api uses short-lived JWT access tokens with rotating refresh tokens stored HttpOnly.
keywords: [auth, jwt, tokens, refresh]
domains: [backend, security]
---

Access tokens are signed JWT with 15-minute expiry. Refresh tokens are opaque random bytes stored as HttpOnly SameSite=Strict cookies; each refresh rotates the token and revokes the old one.

Logout clears the refresh token server-side and empties the cookie.

Rate limits: 20 refresh attempts per hour per account.
