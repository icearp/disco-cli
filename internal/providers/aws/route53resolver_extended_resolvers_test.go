package aws

import (
	"encoding/json"
	"errors"
	"testing"

	r53rtypes "github.com/aws/aws-sdk-go-v2/service/route53resolver/types"
	"github.com/icearp/disco-cli/store"
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

// r53rUpsertManaged stores a ManagedByProvider row and returns its ID.
func r53rUpsertManaged(t *testing.T, st *store.Store, typ, nativeID, attrs string) string {
	t.Helper()
	region := testRegion
	if _, err := st.UpsertResource(&store.Resource{
		Provider: "aws", AccountID: testAccountID, Type: typ, NativeID: nativeID, Region: &region,
		AttributesJSON: attrs, DiscoveredBy: testScanID, ManagedByProvider: true,
	}); err != nil {
		t.Fatalf("upsert %s: %v", nativeID, err)
	}
	return store.ResourceID("aws", testAccountID, nativeID)
}

func r53rRelsFrom(t *testing.T, st *store.Store, id string) []store.Relationship {
	t.Helper()
	rels, err := st.RelationshipsFrom(id)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	return rels
}

// An address links to its subnet; an address whose subnet was not scanned, or
// that names none, links to nothing.
func TestResolveR53REndpointIPSubnet(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	subnetID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, ec2ARN(testRegion, acct.ID, "subnet", "subnet-a"), testRegion, "{}")
	ip := func(native string, a r53rtypes.IpAddressResponse) string {
		return upsertTestResource(t, st, "aws", acct.ID, TypeRoute53ResolverResolverEndpointIPAddress, native, testRegion, mustJSON(a))
	}
	inA := ip("ep/ip-address/rni-1", r53rtypes.IpAddressResponse{IpId: ptrStr("rni-1"), SubnetId: ptrStr("subnet-a")})
	unscanned := ip("ep/ip-address/rni-3", r53rtypes.IpAddressResponse{IpId: ptrStr("rni-3"), SubnetId: ptrStr("subnet-missing")})
	noSubnet := ip("ep/ip-address/rni-4", r53rtypes.IpAddressResponse{IpId: ptrStr("rni-4")})

	if err := resolveR53REndpointIPSubnet(acct, st); err != nil {
		t.Fatalf("resolveR53REndpointIPSubnet: %v", err)
	}
	rels := r53rRelsFrom(t, st, inA)
	if len(rels) != 1 {
		t.Errorf("%s edges = %v, want one", inA, rels)
	}
	assertRelationship(t, rels, inA, subnetID, store.RelAttachedTo)
	for _, id := range []string{unscanned, noSubnet} {
		if rels := r53rRelsFrom(t, st, id); len(rels) != 0 {
			t.Errorf("%s edges = %v, want none", id, rels)
		}
	}
}

// A rule links to the domain list it matches in its own region, an AWS-managed
// list included; a threat-protection rule, or a rule whose list is in another
// region, links to nothing.
func TestResolveR53RFirewallRuleDomainList(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	// The index keys on the list ARN, so the rows carry no attributes.
	ownedID := upsertTestResource(t, st, "aws", acct.ID, TypeRoute53ResolverFirewallDomainList, r53rARN(testRegion, acct.ID, "firewall-domain-list", "rslvr-fdl-1"), testRegion, "{}")
	managedID := r53rUpsertManaged(t, st, TypeRoute53ResolverFirewallDomainList, r53rARN(testRegion, acct.ID, "firewall-domain-list", "rslvr-fdl-m"), "{}")
	upsertTestResource(t, st, "aws", acct.ID, TypeRoute53ResolverFirewallDomainList, r53rARN("eu-west-1", acct.ID, "firewall-domain-list", "rslvr-fdl-eu"), "eu-west-1", "{}")
	rule := func(native string, r r53rtypes.FirewallRule) string {
		return upsertTestResource(t, st, "aws", acct.ID, TypeRoute53ResolverFirewallRule, native, testRegion, mustJSON(r))
	}
	toOwned := rule("g/firewall-rule/domain-list/rslvr-fdl-1", r53rtypes.FirewallRule{FirewallDomainListId: ptrStr("rslvr-fdl-1")})
	toManaged := rule("g/firewall-rule/domain-list/rslvr-fdl-m", r53rtypes.FirewallRule{FirewallDomainListId: ptrStr("rslvr-fdl-m")})
	otherRegion := rule("g/firewall-rule/domain-list/rslvr-fdl-eu", r53rtypes.FirewallRule{FirewallDomainListId: ptrStr("rslvr-fdl-eu")})
	threat := rule("g/firewall-rule/threat-protection/rslvr-ftp-1", r53rtypes.FirewallRule{FirewallThreatProtectionId: ptrStr("rslvr-ftp-1")})

	if err := resolveR53RFirewallRuleDomainList(acct, st); err != nil {
		t.Fatalf("resolveR53RFirewallRuleDomainList: %v", err)
	}
	for _, tc := range []struct{ from, to string }{{toOwned, ownedID}, {toManaged, managedID}} {
		rels := r53rRelsFrom(t, st, tc.from)
		if len(rels) != 1 {
			t.Errorf("%s edges = %v, want one", tc.from, rels)
		}
		assertRelationship(t, rels, tc.from, tc.to, store.RelUses)
	}
	for _, id := range []string{otherRegion, threat} {
		if rels := r53rRelsFrom(t, st, id); len(rels) != 0 {
			t.Errorf("%s edges = %v, want none", id, rels)
		}
	}
}

func TestResolveR53RChildResolvers_NoRows(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	if err := resolveR53RFirewallRuleDomainList(acct, st); err != nil {
		t.Errorf("rule resolver err = %v, want nil", err)
	}
	if err := resolveR53REndpointIPSubnet(acct, st); err != nil {
		t.Errorf("ip resolver err = %v, want nil", err)
	}
}

// Undecodable attributes fail the resolver rather than dropping its edges.
func TestResolveR53R_DecodeErrorReturned(t *testing.T) {
	for _, tc := range []struct {
		name    string
		typ     string
		resolve func(*account, *store.Store) error
	}{
		{"endpoint ip address", TypeRoute53ResolverResolverEndpointIPAddress, resolveR53REndpointIPSubnet},
		{"firewall rule", TypeRoute53ResolverFirewallRule, resolveR53RFirewallRuleDomainList},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			upsertTestResource(t, st, "aws", acct.ID, TypeRoute53ResolverFirewallRule, "rule-ok", testRegion, `{"FirewallDomainListId":"rslvr-fdl-1"}`)
			upsertTestResource(t, st, "aws", acct.ID, tc.typ, "bad", testRegion, `{"Id":`)
			var syntaxErr *json.SyntaxError
			if err := tc.resolve(acct, st); !errors.As(err, &syntaxErr) {
				t.Errorf("err = %v, want a JSON syntax error", err)
			}
		})
	}
}
