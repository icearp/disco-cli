package aws

import (
	"context"
	"errors"
	"io"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/icearp/disco-cli/store"
)

const (
	tgxPT1       = "tgw-ptb-0001"
	tgxPT2       = "tgw-ptb-0002"
	tgxPT3       = "tgw-ptb-0003"
	tgxRT1       = "tgw-rtb-0001"
	tgxRT2       = "tgw-rtb-0002"
	tgxRT3       = "tgw-rtb-0003"
	tgxPL1       = "pl-0001"
	tgxPL2       = "pl-0002"
	tgxAtt       = "tgw-attach-0001"
	tgxVpc       = "vpc-0001"
	tgxPageNext  = "p2"
	tgxOtherScan = "scan-other"
	tgxMetaKey   = "app"
)

var (
	tgxDenied  = apiErr("UnauthorizedOperation", "You are not authorized to perform this operation. User: arn:aws:iam::123456789012:user/x is not authorized to perform: ec2:Get")
	errTgxTest = errors.New("ec2 tgw extended test failure")
)

func tgxPTARN(id string) string {
	return ec2ARN(testRegion, testAccountID, "transit-gateway-policy-table", id)
}

func tgxRTARN(id string) string {
	return ec2ARN(testRegion, testAccountID, "transit-gateway-route-table", id)
}

func tgxEntryID(table, rule string) string {
	return tgwPolicyTableEntryNativeID(tgxPTARN(table), rule)
}

func tgxRefID(table, pl string) string {
	return tgwPrefixListReferenceNativeID(tgxRTARN(table), pl)
}

func tgxClient(handlers map[string]func(in any) (any, error)) ec2API {
	return ec2.NewFromConfig(opStubConfig(handlers, tgxDenied))
}

// tgxSeedParent stores a parent row as its own scanner would, in region,
// with status, seen by scanID.
func tgxSeedParent(t *testing.T, st *store.Store, typ, nativeID, region, status, scanID string) {
	t.Helper()
	if _, err := st.UpsertResource(&store.Resource{
		Provider: "aws", AccountID: testAccountID, Type: typ, NativeID: nativeID,
		Region: &region, Status: &status, AttributesJSON: "{}", DiscoveredBy: scanID,
	}); err != nil {
		t.Fatalf("seed %s %s: %v", typ, nativeID, err)
	}
}

// tgxSeedPolicyTables stores live policy tables 1 and 2, plus three that
// must not be queried: a deleted one, one in another Region and one stored by
// another scan.
func tgxSeedPolicyTables(t *testing.T, st *store.Store) {
	t.Helper()
	tgxSeedParent(t, st, TypeEC2TransitGatewayPolicyTable, tgxPTARN(tgxPT1), testRegion, "available", testScanID)
	tgxSeedParent(t, st, TypeEC2TransitGatewayPolicyTable, tgxPTARN(tgxPT2), testRegion, "deleting", testScanID)
	tgxSeedParent(t, st, TypeEC2TransitGatewayPolicyTable, tgxPTARN(tgxPT3), testRegion, "deleted", testScanID)
	tgxSeedParent(t, st, TypeEC2TransitGatewayPolicyTable,
		ec2ARN("eu-west-1", testAccountID, "transit-gateway-policy-table", "tgw-ptb-0eu"), "eu-west-1", "available", testScanID)
	insertTestScan(t, st, tgxOtherScan, "completed")
	tgxSeedParent(t, st, TypeEC2TransitGatewayPolicyTable, tgxPTARN("tgw-ptb-0old"), testRegion, "available", tgxOtherScan)
}

// tgxSeedRouteTables is tgxSeedPolicyTables for route tables.
func tgxSeedRouteTables(t *testing.T, st *store.Store) {
	t.Helper()
	tgxSeedParent(t, st, TypeEC2TransitGatewayRouteTable, tgxRTARN(tgxRT1), testRegion, "available", testScanID)
	tgxSeedParent(t, st, TypeEC2TransitGatewayRouteTable, tgxRTARN(tgxRT2), testRegion, "pending", testScanID)
	tgxSeedParent(t, st, TypeEC2TransitGatewayRouteTable, tgxRTARN(tgxRT3), testRegion, "deleted", testScanID)
	tgxSeedParent(t, st, TypeEC2TransitGatewayRouteTable,
		ec2ARN("eu-west-1", testAccountID, "transit-gateway-route-table", "tgw-rtb-0eu"), "eu-west-1", "available", testScanID)
	insertTestScan(t, st, tgxOtherScan, "completed")
	tgxSeedParent(t, st, TypeEC2TransitGatewayRouteTable, tgxRTARN("tgw-rtb-0old"), testRegion, "available", tgxOtherScan)
}

