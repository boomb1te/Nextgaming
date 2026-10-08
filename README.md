# NextGaming

Gaming-club management system, v1: a PanCafe-style replacement.
The admin at the front desk starts paid sessions; each gaming PC stays locked
until a session starts and locks itself again when the time runs out.

No customer accounts in v1: the customer pays at the desk and the admin gives
the PC time. Money is tracked per session, per shift and per cashier.

## What's in the box

| Program | Runs on | What it does |
|---|---|---|
| `nextgaming-server.exe` | Front-desk PC (Windows service) | Database, admin panel at `http://<server>:8080`, talks to every PC |
| `nextgaming-agent.exe` | Every gaming PC (Windows service) | Connects to the server, keeps the session timer, restarts the lock screen if it is closed, runs shutdown / restart / Wake-on-LAN checks |
| `nextgaming-client.exe` | Every gaming PC (started by the agent) | Full-screen lock screen, session timer overlay, warnings and admin messages |

The admin panel is a web page, so the owner can also open it from a phone on the club Wi-Fi.

## Features (v1)

**Sessions**
- Prepaid (packages or minutes at the hourly rate); the PC locks when time runs out
- Open (timer counts up, charged per started minute at the end, with a grace period and optional minimum)
- Add time, pause / resume, move to another PC, end with refund (prepaid) or collection (open)
- Free "staff" unlock (never billed, always logged)
- Player can press **End session** on the PC: the clock stops, the PC locks, the tile turns red ("Wants to pay") until the admin settles it

**Timer on the gaming PC**
- A small panel in the corner shows the tariff, time left/used, the hourly rate and the running cost
- It is an ordinary window, so games and apps cover it (it no longer floats over everything); only time warnings and admin messages pop up briefly on top
- No "end session" button on the PC — the desk ends sessions

**PCs**
- Live grid: free / in use / paused / wants to pay / staff / offline, with countdowns
- Lock / unlock, message to one or all PCs, restart, shut down, **turn on (Wake-on-LAN)**
- Bulk: wake all offline PCs, shut down all free PCs
- New PCs register themselves as "PC 1, PC 2…"; rename and assign zones in Settings
- **Select** several PCs at once (tap Select, tap tiles) to start sessions, message, lock, wake or shut down together; starting a session on a selection asks prepaid or open first
- A **free note per PC** (e.g. "VIP seat", "keyboard replaced"), shown in the PC window with a 📝 mark on the tile
- Removing a PC uninstalls its agent and keeps it removed; a PC is matched by its network card, so it never appears twice after a wake or an update

**Wake-on-LAN assistant**
- The agent checks the Windows side (network card settings, "allow this device to wake the computer", Fast Startup, Ethernet vs Wi-Fi) and fixes it automatically
- **Test Wake-on-LAN** shuts a free PC down and wakes it with a magic packet
- If Windows is correct but the PC does not wake, the problem is the BIOS: the panel shows step-by-step BIOS instructions for that motherboard's brand (ASUS, MSI, Gigabyte, ASRock, Dell, HP, Lenovo, Biostar, or generic)

**Money**
- Zones with hourly rates, packages (fixed time, "until 08:00" night packs, sell-time windows)
- Shifts: open with the cash in the drawer, close with the counted cash, see the difference
- Every payment, refund and correction is a row in an append-only cash log (the database refuses edits)
- Daily report: revenue, by hour, busiest PCs, by cashier, shifts, all payments
- Activity log of every admin action
- Owner and cashier roles, **fully editable** (rename, change role, reset password, enable/disable, delete; the last owner is protected); daily automatic database backups (30 kept)
- **Data tab**: download a full backup, restore from a backup (applied on restart), and export payments and sessions to CSV

**Networks and VPNs**
- NextGaming only talks on the club's local network, so game VPNs players run do not affect it. VPN and virtual adapters are ignored when identifying a PC.

**Reliability**
- Server is the source of truth; PCs resynchronise on every reconnect
- If the network drops, the PC keeps counting and still locks on time
- If the agent stops, the lock screen locks the PC ("fails closed")
- The lock screen runs as SYSTEM, so the Gamer account cannot kill it; if it dies anyway it is restarted within a second and the admin gets an alert
- The lock screen and the agent watch each other: stop one and the other starts it again, and the admin gets an alert
- If the lock screen is killed repeatedly it keeps coming back (within ~0.5 s), never giving up, so the desktop is never left exposed for long. The surest protection, though, is running harden-gamer-account.ps1 so the player account cannot open Task Manager at all.

## Building

