package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

func timesheetDraftsHandler(s *TimesheetSyncService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"drafts": s.list()})
	}
}

func prepareTimesheetHandler(s *TimesheetSyncService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		days := 7
		if raw := r.URL.Query().Get("days"); raw != "" {
			var err error
			days, err = strconv.Atoi(raw)
			if err != nil || days < 1 || days > 30 {
				writeError(w, http.StatusBadRequest, "укажите от 1 до 30 дней")
				return
			}
		}
		ctx, cancel := context.WithTimeout(r.Context(), 180*time.Second)
		defer cancel()
		result, err := s.prepare(ctx, days)
		if err != nil {
			writeError(w, http.StatusBadGateway, "не удалось подготовить таймшит: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func reviewTimesheetDraftHandler(s *TimesheetSyncService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in TimesheetReviewInput
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in); err != nil {
			writeError(w, http.StatusBadRequest, "некорректное тело запроса")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		defer cancel()
		draft, err := s.review(ctx, r.PathValue("id"), in)
		if err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, draft)
	}
}

func syncTimesheetDraftHandler(s *TimesheetSyncService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		draft, err := s.syncOne(ctx, r.PathValue("id"))
		if err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, draft)
	}
}

func reconcileTimesheetDraftHandler(s *TimesheetSyncService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		defer cancel()
		draft, err := s.reconcile(ctx, r.PathValue("id"))
		if err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, draft)
	}
}
