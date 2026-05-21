package usage

import (
	"testing"
	"time"
)

func TestRequestLifecycleSubscribeReceivesPublishedEvents(t *testing.T) {
	events, unsubscribe := SubscribeRequestLifecycleEvents(1)
	defer unsubscribe()

	startedAt := time.Date(2026, 5, 21, 8, 0, 0, 0, time.UTC)
	PublishRequestLifecycleEvent(RequestLifecycleEvent{
		Event:     RequestLifecycleStarted,
		RequestID: "abc12345",
		Model:     "gpt-5.5",
		StartedAt: startedAt,
	})

	select {
	case event := <-events:
		if event.Event != RequestLifecycleStarted {
			t.Fatalf("event = %q, want %q", event.Event, RequestLifecycleStarted)
		}
		if event.RequestID != "abc12345" {
			t.Fatalf("request id = %q, want abc12345", event.RequestID)
		}
		if event.Timestamp.IsZero() {
			t.Fatal("timestamp was not populated")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for lifecycle event")
	}
}

func TestRequestLifecycleSubscribeIgnoresEmptyEventName(t *testing.T) {
	events, unsubscribe := SubscribeRequestLifecycleEvents(1)
	defer unsubscribe()

	PublishRequestLifecycleEvent(RequestLifecycleEvent{RequestID: "abc12345"})

	select {
	case event := <-events:
		t.Fatalf("unexpected event: %+v", event)
	case <-time.After(20 * time.Millisecond):
	}
}
