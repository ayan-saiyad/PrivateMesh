package authorization

import (
	"context"
	"sync"
)

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
