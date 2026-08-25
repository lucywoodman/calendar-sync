// Command calendar-sync reads events from iCloud over CalDAV and pushes them to
// Genki Tracker, where they become the free/busy view Maureen schedules around.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	// Embeds the timezone database in the binary. The sync image is bare
	// Alpine, which ships none, so CALENDAR_TZ would otherwise fail with
	// "unknown time zone Europe/London" only once deployed. Carrying it here
	// rather than apk-installing tzdata keeps that working whatever the image
	// is built from later.
	_ "time/tzdata"

	"github.com/lucywoodman/calendar-sync/internal/events"
	"github.com/lucywoodman/calendar-sync/internal/genki"
	"github.com/lucywoodman/calendar-sync/internal/icloud"
	"github.com/spf13/cobra"
)

const (
	dateLayout = "2006-01-02"
	timeout    = 30 * time.Second
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "calendar-sync",
		Short:         "Read iCloud calendar events over CalDAV, push them to Genki Tracker.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newPushCmd(), newCalendarsCmd())
	return root
}

func newPushCmd() *cobra.Command {
	var startStr, endStr string
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "push",
		Short: "Fetch events and push them to Genki Tracker",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runPush(cmd.Context(), startStr, endStr, dryRun)
		},
	}
	cmd.Flags().StringVar(&startStr, "start", "", "First date to sync (YYYY-MM-DD, default: today)")
	cmd.Flags().StringVar(&endStr, "end", "", "Last date to sync (YYYY-MM-DD, default: tomorrow)")
	// Checking the mapping shouldn't need somewhere to push to. Without this,
	// working out why a day looked free meant running against a live Genki.
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print what would be pushed, and push nothing")
	return cmd
}

// newCalendarsCmd exists to make the first run diagnosable. It confirms the
// app-specific password works and prints the exact names to choose
// AWAY_CALENDAR from, without touching Genki.
func newCalendarsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "calendars",
		Short: "List the calendars this account can see, and exit",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			client, err := icloud.Connect(cmd.Context(), cfg.caldavURL, cfg.username, cfg.appPassword, timeout)
			if err != nil {
				return err
			}
			for _, cal := range client.Calendars() {
				// The component set is shown because it's what the skip
				// decision turns on, and an empty one means the server didn't
				// say rather than that it holds nothing.
				components := "unspecified"
				if len(cal.SupportedComponentSet) > 0 {
					components = strings.Join(cal.SupportedComponentSet, ",")
				}
				// Apple's Reminders lists come back from the same discovery.
				// Shown rather than hidden, so a calendar missing from this
				// list is obviously missing rather than quietly filtered.
				skipped := ""
				if !icloud.SupportsEvents(cal) {
					skipped = "  (no events - skipped)"
				}
				fmt.Printf("%s  [%s]%s\n", cal.Name, components, skipped)
			}
			return nil
		},
	}
}

// config holds the validated environment configuration for a run.
type config struct {
	caldavURL     string
	username      string
	appPassword   string
	awayCalendar  string
	loc           *time.Location
	genkiURL      string
	genkiPassword string
}

func loadConfig() (config, error) {
	cfg := config{
		caldavURL:     strings.TrimSpace(os.Getenv("ICLOUD_CALDAV_URL")),
		username:      strings.TrimSpace(os.Getenv("ICLOUD_USERNAME")),
		appPassword:   os.Getenv("ICLOUD_APP_PASSWORD"),
		awayCalendar:  strings.TrimSpace(os.Getenv("AWAY_CALENDAR")),
		genkiURL:      strings.TrimRight(os.Getenv("GENKI_URL"), "/"),
		genkiPassword: os.Getenv("GENKI_PASSWORD"),
	}
	if cfg.caldavURL == "" {
		cfg.caldavURL = icloud.DefaultURL
	}
	if cfg.username == "" || cfg.appPassword == "" {
		return config{}, fmt.Errorf("ICLOUD_USERNAME and ICLOUD_APP_PASSWORD environment variables are required")
	}
	// Genki is not checked here: `calendars` and `push --dry-run` never reach
	// it, and demanding credentials for a command that can't use them makes the
	// two commands you'd reach for while diagnosing the hardest to run.

	// Required rather than defaulted to time.Local: a container's clock is UTC
	// unless told otherwise, and Genki stores local times. Defaulting would put
	// every summer meeting in the DB an hour early and look entirely fine.
	tz := strings.TrimSpace(os.Getenv("CALENDAR_TZ"))
	if tz == "" {
		return config{}, fmt.Errorf("CALENDAR_TZ environment variable is required (e.g. Europe/London)")
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return config{}, fmt.Errorf("CALENDAR_TZ is not a known timezone: %q", tz)
	}
	cfg.loc = loc

	return cfg, nil
}

