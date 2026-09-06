package main

import (
	"errors"
	"fmt"
	"strings"
)

// EstimateRequest is the payload accepted by POST /api/estimate.
type EstimateRequest struct {
	Task string `json:"task"`
}

// EstimateResponse is the structured estimate returned to the frontend.
// It mirrors the JSON schema requested from the LLM in llm.go.
type EstimateResponse struct {
	Summary           string   `json:"summary"`
	Category          string   `json:"category"`
	Complexity        string   `json:"complexity"`
	EstimatedHoursMin float64  `json:"estimated_hours_min"`
	EstimatedHoursMax float64  `json:"estimated_hours_max"`
	Risks             []string `json:"risks"`
	Assumptions       []string `json:"assumptions"`
}

var validComplexity = map[string]bool{"low": true, "medium": true, "high": true}

// Validate rejects model output that doesn't satisfy the application's schema,
// so a malformed LLM response never reaches the frontend as if it were trusted data.
func (e EstimateResponse) Validate() error {
	if strings.TrimSpace(e.Summary) == "" {
		return errors.New("summary must not be empty")
	}
	if strings.TrimSpace(e.Category) == "" {
		return errors.New("category must not be empty")
	}
	if !validComplexity[e.Complexity] {
		return fmt.Errorf("complexity must be low, medium, or high, got %q", e.Complexity)
	}
	if e.EstimatedHoursMin < 0 || e.EstimatedHoursMax < 0 {
		return errors.New("estimated hours must not be negative")
	}
	if e.EstimatedHoursMin > e.EstimatedHoursMax {
		return errors.New("estimated_hours_min must not exceed estimated_hours_max")
	}
	if e.Risks == nil {
		return errors.New("risks must be an array")
	}
	if e.Assumptions == nil {
		return errors.New("assumptions must be an array")
	}
	return nil
}
