# 0014. Visual layer: shadcn/ui, Tailwind, Recharts, own calendar, i18n

- Status: Accepted
- Date: 2026-11-06

## Decision

| Piece | Choice | Why |
|---|---|---|
| Components | **shadcn/ui on Base UI** | The code lives in our repo, with our own look. Base UI is built by Radix's original authors and is very active (v1.9, Oct 2026) |
| Styling | **Tailwind v4** + CSS-variable tokens | Light/dark mode and a **user-selectable accent colour** |
| Charts | **Recharts v3** (shadcn charts) + an **own trace waterfall** (CSS bars, Jaeger-style) + an **own latency heatmap** (SVG grid) | One theme everywhere. ECharts rejected (~300 KB, its own aesthetics) |
| Chart data | **Downsampled in the backend**: 1-minute buckets over 6 h, 15-minute over 7 days, 1 h over 30 days | At most ~700 points per series |
| Calendar | **Own**: `date-fns` v4 for the grid, `dnd-kit` for drag | **Dragging a charge = "change date, this charge only"**. FullCalendar rejected (heavy, built for timed events) |
| Date picker | `react-day-picker` v9 (shadcn Calendar) | Already integrated with shadcn |
| Tables | TanStack Table (+ TanStack Virtual for the event log only) | Headless, sortable and filterable; virtualization only where there are thousands of rows |
| Icons, toasts, ⌘K | lucide-react, sonner, cmdk | The shadcn defaults |
| Motion | CSS transitions + `tw-animate-css` | Subtle and adds no weight. `motion` only if an interaction needs it |
| Fonts | **Self-hosted** with `@fontsource-variable` (Inter, Geist Mono) | A self-hosted app must not call Google Fonts, for privacy and for offline use |

## Service logos

1. **simple-icons** (CC0, ~3,000 brands) embedded in the binary: instant, offline, and nothing is leaked to third parties.
2. Coloured monogram.
3. User upload.
4. Web search **only if the user enables it**.

## Internationalization

| | Web | Server (bot and email messages) |
|---|---|---|
| Library | **react-i18next** | **nicksnyder/go-i18n** v2 |
| Format | i18next JSON v4 | go-i18n JSON v2 |
| Plural rules | CLDR | CLDR |

Both formats are natively supported by **Weblate**, which is free for libre projects. Locales are loaded lazily. Lingui and Paraglide were rejected: they are more elegant, but fewer translators know them and sharing a workflow with Go would be harder.

## Revisit when

The observability dashboard needs hundreds of thousands of real-time points. That page alone would then switch to uPlot or ECharts.
