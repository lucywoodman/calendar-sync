// Package events maps iCalendar events onto the shape genki stores.
//
// This is where the judgment lives. genki deliberately doesn't decide whether
// an event occupies Lucy's time or marks her as away — those questions are
// answered by iCalendar properties, and this is the only place that sees them.
// genki honours the flags this package sets, the same split as weather-sync's
// conditions.
package events

import (
	"fmt"
	"strings"
	"time"

	"github.com/emersion/go-ical"
)

const (
	hourMinute = "15:04"
	dateOnly   = "2006-01-02"
)

// Event is one calendar event in the shape POST /api/calendar expects.
type Event struct {
	UID     string `json:"uid"`
	Summary string `json:"summary"`
	Start   string `json:"start"` // HH:MM local; empty when AllDay
	End     string `json:"end"`   // HH:MM local; empty when AllDay
	AllDay  bool   `json:"all_day"`
	Busy    bool   `json:"busy"`
	Away    bool   `json:"away"`
}

// Mapper converts iCalendar events for one person, in one timezone.
type Mapper struct {
	// Loc is the timezone the HH:MM times are expressed in. genki stores local
	// times, and a container defaulting to UTC would quietly shift every
	// meeting by an hour for the whole of British Summer Time.
	Loc *time.Location

	// Email identifies Lucy among an event's attendees, so that her own
	// PARTSTAT is the one that decides whether she's busy. Without it, a
	// colleague declining would appear to free up her morning.
	Email string
}

// Convert maps one VEVENT onto the given local day. ok is false when the event
// doesn't touch that day at all, which is the normal outcome for most of a
// query's results rather than an error.
func (m Mapper) Convert(e ical.Event, day time.Time, away bool) (Event, bool, error) {
	uid, _ := e.Props.Text(ical.PropUID)
	summary, _ := e.Props.Text(ical.PropSummary)
	out := Event{UID: uid, Summary: summary, Busy: m.busy(e), Away: away}

	if isAllDay(e) {
		on, err := allDayFallsOn(e, m.Loc, day)
		if err != nil || !on {
			return Event{}, false, err
		}
		out.AllDay = true
		return out, true, nil
	}

	start, err := e.DateTimeStart(m.Loc)
	if err != nil {
		return Event{}, false, fmt.Errorf("event %q has no usable start: %w", summary, err)
	}
	end, err := e.DateTimeEnd(m.Loc)
	if err != nil {
		return Event{}, false, fmt.Errorf("event %q has no usable end: %w", summary, err)
	}
	start, end = start.In(m.Loc), end.In(m.Loc)

	dayFrom := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, m.Loc)
	dayTo := dayFrom.AddDate(0, 0, 1)
	if !end.After(dayFrom) || !start.Before(dayTo) {
		return Event{}, false, nil
	}
	// Clamped, because HH:MM has no way to say "carries on into tomorrow".
	// genki narrows to its own day bounds afterwards, so the minute either side
	// of midnight makes no practical difference.
	if start.Before(dayFrom) {
		start = dayFrom
	}
	if end.After(dayTo) {
		end = dayTo.Add(-time.Minute)
	}

	out.Start = start.Format(hourMinute)
	out.End = end.Format(hourMinute)
	return out, true, nil
}

// busy answers whether the event actually occupies Lucy's time. A meeting she
// declined, one marked free, and one that's been cancelled all leave the hour
// available — counting them as busy would hide windows she could train in.
func (m Mapper) busy(e ical.Event) bool {
	if transp, err := e.Props.Text(ical.PropTransparency); err == nil && strings.EqualFold(transp, "TRANSPARENT") {
		return false
	}
	if status, err := e.Status(); err == nil && status == ical.EventCancelled {
		return false
	}
	for _, attendee := range e.Props.Values(ical.PropAttendee) {
		if !m.isLucy(attendee.Value) {
			continue
		}
		if strings.EqualFold(attendee.Params.Get(ical.ParamParticipationStatus), "DECLINED") {
			return false
		}
	}
	return true
}

// isLucy matches an ATTENDEE value, which is a URI like
// "mailto:someone@example.com", against the configured address.
func (m Mapper) isLucy(value string) bool {
	if m.Email == "" {
		return false
	}
	addr := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), "mailto:")
	return strings.EqualFold(addr, m.Email)
}

// isAllDay reports whether DTSTART is a plain date rather than a timestamp,
// which is how iCalendar distinguishes an all-day event.
func isAllDay(e ical.Event) bool {
	prop := e.Props.Get(ical.PropDateTimeStart)
	return prop != nil && prop.ValueType() == ical.ValueDate
}

// allDayFallsOn reports whether an all-day event covers the given day. DTEND is
// exclusive for all-day events, and optional when the event covers one day.
func allDayFallsOn(e ical.Event, loc *time.Location, day time.Time) (bool, error) {
	start, err := e.DateTimeStart(loc)
	if err != nil {
		return false, fmt.Errorf("all-day event has no usable start: %w", err)
	}
	end, err := e.DateTimeEnd(loc)
	if err != nil {
		end = start.AddDate(0, 0, 1)
	}

	target := day.Format(dateOnly)
	for d := start; d.Before(end); d = d.AddDate(0, 0, 1) {
		if d.Format(dateOnly) == target {
			return true, nil
		}
	}
	return false, nil
}
