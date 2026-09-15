package authorization

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/ayansaiyad/privatemesh/internal/identity"
)

func TestEnforcerAppliesCollectionAndDocumentPolicies(t *testing.T) {
	t.Parallel()

	store := NewMemoryStore()
	if err := store.SetCollectionPolicy("research", Policy{
		Version: "7",
		Rules: []Rule{
			{Effect: EffectAllow, Actions: []Action{ActionSearch}, Groups: []string{"engineering"}, Scopes: []string{"search.execute"}},
			{Effect: EffectDeny, Actions: []Action{ActionSearch}, Subjects: []string{"suspended-user"}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetDocumentPolicy("research", "restricted-doc", Policy{
		Version: "3",
		Rules: []Rule{
			{Effect: EffectAllow, Actions: []Action{ActionSearch}, Groups: []string{"leads"}, Scopes: []string{"search.execute"}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	audit := &MemoryAuditSink{}
	enforcer, err := NewEnforcer(store, audit, []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		principal identity.Principal
		document  string
		action    Action
		allowed   bool
		reason    string
	}{
		{
			name: "collection grant", principal: identity.Principal{Subject: "alice", Groups: []string{"engineering"}, Scopes: []string{"search.execute"}},
			document: "general-doc", action: ActionSearch, allowed: true, reason: "policy_allowed",
		},
		{
			name: "wrong group", principal: identity.Principal{Subject: "bob", Groups: []string{"sales"}, Scopes: []string{"search.execute"}},
			document: "general-doc", action: ActionSearch, reason: "collection_policy_denied",
		},
		{
			name: "missing scope", principal: identity.Principal{Subject: "alice", Groups: []string{"engineering"}},
			document: "general-doc", action: ActionSearch, reason: "collection_policy_denied",
		},
		{
			name: "deny overrides grant", principal: identity.Principal{Subject: "suspended-user", Groups: []string{"engineering"}, Scopes: []string{"search.execute"}},
			document: "general-doc", action: ActionSearch, reason: "collection_policy_denied",
		},
		{
			name: "document restriction", principal: identity.Principal{Subject: "alice", Groups: []string{"engineering"}, Scopes: []string{"search.execute"}},
			document: "restricted-doc", action: ActionSearch, reason: "document_policy_denied",
		},
		{
			name: "document grant", principal: identity.Principal{Subject: "lead", Groups: []string{"engineering", "leads"}, Scopes: []string{"search.execute"}},
			document: "restricted-doc", action: ActionSearch, allowed: true, reason: "policy_allowed",
		},
		{
			name: "unsupported action by policy", principal: identity.Principal{Subject: "alice", Groups: []string{"engineering"}, Scopes: []string{"search.execute"}},
			document: "general-doc", action: ActionRead, reason: "collection_policy_denied",
		},
		{
			name: "unauthenticated", principal: identity.Principal{},
			document: "general-doc", action: ActionSearch, reason: "unauthenticated",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			decision, err := enforcer.Authorize(context.Background(), Request{
				RequestID: "request-1", Principal: test.principal, Action: test.action,
				CollectionID: "research", DocumentID: test.document,
			})
			if err != nil {
				t.Fatal(err)
			}
			if decision.Allowed != test.allowed || decision.Reason != test.reason {
				t.Fatalf("Authorize() = %+v, want allowed=%v reason=%q", decision, test.allowed, test.reason)
			}
		})
	}
}

func TestEnforcerFiltersResultsLocally(t *testing.T) {
	t.Parallel()

	store := NewMemoryStore()
	allow := Policy{Version: "1", Rules: []Rule{{
		Effect: EffectAllow, Actions: []Action{ActionSearch}, Subjects: []string{"alice"},
	}}}
	if err := store.SetCollectionPolicy("collection-a", allow); err != nil {
		t.Fatal(err)
	}
	if err := store.SetDocumentPolicy("collection-a", "hidden", Policy{Version: "1", Rules: []Rule{{
		Effect: EffectDeny, Actions: []Action{ActionSearch}, Subjects: []string{"alice"},
	}}}); err != nil {
		t.Fatal(err)
	}
	enforcer, err := NewEnforcer(store, &MemoryAuditSink{}, []byte("0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := enforcer.Filter(
		context.Background(), "request-1", identity.Principal{Subject: "alice"}, ActionSearch,
		"collection-a", []string{"visible", "hidden"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(allowed, []string{"visible"}) {
		t.Fatalf("Filter() = %v, want [visible]", allowed)
	}
}

func TestAuditEventDoesNotExposeProtectedValues(t *testing.T) {
	t.Parallel()

	store := NewMemoryStore()
	if err := store.SetCollectionPolicy("confidential-collection", Policy{Version: "1", Rules: []Rule{{
		Effect: EffectAllow, Actions: []Action{ActionSearch}, Subjects: []string{"alice@example.test"},
	}}}); err != nil {
		t.Fatal(err)
	}
	audit := &MemoryAuditSink{}
	enforcer, err := NewEnforcer(store, audit, []byte("0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enforcer.Authorize(context.Background(), Request{
		RequestID: "request-1", Principal: identity.Principal{Subject: "alice@example.test"}, Action: ActionSearch,
		CollectionID: "confidential-collection", DocumentID: "secret-document-name",
	}); err != nil {
		t.Fatal(err)
	}
	events := audit.Events()
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(events))
	}
	encoded, err := json.Marshal(events[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, protected := range []string{"alice@example.test", "confidential-collection", "secret-document-name"} {
		if strings.Contains(string(encoded), protected) {
			t.Fatalf("audit event exposes protected value %q: %s", protected, encoded)
		}
	}
}

func TestPolicyValidation(t *testing.T) {
	t.Parallel()

	store := NewMemoryStore()
	if err := store.SetCollectionPolicy("collection-a", Policy{}); err == nil {
		t.Fatal("SetCollectionPolicy() error = nil, want validation error")
	}
	if _, err := NewEnforcer(store, &MemoryAuditSink{}, []byte("short")); err == nil {
		t.Fatal("NewEnforcer() error = nil, want short key error")
	}
}