// tgxEntriesHandler serves policy table 1 over two pages and table 2 in one,
// recording every table queried.
func tgxEntriesHandler(calls *parentCalls) func(in any) (any, error) {
	return func(in any) (any, error) {
		p := in.(*ec2.GetTransitGatewayPolicyTableEntriesInput)
		calls.add(sv(p.TransitGatewayPolicyTableId))
		if sv32(p.MaxResults) != ec2TGWChildPageLimit {
			return nil, apiErr("InvalidParameterValue", "MaxResults must be set to the page cap")
		}
		switch {
		case sv(p.TransitGatewayPolicyTableId) == tgxPT2:
			return &ec2.GetTransitGatewayPolicyTableEntriesOutput{TransitGatewayPolicyTableEntries: []ec2types.TransitGatewayPolicyTableEntry{
				{PolicyRuleNumber: ptrStr("100"), State: ec2types.TransitGatewayPolicyTableEntryStateActive, TargetRouteTableId: ptrStr(tgxRT2)},
			}}, nil
		case p.NextToken == nil:
			return &ec2.GetTransitGatewayPolicyTableEntriesOutput{NextToken: ptrStr(tgxPageNext), TransitGatewayPolicyTableEntries: []ec2types.TransitGatewayPolicyTableEntry{{
				PolicyRuleNumber: ptrStr("100"), State: ec2types.TransitGatewayPolicyTableEntryStateActive, TargetRouteTableId: ptrStr(tgxRT1),
				PolicyRule: &ec2types.TransitGatewayPolicyRule{
					MetaData: &ec2types.TransitGatewayPolicyRuleMetaData{MetaDataKey: ptrStr(tgxMetaKey), MetaDataValue: ptrStr("web")},
				},
			}}}, nil
		default:
			return &ec2.GetTransitGatewayPolicyTableEntriesOutput{TransitGatewayPolicyTableEntries: []ec2types.TransitGatewayPolicyTableEntry{
				{PolicyRuleNumber: ptrStr("200"), State: ec2types.TransitGatewayPolicyTableEntryStateActive},
				{PolicyRuleNumber: ptrStr("300"), State: ec2types.TransitGatewayPolicyTableEntryStateDeleted},
				{State: ec2types.TransitGatewayPolicyTableEntryStateActive},
			}}, nil
		}
	}
}

// tgxRefsHandler serves route table 1 over two pages and table 2 in one,
// recording every table queried.
func tgxRefsHandler(calls *parentCalls) func(in any) (any, error) {
	return func(in any) (any, error) {
		p := in.(*ec2.GetTransitGatewayPrefixListReferencesInput)
		calls.add(sv(p.TransitGatewayRouteTableId))
		if sv32(p.MaxResults) != ec2TGWChildPageLimit {
			return nil, apiErr("InvalidParameterValue", "MaxResults must be set to the page cap")
		}
		switch {
		case sv(p.TransitGatewayRouteTableId) == tgxRT2:
			return &ec2.GetTransitGatewayPrefixListReferencesOutput{TransitGatewayPrefixListReferences: []ec2types.TransitGatewayPrefixListReference{
				{PrefixListId: ptrStr(tgxPL1), State: ec2types.TransitGatewayPrefixListReferenceStatePending, TransitGatewayRouteTableId: ptrStr(tgxRT2)},
			}}, nil
		case p.NextToken == nil:
			return &ec2.GetTransitGatewayPrefixListReferencesOutput{NextToken: ptrStr(tgxPageNext), TransitGatewayPrefixListReferences: []ec2types.TransitGatewayPrefixListReference{{
				PrefixListId: ptrStr(tgxPL1), State: ec2types.TransitGatewayPrefixListReferenceStateAvailable, TransitGatewayRouteTableId: ptrStr(tgxRT1),
				TransitGatewayAttachment: &ec2types.TransitGatewayPrefixListAttachment{
					TransitGatewayAttachmentId: ptrStr(tgxAtt), ResourceId: ptrStr(tgxVpc), ResourceType: ec2types.TransitGatewayAttachmentResourceTypeVpc,
				},
			}}}, nil
		default:
			return &ec2.GetTransitGatewayPrefixListReferencesOutput{TransitGatewayPrefixListReferences: []ec2types.TransitGatewayPrefixListReference{
				{PrefixListId: ptrStr(tgxPL2), State: ec2types.TransitGatewayPrefixListReferenceStateDeleting, Blackhole: ptrBool(true)},
				{State: ec2types.TransitGatewayPrefixListReferenceStateAvailable},
			}}, nil
		}
	}
}

