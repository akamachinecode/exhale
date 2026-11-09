# Open questions

Decisions still pending. Each one becomes an ADR (or a product note) once resolved.

| Topic | Notes | Blocks |
|---|---|---|
| **Claim the name** | Docker Hub namespace `exhale` is free; free domains: `exhale.cash`, `exhale.tools`, `getexhale.dev`, `exhaleapp.dev` (`exhale.dev` and `exhale.app` are for sale at Afternic) | Public launch |
| **Edit scope defaults** | Proposed: date → this charge only; amount and payment method → this and following; cancel → end of period. Always asked, the default only pre-selects | Action layer ⑥ design |
| **OAuth AS library** (ADR 0018) | zitadel/oidc vs ory/fosite vs own minimal | Milestone 3 |
| **Remote access guidance** | We design as if exposed to the internet. Docs should recommend VPN (Tailscale/WireGuard) or a reverse proxy with HTTPS | Docs |

## Roadmap

See the milestones in [ADR 0018](adr/0018-agent-ready.md#milestones-updated).
