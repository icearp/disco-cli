package aws

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ram"
	ramtypes "github.com/aws/aws-sdk-go-v2/service/ram/types"
	"github.com/icearp/disco-cli/store"
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

func (s *stubRAM) GetResourceShareAssociations(_ context.Context, _ *ram.GetResourceShareAssociationsInput, _ ...func(*ram.Options)) (*ram.GetResourceShareAssociationsOutput, error) {
	return &ram.GetResourceShareAssociationsOutput{}, nil
}

// The AWS-managed RAM permission catalogue has region-less ARNs. scanRAM runs
// per region, so without dedup the same managed ARN collides on the
// region-excluded natural key across concurrent per-region upserts (SQLSTATE
// 23505). Assert it's emitted once (region "global") from us-east-1 and
// skipped elsewhere, while customer-managed permissions stay per-region.
func TestScanRAMPermissions_ManagedCatalogueDedup(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	// The managed entry skipped outside us-east-1 is not a row lost.
	warnings := countOpWarnings(st, ramPermissionsOp)
	defer func() {
		if *warnings != 0 {
			t.Errorf("%s warnings = %d, want 0", ramPermissionsOp, *warnings)
		}
	}()

	managedARN := "arn:aws:ram::aws:permission/AWSRAMDefaultPermissionVPC"
	managedName := "AWSRAMDefaultPermissionVPC"
	custEastARN := "arn:aws:ram:us-east-1:" + acct.ID + ":permission/my-east-perm"
	custEastName := "my-east-perm"
	custWestARN := "arn:aws:ram:us-west-2:" + acct.ID + ":permission/my-west-perm"
	custWestName := "my-west-perm"

	managed := ramtypes.ResourceSharePermissionSummary{Arn: &managedARN, Name: &managedName, PermissionType: ramtypes.PermissionTypeAwsManaged}

	// us-east-1: managed catalogue entry + a regional customer permission.
	eastStub := &stubRAM{perms: []ramtypes.ResourceSharePermissionSummary{
		managed,
		{Arn: &custEastARN, Name: &custEastName, PermissionType: ramtypes.PermissionTypeCustomerManaged},
	}}
	if _, _, err := scanRAMPermissions(context.Background(), eastStub, acct, "us-east-1", st, testScanID); err != nil {
		t.Fatalf("us-east-1 scan: %v", err)
	}

	// us-west-2: same managed entry (must be skipped, not re-upserted) + a
	// different regional customer permission.
	westStub := &stubRAM{perms: []ramtypes.ResourceSharePermissionSummary{
		managed,
		{Arn: &custWestARN, Name: &custWestName, PermissionType: ramtypes.PermissionTypeCustomerManaged},
	}}
	westTotal, _, err := scanRAMPermissions(context.Background(), westStub, acct, "us-west-2", st, testScanID)
	if err != nil {
		t.Fatalf("us-west-2 scan (managed-key collision regression): %v", err)
	}
	if westTotal != 1 {
		t.Errorf("us-west-2 total = %d, want 1: only the customer permission, not the managed entry", westTotal)
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

var (
	ramTestDenied = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:user/x is not authorized to perform: ram:GetResourceShareAssociations")
	errRAMTest    = errors.New("ram test failure")
)

const (
	ramShareARN      = "arn:aws:ram:us-east-1:123456789012:resource-share/11111111-aaaa-bbbb-cccc-000000000001"
	ramGoneShareARN  = "arn:aws:ram:us-east-1:123456789012:resource-share/11111111-aaaa-bbbb-cccc-000000000002"
	ramPrincipalAcct = "210987654321"
	ramPrincipalOU   = "arn:aws:organizations::123456789012:ou/o-abc123/ou-ab12-cdef3456"
	ramSubnetARN     = "arn:aws:ec2:us-east-1:123456789012:subnet/subnet-0123456789abcdef0"
	ramCustPermARN   = "arn:aws:ram:us-east-1:123456789012:permission/cust"
)

var (
	ramNativePrincipalAcct = ramShareARN + "/principal/" + ramPrincipalAcct
	ramNativePrincipalOU   = ramGoneShareARN + "/principal/" + ramPrincipalOU
	ramNativeSubnet        = ramShareARN + "/resource/" + ramSubnetARN
)

// ramCallRecorder captures the inputs each GetResourceShareAssociations call
// was made with.
type ramCallRecorder struct {
	mu    sync.Mutex
	calls []ram.GetResourceShareAssociationsInput
}

func (r *ramCallRecorder) record(in *ram.GetResourceShareAssociationsInput) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, *in)
}

