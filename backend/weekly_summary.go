package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"aiwork/backend/internal/mcpclient"
)

// Day 20's weekly digest: this week vs last, facts only (hours, meeting
// share, merged PRs), written up by the model in the user's own profile
// tone — never a productivity label, just what happened. It reads its facts
// from the Worklog MCP server (get_analytics, get_activity_digest) exactly
// like any REST handler does, then makes one plain LLM call (no tools) to
// turn them into a short Russian summary. Triggered on demand from the
// Analytics card (generateWeeklySummaryHandler) and once more automatically
// every Friday evening (startWeeklySummarySchedule) — see weeklySummaryDue.

// WeeklyPeriodFacts is one week's numbers, exactly as read from
// get_analytics/get_activity_digest — nothing derived, nothing judged.
type WeeklyPeriodFacts struct {
	From         string  `json:"from"`
	To           string  `json:"to"`
	Hours        float64 `json:"hours"`
	MeetingHours float64 `json:"meeting_hours"`
	ActiveDays   int     `json:"active_days"`
	TopProject   string  `json:"top_project,omitempty"`
	MergedPRs    int     `json:"merged_prs"`
}

// WeeklySummaryFacts is the whole comparison the model is given — this week
// against the one before it.
type WeeklySummaryFacts struct {
	ThisWeek WeeklyPeriodFacts `json:"this_week"`
	LastWeek WeeklyPeriodFacts `json:"last_week"`
}

// WeeklySummary is one generated digest, persisted so the Analytics card has
// something to show without regenerating it on every page load.
type WeeklySummary struct {
	WeekStart   string             `json:"week_start" jsonschema:"YYYY-MM-DD, this week's Monday"`
	WeekEnd     string             `json:"week_end"`
	GeneratedAt time.Time          `json:"generated_at"`
	Text        string             `json:"text"`
	Facts       WeeklySummaryFacts `json:"facts"`
}

// WeeklySummaryStore persists the single latest summary — like
// ProfileStore, there is only ever one, so a fixed path, not an ID.
type WeeklySummaryStore struct {
	path string
}

func NewWeeklySummaryStore(path string) *WeeklySummaryStore {
	return &WeeklySummaryStore{path: path}
}

// Load returns the stored summary, or nil if none has been generated yet.
func (s *WeeklySummaryStore) Load() (*WeeklySummary, error) {
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var summary WeeklySummary
	if err := json.Unmarshal(data, &summary); err != nil {
		return nil, err
	}
	return &summary, nil
}

func (s *WeeklySummaryStore) Save(summary *WeeklySummary) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o644)
}

// WeeklySummaryGenerator gathers the facts and makes the one LLM call that
// turns them into text.
type WeeklySummaryGenerator struct {
	client  *LiteLLMClient
	worklog *mcpclient.Conn
	profile *ProfileStore
	store   *WeeklySummaryStore
}

// weekBounds is [monday, monday+7) for the week containing t, in local time.
func weekBounds(t time.Time) (monday, nextMonday time.Time) {
	daysSinceMonday := (int(t.Weekday()) + 6) % 7 // Mon=0 ... Sun=6
	d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local)
	monday = d.AddDate(0, 0, -daysSinceMonday)
	return monday, monday.AddDate(0, 0, 7)
}

// factsFor reads one week's numbers from the Worklog server. to is
// exclusive; both calls are capped at "now" by the tools themselves for the
// current, still-open week.
func (g *WeeklySummaryGenerator) factsFor(ctx context.Context, from, to time.Time) (WeeklyPeriodFacts, error) {
	facts := WeeklyPeriodFacts{From: from.Format("2006-01-02"), To: to.AddDate(0, 0, -1).Format("2006-01-02")}

	var analytics struct {
		KPI struct {
			TotalHours   float64 `json:"total_hours"`
			MeetingHours float64 `json:"meeting_hours"`
			ActiveDays   int     `json:"active_days"`
		} `json:"kpi"`
		TimeByProject []struct {
			Project string  `json:"project"`
			Hours   float64 `json:"hours"`
		} `json:"time_by_project"`
	}
	if err := g.call(ctx, "get_analytics", map[string]any{"from": from.Format("2006-01-02"), "to": to.Format("2006-01-02")}, &analytics); err != nil {
		return facts, err
	}
	facts.Hours = analytics.KPI.TotalHours
	facts.MeetingHours = analytics.KPI.MeetingHours
	facts.ActiveDays = analytics.KPI.ActiveDays
	if len(analytics.TimeByProject) > 0 {
		facts.TopProject = analytics.TimeByProject[0].Project
	}

	var digest struct {
		Total int `json:"total"`
	}
	if err := g.call(ctx, "get_activity_digest", map[string]any{
		"from": from.Format("2006-01-02"), "to": to.Format("2006-01-02"), "kinds": []string{"pr_merged"},
	}, &digest); err != nil {
		return facts, err
	}
	facts.MergedPRs = digest.Total
	return facts, nil
}

func (g *WeeklySummaryGenerator) call(ctx context.Context, tool string, args map[string]any, out any) error {
	res, err := g.worklog.Call(ctx, tool, args)
	if err != nil {
		return fmt.Errorf("worklog.%s: %w", tool, err)
	}
	if res.IsError {
		return fmt.Errorf("worklog.%s: %s", tool, res.Text)
	}
	raw := res.Structured
	if len(raw) == 0 {
		raw = json.RawMessage(res.Text)
	}
	return json.Unmarshal(raw, out)
}

