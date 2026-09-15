package controlplane

import (
	"context"
	"testing"
	"time"

	privatemeshv1 "github.com/ayansaiyad/privatemesh/gen/go/privatemesh/v1"
	"github.com/ayansaiyad/privatemesh/internal/registry"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestServiceManagesNodeLease(t *testing.T) {
	t.Parallel()

	nodeRegistry, err := registry.New(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(nodeRegistry)
	if err != nil {
		t.Fatal(err)
	}
	registered, err := service.RegisterNode(context.Background(), &privatemeshv1.RegisterNodeRequest{
		Node: &privatemeshv1.NodeDescriptor{NodeId: "node-a", Address: "node-a:8091", CollectionIds: []string{"engineering"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if registered.GetLeaseId() == "" || registered.GetExpiresAt() == nil {
		t.Fatalf("RegisterNode() = %+v", registered)
	}
	if _, err := service.Heartbeat(context.Background(), &privatemeshv1.HeartbeatRequest{
		NodeId: "node-a", LeaseId: registered.GetLeaseId(), AppliedLogOffset: 42,
	}); err != nil {
		t.Fatal(err)
	}
	nodes := nodeRegistry.Nodes(nil)
	if len(nodes) != 1 || nodes[0].AppliedLogOffset != 42 {
		t.Fatalf("registry nodes = %+v", nodes)
	}
	if _, err := service.DeregisterNode(context.Background(), &privatemeshv1.DeregisterNodeRequest{
		NodeId: "node-a", LeaseId: registered.GetLeaseId(),
	}); err != nil {
		t.Fatal(err)
	}
	if len(nodeRegistry.Nodes(nil)) != 0 {
		t.Fatal("node remains registered after deregistration")
	}
}

func TestServiceRejectsIncorrectLease(t *testing.T) {
	t.Parallel()

	nodeRegistry, err := registry.New(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(nodeRegistry)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.RegisterNode(context.Background(), &privatemeshv1.RegisterNodeRequest{
		Node: &privatemeshv1.NodeDescriptor{NodeId: "node-a", Address: "node-a:8091"},
	}); err != nil {
		t.Fatal(err)
	}
	_, err = service.Heartbeat(context.Background(), &privatemeshv1.HeartbeatRequest{
		NodeId: "node-a", LeaseId: "incorrect",
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("Heartbeat() code = %s, want %s", status.Code(err), codes.PermissionDenied)
	}
}