func ramAssoc(share, entity string, typ ramtypes.ResourceShareAssociationType, status ramtypes.ResourceShareAssociationStatus) ramtypes.ResourceShareAssociation {
	return ramtypes.ResourceShareAssociation{
		ResourceShareArn: ptrStr(share), ResourceShareName: ptrStr("share"), AssociatedEntity: ptrStr(entity),
		AssociationType: typ, Status: status, External: sdkaws.Bool(true),
		CreationTime: sdkaws.Time(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)),
	}
}

// ramHandlers answers every RAM op: one owned share and one customer
// permission; two principal pages (the first holding an element without an
// entity and a DISASSOCIATED one, the second an association of a share that
// was not listed) and one resource page.
func ramHandlers(rec *ramCallRecorder) map[string]func(any) (any, error) {
	return map[string]func(any) (any, error){
		"GetResourceShares": func(any) (any, error) {
			return &ram.GetResourceSharesOutput{ResourceShares: []ramtypes.ResourceShare{
				{ResourceShareArn: ptrStr(ramShareARN), Name: ptrStr("share"), Status: ramtypes.ResourceShareStatusActive},
			}}, nil
		},
		"ListPermissions": func(any) (any, error) {
			return &ram.ListPermissionsOutput{Permissions: []ramtypes.ResourceSharePermissionSummary{
				{Arn: ptrStr(ramCustPermARN), Name: ptrStr("cust")},
			}}, nil
		},
		"GetResourceShareAssociations": func(in any) (any, error) {
			req := in.(*ram.GetResourceShareAssociationsInput)
			rec.record(req)
			switch req.AssociationType {
			case ramtypes.ResourceShareAssociationTypePrincipal:
				if req.NextToken == nil {
					return &ram.GetResourceShareAssociationsOutput{
						ResourceShareAssociations: []ramtypes.ResourceShareAssociation{
							ramAssoc(ramShareARN, ramPrincipalAcct, req.AssociationType, ramtypes.ResourceShareAssociationStatusAssociated),
							{ResourceShareArn: ptrStr(ramShareARN), AssociationType: req.AssociationType},
							ramAssoc(ramShareARN, "999999999999", req.AssociationType, ramtypes.ResourceShareAssociationStatusDisassociated),
						},
						NextToken: ptrStr("p2"),
					}, nil
				}
				return &ram.GetResourceShareAssociationsOutput{ResourceShareAssociations: []ramtypes.ResourceShareAssociation{
					ramAssoc(ramGoneShareARN, ramPrincipalOU, req.AssociationType, ramtypes.ResourceShareAssociationStatusDisassociating),
				}}, nil
			case ramtypes.ResourceShareAssociationTypeResource:
				return &ram.GetResourceShareAssociationsOutput{ResourceShareAssociations: []ramtypes.ResourceShareAssociation{
					ramAssoc(ramShareARN, ramSubnetARN, req.AssociationType, ramtypes.ResourceShareAssociationStatusAssociated),
				}}, nil
			}
			return nil, errors.New("unexpected association type " + string(req.AssociationType))
		},
	}
}

func scanRAMTopLevel(t *testing.T, st *store.Store, handlers map[string]func(any) (any, error)) (int, int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, ramTestDenied)
	return scanRAM(context.Background(), acct, testRegion, st, testScanID)
}

