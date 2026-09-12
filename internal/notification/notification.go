package notification

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

type Level string

const (
	LevelInfo  Level = "info"
	LevelError Level = "error"
)

type Event struct {
	Level                 Level
	Component             string
	Code                  string
	Message               string
	At                    time.Time
	ExcludeConversationID string
}

type Publisher interface {
	Publish(Event)
}

type TargetProvider interface {
	NotificationTargets() []string
}

type Sender interface {
	SendNotification(context.Context, string, string) error
}

type Dispatcher struct {
	targets TargetProvider
	sender  Sender
	logger  *slog.Logger
	queue   chan Event
}

func NewDispatcher(targets TargetProvider, sender Sender, logger *slog.Logger, queueSize int) (*Dispatcher, error) {
	if targets == nil || sender == nil {
		return nil, fmt.Errorf("notification targets and sender are required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	if queueSize <= 0 {
		queueSize = 64
	}
	return &Dispatcher{targets: targets, sender: sender, logger: logger, queue: make(chan Event, queueSize)}, nil
}

// Publish never blocks the caller. Operational notifications must not become
// part of the success path of the component being observed.
func (d *Dispatcher) Publish(event Event) {
	if event.At.IsZero() {
		event.At = time.Now().UTC()
	}
	select {
	case d.queue <- event:
	default:
		d.logger.Warn("notification queue full", "component", event.Component, "code", event.Code)
	}
}

func (d *Dispatcher) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-d.queue:
			d.dispatch(ctx, event)
		}
	}
}

func (d *Dispatcher) dispatch(ctx context.Context, event Event) {
	text := format(event)
	for _, conversationID := range d.targets.NotificationTargets() {
		if conversationID == event.ExcludeConversationID {
			continue
		}
		if err := d.sender.SendNotification(ctx, conversationID, text); err != nil {
			d.logger.ErrorContext(ctx, "send operator notification", "err", err, "conversation_id", conversationID, "component", event.Component, "code", event.Code)
		}
	}
}

func format(event Event) string {
	level := strings.ToUpper(string(event.Level))
	if level == "" {
		level = "INFO"
	}
	component := strings.TrimSpace(event.Component)
	if component == "" {
		component = "system"
	}
	code := strings.TrimSpace(event.Code)
	if code != "" {
		code = "/" + code
	}
	return fmt.Sprintf("[%s] %s%s\n%s", level, component, code, strings.TrimSpace(event.Message))
}
