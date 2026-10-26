package aws

import (
	"context"
	"fmt"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudhsmv2"
	cloudhsmv2types "github.com/aws/aws-sdk-go-v2/service/cloudhsmv2/types"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/icearp/disco-cli/store"
)

// TestResolveKMSAliasToKey verifies an alias with a TargetKeyId resolves to
// an attached-to edge against the key sharing the same region+account.
func TestResolveKMSAliasToKey(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	keyID := "abcd-1234"
	keyARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/%s", testRegion, acct.ID, keyID)
	aliasARN := fmt.Sprintf("arn:aws:kms:%s:%s:alias/prod", testRegion, acct.ID)
	attrsJSON := fmt.Sprintf(`{"TargetKeyId": "%s"}`, keyID)

	keyResID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")
	aliasResID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSAlias, aliasARN, testRegion, attrsJSON)

	if err := resolveKMSAliases(acct, st); err != nil {
		t.Fatalf("resolveKMSAliases: %v", err)
	}
	rels, err := st.RelationshipsFrom(aliasResID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, aliasResID, keyResID, store.RelAttachedTo)

	// Inverse: key → alias (contains).
	fromKey, err := st.RelationshipsFrom(keyResID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, fromKey, keyResID, aliasResID, store.RelContains)
}

// TestResolveKMSAliasToKey_NoAttrs verifies alias with empty attrs is a no-op.
func TestResolveKMSAliasToKey_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	aliasARN := fmt.Sprintf("arn:aws:kms:%s:%s:alias/orphan", testRegion, acct.ID)
	aliasResID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSAlias, aliasARN, testRegion, "{}")

	if err := resolveKMSAliases(acct, st); err != nil {
		t.Fatalf("resolveKMSAliases: %v", err)
	}
	rels, _ := st.RelationshipsFrom(aliasResID)
	if len(rels) != 0 {
		t.Errorf("unexpected rels: %+v", rels)
	}
}

const (
	kmsTestClusterARN = "arn:aws:cloudhsm:us-east-1:123456789012:cluster/cluster-234567abcdefg"
	kmsTestVPCESvcARN = "arn:aws:ec2:us-east-1:123456789012:vpc-endpoint-service/vpce-svc-0123456789abcdef0"
)

func kmsStoreRow(t *testing.T, st *store.Store, ks kmstypes.CustomKeyStoresListEntry) string {
	t.Helper()
	return upsertTestResource(t, st, "aws", testAccountID, TypeKMSCustomKeyStore,
		kmsCustomKeyStoreNativeID(testRegion, testAccountID, sdkaws.ToString(ks.CustomKeyStoreId)), testRegion, mustJSON(ks))
}

func kmsRelsFrom(t *testing.T, st *store.Store, id string) []store.Relationship {
	t.Helper()
	rels, err := st.RelationshipsFrom(id)
	if err != nil {
		t.Fatalf("RelationshipsFrom(%s): %v", id, err)
	}
	return rels
}

func TestResolveKMSCustomKeyStoreBackends_ClusterAndEndpointService(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	clusterID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudHSMCluster, kmsTestClusterARN, testRegion, "{}")
	vpceSvcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPCEndpointService, kmsTestVPCESvcARN, testRegion, "{}")
	hsmID := kmsStoreRow(t, st, kmsHSMStore())
	xksID := kmsStoreRow(t, st, kmsXKSStore())

	foreign := kmsXKSStore()
	foreign.CustomKeyStoreId = sdkaws.String("cks-3333333333333333c")
	foreign.XksProxyConfiguration.VpcEndpointServiceOwner = sdkaws.String("210987654321")
	foreignID := kmsStoreRow(t, st, foreign)
	unscanned := kmsHSMStore()
	unscanned.CustomKeyStoreId = sdkaws.String("cks-4444444444444444d")
	unscanned.CloudHsmClusterId = sdkaws.String("cluster-unscannedabcd")
	unscannedID := kmsStoreRow(t, st, unscanned)

	if err := resolveKMSCustomKeyStoreBackends(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	hsmRels := kmsRelsFrom(t, st, hsmID)
	assertRelationship(t, hsmRels, hsmID, clusterID, store.RelUses)
	xksRels := kmsRelsFrom(t, st, xksID)
	assertRelationship(t, xksRels, xksID, vpceSvcID, store.RelUses)
	if len(hsmRels) != 1 || len(xksRels) != 1 {
		t.Errorf("rels hsm = %+v, xks = %+v; want one edge each", hsmRels, xksRels)
	}
	for _, id := range []string{foreignID, unscannedID} {
		if rels := kmsRelsFrom(t, st, id); len(rels) != 0 {
			t.Errorf("store %s rels = %+v; want none (target cross-account or unscanned)", id, rels)
		}
	}
}

