# 0012. Frontend framework: React 19 + Vite SPA embedded in the binary

- Status: Accepted
- Date: 2026-11-06

## Context — what it has to do

- Compile to **static files served by the Go binary**: no Node on the user's machine.
- A highly interactive, data-dense UI: tables, rich forms, calendar, charts, the observability dashboard, live updates, PWA, i18n, accessibility, dark mode.
- A strong component ecosystem, orval support, and an appeal to contributors.

## Options

React 19 + Vite, Svelte 5, SolidJS, Vue 3, Next.js.

## Decision

**React 19 + Vite in SPA mode, with React Compiler.**

- Node is used **only at development and build time**.
- `vite build` → `web/dist` → `//go:embed` → a single binary. Unknown routes fall back to `index.html`.
- This is the same pattern used by Grafana, Memos, Gitea, PocketBase and Syncthing.

**Next.js is rejected on first principles.** Its value (SSR, server components, API routes) needs a Node runtime, and here the server is Go.

## Development workflow

Two processes: Vite (HMR, proxies `/api`) and the Go server (rebuilt on save). Both are exposed as mise tasks: `mise run dev:web`, `mise run dev:api`.

## Consequences — cost we accept

A larger bundle than Svelte. This barely matters on a LAN with a service worker cache.

## Revisit when

Bundle size hurts the PWA on mobile data, or Svelte's chart and calendar ecosystem catches up.