func ptrBool(b bool) *bool { return &b }

func TestEC2TGWExtendedNativeIDShapes(t *testing.T) {
	if got, want := tgxEntryID(tgxPT1, "100"), "arn:aws:ec2:us-east-1:123456789012:transit-gateway-policy-table/tgw-ptb-0001/entry/100"; got != want {
		t.Errorf("policy table entry NativeID = %q, want %q", got, want)
	}
	if got, want := tgxRefID(tgxRT1, tgxPL1), "arn:aws:ec2:us-east-1:123456789012:transit-gateway-route-table/tgw-rtb-0001/prefix-list-reference/pl-0001"; got != want {
		t.Errorf("prefix list reference NativeID = %q, want %q", got, want)
	}
}

func TestScanEC2TGWPolicyTableEntries_StoresEntriesUnderTables(t *testing.T) {
	st := newTestStore(t)
	tgxSeedPolicyTables(t, st)
	var calls parentCalls
	client := tgxClient(map[string]func(any) (any, error){
		"GetTransitGatewayPolicyTableEntries": tgxEntriesHandler(&calls),
	})
	total, inserted, err := scanEC2TGWPolicyTableEntries(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanEC2TGWPolicyTableEntries: %v", err)
	}
	if total != 3 || inserted != 3 {
		t.Errorf("total, inserted = %d, %d; want 3, 3", total, inserted)
	}
	// Re-listing the unchanged rows inserts none.
	total, inserted, err = scanEC2TGWPolicyTableEntries(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil || total != 3 || inserted != 0 {
		t.Errorf("re-run total, inserted, err = %d, %d, %v; want 3, 0, nil", total, inserted, err)
	}
	if got := calls.distinct(); !slices.Equal(got, []string{tgxPT1, tgxPT2}) {
		t.Errorf("tables queried = %v, want [%s %s] (deleted, other-Region and other-scan tables skipped)", got, tgxPT1, tgxPT2)
	}
	rows := rowsByNativeID(t, st, TypeEC2TransitGatewayPolicyTableEntry)
	want := []string{tgxEntryID(tgxPT1, "100"), tgxEntryID(tgxPT1, "200"), tgxEntryID(tgxPT2, "100")}
	if got := nativeIDs(rows); !slices.Equal(got, want) {
		t.Fatalf("rows = %v, want %v (deleted and rule-less entries dropped)", got, want)
	}
	r := rows[tgxEntryID(tgxPT1, "100")]
	if sv(r.Name) != "100" || sv(r.Status) != "active" || sv(r.Region) != testRegion {
		t.Errorf("name/status/region = %q/%q/%q, want 100/active/%s", sv(r.Name), sv(r.Status), sv(r.Region), testRegion)
	}
	rule, _ := ipxAttrs(t, r)["PolicyRule"].(map[string]any)
	meta, _ := rule["MetaData"].(map[string]any)
	if meta["MetaDataKey"] != tgxMetaKey {
		t.Errorf("attrs PolicyRule.MetaData = %v, want key %s", meta, tgxMetaKey)
	}
	assertContainsNative(t, st, tgxPTARN(tgxPT1), tgxEntryID(tgxPT1, "100"))
	assertContainsNative(t, st, tgxPTARN(tgxPT1), tgxEntryID(tgxPT1, "200"))
	assertContainsNative(t, st, tgxPTARN(tgxPT2), tgxEntryID(tgxPT2, "100"))
}

func TestScanEC2TGWPrefixListReferences_StoresReferencesUnderTables(t *testing.T) {
	st := newTestStore(t)
	tgxSeedRouteTables(t, st)
	var calls parentCalls
	client := tgxClient(map[string]func(any) (any, error){
		"GetTransitGatewayPrefixListReferences": tgxRefsHandler(&calls),
	})
	total, inserted, err := scanEC2TGWPrefixListReferences(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanEC2TGWPrefixListReferences: %v", err)
	}
	if total != 3 || inserted != 3 {
		t.Errorf("total, inserted = %d, %d; want 3, 3", total, inserted)
	}
	// Re-listing the unchanged rows inserts none.
	total, inserted, err = scanEC2TGWPrefixListReferences(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil || total != 3 || inserted != 0 {
		t.Errorf("re-run total, inserted, err = %d, %d, %v; want 3, 0, nil", total, inserted, err)
	}
	if got := calls.distinct(); !slices.Equal(got, []string{tgxRT1, tgxRT2}) {
		t.Errorf("tables queried = %v, want [%s %s] (deleted, other-Region and other-scan tables skipped)", got, tgxRT1, tgxRT2)
	}
	rows := rowsByNativeID(t, st, TypeEC2TransitGatewayPrefixListReference)
	want := []string{tgxRefID(tgxRT1, tgxPL1), tgxRefID(tgxRT1, tgxPL2), tgxRefID(tgxRT2, tgxPL1)}
	if got := nativeIDs(rows); !slices.Equal(got, want) {
		t.Fatalf("rows = %v, want %v (prefix-list-less references dropped)", got, want)
	}
	r := rows[tgxRefID(tgxRT1, tgxPL1)]
	if sv(r.Name) != tgxPL1 || sv(r.Status) != "available" || sv(r.Region) != testRegion {
		t.Errorf("name/status/region = %q/%q/%q, want %s/available/%s", sv(r.Name), sv(r.Status), sv(r.Region), tgxPL1, testRegion)
	}
	att, _ := ipxAttrs(t, r)["TransitGatewayAttachment"].(map[string]any)
	if att["TransitGatewayAttachmentId"] != tgxAtt {
		t.Errorf("attrs TransitGatewayAttachment = %v, want id %s", att, tgxAtt)
	}
	if got := sv(rows[tgxRefID(tgxRT1, tgxPL2)].Status); got != "deleting" {
		t.Errorf("deleting reference status = %q, want deleting (kept)", got)
	}
	assertContainsNative(t, st, tgxRTARN(tgxRT1), tgxRefID(tgxRT1, tgxPL1))
	assertContainsNative(t, st, tgxRTARN(tgxRT1), tgxRefID(tgxRT1, tgxPL2))
	assertContainsNative(t, st, tgxRTARN(tgxRT2), tgxRefID(tgxRT2, tgxPL1))
}

// tgxPhase is one of the two fan-out phases, for tests that run both.
type tgxPhase struct {
	name, op, sdkOp, typ, goneCode string
	seed                           func(*testing.T, *store.Store)
	handler                        func(*parentCalls) func(any) (any, error)
	run                            func(context.Context, ec2API, *account, string, *store.Store, string) (int, int, error)
	parentID                       func(in any) string
	// kept is a row of the second live parent, which the per-parent error
	// cases never fail.
	kept string
	// repeat answers every call with one row and the same NextToken.
	repeat any
}

var tgxPhases = []tgxPhase{
	{
		name: "policy table entries", op: ec2TGWPolicyTableEntriesOp, sdkOp: "GetTransitGatewayPolicyTableEntries",
		typ: TypeEC2TransitGatewayPolicyTableEntry, goneCode: ec2TGWPolicyTableNotFound,
		seed: tgxSeedPolicyTables, handler: tgxEntriesHandler, run: scanEC2TGWPolicyTableEntries,
		parentID: func(in any) string {
			return sv(in.(*ec2.GetTransitGatewayPolicyTableEntriesInput).TransitGatewayPolicyTableId)
		},
		kept: tgxEntryID(tgxPT2, "100"),
		repeat: &ec2.GetTransitGatewayPolicyTableEntriesOutput{NextToken: ptrStr(tgxPageNext), TransitGatewayPolicyTableEntries: []ec2types.TransitGatewayPolicyTableEntry{
			{PolicyRuleNumber: ptrStr("100"), State: ec2types.TransitGatewayPolicyTableEntryStateActive},
		}},
	},
	{
		name: "prefix list references", op: ec2TGWPrefixListReferencesOp, sdkOp: "GetTransitGatewayPrefixListReferences",
		typ: TypeEC2TransitGatewayPrefixListReference, goneCode: ec2TGWRouteTableNotFound,
		seed: tgxSeedRouteTables, handler: tgxRefsHandler, run: scanEC2TGWPrefixListReferences,
		parentID: func(in any) string {
			return sv(in.(*ec2.GetTransitGatewayPrefixListReferencesInput).TransitGatewayRouteTableId)
		},
		kept: tgxRefID(tgxRT2, tgxPL1),
		repeat: &ec2.GetTransitGatewayPrefixListReferencesOutput{NextToken: ptrStr(tgxPageNext), TransitGatewayPrefixListReferences: []ec2types.TransitGatewayPrefixListReference{
			{PrefixListId: ptrStr(tgxPL1), State: ec2types.TransitGatewayPrefixListReferenceStateAvailable},
		}},
	},
}

// With no stored parent nothing is called; a parent with no children stores
// nothing.
func TestScanEC2TGWExtended_Empty(t *testing.T) {
	for _, ph := range tgxPhases {
		t.Run(ph.name+"/no parents", func(t *testing.T) {
			st := newTestStore(t)
			total, _, err := ph.run(context.Background(), tgxClient(map[string]func(any) (any, error){
				ph.sdkOp: func(any) (any, error) { return nil, errTgxTest },
			}), newTestAccount(testAccountID), testRegion, st, testScanID)
			if err != nil || total != 0 {
				t.Fatalf("total, err = %d, %v; want 0, nil", total, err)
			}
		})
		t.Run(ph.name+"/no children", func(t *testing.T) {
			st := newTestStore(t)
			ph.seed(t, st)
			total, _, err := ph.run(context.Background(), tgxClient(map[string]func(any) (any, error){
				ph.sdkOp: func(any) (any, error) {
					if ph.sdkOp == "GetTransitGatewayPolicyTableEntries" {
						return &ec2.GetTransitGatewayPolicyTableEntriesOutput{}, nil
					}
					return &ec2.GetTransitGatewayPrefixListReferencesOutput{}, nil
				},
			}), newTestAccount(testAccountID), testRegion, st, testScanID)
			if err != nil || total != 0 {
				t.Fatalf("total, err = %d, %v; want 0, nil", total, err)
			}
			if n := len(rowsByNativeID(t, st, ph.typ)); n != 0 {
				t.Errorf("rows = %d, want 0", n)
			}
		})
	}
}

// Per-parent errors: a parent gone since it was listed and a transient
// failure are skipped with one warning each, keeping the other parent's rows;
// a denial warns once for every parent; anything else fails the phase.
func TestScanEC2TGWExtended_PerParentErrors(t *testing.T) {
	for _, ph := range tgxPhases {
		cases := []struct {
			name     string
			failErr  error
			wantErr  error
			wantWarn int
		}{
			{"parent gone", apiErr(ph.goneCode, "The ID does not exist"), nil, 1},
			{"transient", io.EOF, nil, 1},
			{"denied", tgxDenied, nil, 1},
			{"incorrect state", apiErr(ec2IncorrectState, "The table is not in a valid state"), nil, 1},
			{"other", errTgxTest, errTgxTest, 0},
		}
		for _, c := range cases {
			t.Run(ph.name+"/"+c.name, func(t *testing.T) {
				st := newTestStore(t)
				ph.seed(t, st)
				// A third live parent, so the skip is tallied across two.
				if ph.typ == TypeEC2TransitGatewayPolicyTableEntry {
					tgxSeedParent(t, st, TypeEC2TransitGatewayPolicyTable, tgxPTARN("tgw-ptb-0009"), testRegion, "available", testScanID)
				} else {
					tgxSeedParent(t, st, TypeEC2TransitGatewayRouteTable, tgxRTARN("tgw-rtb-0009"), testRegion, "available", testScanID)
				}
				var calls parentCalls
				warnings := countOpWarnings(st, ph.op)
				total, _, err := ph.run(context.Background(),
					tgxClient(map[string]func(any) (any, error){ph.sdkOp: tgxFailAfterKept(ph, &calls, c.failErr)}),
					newTestAccount(testAccountID), testRegion, st, testScanID)
				if c.wantErr == nil && err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				if c.wantErr != nil && !errors.Is(err, c.wantErr) {
					t.Fatalf("err = %v, want %v", err, c.wantErr)
				}
				if *warnings != c.wantWarn {
					t.Errorf("warnings = %d, want %d", *warnings, c.wantWarn)
				}
				if _, ok := rowsByNativeID(t, st, ph.typ)[ph.kept]; !ok {
					t.Errorf("row %s missing, want it kept", ph.kept)
				}
				if total < 1 {
					t.Errorf("total = %d, want the kept parent's rows counted", total)
				}
			})
		}
	}
}

// tgxFailAfterKept answers the phase's op: the second live parent (whose row
// is ph.kept) as tgxEntriesHandler/tgxRefsHandler do, and the other live
// parents with failErr, but only after the kept parent has answered, so a hard
// failure cannot stop the fan-out before the kept parent's rows are listed.
func tgxFailAfterKept(ph tgxPhase, calls *parentCalls, failErr error) func(any) (any, error) {
	inner := ph.handler(calls)
	kept := make(chan struct{})
	var once sync.Once
	return func(in any) (any, error) {
		id := ph.parentID(in)
		if id == tgxPT2 || id == tgxRT2 {
			defer once.Do(func() { close(kept) })
			return inner(in)
		}
		select {
		case <-kept:
			return nil, failErr
		case <-time.After(5 * time.Second):
			return nil, errors.New("gate: kept parent never called")
		}
	}
}

// Each skip code warns once, naming its own code: one parent gone and one
// in an incorrect state give two warnings.
func TestScanEC2TGWExtended_OneWarningPerCode(t *testing.T) {
	for _, ph := range tgxPhases {
		t.Run(ph.name, func(t *testing.T) {
			st := newTestStore(t)
			ph.seed(t, st)
			var calls parentCalls
			inner := ph.handler(&calls)
			warnings := countOpWarnings(st, ph.op)
			_, _, err := ph.run(context.Background(), tgxClient(map[string]func(any) (any, error){
				ph.sdkOp: func(in any) (any, error) {
					switch ph.parentID(in) {
					case tgxPT1, tgxRT1:
						return nil, apiErr(ph.goneCode, "The ID does not exist")
					case tgxPT2, tgxRT2:
						return nil, apiErr(ec2IncorrectState, "The table is not in a valid state")
					}
					return inner(in)
				},
			}), newTestAccount(testAccountID), testRegion, st, testScanID)
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if *warnings != 2 {
				t.Errorf("warnings = %d, want 2 (one per code)", *warnings)
			}
		})
	}
}

// A repeated NextToken ends a parent's loop instead of spinning.
func TestScanEC2TGWExtended_RepeatedTokenStops(t *testing.T) {
	for _, ph := range tgxPhases {
		t.Run(ph.name, func(t *testing.T) {
			st := newTestStore(t)
			if ph.typ == TypeEC2TransitGatewayPolicyTableEntry {
				tgxSeedParent(t, st, TypeEC2TransitGatewayPolicyTable, tgxPTARN(tgxPT1), testRegion, "available", testScanID)
			} else {
				tgxSeedParent(t, st, TypeEC2TransitGatewayRouteTable, tgxRTARN(tgxRT1), testRegion, "available", testScanID)
			}
			calls := 0
			_, _, err := ph.run(context.Background(), tgxClient(map[string]func(any) (any, error){
				ph.sdkOp: func(any) (any, error) {
					calls++
					if calls > 3 {
						return nil, errTgxTest
					}
					return ph.repeat, nil
				},
			}), newTestAccount(testAccountID), testRegion, st, testScanID)
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if calls != 2 {
				t.Errorf("calls = %d, want 2 (stop when the token repeats)", calls)
			}
		})
	}
}

// A stored parent whose NativeID is not its scanner's ec2ARN shape fails the
// phase rather than being queried with a wrong id.
func TestEC2StoredParents_BadNativeID(t *testing.T) {
	st := newTestStore(t)
	tgxSeedParent(t, st, TypeEC2TransitGatewayRouteTable, "tgw-rtb-bare", testRegion, "available", testScanID)
	_, err := ec2StoredParents(st, newTestAccount(testAccountID), testRegion, testScanID, TypeEC2TransitGatewayRouteTable, "transit-gateway-route-table", ec2TGWParentDeleted)
	if !errors.Is(err, errEC2ParentNativeID) {
		t.Fatalf("err = %v, want errEC2ParentNativeID", err)
	}
}

// tgxOlderSnapshot is a snapshot row an older category (compute mgmt) stores.
const tgxOlderSnapshot = "snap-tgx-older"

// tgxTopLevelHandlers answers the parent listings the two phases read, their
// child ops, and DescribeSnapshots for an unrelated older-category row; scanEC2
// runs every other EC2 op against the denial.
func tgxTopLevelHandlers(entryCalls, refCalls *parentCalls) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"DescribeSnapshots": func(any) (any, error) {
			return &ec2.DescribeSnapshotsOutput{Snapshots: []ec2types.Snapshot{{SnapshotId: ptrStr(tgxOlderSnapshot)}}}, nil
		},
		"DescribeTransitGatewayPolicyTables": func(any) (any, error) {
			return &ec2.DescribeTransitGatewayPolicyTablesOutput{TransitGatewayPolicyTables: []ec2types.TransitGatewayPolicyTable{
				{TransitGatewayPolicyTableId: ptrStr(tgxPT1), State: ec2types.TransitGatewayPolicyTableStateAvailable},
				{TransitGatewayPolicyTableId: ptrStr(tgxPT2), State: ec2types.TransitGatewayPolicyTableStateAvailable},
				{TransitGatewayPolicyTableId: ptrStr(tgxPT3), State: ec2types.TransitGatewayPolicyTableStateDeleted},
			}}, nil
		},
		"DescribeTransitGatewayRouteTables": func(any) (any, error) {
			return &ec2.DescribeTransitGatewayRouteTablesOutput{TransitGatewayRouteTables: []ec2types.TransitGatewayRouteTable{
				{TransitGatewayRouteTableId: ptrStr(tgxRT1), State: ec2types.TransitGatewayRouteTableStateAvailable},
				{TransitGatewayRouteTableId: ptrStr(tgxRT2), State: ec2types.TransitGatewayRouteTableStateAvailable},
				{TransitGatewayRouteTableId: ptrStr(tgxRT3), State: ec2types.TransitGatewayRouteTableStateDeleted},
			}}, nil
		},
		"GetTransitGatewayPolicyTableEntries":   tgxEntriesHandler(entryCalls),
		"GetTransitGatewayPrefixListReferences": tgxRefsHandler(refCalls),
	}
}

