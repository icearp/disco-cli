package aws

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	amptypes "github.com/aws/aws-sdk-go-v2/service/amp/types"

	"github.com/icearp/disco-cli/store"
)

// apsScraperFixture seeds one scanned target of every type a scraper can
// reference, plus a second workspace no scraper names.
type apsScraperFixture struct {
	roleARN, wsARN, eksARN                      string
	roleID, wsID, otherWsID, eksID, sgID, subID string
}

func seedAPSScraperTargets(t *testing.T, st *store.Store, acct *account) apsScraperFixture {
	t.Helper()
	f := apsScraperFixture{
		roleARN: fmt.Sprintf("arn:aws:iam::%s:role/scraper", acct.ID),
		wsARN:   fmt.Sprintf("arn:aws:aps:%s:%s:workspace/ws-1", testRegion, acct.ID),
		eksARN:  fmt.Sprintf("arn:aws:eks:%s:%s:cluster/c1", testRegion, acct.ID),
	}
	otherWsARN := fmt.Sprintf("arn:aws:aps:%s:%s:workspace/ws-2", testRegion, acct.ID)
	f.roleID = upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, f.roleARN, "", "{}")
	f.wsID = upsertTestResource(t, st, "aws", acct.ID, TypeAPSWorkspace, f.wsARN, testRegion, "{}")
	f.otherWsID = upsertTestResource(t, st, "aws", acct.ID, TypeAPSWorkspace, otherWsARN, testRegion, "{}")
	f.eksID = upsertTestResource(t, st, "aws", acct.ID, TypeEKSCluster, f.eksARN, testRegion, "{}")
	f.sgID = upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroup,
		ec2ARN(testRegion, acct.ID, "security-group", "sg-1"), testRegion, "{}")
	f.subID = upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet,
		ec2ARN(testRegion, acct.ID, "subnet", "subnet-1"), testRegion, "{}")
	return f
}

func upsertTestScraper(t *testing.T, st *store.Store, acct *account, id string, s amptypes.ScraperSummary) string {
	t.Helper()
	arn := fmt.Sprintf("arn:aws:aps:%s:%s:scraper/%s", testRegion, acct.ID, id)
	return upsertTestResource(t, st, "aws", acct.ID, TypeAPSScraper, arn, testRegion, mustJSON(s))
}

func assertNoRelationshipTo(t *testing.T, rels []store.Relationship, toID string) {
	t.Helper()
	for _, r := range rels {
		if r.ToID == toID {
			t.Errorf("unexpected relationship: %s -[%s]-> %s", r.FromID, r.Kind, r.ToID)
		}
	}
}

func TestResolveAPSScraperTargets_EKSSourceAMPDestination(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	f := seedAPSScraperTargets(t, st, acct)
	scID := upsertTestScraper(t, st, acct, "s-eks", amptypes.ScraperSummary{
		RoleArn: ptrStr(f.roleARN),
		Source: &amptypes.SourceMemberEksConfiguration{Value: amptypes.EksConfiguration{
			ClusterArn: ptrStr(f.eksARN), SecurityGroupIds: []string{"sg-1"}, SubnetIds: []string{"subnet-1"},
		}},
		Destination: &amptypes.DestinationMemberAmpConfiguration{Value: amptypes.AmpConfiguration{WorkspaceArn: ptrStr(f.wsARN)}},
	})

	if err := resolveAPSScraperTargets(acct, st); err != nil {
		t.Fatalf("resolveAPSScraperTargets: %v", err)
	}
	rels, err := st.RelationshipsFrom(scID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, scID, f.roleID, store.RelUses)
	assertRelationship(t, rels, scID, f.wsID, store.RelUses)
	assertRelationship(t, rels, scID, f.eksID, store.RelUses)
	assertRelationship(t, rels, scID, f.sgID, store.RelUses)
	assertRelationship(t, rels, scID, f.subID, store.RelAttachedTo)
	assertNoRelationshipTo(t, rels, f.otherWsID)
	if len(rels) != 5 {
		t.Errorf("got %d relationships, want 5: %v", len(rels), rels)
	}
}

