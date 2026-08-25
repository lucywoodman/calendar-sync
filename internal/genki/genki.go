// Package genki posts a day's calendar events to the Genki Tracker API.
package genki

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/lucywoodman/calendar-sync/internal/events"
)

// payload is the body for POST /api/calendar: a date and the full set of events
// for it. The endpoint replaces the date's events wholesale, so a meeting
// deleted in the calendar disappears on the next run.
type payload struct {
	Date   string         `json:"date"`
	Events []events.Event `json:"events"`
}

// PostEvents sends one day's events to Genki Tracker.
//
// Always called, even for a day with no events: genki records every date that
// has actually been synced, and that record is the only thing distinguishing an
// empty diary from a sync that never ran.
func PostEvents(client *http.Client, url, password, date string, evs []events.Event) error {
	if evs == nil {
		evs = []events.Event{}
	}
	return post(client, url+"/api/calendar", password, payload{Date: date, Events: evs})
}

// post marshals payload and POSTs it with bearer auth, returning an error for
// any non-2xx response. endpoint is the full URL including path.
func post(client *http.Client, endpoint, password string, body any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encoding payload: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("building Genki request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+password)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("posting to Genki: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Genki API returned %d: %s", resp.StatusCode, respBody)
	}
	return nil
}
