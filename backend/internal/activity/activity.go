// Package activity holds the product's normalized work-activity record and
// the period parsing shared by the MCP servers that produce it (mcp-github)
// and store it (mcp-worklog), so both speak exactly the same shape.
package activity

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Kind is the normalized type of one activity event.
type Kind string

const (
	KindCommit       Kind = "commit"
	KindPROpened     Kind = "pr_opened"
	KindPRMerged     Kind = "pr_merged"
	KindReview       Kind = "review"
	KindIssueComment Kind = "issue_comment"
	// KindMeeting is a calendar event (day 20, mcp-calendar) — the only kind
	// that is an interval rather than an instant: EndsAt is set alongside
	// OccurredAt (its start).
	KindMeeting Kind = "meeting"
)

// Event is the product's source-agnostic unit of work activity.
type Event struct {
	ID         string    `json:"id" jsonschema:"stable unique id, e.g. github:commit:<sha>"`
	Source     string    `json:"source" jsonschema:"where the event comes from, e.g. github or calendar"`
	Kind       Kind      `json:"kind" jsonschema:"what happened: commit, pr_opened (PR created — says nothing about whether it is still open), pr_merged, review, issue_comment, or meeting (a calendar event)"`
	Repo       string    `json:"repo" jsonschema:"owner/repo; empty for a meeting"`
	Title      string    `json:"title" jsonschema:"commit subject, PR title, comment excerpt, or meeting title"`
	URL        string    `json:"url" jsonschema:"link to the event — github.com for GitHub kinds, the calendar's own web link for a meeting when the source provides one; empty if not"`
	OccurredAt time.Time `json:"occurred_at" jsonschema:"when the event happened (RFC3339); a meeting's start"`
	Author     string    `json:"author" jsonschema:"GitHub login of the author; empty for a meeting"`
	Ref        string    `json:"ref,omitempty" jsonschema:"branch name for commits, #number for PR/review/comment events"`
	EndsAt     time.Time `json:"ends_at,omitempty" jsonschema:"end of the event (RFC3339); only meaningful for kind=meeting — every other kind is instantaneous and leaves this zero"`
}

// ParseBound accepts an RFC3339 timestamp or a bare YYYY-MM-DD date in the
// local timezone — the start of that day for a lower bound, its end for an
// upper one, so "until: 2026-09-26" includes all of the 26th.
func ParseBound(s string, endOfDay bool) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, errors.New("is required")
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	d, err := time.ParseInLocation("2006-01-02", s, time.Local)
	if err != nil {
		return time.Time{}, fmt.Errorf("%q is neither RFC3339 nor YYYY-MM-DD", s)
	}
	if endOfDay {
		return d.AddDate(0, 0, 1).Add(-time.Second), nil
	}
	return d, nil
}

// LocalZoneName describes the local timezone, e.g. "MSK (UTC+03:00)".
func LocalZoneName() string {
	name, offset := time.Now().Zone()
	minutes := offset % 3600 / 60
	if minutes < 0 {
		minutes = -minutes
	}
	return fmt.Sprintf("%s (UTC%+03d:%02d)", name, offset/3600, minutes)
}
