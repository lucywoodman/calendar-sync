// Package icloud reads events from iCloud calendars over CalDAV.
package icloud

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
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

	// ExpandRecurrences asks the server to return each occurrence of a
	// recurring event separately. On by default, because without it a weekly
	// stand-up arrives as one event with a rule attached and 09:30 looks free
	// every week but the first. Switchable so that a server which mishandles
	// the request can be identified rather than guessed at.
	ExpandRecurrences bool
}

// Connect authenticates with an app-specific password and discovers the
// account's calendars. iCloud rejects a normal Apple ID password here.
func Connect(ctx context.Context, endpoint, username, password string, timeout time.Duration, debug bool) (*Client, error) {
	base := &http.Client{Timeout: timeout}
	if debug {
		base.Transport = debugTransport{inner: http.DefaultTransport}
	}
	httpClient := webdav.HTTPClientWithBasicAuth(base, username, password)
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
	return &Client{dav: dav, calendars: calendars, ExpandRecurrences: true}, nil
}

// Calendars lists every discovered collection, including ones that hold no
// events. Only `calendar-sync calendars` wants this; a sync wants EventCalendars.
func (c *Client) Calendars() []caldav.Calendar {
	return c.calendars
}

// EventCalendars returns just the collections that hold events.
//
// Apple serves Reminders lists from the same endpoint as calendars, and they're
// VTODO collections — asking one for VEVENTs is wasted work, and depending on
// the server, an error rather than an empty result.
func (c *Client) EventCalendars() []caldav.Calendar {
	var out []caldav.Calendar
	for _, cal := range c.calendars {
		if SupportsEvents(cal) {
			out = append(out, cal)
		}
	}
	return out
}

// SupportsEvents reports whether a collection holds events. An empty
// SupportedComponentSet means the server didn't say, which RFC 4791 defines as
// supporting every component — so absent has to mean yes, not no.
func SupportsEvents(cal caldav.Calendar) bool {
	if len(cal.SupportedComponentSet) == 0 {
		return true
	}
	return slices.Contains(cal.SupportedComponentSet, ical.CompEvent)
}

// EventsBetween returns every VEVENT in one calendar that overlaps
// [start, end), with recurrences expanded into individual occurrences.
//
// Expanding server-side is the whole game: an unexpanded weekly stand-up comes
// back as a single event with a recurrence rule, which would leave 09:30 looking
// free every day but the first.
// QueryResult is what one query returned. Objects is kept alongside the events
// because an empty Events slice has two very different causes - the server
// matched nothing, or it matched objects and sent them with no VEVENT inside -
// and they need telling apart.
type QueryResult struct {
	Objects int
	Events  []ical.Event
}

func (c *Client) EventsBetween(ctx context.Context, cal caldav.Calendar, start, end time.Time) (QueryResult, error) {
	// Ask for the whole object rather than naming the components wanted.
	// Spelling out <comp name="VEVENT"> got VEVENTs back with no properties at
	// all from iCloud - the right events, entirely empty - so allcomp it is.
	// Nothing here needs less than the full event anyway.
	compRequest := caldav.CalendarCompRequest{
		Name:     ical.CompCalendar,
		AllProps: true,
		AllComps: true,
	}
	if c.ExpandRecurrences {
		compRequest.Expand = &caldav.CalendarExpandRequest{Start: start, End: end}
	}

	query := &caldav.CalendarQuery{
		CompRequest: compRequest,
		CompFilter: caldav.CompFilter{
			Name:  ical.CompCalendar,
			Comps: []caldav.CompFilter{{Name: ical.CompEvent, Start: start, End: end}},
		},
	}

	objects, err := c.dav.QueryCalendar(ctx, cal.Path, query)
	if err != nil {
		return QueryResult{}, fmt.Errorf("querying calendar %q: %w", cal.Name, err)
	}

	result := QueryResult{Objects: len(objects)}
	for _, object := range objects {
		if object.Data == nil {
			continue
		}
		result.Events = append(result.Events, object.Data.Events()...)
	}
	return result, nil
}

// debugTransport reports what actually went over the wire.
//
// Request bodies are the CalDAV query XML, which contains no personal data and
// is safe to paste anywhere. Responses are reported as a status and a byte
// count only — that body is Lucy's diary, and the size is enough to tell an
// empty multistatus from a full one.
type debugTransport struct{ inner http.RoundTripper }

func (d debugTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var reqBody []byte
	if req.Body != nil {
		reqBody, _ = io.ReadAll(req.Body)
		req.Body = io.NopCloser(bytes.NewReader(reqBody))
	}

	resp, err := d.inner.RoundTrip(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[http] %s %s -> %v\n", req.Method, req.URL.Path, err)
		return resp, err
	}

	respBody, _ := io.ReadAll(resp.Body)
	resp.Body = io.NopCloser(bytes.NewReader(respBody))
	fmt.Fprintf(os.Stderr, "[http] %s %s -> %s (%d bytes)\n",
		req.Method, req.URL.Path, resp.Status, len(respBody))
	if len(reqBody) > 0 {
		fmt.Fprintf(os.Stderr, "[http] sent:\n%s\n", reqBody)
	}
	return resp, nil
}
