# NextGaming — Remote access with Tailscale (setup guide)

Reach the admin panel from anywhere (your phone, your home laptop) over the
internet, securely, with **no router changes and no change to NextGaming**.
Tailscale builds a tiny private encrypted network between *your own devices*;
only devices you add can see the panel. On top of that you still log in to
NextGaming as usual, so there are two locks on the door.

Time: ~15 minutes, once. Cost: free (Tailscale's Personal plan is plenty).

---

## What you need
- The **front-desk PC** (where the NextGaming server runs) — on and online.
- Your **phone** and/or a **laptop** you'll check the club from.
- One account to sign in with on all of them (Google, Microsoft, or email).

---

## Step 1 — Install Tailscale on the front-desk PC (Windows)
1. On the front PC, open https://tailscale.com/download and get the Windows app.
2. Install it, then **Log in** and sign in with your account. Approve/allow it.
3. The PC now joins your private network and gets a **Tailscale IP** that looks
   like `100.x.y.z`. You'll use that address to reach the panel.
   - To see it: click the Tailscale icon in the system tray → it shows this
     machine's IP, or open https://login.tailscale.com/admin/machines.
4. **Make it always-on (important for a front desk PC):** right-click the
   Tailscale tray icon → **Preferences** → turn on **Run unattended**
   (also called "Run Tailscale when I'm logged out"). This keeps the tunnel up
   even if nobody is logged into Windows, so you can reach the club anytime.

> NextGaming already allowed its port (8080) in the firewall during install, and
> the server listens on all network cards, so Tailscale traffic reaches it with
> nothing extra to configure.

## Step 2 — Install Tailscale on your phone / laptop
1. Phone: install **Tailscale** from the App Store (iOS) or Play Store (Android).
   Laptop: get it from https://tailscale.com/download.
2. **Sign in with the same account** you used on the front PC, and connect.
3. That's it — your phone and the front PC are now on the same private network.

## Step 3 — Open the panel remotely
1. Make sure Tailscale is **connected** (toggled on) on your phone/laptop.
2. In the browser, go to:  `http://<front-PC-Tailscale-IP>:8080`
   - Example: `http://100.101.102.103:8080`
3. You'll see the normal NextGaming login. Log in as owner — and you're running
   the club from anywhere.

### Nicer: use a name instead of the number (optional)
In the Tailscale admin console (https://login.tailscale.com/admin/dns) turn on
**MagicDNS**. Then you can reach the panel by the PC's name instead of the IP,
e.g. `http://front-pc:8080` (the name is shown next to the machine in the admin
console). Easier to remember; nothing else changes.

---

## Good to know
- **Only your devices** can reach the panel — Tailscale is private to your
  account. Random people on the internet can't see it. The NextGaming login is
  the second layer.
- **The front PC must be on and online** for remote access to work (same as any
  remote access).
- **Data use:** everything except live screens is tiny and fine on any
  connection. For live screens on slow/mobile internet, use the data modes
  (see `screen-monitoring-data-modes.md`) — prefer Low and view one PC at a time.
- **Battery/updates:** keep the Tailscale app allowed to run in the background on
  your phone so it reconnects on its own.

---

## If Tailscale won't connect from Turkmenistan
Tailscale needs to reach its sign-in/relay servers. On a restricted network that
can occasionally be blocked. First just **try it** — it usually works, and it can
fall back to relaying over HTTPS (port 443), which commonly gets through.

If it genuinely can't connect, the self-reliant fallback is a **cheap rented
server abroad (a $4–5/month VPS) running your own private tunnel (WireGuard)**, or
a self-hosted Tailscale control server (**Headscale**) on that VPS with the normal
Tailscale apps pointed at it. That depends on no company and is very hard to
block. It's a bit more setup — ask and I'll write that guide too.

---

## Quick recap
1. Install Tailscale on the front PC → log in → turn on **Run unattended**.
2. Install Tailscale on your phone/laptop → log in with the **same account**.
3. With Tailscale connected, open `http://<front-PC-Tailscale-IP>:8080` → log in.

No NextGaming changes, no router changes, private and encrypted.
