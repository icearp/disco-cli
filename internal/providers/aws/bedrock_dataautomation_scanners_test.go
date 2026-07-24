package aws

import (
	"context"
	"testing"

	"github.com/icearp/disco-cli/store"
	bda "github.com/aws/aws-sdk-go-v2/service/bedrockdataautomation"
	bdatypes "github.com/aws/aws-sdk-go-v2/service/bedrockdataautomation/types"
)

type stubBedrockDA struct {
	accountBlueprints []bdatypes.BlueprintSummary
	serviceBlueprints []bdatypes.BlueprintSummary
	projects          []bdatypes.DataAutomationProjectSummary
	libraries         []bdatypes.DataAutomationLibrarySummary
}

func (s *stubBedrockDA) ListBlueprints(_ context.Context, in *bda.ListBlueprintsInput, _ ...func(*bda.Options)) (*bda.ListBlueprintsOutput, error) {
	if in.ResourceOwner == bdatypes.ResourceOwnerService {
		return &bda.ListBlueprintsOutput{Blueprints: s.serviceBlueprints}, nil
	}
	return &bda.ListBlueprintsOutput{Blueprints: s.accountBlueprints}, nil
}

func (s *stubBedrockDA) ListDataAutomationProjects(_ context.Context, _ *bda.ListDataAutomationProjectsInput, _ ...func(*bda.Options)) (*bda.ListDataAutomationProjectsOutput, error) {
	return &bda.ListDataAutomationProjectsOutput{Projects: s.projects}, nil
}

func (s *stubBedrockDA) ListDataAutomationLibraries(_ context.Context, _ *bda.ListDataAutomationLibrariesInput, _ ...func(*bda.Options)) (*bda.ListDataAutomationLibrariesOutput, error) {
	return &bda.ListDataAutomationLibrariesOutput{Libraries: s.libraries}, nil
}

func TestScanBedrockDataAutomation(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	acctBpArn := "arn:aws:bedrock:" + testRegion + ":" + testAccountID + ":blueprint/bp-acct"
	svcBpArn := "arn:aws:bedrock:" + testRegion + ":aws:blueprint/bp-sample"
	projArn := "arn:aws:bedrock:" + testRegion + ":" + testAccountID + ":data-automation-project/proj-1"
	libArn := "arn:aws:bedrock:" + testRegion + ":" + testAccountID + ":data-automation-library/lib-1"
	acctBp, svcBp, projName, libName := "bp", "sample", "proj", "lib"

	stub := &stubBedrockDA{
		accountBlueprints: []bdatypes.BlueprintSummary{{BlueprintArn: &acctBpArn, BlueprintName: &acctBp}},
		serviceBlueprints: []bdatypes.BlueprintSummary{{BlueprintArn: &svcBpArn, BlueprintName: &svcBp}},
		projects:          []bdatypes.DataAutomationProjectSummary{{ProjectArn: &projArn, ProjectName: &projName}},
		libraries:         []bdatypes.DataAutomationLibrarySummary{{LibraryArn: &libArn, LibraryName: &libName}},
	}
	total, _, err := scanBedrockDataAutomation(context.Background(), stub, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 4 {
		t.Fatalf("total=%d want 4", total)
	}

	// Account-owned blueprint is a normal resource; SERVICE-owned is the AWS
	// sample catalog (ManagedByProvider).
	acctRow, err := st.GetResource(store.ResourceID("aws", acct.ID, acctBpArn))
	if err != nil {
		t.Fatalf("account blueprint missing: %v", err)
	}
	if acctRow.ManagedByProvider {
		t.Error("account-owned blueprint should not be ManagedByProvider")
	}
	svcRow, err := st.GetResource(store.ResourceID("aws", acct.ID, svcBpArn))
	if err != nil {
		t.Fatalf("service blueprint missing: %v", err)
	}
	if !svcRow.ManagedByProvider {
		t.Error("service-owned blueprint should be ManagedByProvider")
	}
	for _, tc := range []struct{ rtype, arn string }{
		{TypeBedrockDataAutomationProject, projArn},
		{TypeBedrockDataAutomationLibrary, libArn},
	} {
		if _, err := st.GetResource(store.ResourceID("aws", acct.ID, tc.arn)); err != nil {
			t.Errorf("%s missing: %v", tc.rtype, err)
		}
	}
}

// pagedBlueprints returns ACCOUNT blueprints across two pages to exercise the
// manual NextToken loop, then an empty SERVICE page.
type pagedBlueprints struct {
	stubBedrockDA
	page1, page2 []bdatypes.BlueprintSummary
}

func (s *pagedBlueprints) ListBlueprints(_ context.Context, in *bda.ListBlueprintsInput, _ ...func(*bda.Options)) (*bda.ListBlueprintsOutput, error) {
	if in.ResourceOwner == bdatypes.ResourceOwnerService {
		return &bda.ListBlueprintsOutput{}, nil
	}
	if in.NextToken == nil {
		tok := "p2"
		return &bda.ListBlueprintsOutput{Blueprints: s.page1, NextToken: &tok}, nil
	}
	return &bda.ListBlueprintsOutput{Blueprints: s.page2}, nil
}

func TestScanBedrockBlueprints_Paginates(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	a1 := "arn:aws:bedrock:" + testRegion + ":" + testAccountID + ":blueprint/bp-1"
	a2 := "arn:aws:bedrock:" + testRegion + ":" + testAccountID + ":blueprint/bp-2"
	n1, n2 := "bp1", "bp2"
	stub := &pagedBlueprints{
		page1: []bdatypes.BlueprintSummary{{BlueprintArn: &a1, BlueprintName: &n1}},
		page2: []bdatypes.BlueprintSummary{{BlueprintArn: &a2, BlueprintName: &n2}},
	}
	total, _, err := scanBedrockBlueprints(context.Background(), stub, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 2 {
		t.Fatalf("total=%d want 2 (both pages)", total)
	}
	for _, arn := range []string{a1, a2} {
		if _, err := st.GetResource(store.ResourceID("aws", acct.ID, arn)); err != nil {
			t.Errorf("blueprint %s missing: %v", arn, err)
		}
	}
}

func TestScanBedrockDataAutomation_Empty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	total, _, err := scanBedrockDataAutomation(context.Background(), &stubBedrockDA{}, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 0 {
		t.Errorf("total=%d want 0", total)
	}
}
