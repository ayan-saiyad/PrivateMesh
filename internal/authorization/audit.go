package authorization

import (
	"context"
	"errors"
	"log/slog"
	"sync"
)

// LogAuditSink records structured authorization events.
type LogAuditSink struct {
	logger *slog.Logger
}

// NewLogAuditSink creates a structured audit sink.
func NewLogAuditSink(logger *slog.Logger) (*LogAuditSink, error) {
	if logger == nil {
		return nil, errors.New("audit logger is required")
	}
	return &LogAuditSink{logger: logger}, nil
}

// Record writes an authorization decision without protected content.
func (s *LogAuditSink) Record(_ context.Context, event AuditEvent) error {
	s.logger.Info("authorization decision",
		"time", event.Time,
		"request_id", event.RequestID,
		"action", event.Action,
		"allowed", event.Allowed,
		"reason", event.Reason,
		"subject_hash", event.SubjectHash,
		"resource_hash", event.ResourceHash,
	)
	return nil
}

// MemoryAuditSink stores audit events for inspection and testing.
type MemoryAuditSink struct {
	mu     sync.RWMutex
	events []AuditEvent
}

// Record appends an audit event.
func (s *MemoryAuditSink) Record(_ context.Context, event AuditEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return nil
}

// Events returns a copy of all recorded events.
func (s *MemoryAuditSink) Events() []AuditEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]AuditEvent(nil), s.events...)
}
