package main

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	ical "github.com/emersion/go-ical"
	webdav "github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/caldav"

	"aiwork/backend/internal/activity"
)

// calendarAPI wraps a CalDAV client for one account: principal, calendar
// home set and calendar list are discovered once (a CalDAV round trip each)
// and cached, since they essentially never change between calls.
type calendarAPI struct {
	client   *caldav.Client
	username string

	mu        sync.Mutex
	calendars []caldav.Calendar
}

func newCalendarAPI(serverURL, username, password string) (*calendarAPI, error) {
	httpClient := webdav.HTTPClientWithBasicAuth(&http.Client{Timeout: 30 * time.Second}, username, password)
	c, err := caldav.NewClient(httpClient, serverURL)
	if err != nil {
		return nil, fmt.Errorf("caldav client: %w", err)
	}
	return &calendarAPI{client: c, username: username}, nil
}

// discover resolves and caches the account's calendars.
func (a *calendarAPI) discover(ctx context.Context) ([]caldav.Calendar, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.calendars != nil {
		return a.calendars, nil
	}
	principal, err := a.client.FindCurrentUserPrincipal(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve principal: %w", err)
	}
	homeSet, err := a.client.FindCalendarHomeSet(ctx, principal)
	if err != nil {
		return nil, fmt.Errorf("resolve calendar home set: %w", err)
	}
	calendars, err := a.client.FindCalendars(ctx, homeSet)
	if err != nil {
		return nil, fmt.Errorf("list calendars: %w", err)
	}
	sort.Slice(calendars, func(i, j int) bool { return calendars[i].Name < calendars[j].Name })
	a.calendars = calendars
	return calendars, nil
}

// events fetches every VEVENT overlapping [since, until] from the given
// calendars, expands recurring events within that window, and drops
// declined, cancelled and all-day ones. It never fails outright on one
// calendar's error — that calendar's problem becomes a warning, the rest
// still return their events.
func (a *calendarAPI) events(ctx context.Context, calendars []caldav.Calendar, since, until time.Time) ([]activity.Event, []string) {
	var (
		events   []activity.Event
		warnings []string
	)
	for _, cal := range calendars {
		objs, err := a.client.QueryCalendar(ctx, cal.Path, &caldav.CalendarQuery{
			CompRequest: caldav.CalendarCompRequest{
				Name:     "VCALENDAR",
				AllProps: true,
				Comps:    []caldav.CalendarCompRequest{{Name: "VEVENT", AllProps: true}},
			},
			CompFilter: caldav.CompFilter{
				Name: "VCALENDAR",
				Comps: []caldav.CompFilter{{
					Name: "VEVENT", Start: since, End: until,
				}},
			},
		})
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", cal.Name, err))
			continue
		}
		for _, obj := range objs {
			if obj.Data == nil {
				continue
			}
			for _, ev := range obj.Data.Events() {
				occs, err := a.expand(ev, since, until)
				if err != nil {
					warnings = append(warnings, fmt.Sprintf("%s: %s: %v", cal.Name, eventUID(ev), err))
					continue
				}
				events = append(events, occs...)
			}
		}
	}
	sort.Slice(events, func(i, j int) bool { return events[i].OccurredAt.Before(events[j].OccurredAt) })
	return events, warnings
}

// expand turns one VEVENT (which may recur) into zero or more meeting
// events, one per occurrence inside [since, until), skipping any occurrence
// that is all-day, cancelled or declined by the configured user.
func (a *calendarAPI) expand(ev ical.Event, since, until time.Time) ([]activity.Event, error) {
	if isAllDay(ev) {
		return nil, nil
	}
	if status, err := ev.Status(); err == nil && status == ical.EventCancelled {
		return nil, nil
	}
	if declinedBy(ev, a.username) {
		return nil, nil
	}

	start, err := ev.DateTimeStart(time.Local)
	if err != nil {
		return nil, fmt.Errorf("dtstart: %w", err)
	}
	end, err := ev.DateTimeEnd(time.Local)
	if err != nil || !end.After(start) {
		end = start // a malformed/zero-length event still gets a start marker, not a fabricated duration
	}
	duration := end.Sub(start)
	uid := eventUID(ev)
	title := summary(ev)

	set, err := ev.RecurrenceSet(time.Local)
	if err != nil {
		return nil, fmt.Errorf("recurrence: %w", err)
	}
	if set == nil {
		if start.Before(until) && !end.Before(since) {
			return []activity.Event{meetingEvent(uid, title, start, end)}, nil
		}
		return nil, nil
	}

	var out []activity.Event
	for _, occStart := range set.Between(since, until, true) {
		out = append(out, meetingEvent(uid+":"+occStart.UTC().Format(time.RFC3339), title, occStart, occStart.Add(duration)))
	}
	return out, nil
}

func meetingEvent(id, title string, start, end time.Time) activity.Event {
	return activity.Event{
		ID: "calendar:" + id, Source: "calendar", Kind: activity.KindMeeting,
		Title: title, OccurredAt: start.In(time.Local), EndsAt: end.In(time.Local),
	}
}

func eventUID(ev ical.Event) string {
	if p := ev.Props.Get(ical.PropUID); p != nil {
		return p.Value
	}
	return ""
}

func summary(ev ical.Event) string {
	if p := ev.Props.Get(ical.PropSummary); p != nil && p.Value != "" {
		return p.Value
	}
	return "Без названия"
}

// isAllDay is true when DTSTART carries VALUE=DATE (a whole-day entry with
// no time component), as opposed to the default DATE-TIME.
func isAllDay(ev ical.Event) bool {
	p := ev.Props.Get(ical.PropDateTimeStart)
	return p != nil && p.ValueType() == ical.ValueDate
}

// declinedBy is true when one of the event's ATTENDEE entries identifies the
// given account (by email, matched against the mailto: URI, case-insensitive)
// and its PARTSTAT is DECLINED.
func declinedBy(ev ical.Event, username string) bool {
	username = strings.ToLower(strings.TrimPrefix(strings.ToLower(username), "mailto:"))
	if username == "" {
		return false
	}
	for _, p := range ev.Props["ATTENDEE"] {
		addr := strings.ToLower(strings.TrimPrefix(strings.ToLower(p.Value), "mailto:"))
		if addr != username {
			continue
		}
		if partstat := p.Params.Get("PARTSTAT"); strings.EqualFold(partstat, "DECLINED") {
			return true
		}
	}
	return false
}
