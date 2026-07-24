package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveVpcLatticeALSRefs_Service_LogGroup(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	svcARN := fmt.Sprintf("arn:aws:vpc-lattice:%s:%s:service/svc-1", testRegion, acct.ID)
	svcID := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeService, svcARN, testRegion, "{}")
	lgARN := fmt.Sprintf("arn:aws:logs:%s:%s:log-group:/aws/vpcl", testRegion, acct.ID)
	lgID := upsertTestResource(t, st, "aws", acct.ID, TypeLogsLogGroup, lgARN, testRegion, "{}")
	alsARN := fmt.Sprintf("arn:aws:vpc-lattice:%s:%s:accesslogsubscription/als-1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"ResourceArn":"%s","DestinationArn":"%s:*"}`, svcARN, lgARN)
	alsID := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeAccessLogSubscription, alsARN, testRegion, attrs)
	if err := resolveVpcLatticeALSRefs(acct, st); err != nil {
		t.Fatalf("resolveVpcLatticeALSRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(alsID)
	assertRelationship(t, rels, alsID, svcID, store.RelAttachedTo)
	assertRelationship(t, rels, alsID, lgID, store.RelUses)
}

func TestResolveVpcLatticeALSRefs_ServiceNetwork_S3(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	snARN := fmt.Sprintf("arn:aws:vpc-lattice:%s:%s:servicenetwork/sn-1", testRegion, acct.ID)
	snID := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeServiceNetwork, snARN, testRegion, "{}")
	bktARN := "arn:aws:s3:::vpcl-logs"
	bktID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, bktARN, "us-east-1", "{}")
	alsARN := fmt.Sprintf("arn:aws:vpc-lattice:%s:%s:accesslogsubscription/als-2", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"ResourceArn":"%s","DestinationArn":"%s"}`, snARN, bktARN)
	alsID := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeAccessLogSubscription, alsARN, testRegion, attrs)
	if err := resolveVpcLatticeALSRefs(acct, st); err != nil {
		t.Fatalf("resolveVpcLatticeALSRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(alsID)
	assertRelationship(t, rels, alsID, snID, store.RelAttachedTo)
	assertRelationship(t, rels, alsID, bktID, store.RelUses)
}

func TestResolveVpcLatticeREARefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	rcARN := fmt.Sprintf("arn:aws:vpc-lattice:%s:%s:resourceconfiguration/rc-1", testRegion, acct.ID)
	rcID := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeResourceConfiguration, rcARN, testRegion, "{}")
	reaARN := fmt.Sprintf("arn:aws:vpc-lattice:%s:%s:resourceendpointassociation/rea-1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"ResourceConfigurationArn":"%s"}`, rcARN)
	reaID := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeResourceEndpointAssociation, reaARN, testRegion, attrs)
	if err := resolveVpcLatticeREARefs(acct, st); err != nil {
		t.Fatalf("resolveVpcLatticeREARefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(reaID)
	assertRelationship(t, rels, reaID, rcID, store.RelAttachedTo)
}

func TestResolveVpcLatticeREARefs_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	reaARN := fmt.Sprintf("arn:aws:vpc-lattice:%s:%s:resourceendpointassociation/rea-1", testRegion, acct.ID)
	reaID := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeResourceEndpointAssociation, reaARN, testRegion, "{}")
	if err := resolveVpcLatticeREARefs(acct, st); err != nil {
		t.Fatalf("resolveVpcLatticeREARefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(reaID)
	if len(rels) != 0 {
		t.Fatalf("expected no edges for REA with no attrs, got %d", len(rels))
	}
}
