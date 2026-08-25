package icloud

import (
	"testing"

	"github.com/emersion/go-webdav/caldav"
)

func TestSupportsEvents_RecognisesAnEventCalendar(t *testing.T) {
	cal := caldav.Calendar{Name: "My Calendar", SupportedComponentSet: []string{"VEVENT"}}
	if !SupportsEvents(cal) {
		t.Error("want an event calendar to be included")
	}
}

func TestSupportsEvents_SkipsARemindersList(t *testing.T) {
	// Apple serves Reminders lists from the same endpoint. They hold VTODOs and
	// have no events to find.
	cal := caldav.Calendar{Name: "Shopping", SupportedComponentSet: []string{"VTODO"}}
	if SupportsEvents(cal) {
		t.Error("want a VTODO-only list skipped")
	}
}

func TestSupportsEvents_TreatsSilenceAsYes(t *testing.T) {
	// RFC 4791: an absent supported-component-set means the collection supports
	// every component. Reading absent as "no" would sync nothing at all on a
	// server that doesn't advertise it.
	if !SupportsEvents(caldav.Calendar{Name: "Quiet server"}) {
		t.Error("want an unspecified component set treated as supporting events")
	}
}

func TestEventCalendars_FiltersTheList(t *testing.T) {
	client := &Client{calendars: []caldav.Calendar{
		{Name: "My Calendar", SupportedComponentSet: []string{"VEVENT"}},
		{Name: "Family", SupportedComponentSet: []string{"VEVENT"}},
		{Name: "Reminders", SupportedComponentSet: []string{"VTODO"}},
	}}
	got := client.EventCalendars()
	if len(got) != 2 || got[0].Name != "My Calendar" || got[1].Name != "Family" {
		t.Errorf("want the two event calendars, got %v", got)
	}
	if len(client.Calendars()) != 3 {
		t.Error("want Calendars() to still show everything, for the calendars command")
	}
}
