package genki

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lucywoodman/calendar-sync/internal/events"
)

// capture stands in for Genki Tracker and records what was actually sent.
func capture(t *testing.T, status int) (*httptest.Server, *map[string]any, *string) {
	t.Helper()
	body := map[string]any{}
	auth := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("sent body is not JSON: %v", err)
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(server.Close)
	return server, &body, &auth
}

func TestPostEvents_SendsTheDateAndEvents(t *testing.T) {
	server, body, auth := capture(t, http.StatusOK)

	err := PostEvents(server.Client(), server.URL, "secret", "2026-06-14", []events.Event{
		{UID: "a", Summary: "Standup", Start: "09:30", End: "09:45", Busy: true},
	})
	if err != nil {
		t.Fatalf("post: %v", err)
	}

	if (*body)["date"] != "2026-06-14" {
		t.Errorf("date: got %v", (*body)["date"])
	}
	sent, ok := (*body)["events"].([]any)
	if !ok || len(sent) != 1 {
		t.Fatalf("events: got %v", (*body)["events"])
	}
	first := sent[0].(map[string]any)
	if first["start"] != "09:30" || first["busy"] != true {
		t.Errorf("event fields: got %v", first)
	}
	if *auth != "Bearer secret" {
		t.Errorf("auth header: got %q", *auth)
	}
}

func TestPostEvents_SendsAnEmptyArrayNotNull(t *testing.T) {
	// A day with nothing in the diary still has to be pushed, so genki records
	// the date as synced. `null` would decode to no events either way, but an
	// explicit empty array says "we looked, there was nothing" — which is the
	// distinction the whole endpoint exists to preserve.
	server, body, _ := capture(t, http.StatusOK)

	if err := PostEvents(server.Client(), server.URL, "secret", "2026-06-14", nil); err != nil {
		t.Fatalf("post: %v", err)
	}
	sent, ok := (*body)["events"].([]any)
	if !ok {
		t.Fatalf("events: want an array, got %#v", (*body)["events"])
	}
	if len(sent) != 0 {
		t.Errorf("events: want empty, got %v", sent)
	}
}

func TestPostEvents_ReportsARejection(t *testing.T) {
	// A 4xx from genki means the payload was wrong. Swallowing it would leave
	// the diary silently stale, which is indistinguishable from a free day.
	server, _, _ := capture(t, http.StatusBadRequest)

	err := PostEvents(server.Client(), server.URL, "secret", "2026-06-14", nil)
	if err == nil {
		t.Fatal("want an error when Genki rejects the push")
	}
}
