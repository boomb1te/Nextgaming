# NextGaming admin panel — design system

Source of truth for the panel redesign (provided by Boom, 2026-10-13). The
mockup lives in a Claude Design canvas; the real panel is `internal/server/web`
(`app.css` / `app.js`). Apply these tokens when porting the redesign in.

## Themes

Dark is the **default**; light is a daytime option (a theme toggle).

### Dark theme (default)
| Role | Hex |
|---|---|
| Background | `#121417` |
| Surface (cards) | `#1A1D21` |
| Raised / hover | `#22262B` |
| Border | `#2E333A` |
| Text primary | `#E6E8EB` |
| Text secondary | `#9BA3AE` |
| Accent | `#5B8DEF` |

### Light theme (daytime option)
| Role | Hex |
|---|---|
| Background | `#F6F7F9` |
| Surface (cards) | `#FFFFFF` |
| Border | `#D0D7DE` |
| Text primary | `#1F2328` |
| Text secondary | `#59636E` |
| Accent | `#2F6FDB` |

## PC status colors
| Status | Hex | |
|---|---|---|
| Free | `#3FB950` | green |
| In session | `#5B8DEF` | blue |
| Ending soon (<10 min) | `#D29922` | amber |
| Offline | `#6E7681` | gray |
| Problem / error | `#F85149` | red |

## Fonts
- Main: **Inter**
- Alternative: IBM Plex Sans
- Monospace (IP, MAC, logs): **JetBrains Mono**
- Numbers (timers, money): `font-variant-numeric: tabular-nums`

## Sizes
- Body text: 14–15px
- PC names & timers: 16–18px
- Line height: 1.5
- Weights: 400 regular, 500/600 medium

## Rules
- No pure black (`#000`) or pure white (`#FFF`).
- Status colors only on small badges, dots or borders — **never whole tiles**.
- Always pair a status color with a text label or icon.
- Text contrast ≥ 4.5:1.
- Minimal animation; flash once, never continuously.

## Navigation (grouped sidebar, replaces the flat tab row)
- **Manage** → PCs, Screens, File sharing, Activity log
- **Clients** → Basic clients, Authorized clients
- **Bookings** (single item)
- **Finance** → Shift & cash, Reports
- **Settings** — pinned bottom-left, above the account chip.
- Active item: raised background (`#22262B`) + a 3px accent left bar.

## Extra status mappings (beyond the 5 defined colors)
The 5 PC status colors don't cover these; agreed mappings:
- **Paused** → gray (idle), with a pause icon + "PAUSED" label.
- **Checkout** (player wants to pay) → amber + amber card border (red is reserved
  for real problems/errors).
- **Staff** (maintenance / free unlock) → gray badge.

## Settings owner-gate
Settings is **owner-only**. When a non-owner operator opens it, a prompt asks for
the owner account's password; on success Settings opens for that visit. (Scope:
all of Settings for now — revisit if some parts should be staff-accessible.)
