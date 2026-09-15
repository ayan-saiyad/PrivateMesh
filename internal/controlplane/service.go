// Package controlplane exposes node lease management through gRPC.
package controlplane

import (
	"context"
	"errors"
	"strings"

	privatemeshv1 "github.com/ayansaiyad/privatemesh/gen/go/privatemesh/v1"
	"github.com/ayansaiyad/privatemesh/internal/registry"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Service maps control-plane RPCs to the node registry.
type Service struct {
	privatemeshv1.UnimplementedControlPlaneServiceServer
	registry *registry.Registry
}

// New creates a control-plane service.
func New(nodeRegistry *registry.Registry) (*Service, error) {
	if nodeRegistry == nil {
		return nil, errors.New("node registry is required")
	}
	return &Service{registry: nodeRegistry}, nil
}

// RegisterNode grants a new lease to a search node.
func (s *Service) RegisterNode(_ context.Context, request *privatemeshv1.RegisterNodeRequest) (*privatemeshv1.RegisterNodeResponse, error) {
	if request == nil || request.GetNode() == nil {
		return nil, status.Error(codes.InvalidArgument, "node descriptor is required")
	}
	node := request.GetNode()
	lease, err := s.registry.Register(registry.Node{
		ID: strings.TrimSpace(node.GetNodeId()), Address: strings.TrimSpace(node.GetAddress()),
		CollectionIDs: node.GetCollectionIds(), Labels: node.GetLabels(),
	})
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return &privatemeshv1.RegisterNodeResponse{
		LeaseId: lease.ID, ExpiresAt: timestamppb.New(lease.ExpiresAt), RegistryRevision: lease.Revision,
	}, nil
}

// Heartbeat renews a node lease and updates its applied offset.
func (s *Service) Heartbeat(_ context.Context, request *privatemeshv1.HeartbeatRequest) (*privatemeshv1.HeartbeatResponse, error) {
	if request == nil {
		return nil, status.Error(codes.InvalidArgument, "heartbeat request is required")
	}
	lease, err := s.registry.Heartbeat(request.GetNodeId(), request.GetLeaseId(), request.GetAppliedLogOffset())
	if err != nil {
		return nil, registryStatus(err)
	}
	return &privatemeshv1.HeartbeatResponse{
		ExpiresAt: timestamppb.New(lease.ExpiresAt), RegistryRevision: lease.Revision,
	}, nil
}

// DeregisterNode releases a node lease.
func (s *Service) DeregisterNode(_ context.Context, request *privatemeshv1.DeregisterNodeRequest) (*privatemeshv1.DeregisterNodeResponse, error) {
	if request == nil {
		return nil, status.Error(codes.InvalidArgument, "deregister request is required")
	}
	if err := s.registry.Deregister(request.GetNodeId(), request.GetLeaseId()); err != nil {
		return nil, registryStatus(err)
	}
	return &privatemeshv1.DeregisterNodeResponse{}, nil
}

func registryStatus(err error) error {
	switch {
	case errors.Is(err, registry.ErrNodeNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, registry.ErrLeaseMismatch):
		return status.Error(codes.PermissionDenied, err.Error())
	default:
		return status.Error(codes.Internal, "registry operation failed")
	}
}
