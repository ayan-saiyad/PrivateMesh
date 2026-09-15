// Package authorization enforces collection and document access policies.
package authorization

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ayansaiyad/privatemesh/internal/identity"
)

// Action identifies an operation protected by policy.
type Action string

const (
	// ActionSearch allows a document to appear in search results.
	ActionSearch Action = "search"
	// ActionRead allows retrieval of a full document.
	ActionRead Action = "read"
)

// Effect is the outcome of a matching rule.
type Effect string

const (
	// EffectAllow grants an action when no deny rule matches.
	EffectAllow Effect = "allow"
	// EffectDeny denies an action and overrides allow rules.
	EffectDeny Effect = "deny"
)

// Rule matches an action, identity, and required scopes.
type Rule struct {
	Effect   Effect
	Actions  []Action
	Subjects []string
	Groups   []string
	Scopes   []string
}

// Policy is an ordered-independent set of allow and deny rules.
type Policy struct {
	Version string
	Rules   []Rule
}

// Store resolves policies at the search node that owns a document.
type Store interface {
	CollectionPolicy(ctx context.Context, collectionID string) (Policy, bool, error)
	DocumentPolicy(ctx context.Context, collectionID, documentID string) (Policy, bool, error)
}

// MemoryStore keeps validated policies in memory.
type MemoryStore struct {
	mu          sync.RWMutex
	collections map[string]Policy
	documents   map[string]Policy
}

// NewMemoryStore creates an empty policy store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		collections: make(map[string]Policy),
		documents:   make(map[string]Policy),
	}
}

// SetCollectionPolicy replaces a collection policy.
func (s *MemoryStore) SetCollectionPolicy(collectionID string, policy Policy) error {
	collectionID = strings.TrimSpace(collectionID)
	if collectionID == "" {
		return errors.New("collection ID is required")
	}
	if err := validatePolicy(policy); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.collections[collectionID] = clonePolicy(policy)
	return nil
}

// SetDocumentPolicy replaces a document-specific policy.
func (s *MemoryStore) SetDocumentPolicy(collectionID, documentID string, policy Policy) error {
	collectionID = strings.TrimSpace(collectionID)
	documentID = strings.TrimSpace(documentID)
	if collectionID == "" || documentID == "" {
		return errors.New("collection and document IDs are required")
	}
	if err := validatePolicy(policy); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.documents[resourceKey(collectionID, documentID)] = clonePolicy(policy)
	return nil
}

// CollectionPolicy returns a collection policy.
func (s *MemoryStore) CollectionPolicy(_ context.Context, collectionID string) (Policy, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	policy, ok := s.collections[strings.TrimSpace(collectionID)]
	return clonePolicy(policy), ok, nil
}

// DocumentPolicy returns a document-specific policy.
func (s *MemoryStore) DocumentPolicy(_ context.Context, collectionID, documentID string) (Policy, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	policy, ok := s.documents[resourceKey(collectionID, documentID)]
	return clonePolicy(policy), ok, nil
}

// Decision explains an authorization result without exposing protected content.
type Decision struct {
	Allowed bool
	Reason  string
}

// Request contains the fields needed for local policy evaluation.
type Request struct {
	RequestID    string
	Principal    identity.Principal
	Action       Action
	CollectionID string
	DocumentID   string
}

// AuditEvent is deliberately limited to opaque resource and subject fingerprints.
type AuditEvent struct {
	Time         time.Time `json:"time"`
	RequestID    string    `json:"request_id"`
	Action       Action    `json:"action"`
	Allowed      bool      `json:"allowed"`
	Reason       string    `json:"reason"`
	SubjectHash  string    `json:"subject_hash"`
	ResourceHash string    `json:"resource_hash"`
}

// AuditSink records authorization outcomes.
type AuditSink interface {
	Record(ctx context.Context, event AuditEvent) error
}

// Enforcer evaluates policies and emits privacy-safe audit events.
type Enforcer struct {
	store Store
	audit AuditSink
	key   []byte
	now   func() time.Time
}

// NewEnforcer creates a local policy enforcer.
func NewEnforcer(store Store, audit AuditSink, fingerprintKey []byte) (*Enforcer, error) {
	if store == nil || audit == nil {
		return nil, errors.New("policy store and audit sink are required")
	}
	if len(fingerprintKey) < 16 {
		return nil, errors.New("audit fingerprint key must be at least 16 bytes")
	}
	return &Enforcer{store: store, audit: audit, key: append([]byte(nil), fingerprintKey...), now: time.Now}, nil
}

