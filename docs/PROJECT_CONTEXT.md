# NextGaming — project context

Read this with `CLAUDE.md` (the engineering contract: architecture, the rules
that must not break, where to add things). This file is the product + status
context: what NextGaming is, what's built, what's next, and how to work on it.

## What it is

A gaming-club management system for a real club in **Turkmenistan** (owned by
the maintainer's brother) — a self-hosted replacement for **PanCafe /
SmartShell**. It runs entirely on the club's **LAN**, no cloud. Money is in
**TMT** (Turkmen manat), `core.Money` = int64 minor units.

Three Windows programs plus a browser admin panel:

- **Server** (`cmd/nextgaming-server`) — the single source of truth. Holds the
  SQLite database, talks to every gaming PC, and serves the admin panel. Runs on
  the front-desk PC (also builds + runs natively on macOS/Linux).
- **Agent** (`cmd/nextgaming-agent`) — a Windows service on each gaming PC.
  Connects to the server, enforces lock/unlock, power, Wake-on-LAN, screen
  capture, file sharing, and now the desktop wallpaper.
- **Lock screen / client** (`cmd/nextgaming-client`) — the full-screen app the
  player sees when a PC is locked; also the screen-capture and remote-control
  helper. Fails closed (locks when in doubt).
- **Admin panel** (`internal/server/web`) — single-page vanilla JS, no build
  step, embedded into the server binary with `//go:embed`.

Audience: front-desk staff (start/stop sessions, take money, bookings) and the
owner (settings, reports, staff accounts).

## Current state

**Version 1.33.0.** Built and shipping. Everything compiles with no CGO, deps
vendored, and vets for both native and `GOOS=windows`. The full test suite
passes. Windows-only GDI code (lock screen rendering, wallpaper) compiles and
vets but should be smoke-tested on one real PC before rollout.

### What's built
- **Sessions & billing**: prepaid (fixed time / packages) and open (pay at the
  end). Open is the default. Grace period + minimum charge for open sessions.
  Pause/resume (paused time never billed). Add time. Settle & end with
  refund/collect. Money table is append-only.
- **PCs dashboard**: live tiles, count pills, status pills, checkout state,
  offline. Double-click a free PC to start an open session; double-click an
  in-session PC to settle & end. Multi-select + bulk actions.
- **Clients & members**: clients (name + unique Turkmen phone). A client becomes
  an **authorized member** with a system-generated one-time password stored as a
  PBKDF2-HMAC-SHA256 hash (stdlib, not bcrypt). Authorize / reset / deauthorize.
  Hours played are **derived** from sessions, never stored. Ratings deferred.
- **Bookings**: schedule PCs, auto-start, no-show, cancel, and **delete**
  (removes the reservation; running sessions keep going).
- **Screens**: live monitoring (capture broadcast only while an admin watches),
  one-off HD snapshot, and **remote control** (mouse + keyboard; blocks the
  player's input while controlling).
- **File sharing (LAN only)**: browse the front desk's filesystem, each PC
  exposes one shared folder, copy files either way, multi-select.
- **Incoming-call booking (LAN only)**: operator phone (MacroDroid) POSTs the
  caller's number; matched to a client by phone and turned into a pre-filled
  booking.
- **Wake-on-LAN**: per-PC check/fix of Windows settings, real wake test, BIOS
  guidance.
- **Appearance** (pushed to PCs over the agent channel): desktop wallpaper,
  lock-screen background image, and lock-screen PC-name position
  (center / top-left). Lock-screen low-time warning is a small top-left toast.
- **Admin panel redesign**: a dark design system (`docs/design-system.md`),
  Inter + JetBrains Mono bundled locally, grouped pinned sidebar, reusable
  dropdown/select/combobox primitives, segmented controls, searchable client
  picker.
- **Installer safety**: the agent install verifies the server + club key before
  doing anything, so a mistyped key can't produce an invisible locked PC.
- **Shifts, reports (basic), activity log, staff accounts, import/export,
  backups.**

### Open / next up
- **Reports**: still basic. Planned: a proper stats dashboard — revenue by
  day/week/month, busiest-hours heatmap, PC utilization/occupancy, average
  session length, prepaid-vs-open split, revenue by zone, top clients. Pick the
  metrics, then build it with the design system inside the Reports tab.
- **Take control**: confirm whether the reported "doesn't work" was the button
  greyed out on a free/locked PC (by design) or dead on an in-session PC.
- **i18n**: Russian/English UI (design note in `docs/i18n-plan.md`, not built).
- **Real-hardware test** of the appearance feature (wallpaper via
  `SystemParametersInfo`, lock-screen image GDI draw) and the corner warning.

## How to work on it

```bash
go test ./...                                  # all tests
go vet ./...  &&  GOOS=windows go vet ./...     # BOTH targets must compile
VERSION=1.34.0 ./scripts/build.sh              # -> dist/windows/*.exe (+ zip)
./scripts/dev-sim.sh 6                          # server + 6 simulated PCs; panel on http://localhost:8080
```

- No CGO. Deps are vendored in `vendor/`; there is **no `go.sum`** on purpose.
  If you add a dependency, run `go mod tidy && go mod vendor` with normal
  internet and commit `vendor/` + the new `go.sum`.
- SQLite is `github.com/ncruces/go-sqlite3` (WASM via wazero — pure Go, no DLL).
  Screens use `github.com/kirides/go-d3d` (DXGI; only its CGO-free core is
  vendored).
- The admin panel is embedded, so **panel changes only appear after you rebuild
  and restart the server** — not on a browser refresh.
- Agents/clients are updated per gaming PC (re-run the installer, which drops the
  new client). The protocol tolerates version-skew: new message types are
  ignored by old agents, so a server update doesn't break un-updated PCs.

## Deployment topology
- Server `.exe` on the front-desk PC (or a Mac, built natively), panel on
  `:8080`, agent channel on `:47810`.
- Agent + client on each gaming PC; the "Gamer" Windows account is hardened
  (standard user, Task Manager/cmd/regedit blocked, auto-logon) via
  `scripts/harden-gamer-account.ps1`. Keep a separate Administrator account for
  maintenance.
- Optional operator phone (MacroDroid) for incoming-call booking.
- Setup/recovery manuals live in `README.md` and `docs/`.

## Repo map
- `cmd/` — the three programs.
- `internal/core` — pure billing/models/password (unit-tested, no I/O).
- `internal/store` — SQLite (migrations by `user_version`, append-only txns).
- `internal/server` — server, hub (agent TCP), http (panel API + SSE), ops,
  bookings, clients, screens, fs, assets, reports, web/ (the panel).
- `internal/agent` — agent runtime, local HTTP to the lock screen, fs, assets.
- `internal/protocol` — the wire protocol (server↔agent, agent↔lock screen).
- `internal/platform`, `internal/winapi`, `internal/svcrun`, `internal/wol`,
  `internal/bios` — Windows/platform glue.
- `docs/` — design system, i18n plan, call-booking, remote-access, this file.
- `scripts/` — build, dev-sim, install, firewall, account hardening.
