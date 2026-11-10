# 0020. One binary for homes and teams

- Status: Accepted
- Date: 2026-11-10

## Context — what it has to do

Exhale started as a tool for a household. A small business has the same problem with more people: tools billed monthly or yearly, renewals nobody remembers, cards that expire, and nobody sure who pays for what. The question was whether a business needs a separate "server edition".

What a team physically needs that a home does not:

1. **Several people with their own accounts**, and a way to bring them in and take them out.
2. **Sign-in with the company's identity provider**, so leaving the company means losing access.
3. **The chat the team already uses** (Slack), with renewals visible to the team, not to one person.
4. **To be installed like any other service**: a package, a service definition, logs, a health check.
5. **Backups** somebody can restore.
6. **HTTPS** when it is reached from outside the office.

None of these changes what a subscription, a charge or a reminder is.

## Criteria

- Self-hosted, nothing central, no paid tier (vision, principle 1).
- One thing to build, test, document and upgrade.
- A home must not pay for what a team needs: no extra services, no extra configuration.

## Options

| Option | Verdict |
|---|---|
| **A) A separate server edition** (other binary or build flags) | Rejected. Two products to test and document, and the features in question (accounts, SSO, Slack, backups) are useful at home too. |
| **B) Multi-tenant: several households in one instance** | Rejected. It needs isolation between tenants everywhere, an admin level above households and its own failure modes, for a benefit a second instance gives for free. Already a non-goal in the vision. |
| **C) The same binary; a team is a household** | **Chosen.** |

## Decision

There is one Exhale. A team is a household: one instance, members with accounts, one data folder. What a business needs ships in the same binary and is off until configured:

- **Accounts, invites and member management** (ADR 0006): owners invite, remove and promote; removing a member passes on what they paid for.
- **Single sign-on** with any OpenID Connect provider, configured by environment; password login can be turned off.
- **Slack** as an interactive channel over Socket Mode, so no public address is needed (ADR 0007, 0019), next to Telegram, email, ntfy and webhooks.
- **Packaging**: `.deb` and `.rpm` that install a hardened systemd service with a dedicated user, a container image, an install script and a launchd agent for macOS.
- **Automatic daily backups** inside the binary, with `exhale backup` and `exhale restore`.
- **Guides** for HTTPS behind a reverse proxy and for running Exhale for a team.

The words in the product stay neutral ("Sharing", "people you share with") so they fit a family and a team.

A company with several teams that must not see each other runs one instance per team.

## Consequences — cost we accept

- No central administration across instances, no per-department permissions inside one instance beyond owner/member and private subscriptions.
- Features aimed at companies with procurement (approval flows, seat usage, vendor benchmarks) are out of scope. The [feature scan](../research/2026-11-10-feature-scan.md) lists them as not worth it for this product.
- Team features must stay invisible to a single user: every one of them is optional and has no cost when unused.

## Revisit when

- Teams ask for roles between owner and member (for example "can see everything, cannot change").
- Running one instance per team becomes the common complaint.