// tgxScanEC2 runs scanEC2 with DescribeSnapshots answering only once both
// parent listings have been answered (or its context is cancelled), so that
// when it fails, the transit gateway category has listed the parents before
// runScanners cancels it.
func tgxScanEC2(st *store.Store, handlers map[string]func(in any) (any, error)) error {
	ctxHandlers := ignoreCtx(handlers)
	var listed []chan struct{}
	for _, op := range []string{"DescribeTransitGatewayPolicyTables", "DescribeTransitGatewayRouteTables"} {
		done := make(chan struct{})
		var once sync.Once
		listed = append(listed, done)
		ctxHandlers[op] = func(_ context.Context, in any) (any, error) {
			defer once.Do(func() { close(done) })
			return handlers[op](in)
		}
	}
	ctxHandlers["DescribeSnapshots"] = func(ctx context.Context, in any) (any, error) {
		for _, done := range listed {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-done:
			case <-time.After(5 * time.Second):
				return nil, errors.New("gate: parent listings never called")
			}
		}
		return handlers["DescribeSnapshots"](in)
	}
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfigCtx(ctxHandlers, tgxDenied)
	_, _, err := scanEC2(context.Background(), acct, testRegion, st, testScanID)
	return err
}

// scanEC2 lists the child rows of the live policy tables and route tables its
// own categories stored this scan.
func TestScanEC2_StoresTGWExtendedRows(t *testing.T) {
	st := newTestStore(t)
	var entryCalls, refCalls parentCalls
	if err := tgxScanEC2(st, tgxTopLevelHandlers(&entryCalls, &refCalls)); err != nil {
		t.Fatalf("scanEC2: %v", err)
	}
	if got := entryCalls.distinct(); !slices.Equal(got, []string{tgxPT1, tgxPT2}) {
		t.Errorf("policy tables queried = %v, want the live [%s %s]", got, tgxPT1, tgxPT2)
	}
	if got := refCalls.distinct(); !slices.Equal(got, []string{tgxRT1, tgxRT2}) {
		t.Errorf("route tables queried = %v, want the live [%s %s]", got, tgxRT1, tgxRT2)
	}
	for typ, want := range map[string]int{TypeEC2TransitGatewayPolicyTableEntry: 3, TypeEC2TransitGatewayPrefixListReference: 3} {
		if n := len(rowsByNativeID(t, st, typ)); n != want {
			t.Errorf("%s rows = %d, want %d", typ, n, want)
		}
	}
	assertContainsNative(t, st, tgxPTARN(tgxPT1), tgxEntryID(tgxPT1, "100"))
	assertContainsNative(t, st, tgxRTARN(tgxRT1), tgxRefID(tgxRT1, tgxPL1))
}

