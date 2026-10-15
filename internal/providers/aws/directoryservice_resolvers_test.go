package aws

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	dstypes "github.com/aws/aws-sdk-go-v2/service/directoryservice/types"
	"github.com/icearp/disco-cli/store"
)

func TestResolveDirectoryServiceVpcRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	vpcARN := ec2ARN(testRegion, acct.ID, "vpc", "vpc-1")
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, vpcARN, testRegion, "{}")
	subARN := ec2ARN(testRegion, acct.ID, "subnet", "subnet-1")
	subID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, subARN, testRegion, "{}")
	sgARN := ec2ARN(testRegion, acct.ID, "security-group", "sg-1")
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroup, sgARN, testRegion, "{}")
	dARN := fmt.Sprintf("arn:aws:ds:%s:%s:directory/d-1", testRegion, acct.ID)
	attrs := `{"VpcSettings":{"VpcId":"vpc-1","SubnetIds":["subnet-1"],"SecurityGroupId":"sg-1"}}`
	dID := upsertTestResource(t, st, "aws", acct.ID, TypeDSMicrosoftAD, dARN, testRegion, attrs)
	if err := resolveDirectoryServiceVpcRefs(acct, st); err != nil {
		t.Fatalf("resolveDirectoryServiceVpcRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(dID)
	assertRelationship(t, rels, dID, vpcID, store.RelAttachedTo)
	assertRelationship(t, rels, dID, subID, store.RelAttachedTo)
	assertRelationship(t, rels, dID, sgID, store.RelAttachedTo)
}

func dsRelsFrom(t *testing.T, st *store.Store, id string) []store.Relationship {
	t.Helper()
	rels, err := st.RelationshipsFrom(id)
	if err != nil {
		t.Fatalf("RelationshipsFrom(%s): %v", id, err)
	}
	return rels
}

func TestResolveDSEventTopics_RoutesToScannedTopic(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	topicARN := "arn:aws:sns:" + testRegion + ":" + testAccountID + ":ds-status"
	topicID := upsertTestResource(t, st, "aws", acct.ID, TypeSNSTopic, topicARN, testRegion, "{}")
	dirARN := dsDirARN("d-1")
	scanned := upsertTestResource(t, st, "aws", acct.ID, TypeDSEventTopic, dirARN+"/event-topic/ds-status", testRegion,
		mustJSON(dstypes.EventTopic{DirectoryId: ptrStr("d-1"), TopicName: ptrStr("ds-status"), TopicArn: &topicARN}))
	unscanned := upsertTestResource(t, st, "aws", acct.ID, TypeDSEventTopic, dirARN+"/event-topic/other", testRegion,
		mustJSON(dstypes.EventTopic{DirectoryId: ptrStr("d-1"), TopicName: ptrStr("other"), TopicArn: ptrStr("arn:aws:sns:us-east-1:123456789012:other")}))
	noARN := upsertTestResource(t, st, "aws", acct.ID, TypeDSEventTopic, dirARN+"/event-topic/bare", testRegion,
		mustJSON(dstypes.EventTopic{DirectoryId: ptrStr("d-1"), TopicName: ptrStr("bare")}))
	if err := resolveDSEventTopics(acct, st); err != nil {
		t.Fatalf("resolveDSEventTopics: %v", err)
	}
	rels := dsRelsFrom(t, st, scanned)
	if len(rels) != 1 {
		t.Errorf("edges from scanned-topic registration = %v, want exactly one", rels)
	}
	assertRelationship(t, rels, scanned, topicID, store.RelRoutesTo)
	for _, id := range []string{unscanned, noARN} {
		if rels := dsRelsFrom(t, st, id); len(rels) != 0 {
			t.Errorf("edges from %s = %v, want none", id, rels)
		}
	}
}

func TestResolveDSLogSubscriptions_RoutesToScannedLogGroup(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	groupID := upsertTestResource(t, st, "aws", acct.ID, TypeLogsLogGroup, logGroupNativeIDFromName(acct.ID, testRegion, "/aws/ds/d-1"), testRegion, "{}")
	scanned := upsertTestResource(t, st, "aws", acct.ID, TypeDSLogSubscription, dsDirARN("d-1")+"/log-subscription//aws/ds/d-1", testRegion,
		mustJSON(dstypes.LogSubscription{DirectoryId: ptrStr("d-1"), LogGroupName: ptrStr("/aws/ds/d-1")}))
	unscanned := upsertTestResource(t, st, "aws", acct.ID, TypeDSLogSubscription, dsDirARN("d-2")+"/log-subscription//aws/ds/d-2", testRegion,
		mustJSON(dstypes.LogSubscription{DirectoryId: ptrStr("d-2"), LogGroupName: ptrStr("/aws/ds/d-2")}))
	if err := resolveDSLogSubscriptions(acct, st); err != nil {
		t.Fatalf("resolveDSLogSubscriptions: %v", err)
	}
	rels := dsRelsFrom(t, st, scanned)
	if len(rels) != 1 {
		t.Errorf("edges from subscription = %v, want exactly one", rels)
	}
	assertRelationship(t, rels, scanned, groupID, store.RelRoutesTo)
	if rels := dsRelsFrom(t, st, unscanned); len(rels) != 0 {
		t.Errorf("edges to unscanned group = %v, want none", rels)
	}
}

// Rows whose attributes do not decode are reported, not skipped silently.
func TestResolveDS_UndecodableAttributesReturnError(t *testing.T) {
	for name, c := range map[string]struct {
		typ     string
		resolve func(*account, *store.Store) error
	}{
		"event topic":      {TypeDSEventTopic, resolveDSEventTopics},
		"log subscription": {TypeDSLogSubscription, resolveDSLogSubscriptions},
	} {
		t.Run(name, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			upsertTestResource(t, st, "aws", acct.ID, c.typ, dsDirARN("d-1")+"/x/bad", testRegion, `{"TopicArn":7,"LogGroupName":7}`)
			var typeErr *json.UnmarshalTypeError
			if err := c.resolve(acct, st); !errors.As(err, &typeErr) {
				t.Errorf("err = %v, want the attributes decode error", err)
			}
		})
	}
}