func TestScanRAM_StoresShareAssociations(t *testing.T) {
	st := newTestStore(t)
	var warnings []store.ScanWarning
	st.OnWarn = func(w store.ScanWarning) { warnings = append(warnings, w) }
	rec := &ramCallRecorder{}
	total, inserted, err := scanRAMTopLevel(t, st, ramHandlers(rec))
	if err != nil {
		t.Fatalf("scanRAM: %v", err)
	}
	// A pair naming the unlisted share would be gated out with a warning.
	if len(warnings) != 0 {
		t.Errorf("warnings = %+v, want none", warnings)
	}
	// 1 share + 1 permission + 3 associations
	if total != 5 || inserted != 5 {
		t.Errorf("total, inserted = %d, %d; want 5, 5", total, inserted)
	}

	rows := rowsByNativeID(t, st, TypeRAMResourceShareAssociation)
	want := map[string]struct{ name, status string }{
		ramNativePrincipalAcct: {ramPrincipalAcct, "ASSOCIATED"},
		ramNativePrincipalOU:   {ramPrincipalOU, "DISASSOCIATING"},
		ramNativeSubnet:        {ramSubnetARN, "ASSOCIATED"},
	}
	if len(rows) != len(want) {
		t.Errorf("association rows = %d (%v), want %d: no keyless or DISASSOCIATED row", len(rows), rows, len(want))
	}
	for native, w := range want {
		r, ok := rows[native]
		if !ok {
			t.Errorf("association %s missing", native)
			continue
		}
		if sv(r.Name) != w.name || sv(r.Status) != w.status || sv(r.Region) != testRegion {
			t.Errorf("%s: name, status, region = %q, %q, %q; want %q, %q, %q", native, sv(r.Name), sv(r.Status), sv(r.Region), w.name, w.status, testRegion)
		}
		var attrs ramtypes.ResourceShareAssociation
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			t.Fatalf("%s attrs: %v", native, err)
		}
		if sv(attrs.AssociatedEntity) != w.name || attrs.External == nil || !*attrs.External || attrs.CreationTime == nil {
			t.Errorf("%s attrs = %s; want the SDK element verbatim", native, r.AttributesJSON)
		}
	}

	assertContainsNative(t, st, ramShareARN, ramNativePrincipalAcct)
	assertContainsNative(t, st, ramShareARN, ramNativeSubnet)
	// The unlisted share has no row, so its association is stored unlinked.
	rels, err := st.RelationshipsTo(store.ResourceID("aws", testAccountID, ramNativePrincipalOU), store.RelContains)
	if err != nil {
		t.Fatalf("RelationshipsTo: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("unlisted share's association has contains edges %v, want none", rels)
	}

	// Two principal pages and one resource page, each at the page cap and
	// listing every owned share (no ResourceShareArns filter).
	var principal, resource int
	for _, c := range rec.calls {
		if sdkaws.ToInt32(c.MaxResults) != ramPageCap || c.ResourceShareArns != nil {
			t.Errorf("call %+v: want MaxResults %d and no ResourceShareArns", c, ramPageCap)
		}
		switch c.AssociationType {
		case ramtypes.ResourceShareAssociationTypePrincipal:
			principal++
		case ramtypes.ResourceShareAssociationTypeResource:
			resource++
		}
	}
	if principal != 2 || resource != 1 || len(rec.calls) != 3 {
		t.Errorf("calls = %d principal, %d resource, %d total; want 2, 1, 3", principal, resource, len(rec.calls))
	}
}

func TestScanRAM_NoAssociations(t *testing.T) {
	st := newTestStore(t)
	h := ramHandlers(&ramCallRecorder{})
	h["GetResourceShareAssociations"] = func(any) (any, error) { return &ram.GetResourceShareAssociationsOutput{}, nil }
	total, inserted, err := scanRAMTopLevel(t, st, h)
	if err != nil {
		t.Fatalf("scanRAM: %v", err)
	}
	if total != 2 || inserted != 2 {
		t.Errorf("total, inserted = %d, %d; want 2, 2 (the share and the permission)", total, inserted)
	}
	if n := len(rowsByNativeID(t, st, TypeRAMResourceShareAssociation)); n != 0 {
		t.Errorf("association rows = %d, want 0", n)
	}
}

