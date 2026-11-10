# Architecture Decision Records

Every significant technical decision is recorded here, so that anyone exploring or contributing to the project understands **why** it is built the way it is.

## How we decide

Every decision is evaluated from first principles, always with the same structure:

1. **What it physically has to do** in *this* app.
2. **Criteria** that actually matter here, and how much each one weighs.
3. **Options** compared against those criteria (with evidence: maintenance status, dependencies, measurements).
4. **Verdict**, the **cost we accept**, and **what would make us revisit it**.

Accepted ADRs are not rewritten. A decision that changes gets a new ADR that supersedes the old one.

## Index

| # | Decision | Status |
|---|---|---|
| [0001](0001-distribution-model.md) | Distribution: one self-hosted server, Docker image + native service, no cloud | Accepted |
| [0002](0002-backend-language-go.md) | Backend language: Go | Accepted |
| [0003](0003-architecture.md) | Architecture: one process, channel-agnostic action layer | Accepted |
| [0004](0004-data-layer.md) | Data: SQLite + sqlc + goose | Accepted |
| [0005](0005-api-contract.md) | API contract: huma v2, code-first OpenAPI 3.1 | Accepted |
| [0006](0006-users-and-auth.md) | Users and authentication: household model, composed libraries, OIDC in v1 | Accepted |
| [0007](0007-channels.md) | Channels: go-telegram/bot, slack-go, go-mail | Accepted |
| [0008](0008-time-and-money.md) | Time and money: own billing engine, bojanz/currency, ECB rates | Accepted |
| [0009](0009-runtime-and-system.md) | Runtime and system: kong, suture, slog, kardianos/service | Accepted |
| [0010](0010-testing-strategy.md) | Testing strategy | Accepted |
| [0011](0011-observability.md) | Observability module | Accepted |
| [0012](0012-frontend-framework.md) | Frontend framework: React 19 + Vite SPA embedded in the binary | Accepted |
| [0013](0013-web-data-layer.md) | Web data layer: orval, TanStack Router/Query/Form, zod | Accepted |
| [0014](0014-visual-layer.md) | Visual layer: shadcn/ui, Tailwind, Recharts, own calendar, i18n | Accepted |
| [0015](0015-frontend-tooling.md) | Frontend tooling: mise, pnpm, TypeScript 7, Biome, Vitest, Playwright | Accepted |
| [0016](0016-repository-ci-releases.md) | Repository, CI and releases | Accepted |
| [0017](0017-license.md) | License: AGPL-3.0-or-later + DCO | Accepted |
| [0018](0018-agent-ready.md) | Agent-ready app: AI is a first-class feature of v1 | Accepted |
| [0019](0019-the-message-is-the-app.md) | The message is the app: one screen format, a living home message in chat | Accepted |
| [0020](0020-one-binary-for-homes-and-teams.md) | One binary for homes and teams | Accepted |
| [0021](0021-dates-that-matter.md) | The dates that matter: trials, notice periods and expiries | Accepted |
| [0022](0022-data-in-and-out.md) | Data in and out: import with a review list, export, calendar feed | Accepted |
| [0023](0023-message-translations.md) | Translating chat messages | Accepted |
| [0025](0025-two-factor-sign-in.md) | Two-factor sign-in: TOTP and backup codes, standard library, QR in the browser | Accepted |
| [0027](0027-logos-and-documents.md) | Own logos and documents: stored in the database, served as inert files | Accepted |

Open questions are tracked in [../open-questions.md](../open-questions.md).

## Template

```markdown
# NNNN. Title

- Status: Proposed | Accepted | Superseded by NNNN
- Date: YYYY-MM-DD

## Context — what it has to do
## Criteria
## Options
## Decision
## Consequences — cost we accept
## Revisit when
```
