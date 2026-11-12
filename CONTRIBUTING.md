# Contributing to Exhale

Thanks for helping! Exhale is a self-hosted subscription manager where the notification is the interface. Every technical decision is recorded in [docs/adr](docs/adr/README.md): read the relevant ADR before proposing a change to it.

## Setup

```sh
mise install          # Go, Node, pnpm, sqlc, golangci-lint at the pinned versions
mise run test         # Go tests with the race detector
mise run dev:api      # API on http://127.0.0.1:8484 (rebuilds on save)
mise run dev:web      # web UI with hot reload (proxies /api)
```

First run: `go run ./cmd/exhale init --owner "Your name" --currency EUR --timezone Europe/Madrid`.

## Rules

- **Tests:** the billing engine (`internal/engine`), money (`internal/money`) and the action layer (`internal/actions`) do not accept changes without tests. Every bug fix comes with a test that reproduces it. Conversation flows are golden files: run `go test ./internal/actions -update` and review the diff.
- **Agent parity:** every use case lives in `internal/app` and is listed in `app.Catalog()`. That exposes it over the REST API and MCP; parity tests fail otherwise.
- **Migrations** in `internal/store/migrations` are immutable once released. Fix forward with a new migration.
- **Generated code** (`internal/store/db`) is committed: run `mise run gen` after editing SQL.
- **Lint:** `mise run lint` must pass.
- **Documentation** is the Markdown in `docs/` (`install/`, `guides/`, `adr/`, `vision.md`), readable on GitHub as it is. The [documentation site](https://akamachinecode.github.io/exhale/docs/) is built from those same files: start with a `# Title` line and link other pages by their relative `.md` path. A new page goes into the sidebar in `docs/astro.config.mjs`. `pnpm -C docs dev` shows the site at `http://localhost:4321/exhale/docs/`, and `pnpm -C docs build` fails on a broken link.

## Commits and pull requests

- PR titles follow [Conventional Commits](https://www.conventionalcommits.org/) (`feat(engine): …`, `fix(telegram): …`, `docs: …`). PRs are squash-merged, so the title becomes the commit and the changelog entry.
- Sign off every commit (`git commit -s`): it certifies the [Developer Certificate of Origin](https://developercertificate.org/). The project is licensed under AGPL-3.0-or-later; there is no CLA.
- Keep PRs focused. Discuss larger changes in an issue or a Discussion first.