// A page that repeats the token it was asked with ends the listing instead of
// looping on it.
func TestScanRAM_AssociationsStopOnRepeatedToken(t *testing.T) {
	st := newTestStore(t)
	h := ramHandlers(&ramCallRecorder{})
	calls := 0
	h["GetResourceShareAssociations"] = func(in any) (any, error) {
		calls++
		if calls > 4 {
			return nil, errRAMTest
		}
		req := in.(*ram.GetResourceShareAssociationsInput)
		return &ram.GetResourceShareAssociationsOutput{
			ResourceShareAssociations: []ramtypes.ResourceShareAssociation{
				ramAssoc(ramShareARN, ramPrincipalAcct, req.AssociationType, ramtypes.ResourceShareAssociationStatusAssociated),
			},
			NextToken: ptrStr("same"),
		}, nil
	}
	if _, _, err := scanRAMTopLevel(t, st, h); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	// Per type: the first page, then the page asked with the repeated token.
	if calls != 4 {
		t.Errorf("calls = %d, want 4", calls)
	}
}

// A denied association listing warns once and costs the other phases no rows.
func TestScanRAM_AssociationsDenied(t *testing.T) {
	st := newTestStore(t)
	h := ramHandlers(&ramCallRecorder{})
	delete(h, "GetResourceShareAssociations")
	warnings := countOpWarnings(st, ramAssociationsOp)
	total, inserted, err := scanRAMTopLevel(t, st, h)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if *warnings != 1 {
		t.Errorf("%s warnings = %d, want 1", ramAssociationsOp, *warnings)
	}
	if total != 2 || inserted != 2 {
		t.Errorf("total, inserted = %d, %d; want 2, 2", total, inserted)
	}
	if n := len(rowsByNativeID(t, st, TypeRAMResourceShare)); n != 1 {
		t.Errorf("share rows = %d, want 1", n)
	}
}

// An error after the first page keeps, and links, the rows of the pages read
// before it: a denial warns, any other error is returned.
func TestScanRAM_AssociationsErrorOnSecondPageKeepsFirst(t *testing.T) {
	cases := []struct {
		name     string
		failErr  error
		wantErr  error
		warnings int
	}{
		{"denied", ramTestDenied, nil, 1},
		{"hard error", errRAMTest, errRAMTest, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			h := ramHandlers(&ramCallRecorder{})
			first := h["GetResourceShareAssociations"]
			calls := 0
			h["GetResourceShareAssociations"] = func(in any) (any, error) {
				calls++
				if calls > 1 {
					return nil, c.failErr
				}
				return first(in)
			}
			warnings := countOpWarnings(st, ramAssociationsOp)
			total, inserted, err := scanRAMTopLevel(t, st, h)
			if !errors.Is(err, c.wantErr) || (c.wantErr == nil && err != nil) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			if *warnings != c.warnings {
				t.Errorf("%s warnings = %d, want %d", ramAssociationsOp, *warnings, c.warnings)
			}
			if total != 3 || inserted != 3 {
				t.Errorf("total, inserted = %d, %d; want 3, 3 (share, permission, first-page association)", total, inserted)
			}
			rows := rowsByNativeID(t, st, TypeRAMResourceShareAssociation)
			if _, ok := rows[ramNativePrincipalAcct]; !ok || len(rows) != 1 {
				t.Errorf("association rows = %v, want only %s", rows, ramNativePrincipalAcct)
			}
			assertContainsNative(t, st, ramShareARN, ramNativePrincipalAcct)
		})
	}
}

