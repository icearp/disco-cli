package aws

import (
	"context"
	"errors"
	"testing"

	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/omics"
)

// stubOmics returns annErr from ListAnnotationStores; the other omicsAPI methods
// are unused by the test path and return empty.
type stubOmics struct{ annErr error }

func (s stubOmics) ListAnnotationStores(context.Context, *omics.ListAnnotationStoresInput, ...func(*omics.Options)) (*omics.ListAnnotationStoresOutput, error) {
	return nil, s.annErr
}

func (stubOmics) ListConfigurations(context.Context, *omics.ListConfigurationsInput, ...func(*omics.Options)) (*omics.ListConfigurationsOutput, error) {
	return &omics.ListConfigurationsOutput{}, nil
}

func (stubOmics) ListReferenceStores(context.Context, *omics.ListReferenceStoresInput, ...func(*omics.Options)) (*omics.ListReferenceStoresOutput, error) {
	return &omics.ListReferenceStoresOutput{}, nil
}

func (stubOmics) ListRunGroups(context.Context, *omics.ListRunGroupsInput, ...func(*omics.Options)) (*omics.ListRunGroupsOutput, error) {
	return &omics.ListRunGroupsOutput{}, nil
}

func (stubOmics) ListSequenceStores(context.Context, *omics.ListSequenceStoresInput, ...func(*omics.Options)) (*omics.ListSequenceStoresOutput, error) {
	return &omics.ListSequenceStoresOutput{}, nil
}

func (stubOmics) ListVariantStores(context.Context, *omics.ListVariantStoresInput, ...func(*omics.Options)) (*omics.ListVariantStoresOutput, error) {
	return &omics.ListVariantStoresOutput{}, nil
}

func (stubOmics) ListWorkflows(context.Context, *omics.ListWorkflowsInput, ...func(*omics.Options)) (*omics.ListWorkflowsOutput, error) {
	return &omics.ListWorkflowsOutput{}, nil
}

func (stubOmics) ListWorkflowVersions(context.Context, *omics.ListWorkflowVersionsInput, ...func(*omics.Options)) (*omics.ListWorkflowVersionsOutput, error) {
	return &omics.ListWorkflowVersionsOutput{}, nil
}

func (stubOmics) ListAnnotationStoreVersions(context.Context, *omics.ListAnnotationStoreVersionsInput, ...func(*omics.Options)) (*omics.ListAnnotationStoreVersionsOutput, error) {
	return &omics.ListAnnotationStoreVersionsOutput{}, nil
}

func (stubOmics) ListReferences(context.Context, *omics.ListReferencesInput, ...func(*omics.Options)) (*omics.ListReferencesOutput, error) {
	return &omics.ListReferencesOutput{}, nil
}

func (stubOmics) ListRunCaches(context.Context, *omics.ListRunCachesInput, ...func(*omics.Options)) (*omics.ListRunCachesOutput, error) {
	return &omics.ListRunCachesOutput{}, nil
}

// In a region where HealthOmics isn't offered the endpoint answers
// "Unable to determine service/operation name" — the whole service is absent,
// so the phase returns the errServiceUnavailable sentinel (the dispatcher
// renders "(service unavailable)") with zero rows and records no scan warning.
func TestScanOmicsAnnotationStores_ReturnsUnavailableSentinel(t *testing.T) {
	st := newTestStore(t)
	warned := false
	st.OnWarn = func(store.ScanWarning) { warned = true }
	acct := newTestAccount(testAccountID)
	client := stubOmics{annErr: apiErr("AccessDeniedException", "Unable to determine service/operation name to be authorized")}

	_, total, inserted, err := scanOmicsAnnotationStores(context.Background(), client, acct, "us-east-2", st, testScanID)
	if !errors.Is(err, errServiceUnavailable) {
		t.Fatalf("want errServiceUnavailable, got %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Errorf("want (0,0), got (%d,%d)", total, inserted)
	}
	if warned {
		t.Error("region-gap must not record a scan warning")
	}
}
