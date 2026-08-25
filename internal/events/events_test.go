package events

import (
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"
)

// Every fixture here is written by hand. Nothing is exported from a real
// calendar: a genuine .ics carries meeting titles, attendee addresses and the
// account's own CalDAV paths, and this repo is public.
func parseEvent(t *testing.T, vevent string) ical.Event {
	t.Helper()
	doc := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//test//EN\r\n" +
		strings.ReplaceAll(strings.TrimSpace(vevent), "\n", "\r\n") +
		"\r\nEND:VCALENDAR\r\n"
	cal, err := ical.NewDecoder(strings.NewReader(doc)).Decode()
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	all := cal.Events()
	if len(all) != 1 {
		t.Fatalf("want exactly 1 event in the fixture, got %d", len(all))
	}
	return all[0]
}

func london(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/London")
	if err != nil {
		t.Fatalf("load Europe/London: %v", err)
	}
	return loc
}

func mapper(t *testing.T) Mapper {
	return Mapper{Loc: london(t), Email: "lucy@example.com"}
}

func day(t *testing.T, date string) time.Time {
	t.Helper()
	d, err := time.ParseInLocation(dateOnly, date, london(t))
	if err != nil {
		t.Fatalf("parse day: %v", err)
	}
	return d
}

func convert(t *testing.T, vevent, date string, away bool) (Event, bool) {
	t.Helper()
	got, ok, err := mapper(t).Convert(parseEvent(t, vevent), day(t, date), away)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	return got, ok
}

const standup = `
BEGIN:VEVENT
UID:standup-1
SUMMARY:Standup
DTSTART:20260614T083000Z
DTEND:20260614T084500Z
END:VEVENT`

func TestConvert_TimedEventIsBusy(t *testing.T) {
	got, ok := convert(t, standup, "2026-06-14", false)
	if !ok {
		t.Fatal("want the event to land on the day")
	}
	if !got.Busy {
		t.Error("want busy for an ordinary meeting")
	}
	if got.AllDay {
		t.Error("want all_day false for a timed event")
	}
	if got.UID != "standup-1" || got.Summary != "Standup" {
		t.Errorf("uid/summary: got %q / %q", got.UID, got.Summary)
	}
}

func TestConvert_TimesAreLocalNotUTC(t *testing.T) {
	// 08:30Z in June is 09:30 in London. A container defaulting to UTC would
	// store every summer meeting an hour early, and Maureen would cheerfully
	// suggest a run during a real one.
	got, _ := convert(t, standup, "2026-06-14", false)
	if got.Start != "09:30" || got.End != "09:45" {
		t.Errorf("want 09:30-09:45 local, got %s-%s", got.Start, got.End)
	}
}

func TestConvert_SkipsEventsOnOtherDays(t *testing.T) {
	if _, ok := convert(t, standup, "2026-06-15", false); ok {
		t.Error("want the event skipped on a day it doesn't touch")
	}
}

func TestConvert_TransparentEventIsNotBusy(t *testing.T) {
	// "Show as free" in the calendar UI. The hour is still available.
	got, _ := convert(t, `
BEGIN:VEVENT
UID:focus-1
SUMMARY:Blocked out
DTSTART:20260614T090000Z
DTEND:20260614T100000Z
TRANSP:TRANSPARENT
END:VEVENT`, "2026-06-14", false)
	if got.Busy {
		t.Error("want busy false for a TRANSPARENT event")
	}
}

func TestConvert_CancelledEventIsNotBusy(t *testing.T) {
	got, _ := convert(t, `
BEGIN:VEVENT
UID:gone-1
SUMMARY:Cancelled workshop
DTSTART:20260614T090000Z
DTEND:20260614T100000Z
STATUS:CANCELLED
END:VEVENT`, "2026-06-14", false)
	if got.Busy {
		t.Error("want busy false for a cancelled event")
	}
}

func TestConvert_DeclinedByLucyIsNotBusy(t *testing.T) {
	got, _ := convert(t, `
BEGIN:VEVENT
UID:optional-1
SUMMARY:Optional all-hands
DTSTART:20260614T090000Z
DTEND:20260614T100000Z
ATTENDEE;PARTSTAT=DECLINED:mailto:lucy@example.com
END:VEVENT`, "2026-06-14", false)
	if got.Busy {
		t.Error("want busy false for a meeting Lucy declined")
	}
}

func TestConvert_AcceptedByLucyIsBusy(t *testing.T) {
	got, _ := convert(t, `
BEGIN:VEVENT
UID:accepted-1
SUMMARY:Design review
DTSTART:20260614T090000Z
DTEND:20260614T100000Z
ATTENDEE;PARTSTAT=ACCEPTED:mailto:lucy@example.com
END:VEVENT`, "2026-06-14", false)
	if !got.Busy {
		t.Error("want busy true for a meeting Lucy accepted")
	}
}