// Authorize evaluates collection and document policies at the owning node.
func (e *Enforcer) Authorize(ctx context.Context, request Request) (Decision, error) {
	request.RequestID = strings.TrimSpace(request.RequestID)
	request.CollectionID = strings.TrimSpace(request.CollectionID)
	request.DocumentID = strings.TrimSpace(request.DocumentID)
	request.Principal.Subject = strings.TrimSpace(request.Principal.Subject)
	if request.RequestID == "" || request.CollectionID == "" || request.DocumentID == "" {
		return Decision{}, errors.New("request, collection, and document IDs are required")
	}
	if request.Action != ActionSearch && request.Action != ActionRead {
		return Decision{}, errors.New("unsupported authorization action")
	}

	decision, err := e.evaluate(ctx, request)
	if err != nil {
		return Decision{}, err
	}
	event := AuditEvent{
		Time:         e.now().UTC(),
		RequestID:    request.RequestID,
		Action:       request.Action,
		Allowed:      decision.Allowed,
		Reason:       decision.Reason,
		SubjectHash:  e.fingerprint(request.Principal.Subject),
		ResourceHash: e.fingerprint(resourceKey(request.CollectionID, request.DocumentID)),
	}
	if err := e.audit.Record(ctx, event); err != nil {
		return Decision{}, fmt.Errorf("record authorization audit event: %w", err)
	}
	return decision, nil
}

// Filter returns only candidate document IDs that pass local authorization.
func (e *Enforcer) Filter(
	ctx context.Context,
	requestID string,
	principal identity.Principal,
	action Action,
	collectionID string,
	documentIDs []string,
) ([]string, error) {
	allowed := make([]string, 0, len(documentIDs))
	for _, documentID := range documentIDs {
		decision, err := e.Authorize(ctx, Request{
			RequestID:    requestID,
			Principal:    principal,
			Action:       action,
			CollectionID: collectionID,
			DocumentID:   documentID,
		})
		if err != nil {
			return nil, err
		}
		if decision.Allowed {
			allowed = append(allowed, documentID)
		}
	}
	return allowed, nil
}

func (e *Enforcer) evaluate(ctx context.Context, request Request) (Decision, error) {
	if request.Principal.Subject == "" {
		return Decision{Reason: "unauthenticated"}, nil
	}
	collectionPolicy, found, err := e.store.CollectionPolicy(ctx, request.CollectionID)
	if err != nil {
		return Decision{}, fmt.Errorf("load collection policy: %w", err)
	}
	if !found {
		return Decision{Reason: "collection_default_deny"}, nil
	}
	if !evaluatePolicy(collectionPolicy, request.Principal, request.Action) {
		return Decision{Reason: "collection_policy_denied"}, nil
	}
	documentPolicy, found, err := e.store.DocumentPolicy(ctx, request.CollectionID, request.DocumentID)
	if err != nil {
		return Decision{}, fmt.Errorf("load document policy: %w", err)
	}
	if found && !evaluatePolicy(documentPolicy, request.Principal, request.Action) {
		return Decision{Reason: "document_policy_denied"}, nil
	}
	return Decision{Allowed: true, Reason: "policy_allowed"}, nil
}

func (e *Enforcer) fingerprint(value string) string {
	digest := hmac.New(sha256.New, e.key)
	_, _ = digest.Write([]byte(value))
	return hex.EncodeToString(digest.Sum(nil))
}

func evaluatePolicy(policy Policy, principal identity.Principal, action Action) bool {
	allowed := false
	for _, rule := range policy.Rules {
		if !ruleMatches(rule, principal, action) {
			continue
		}
		if rule.Effect == EffectDeny {
			return false
		}
		allowed = true
	}
	return allowed
}

func ruleMatches(rule Rule, principal identity.Principal, action Action) bool {
	if !containsAction(rule.Actions, action) || !containsAll(principal.Scopes, rule.Scopes) {
		return false
	}
	if len(rule.Subjects) == 0 && len(rule.Groups) == 0 {
		return true
	}
	if contains(rule.Subjects, principal.Subject) {
		return true
	}
	for _, group := range principal.Groups {
		if contains(rule.Groups, group) {
			return true
		}
	}
	return false
}

func validatePolicy(policy Policy) error {
	if strings.TrimSpace(policy.Version) == "" {
		return errors.New("policy version is required")
	}
	if len(policy.Rules) == 0 {
		return errors.New("policy must contain at least one rule")
	}
	for _, rule := range policy.Rules {
		if rule.Effect != EffectAllow && rule.Effect != EffectDeny {
			return errors.New("policy rule has an invalid effect")
		}
		if len(rule.Actions) == 0 {
			return errors.New("policy rule must contain an action")
		}
		for _, action := range rule.Actions {
			if action != ActionSearch && action != ActionRead {
				return errors.New("policy rule has an invalid action")
			}
		}
	}
	return nil
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == target {
			return true
		}
	}
	return false
}

func containsAction(values []Action, target Action) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func containsAll(values, required []string) bool {
	for _, requirement := range required {
		if !contains(values, strings.TrimSpace(requirement)) {
			return false
		}
	}
	return true
}

func clonePolicy(policy Policy) Policy {
	cloned := Policy{Version: policy.Version, Rules: make([]Rule, len(policy.Rules))}
	for index, rule := range policy.Rules {
		cloned.Rules[index] = Rule{
			Effect:   rule.Effect,
			Actions:  append([]Action(nil), rule.Actions...),
			Subjects: append([]string(nil), rule.Subjects...),
			Groups:   append([]string(nil), rule.Groups...),
			Scopes:   append([]string(nil), rule.Scopes...),
		}
	}
	return cloned
}

func resourceKey(collectionID, documentID string) string {
	return strings.TrimSpace(collectionID) + "\x00" + strings.TrimSpace(documentID)
}
