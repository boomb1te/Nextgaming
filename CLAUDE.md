# NextGaming — notes for Claude Code

Gaming-club management system (PanCafe replacement). Go 1.24, no CGO, dependencies vendored.
Read README.md for the product view. This file is the engineering contract.

## Commands

```bash
go test ./...                    # all tests, incl. end-to-end server + in-process agents
go test -race ./internal/...     # run before committing concurrency changes
GOOS=windows go vet ./...        # Windows-only files must compile too
./scripts/build.sh               # dist/windows/*.exe
./scripts/dev-sim.sh 6           # server + 6 simulated PCs on macOS/Linux, panel on :8080
```

Always run both `go vet ./...` and `GOOS=windows go vet ./...`: many files are `//go:build windows`.

## Architecture

- **Server** (`internal/server`) is the single source of truth. Every state-changing
  operation takes `s.mu` and runs in one `store.Tx`. After a change it calls
  `s.pushState(pcID)` (tells the agent) and `s.changed()` (refreshes admin panels via SSE).
- **Agent ↔ server**: TCP, one JSON `protocol.Envelope` per line (`internal/protocol`).
  The server pushes a full `DesiredState` (durations, never wall-clock times) on every
  change and on reconnect. Agents send `Heartbeat` every 3 s; 12 s of silence = offline.
- **Agent ↔ lock screen**: HTTP on 127.0.0.1 (`GET /state`, `POST /logout`, and for screen
  monitoring `GET /capture-wait` long-poll + `POST /screen`, all with header
  `X-NextGaming-Client: 1`). The lock screen polls `/state` every 500 ms and **fails closed**.
- **Screen monitoring**: the server broadcasts `TypeCapture` to all online PCs on a shared
  tick only while an admin watches the Screens tab (`internal/server/screens.go`). The agent
  wakes `/capture-wait`; a capture helper — `nextgaming-client.exe -capture`, which the agent
  runs **as the logged-in user** (a SYSTEM process's GDI grab is black), separate from the
  SYSTEM lock screen — grabs the screen and posts a JPEG, relayed as `TypeScreenFrame` and
  served from `GET /api/pcs/{id}/screen.jpg`. Frames can be a few hundred KB, so
  `protocol.MaxLine` is 8 MB. The lock-screen process no longer captures. Capture is
  DXGI Desktop Duplication (captures borderless games, follows resolution) with a GDI
  fallback (`cmd/nextgaming-client/capture_dxgi_windows.go`, `capture_windows.go`). The
  panel's CSP allows `blob:` images because frames are shown via `URL.createObjectURL`.
- **File sharing (LAN only)**: a **Files** tab. The front desk browses its own
  filesystem (`ListLocal`); each gaming PC exposes one fixed shared folder
  (`platform.DefaultSharedDir`, `C:\NextGaming\Shared`), listed over the control
  channel (`TypeFSList`). File bytes move over a direct HTTP request between the
  agent and the server, keyed by the club key (`X-NextGaming-Agent`) + a one-time
  token (`/api/fs/serve/{token}` to the PC, `/api/fs/recv/{token}` from it), so a
  big copy never blocks heartbeats/screens. `internal/agent/fs.go` enforces
  containment (no `..`/symlink/absolute escape); `internal/server/fs.go` +
  `transfers.go` drive it. Never expose this in any future online build.
- **Incoming-call booking (LAN only)**: the operator phone (MacroDroid) POSTs an
  answered call's number to `POST /api/phone/call` (club key in `X-NextGaming-Phone`,
  `?number=` or JSON). `internal/server/calls.go` holds pending calls in memory
  (coalesced per number, 1h TTL) and they ride `Snapshot.PendingCalls` to the
  panel. Client phones are unique and matched by their 8-digit local key
  (`core.NormalizeTMPhone` / `store.ClientByPhoneKey`); migration v6 added
  `clients.phone_key`. The panel (`web/app.js`: `updateCallBar`, `bookingModal`)
  turns a prompt into a pre-filled booking. Never expose in any online build. Docs:
  `docs/incoming-call-booking.md`.
- **Clients & members**: `clients` holds the booking contact (name + unique phone).
  A client becomes an **authorized member** when a `client_accounts` row is added
  (migration v7): a system-generated one-time password, stored only as a
  PBKDF2-HMAC-SHA256 hash (`internal/core/password.go`, stdlib — no bcrypt dep),
  plus `registered_at/by`. Ops in `internal/server/clients.go`:
  `AuthorizeClient` / `ResetMemberPassword` return the plain password ONCE (SMS
  later; shown on screen now), `DeauthorizeClient` removes membership. "Hours
  played" is derived, never stored — `store.ClientHoursSeconds` sums active
  seconds of sessions whose `customer_id` is that client. Ratings are deferred
  (shown as "not available yet"). All member data is local for now; the phone is
  the eventual national/app login id.
- **Admin panel**: `internal/server/web`, plain JS, no build step, embedded with `go:embed`.
  JSON API under `/api`, snapshots over SSE at `/api/events`. State-changing requests need
  the header `X-NextGaming: 1` (CSRF guard).

## Rules that must not break

1. Money is `core.Money` (int64 minor units). Never floats for money.
2. The `transactions` table is append-only (SQLite triggers enforce it). Fix mistakes with a
   `correction` or `refund` transaction, never by editing rows.
3. A session change and its transaction are written in the same `store.Tx`.
4. Inside a `store.Tx` callback, only use the `q` it gives you, never `s.st.DB`
   (the pool has one connection; using `s.st.DB` there deadlocks).
5. Pure billing rules live in `internal/core` with unit tests. Keep them free of I/O.
6. The lock screen must fail closed: when in doubt, lock.
7. Paused time is never billed; a player's "End session" pauses (CheckoutRequested) instead of ending.
8. `customer_id` on sessions holds the attached **client** id (0/NULL = none). It
   credits that client's played hours; it's set at start (`StartRequest.ClientID`,
   or a single-client booking) and can be changed on a running session
   (`SetSessionClient`). `UpdateSession` must keep writing it.

## Where to add things

- New admin action: method on `*Server` (ops.go) → route in http.go → button in web/app.js → test in e2e_test.go.
- New agent command: constant + payload in protocol.go → `Agent.handle` → `platform` function
  (Windows file + `platform_other.go` simulation).
- Windows API calls: `internal/winapi` (lock screen) or `golang.org/x/sys/windows` (agent).

## Testing on real Windows (not yet done)

Services, SYSTEM lock screen in the user session, keyboard hook, `wolcheck.ps1`,
real Wake-on-LAN test, `harden-gamer-account.ps1`. Test on one club PC before rollout.

## Dependencies

`github.com/ncruces/go-sqlite3` (SQLite compiled to WebAssembly, run by wazero: pure Go,
cross-compiles, no DLL), `golang.org/x/sys`, and `github.com/kirides/go-d3d` (DXGI Desktop
Duplication for the Screens tab — only its CGO-free core packages are vendored; its
examples/mjpeg deps are not). They are vendored in `vendor/`. There is no
`go.sum` in the repo on purpose; if you change dependencies, run `go mod tidy && go mod vendor`
with normal internet access and commit the new `go.sum` and `vendor/`.
