# calendar-sync

Reads calendar events from iCloud over CalDAV and pushes them to
[Genki Tracker](https://github.com/lucywoodman/genki-tracker), where they become
the free/busy view Maureen schedules training around.

Sibling of `weather-sync`, and built the same way: fetch from an external source,
push to genki, let genki serve the derived view.

## What this repo decides

genki deliberately makes no judgment about calendar events — it stores what it's
given and derives free windows arithmetically. The interpretation happens here,
because this is the only place that sees the raw iCalendar properties.

**Is Lucy busy?** No, if the event is `TRANSP:TRANSPARENT` ("show as free"), if
it's `STATUS:CANCELLED`, or if her own attendee record says
`PARTSTAT=DECLINED`. That last one is why `ICLOUD_USERNAME` is needed for more
than logging in: without it, a colleague declining would look like her morning
freeing up.

**Is she away?** Only if the event came from the calendar named in
`AWAY_CALENDAR`. This is deliberately *not* inferred from "is it an all-day
event" — "Bin day" is an all-day event too, and treating it as a holiday would
silently suppress a week of training recommendations. You decide what counts as
away by which calendar you put it in.

**Recurring events** are expanded server-side via CalDAV's `expand`. Without
that, a weekly stand-up comes back as one event with a recurrence rule, and
09:30 would look free every day but the first.

## Setup

You need an **app-specific password** from
[appleid.apple.com](https://appleid.apple.com) — a normal Apple ID password will
not authenticate against CalDAV.

Copy `.envrc.example` to `.envrc` and fill it in, then check the connection
works before wiring anything up:

```bash
make build
./calendar-sync calendars
```

That authenticates, discovers the account's calendars and prints their names,
without touching genki. It's also how you find the exact name for
`AWAY_CALENDAR`.

Then:

```bash
./calendar-sync push               # today and tomorrow
./calendar-sync push --start 2026-08-25 --end 2026-08-27
```

Today is what a morning briefing slots around; tomorrow is what an evening one
sets up.

## Configuration

| | |
|---|---|
| `ICLOUD_USERNAME` | Apple ID. Also matches your own `PARTSTAT` among attendees. |
| `ICLOUD_APP_PASSWORD` | From appleid.apple.com. Not your Apple ID password. |
| `ICLOUD_CALDAV_URL` | Optional, defaults to `https://caldav.icloud.com/`. |
| `AWAY_CALENDAR` | Optional. Events here mark the day as away. |
| `CALENDAR_TZ` | **Required**, e.g. `Europe/London`. |
| `GENKI_URL` | e.g. `http://localhost:8050`. |
| `GENKI_PASSWORD` | Sent as a bearer token. |

`CALENDAR_TZ` has no default on purpose. genki stores local times, and a
container's clock is UTC unless told otherwise — defaulting would put every
summer meeting in the database an hour early and look entirely fine while doing
it. A missing `AWAY_CALENDAR` name is a hard error rather than a warning, for
the same reason: the quiet version is a holiday that never registers.

## Nothing personal lives here

This repo is public. Credentials come from the environment and are never
committed, the per-account CalDAV path (`p12-caldav.icloud.com/<id>/...`) is
discovered at runtime rather than configured, and every test fixture is written
by hand. A real `.ics` export carries meeting titles, attendee addresses and
account paths, so none is ever checked in.

Event summaries are sent to genki and stored there, on your own server. They are
not part of the `/api/calendar-slots` response, so they never reach Maureen or
the Anthropic API.

## Deployment

Built into genki-tracker's shared sync image alongside `garmin-data` and
`weather-sync`. Adding it needs the same cache-busting trick the others use:

```dockerfile
ADD https://api.github.com/repos/lucywoodman/calendar-sync/commits/main /tmp/calendar-sync.rev
WORKDIR /build-calendar
RUN git clone --depth 1 https://github.com/lucywoodman/calendar-sync.git . && \
    go build -o /usr/local/bin/calendar-sync ./cmd/calendar-sync
```

The `ADD` is not optional. Docker fingerprints the `RUN` instruction text, not
the state of the remote branch, so a bare `git clone` layer is a cache hit
forever and new commits never reach the image.

Then a line in `entrypoint.sh`, following the existing pattern of running every
sync even if an earlier one failed, while still exiting non-zero if any did.

## Tests

```bash
make test
```

The event mapping is covered thoroughly — it holds all the judgment, and it's
pure. The CalDAV client is thin I/O and is verified by running `calendars`
against the real service; there's no fake iCloud here.
