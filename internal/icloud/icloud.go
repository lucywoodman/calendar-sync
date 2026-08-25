// Package icloud reads events from iCloud calendars over CalDAV.
package icloud

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/caldav"
)

// DefaultURL is iCloud's CalDAV entry point. An account's real calendar host is
// sharded — p12-caldav.icloud.com/<numeric id>/calendars/... — and is found by
// following discovery from here, so that per-account path is never configured
// and never ends up committed.
const DefaultURL = "https://caldav.icloud.com/"

// Client is a connected, discovered iCloud CalDAV account.
type Client struct {
	dav       *caldav.Client
	calendars []caldav.Calendar
}

// Connect authenticates with an app-specific password and discovers the
// account's calendars. iCloud rejects a normal Apple ID password here.
func Connect(ctx context.Context, endpoint, username, password string, timeout time.Duration) (*Client, error) {
	httpClient := webdav.HTTPClientWithBasicAuth(&http.Client{Timeout: timeout}, username, password)
	dav, err := caldav.NewClient(httpClient, endpoint)
	if err != nil {
		return nil, fmt.Errorf("building CalDAV client: %w", err)
	}

	principal, err := dav.FindCurrentUserPrincipal(ctx)
	if err != nil {
		return nil, fmt.Errorf("finding the account principal (is the app-specific password right?): %w", err)
	}
	homeSet, err := dav.FindCalendarHomeSet(ctx, principal)
	if err != nil {
		return nil, fmt.Errorf("finding the calendar home set: %w", err)
	}
	calendars, err := dav.FindCalendars(ctx, homeSet)
	if err != nil {
		return nil, fmt.Errorf("listing calendars: %w", err)
	}
	return &Client{dav: dav, calendars: calendars}, nil
}

// Calendars lists the discovered calendars.
func (c *Client) Calendars() []caldav.Calendar {
	return c.calendars
}

// EventsBetween returns every VEVENT in one calendar that overlaps
// [start, end), with recurrences expanded into individual occurrences.
//
// Expanding server-side is the whole game: an unexpanded weekly stand-up comes
// back as a single event with a recurrence rule, which would leave 09:30 looking
// free every day but the first.
func (c *Client) EventsBetween(ctx context.Context, cal caldav.Calendar, start, end time.Time) ([]ical.Event, error) {
	query := &caldav.CalendarQuery{
		CompRequest: caldav.CalendarCompRequest{
			Name:     ical.CompCalendar,
			AllProps: true,
			Comps:    []caldav.CalendarCompRequest{{Name: ical.CompEvent, AllProps: true}},
			Expand:   &caldav.CalendarExpandRequest{Start: start, End: end},
		},
		CompFilter: caldav.CompFilter{
			Name:  ical.CompCalendar,
			Comps: []caldav.CompFilter{{Name: ical.CompEvent, Start: start, End: end}},
		},
	}

	objects, err := c.dav.QueryCalendar(ctx, cal.Path, query)
	if err != nil {
		return nil, fmt.Errorf("querying calendar %q: %w", cal.Name, err)
	}

	var out []ical.Event
	for _, object := range objects {
		if object.Data == nil {
			continue
		}
		out = append(out, object.Data.Events()...)
	}
	return out, nil
}