func runPush(ctx context.Context, startStr, endStr string, dryRun bool) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	if !dryRun && (cfg.genkiURL == "" || cfg.genkiPassword == "") {
		return fmt.Errorf("GENKI_URL and GENKI_PASSWORD environment variables are required (or use --dry-run)")
	}

	start, end, err := dateRange(startStr, endStr, cfg.loc)
	if err != nil {
		return err
	}

	client, err := icloud.Connect(ctx, cfg.caldavURL, cfg.username, cfg.appPassword, timeout)
	if err != nil {
		return err
	}
	if cfg.awayCalendar != "" && !hasCalendar(client, cfg.awayCalendar) {
		// Loud, because the silent version is a holiday that never registers.
		return fmt.Errorf("AWAY_CALENDAR is %q but no calendar of that name exists; run `calendar-sync calendars` to see the names", cfg.awayCalendar)
	}

	// One day past the last date requested: CalDAV ranges are half-open, and an
	// event starting at 23:00 on the last day still belongs to it.
	queryEnd := end.AddDate(0, 0, 1)
	mapper := events.Mapper{Loc: cfg.loc, Email: cfg.username}
	httpClient := &http.Client{Timeout: timeout}

	calendars := client.EventCalendars()
	if len(calendars) == 0 {
		return fmt.Errorf("no calendars hold events; run `calendar-sync calendars` to see what was discovered")
	}

	byDate := map[string][]events.Event{}
	for _, cal := range calendars {
		away := cfg.awayCalendar != "" && strings.EqualFold(cal.Name, cfg.awayCalendar)
		found, err := client.EventsBetween(ctx, cal, start, queryEnd)
		if err != nil {
			return err
		}
		// Counted before mapping, because "the server sent nothing" and "we
		// discarded everything it sent" are different faults and look identical
		// in the result.
		fmt.Printf("  %s: %d event(s) returned for %s..%s\n",
			cal.Name, len(found), start.Format(dateLayout), queryEnd.Format(dateLayout))
		for _, raw := range found {
			mapped := false
			for day := start; !day.After(end); day = day.AddDate(0, 0, 1) {
				converted, ok, err := mapper.Convert(raw, day, away)
				if err != nil {
					return err
				}
				if ok {
					mapped = true
					date := day.Format(dateLayout)
					byDate[date] = append(byDate[date], converted)
				}
			}
			// The server matched this event against the range, so landing on no
			// day means we disagree with it about when the event is.
			if !mapped && dryRun {
				fmt.Printf("    unmapped: %s\n", events.Diagnose(raw, cfg.loc))
			}
		}
	}

	for day := start; !day.After(end); day = day.AddDate(0, 0, 1) {
		date := day.Format(dateLayout)
		if dryRun {
			fmt.Printf("  %s (%d event(s), not pushed)\n", date, len(byDate[date]))
			for _, e := range byDate[date] {
				fmt.Printf("    %s\n", describe(e))
			}
			continue
		}
		if err := genki.PostEvents(httpClient, cfg.genkiURL, cfg.genkiPassword, date, byDate[date]); err != nil {
			return err
		}
		fmt.Printf("  Pushed %s (%d event(s))\n", date, len(byDate[date]))
	}

	fmt.Println("Done.")
	return nil
}

// dateRange defaults to today and tomorrow: today is what the morning briefing
// slots around, and tomorrow is what an evening one sets up.
func dateRange(startStr, endStr string, loc *time.Location) (time.Time, time.Time, error) {
	now := time.Now().In(loc)
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	if startStr != "" {
		parsed, err := parseDate(startStr, loc)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
		start = parsed
	}

	end := start.AddDate(0, 0, 1)
	if endStr != "" {
		parsed, err := parseDate(endStr, loc)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
		end = parsed
	}
	if end.Before(start) {
		return time.Time{}, time.Time{}, fmt.Errorf("--end %s is before --start %s", endStr, startStr)
	}
	return start, end, nil
}

func parseDate(value string, loc *time.Location) (time.Time, error) {
	d, err := time.ParseInLocation(dateLayout, value, loc)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid date format: %q (expected YYYY-MM-DD)", value)
	}
	return d, nil
}

// describe renders one mapped event for --dry-run. The flags are the point:
// an all-day event or one that isn't busy contributes no time, so a day full of
// birthdays is correctly reported as wide open, and that needs to be visible
// rather than looking like a sync that found nothing.
func describe(e events.Event) string {
	when := fmt.Sprintf("%s-%s", e.Start, e.End)
	if e.AllDay {
		when = "all day"
	}
	flags := []string{}
	if !e.Busy {
		flags = append(flags, "not busy")
	}
	if e.Away {
		flags = append(flags, "away")
	}
	summary := e.Summary
	if summary == "" {
		summary = "(no title)"
	}
	if len(flags) == 0 {
		return fmt.Sprintf("%-11s %s", when, summary)
	}
	return fmt.Sprintf("%-11s %s  [%s]", when, summary, strings.Join(flags, ", "))
}

// hasCalendar checks the event calendars, not every collection: naming a
// Reminders list as AWAY_CALENDAR would otherwise pass this check and then
// never mark a single day as away.
func hasCalendar(client *icloud.Client, name string) bool {
	for _, cal := range client.EventCalendars() {
		if strings.EqualFold(cal.Name, name) {
			return true
		}
	}
	return false
}