// The two phases run after every older category: a hard failure of one
// leaves an older category's slow call uncancelled and does not stop the
// other phase, and the error reaches the caller.
func TestScanEC2_TGWExtendedPhasesAfterOlderCategories(t *testing.T) {
	snapshotID := ec2ARN(testRegion, testAccountID, "snapshot", tgxOlderSnapshot)
	for _, failed := range tgxPhases {
		t.Run(failed.name+" fails", func(t *testing.T) {
			st := newTestStore(t)
			var entryCalls, refCalls parentCalls
			h := tgxTopLevelHandlers(&entryCalls, &refCalls)
			h[failed.sdkOp] = func(any) (any, error) { return nil, errTgxTest }
			ctxHandlers := ignoreCtx(h)
			// DescribeSnapshots answers only after a pause, and gives up if
			// its context is cancelled first, as it would be by a sibling
			// failure. A timer, not a gate on the failing op: the late
			// phases run only after runScanners returns, and runScanners
			// waits for DescribeSnapshots, so a gate would deadlock.
			ctxHandlers["DescribeSnapshots"] = func(ctx context.Context, in any) (any, error) {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(200 * time.Millisecond):
					return h["DescribeSnapshots"](in)
				}
			}
			acct := newTestAccount(testAccountID)
			acct.cfg = opStubConfigCtx(ctxHandlers, tgxDenied)
			_, _, err := scanEC2(context.Background(), acct, testRegion, st, testScanID)
			if !errors.Is(err, errTgxTest) {
				t.Fatalf("err = %v, want the %s error", err, failed.sdkOp)
			}
			if _, ok := rowsByNativeID(t, st, TypeEC2Snapshot)[snapshotID]; !ok {
				t.Errorf("snapshot row missing: the %s failure cancelled an older category", failed.sdkOp)
			}
			for _, other := range tgxPhases {
				if other.name != failed.name && len(rowsByNativeID(t, st, other.typ)) == 0 {
					t.Errorf("%s rows missing after the %s failure", other.typ, failed.sdkOp)
				}
			}
		})
	}
}

