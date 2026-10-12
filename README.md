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
- **Select** several PCs at once (tap Select, tap tiles) to start sessions, message, pause, resume, lock, wake or shut down together; starting a session on a selection asks prepaid or open first. Lock pauses a paid session (its time and money are kept — settle it per PC to close it) and fully ends a free/staff one
- A **free note per PC** (e.g. "VIP seat", "keyboard replaced"), shown in the PC window with a 📝 mark on the tile
- Removing a PC uninstalls its agent and keeps it removed; a PC is matched by its network card, so it never appears twice after a wake or an update

**Live screens**
- A **Screens** tab shows every online PC as a thumbnail, refreshed every 2–3 seconds; all PCs are captured on the same tick so the grid updates together
- Click a screen to open it full-size; that one PC then sends a sharper image about once a second
- Capturing happens only while the tab is open — nothing is sent, and no PC is touched, when nobody is watching
- Low bandwidth and CPU: small, low-quality JPEGs, captured by a small helper that the agent runs as the logged-in user (a SYSTEM process's screen grab comes back black)
- Capture uses the Desktop Duplication API (the real GPU output), so **borderless / windowed-fullscreen games are captured** and resolution changes are followed automatically; it falls back to a plain screen grab on older hardware or Remote Desktop
- **True exclusive-fullscreen games cannot be captured** by any monitoring tool without injecting into the game — they show the desktop instead. Set games to borderless / windowed-fullscreen (most default to this) to see them on the grid
- **HD snapshot**: click a PC to enlarge it, then **📷 HD snapshot** for one sharp, full-resolution still image (captured on demand, not streamed) — handy for reading fine detail. It opens frozen with an "Open full size" link; the live grid is unaffected
- **Take control** (LAN only): from a PC's enlarged view on a **computer** (not a phone — it needs a mouse), click **Take control** to drive that PC's **mouse** remotely at a ~0.5s refresh (1600px, readable) — for fixing something on a gaming PC without walking over. **Full screen** button to fill your display. While you control a PC the **player's own mouse and keyboard are blocked** so they don't fight you — and that block **auto-releases** within a few seconds if the panel closes or the network drops, so no one is ever left with a dead keyboard. Available only while the PC is unlocked (in a session); input is injected by the user-session helper. Keyboard control and an on-PC "staff is assisting" indicator are the next steps. A true exclusive-fullscreen game still can't be seen or controlled (borderless only)

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

**Clients & groups** (for booking)
- A **Clients** tab: add customers with a name and phone (both required). The **phone number is unique** and must be a valid Turkmen number — stored as `+993…` and used to recognise callers; names may still repeat. Optional note ("regular", "VIP", "banned"). Search by name or phone.
- **Groups**: a named set of clients booked together (e.g. a team). A group keeps at least one member; add or remove members anytime.
- Deleting a client keeps booking history; the client just leaves its groups.

**Bookings**
- A **Bookings** tab with a weekly view: reserve one or more PCs for a client or a group, for a time window.
- **Fully editable** anytime — change the time, length, PCs or who it's for; cancel it.
- **Auto-start**: when a booking's time comes, its PCs start an open session for that client automatically; offline PCs are woken a few minutes beforehand. A free PC shows a "📅 Booked 19:00 — Merdan" marker until then.
- **Never auto-ends** — you always end sessions yourself, so no live game is cut off. End and collect at the desk when the group leaves.
- **Overlaps are a soft warning** — booking a PC that's already booked for that time warns you and lets you book anyway; you resolve clashes by editing the bookings.
- **Release — no-show**: if a group doesn't show, one click frees their PCs (ends the sessions with no charge) and logs the no-show.

**Incoming-call booking** (operator phone, local network only)
- When a client calls the booking phone, the admin panel shows a prompt the moment the call ends: for a **known number**, *"Merdan wants to book a PC"* → one click opens the booking screen with that client already selected; for an **unknown number**, *"New caller — add & book"* → a quick add-client box with the number pre-filled (just type the name) → straight into booking.
- The booking screen shows **who is free**: once you pick a time, it marks which PCs are already booked for that window and shows a free count ("4 of 15 PCs free for 18:00–20:00"). If the caller books for a **team**, their groups are offered as one-tap options and the PC count is pre-filled to the group size.
- Phone numbers are matched regardless of how they were typed (`+993 65 12 34 56`, `065123456`, `65123456` all match), so a returning caller is recognised. Client numbers are **unique** for this reason.
- The prompt **waits** until the operator books or dismisses it (it doesn't flash away), and a repeat call from the same number doesn't pile up. Missed and outgoing calls are ignored.
- The operator phone (Android) sends the number with a tiny **MacroDroid** automation — no custom app. **Local network only**, authenticated by the club key, and excluded from any future online build. Setup: `docs/incoming-call-booking.md`. You can try the whole flow with no phone via **Settings → "Testing: simulate an incoming call"**.

**File sharing** (local network only)
- A **Files** tab with two panes: the **front-desk PC's own filesystem** on the left (browse anywhere — it's your trusted machine) and **one gaming PC's shared folder** on the right. Copy a file either way with one click.
- Each gaming PC exposes exactly **one fixed shared folder** — `C:\NextGaming\Shared` — and nothing outside it; the agent refuses any path that would escape it (no `..`, no symlinks, no absolute paths).
- The file bytes travel over a direct HTTP request between the agent and the server (authenticated by the club key and a one-time token), so a large transfer never blocks heartbeats or live screens. Listings and commands go over the normal control channel.
- A live **Transfers** list shows each copy's direction, size, progress and result; a file is never silently overwritten on the front desk (it gets " (2)", " (3)"… instead).
- This feature is **local-network only** and is deliberately excluded from any future remote/online build.

**Networks and VPNs**
- NextGaming talks only on the club's local network, and VPN/virtual adapters are ignored when identifying a PC (so a PC never appears twice because of one).
- **A player's VPN is fine as long as it is split-tunnel.** A *full-tunnel* VPN (all traffic through the VPN, e.g. AmneziaVPN by default) swallows the club LAN and makes the PC show "offline during a session" — exclude the club subnet from the VPN to fix it. See **Installing → Each gaming PC → step 4**.

**Staff at a gaming PC**
- A staff member at a locked gaming PC clicks the **Staff log in** button on the lock screen (or presses Ctrl+Alt+L), enters their NextGaming username and password, and the PC unlocks as a staff (free) session — no walking back to the desk. No browser opens and the desktop is never exposed.

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
4. **If the PC uses a VPN (players often run one for FACEIT) — set it to split-tunnel.** This step is mandatory on any PC where a VPN will be switched on, or NextGaming will lose that PC.
   - **The problem:** a *full-tunnel* VPN routes **all** traffic through the VPN, including the club LAN. The agent can then no longer reach the server, so the panel shows the PC **"offline during a session"** even though it is on and the player is gaming. AmneziaVPN (which uses its own network adapter) does this by default.
   - **The fix — exclude the club network from the VPN.** In AmneziaVPN (Russian UI): open the connection's split-tunnel / sites settings, set the mode to **«Весь трафик, кроме указанного»** (all traffic *except* the list), and add your **club subnet**, e.g. `192.168.1.0/24`. Match it to your server's IP — if the server is `192.168.1.10`, the subnet is `192.168.1.0/24`. FACEIT and everything else still go through the VPN as normal; only club-LAN traffic skips it.
   - **Alternative (per-app):** instead of a subnet, exclude the apps `nextgaming-agent.exe` and `nextgaming-client.exe` from the VPN. The subnet method is simpler and usually better.
   - **Check:** with the VPN **on**, the gaming PC must still open the panel / reach the server. If its tile stays green and the session keeps counting while the VPN is connected, you're set.
   - **Tailscale and other split-tunnel VPNs** need no change — they already leave the LAN alone.
   - **Same warning for the front-desk PC:** never run a full-tunnel VPN there. It severs the server from *every* agent at once and breaks Wake-on-LAN (the magic packet must go out on the club LAN adapter). If you must, exclude the club subnet there too.

### 3. Wake-on-LAN

**Turn it on in the BIOS first.** On ASUS UEFI boards: Del/F2 at boot → F7 (Advanced Mode) →
Advanced → APM Configuration → **ErP Ready = Disabled**, **Power On By PCI-E = Enabled** → F10.
ErP Ready cuts power to the network card when the PC is off; it was what blocked Wake-on-LAN on
the first club PC. Quick check: after a normal shutdown the Ethernet port light must stay on.

Then open the PC in the panel → **Check & fix Windows settings** → **Test Wake-on-LAN**
(**Test anyway** if the Windows check still shows problems you have already fixed by hand).
If the test fails, follow the BIOS steps shown in the panel, then test again.
The PCs must be on Ethernet and stay plugged in (power strip on) when off.

### 4. Booking phone (optional)

To turn incoming calls into one-click bookings, set up the booking phone
(Android, on the club Wi-Fi) with a small MacroDroid automation — see
`docs/incoming-call-booking.md`. No custom app, local network only. You can try
the feature first with no phone via **Settings → "Testing: simulate an incoming
call"**.

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
- Screen capture for the Screens tab (DXGI Desktop Duplication with a GDI fallback; untestable off Windows)
- `wolcheck.ps1` (network card, Fast Startup) and the real Wake-on-LAN test
- `harden-gamer-account.ps1`
- Multi-monitor and high-DPI layouts of the lock screen

## Known limitations of v1

- No customer accounts or balances (the database already has an empty `customer_id` on sessions for later)
- Games left open by the previous player are not closed when the PC locks
- No online payments, receipts, snack sales, remote PC control, or multi-club support (booking is admin-entered only — customers can't self-book)
- File sharing is one fixed folder per gaming PC (not a go-anywhere file manager) and local-network only; whole-folder/recursive copies and browsing arbitrary folders on a gaming PC are not in v1
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
