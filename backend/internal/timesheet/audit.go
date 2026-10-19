package timesheet

import "time"

type Flag string

const (
	PossibleDuplicate        Flag = "possible_duplicate"
	UnmappedSolidtimeProject Flag = "unmapped_solidtime_project"
	SuspiciousDuration       Flag = "suspicious_duration"
	MaxPlausibleDuration          = 10 * time.Hour
)

type Session struct {
	Start      time.Time `json:"start"`
	End        time.Time `json:"end"`
	Kind       string    `json:"kind"`
	Repo       string    `json:"repo,omitempty"`
	Title      string    `json:"title,omitempty"`
	EventCount int       `json:"event_count"`
	Project    string    `json:"project,omitempty"`
}

type ExistingEntry struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// FlagSession is independent of MCP, LLM and HTTP transport.
func FlagSession(s Session, existing []ExistingEntry, mapped map[string]bool) []Flag {
	var flags []Flag
	if !s.End.After(s.Start) || s.End.Sub(s.Start) > MaxPlausibleDuration {
		flags = append(flags, SuspiciousDuration)
	}
	if s.Kind == "work" && !mapped[s.Repo] {
		flags = append(flags, UnmappedSolidtimeProject)
	}
	for _, e := range existing {
		if s.Start.Before(e.End) && e.Start.Before(s.End) {
			flags = append(flags, PossibleDuplicate)
			break
		}
	}
	return flags
}