// The resolver's cluster target is the row the CloudHSM scanner stores, not a
// hand-built ARN that could drift from it.
func TestResolveKMSCustomKeyStoreBackends_TargetsScannedCluster(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	hsm := kmsHSMStore()
	client := cloudhsmv2.NewFromConfig(opStubConfig(map[string]func(any) (any, error){
		"DescribeClusters": func(any) (any, error) {
			return &cloudhsmv2.DescribeClustersOutput{Clusters: []cloudhsmv2types.Cluster{{ClusterId: hsm.CloudHsmClusterId}}}, nil
		},
	}, kmsDenied))
	if _, _, err := scanCloudHSMClusters(context.Background(), client, acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("scanCloudHSMClusters: %v", err)
	}
	clusters := rowsByNativeID(t, st, TypeCloudHSMCluster)
	if len(clusters) != 1 {
		t.Fatalf("cluster rows = %d; want 1", len(clusters))
	}
	var clusterID string
	for _, r := range clusters {
		clusterID = r.ID
	}
	storeID := kmsStoreRow(t, st, hsm)

	if err := resolveKMSCustomKeyStoreBackends(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	assertRelationship(t, kmsRelsFrom(t, st, storeID), storeID, clusterID, store.RelUses)
}

func TestResolveKMSCustomKeyStoreBackends_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	upsertTestResource(t, st, "aws", acct.ID, TypeCloudHSMCluster, kmsTestClusterARN, testRegion, "{}")
	id := upsertTestResource(t, st, "aws", acct.ID, TypeKMSCustomKeyStore, kmsCustomKeyStoreNativeID(testRegion, acct.ID, "cks-empty"), testRegion, "{}")
	if err := resolveKMSCustomKeyStoreBackends(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if rels := kmsRelsFrom(t, st, id); len(rels) != 0 {
		t.Errorf("rels = %+v; want none", rels)
	}
}

func TestResolveKMSCustomKeyStoreBackends_DecodeErrorReturned(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	upsertTestResource(t, st, "aws", acct.ID, TypeKMSCustomKeyStore, kmsCustomKeyStoreNativeID(testRegion, acct.ID, "cks-bad"), testRegion, `{"CloudHsmClusterId":7}`)
	if err := resolveKMSCustomKeyStoreBackends(acct, st); err == nil {
		t.Fatal("resolve: nil error; want the decode error")
	}
}

func TestVPCEndpointServiceARNFromName(t *testing.T) {
	for _, tc := range []struct {
		name, owner, want string
		ok                bool
	}{
		{"com.amazonaws.vpce.us-east-1.vpce-svc-0123456789abcdef0", testAccountID, kmsTestVPCESvcARN, true},
		{"eu.amazonaws.vpce.eu-west-1.vpce-svc-0a", "210987654321", "arn:aws:ec2:eu-west-1:210987654321:vpc-endpoint-service/vpce-svc-0a", true},
		{"com.amazonaws.vpce.us-east-1.vpce-svc-0a", "", "", false},
		{"com.amazonaws.us-east-1.s3", testAccountID, "", false},
		{"com.amazonaws.vpce.us-east-1.vpce-0a", testAccountID, "", false},
		{"", testAccountID, "", false},
	} {
		got, ok := vpcEndpointServiceARNFromName("aws", tc.name, tc.owner)
		if got != tc.want || ok != tc.ok {
			t.Errorf("vpcEndpointServiceARNFromName(%q, %q) = %q, %v; want %q, %v", tc.name, tc.owner, got, ok, tc.want, tc.ok)
		}
	}
}

func kmsKeyRow(t *testing.T, st *store.Store, keyID string, storeID *string) string {
	t.Helper()
	md := kmstypes.KeyMetadata{KeyId: sdkaws.String(keyID), CustomKeyStoreId: storeID}
	return upsertTestResource(t, st, "aws", testAccountID, TypeKMSKey,
		"arn:aws:kms:us-east-1:123456789012:key/"+keyID, testRegion, mustJSON(kmsKeyAttrs{Metadata: md}))
}

func TestResolveKMSKeyCustomKeyStores_KeyInStore(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	storeID := kmsStoreRow(t, st, kmsHSMStore())
	inStore := kmsKeyRow(t, st, "key-in-store", sdkaws.String(kmsHSMStoreID))
	plain := kmsKeyRow(t, st, "key-plain", nil)
	dangling := kmsKeyRow(t, st, "key-dangling", sdkaws.String("cks-unscanned"))

	if err := resolveKMSKeyCustomKeyStores(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels := kmsRelsFrom(t, st, inStore)
	assertRelationship(t, rels, inStore, storeID, store.RelAttachedTo)
	if len(rels) != 1 {
		t.Errorf("in-store key rels = %+v; want one", rels)
	}
	for _, id := range []string{plain, dangling} {
		if rels := kmsRelsFrom(t, st, id); len(rels) != 0 {
			t.Errorf("key %s rels = %+v; want none", id, rels)
		}
	}
}

func TestResolveKMSKeyCustomKeyStores_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	kmsStoreRow(t, st, kmsHSMStore())
	id := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, "arn:aws:kms:us-east-1:123456789012:key/k0", testRegion, "{}")
	if err := resolveKMSKeyCustomKeyStores(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if rels := kmsRelsFrom(t, st, id); len(rels) != 0 {
		t.Errorf("rels = %+v; want none", rels)
	}
}

func TestResolveKMSKeyCustomKeyStores_DecodeErrorReturned(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	kmsStoreRow(t, st, kmsHSMStore())
	upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, "arn:aws:kms:us-east-1:123456789012:key/kbad", testRegion, `{"Metadata":{"CustomKeyStoreId":7}}`)
	if err := resolveKMSKeyCustomKeyStores(acct, st); err == nil {
		t.Fatal("resolve: nil error; want the decode error")
	}
}
