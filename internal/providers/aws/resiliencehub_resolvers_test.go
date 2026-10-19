package aws

import (
	"encoding/json"
	"fmt"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	rhtypes "github.com/aws/aws-sdk-go-v2/service/resiliencehub/types"
	rhv2types "github.com/aws/aws-sdk-go-v2/service/resiliencehubv2/types"
	"github.com/icearp/disco-cli/store"
)

func TestResolveRHAppAssessmentToApp(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	appARN := fmt.Sprintf("arn:aws:resiliencehub:%s:%s:app/app-1", testRegion, acct.ID)
	appID := upsertTestResource(t, st, "aws", acct.ID, TypeResilienceHubApp, appARN, testRegion, "{}")
	asARN := fmt.Sprintf("arn:aws:resiliencehub:%s:%s:app-assessment/as-1", testRegion, acct.ID)
	asID := upsertTestResource(t, st, "aws", acct.ID, TypeResilienceHubAppAssessment, asARN, testRegion,
		fmt.Sprintf(`{"AssessmentArn":"%s","AppArn":"%s"}`, asARN, appARN))
	if err := resolveRHAppAssessmentToApp(acct, st); err != nil {
		t.Fatalf("resolveRHAppAssessmentToApp: %v", err)
	}
	rels, _ := st.RelationshipsFrom(asID)
	assertRelationship(t, rels, asID, appID, store.RelAttachedTo)
}

func TestResolveRHAppAssessmentToApp_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	asARN := fmt.Sprintf("arn:aws:resiliencehub:%s:%s:app-assessment/as-1", testRegion, acct.ID)
	asID := upsertTestResource(t, st, "aws", acct.ID, TypeResilienceHubAppAssessment, asARN, testRegion, "{}")
	if err := resolveRHAppAssessmentToApp(acct, st); err != nil {
		t.Fatalf("resolveRHAppAssessmentToApp (no attrs): %v", err)
	}
	rels, _ := st.RelationshipsFrom(asID)
	if len(rels) != 0 {
		t.Fatalf("expected no relationships, got %d", len(rels))
	}
}

