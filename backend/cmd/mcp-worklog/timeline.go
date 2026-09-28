package main

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"aiwork/backend/internal/activity"
)

// get_day_timeline is day 20's other read tool: one day, laid out as
// non-overlapping blocks (work in a repository, or a meeting) — the data a
// Gantt-style day view draws directly, and what the "what did I do
// yesterday" chat flow reads instead of reconstructing the day from raw
// sessions itself.

type GetDayTimelineInput struct {
	Date string `json:"date" jsonschema:"the day, as YYYY-MM-DD (local time)"`
}

type TimelineBlock struct {
	Start   time.Time `json:"start"`
	End     time.Time `json:"end"`
	Kind    string    `json:"kind" jsonschema:"work or meeting"`
	Repo    string    `json:"repo,omitempty"`
	Project string    `json:"project,omitempty"`
	Titles  []string  `json:"titles,omitempty" jsonschema:"meeting title(s); several when overlapping meetings were merged into one block"`
}

type GetDayTimelineOutput struct {
	Date     string          `json:"date"`
	Weekday  string          `json:"weekday"`
	Timezone string          `json:"timezone"`
	Blocks   []TimelineBlock `json:"blocks" jsonschema:"non-overlapping, oldest first; a meeting always wins time it shares with a work block"`
	Warnings []string        `json:"warnings,omitempty"`
}

func (s *server) getDayTimeline(ctx context.Context, _ *mcp.CallToolRequest, in GetDayTimelineInput) (*mcp.CallToolResult, GetDayTimelineOutput, error) {
	day, err := time.ParseInLocation("2006-01-02", in.Date, time.Local)
	if err != nil {
		return nil, GetDayTimelineOutput{}, fmt.Errorf("date: %w", err)
	}
	dayStart := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.Local)
	dayEnd := dayStart.AddDate(0, 0, 1)
	now := time.Now()
	if dayEnd.After(now) {
		dayEnd = now
	}
	if !dayEnd.After(dayStart) {
		return nil, GetDayTimelineOutput{}, fmt.Errorf("date %s is in the future", in.Date)
	}

	f, warnings, err := s.resolveSessionFilter(ctx, dayStart.Format(time.RFC3339), dayEnd.Format(time.RFC3339), nil, "", maxDigestWindow)
	if err != nil {
		return nil, GetDayTimelineOutput{}, err
	}
	rows, err := s.store.sessions(ctx, f)
	if err != nil {
		return nil, GetDayTimelineOutput{}, err
	}

	blocks := resolveIntervals(rows, dayStart, dayEnd)
	sort.Slice(blocks, func(i, j int) bool { return blocks[i].Start.Before(blocks[j].Start) })
	out := GetDayTimelineOutput{
		Date: dayStart.Format("2006-01-02"), Weekday: weekdays[dayStart.Weekday()],
		Timezone: activity.LocalZoneName(), Warnings: warnings, Blocks: make([]TimelineBlock, len(blocks)),
	}
	for i, b := range blocks {
		out.Blocks[i] = TimelineBlock{Start: b.Start, End: b.End, Kind: b.Kind, Repo: b.Repo, Project: b.Project, Titles: b.Titles}
	}
	return nil, out, nil
}
