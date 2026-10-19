package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"aiwork/backend/internal/activity"
	"aiwork/backend/internal/mcpclient"
)

type TimesheetDraft struct {
	ID               string         `json:"id"`
	Session          AuditedSession `json:"session"`
	ProjectID        string         `json:"project_id,omitempty"`
	TaskID           string         `json:"task_id,omitempty"`
	Description      string         `json:"description"`
	Billable         bool           `json:"billable"`
	Status           string         `json:"status"` // draft, confirmed, rejected, syncing, synced
	SolidtimeEntryID string         `json:"solidtime_entry_id,omitempty"`
}

type TimesheetPrepareResult struct {
	Drafts   []TimesheetDraft `json:"drafts"`
	Warnings []string         `json:"warnings,omitempty"`
}

type TimesheetSyncService struct {
	worklog       *mcpclient.Conn
	solidtime     *mcpclient.Conn
	worklogCall   mcpCall
	solidtimeCall mcpCall
	client        *LiteLLMClient
	path          string
	mu            sync.Mutex
	drafts        map[string]TimesheetDraft
}

func (s *TimesheetSyncService) solidtimeEnabled() bool {
	return s.solidtime != nil || s.solidtimeCall != nil
}

func (s *TimesheetSyncService) calls(ctx context.Context) (mcpCall, mcpCall) {
	worklog, solidtime := auditMCPCalls(ctx, s.worklog, s.solidtime)
	if s.worklogCall != nil {
		worklog = s.worklogCall
	}
	if s.solidtimeCall != nil {
		solidtime = s.solidtimeCall
	}
	return worklog, solidtime
}