// Every phase runs whatever another returns: a hard error anywhere is
// returned, and the other phases' rows are stored.
func TestScanRAM_PhaseErrorPropagates(t *testing.T) {
	cases := []struct {
		op        string
		wantTotal int
		wantRows  map[string]int
	}{
		// No share was stored, so the associations are stored unlinked.
		{"GetResourceShares", 4, map[string]int{TypeRAMResourceShare: 0, TypeRAMPermission: 1, TypeRAMResourceShareAssociation: 3}},
		{"ListPermissions", 4, map[string]int{TypeRAMResourceShare: 1, TypeRAMPermission: 0, TypeRAMResourceShareAssociation: 3}},
		{"GetResourceShareAssociations", 2, map[string]int{TypeRAMResourceShare: 1, TypeRAMPermission: 1, TypeRAMResourceShareAssociation: 0}},
	}
	for _, c := range cases {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			h := ramHandlers(&ramCallRecorder{})
			h[c.op] = func(any) (any, error) { return nil, errRAMTest }
			total, inserted, err := scanRAMTopLevel(t, st, h)
			if !errors.Is(err, errRAMTest) {
				t.Fatalf("err = %v, want the %s error", err, c.op)
			}
			// A fresh store: every stored row is a first discovery.
			if total != c.wantTotal || inserted != c.wantTotal {
				t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, c.wantTotal, c.wantTotal)
			}
			for typ, want := range c.wantRows {
				if n := len(rowsByNativeID(t, st, typ)); n != want {
					t.Errorf("%s rows = %d, want %d", typ, n, want)
				}
			}
			if c.op == "GetResourceShares" {
				rels, err := st.RelationshipsTo(store.ResourceID("aws", testAccountID, ramNativePrincipalAcct), store.RelContains)
				if err != nil {
					t.Fatalf("RelationshipsTo: %v", err)
				}
				if len(rels) != 0 {
					t.Errorf("association of an unstored share has contains edges %v, want none", rels)
				}
			}
		})
	}
}

// A phase's transient error is demoted to a warning under its op instead of
// buried in another phase's hard error.
func TestScanRAM_TransientPhaseDoesNotMaskHardError(t *testing.T) {
	throttle := apiErr("ThrottlingException", "Rate exceeded")
	cases := []struct {
		name, transientOp, hardOp, warnOp string
	}{
		{"throttled share phase", "GetResourceShares", "GetResourceShareAssociations", ramResourceSharesOp},
		{"throttled permission phase", "ListPermissions", "GetResourceShareAssociations", ramPermissionsOp},
		{"throttled association phase", "GetResourceShareAssociations", "GetResourceShares", ramAssociationsOp},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			h := ramHandlers(&ramCallRecorder{})
			h[c.transientOp] = func(any) (any, error) { return nil, throttle }
			h[c.hardOp] = func(any) (any, error) { return nil, errRAMTest }
			warnings := countOpWarnings(st, c.warnOp)
			_, _, err := scanRAMTopLevel(t, st, h)
			if !errors.Is(err, errRAMTest) {
				t.Fatalf("err = %v, want the %s error", err, c.hardOp)
			}
			if got := classifyServiceError(err); got != outcomeError {
				t.Errorf("classifyServiceError(%v) = %v, want outcomeError", err, got)
			}
			if *warnings != 1 {
				t.Errorf("%s warnings = %d, want 1", c.warnOp, *warnings)
			}
		})
	}
}

const (
	ramTaggedShareARN = "arn:aws:ram:us-east-1:123456789012:resource-share/22222222-aaaa-bbbb-cccc-000000000001"
	ramPlainShareARN  = "arn:aws:ram:us-east-1:123456789012:resource-share/22222222-aaaa-bbbb-cccc-000000000002"
	ramTaggedPermARN  = "arn:aws:ram:us-east-1:123456789012:permission/tagged"
	ramPlainPermARN   = "arn:aws:ram:us-east-1:123456789012:permission/plain"
)

// ramListInputs records the inputs of each GetResourceShares and
// ListPermissions call.
type ramListInputs struct {
	mu     sync.Mutex
	shares []ram.GetResourceSharesInput
	perms  []ram.ListPermissionsInput
}

