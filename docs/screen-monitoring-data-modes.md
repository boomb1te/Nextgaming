# NextGaming — Screen Monitoring & Data Modes (design note)

A reference for how the live Screens feature uses bandwidth, the three data
modes, and why neither the club's 20 Mb/s Wi-Fi nor a slow remote connection
gets drained. Written as a plan to remember every detail; nothing here is built
yet unless noted.

---

## 1. How screen monitoring works (recap)

- Each gaming PC runs a tiny capture helper (as the logged-in user) that grabs
  the screen, shrinks it, and makes a small JPEG.
- The helper sends the JPEG to the agent on the same PC → the agent forwards it
  to the server on the front desk → the server keeps the latest image per PC →
  the admin panel shows it.
- **Capture only happens while the Screens tab is open.** When nobody is
  looking, nothing is captured, nothing is sent, and the gaming PCs do no extra
  work. This is the single most important bandwidth protection.
- The server drives a shared "capture tick", so all PCs grab a frame at the same
  moment (synchronised grid).

Two separate links carry the images:
1. **Gaming PC → front-desk server** (always on the local network).
2. **Server → the device viewing the panel** (local Wi-Fi when you're at the
   club; the internet when you're remote).

---

## 2. The three data modes

The mode controls how often a frame is sent (the "gap") AND how big / how
compressed each frame is. Lower modes save on both.

| Mode     | Grid refresh | Enlarged PC refresh | Image width | JPEG quality | Intended use                    |
|----------|-------------:|--------------------:|------------:|-------------:|---------------------------------|
| **High** |        2.5 s |                 1 s |       384px |         ~40% | On the club Wi-Fi (local)       |
| **Normal** |       10 s |                 3 s |       320px |         ~35% | A decent remote connection      |
| **Low**  |         20 s |                 5 s |       256px |         ~30% | Slow internet / mobile data     |

Behaviour:
- You pick the mode on the Screens tab; the browser remembers your choice.
- **Auto-default:** opening the panel on the club network starts in **High**;
  opening it remotely starts in **Low**. You can always override.
- The **enlarged (full-size) view** of one PC also follows the mode — it just
  refreshes a bit faster than the grid, and only that one PC streams at the
  bigger size.
- High mode is exactly today's local behaviour (2.5 s).

Approximate per-frame sizes (estimates; a dark lock screen is far smaller, a
busy game a bit larger):
- High 384px/q40 ≈ **~20 KB**
- Normal 320px/q35 ≈ **~12 KB**
- Low 256px/q30 ≈ **~7 KB**

---

## 3. The club Wi-Fi is 20 Mb/s — it will NOT be drained

Units first: **20 Mb/s = 20 megabits/s = 2.5 megabytes/s.**

Load of monitoring all **15 PCs** (worst realistic case, High mode):

```
15 PCs × 20 KB ÷ 2.5 s ≈ 120 KB/s ≈ 0.96 Mbit/s  ≈ 5% of 20 Mb/s
```

| Mode   | All 15 PCs, sustained | Share of 20 Mb/s Wi-Fi |
|--------|----------------------:|-----------------------:|
| High   | ~1 Mbit/s             | ~5%                    |
| Normal | ~0.14 Mbit/s          | <1%                    |
| Low    | ~0.04 Mbit/s          | negligible             |

- The one **enlarged PC** at hi-res (1366px, higher quality) adds roughly
  **1.5 Mbit/s** while open — still under ~10% of the Wi-Fi, and only while you
  have that PC open.
- So even at High with a PC enlarged, screens use well under a quarter of the
  Wi-Fi. The club's 20 Mb/s is safe.

Why it's even safer in practice:
- **Gaming PCs should be on Ethernet cables** (they already need wired LAN for
  Wake-on-LAN). Their screenshots then travel to the server over wired LAN and
  **do not use the Wi-Fi at all** — only your phone/laptop viewing the panel
  uses the Wi-Fi.
- **Capture stops when the Screens tab is closed** — zero cost the rest of the
  time.
- **Skip-if-unchanged** (planned, see §5): idle PCs send almost nothing.

---

## 4. Over slow / remote internet

When you're away from the club, the images are the front PC's **upload** to your
phone, and mobile-data cost matters. Rough figures for all 15 PCs:

| Mode   | Upload needed | Data per hour (all 15 PCs) |
|--------|--------------:|---------------------------:|
| High   | ~1 Mbit/s     | ~450 MB/hour               |
| Normal | ~0.14 Mbit/s  | ~60 MB/hour                |
| Low    | ~0.04 Mbit/s  | ~18 MB/hour                |
| Low, **one PC at a time** | a few KB/s | **~5 MB/hour**   |

Takeaways for Turkmenistan's slow internet:
- **"Watch the whole floor live" is the expensive case.** Use Normal or Low and
  accept a slower refresh.
- **"Check what's on PC 7 right now" is cheap** — open one PC in Low mode and it
  is only a few KB/s; works even on slow links and barely touches mobile data.
- Everything else in the system (sessions, money, bookings, power, Wake-on-LAN,
  reports) is tiny text and works fine on any connection — only screens are
  bandwidth-heavy.

---

## 5. Optimisations that protect bandwidth (Wi-Fi and internet)

1. **Capture only while the Screens tab is open** — already how it works.
2. **Three data modes** (High / Normal / Low) — the gap + size + quality knob
   described above.
3. **Only the opened PC streams at full size** — the grid stays small; the big
   image is just the one PC you clicked.
4. **Skip-if-unchanged (planned):** the capture helper compares each frame to
   the last one; if the screen hasn't changed (e.g. an idle PC on the lock
   screen), it sends a 1-byte "no change" instead of a whole JPEG. Idle PCs then
   cost almost nothing, cutting real usage far below the table figures.
5. **Auto-default by location** — High on the club network, Low when remote,
   with manual override, so you don't accidentally stream High over mobile data.

---

## 6. What this does NOT cover

- **Remote real-time control** (your mouse/keyboard driving a PC live) is the
  one thing that stays hard over slow internet, no matter how it's built. If you
  ever want that remotely, the honest answer is to use a purpose-built tool
  (AnyDesk / the open-source RustDesk) for that single case rather than our
  JPEG approach.
- These modes are about **viewing** screens. Capturing a game that runs in true
  exclusive fullscreen is a separate, already-discussed limitation (use
  borderless / windowed-fullscreen).

---

## 7. One-line summary

Screen monitoring is the only bandwidth-heavy feature; with three data modes
(2.5 s / 10 s / 20 s, each smaller and more compressed), capture only while the
tab is open, wired gaming PCs, and skip-if-unchanged, it uses ~5% of the club's
20 Mb/s Wi-Fi at most, and remotely it is practical in Low mode (especially one
PC at a time) even on Turkmenistan's slow internet.
