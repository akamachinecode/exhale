# 0004. Data: SQLite + sqlc + goose

- Status: Accepted
- Date: 2026-11-05

## Context — what it has to do

- About 12 tables and 60–80 queries: CRUD plus aggregations for statistics, and the scheduler's "due and unsent" query.
- Multi-table transactions (change date → recompute → enqueue the message re-edit → save flow state).
- **Migrations run on machines we will never see.** A broken migration cannot be fixed remotely.

## Criteria

Migration safety (very high), no runtime dependencies (high), clean domain (high), ease of contribution (medium), expressive aggregations (medium), CRUD speed (low).

## Options

- **Database:** SQLite vs PostgreSQL. Postgres means an extra server to install, secure, back up and upgrade, for tiny data. SQLite is one file, and backing it up means copying it.
- **Access layer:**
  - **sqlc + goose:** hand-written SQL, compile-time checked, no runtime dependencies.
  - **ent + Atlas:** Prisma-like; generated diffs can be destructive; ent entities leak into the domain.
  - **GORM:** runtime reflection, errors only at runtime, risky automigrate.
  - **Bun / Jet:** Drizzle-like query builders.

## Decision

- **SQLite only** (WAL mode), driver `modernc.org/sqlite` (pure Go, no cgo).
- **sqlc** for queries, **goose** with **hand-written SQL migrations**.
- Migrations run automatically at startup, **after an automatic backup**. On failure the backup is restored and the previous version keeps running.
- Released migrations are immutable. Fixes go in new migrations.

## Consequences — cost we accept

- About 70 queries are written by hand.
- Rows are mapped to domain types in ⑬.
- sqlc's SQLite support has rough edges. When a query hits one, it is written by hand in the repository.

## Revisit when

The schema grows to 40+ heavily relational tables (ent would pay off), or Postgres support becomes a real requirement.

## Implementation notes: tags and the other price (2026-11-10)

Tags (free labels, many per subscription) and the "also offered at" price live in the subscription's `details` JSON column, like the notice period and the cost shares, not in a tag table. No migration was needed.

- **Why.** A household has tens of subscriptions, not thousands: filtering and counting tags in Go after loading the list is instant, and the subscription's audit snapshot (`before`/`after`) then holds its tags, so undo restores them with no extra code.
- **Cost.** Renaming or deleting a tag rewrites every subscription that has it (one audit entry each), and there is no index on tags. Tags are compared ignoring case, the first spelling wins, and commas and semicolons are refused because they separate tags in CSV and chat.
- **Revisit when** tags need their own data (colour, description) or households reach thousands of subscriptions: then a `tags` table with a join table.
- **Totals stay by category.** Tags never split or add to totals ([feature scan](../research/2026-11-10-feature-scan.md)).
- **The other price** is a `money.Money` in the subscription's currency with `month` or `year`. The yearly comparison is computed in whole minor units for monthly and yearly plans (9.99 a month is exactly 119.88 a year), not through the averaged monthly factor, which can be a few cents off. Only a price the member recorded is used; Exhale never guesses one.