func TestResolveAPSScraperTargets_VPCSourceCloudWatchDestination(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	f := seedAPSScraperTargets(t, st, acct)
	scID := upsertTestScraper(t, st, acct, "s-vpc", amptypes.ScraperSummary{
		Source: &amptypes.SourceMemberVpcConfiguration{Value: amptypes.VpcConfiguration{
			SecurityGroupIds: []string{"sg-1"}, SubnetIds: []string{"subnet-1"},
		}},
		Destination: &amptypes.DestinationMemberCloudWatchConfiguration{Value: amptypes.CloudWatchConfiguration{
			DatasetArn: ptrStr(fmt.Sprintf("arn:aws:logs:%s:%s:dataset/d1", testRegion, acct.ID)),
		}},
	})

	if err := resolveAPSScraperTargets(acct, st); err != nil {
		t.Fatalf("resolveAPSScraperTargets: %v", err)
	}
	rels, err := st.RelationshipsFrom(scID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, scID, f.sgID, store.RelUses)
	assertRelationship(t, rels, scID, f.subID, store.RelAttachedTo)
	assertNoRelationshipTo(t, rels, f.eksID)
	assertNoRelationshipTo(t, rels, f.wsID)
	assertNoRelationshipTo(t, rels, f.otherWsID)
	if len(rels) != 2 {
		t.Errorf("got %d relationships, want 2: %v", len(rels), rels)
	}
}

func TestResolveAPSScraperTargets_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	seedAPSScraperTargets(t, st, acct)
	scID := upsertTestResource(t, st, "aws", acct.ID, TypeAPSScraper,
		fmt.Sprintf("arn:aws:aps:%s:%s:scraper/s-empty", testRegion, acct.ID), testRegion, "{}")

	if err := resolveAPSScraperTargets(acct, st); err != nil {
		t.Fatalf("resolveAPSScraperTargets({}) = %v; want nil", err)
	}
	rels, err := st.RelationshipsFrom(scID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("got relationships %v; want none", rels)
	}
}

// An undecodable row is reported and costs its siblings none of their edges.
func TestResolveAPSScraperTargets_DecodeErrorKeepsSiblings(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	f := seedAPSScraperTargets(t, st, acct)
	upsertTestResource(t, st, "aws", acct.ID, TypeAPSScraper,
		fmt.Sprintf("arn:aws:aps:%s:%s:scraper/s-bad", testRegion, acct.ID), testRegion, `{"Source":"not-an-object"}`)
	scID := upsertTestScraper(t, st, acct, "s-good", amptypes.ScraperSummary{
		Destination: &amptypes.DestinationMemberAmpConfiguration{Value: amptypes.AmpConfiguration{WorkspaceArn: ptrStr(f.wsARN)}},
	})

	err := resolveAPSScraperTargets(acct, st)
	var typeErr *json.UnmarshalTypeError
	if !errors.As(err, &typeErr) {
		t.Fatalf("resolveAPSScraperTargets = %v; want a *json.UnmarshalTypeError", err)
	}
	rels, err := st.RelationshipsFrom(scID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, scID, f.wsID, store.RelUses)
}

func TestResolveAPSRelationships_WorkspaceDecodeErrorKeepsScraperEdges(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	f := seedAPSScraperTargets(t, st, acct)
	upsertTestResource(t, st, "aws", acct.ID, TypeAPSWorkspace,
		fmt.Sprintf("arn:aws:aps:%s:%s:workspace/ws-bad", testRegion, acct.ID), testRegion, `{"KmsKeyArn":7}`)
	scID := upsertTestScraper(t, st, acct, "s-good", amptypes.ScraperSummary{
		Destination: &amptypes.DestinationMemberAmpConfiguration{Value: amptypes.AmpConfiguration{WorkspaceArn: ptrStr(f.wsARN)}},
	})

	err := resolveAPSRelationships(acct, st)
	var typeErr *json.UnmarshalTypeError
	if !errors.As(err, &typeErr) {
		t.Fatalf("resolveAPSRelationships = %v; want a *json.UnmarshalTypeError", err)
	}
	rels, err := st.RelationshipsFrom(scID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, scID, f.wsID, store.RelUses)
}

// A member this SDK version cannot name is stored with a base64 string Value;
// it yields no member edges and must not cost the row its role edge.
func TestResolveAPSScraperTargets_UnknownUnionMemberKeepsRoleEdge(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	f := seedAPSScraperTargets(t, st, acct)
	scID := upsertTestScraper(t, st, acct, "s-unknown", amptypes.ScraperSummary{
		RoleArn:     ptrStr(f.roleARN),
		Source:      &amptypes.UnknownUnionMember{Tag: "FutureSource", Value: []byte(`{"x":1}`)},
		Destination: &amptypes.UnknownUnionMember{Tag: "FutureDestination", Value: []byte(`{"y":2}`)},
	})

	if err := resolveAPSScraperTargets(acct, st); err != nil {
		t.Fatalf("resolveAPSScraperTargets = %v; want nil", err)
	}
	rels, err := st.RelationshipsFrom(scID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, scID, f.roleID, store.RelUses)
	if len(rels) != 1 {
		t.Errorf("got %d relationships, want 1: %v", len(rels), rels)
	}
}
