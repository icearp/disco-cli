package aws

import (
	"encoding/json"
	"errors"
	"testing"

	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/icearp/disco-cli/store"
)

// tgxRefAttrs is the AttributesJSON scanEC2TGWPrefixListReferences stores for
// a reference to prefix list pl with attachment att ("" for none).
func tgxRefAttrs(pl, att string) string {
	ref := ec2types.TransitGatewayPrefixListReference{PrefixListId: ptrStr(pl), TransitGatewayRouteTableId: ptrStr(tgxRT1)}
	if att != "" {
		ref.TransitGatewayAttachment = &ec2types.TransitGatewayPrefixListAttachment{TransitGatewayAttachmentId: ptrStr(att)}
	}
	return mustJSON(ref)
}

// tgxSeedRefTargets stores, in testRegion, prefix list 1 owned by AWS (its
// ARN carries no account of ours, and the row is provider-managed), prefix list 2 in another Region only, and
// the attachment.
func tgxSeedRefTargets(t *testing.T, st *store.Store) (pl1, att string) {
	t.Helper()
	pl1ARN, region := "arn:aws:ec2:us-east-1:aws:prefix-list/"+tgxPL1, testRegion
	// scanPrefixLists flags an AWS-owned list provider-managed.
	if _, err := st.UpsertResource(&store.Resource{
		Provider: "aws", AccountID: testAccountID, Type: TypeEC2PrefixList, NativeID: pl1ARN,
		Region: &region, AttributesJSON: "{}", DiscoveredBy: testScanID, ManagedByProvider: true,
	}); err != nil {
		t.Fatalf("seed prefix list: %v", err)
	}
	pl1 = store.ResourceID("aws", testAccountID, pl1ARN)
	upsertTestResource(t, st, "aws", testAccountID, TypeEC2PrefixList, "arn:aws:ec2:eu-west-1:123456789012:prefix-list/"+tgxPL2, "eu-west-1", "{}")
	att = upsertTestResource(t, st, "aws", testAccountID, TypeEC2TransitGatewayAttachment,
		ec2ARN(testRegion, testAccountID, "transit-gateway-attachment", tgxAtt), testRegion, "{}")
	return pl1, att
}

func tgxAttachedTo(t *testing.T, st *store.Store, from string) map[string]bool {
	t.Helper()
	rels, err := st.RelationshipsFrom(from, store.RelAttachedTo)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	out := map[string]bool{}
	for _, r := range rels {
		out[r.ToID] = true
	}
	return out
}

