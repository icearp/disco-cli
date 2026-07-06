package aws

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/iotfleetwise"
	smithy "github.com/aws/smithy-go"
)

type stubIoTFW struct {
	listCampaignsErr error
}

func (s *stubIoTFW) ListCampaigns(_ context.Context, _ *iotfleetwise.ListCampaignsInput, _ ...func(*iotfleetwise.Options)) (*iotfleetwise.ListCampaignsOutput, error) {
	if s.listCampaignsErr != nil {
		return nil, s.listCampaignsErr
	}
	return &iotfleetwise.ListCampaignsOutput{}, nil
}

func (s *stubIoTFW) ListDecoderManifests(_ context.Context, _ *iotfleetwise.ListDecoderManifestsInput, _ ...func(*iotfleetwise.Options)) (*iotfleetwise.ListDecoderManifestsOutput, error) {
	return &iotfleetwise.ListDecoderManifestsOutput{}, nil
}

func (s *stubIoTFW) ListFleets(_ context.Context, _ *iotfleetwise.ListFleetsInput, _ ...func(*iotfleetwise.Options)) (*iotfleetwise.ListFleetsOutput, error) {
	return &iotfleetwise.ListFleetsOutput{}, nil
}

func (s *stubIoTFW) ListModelManifests(_ context.Context, _ *iotfleetwise.ListModelManifestsInput, _ ...func(*iotfleetwise.Options)) (*iotfleetwise.ListModelManifestsOutput, error) {
	return &iotfleetwise.ListModelManifestsOutput{}, nil
}

func (s *stubIoTFW) ListSignalCatalogs(_ context.Context, _ *iotfleetwise.ListSignalCatalogsInput, _ ...func(*iotfleetwise.Options)) (*iotfleetwise.ListSignalCatalogsOutput, error) {
	return &iotfleetwise.ListSignalCatalogsOutput{}, nil
}

func (s *stubIoTFW) ListStateTemplates(_ context.Context, _ *iotfleetwise.ListStateTemplatesInput, _ ...func(*iotfleetwise.Options)) (*iotfleetwise.ListStateTemplatesOutput, error) {
	return &iotfleetwise.ListStateTemplatesOutput{}, nil
}

func (s *stubIoTFW) ListVehicles(_ context.Context, _ *iotfleetwise.ListVehiclesInput, _ ...func(*iotfleetwise.Options)) (*iotfleetwise.ListVehiclesOutput, error) {
	return &iotfleetwise.ListVehiclesOutput{}, nil
}

// TestGateIoTFleetWiseClosedToAccount verifies an empty-message
// AccessDeniedException (closed-to-new-customers) yields a not-entitled
// sentinel — the account can't self-enable IoT FleetWise — so the dispatcher
// renders `(account: not entitled)` instead of N per-phase warnings.
func TestGateIoTFleetWiseClosedToAccount(t *testing.T) {
	stub := &stubIoTFW{
		listCampaignsErr: &smithy.GenericAPIError{Code: "AccessDeniedException", Message: ""},
	}
	err := gateIoTFleetWise(context.Background(), stub)
	if err == nil {
		t.Fatal("expected not-entitled sentinel; got nil")
	}
	if !errors.Is(err, errServiceNotEntitled) {
		t.Fatalf("expected errServiceNotEntitled; got %v", err)
	}
}

// TestGateIoTFleetWiseRealIAMDenial verifies a per-op IAM denial with an
// action-identifying message does NOT trip the gate — skipIfAccessDenied
// still handles it as a warning.
func TestGateIoTFleetWiseRealIAMDenial(t *testing.T) {
	stub := &stubIoTFW{
		listCampaignsErr: &smithy.GenericAPIError{
			Code:    "AccessDeniedException",
			Message: "User: arn:aws:iam::123:user/x is not authorized to perform: iotfleetwise:ListCampaigns",
		},
	}
	err := gateIoTFleetWise(context.Background(), stub)
	if err != nil {
		t.Fatalf("expected nil from gate on real IAM denial; got %v", err)
	}
}

// TestGateIoTFleetWiseSuccess verifies a successful probe call returns nil
// (not disabled) so the phase loop runs.
func TestGateIoTFleetWiseSuccess(t *testing.T) {
	stub := &stubIoTFW{}
	err := gateIoTFleetWise(context.Background(), stub)
	if err != nil {
		t.Fatalf("expected nil from gate on success; got %v", err)
	}
}
