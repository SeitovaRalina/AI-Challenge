package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestPersonalizedEstimateCommandAndConfidence(t *testing.T) {
	task, ok := parsePersonalizedEstimateCommand(" /ESTIMATE-PERSONALIZED  Сделать CSV экспорт  ")
	if !ok || task != "Сделать CSV экспорт" {
		t.Fatalf("command parse: %q, %v", task, ok)
	}
	if _, ok := parsePersonalizedEstimateCommand("/estimate-personalized-extra task"); ok {
		t.Fatal("accepted a different command")
	}
	chunks := []scoredChunk{{Chunk: Chunk{SessionID: "a"}}, {Chunk: Chunk{SessionID: "a"}}, {Chunk: Chunk{SessionID: "b"}}}
	if got := comparableTaskCount(chunks); got != 2 {
		t.Fatalf("counted chunks rather than chats: %d", got)
	}
	if estimateConfidence(0) != "low" || estimateConfidence(2) != "medium" || estimateConfidence(3) != "high" {
		t.Fatal("confidence thresholds changed")
	}
}

func TestPersonalizedEstimateUpdatesCardAndEvidence(t *testing.T) {
	store := NewChatStore(t.TempDir())
	chat := &Chat{ID: "personalized-test", Title: "Новый чат", Provider: ProviderLiteLLM, ContextStrategy: StrategySlidingWindow, Messages: []AgentMessage{}}
	agent := &Agent{store: store, chats: map[string]*Chat{chat.ID: chat}, profile: &UserProfile{}}
	turn := &agentTurn{
		Estimate:  &EstimateResponse{Summary: "CSV export", Category: "backend", Complexity: "low", EstimatedHoursMin: 2, EstimatedHoursMax: 4},
		Citations: []rawCitation{{ChunkID: "chunk-1", Text: "Предварительная оценка: 2–4 часа"}},
	}
	found := []scoredChunk{{Chunk: Chunk{ID: "chunk-1", SessionID: "old-chat", Text: "Предварительная оценка: 2–4 часа"}, Score: 0.9}}
	reply, err := agent.finishPersonalizedEstimateTurn(context.Background(), chat, "", "/estimate-personalized CSV export", "Уверенность: средняя", time.Now(), &LiteLLMClient{model: "test"}, turn, nil, []ToolCallRecord{{}}, found)
	if err != nil {
		t.Fatal(err)
	}
	if reply.Estimate == nil || reply.Estimate.EstimatedHoursMax != 4 || chat.EstimateRevisions != 1 {
		t.Fatalf("card not updated: reply=%+v revisions=%d", reply.Estimate, chat.EstimateRevisions)
	}
	if len(reply.ToolCalls) != 1 || len(reply.Sources) != 1 || len(reply.Citations) != 1 || !reply.Citations[0].Verified {
		t.Fatalf("immediate evidence missing: tools=%d sources=%d citations=%+v", len(reply.ToolCalls), len(reply.Sources), reply.Citations)
	}
	restored, err := store.LoadAll()
	if err != nil || len(restored) != 1 || restored[0].Estimate == nil || !strings.Contains(restored[0].Messages[1].Content, "Уверенность") {
		t.Fatalf("estimate or answer not persisted: chats=%d err=%v", len(restored), err)
	}
}