// ramTwoPageListHandlers answers GetResourceShares and ListPermissions with
// two pages each: the first holds a tagged element and one without an ARN,
// the second an untagged element. page2Err, when set, fails both second pages.
func ramTwoPageListHandlers(rec *ramListInputs, page2Err error) map[string]func(any) (any, error) {
	h := ramHandlers(&ramCallRecorder{})
	h["GetResourceShareAssociations"] = func(any) (any, error) { return &ram.GetResourceShareAssociationsOutput{}, nil }
	h["GetResourceShares"] = func(in any) (any, error) {
		req := in.(*ram.GetResourceSharesInput)
		rec.mu.Lock()
		rec.shares = append(rec.shares, *req)
		rec.mu.Unlock()
		if req.NextToken == nil {
			return &ram.GetResourceSharesOutput{
				ResourceShares: []ramtypes.ResourceShare{
					{
						ResourceShareArn: ptrStr(ramTaggedShareARN), Name: ptrStr("tagged"), Status: ramtypes.ResourceShareStatusActive,
						AllowExternalPrincipals: sdkaws.Bool(true),
						Tags:                    []ramtypes.Tag{{Key: ptrStr("env"), Value: ptrStr("prod")}, {Key: ptrStr("team"), Value: ptrStr("net")}},
					},
					{Name: ptrStr("no-arn")},
				},
				NextToken: ptrStr("s2"),
			}, nil
		}
		if page2Err != nil {
			return nil, page2Err
		}
		return &ram.GetResourceSharesOutput{ResourceShares: []ramtypes.ResourceShare{
			{ResourceShareArn: ptrStr(ramPlainShareARN), Name: ptrStr("plain")},
		}}, nil
	}
	h["ListPermissions"] = func(in any) (any, error) {
		req := in.(*ram.ListPermissionsInput)
		rec.mu.Lock()
		rec.perms = append(rec.perms, *req)
		rec.mu.Unlock()
		if req.NextToken == nil {
			return &ram.ListPermissionsOutput{
				Permissions: []ramtypes.ResourceSharePermissionSummary{
					{
						Arn: ptrStr(ramTaggedPermARN), Name: ptrStr("tagged-perm"), ResourceType: ptrStr("ec2:subnet"),
						Tags: []ramtypes.Tag{{Key: ptrStr("owner"), Value: ptrStr("ops")}},
					},
					{Name: ptrStr("no-arn-perm")},
				},
				NextToken: ptrStr("p2"),
			}, nil
		}
		if page2Err != nil {
			return nil, page2Err
		}
		return &ram.ListPermissionsOutput{Permissions: []ramtypes.ResourceSharePermissionSummary{
			{Arn: ptrStr(ramPlainPermARN), Name: ptrStr("plain-perm")},
		}}, nil
	}
	return h
}

