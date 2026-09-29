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
- **Agent ↔ lock screen**: HTTP on 127.0.0.1 (`GET /state`, `POST /logout` with header
  `X-NextGaming-Client: 1`). The lock screen polls every 500 ms and **fails closed**.
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
8. `customer_id` on sessions is reserved for customer accounts (not used in v1).

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
cross-compiles, no DLL) and `golang.org/x/sys`. They are vendored in `vendor/`. There is no
`go.sum` in the repo on purpose; if you change dependencies, run `go mod tidy && go mod vendor`
with normal internet access and commit the new `go.sum` and `vendor/`.