You need [Go](https://go.dev/dl/) 1.24 or newer. Dependencies are vendored, so no downloads are needed.

```bash
# on your Mac (or Linux), builds the three Windows .exe files into dist/windows/
./scripts/build.sh

# run the tests (core billing, database, Wake-on-LAN, agent, full server + agents end to end)
go test ./...
```

On Windows: `powershell -File scripts\build.ps1`.

### Try it on your Mac without Windows

```bash
./scripts/dev-sim.sh 6     # server + 6 simulated gaming PCs
```

Open http://localhost:8080. Simulated PCs have no real lock screen, power control or Wake-on-LAN,
but sessions, timers, reports and the admin panel all work.

## Installing at the club

### 1. Server (front-desk PC)

1. Copy `dist/windows` to `C:\NextGaming` on the front-desk PC.
2. Open **Command Prompt as Administrator** in that folder:
   ```
   nextgaming-server.exe install
   powershell -ExecutionPolicy Bypass -File firewall-server.ps1
   ```
3. Open http://localhost:8080 and create the owner account.
4. **Settings**: set the club name, zones and hourly rates, packages (the starter prices are placeholders).
5. **Settings → Add gaming PCs** shows the club key and the exact install command.

Give the front-desk PC a fixed IP address in the router (so gaming PCs always find it).

### 2. Each gaming PC

1. Copy `nextgaming-agent.exe`, `nextgaming-client.exe` and `install-agent.cmd` to the PC.
2. Run as Administrator:
   ```
   install-agent.cmd 192.168.1.10 K7P2-9QXA-M3TD
   ```
   (server IP and club key from the admin panel). The PC appears in the panel within seconds.
3. Lock down the player account (recommended; test on one PC first):
   ```
   powershell -ExecutionPolicy Bypass -File harden-gamer-account.ps1         # creates "Gamer", enables auto-logon
   (restart the PC, let Gamer log on once)
   powershell -ExecutionPolicy Bypass -File harden-gamer-account.ps1 -Apply  # applies restrictions
   ```
   Keep a separate Administrator account with a password for maintenance. `-Undo` reverses it.

### 3. Wake-on-LAN

**Turn it on in the BIOS first.** On ASUS UEFI boards: Del/F2 at boot → F7 (Advanced Mode) →
Advanced → APM Configuration → **ErP Ready = Disabled**, **Power On By PCI-E = Enabled** → F10.
ErP Ready cuts power to the network card when the PC is off; it was what blocked Wake-on-LAN on
the first club PC. Quick check: after a normal shutdown the Ethernet port light must stay on.

Then open the PC in the panel → **Check & fix Windows settings** → **Test Wake-on-LAN**
(**Test anyway** if the Windows check still shows problems you have already fixed by hand).
If the test fails, follow the BIOS steps shown in the panel, then test again.
The PCs must be on Ethernet and stay plugged in (power strip on) when off.

## Pilot plan (recommended)

1. Install the server and **one or two** PCs. Keep PanCafe on the other PCs.
2. Run real sessions for a week. Try to break the lock screen yourself (Alt+Tab, Win key,
   Task Manager, Ctrl+Alt+Del, unplugging the network cable, killing processes).
3. Compare daily revenue in the report with the cash counted.
4. Then roll out to the whole club.

## Not tested on real Windows yet

Everything was built and tested on Linux, and the Windows programs were smoke-tested under Wine
(lock screen, timer overlay, messages, pause, watchdog restart, fail-closed when the agent stops,
server with SQLite). These parts need their first test on a real Windows 10/11 PC:

- Running as Windows services, and launching the lock screen as SYSTEM into the Gamer session
- The keyboard blocking (Wine does not deliver low-level keyboard hooks like Windows does)
- `wolcheck.ps1` (network card, Fast Startup) and the real Wake-on-LAN test
- `harden-gamer-account.ps1`
- Multi-monitor and high-DPI layouts of the lock screen

## Known limitations of v1

- No customer accounts or balances (the database already has an empty `customer_id` on sessions for later)
- Games left open by the previous player are not closed when the PC locks
- No online payments, receipts, snack sales, booking, or multi-club support
- Admin logins are kept in memory: restarting the server logs everyone out
- The admin panel uses plain HTTP on the local network

## Project layout

```
cmd/nextgaming-server   server entry point (run / install / backup / reset-password)
cmd/nextgaming-agent    agent entry point (install / run / wol-check)
cmd/nextgaming-client   Windows lock screen (Win32, no GUI framework)
internal/core           domain model and billing rules (pure, fully unit tested)
internal/store          SQLite storage (append-only cash log, migrations, backups)
internal/server         agent hub, business operations, JSON API, SSE, reports, Wake-on-LAN test
internal/server/web     admin panel (plain HTML/CSS/JS, embedded into the server .exe)
internal/agent          agent logic: connection, local timer, local API for the lock screen
internal/platform       Windows specifics (inventory, shutdown, WoL check, lock-screen launcher) + simulation
internal/protocol       wire messages between server, agent and lock screen
internal/wol            magic packets
internal/bios           BIOS instructions per motherboard brand
internal/svcrun         Windows service plumbing
internal/winapi         minimal Win32 bindings for the lock screen
scripts/                build, dev simulation, install and hardening scripts
```
