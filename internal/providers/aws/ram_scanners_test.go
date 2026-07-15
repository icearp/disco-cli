package aws

import (
	"context"
	"testing"

	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/ram"
	ramtypes "github.com/aws/aws-sdk-go-v2/service/ram/types"
)

type stubRAM struct {
	perms []ramtypes.ResourceSharePermissionSummary
}

func (s *stubRAM) GetResourceShares(_ context.Context, _ *ram.GetResourceSharesInput, _ ...func(*ram.Options)) (*ram.GetResourceSharesOutput, error) {
	return &ram.GetResourceSharesOutput{}, nil
}

func (s *stubRAM) ListPermissions(_ context.Context, _ *ram.ListPermissionsInput, _ ...func(*ram.Options)) (*ram.ListPermissionsOutput, error) {
	return &ram.ListPermissionsOutput{Permissions: s.perms}, nil
}

// The AWS-managed RAM permission catalogue has region-less ARNs. scanRAM runs
// per region, so without dedup the same managed ARN collides on the
// region-excluded natural key across concurrent per-region upserts (SQLSTATE
// 23505). Assert it's emitted once (region "global") from us-east-1 and
// skipped elsewhere, while customer-managed permissions stay per-region.
func TestScanRAMPermissions_ManagedCatalogueDedup(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	managedARN := "arn:aws:ram::aws:permission/AWSRAMDefaultPermissionVPC"
	managedName := "AWSRAMDefaultPermissionVPC"
	custEastARN := "arn:aws:ram:us-east-1:" + acct.ID + ":permission/my-east-perm"
	custEastName := "my-east-perm"
	custWestARN := "arn:aws:ram:us-west-2:" + acct.ID + ":permission/my-west-perm"
	custWestName := "my-west-perm"

	managed := ramtypes.ResourceSharePermissionSummary{Arn: &managedARN, Name: &managedName}

	// us-east-1: managed catalogue entry + a regional customer permission.
	eastStub := &stubRAM{perms: []ramtypes.ResourceSharePermissionSummary{
		managed,
		{Arn: &custEastARN, Name: &custEastName},
	}}
	if _, _, err := scanRAMPermissions(context.Background(), eastStub, acct, "us-east-1", st, testScanID); err != nil {
		t.Fatalf("us-east-1 scan: %v", err)
	}

	// us-west-2: same managed entry (must be skipped, not re-upserted) + a
	// different regional customer permission.
	westStub := &stubRAM{perms: []ramtypes.ResourceSharePermissionSummary{
		managed,
		{Arn: &custWestARN, Name: &custWestName},
	}}
	if _, _, err := scanRAMPermissions(context.Background(), westStub, acct, "us-west-2", st, testScanID); err != nil {
		t.Fatalf("us-west-2 scan (managed-key collision regression): %v", err)
	}

	// Managed row exists exactly once and is tagged global.
	mgr, err := st.GetResource(store.ResourceID("aws", acct.ID, managedARN))
	if err != nil {
		t.Fatalf("managed permission row missing: %v", err)
	}
	if mgr.Region == nil || *mgr.Region != "global" {
		t.Errorf("managed permission region = %v, want global", mgr.Region)
	}
	if !mgr.ManagedByProvider {
		t.Error("managed permission should set ManagedByProvider")
	}

	// Both customer permissions present and regional.
	for _, c := range []struct{ arn, region string }{
		{custEastARN, "us-east-1"},
		{custWestARN, "us-west-2"},
	} {
		r, err := st.GetResource(store.ResourceID("aws", acct.ID, c.arn))
		if err != nil {
			t.Errorf("customer permission %s missing: %v", c.arn, err)
			continue
		}
		if r.Region == nil || *r.Region != c.region {
			t.Errorf("customer permission %s region = %v, want %s", c.arn, r.Region, c.region)
		}
		if r.ManagedByProvider {
			t.Errorf("customer permission %s must not be ManagedByProvider", c.arn)
		}
	}
}
