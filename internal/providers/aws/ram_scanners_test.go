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
		if sdkaws.ToInt32(c.MaxResults) != ramAssociationsPageCap || c.ResourceShareArns != nil {
			t.Errorf("call %+v: want MaxResults %d and no ResourceShareArns", c, ramAssociationsPageCap)
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

// A phase's transient error is demoted to a warning, so it cannot make the
// dispatcher read another phase's hard error as transient.
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