// A transient older category is demoted to one warning under its op instead of
// buried in a new phase's hard error, and a transient new phase does not mask
// an older category's hard error.
func TestScanEC2_TGWExtendedTransientDoesNotMaskHardError(t *testing.T) {
	type tc struct{ name, transientOp, hardOp, warnOp string }
	var cases []tc
	for _, ph := range tgxPhases {
		cases = append(cases,
			tc{"transient older category, " + ph.name + " fails", "DescribeSnapshots", ph.sdkOp, "ec2"},
			tc{"transient " + ph.name, ph.sdkOp, "DescribeSnapshots", ph.op})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			var entryCalls, refCalls parentCalls
			h := tgxTopLevelHandlers(&entryCalls, &refCalls)
			h[c.transientOp] = func(any) (any, error) { return nil, io.EOF }
			h[c.hardOp] = func(any) (any, error) { return nil, errTgxTest }
			warnings := countOpWarnings(st, c.warnOp)
			err := tgxScanEC2(st, h)
			if !errors.Is(err, errTgxTest) {
				t.Fatalf("err = %v, want the %s error", err, c.hardOp)
			}
			if got := classifyServiceError(err); got != outcomeError {
				t.Errorf("classifyServiceError = %v, want outcomeError", got)
			}
			if *warnings != 1 {
				t.Errorf("%s warnings = %d, want 1", c.warnOp, *warnings)
			}
		})
	}
}