func TestResolveTGWPrefixListReference_LinksPrefixListAndAttachment(t *testing.T) {
	st := newTestStore(t)
	pl1, att := tgxSeedRefTargets(t, st)
	ref := upsertTestResource(t, st, "aws", testAccountID, TypeEC2TransitGatewayPrefixListReference,
		tgxRefID(tgxRT1, tgxPL1), testRegion, tgxRefAttrs(tgxPL1, tgxAtt))
	if err := resolveTGWPrefixListReferenceRelationships(newTestAccount(testAccountID), st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	got := tgxAttachedTo(t, st, ref)
	if !got[pl1] || !got[att] || len(got) != 2 {
		t.Errorf("attached-to = %v, want exactly {%s, %s}", got, pl1, att)
	}
}

// A prefix list scanned only in another Region, and an attachment not scanned
// at all, get no edge.
func TestResolveTGWPrefixListReference_UnscannedTargets(t *testing.T) {
	st := newTestStore(t)
	tgxSeedRefTargets(t, st)
	ref := upsertTestResource(t, st, "aws", testAccountID, TypeEC2TransitGatewayPrefixListReference,
		tgxRefID(tgxRT1, tgxPL2), testRegion, tgxRefAttrs(tgxPL2, "tgw-attach-0unscanned"))
	if err := resolveTGWPrefixListReferenceRelationships(newTestAccount(testAccountID), st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got := tgxAttachedTo(t, st, ref); len(got) != 0 {
		t.Errorf("attached-to = %v, want none", got)
	}
}

func TestResolveTGWPrefixListReference_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	tgxSeedRefTargets(t, st)
	ref := upsertTestResource(t, st, "aws", testAccountID, TypeEC2TransitGatewayPrefixListReference,
		tgxRefID(tgxRT1, tgxPL1), testRegion, "{}")
	if err := resolveTGWPrefixListReferenceRelationships(newTestAccount(testAccountID), st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got := tgxAttachedTo(t, st, ref); len(got) != 0 {
		t.Errorf("attached-to = %v, want none", got)
	}
}

// A row whose attributes do not decode is returned as an error and costs the
// row listed after it none of its edges.
func TestResolveTGWPrefixListReference_DecodeErrorKeepsLaterRows(t *testing.T) {
	st := newTestStore(t)
	pl1, att := tgxSeedRefTargets(t, st)
	upsertTestResourceNamed(t, st, TypeEC2TransitGatewayPrefixListReference, tgxRefID(tgxRT2, tgxPL1), testRegion, `{"PrefixListId":7}`, "a-bad")
	good := upsertTestResourceNamed(t, st, TypeEC2TransitGatewayPrefixListReference, tgxRefID(tgxRT1, tgxPL1), testRegion,
		tgxRefAttrs(tgxPL1, tgxAtt), "b-good")
	var typeErr *json.UnmarshalTypeError
	if err := resolveTGWPrefixListReferenceRelationships(newTestAccount(testAccountID), st); !errors.As(err, &typeErr) {
		t.Fatalf("err = %v, want the *json.UnmarshalTypeError", err)
	}
	if got := tgxAttachedTo(t, st, good); !got[pl1] || !got[att] {
		t.Errorf("later row attached-to = %v, want {%s, %s}", got, pl1, att)
	}
}

// tgxEntryAttrs is the AttributesJSON scanEC2TGWPolicyTableEntries stores for
// an entry targeting route table rt ("" for none).
func tgxEntryAttrs(rt string) string {
	e := ec2types.TransitGatewayPolicyTableEntry{PolicyRuleNumber: ptrStr("100"), State: ec2types.TransitGatewayPolicyTableEntryStateActive}
	if rt != "" {
		e.TargetRouteTableId = ptrStr(rt)
	}
	return mustJSON(e)
}

// tgxSeedEntryTargets stores route table 1 in testRegion and route table 2
// in another Region only.
func tgxSeedEntryTargets(t *testing.T, st *store.Store) (rt1 string) {
	t.Helper()
	rt1 = upsertTestResource(t, st, "aws", testAccountID, TypeEC2TransitGatewayRouteTable, tgxRTARN(tgxRT1), testRegion, "{}")
	upsertTestResource(t, st, "aws", testAccountID, TypeEC2TransitGatewayRouteTable, tgxRTEUARN, tgxOtherRegion, "{}")
	return rt1
}

const tgxOtherRegion = "eu-west-1"

var tgxRTEUARN = ec2ARN(tgxOtherRegion, testAccountID, "transit-gateway-route-table", tgxRT2)

func TestResolveTGWPolicyTableEntry_Edges(t *testing.T) {
	cases := []struct {
		name, region, attrs string
		// want is the NativeID of the one route table linked, "" for none.
		want string
	}{
		{"scanned target", testRegion, tgxEntryAttrs(tgxRT1), tgxRTARN(tgxRT1)},
		{"target scanned in the entry's other Region", tgxOtherRegion, tgxEntryAttrs(tgxRT2), tgxRTEUARN},
		{"target scanned only in another Region", testRegion, tgxEntryAttrs(tgxRT2), ""},
		{"target not scanned", testRegion, tgxEntryAttrs(tgxRT3), ""},
		{"no TargetRouteTableId", testRegion, tgxEntryAttrs(""), ""},
		{"no attrs", testRegion, "{}", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			tgxSeedEntryTargets(t, st)
			entry := upsertTestResource(t, st, "aws", testAccountID, TypeEC2TransitGatewayPolicyTableEntry,
				tgxEntryID(tgxPT1, "100"), c.region, c.attrs)
			if err := resolveTGWPolicyTableEntryRelationships(newTestAccount(testAccountID), st); err != nil {
				t.Fatalf("resolve: %v", err)
			}
			got := tgxAttachedTo(t, st, entry)
			if c.want == "" && len(got) != 0 {
				t.Errorf("attached-to = %v, want none", got)
			}
			if want := store.ResourceID("aws", testAccountID, c.want); c.want != "" && (!got[want] || len(got) != 1) {
				t.Errorf("attached-to = %v, want exactly {%s}", got, want)
			}
		})
	}
}

// A row whose attributes do not decode is returned as an error and costs the
// row listed after it no edge.
func TestResolveTGWPolicyTableEntry_DecodeErrorKeepsLaterRows(t *testing.T) {
	st := newTestStore(t)
	rt1 := tgxSeedEntryTargets(t, st)
	upsertTestResourceNamed(t, st, TypeEC2TransitGatewayPolicyTableEntry, tgxEntryID(tgxPT1, "50"), testRegion, `{"TargetRouteTableId":7}`, "a-bad")
	good := upsertTestResourceNamed(t, st, TypeEC2TransitGatewayPolicyTableEntry, tgxEntryID(tgxPT1, "100"), testRegion,
		tgxEntryAttrs(tgxRT1), "b-good")
	var typeErr *json.UnmarshalTypeError
	if err := resolveTGWPolicyTableEntryRelationships(newTestAccount(testAccountID), st); !errors.As(err, &typeErr) {
		t.Fatalf("err = %v, want the *json.UnmarshalTypeError", err)
	}
	if got := tgxAttachedTo(t, st, good); !got[rt1] {
		t.Errorf("later row attached-to = %v, want %s", got, rt1)
	}
}
