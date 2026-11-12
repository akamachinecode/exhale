# 0031. The window draws its own title bar

- Status: Accepted
- Date: 2026-11-11
- Amends: [0030](0030-desktop-window.md) (the window had the system's title bar)

## Context — what it has to do

With ADR 0030 Exhale opens in a window of its own, under the system's title bar: on Linux a GTK header with the title and a lone close button, above a page that has its own sidebar and header. The owner tried it and asked for what current desktop apps do (OpenWhispr, Perplexity, Linear): no separate title bar. The sidebar sits on a muted frame, the page sits beside it in a rounded card, and the window buttons are part of the app: minimise, maximise and close at the top right on Windows and Linux, the traffic lights at the top left on a Mac.

Physically, a window without the system's title bar still has to:

1. Move when dragged by its top, and maximise on a double click.
2. Resize from its edges.
3. Minimise, maximise or restore, and close, with buttons that show which one applies.
4. Look like a window: an edge on any desktop, and the Mac's own traffic lights and full screen.

## Criteria

| Criterion | Weight |
|---|---|
| Looks like the apps the owner pointed to, on all three systems | High |
| Moving, resizing and the buttons behave as the system's do | High |
| Nothing new to keep up: the same Wails window, the same bridge | High |
| The browser keeps working as before | Medium |

## Options

| | System title bar (ADR 0030) | **No system frame, the page draws it** | GTK header bar / Windows custom caption |
|---|---|---|---|
| Looks like the references | No: a bar above the app | Yes | Partly: native widgets in each system's style |
| Code | None | A small component, a few bridge messages | Native code per system, outside what Wails offers |
| Mac | Title bar above the sidebar | Traffic lights over the sidebar (Wails "hidden inset") | Same as the middle column |

## Decision

**On Windows and Linux the window has no system frame and the page draws the title bar; on a Mac the title bar is hidden and the traffic lights stay, over the sidebar.**

- **Window** (`internal/desktop/window`): `Frameless` on Windows and Linux, `MacTitleBarHiddenInset` on a Mac. The bridge takes three more messages, `{"type":"window","action":"minimise"|"maximise"|"close"}`, and tells the page when the window is maximised or in full screen with an `exhale:window` event.
- **Moving and resizing** (`web/src/lib/desktop.ts`): Wails moves or resizes a window when the page sends `wails:drag` or `wails:resize:<edge>` while the button is down. Its JavaScript runtime does that for pages Wails serves; Exhale's pages come from the local server, so `desktop.ts` does the same in a few lines: an area marked `data-drag` moves the window (a closer `data-no-drag` one, and buttons, links and fields, don't), a double click maximises, and on Windows and Linux the 5px band along each edge resizes it. A double click asks the window, which does what the system's title bar would: maximise on Windows and Linux; on a Mac, read "Double-click a window's title bar to" (`AppleActionOnDoubleClick`) and zoom for Fill or Zoom, minimise, or do nothing. Wails 3.1 treats Fill, the default since macOS 15, as nothing, so the window reads it itself. The window only starts moving after the pointer moves 3px, so a double click is not taken for a drag.
- **Frame** (`web/src/components/layout.tsx`): at 1024px and wider, the sidebar sits on a muted frame (`--frame`) and the page in a rounded card that scrolls on its own. This is the same in a browser: the app looks the same wherever it opens. In the window, the card starts with a 44px title band to move it by, holding the buttons on Windows and Linux. Narrower, the frame is unchanged; in the window, the header gets the buttons (or room for the traffic lights).
- **Edges**: Windows draws its own border and shadow around a window without a frame. Linux draws none, so the page adds a hairline, gone when maximised.

## Consequences — cost we accept

- **Our buttons, not the system's.** They follow the app's look, not the GNOME or Windows theme, and Windows 11's snap layouts menu does not open from our maximise button (Win+Z and dragging to the screen edges still work).
- **No shadow on Linux.** GTK gives a window without a frame no shadow; the hairline is the outline.
- **The Mac layout is checked in CI only.** The installers workflow opens the window on a real Mac, but the place of the traffic lights over the sidebar (a 52px band) is only checked by eye on a Mac.
- **The page scrolls inside its card** on wide screens, so the router resets that element's scroll on navigation too (`scrollToTopSelectors`).

## Revisit when

- People ask for the system's own buttons: Wails's `Frameless: false` brings the title bar back, the page keeps working without it.
- Wails serves its runtime to pages it does not host: drop the copy of the drag logic in `desktop.ts`.
