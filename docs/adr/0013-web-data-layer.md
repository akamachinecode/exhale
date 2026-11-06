# 0013. Web data layer: orval, TanStack Router/Query/Form, zod

- Status: Accepted
- Date: 2026-11-06

## Decision

| Piece | Choice | Why |
|---|---|---|
| Generated client | **orval**, using native `fetch` | One config generates types, the client, TanStack Query hooks, **zod schemas** and **MSW mocks** |
| Router | **TanStack Router** | **Typed, validated search params**, so filters and calendar month live in the URL. Deep links from reminders ("Open in web" → `/subscriptions/123`) |
| Server state | **TanStack Query** v5 | Cache, retries, optimistic updates, invalidation |
| Forms | **TanStack Form** | See below |
| Validation | **zod v4**, generated from OpenAPI | Rules are written once, in the Go structs |
| Global state | **None** | State lives in `useState`, the URL and the Query cache |

## Why not react-hook-form

It relies on mutable refs and hidden subscriptions that **conflict with React Compiler**. Its repository had several 2026 issues about stale state with the compiler, and an open v7.91 regression (Nov 2026). TanStack Form uses explicit store selectors and infers better types for nested dynamic fields, such as the recurrence builder.

- **Confidence:** medium-high.
- **Plan B:** react-hook-form with `"use no memo"` in the form components.

## Design rules

1. **The web never computes charge dates.** While editing a recurrence, it calls `POST /subscriptions/preview` (debounced). The engine ⑨ is the single source of truth.
2. **SSE feeds the cache.** One `EventSource` per tab on `/api/events`. Events invalidate exactly the affected query keys. **On reconnect, refetch everything**, because events may have been missed.
3. **Server errors land on their field.** huma returns RFC 9457 problem details with locations (`body.amount`), and a small helper maps them onto form fields.

## Revisit when

orval lags on OpenAPI 3.1 (hey-api is the natural successor), or TanStack Form blocks us with the compiler.
