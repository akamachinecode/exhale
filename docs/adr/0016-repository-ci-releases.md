# 0016. Repository, CI and releases

- Status: Accepted
- Date: 2026-11-06

## Context — what it has to do

- One repo and one version: backend and web ship together.
- Reproducible builds, with generated code always in sync with its source.
- One-command onboarding on any OS.
- **Verifiable releases**: this app stores financial data.
- Reach homelab users where they already are.

The repository is **public from day one**, with exemplary open-source hygiene.

## Layout

```
cmd/exhale/            entry point (kong)
internal/            one package per diagram piece (see 0003); no public Go API promised
web/                 React app (Vite)
docs/                vision, ADRs, docs site (Starlight)
deploy/              Dockerfile, compose examples (incl. observability profile), Grafana dashboards
testdata/migrations/ one sample DB per released version
.github/             workflows, issue forms, PR template
mise.toml · go.mod · sqlc.yaml · .goreleaser.yaml · renovate.json · pnpm-workspace.yaml
```

There is no Nx or Turborepo: one Go module plus two JS packages (`web`, `docs`) in a pnpm workspace.

## Decision

| Piece | Choice |
|---|---|
| Task runner | **mise tasks** (`mise run dev`, `gen`, `test`, `lint`). `mise watch` rebuilds Go on save, so air is not needed. Works on Windows, unlike Make |
| Generated code | **Committed**. CI regenerates it and fails on any diff |
| Go lint | golangci-lint v2 (gosec, exhaustive…) |
| Vulnerabilities | **osv-scanner** (Go and npm in one tool) |
| CI security | Actions pinned by SHA (updated by Renovate), least-privilege permissions, **zizmor** |
| Dependency licenses | Checked in CI against our license (0017) |
| Commits | **Conventional Commits** in PR titles, **squash merge** |
| Versioning | **SemVer**. 0.x while building; **1.0 when Telegram + Slack + web are done**. Released migrations are immutable |
| Release flow | **release-please** opens a reviewable release PR with the changelog. Merging it creates the tag |
| Build | **goreleaser** |
| Targets | Linux (amd64, arm64, armv7), macOS (amd64, arm64), Windows (amd64, arm64); **.deb/.rpm with systemd unit**; Homebrew tap; multi-arch images |
| Supply-chain trust | **SBOM** + **cosign keyless signing** + **GitHub build provenance attestations** |
| Images | **GHCR** (primary) + **Docker Hub** mirror. Tags `vX.Y.Z`, `vX.Y`, `vX`, `latest`, `edge` (every merge to main) |
| Docs site | **Starlight** on GitHub Pages, with an API reference from OpenAPI and the ADRs published |
| Branching | Trunk-based. `main` protected, required checks |

## CI

| When | What |
|---|---|
| Every PR | Go lint + Biome, typecheck, `go test -race` on Linux/macOS/Windows, web tests, flagship E2E, migration upgrade, generated-code diff, osv-scanner, license check, size-limit, build (no publish) |
| Merge to main | `edge` image |
| Release PR merged | goreleaser: binaries, packages, images, signatures, SBOM, attestations, Homebrew |

## Community files (day one)

- README (diagram, screenshots, "try it in 1 minute").
- CONTRIBUTING (`mise install && mise run dev`, test rules, ADRs).
- SECURITY.md (GitHub private vulnerability reporting).
- CODE_OF_CONDUCT (Contributor Covenant).
- Issue forms: the bug form asks for `exhale doctor` output.
- PR template, labels, GitHub Discussions.

## Later

Homelab app stores (Unraid, CasaOS, Umbrel, TrueNAS), winget, Scoop.

## Revisit when

A PR takes more than about 10 minutes in CI. E2E and the macOS/Windows test runs would then move to merge-only.
