# NextGaming — Incoming-call booking (operator phone setup)

When a client calls the club's booking phone, NextGaming can show the operator a
prompt on the admin panel — *"Merdan wants to book a PC"* for a known caller, or
*"New caller — add & book"* for an unknown one — and take them straight into the
booking screen. This removes the "who's calling / add them by hand" step.

It works by having a tiny automation on the **operator phone** send the caller's
number to the server when an answered incoming call ends. All the intelligence
(matching the number to a client, the prompt, the booking flow) lives in the
server and panel, so the phone only has to do one thing: **post the number.**

**Local network only.** The phone talks to the server over the club Wi-Fi; this
feature is never exposed to the internet.

---

## What you need

- The **operator phone** (Android), on the **club Wi-Fi**.
- The **server's IP address** and the **club key** — both shown in the admin
  panel under **Settings → Add gaming PCs** (the key is the same one the gaming
  PCs use).
- The free app **MacroDroid** (Play Store). No custom app to install.

> iPhone can't do this — iOS does not let an app read the incoming number. Use
> an Android phone for the booking line.

---

## How it works (one line)

Answered incoming call ends → MacroDroid posts the number to
`http://<server-ip>:8080/api/phone/call` → the panel shows a booking prompt.

Missed and outgoing calls are ignored.

---

## Set up MacroDroid (one time, ~5 minutes)

1. Install **MacroDroid** and open it. Grant it the permissions it asks for —
   it needs **Phone / Call log** access to read the number (allow "Phone" and,
   if asked, "Call logs").
2. Tap **Add Macro**.
3. **Trigger** → **Phone/SMS** → **Call Ended**.
   - Set it to **Incoming** calls. (A missed call fires "Call Missed", not
     "Call Ended", so answered-only is automatic — exactly what we want.)
4. **Action** → **Connectivity** → **HTTP Request** (method **GET**).
   - **URL** — one line, no headers needed (the club key goes in the URL):
     ```
     http://<SERVER-IP>:8080/api/phone/call?key=YOUR-CLUB-KEY&number=
     ```
     Then tap the **magic-text button** and pick the **call number**; MacroDroid
     inserts it in curly braces, e.g. `{call_number}`. Finished URL:
     ```
     http://192.168.1.10:8080/api/phone/call?key=K7P2-9QXA-M3TD&number={call_number}
     ```
     Use *your* server IP and club key from the panel. The number's `+`/spaces
     don't matter — the server reads the digits. **No custom header is required.**
     (The older header method still works: header `X-NextGaming-Phone` = club key.)
5. **Save** the macro and give it a name like "NextGaming booking line".
6. Make sure MacroDroid is allowed to **run in the background** (disable battery
   optimisation for it) so it fires even when the screen is off.

That's it. The phone now tells the panel about every answered incoming call.

---

## Test it

**Without the phone first:** in the panel, **Settings → "Testing: simulate an
incoming call"** — type a number and the prompt appears exactly as a real call
would. Use this to confirm the booking flow before touching the phone.

**From a browser (proves the phone can reach the server):** on the phone, on the
club Wi-Fi, open this URL — if it works you'll see "NextGaming: call … received"
and the prompt pops on the panel:
```
http://<SERVER-IP>:8080/api/phone/call?key=YOUR-CLUB-KEY&number=65121212
```
If that works, MacroDroid will work with the same URL (just `{call_number}` in
place of the fixed number). If it shows a 404, the server is not updated to a
build with this feature; if "wrong or missing club key", fix the key.

**With the phone:** from another phone, call the operator phone, answer, then
hang up. Within a second or two the prompt should appear at the top of the admin
panel. Click it to add/know the caller and go into booking.

---

## Troubleshooting

- **No prompt appears.**
  - Is the operator phone on the **club Wi-Fi** (not mobile data)?
  - Open `http://<server-ip>:8080` in the phone's browser — if the panel doesn't
    load, the phone can't reach the server (check Wi-Fi / the IP). If the phone
    runs a **full-tunnel VPN**, exclude the club subnet (same fix as the gaming
    PCs — see the VPN step in the README).
  - In MacroDroid, open the macro and use **Test Actions** / check the macro log
    to see the HTTP request and its response.
- **"forbidden" / nothing recorded.** The `X-NextGaming-Phone` header value must
  be exactly the club key from the panel.
- **Prompt shows the wrong/!no number.** Make sure the URL ends with
  `?number=[call_number]` and that `[call_number]` was inserted from the
  magic-text picker (not typed literally).
- **It fires on outgoing calls too.** Set the **Call Ended** trigger to
  **Incoming** only.

---

## Tasker alternative

If you prefer Tasker: Profile → **Event → Phone → Call Ended** (incoming), Task →
**Net → HTTP Request**, POST to the same URL using `%CNUM` for the number
(`.../api/phone/call?number=%CNUM`) and add the `X-NextGaming-Phone` header with
the club key. MacroDroid is simpler; either works.

---

## Privacy / security

- The number is a client's phone number travelling **only on the club LAN** to
  your own server; it is never sent anywhere else.
- The phone authenticates with the club key, and the endpoint is not part of any
  future online/remote build.
- Numbers are stored only when you actually add the caller as a client; a call
  you dismiss saves nothing.
