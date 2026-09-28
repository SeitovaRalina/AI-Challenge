// Command mcp-calendar is the product's own Calendar MCP server (day 20): a
// thin, stateless adapter over CalDAV, turning meetings into the product's
// normalized ActivityEvent shape (kind=meeting), exactly like mcp-github
// does for commits and PRs. It exposes two read-only tools:
//
//   - list_calendars — which calendars are visible on the account
//   - get_events     — meetings in a period, declined/cancelled/all-day
//     entries already filtered out
//
// Configuration comes from the environment (the backend passes its own):
// CALDAV_URL (defaults to Yandex's CalDAV endpoint), CALDAV_USERNAME and
// CALDAV_APP_PASSWORD (required), CALDAV_CALENDARS (optional, comma-
// separated calendar names; when empty, every calendar on the account is
// used). Stdout carries the MCP protocol, so all logging goes to stderr.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/emersion/go-webdav/caldav"
	"github.com/joho/godotenv"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"aiwork/backend/internal/activity"
)

const (
	defaultCalDAVURL = "https://caldav.yandex.ru"
	maxWindow        = 90 * 24 * time.Hour
	maxEvents        = 500
)

type GetEventsInput struct {
	From      string   `json:"from" jsonschema:"start of the period: RFC3339 timestamp with offset, or a YYYY-MM-DD date (start of that day, server local time)"`
	To        string   `json:"to,omitempty" jsonschema:"end of the period: RFC3339 timestamp, or a YYYY-MM-DD date (end of that day, server local time); defaults to now"`
	Calendars []string `json:"calendars,omitempty" jsonschema:"only these calendars (by name); defaults to every configured calendar"`
}

type GetEventsOutput struct {
	From      time.Time        `json:"from"`
	To        time.Time        `json:"to"`
	Timezone  string           `json:"timezone" jsonschema:"server local timezone used to interpret dates"`
	Calendars []string         `json:"calendars" jsonschema:"calendars that were checked"`
	Events    []activity.Event `json:"events" jsonschema:"meetings (kind=meeting), oldest first; declined, cancelled and all-day entries are already excluded"`
	Truncated bool             `json:"truncated" jsonschema:"true when more than 500 meetings matched and the rest were dropped"`
	Warnings  []string         `json:"warnings,omitempty"`
}

type ListCalendarsInput struct{}

type CalendarInfo struct {
	Name string `json:"name"`
	Path string `json:"path" jsonschema:"CalDAV collection path — not needed to call get_events, which takes calendar names"`
}

type ListCalendarsOutput struct {
	Calendars []CalendarInfo `json:"calendars"`
}

type server struct {
	api        *calendarAPI
	configured []string
}

func main() {
	log.SetOutput(os.Stderr)
	log.SetPrefix("mcp-calendar: ")
	_ = godotenv.Load()

	serverURL := strings.TrimSpace(os.Getenv("CALDAV_URL"))
	if serverURL == "" {
		serverURL = defaultCalDAVURL
	}
	username := strings.TrimSpace(os.Getenv("CALDAV_USERNAME"))
	password := strings.TrimSpace(os.Getenv("CALDAV_APP_PASSWORD"))
	if username == "" || password == "" {
		log.Fatal("CALDAV_USERNAME and CALDAV_APP_PASSWORD must be set")
	}

	api, err := newCalendarAPI(serverURL, username, password)
	if err != nil {
		log.Fatalf("caldav: %v", err)
	}
	s := &server{api: api, configured: splitList(os.Getenv("CALDAV_CALENDARS"))}

	srv := mcp.NewServer(&mcp.Implementation{Name: "aiwork-calendar", Title: "Calendar", Version: "0.1.0"}, &mcp.ServerOptions{
		Instructions: "Read-only access to the user's own calendar meetings over CalDAV, normalized into ActivityEvent records (kind=meeting).",
	})
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_calendars",
		Title:       "Доступные календари",
		Description: "List the calendars visible on the configured account.",
		Annotations: readOnly,
	}, s.listCalendars)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_events",
		Title:       "События календаря за период",
		Description: "Get the user's own meetings for a period of at most 90 days, as normalized events (kind=meeting) with a start and an end. Declined meetings, cancelled meetings and all-day entries are already excluded — every event returned is a real, attended time block. Recurring meetings are expanded into their individual occurrences inside the period.",
		Annotations: readOnly,
	}, s.getEvents)

	if err := srv.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatal(err)
	}
}