// Shares and permissions store the Tags their list element carries, read
// every page with the page cap and every owned share, and warn once per op
// for the elements without an ARN.
func TestScanRAM_SharesAndPermissionsStoreTags(t *testing.T) {
	st := newTestStore(t)
	shareWarns := countOpWarnings(st, ramResourceSharesOp)
	permWarns := countOpWarnings(st, ramPermissionsOp)
	rec := &ramListInputs{}
	total, inserted, err := scanRAMTopLevel(t, st, ramTwoPageListHandlers(rec, nil))
	if err != nil {
		t.Fatalf("scanRAM: %v", err)
	}
	if total != 4 || inserted != 4 {
		t.Errorf("total, inserted = %d, %d; want 4, 4 (two shares, two permissions)", total, inserted)
	}
	if *shareWarns != 1 || *permWarns != 1 {
		t.Errorf("warnings: shares %d, permissions %d; want 1 each for the element without an ARN", *shareWarns, *permWarns)
	}

	shares := rowsByNativeID(t, st, TypeRAMResourceShare)
	if len(shares) != 2 {
		t.Fatalf("share rows = %v, want %s and %s", shares, ramTaggedShareARN, ramPlainShareARN)
	}
	tagged := shares[ramTaggedShareARN]
	if sv(tagged.Name) != "tagged" || sv(tagged.Status) != "ACTIVE" || sv(tagged.Region) != testRegion {
		t.Errorf("tagged share name, status, region = %q, %q, %q; want tagged, ACTIVE, %s", sv(tagged.Name), sv(tagged.Status), sv(tagged.Region), testRegion)
	}
	var shareAttrs ramtypes.ResourceShare
	if err := json.Unmarshal([]byte(tagged.AttributesJSON), &shareAttrs); err != nil {
		t.Fatalf("share attrs: %v", err)
	}
	if sv(shareAttrs.ResourceShareArn) != ramTaggedShareARN || !sdkaws.ToBool(shareAttrs.AllowExternalPrincipals) || len(shareAttrs.Tags) != 2 {
		t.Errorf("share attrs = %s; want the SDK element verbatim, Tags included", tagged.AttributesJSON)
	}
	assertTagsMap(t, tagged.TagsJSON, map[string]string{"env": "prod", "team": "net"})
	plain := shares[ramPlainShareARN]
	if sv(plain.Name) != "plain" || plain.TagsJSON != nil || plain.Status != nil {
		t.Errorf("untagged share name, tags, status = %q, %v, %v; want plain, nil, nil", sv(plain.Name), plain.TagsJSON, plain.Status)
	}

	perms := rowsByNativeID(t, st, TypeRAMPermission)
	if len(perms) != 2 {
		t.Fatalf("permission rows = %v, want %s and %s", perms, ramTaggedPermARN, ramPlainPermARN)
	}
	tp := perms[ramTaggedPermARN]
	if sv(tp.Name) != "tagged-perm" || sv(tp.Region) != testRegion || tp.ManagedByProvider {
		t.Errorf("tagged permission name, region, managed = %q, %q, %v; want tagged-perm, %s, false", sv(tp.Name), sv(tp.Region), tp.ManagedByProvider, testRegion)
	}
	var permAttrs ramtypes.ResourceSharePermissionSummary
	if err := json.Unmarshal([]byte(tp.AttributesJSON), &permAttrs); err != nil {
		t.Fatalf("permission attrs: %v", err)
	}
	if sv(permAttrs.Arn) != ramTaggedPermARN || sv(permAttrs.ResourceType) != "ec2:subnet" || len(permAttrs.Tags) != 1 {
		t.Errorf("permission attrs = %s; want the SDK element verbatim, Tags included", tp.AttributesJSON)
	}
	assertTagsMap(t, tp.TagsJSON, map[string]string{"owner": "ops"})
	if pp := perms[ramPlainPermARN]; sv(pp.Name) != "plain-perm" || pp.TagsJSON != nil {
		t.Errorf("untagged permission name, tags = %q, %v; want plain-perm, nil", sv(pp.Name), pp.TagsJSON)
	}

	if len(rec.shares) != 2 || len(rec.perms) != 2 {
		t.Fatalf("calls: %d GetResourceShares, %d ListPermissions; want 2 each", len(rec.shares), len(rec.perms))
	}
	for i, wantToken := range []string{"", "s2"} {
		in := rec.shares[i]
		if sv(in.NextToken) != wantToken || sdkaws.ToInt32(in.MaxResults) != ramPageCap || in.ResourceOwner != ramtypes.ResourceOwnerSelf ||
			in.Name != nil || in.PermissionArn != nil || in.PermissionVersion != nil || in.ResourceShareArns != nil || in.ResourceShareStatus != "" || in.TagFilters != nil {
			t.Errorf("GetResourceShares call %d = %+v; want NextToken %q, MaxResults %d, ResourceOwner SELF and no other filter", i, in, wantToken, ramPageCap)
		}
	}
	for i, wantToken := range []string{"", "p2"} {
		in := rec.perms[i]
		if sv(in.NextToken) != wantToken || sdkaws.ToInt32(in.MaxResults) != ramPageCap || in.PermissionType != "" || in.ResourceType != nil {
			t.Errorf("ListPermissions call %d = %+v; want NextToken %q, MaxResults %d, no PermissionType or ResourceType", i, in, wantToken, ramPageCap)
		}
	}
}