func TestConvert_SomeoneElseDecliningDoesNotFreeLucy(t *testing.T) {
	// The reason Mapper needs an email at all. Without it, one colleague
	// dropping out would empty her diary.
	got, _ := convert(t, `
BEGIN:VEVENT
UID:shared-1
SUMMARY:Pairing
DTSTART:20260614T090000Z
DTEND:20260614T100000Z
ATTENDEE;PARTSTAT=DECLINED:mailto:someone-else@example.com
ATTENDEE;PARTSTAT=ACCEPTED:mailto:lucy@example.com
END:VEVENT`, "2026-06-14", false)
	if !got.Busy {
		t.Error("want busy true when it was someone else who declined")
	}
}

const annualLeave = `
BEGIN:VEVENT
UID:leave-1
SUMMARY:Annual leave
DTSTART;VALUE=DATE:20260614
DTEND;VALUE=DATE:20260620
END:VEVENT`

func TestConvert_AllDayEventHasNoHours(t *testing.T) {
	// An all-day event contributes no busy time. genki would otherwise have
	// nothing left to schedule into on every holiday.
	got, ok := convert(t, annualLeave, "2026-06-14", true)
	if !ok {
		t.Fatal("want the all-day event on its first day")
	}
	if !got.AllDay {
		t.Error("want all_day true")
	}
	if got.Start != "" || got.End != "" {
		t.Errorf("want no start/end for an all-day event, got %q-%q", got.Start, got.End)
	}
}

func TestConvert_AllDayEventCoversEveryDayInItsRange(t *testing.T) {
	// DTEND is exclusive for all-day events, so the 20th is the first day back.
	for _, date := range []string{"2026-06-14", "2026-06-17", "2026-06-19"} {
		if _, ok := convert(t, annualLeave, date, true); !ok {
			t.Errorf("want the holiday to cover %s", date)
		}
	}
	if _, ok := convert(t, annualLeave, "2026-06-20", true); ok {
		t.Error("DTEND is exclusive: the 20th is back at work")
	}
}

func TestConvert_AllDayEventWithoutDTENDCoversOneDay(t *testing.T) {
	single := `
BEGIN:VEVENT
UID:oneday-1
SUMMARY:Day off
DTSTART;VALUE=DATE:20260614
END:VEVENT`
	if _, ok := convert(t, single, "2026-06-14", true); !ok {
		t.Error("want the event on its own day")
	}
	if _, ok := convert(t, single, "2026-06-15", true); ok {
		t.Error("want a DTEND-less all-day event to cover exactly one day")
	}
}

func TestConvert_AwayIsPassedThrough(t *testing.T) {
	// Which calendar an event came from is the caller's business — this package
	// only records the answer.
	away, _ := convert(t, annualLeave, "2026-06-14", true)
	if !away.Away {
		t.Error("want away true when the event came from the away calendar")
	}
	ordinary, _ := convert(t, standup, "2026-06-14", false)
	if ordinary.Away {
		t.Error("want away false for an ordinary meeting")
	}
}

func TestConvert_ClampsAnEventRunningPastMidnight(t *testing.T) {
	// HH:MM can't say "carries on into tomorrow", so the day it's asked about
	// is the day it reports.
	overnight := `
BEGIN:VEVENT
UID:redeye-1
SUMMARY:Overnight
DTSTART:20260614T220000Z
DTEND:20260615T060000Z
END:VEVENT`
	first, ok := convert(t, overnight, "2026-06-14", false)
	if !ok {
		t.Fatal("want the event on its first day")
	}
	if first.Start != "23:00" || first.End != "23:59" {
		t.Errorf("first day: want 23:00-23:59, got %s-%s", first.Start, first.End)
	}
	second, ok := convert(t, overnight, "2026-06-15", false)
	if !ok {
		t.Fatal("want the event on its second day too")
	}
	if second.Start != "00:00" || second.End != "07:00" {
		t.Errorf("second day: want 00:00-07:00, got %s-%s", second.Start, second.End)
	}
}

func TestConvert_ToleratesAMissingSummary(t *testing.T) {
	// A private event synced from elsewhere may have no SUMMARY. It still
	// occupies the hour, which is the only part that reaches Maureen.
	got, ok := convert(t, `
BEGIN:VEVENT
UID:untitled-1
DTSTART:20260614T090000Z
DTEND:20260614T100000Z
END:VEVENT`, "2026-06-14", false)
	if !ok || !got.Busy {
		t.Error("want a summary-less event to still count as busy")
	}
}
