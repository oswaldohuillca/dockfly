# Design System

## Principles
Dockfly is a panel for people who run their own infrastructure. It should feel **clean, airy and energetic**:
an icon sidebar, white cards floating over a blue-grey background, prominent figures at the top and a
brand colour (olive) that points at where to act, with a sand accent for details. It must not look like a flat, generic grey template.

The tokens live in `web/src/index.css` (CSS variables that Tailwind exposes as `bg-primary`, `text-muted-foreground`…).
No one-off colours outside of them, except for the status colours listed below.

## Colour
Neutrals with an olive tint (hue 115, low chroma), olive as the primary colour and sand as the accent. Logo: a dragonfly with container wings (`web/public/favicon.svg`).

| Token | Light | Dark | Use |
|---|---|---|---|
| `--background` | `oklch(0.972 0.008 264)` | `oklch(0.155 0.015 265)` | Page background |
| `--card` / `--popover` | `oklch(1 0 0)` | `oklch(0.2 0.018 265)` | Cards, sidebar, fields, dialogs |
| `--foreground` | `oklch(0.17 0.02 260)` | `oklch(0.97 0.005 255)` | Text |
| `--muted-foreground` | `oklch(0.5 0.02 257)` | `oklch(0.72 0.02 257)` | Secondary text (≥ 4.5:1 over `--muted`) |
| `--primary` | `oklch(0.488 0.088 124)` (#56682c) | `oklch(0.809 0.11 122)` (#b5cc7a) | Primary action, active link, focus |
| `--highlight` | `oklch(0.664 0.109 77)` (#b98a3e) | `oklch(0.842 0.067 80)` (#e2c79a) | Sand accent: charts, one figure per page, details. Never buttons or text over white (use `--highlight-foreground`) |
| `--primary-foreground` | `oklch(0.985 0.005 255)` | `oklch(0.16 0.03 262)` | Text over primary |
| `--muted` / `--secondary` | `oklch(0.955 0.012 264)` | `oklch(0.27 0.015 260)` | Tonal surfaces, hover |
| `--destructive` | `oklch(0.52 0.22 27)` | `oklch(0.704 0.191 22.216)` | Delete, errors |
| `--border` | `oklch(0.925 0.01 264)` | white 10 % | Dividers |
| `--input` | `oklch(0.87 0.014 264)` | white 16 % | Field border (deliberately soft: the field stands out through its white background) |
| `--ring` | = `--primary` | = `--primary` | Focus ring |

Status (always with text, never colour alone): success `emerald-500` for dots and `text-emerald-700 dark:text-emerald-400`
for text; warning `amber-500` / `text-amber-700 dark:text-amber-400`; failure `--destructive`.

## Typography
Geist Variable throughout; `font-mono` for technical values (internal domains, images, ports, ids).
Scale: 12 (`text-xs`, metadata and table headers) · 14 (`text-sm`, body) · 16 (`text-base`, card title) ·
18 (`text-lg`, section card title) · 24 (`text-2xl`, page title and figures). Titles in `font-semibold tracking-tight`; everything else 400/500.

## Spacing
Tailwind's scale (4 px). Within a group 4–8 px; between fields 16 px; between cards 16–20 px; card padding 24 px.
Maximum content width: `max-w-6xl`.

## Radius, shadow, border
- No radius: `--radius: 0`, every corner square (cards, buttons, fields, badges, switches, status dots and logo). Don't use `rounded-full` or fixed radii.
- Depth through a **soft shadow** (`shadow-card`), with no borders on the cards. In dark mode the shadow is replaced by
  a faint inner outline. The primary button and the active link carry a brand-tinted shadow.

## Layout
- Desktop (≥ lg): fixed 16 rem sidebar with the logo, icon navigation and the system status at the bottom.
- Below lg: logo and user at the top, navigation in a scrollable row of pills.
- Every page: `PageHeader` (title, description, primary action) → `StatGrid` with figures → content in cards.

## Motion
100–150 ms, only on hover and dialog entry. Nothing decorative that delays the task.

## Components
- **One primary button per view** (the create one, in `PageHeader`). Everything else `outline` or `ghost`; delete in `destructive`.
- **Figures**: `Stat` inside `StatGrid` (icon in a tinted square, small label, large number). Four at most.
- **Grid items** (apps, sites): floating card with an icon in a tinted square, the name and the status.
- **Loading and load failure**: `ResourceState` with `useResource`. An empty list and a failed load must not look the same.
- **Forms in a dialog**: `FormDialog` (doesn't close on an outside click or while saving).
- **Confirmations**: `useConfirm`, with a `description` when the action breaks more than the deleted item.
- **Icon-only buttons**: always with an `aria-label` that names the item (`Delete ${name}`).
- **Empty state**: icon in a `bg-primary/10 text-primary` circle, a title, one line and the create action.
- **Tables**: small, muted, uppercase header; clickable rows also carry a focusable button.

## Landing (`site/`)
A static Astro site with the same tokens (copied into `site/src/styles/global.css`). It adds `--ink` (`oklch(0.2 0.03 122)`),
an almost-black olive, for the header, the hero, the install block and the code blocks, the same in light and dark.
Over `--ink`, focus uses `--ink-foreground` and the secondary button carries a `white/25` border. The hero demo (`#demo`) reproduces
real panel screens with made-up data (`acme.dev`), always in dark mode (class `.demo`), and walks through them on a
looping script: sites, push, build, published, apps, servers. It pauses with its own button, off screen and with
`prefers-reduced-motion`. It overhangs the dark block (negative margin; the next section compensates with its top padding). A wider type scale
than the panel's (headline up to 60 px, headlines with `text-balance`). Highlighted code (Shiki, `css-variables` theme)
uses the palette: keys in sand, values in light olive, punctuation in `--ink-muted`. `shadow-card` becomes an inner outline
in dark mode, and `--ink` blocks inside a card carry a `white/10` border so they don't blend into it. Every other rule (no radius, no gradients, one primary button) still applies.

## Do / Don't
- Do: olive only on the primary action, active navigation, links and focus; sand sparingly.
- Don't: colour gradients, gradient text, one-off Tailwind colours for decoration.
- Don't: `text-*-600` for status text in light mode (it doesn't reach 4.5:1); use `-700`.
