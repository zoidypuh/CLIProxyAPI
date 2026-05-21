package usage

import (
	"strings"
	"sync"
	"time"
)

const (
	RequestLifecycleStarted   = "request.started"
	RequestLifecycleCompleted = "request.completed"
)

// RequestLifecycleEvent is a live request-level event emitted at proxy ingress and completion.
type RequestLifecycleEvent struct {
	Event           string      `json:"event"`
	Timestamp       time.Time   `json:"timestamp"`
	RequestID       string      `json:"request_id,omitempty"`
	LogFile         string      `json:"log_file,omitempty"`
	Method          string      `json:"method,omitempty"`
	Endpoint        string      `json:"endpoint,omitempty"`
	Model           string      `json:"model,omitempty"`
	Source          string      `json:"source,omitempty"`
	SessionID       string      `json:"session_id,omitempty"`
	Stream          bool        `json:"stream,omitempty"`
	MessageCount    int         `json:"message_count,omitempty"`
	MaxOutputTokens int64       `json:"max_output_tokens,omitempty"`
	StartedAt       time.Time   `json:"started_at,omitempty"`
	CompletedAt     time.Time   `json:"completed_at,omitempty"`
	DurationMs      int64       `json:"duration_ms,omitempty"`
	StatusCode      int         `json:"status_code,omitempty"`
	Failed          bool        `json:"failed,omitempty"`
	Tokens          *TokenStats `json:"tokens,omitempty"`
}

type requestLifecycleBus struct {
	mu          sync.RWMutex
	subscribers map[chan RequestLifecycleEvent]struct{}
}

var defaultRequestLifecycleBus = &requestLifecycleBus{subscribers: make(map[chan RequestLifecycleEvent]struct{})}

// PublishRequestLifecycleEvent publishes a live request lifecycle event to current subscribers.
func PublishRequestLifecycleEvent(event RequestLifecycleEvent) {
	if strings.TrimSpace(event.Event) == "" {
		return
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}
	defaultRequestLifecycleBus.publish(event)
}

// SubscribeRequestLifecycleEvents returns a channel that receives new request lifecycle events.
func SubscribeRequestLifecycleEvents(buffer int) (<-chan RequestLifecycleEvent, func()) {
	return defaultRequestLifecycleBus.subscribe(buffer)
}

func (b *requestLifecycleBus) publish(event RequestLifecycleEvent) {
	if b == nil {
		return
	}
	b.mu.RLock()
	subscribers := make([]chan RequestLifecycleEvent, 0, len(b.subscribers))
	for subscriber := range b.subscribers {
		subscribers = append(subscribers, subscriber)
	}
	b.mu.RUnlock()

	for _, subscriber := range subscribers {
		select {
		case subscriber <- event:
		default:
		}
	}
}

func (b *requestLifecycleBus) subscribe(buffer int) (<-chan RequestLifecycleEvent, func()) {
	if b == nil {
		closed := make(chan RequestLifecycleEvent)
		close(closed)
		return closed, func() {}
	}
	if buffer <= 0 {
		buffer = 1
	}
	ch := make(chan RequestLifecycleEvent, buffer)
	b.mu.Lock()
	if b.subscribers == nil {
		b.subscribers = make(map[chan RequestLifecycleEvent]struct{})
	}
	b.subscribers[ch] = struct{}{}
	b.mu.Unlock()

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subscribers, ch)
			close(ch)
			b.mu.Unlock()
		})
	}
	return ch, unsubscribe
}