func rhInputSourceAttrs(t *testing.T, src any) string {
	t.Helper()
	b, err := json.Marshal(src)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

// Each import type links to the scanned target its source names; Terraform
// sources and unscanned targets get no edge.
func TestResolveRHInputSourceTargets(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	appARN := rhARN(acct.ID, "app", "a1")
	stackARN := "arn:aws:cloudformation:" + testRegion + ":" + acct.ID + ":stack/s1/guid"
	scarARN := "arn:aws:servicecatalog:" + testRegion + ":" + acct.ID + ":/applications/app1"
	groupARN := "arn:aws:resource-groups:" + testRegion + ":" + acct.ID + ":group/g1"
	clusterARN := "arn:aws:eks:" + testRegion + ":" + acct.ID + ":cluster/c1"
	targets := map[string]string{
		stackARN:   upsertTestResource(t, st, "aws", acct.ID, TypeCloudFormationStack, stackARN, testRegion, "{}"),
		scarARN:    upsertTestResource(t, st, "aws", acct.ID, TypeSCARApplication, scarARN, testRegion, "{}"),
		groupARN:   upsertTestResource(t, st, "aws", acct.ID, TypeResourceGroupsGroup, groupARN, testRegion, "{}"),
		clusterARN: upsertTestResource(t, st, "aws", acct.ID, TypeEKSCluster, clusterARN, testRegion, "{}"),
	}
	sources := map[string]rhtypes.AppInputSource{
		stackARN: {ImportType: rhtypes.ResourceMappingTypeCfnStack, SourceArn: sdkaws.String(stackARN)},
		scarARN:  {ImportType: rhtypes.ResourceMappingTypeAppRegistryApp, SourceArn: sdkaws.String(scarARN)},
		groupARN: {ImportType: rhtypes.ResourceMappingTypeResourceGroup, SourceArn: sdkaws.String(groupARN)},
		clusterARN: {ImportType: rhtypes.ResourceMappingTypeEks, EksSourceClusterNamespace: &rhtypes.EksSourceClusterNamespace{
			EksClusterArn: sdkaws.String(clusterARN), Namespace: sdkaws.String("ns1"),
		}},
	}
	sourceIDs := map[string]string{}
	for target, src := range sources {
		sourceIDs[target] = upsertTestResource(t, st, "aws", acct.ID, TypeResilienceHubAppInputSource,
			appARN+"/input-source/"+target, testRegion, rhInputSourceAttrs(t, src))
	}
	serviceARN := rhARN(acct.ID, "service", "s1")
	v2Sources := map[string]rhv2types.InputSourceSummary{
		stackARN:   {InputSourceId: sdkaws.String("is-stack"), Type: rhv2types.InputSourceTypeCfnStack, CfnStackArn: sdkaws.String(stackARN)},
		clusterARN: {InputSourceId: sdkaws.String("is-eks"), Type: rhv2types.InputSourceTypeEks, Eks: &rhv2types.EksSource{ClusterArn: sdkaws.String(clusterARN), Namespaces: []string{"ns1"}}},
	}
	v2SourceIDs := map[string]string{}
	for target, src := range v2Sources {
		v2SourceIDs[target] = upsertTestResource(t, st, "aws", acct.ID, TypeResilienceHubInputSource,
			serviceARN+"/input-source/"+sdkaws.ToString(src.InputSourceId), testRegion, rhInputSourceAttrs(t, src))
	}
	unscannedStack := "arn:aws:cloudformation:" + testRegion + ":" + acct.ID + ":stack/gone/guid"
	noEdgeIDs := []string{
		upsertTestResource(t, st, "aws", acct.ID, TypeResilienceHubAppInputSource, appARN+"/input-source/"+unscannedStack, testRegion,
			rhInputSourceAttrs(t, rhtypes.AppInputSource{ImportType: rhtypes.ResourceMappingTypeCfnStack, SourceArn: sdkaws.String(unscannedStack)})),
		upsertTestResource(t, st, "aws", acct.ID, TypeResilienceHubAppInputSource, appARN+"/input-source/s3://b/k", testRegion,
			rhInputSourceAttrs(t, rhtypes.AppInputSource{
				ImportType: rhtypes.ResourceMappingTypeTerraform, SourceArn: sdkaws.String(stackARN),
				TerraformSource: &rhtypes.TerraformSource{S3StateFileUrl: sdkaws.String("s3://b/k")},
			})),
		upsertTestResource(t, st, "aws", acct.ID, TypeResilienceHubInputSource, serviceARN+"/input-source/is-tf", testRegion,
			rhInputSourceAttrs(t, rhv2types.InputSourceSummary{
				InputSourceId: sdkaws.String("is-tf"), Type: rhv2types.InputSourceTypeTerraform,
				CfnStackArn: sdkaws.String(stackARN), TfStateFileUrl: sdkaws.String("s3://b/k"),
			})),
	}

	if err := resolveRHInputSourceTargets(acct, st); err != nil {
		t.Fatalf("resolveRHInputSourceTargets: %v", err)
	}
	for target, srcID := range sourceIDs {
		rels, err := st.RelationshipsFrom(srcID)
		if err != nil {
			t.Fatalf("RelationshipsFrom: %v", err)
		}
		if len(rels) != 1 {
			t.Errorf("%s: %d edges; want 1", target, len(rels))
		}
		assertRelationship(t, rels, srcID, targets[target], store.RelUses)
	}
	for target, srcID := range v2SourceIDs {
		rels, err := st.RelationshipsFrom(srcID)
		if err != nil {
			t.Fatalf("RelationshipsFrom: %v", err)
		}
		if len(rels) != 1 {
			t.Errorf("v2 %s: %d edges; want 1", target, len(rels))
		}
		assertRelationship(t, rels, srcID, targets[target], store.RelUses)
	}
	for _, id := range noEdgeIDs {
		if rels, _ := st.RelationshipsFrom(id); len(rels) != 0 {
			t.Errorf("%s: edges = %+v; want none (unscanned target or Terraform source)", id, rels)
		}
	}
}

func TestResolveRHInputSourceTargets_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	ids := []string{
		upsertTestResource(t, st, "aws", acct.ID, TypeResilienceHubAppInputSource, rhARN(acct.ID, "app", "a1")+"/input-source/x", testRegion, "{}"),
		upsertTestResource(t, st, "aws", acct.ID, TypeResilienceHubInputSource, rhARN(acct.ID, "service", "s1")+"/input-source/x", testRegion, "{}"),
	}
	if err := resolveRHInputSourceTargets(acct, st); err != nil {
		t.Fatalf("resolveRHInputSourceTargets (no attrs): %v", err)
	}
	for _, id := range ids {
		if rels, _ := st.RelationshipsFrom(id); len(rels) != 0 {
			t.Errorf("%s: expected no relationships, got %d", id, len(rels))
		}
	}
}

// A row of either type that does not decode is returned as an error and
// costs the other rows none of their edges.
func TestResolveRHInputSourceTargets_DecodeErrorKeepsOtherEdges(t *testing.T) {
	for _, tc := range []struct{ name, typ, attrs string }{
		{name: "app input source", typ: TypeResilienceHubAppInputSource, attrs: `{"ImportType":7}`},
		{name: "service input source", typ: TypeResilienceHubInputSource, attrs: `{"Type":7}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			appARN := rhARN(acct.ID, "app", "a1")
			stackARN := "arn:aws:cloudformation:" + testRegion + ":" + acct.ID + ":stack/s1/guid"
			stackID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFormationStack, stackARN, testRegion, "{}")
			// ListResources orders by name, so the bad row is read first.
			upsertTestResourceNamed(t, st, tc.typ, rhARN(acct.ID, "service", "s1")+"/input-source/bad", testRegion, tc.attrs, "a-bad")
			goodID := upsertTestResourceNamed(t, st, TypeResilienceHubAppInputSource, appARN+"/input-source/"+stackARN, testRegion,
				rhInputSourceAttrs(t, rhtypes.AppInputSource{ImportType: rhtypes.ResourceMappingTypeCfnStack, SourceArn: sdkaws.String(stackARN)}), "b-good")

			if err := resolveRHInputSourceTargets(acct, st); err == nil {
				t.Error("err = nil; want the decode error returned")
			}
			rels, _ := st.RelationshipsFrom(goodID)
			assertRelationship(t, rels, goodID, stackID, store.RelUses)
		})
	}
}