func NewTimesheetSyncService(worklog, solidtime *mcpclient.Conn, client *LiteLLMClient, path string) (*TimesheetSyncService, error) {
	s := &TimesheetSyncService{worklog: worklog, solidtime: solidtime, client: client, path: path, drafts: map[string]TimesheetDraft{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var stored []TimesheetDraft
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, fmt.Errorf("decode timesheet drafts: %w", err)
	}
	for _, d := range stored {
		s.drafts[d.ID] = d
	}
	return s, nil
}

func (s *TimesheetSyncService) saveLocked() error {
	list := make([]TimesheetDraft, 0, len(s.drafts))
	for _, d := range s.drafts {
		list = append(list, d)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".timesheet-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}

func sessionDraftID(row AuditedSession) string {
	raw := row.Repo + "\x00" + row.Kind + "\x00" + row.Start.UTC().Format(time.RFC3339Nano) + "\x00" + row.End.UTC().Format(time.RFC3339Nano)
	digest := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(digest[:])
}

func (s *TimesheetSyncService) list() []TimesheetDraft {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := make([]TimesheetDraft, 0, len(s.drafts))
	for _, d := range s.drafts {
		list = append(list, d)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Session.Start.After(list[j].Session.Start) })
	return list
}

func (s *TimesheetSyncService) prepare(ctx context.Context, days int) (TimesheetPrepareResult, error) {
	if !s.solidtimeEnabled() {
		return TimesheetPrepareResult{}, fmt.Errorf("Solidtime не настроен")
	}
	if days < 1 || days > 30 {
		return TimesheetPrepareResult{}, fmt.Errorf("период должен быть от 1 до 30 дней")
	}
	to := time.Now()
	from := to.AddDate(0, 0, -days)
	callWorklog, callSolidtime := s.calls(ctx)
	audit, err := fetchAndAudit(from, to, true, callWorklog, callSolidtime)
	if err != nil {
		return TimesheetPrepareResult{}, err
	}
	if !audit.SolidtimeChecked {
		return TimesheetPrepareResult{}, fmt.Errorf("проверка дублей в Solidtime не завершилась")
	}
	if len(audit.Sessions) > 30 {
		return TimesheetPrepareResult{}, fmt.Errorf("найдено %d сессий; выберите меньший период (максимум 30 сессий)", len(audit.Sessions))
	}
	var mapping struct {
		Repos []struct {
			Repo               string `json:"repo"`
			SolidtimeProjectID string `json:"solidtime_project_id"`
		} `json:"repos"`
	}
	if err := callWorklog("get_repo_solidtime_projects", map[string]any{}, &mapping); err != nil {
		return TimesheetPrepareResult{}, err
	}
	projectByRepo := map[string]string{}
	for _, row := range mapping.Repos {
		projectByRepo[row.Repo] = row.SolidtimeProjectID
	}
	var projects struct {
		Projects []ProjectInfoView `json:"projects"`
	}
	if err := callSolidtime("list_projects", map[string]any{}, &projects); err != nil {
		return TimesheetPrepareResult{}, err
	}
	projectByID := map[string]ProjectInfoView{}
	for _, p := range projects.Projects {
		projectByID[p.ID] = p
	}
	var events struct {
		Events    []activity.Event `json:"events"`
		Truncated bool             `json:"truncated"`
	}
	if err := callWorklog("list_events", map[string]any{"from": from.Format(time.RFC3339), "to": to.Format(time.RFC3339), "limit": 500}, &events); err != nil {
		return TimesheetPrepareResult{}, err
	}
	var history struct {
		Entries []trackHistoryExample `json:"entries"`
	}
	if err := callSolidtime("list_time_entries", map[string]any{"from": from.AddDate(0, 0, -30).Format(time.RFC3339), "to": to.Format(time.RFC3339)}, &history); err != nil {
		return TimesheetPrepareResult{}, err
	}
	result := TimesheetPrepareResult{Warnings: audit.Warnings}
	if events.Truncated {
		result.Warnings = append(result.Warnings, "Список событий сокращён; проверьте текст каждого трека")
	}
	if s.client != nil {
		if err := writeAuditRemarks(ctx, s.client, &audit); err != nil {
			result.Warnings = append(result.Warnings, "AI-замечания недоступны: "+err.Error())
		}
	} else {
		result.Warnings = append(result.Warnings, "AI недоступен: описание нужно заполнить вручную")
	}
	for _, row := range audit.Sessions {
		if row.Kind != "work" {
			continue
		}
		projectID := projectByRepo[row.Repo]
		project := projectByID[projectID]
		result.Drafts = append(result.Drafts, TimesheetDraft{ID: sessionDraftID(row), Session: row, ProjectID: projectID, Billable: project.IsBillable, Status: "draft"})
	}
	if s.client != nil {
		if err := suggestTrackDescriptions(ctx, s.client, result.Drafts, events.Events, history.Entries); err != nil {
			result.Warnings = append(result.Warnings, "AI-описания недоступны: "+err.Error())
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, d := range result.Drafts {
		if old, ok := s.drafts[d.ID]; ok {
			if old.Status == "confirmed" || old.Status == "rejected" || old.Status == "syncing" || old.Status == "synced" {
				d = old
			} else if old.Description != "" {
				d.Description = old.Description
			}
		}
		s.drafts[d.ID] = d
		result.Drafts[i] = d
	}
	if err := s.saveLocked(); err != nil {
		return TimesheetPrepareResult{}, err
	}
	return result, nil
}

type trackHistoryExample struct {
	ProjectID   string `json:"project_id"`
	Description string `json:"description"`
}

func suggestTrackDescriptions(ctx context.Context, client *LiteLLMClient, drafts []TimesheetDraft, events []activity.Event, history []trackHistoryExample) error {
	var prompt strings.Builder
	for i, d := range drafts {
		fmt.Fprintf(&prompt, "[%d] repo=%s project_id=%s start=%s end=%s\n", i+1, d.Session.Repo, d.ProjectID, d.Session.Start.Format(time.RFC3339), d.Session.End.Format(time.RFC3339))
		seen := 0
		for _, e := range events {
			if e.Repo == d.Session.Repo && !e.OccurredAt.Before(d.Session.Start) && !e.OccurredAt.After(d.Session.End) && e.Title != "" {
				fmt.Fprintf(&prompt, "event: %s\n", cleanReportText(e.Title))
				seen++
				if seen == 8 {
					break
				}
			}
		}
		seen = 0
		for _, e := range history {
			if e.ProjectID == d.ProjectID && e.Description != "" {
				fmt.Fprintf(&prompt, "past_style: %s\n", cleanReportText(e.Description))
				seen++
				if seen == 3 {
					break
				}
			}
		}
	}
	const system = `Предложи описание трека на русском для каждой сессии. Используй только перечисленные event как факты; past_style показывает стиль автора, но не является фактом о новой работе. Стиль: конкретное действие, при необходимости «тема | что сделано». Если нет event, верни пустой текст. Не выдумывай проект, задачу, часы или результат. Ответь только JSON-массивом [{"index":1,"description":"..."}].`
	raw, err := client.chatComplete(ctx, []chatMessage{{Role: "system", Content: system}, {Role: "user", Content: prompt.String()}}, 0.1, 2500, nil)
	if err != nil {
		return err
	}
	var suggestions []struct {
		Index       int    `json:"index"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal([]byte(stripJSONArrayFences(raw)), &suggestions); err != nil {
		return fmt.Errorf("decode AI descriptions: %w", err)
	}
	for _, row := range suggestions {
		if row.Index < 1 || row.Index > len(drafts) {
			continue
		}
		description := strings.TrimSpace(row.Description)
		if len([]rune(description)) <= 5000 {
			drafts[row.Index-1].Description = description
		}
	}
	return nil
}

type TimesheetReviewInput struct {
	Action      string `json:"action"`
	TaskID      string `json:"task_id"`
	Description string `json:"description"`
	Billable    bool   `json:"billable"`
}

func (s *TimesheetSyncService) putLocked(d TimesheetDraft) error {
	previous, existed := s.drafts[d.ID]
	s.drafts[d.ID] = d
	if err := s.saveLocked(); err != nil {
		if existed {
			s.drafts[d.ID] = previous
		} else {
			delete(s.drafts, d.ID)
		}
		return err
	}
	return nil
}

func (s *TimesheetSyncService) review(ctx context.Context, id string, in TimesheetReviewInput) (TimesheetDraft, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.drafts[id]
	if !ok {
		return TimesheetDraft{}, fmt.Errorf("черновик не найден")
	}
	if d.Status == "syncing" || d.Status == "synced" {
		return TimesheetDraft{}, fmt.Errorf("отправленную запись нельзя изменить")
	}
	if in.Action == "reject" {
		d.Status = "rejected"
		if err := s.putLocked(d); err != nil {
			return TimesheetDraft{}, err
		}
		return d, nil
	}
	if in.Action != "confirm" {
		return TimesheetDraft{}, fmt.Errorf("неизвестное действие")
	}
	if !s.solidtimeEnabled() {
		return TimesheetDraft{}, fmt.Errorf("Solidtime не настроен")
	}
	if d.Session.Kind != "work" || len(d.Session.Flags) != 0 {
		return TimesheetDraft{}, fmt.Errorf("сессия требует проверки перед подтверждением")
	}
	if d.ProjectID == "" {
		return TimesheetDraft{}, fmt.Errorf("для репозитория не выбран проект Solidtime")
	}
	description := strings.TrimSpace(in.Description)
	if description == "" || len([]rune(description)) > 5000 {
		return TimesheetDraft{}, fmt.Errorf("описание должно содержать от 1 до 5000 символов")
	}
	var projects struct {
		Projects []ProjectInfoView `json:"projects"`
	}
	_, callSolidtime := s.calls(ctx)
	if err := callSolidtime("list_projects", map[string]any{}, &projects); err != nil {
		return TimesheetDraft{}, err
	}
	validProject := false
	for _, p := range projects.Projects {
		if p.ID == d.ProjectID && !p.IsArchived {
			validProject = true
			break
		}
	}
	if !validProject {
		return TimesheetDraft{}, fmt.Errorf("проект Solidtime не найден или архивирован")
	}
	var tasks struct {
		Tasks []TaskInfoView `json:"tasks"`
	}
	if err := callSolidtime("list_tasks", map[string]any{}, &tasks); err != nil {
		return TimesheetDraft{}, err
	}
	validTask := false
	for _, task := range tasks.Tasks {
		if task.ID == in.TaskID && task.ProjectID == d.ProjectID && !task.IsDone {
			validTask = true
			break
		}
	}
	if !validTask {
		return TimesheetDraft{}, fmt.Errorf("выберите действующую задачу этого проекта")
	}
	d.TaskID = in.TaskID
	d.Description = description
	d.Billable = in.Billable
	d.Status = "confirmed"
	if err := s.putLocked(d); err != nil {
		return TimesheetDraft{}, err
	}
	return d, nil
}

func (s *TimesheetSyncService) syncOne(ctx context.Context, id string) (TimesheetDraft, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.drafts[id]
	if !ok {
		return TimesheetDraft{}, fmt.Errorf("черновик не найден")
	}
	if d.Status != "confirmed" {
		return TimesheetDraft{}, fmt.Errorf("сначала подтвердите запись")
	}
	if !s.solidtimeEnabled() {
		return TimesheetDraft{}, fmt.Errorf("Solidtime не настроен")
	}
	from, to := d.Session.Start.Add(-24*time.Hour), d.Session.End.Add(24*time.Hour)
	callWorklog, callSolidtime := s.calls(ctx)
	audit, err := fetchAndAudit(from, to, true, callWorklog, callSolidtime)
	if err != nil {
		return TimesheetDraft{}, err
	}
	if !audit.SolidtimeChecked {
		return TimesheetDraft{}, fmt.Errorf("проверка дублей в Solidtime недоступна")
	}
	found := false
	for _, row := range audit.Sessions {
		if sessionDraftID(row) != d.ID {
			continue
		}
		if len(row.Flags) != 0 {
			return TimesheetDraft{}, fmt.Errorf("аудит изменился; обновите черновик и проверьте флаги")
		}
		found = true
		break
	}
	if !found {
		return TimesheetDraft{}, fmt.Errorf("исходная сессия изменилась; создайте новый черновик")
	}
	var mapping struct {
		Repos []struct {
			Repo               string `json:"repo"`
			SolidtimeProjectID string `json:"solidtime_project_id"`
		} `json:"repos"`
	}
	if err := callWorklog("get_repo_solidtime_projects", map[string]any{}, &mapping); err != nil {
		return TimesheetDraft{}, err
	}
	matches := false
	for _, row := range mapping.Repos {
		if row.Repo == d.Session.Repo && row.SolidtimeProjectID == d.ProjectID {
			matches = true
			break
		}
	}
	if !matches {
		return TimesheetDraft{}, fmt.Errorf("привязка репозитория к проекту изменилась")
	}
	d.Status = "syncing"
	if err := s.putLocked(d); err != nil {
		return TimesheetDraft{}, err
	}
	var created struct {
		Entry struct {
			ID string `json:"id"`
		} `json:"entry"`
	}
	err = callSolidtime("create_time_entry", map[string]any{
		"start": d.Session.Start.Format(time.RFC3339), "end": d.Session.End.Format(time.RFC3339),
		"project_id": d.ProjectID, "task_id": d.TaskID, "description": d.Description, "billable": d.Billable,
	}, &created)
	if err != nil {
		return d, fmt.Errorf("результат записи неизвестен; повторная отправка заблокирована: %w", err)
	}
	if created.Entry.ID == "" {
		return d, fmt.Errorf("Solidtime не вернул ID записи; повторная отправка заблокирована")
	}
	d.Status = "synced"
	d.SolidtimeEntryID = created.Entry.ID
	if err := s.putLocked(d); err != nil {
		return d, fmt.Errorf("запись создана в Solidtime (%s), но локальный статус не сохранился: %w", created.Entry.ID, err)
	}
	return d, nil
}

func (s *TimesheetSyncService) reconcile(ctx context.Context, id string) (TimesheetDraft, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.drafts[id]
	if !ok {
		return TimesheetDraft{}, fmt.Errorf("черновик не найден")
	}
	if d.Status != "syncing" {
		return TimesheetDraft{}, fmt.Errorf("сверка нужна только для записи с неизвестным результатом")
	}
	var entries struct {
		Entries []struct {
			ID          string    `json:"id"`
			Start       time.Time `json:"start"`
			End         time.Time `json:"end"`
			ProjectID   string    `json:"project_id"`
			TaskID      string    `json:"task_id"`
			Description string    `json:"description"`
		} `json:"entries"`
	}
	_, callSolidtime := s.calls(ctx)
	if err := callSolidtime("list_time_entries", map[string]any{"from": d.Session.Start.Add(-24 * time.Hour).Format(time.RFC3339), "to": d.Session.End.Add(24 * time.Hour).Format(time.RFC3339)}, &entries); err != nil {
		return TimesheetDraft{}, err
	}
	for _, entry := range entries.Entries {
		if entry.Start.Equal(d.Session.Start) && entry.End.Equal(d.Session.End) && entry.ProjectID == d.ProjectID && entry.TaskID == d.TaskID && entry.Description == d.Description {
			d.Status = "synced"
			d.SolidtimeEntryID = entry.ID
			if err := s.putLocked(d); err != nil {
				return TimesheetDraft{}, err
			}
			return d, nil
		}
	}
	return d, fmt.Errorf("точная запись в Solidtime не найдена; автоматическая повторная отправка остаётся заблокирована")
}
