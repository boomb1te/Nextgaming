# NextGaming — Russian/English UI (design note, PAUSED)

Status: **analysed and agreed, not started.** Picking this up later. This note
captures the decisions so we don't re-litigate them.

## Goal

Every admin-panel user can switch the interface between **English** and
**Russian** with a toggle in the header. The choice is saved **per admin** (in
the database) so it follows them to any device.

## Agreed decisions

- **Toggle in the header**, EN / RU. Switching re-renders instantly (the panel
  is client-rendered) — no reload.
- **Saved per admin** in the DB (a `lang` column on admins, or a small
  per-admin settings row), with a club default.
- **Terminology (confirmed by Boom):** PC → **ПК**, booking → **Бронь**,
  shift → **Смена**. Tone: decide вы/ты at build time (Boom reviews).
- **Round one = Layer 1 only** (the whole panel UI). See layers below.

## Open decisions (ask before building)

1. **Default language on first load** — Russian or English? (Boom leaned toward
   Russian for the club; each user can still toggle. Only affects a brand-new
   login.)
2. **Lock screen (Layer 3)** — translate the player-facing gaming-PC text now as
   a club default, or leave English for a later pass?

## The three layers of text

1. **Panel UI (ROUND ONE):** every button, tab, modal, hint, and all
   **client-side** messages generated in `internal/server/web/app.js`
   (validation toasts like "Name is required", "Choose at least one PC",
   confirm dialogs, "Booking saved", etc.). This is the bulk and what Boom means
   by "the actions."
2. **Server-generated messages (DEFERRED — Boom said not important for now):**
   the handful of errors/notices thrown by the Go server and shown as red
   popups. If skipped, these occasionally appear in English. Examples:
   - "open a shift first", "ПК is offline",
   - "that number already belongs to <name>", booking-overlap text,
   - "only the owner can do this", "please log in again", login throttle.
   Sources: `errBad`/`errConflict` strings across `internal/server/*.go`, the
   alerts (`s.alert(...)`), the activity-log action descriptions, and the
   WoL/BIOS guidance (`internal/bios`, wolcheck advice). BIOS text is long and
   rarely seen — do it last.
3. **Lock screen / player-facing (SEPARATE, DEFERRED):** `cmd/nextgaming-client`
   strings (timer labels, warnings, "time's up", admin messages). Not per-staff
   — it's a club-wide default language. Would need a `lang` field pushed in
   `DesiredState`/`ClientState`.

## Approach (how to do it right)

- **Extract every user-facing string into a dictionary** with stable keys and a
  `t('key', params)` function in app.js. EN and RU tables. This is what makes
  the toggle instant and makes it verifiable.
- **Verify coverage by grep:** after extraction, no bare English string literals
  should remain in render code — grep to prove it, so switching never leaves half
  the screen in English.
- **Russian plurals need real logic** (3 forms: 1 / 2–4 / 5+, with the usual
  teen exceptions): "1 минута / 2 минуты / 5 минут", "1 ПК / 2 ПК". Build a
  `plural(n, [one, few, many])` helper; don't hardcode.
- **Locale-aware formatting:** dates/times/numbers via `toLocaleString('ru-RU')`
  vs `('en-US')` based on the active language (the code currently passes `[]`).
- Money stays a plain number + currency; only separators/placement localise.

## Staged build plan (when resumed)

1. **i18n machinery + header toggle + per-admin persistence** (DB column, load on
   login, `t()` + `plural()` + locale formatting, EN table = current strings).
2. **Translate all panel strings to RU** (Layer 1), then grep-verify no English
   leaks; Boom reviews tone/terms.
3. (Later) Layer 2 server messages. (Later) Layer 3 lock screen.

## Also still parked (unrelated to i18n)

- Push the repo to **GitHub** (backup; source currently lives only in the build
  container).
- **MacroDroid** booking-phone setup — waiting on physical access to the phone
  (`docs/incoming-call-booking.md`).
- Optional: let the incoming-call endpoint accept the club key as a `?key=`
  query param (so header-less / free automation apps work).
