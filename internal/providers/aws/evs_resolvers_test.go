package aws

import (
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	evstypes "github.com/aws/aws-sdk-go-v2/service/evs/types"
	"github.com/icearp/disco-cli/store"
)

// assertNoEdgesFrom fails the test if id has any outbound edge.
func assertNoEdgesFrom(t *testing.T, st *store.Store, id string) {
	t.Helper()
	rels, err := st.RelationshipsFrom(id)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("edges from %s = %v, want none", id, rels)
	}
}

func TestResolveEVSHostRefs_InstanceAndDedicatedHost(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	instARN := ec2ARN(testRegion, testAccountID, "instance", "i-0abc")
	dhARN := ec2ARN(testRegion, testAccountID, "dedicated-host", "h-0abc")
	instID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Instance, instARN, testRegion, "{}")
	dhID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Host, dhARN, testRegion, "{}")
	hostID := upsertTestResource(t, st, "aws", acct.ID, TypeEVSEnvironmentHost, evsTestHostA1, testRegion,
		mustJSON(evstypes.Host{HostName: sdkaws.String("esx-a1"), Ec2InstanceId: sdkaws.String("i-0abc"), DedicatedHostId: sdkaws.String("h-0abc")}))
	// Its instance was not scanned: no edge.
	orphanID := upsertTestResource(t, st, "aws", acct.ID, TypeEVSEnvironmentHost, evsTestHostA2, testRegion,
		mustJSON(evstypes.Host{HostName: sdkaws.String("esx-a2"), Ec2InstanceId: sdkaws.String("i-0zzz")}))

	if err := resolveEVSHostRefs(acct, st); err != nil {
		t.Fatalf("resolveEVSHostRefs: %v", err)
	}
	rels, err := st.RelationshipsFrom(hostID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 2 {
		t.Errorf("host edges = %d, want 2", len(rels))
	}
	assertRelationship(t, rels, hostID, instID, store.RelUses)
	assertRelationship(t, rels, hostID, dhID, store.RelAttachedTo)
	assertNoEdgesFrom(t, st, orphanID)
}

func TestResolveEVSVlanSubnets_Subnet(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	subnetID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, ec2ARN(testRegion, testAccountID, "subnet", "subnet-0abc"), testRegion, "{}")
	vlanID := upsertTestResource(t, st, "aws", acct.ID, TypeEVSEnvironmentVlan, evsTestVlanA1, testRegion,
		mustJSON(evstypes.Vlan{FunctionName: sdkaws.String("hcx"), SubnetId: sdkaws.String("subnet-0abc")}))

	if err := resolveEVSVlanSubnets(acct, st); err != nil {
		t.Fatalf("resolveEVSVlanSubnets: %v", err)
	}
	rels, err := st.RelationshipsFrom(vlanID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Errorf("vlan edges = %d, want 1", len(rels))
	}
	assertRelationship(t, rels, vlanID, subnetID, store.RelUses)
}

func TestResolveEVSConnectorSecrets_FullARNOnly(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	secretARN := "arn:aws:secretsmanager:us-east-1:123456789012:secret:evs/vcenter-AbCdEf"
	secretID := upsertTestResource(t, st, "aws", acct.ID, TypeSecretsManagerSecret, secretARN, testRegion, "{}")
	connID := upsertTestResource(t, st, "aws", acct.ID, TypeEVSEnvironmentConnector, evsTestConnVcA, testRegion,
		mustJSON(evstypes.Connector{ConnectorId: sdkaws.String("cnctr-vcaaaaaaaa"), SecretArn: sdkaws.String(secretARN)}))
	// A secret named rather than given by ARN cannot be matched: no edge.
	byNameID := upsertTestResource(t, st, "aws", acct.ID, TypeEVSEnvironmentConnector, evsTestConnVcB, testRegion,
		mustJSON(evstypes.Connector{ConnectorId: sdkaws.String("cnctr-vcbbbbbbbb"), SecretArn: sdkaws.String("evs/vcenter")}))

	if err := resolveEVSConnectorSecrets(acct, st); err != nil {
		t.Fatalf("resolveEVSConnectorSecrets: %v", err)
	}
	rels, err := st.RelationshipsFrom(connID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, connID, secretID, store.RelUses)
	assertNoEdgesFrom(t, st, byNameID)
}

// Rows without the reference fields, and no rows at all, emit no edges and
// no error; undecodable attributes are an error.
func TestResolveEVS_NoAttrs(t *testing.T) {
	resolvers := []struct {
		name    string
		typ     string
		resolve func(*account, *store.Store) error
	}{
		{"host", TypeEVSEnvironmentHost, resolveEVSHostRefs},
		{"vlan", TypeEVSEnvironmentVlan, resolveEVSVlanSubnets},
		{"connector", TypeEVSEnvironmentConnector, resolveEVSConnectorSecrets},
	}
	for _, r := range resolvers {
		t.Run(r.name, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			if err := r.resolve(acct, st); err != nil {
				t.Fatalf("no rows: %v", err)
			}
			id := upsertTestResource(t, st, "aws", acct.ID, r.typ, evsTestEnvA+"/x/empty", testRegion, "{}")
			if err := r.resolve(acct, st); err != nil {
				t.Fatalf("empty attrs: %v", err)
			}
			assertNoEdgesFrom(t, st, id)
			upsertTestResource(t, st, "aws", acct.ID, r.typ, evsTestEnvA+"/x/bad", testRegion, "[1]")
			if err := r.resolve(acct, st); err == nil {
				t.Error("undecodable attrs: err = nil, want a decode error")
			}
		})
	}
}
