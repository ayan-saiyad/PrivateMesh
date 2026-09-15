package registry

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
)

func TestRegistryLeaseLifecycle(t *testing.T) {
	t.Parallel()

	registry := testRegistry(t)
	lease, err := registry.Register(Node{
		ID:            "node-a",
		Address:       "node-a:9000",
		CollectionIDs: []string{"beta", "alpha", "alpha"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if lease.ID != "lease-1" || lease.Revision != 1 {
		t.Fatalf("Register() lease = %+v", lease)
	}
	lease, err = registry.Heartbeat("node-a", lease.ID, 42)
	if err != nil {
		t.Fatal(err)
	}
	if lease.Revision != 1 {
		t.Fatalf("Heartbeat() revision = %d, want 1", lease.Revision)
	}
	nodes := registry.Nodes([]string{"alpha"})
	if len(nodes) != 1 || nodes[0].AppliedLogOffset != 42 {
		t.Fatalf("Nodes() = %+v", nodes)
	}
	if !reflect.DeepEqual(nodes[0].CollectionIDs, []string{"alpha", "beta"}) {
		t.Fatalf("collection IDs = %v", nodes[0].CollectionIDs)
	}
	if err := registry.Deregister("node-a", lease.ID); err != nil {
		t.Fatal(err)
	}
	if len(registry.Nodes(nil)) != 0 || registry.Revision() != 2 {
		t.Fatalf("registry was not empty after deregistration")
	}
}

func TestRegistryExpiresStaleNodes(t *testing.T) {
	t.Parallel()

	registry := testRegistry(t)
	now := time.Date(2026, time.September, 14, 12, 0, 0, 0, time.UTC)
	registry.now = func() time.Time { return now }
	lease, err := registry.Register(Node{ID: "node-a", Address: "node-a:9000"})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	if len(registry.Nodes(nil)) != 0 {
		t.Fatal("expired node remained active")
	}
	if registry.Revision() != 2 {
		t.Fatalf("Revision() = %d, want 2", registry.Revision())
	}
	if _, err := registry.Heartbeat("node-a", lease.ID, 0); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("Heartbeat() error = %v, want %v", err, ErrNodeNotFound)
	}
}

func TestRegistryRejectsWrongLease(t *testing.T) {
	t.Parallel()

	registry := testRegistry(t)
	if _, err := registry.Register(Node{ID: "node-a", Address: "node-a:9000"}); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Heartbeat("node-a", "wrong", 0); !errors.Is(err, ErrLeaseMismatch) {
		t.Fatalf("Heartbeat() error = %v, want %v", err, ErrLeaseMismatch)
	}
	if err := registry.Deregister("node-a", "wrong"); !errors.Is(err, ErrLeaseMismatch) {
		t.Fatalf("Deregister() error = %v, want %v", err, ErrLeaseMismatch)
	}
}

func TestRegistryReplacesRegistration(t *testing.T) {
	t.Parallel()

	registry := testRegistry(t)
	first, err := registry.Register(Node{ID: "node-a", Address: "old:9000"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := registry.Register(Node{ID: "node-a", Address: "new:9000"})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID || second.Revision != 2 {
		t.Fatalf("replacement leases = (%+v, %+v)", first, second)
	}
	if _, err := registry.Heartbeat("node-a", first.ID, 0); !errors.Is(err, ErrLeaseMismatch) {
		t.Fatalf("old lease heartbeat error = %v", err)
	}
	nodes := registry.Nodes(nil)
	if len(nodes) != 1 || nodes[0].Address != "new:9000" {
		t.Fatalf("Nodes() = %+v", nodes)
	}
}

func testRegistry(t *testing.T) *Registry {
	t.Helper()
	registry, err := New(30 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	nextID := 0
	registry.newID = func() (string, error) {
		nextID++
		return fmt.Sprintf("lease-%d", nextID), nil
	}
	return registry
}
