package openai

import (
	"net/http"
	"testing"

	"github.com/tidwall/gjson"
)

func TestForceHermesGPT55ReasoningEffortAddsHigh(t *testing.T) {
	headers := http.Header{"Authorization": {"Bearer hermes"}}
	raw := []byte(`{"model":"gpt-5.5"}`)

	got := forceHermesGPT55ReasoningEffort(raw, headers)

	if effort := gjson.GetBytes(got, "reasoning_effort").String(); effort != "high" {
		t.Fatalf("reasoning_effort = %q, want high", effort)
	}
}

func TestForceHermesGPT55ReasoningEffortOverridesLow(t *testing.T) {
	headers := http.Header{"Authorization": {"Bearer hermes"}}
	raw := []byte(`{"model":"gpt-5.5","reasoning_effort":"low"}`)

	got := forceHermesGPT55ReasoningEffort(raw, headers)

	if effort := gjson.GetBytes(got, "reasoning_effort").String(); effort != "high" {
		t.Fatalf("reasoning_effort = %q, want high", effort)
	}
}

func TestForceHermesGPT55ReasoningEffortUpdatesResponsesShapeWhenPresent(t *testing.T) {
	headers := http.Header{"Authorization": {"Bearer hermes"}}
	raw := []byte(`{"model":"gpt-5.5","reasoning":{"effort":"medium","summary":"auto"}}`)

	got := forceHermesGPT55ReasoningEffort(raw, headers)

	if effort := gjson.GetBytes(got, "reasoning_effort").String(); effort != "high" {
		t.Fatalf("reasoning_effort = %q, want high", effort)
	}
	if effort := gjson.GetBytes(got, "reasoning.effort").String(); effort != "high" {
		t.Fatalf("reasoning.effort = %q, want high", effort)
	}
	if summary := gjson.GetBytes(got, "reasoning.summary").String(); summary != "auto" {
		t.Fatalf("reasoning.summary = %q, want auto", summary)
	}
}

func TestForceHermesGPT55ResponsesReasoningEffortAddsHigh(t *testing.T) {
	headers := http.Header{"Authorization": {"Bearer hermes"}}
	raw := []byte(`{"model":"gpt-5.5","input":"hi"}`)

	got := forceHermesGPT55ResponsesReasoningEffort(raw, headers)

	if effort := gjson.GetBytes(got, "reasoning.effort").String(); effort != "high" {
		t.Fatalf("reasoning.effort = %q, want high", effort)
	}
}

func TestForceHermesGPT55ResponsesReasoningEffortRequiresHermes(t *testing.T) {
	headers := http.Header{"Authorization": {"Bearer honcho"}}
	raw := []byte(`{"model":"gpt-5.5","input":"hi","reasoning":{"effort":"medium"}}`)

	got := forceHermesGPT55ResponsesReasoningEffort(raw, headers)

	if effort := gjson.GetBytes(got, "reasoning.effort").String(); effort != "medium" {
		t.Fatalf("reasoning.effort = %q, want medium", effort)
	}
}

func TestForceHermesGPT55ReasoningEffortRequiresHermes(t *testing.T) {
	headers := http.Header{"Authorization": {"Bearer honcho"}}
	raw := []byte(`{"model":"gpt-5.5","reasoning_effort":"medium"}`)

	got := forceHermesGPT55ReasoningEffort(raw, headers)

	if effort := gjson.GetBytes(got, "reasoning_effort").String(); effort != "medium" {
		t.Fatalf("reasoning_effort = %q, want medium", effort)
	}
}

func TestForceHermesGPT55ReasoningEffortRequiresGPT55(t *testing.T) {
	headers := http.Header{"Authorization": {"Bearer hermes"}}
	raw := []byte(`{"model":"gpt-5.4-mini","reasoning_effort":"medium"}`)

	got := forceHermesGPT55ReasoningEffort(raw, headers)

	if effort := gjson.GetBytes(got, "reasoning_effort").String(); effort != "medium" {
		t.Fatalf("reasoning_effort = %q, want medium", effort)
	}
}
