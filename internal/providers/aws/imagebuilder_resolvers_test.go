package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveImageBuilderPipelineRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	irARN := fmt.Sprintf("arn:aws:imagebuilder:%s:%s:image-recipe/myrec/1.0.0", testRegion, acct.ID)
	irID := upsertTestResource(t, st, "aws", acct.ID, TypeImageBuilderImageRecipe, irARN, testRegion, "{}")
	icARN := fmt.Sprintf("arn:aws:imagebuilder:%s:%s:infrastructure-configuration/inf1", testRegion, acct.ID)
	icID := upsertTestResource(t, st, "aws", acct.ID, TypeImageBuilderInfrastructureConfig, icARN, testRegion, "{}")
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/Exec", acct.ID)
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, testRegion, "{}")
	pARN := fmt.Sprintf("arn:aws:imagebuilder:%s:%s:image-pipeline/p1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"ImageRecipeArn":%q,"InfrastructureConfigurationArn":%q,"ExecutionRole":%q}`, irARN, icARN, roleARN)
	pID := upsertTestResource(t, st, "aws", acct.ID, TypeImageBuilderImagePipeline, pARN, testRegion, attrs)
	if err := resolveImageBuilderPipelineRefs(acct, st); err != nil {
		t.Fatalf("resolveImageBuilderPipelineRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(pID)
	assertRelationship(t, rels, pID, irID, store.RelUses)
	assertRelationship(t, rels, pID, icID, store.RelUses)
	assertRelationship(t, rels, pID, rID, store.RelAssumes)
}

func TestResolveImageBuilderInfraInstanceProfile(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	ipARN := fmt.Sprintf("arn:aws:iam::%s:instance-profile/MyProf", acct.ID)
	ipID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMInstanceProfile, ipARN, testRegion, "{}")
	icARN := fmt.Sprintf("arn:aws:imagebuilder:%s:%s:infrastructure-configuration/inf1", testRegion, acct.ID)
	attrs := `{"InstanceProfileName":"MyProf"}`
	icID := upsertTestResource(t, st, "aws", acct.ID, TypeImageBuilderInfrastructureConfig, icARN, testRegion, attrs)
	if err := resolveImageBuilderInfraInstanceProfile(acct, st); err != nil {
		t.Fatalf("resolveImageBuilderInfraInstanceProfile: %v", err)
	}
	rels, _ := st.RelationshipsFrom(icID)
	assertRelationship(t, rels, icID, ipID, store.RelAssumes)
}

func TestResolveImageBuilderLifecycleRole(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/Lifecycle", acct.ID)
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, testRegion, "{}")
	lpARN := fmt.Sprintf("arn:aws:imagebuilder:%s:%s:lifecycle-policy/lp1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"ExecutionRole":%q}`, roleARN)
	lpID := upsertTestResource(t, st, "aws", acct.ID, TypeImageBuilderLifecyclePolicy, lpARN, testRegion, attrs)
	if err := resolveImageBuilderLifecycleRole(acct, st); err != nil {
		t.Fatalf("resolveImageBuilderLifecycleRole: %v", err)
	}
	rels, _ := st.RelationshipsFrom(lpID)
	assertRelationship(t, rels, lpID, rID, store.RelAssumes)
}
