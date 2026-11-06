# 0017. License: AGPL-3.0-or-later + DCO

- Status: Accepted
- Date: 2026-11-06

## Context — what it has to do

- Keep the project free. The real risk is someone improving the code and running it as a closed paid service without giving anything back.
- Not get in the way of people self-hosting it, or of contributors.
- Be compatible with every dependency. They are MIT, BSD, Apache-2.0 or ISC; simple-icons is CC0; the fonts are OFL.
- Never need changing: there is no plan to charge or to offer paid editions.

## Options

| | MIT / Apache-2.0 | GPL-3.0 (Wallos) | AGPL-3.0 |
|---|---|---|---|
| Self-hosting, private modifications | Free | Free | Free |
| Closed SaaS built on our code | Allowed | **Allowed** (GPL only triggers on distribution) | Must publish its source |
| Norm in self-hosted software | — | — | Immich, Nextcloud, Grafana, Mastodon, Plausible, Bitwarden server |

## Decision

- **AGPL-3.0-or-later.**
- **Developer Certificate of Origin**: every commit carries `Signed-off-by` (`git commit -s`).
- **No CLA.** A CLA only serves to relicense later, and it deters contributors.

## Consequences — cost we accept

Some companies avoid AGPL code. They are not our audience.

## Revisit when

We want the app embedded in commercial products. That would mean Apache-2.0, and every contributor would have to agree.