func (s *server) listCalendars(ctx context.Context, _ *mcp.CallToolRequest, _ ListCalendarsInput) (*mcp.CallToolResult, ListCalendarsOutput, error) {
	calendars, err := s.api.discover(ctx)
	if err != nil {
		return nil, ListCalendarsOutput{}, fmt.Errorf("calendar: %w", err)
	}
	out := ListCalendarsOutput{Calendars: make([]CalendarInfo, 0, len(calendars))}
	for _, c := range calendars {
		out.Calendars = append(out.Calendars, CalendarInfo{Name: c.Name, Path: c.Path})
	}
	return nil, out, nil
}

func (s *server) getEvents(ctx context.Context, _ *mcp.CallToolRequest, in GetEventsInput) (*mcp.CallToolResult, GetEventsOutput, error) {
	now := time.Now()
	since, err := activity.ParseBound(in.From, false)
	if err != nil {
		return nil, GetEventsOutput{}, fmt.Errorf("from: %w", err)
	}
	until := now
	if in.To != "" {
		if until, err = activity.ParseBound(in.To, true); err != nil {
			return nil, GetEventsOutput{}, fmt.Errorf("to: %w", err)
		}
	}
	if until.After(now) {
		until = now
	}
	if !until.After(since) {
		return nil, GetEventsOutput{}, errors.New("to must be after from")
	}
	if until.Sub(since) > maxWindow {
		return nil, GetEventsOutput{}, errors.New("the period must not exceed 90 days")
	}

	all, err := s.api.discover(ctx)
	if err != nil {
		return nil, GetEventsOutput{}, fmt.Errorf("calendar: %w", err)
	}
	names := s.configured
	if len(in.Calendars) > 0 {
		names = in.Calendars
	}
	calendars, warnings := filterCalendars(all, names)

	events, evWarnings := s.api.events(ctx, calendars, since, until)
	warnings = append(warnings, evWarnings...)

	out := GetEventsOutput{From: since, To: until, Timezone: activity.LocalZoneName(), Calendars: make([]string, 0, len(calendars)), Warnings: warnings}
	for _, c := range calendars {
		out.Calendars = append(out.Calendars, c.Name)
	}
	if len(events) > maxEvents {
		events = events[:maxEvents]
		out.Truncated = true
	}
	out.Events = events
	if out.Events == nil {
		out.Events = []activity.Event{}
	}
	log.Printf("get_events %s..%s: %d calendar(s), %d meeting(s), %d warning(s)",
		since.Format(time.RFC3339), until.Format(time.RFC3339), len(calendars), len(events), len(warnings))
	return nil, out, nil
}

func splitList(s string) []string {
	var out []string
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// filterCalendars keeps the calendars whose name is in names (case-
// insensitive); an empty names means every calendar.
func filterCalendars(all []caldav.Calendar, names []string) (matched []caldav.Calendar, warnings []string) {
	if len(names) == 0 {
		return all, nil
	}
	byName := map[string]caldav.Calendar{}
	for _, c := range all {
		byName[strings.ToLower(c.Name)] = c
	}
	picked := map[string]bool{}
	for _, raw := range names {
		name := strings.ToLower(strings.TrimSpace(raw))
		c, ok := byName[name]
		if !ok {
			warnings = append(warnings, fmt.Sprintf("%s: нет такого календаря", raw))
			continue
		}
		if !picked[c.Path] {
			picked[c.Path] = true
			matched = append(matched, c)
		}
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].Name < matched[j].Name })
	return matched, warnings
}
