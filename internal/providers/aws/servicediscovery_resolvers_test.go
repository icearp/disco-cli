package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveServiceDiscoveryServiceNamespace(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	nsID := "ns-abc"
	nsARN := sdNamespaceARN(testRegion, acct.ID, nsID)
	nsRowID := upsertTestResource(t, st, "aws", acct.ID, TypeServiceDiscoveryPrivateDNSNamespace, nsARN, testRegion, "{}")

	svcARN := fmt.Sprintf("arn:aws:servicediscovery:%s:%s:service/srv-1", testRegion, acct.ID)
	svcAttrs := fmt.Sprintf(`{"DnsConfig":{"NamespaceId":%q}}`, nsID)
	svcID := upsertTestResource(t, st, "aws", acct.ID, TypeServiceDiscoveryService, svcARN, testRegion, svcAttrs)

	if err := resolveServiceDiscoveryServiceNamespace(acct, st); err != nil {
		t.Fatalf("resolveServiceDiscoveryServiceNamespace: %v", err)
	}
	rels, _ := st.RelationshipsFrom(svcID)
	assertRelationship(t, rels, svcID, nsRowID, store.RelAttachedTo)
}

func TestResolveServiceDiscoveryNamespaceHostedZone(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	zoneARN := "arn:aws:route53:::hostedzone/Z1234567890"
	zoneID := upsertTestResource(t, st, "aws", acct.ID, TypeRoute53HostedZone, zoneARN, "", "{}")

	nsARN := sdNamespaceARN(testRegion, acct.ID, "ns-abc")
	nsAttrs := `{"Properties":{"DnsProperties":{"HostedZoneId":"Z1234567890"}}}`
	nsRowID := upsertTestResource(t, st, "aws", acct.ID, TypeServiceDiscoveryPrivateDNSNamespace, nsARN, testRegion, nsAttrs)

	if err := resolveServiceDiscoveryNamespaceHostedZone(acct, st); err != nil {
		t.Fatalf("resolveServiceDiscoveryNamespaceHostedZone: %v", err)
	}
	rels, _ := st.RelationshipsFrom(nsRowID)
	assertRelationship(t, rels, nsRowID, zoneID, store.RelUses)
}

func TestResolveServiceDiscoveryInstanceService(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	svcARN := fmt.Sprintf("arn:aws:servicediscovery:%s:%s:service/srv-1", testRegion, acct.ID)
	svcID := upsertTestResource(t, st, "aws", acct.ID, TypeServiceDiscoveryService, svcARN, testRegion, "{}")

	insARN := svcARN + "/instance/i-1"
	insID := upsertTestResource(t, st, "aws", acct.ID, TypeServiceDiscoveryInstance, insARN, testRegion, "{}")

	if err := resolveServiceDiscoveryInstanceService(acct, st); err != nil {
		t.Fatalf("resolveServiceDiscoveryInstanceService: %v", err)
	}
	rels, _ := st.RelationshipsFrom(insID)
	assertRelationship(t, rels, insID, svcID, store.RelAttachedTo)
}
