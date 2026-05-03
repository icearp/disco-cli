package aws

import (
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveR53RResolverEndpointVPC(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	vARN := ec2ARN(testRegion, acct.ID, "vpc", "vpc-1")
	vID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, vARN, testRegion, "{}")
	eARN := r53rARN(testRegion, acct.ID, "resolver-endpoint", "rslvr-in-001")
	attrs := `{"HostVPCId":"vpc-1"}`
	eID := upsertTestResource(t, st, "aws", acct.ID, TypeRoute53ResolverResolverEndpoint, eARN, testRegion, attrs)
	if err := resolveR53RResolverEndpointVPC(acct, st); err != nil {
		t.Fatalf("resolveR53RResolverEndpointVPC: %v", err)
	}
	rels, _ := st.RelationshipsFrom(eID)
	assertRelationship(t, rels, eID, vID, store.RelAttachedTo)
}

func TestResolveR53RDNSSECConfigVPC(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	vARN := ec2ARN(testRegion, acct.ID, "vpc", "vpc-2")
	vID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, vARN, testRegion, "{}")
	cARN := r53rARN(testRegion, acct.ID, "resolver-dnssec-config", "cfg-1")
	attrs := `{"ResourceId":"vpc-2"}`
	cID := upsertTestResource(t, st, "aws", acct.ID, TypeRoute53ResolverResolverDNSSECConfig, cARN, testRegion, attrs)
	if err := resolveR53RDNSSECConfigVPC(acct, st); err != nil {
		t.Fatalf("resolveR53RDNSSECConfigVPC: %v", err)
	}
	rels, _ := st.RelationshipsFrom(cID)
	assertRelationship(t, rels, cID, vID, store.RelAttachedTo)
}

func TestResolveR53RQueryLogAssocRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	vARN := ec2ARN(testRegion, acct.ID, "vpc", "vpc-3")
	vID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, vARN, testRegion, "{}")
	cfgARN := r53rARN(testRegion, acct.ID, "resolver-query-logging-config", "qlc-1")
	cfgID := upsertTestResource(t, st, "aws", acct.ID, TypeRoute53ResolverResolverQueryLoggingConfig, cfgARN, testRegion, `{"Id":"qlc-1"}`)
	aARN := r53rARN(testRegion, acct.ID, "resolver-query-logging-config-association", "a-1")
	attrs := `{"ResolverQueryLogConfigId":"qlc-1","ResourceId":"vpc-3"}`
	aID := upsertTestResource(t, st, "aws", acct.ID, TypeRoute53ResolverResolverQueryLoggingConfigAssociation, aARN, testRegion, attrs)
	if err := resolveR53RQueryLogAssocRefs(acct, st); err != nil {
		t.Fatalf("resolveR53RQueryLogAssocRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(aID)
	assertRelationship(t, rels, aID, cfgID, store.RelAttachedTo)
	assertRelationship(t, rels, aID, vID, store.RelAttachedTo)
}