// Generate gathers this week's and last week's facts, asks the model for a
// short comparison in the user's profile tone, and persists the result.
func (g *WeeklySummaryGenerator) Generate(ctx context.Context) (*WeeklySummary, error) {
	now := time.Now()
	thisMonday, nextMonday := weekBounds(now)
	lastMonday := thisMonday.AddDate(0, 0, -7)

	thisWeek, err := g.factsFor(ctx, thisMonday, nextMonday)
	if err != nil {
		return nil, err
	}
	lastWeek, err := g.factsFor(ctx, lastMonday, thisMonday)
	if err != nil {
		return nil, err
	}
	facts := WeeklySummaryFacts{ThisWeek: thisWeek, LastWeek: lastWeek}

	profile, err := g.profile.Load()
	if err != nil {
		return nil, fmt.Errorf("profile: %w", err)
	}

	factsJSON, err := json.MarshalIndent(facts, "", "  ")
	if err != nil {
		return nil, err
	}
	text, err := g.client.chatComplete(ctx, []chatMessage{
		{Role: "system", Content: weeklySummarySystemPrompt(profile)},
		{Role: "user", Content: string(factsJSON)},
	}, 0.6, 400, nil)
	if err != nil {
		return nil, fmt.Errorf("llm: %w", err)
	}

	summary := &WeeklySummary{
		WeekStart: thisMonday.Format("2006-01-02"), WeekEnd: now.Format("2006-01-02"),
		GeneratedAt: now, Text: strings.TrimSpace(text), Facts: facts,
	}
	if err := g.store.Save(summary); err != nil {
		log.Printf("weekly summary: failed to persist: %v", err)
	}
	return summary, nil
}

// weeklySummarySystemPrompt instructs the model to compare strictly from the
// given numbers, in the user's own tone, without ever calling the result
// "productive" or similar — the same principle get_analytics itself follows
// (counts and hours only, no productivity score).
func weeklySummarySystemPrompt(profile *UserProfile) string {
	var who string
	if profile != nil && profile.Name != "" {
		who = fmt.Sprintf(" Address the user by name: %s.", profile.Name)
	}
	var style string
	if profile != nil && profile.Style != "" {
		style = fmt.Sprintf(" Match this tone/style preference: %s.", profile.Style)
	}
	return `You write a short weekly work summary in Russian, comparing this week to last week, from the JSON facts given in the next message — this_week and last_week, each with hours, meeting_hours, active_days, top_project (may be empty) and merged_prs.

Rules:
- Use only these numbers. Never invent a project, a count or an event not present in the facts.
- Compare the two weeks plainly (more/less hours, more/fewer merged PRs, etc.) — no scoring, no ranking, no words like "продуктивно", "эффективно", "молодец" or any productivity judgment. This is a factual recap, not a performance review.
- If this_week is clearly partial (fewer active_days than a full week so far — check the dates), say so honestly rather than comparing it to a full last week as if they were equal.
- 2-4 sentences, warm and human, plain text (no markdown, no bullet points).
- End with a short, genuine wish for rest / a good weekend — not generic corporate cheer.` + who + style
}

func getWeeklySummaryHandler(store *WeeklySummaryStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		summary, err := store.Load()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "не удалось прочитать сводку недели")
			return
		}
		writeJSON(w, http.StatusOK, summary)
	}
}

func generateWeeklySummaryHandler(gen *WeeklySummaryGenerator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		summary, err := gen.Generate(ctx)
		if err != nil {
			log.Printf("weekly summary: generate failed: %v", err)
			writeError(w, http.StatusBadGateway, "не удалось собрать сводку недели")
			return
		}
		writeJSON(w, http.StatusOK, summary)
	}
}

// weeklySummaryWeekday/Hour: Friday evening, local time — the scheduled
// run's trigger.
const (
	weeklySummaryWeekday     = time.Friday
	weeklySummaryHour        = 18
	weeklySummaryPoll        = 30 * time.Minute
	weeklySummaryCallTimeout = 60 * time.Second
)

// weeklySummaryDue is true once it's Friday evening or later and the stored
// summary (if any) isn't already for the current week — so a restart or a
// missed tick never produces a duplicate for the same week, and a run
// triggered manually earlier in the week doesn't block Friday's from firing.
func weeklySummaryDue(now time.Time, stored *WeeklySummary) bool {
	thisMonday, _ := weekBounds(now)
	dueSince := thisMonday.AddDate(0, 0, int(weeklySummaryWeekday-time.Monday)).Add(weeklySummaryHour * time.Hour)
	if now.Before(dueSince) {
		return false
	}
	return stored == nil || stored.WeekStart != thisMonday.Format("2006-01-02")
}

// startWeeklySummarySchedule polls (no external cron dependency, same style
// as Collector.Start) and generates once the week's Friday-evening slot is
// reached and hasn't been filled yet — by a scheduled run or an earlier
// on-demand one, either way it's covered.
func startWeeklySummarySchedule(ctx context.Context, gen *WeeklySummaryGenerator) {
	go func() {
		ticker := time.NewTicker(weeklySummaryPoll)
		defer ticker.Stop()
		for {
			stored, err := gen.store.Load()
			if err != nil {
				log.Printf("weekly summary: cannot read stored summary: %v", err)
			} else if weeklySummaryDue(time.Now(), stored) {
				log.Printf("weekly summary: generating the scheduled Friday-evening summary")
				callCtx, cancel := context.WithTimeout(ctx, weeklySummaryCallTimeout)
				if _, err := gen.Generate(callCtx); err != nil {
					log.Printf("weekly summary: scheduled generation failed: %v", err)
				}
				cancel()
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}