// A failed later page keeps the rows of the pages read before it: a denial
// warns once per op, any other error is returned.
func TestScanRAM_SharesAndPermissionsKeepRowsPastFailedPage(t *testing.T) {
	for _, c := range []struct {
		name      string
		page2Err  error
		wantErr   error
		wantWarns int // per op: the element without an ARN, plus the denial
	}{
		{"denied", ramTestDenied, nil, 2},
		{"hard error", errRAMTest, errRAMTest, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			shareWarns := countOpWarnings(st, ramResourceSharesOp)
			permWarns := countOpWarnings(st, ramPermissionsOp)
			total, inserted, err := scanRAMTopLevel(t, st, ramTwoPageListHandlers(&ramListInputs{}, c.page2Err))
			if !errors.Is(err, c.wantErr) || (c.wantErr == nil && err != nil) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			if total != 2 || inserted != 2 {
				t.Errorf("total, inserted = %d, %d; want 2, 2 (the first page's share and permission)", total, inserted)
			}
			if _, ok := rowsByNativeID(t, st, TypeRAMResourceShare)[ramTaggedShareARN]; !ok {
				t.Errorf("share %s from the first page missing", ramTaggedShareARN)
			}
			if _, ok := rowsByNativeID(t, st, TypeRAMPermission)[ramTaggedPermARN]; !ok {
				t.Errorf("permission %s from the first page missing", ramTaggedPermARN)
			}
			if *shareWarns != c.wantWarns || *permWarns != c.wantWarns {
				t.Errorf("warnings: shares %d, permissions %d; want %d each", *shareWarns, *permWarns, c.wantWarns)
			}
		})
	}
}

// A page that repeats its NextToken ends the listing as an error, keeping the
// rows read.
func TestScanRAM_SharesAndPermissionsStopOnRepeatedToken(t *testing.T) {
	st := newTestStore(t)
	h := ramTwoPageListHandlers(&ramListInputs{}, nil)
	shares, perms := h["GetResourceShares"], h["ListPermissions"]
	h["GetResourceShares"] = func(in any) (any, error) {
		out, err := shares(in)
		if err == nil {
			out.(*ram.GetResourceSharesOutput).NextToken = ptrStr("same")
		}
		return out, err
	}
	h["ListPermissions"] = func(in any) (any, error) {
		out, err := perms(in)
		if err == nil {
			out.(*ram.ListPermissionsOutput).NextToken = ptrStr("same")
		}
		return out, err
	}
	total, _, err := scanRAMTopLevel(t, st, h)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v, want errRepeatedNextToken", err)
	}
	// Both pages of each listing: the second was asked with "same" and answered "same".
	if total != 4 {
		t.Errorf("total = %d, want 4", total)
	}
}

// A store write failure stops the later phases: every later write would fail
// too.
func TestScanRAM_StoreWriteStopsLaterPhases(t *testing.T) {
	for _, failing := range []string{"GetResourceShares", "ListPermissions"} {
		t.Run(failing, func(t *testing.T) {
			st, closeStore := openClosingStore(t)
			h := ramHandlers(&ramCallRecorder{})
			var called []string
			for op, next := range h {
				h[op] = func(in any) (any, error) {
					called = append(called, op)
					if op == failing {
						closeStore()
					}
					return next(in)
				}
			}
			_, _, err := scanRAMTopLevel(t, st, h)
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v, want ErrStoreWrite", err)
			}
			if last := called[len(called)-1]; last != failing {
				t.Errorf("ops called = %v; want none after %s", called, failing)
			}
		})
	}
}
