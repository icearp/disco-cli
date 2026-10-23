package aws

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	ibtypes "github.com/aws/aws-sdk-go-v2/service/imagebuilder/types"
	"github.com/icearp/disco-cli/store"
)

func ibRelsFrom(t *testing.T, st *store.Store, id string) []store.Relationship {
	t.Helper()
	rels, err := st.RelationshipsFrom(id)
	if err != nil {
		t.Fatalf("RelationshipsFrom(%s): %v", id, err)
	}
	return rels
}

func TestResolveImageBuilderPipelineRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	irARN := fmt.Sprintf("arn:aws:imagebuilder:%s:%s:image-recipe/myrec/1.0.0", testRegion, acct.ID)
	irID := upsertTestResource(t, st, "aws", acct.ID, TypeImageBuilderImageRecipe, irARN, testRegion, "{}")
	crARN := fmt.Sprintf("arn:aws:imagebuilder:%s:%s:container-recipe/myctr/1.0.0", testRegion, acct.ID)
	crID := upsertTestResource(t, st, "aws", acct.ID, TypeImageBuilderContainerRecipe, crARN, testRegion, "{}")
	dcARN := fmt.Sprintf("arn:aws:imagebuilder:%s:%s:distribution-configuration/dist1", testRegion, acct.ID)
	dcID := upsertTestResource(t, st, "aws", acct.ID, TypeImageBuilderDistributionConfiguration, dcARN, testRegion, "{}")
	icARN := fmt.Sprintf("arn:aws:imagebuilder:%s:%s:infrastructure-configuration/inf1", testRegion, acct.ID)
	icID := upsertTestResource(t, st, "aws", acct.ID, TypeImageBuilderInfrastructureConfig, icARN, testRegion, "{}")
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/Exec", acct.ID)
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, testRegion, "{}")
	pARN := fmt.Sprintf("arn:aws:imagebuilder:%s:%s:image-pipeline/p1", testRegion, acct.ID)
	attrs := mustJSON(ibtypes.ImagePipeline{
		Arn: &pARN, ImageRecipeArn: &irARN, ContainerRecipeArn: &crARN, DistributionConfigurationArn: &dcARN,
		InfrastructureConfigurationArn: &icARN, ExecutionRole: &roleARN,
	})
	pID := upsertTestResource(t, st, "aws", acct.ID, TypeImageBuilderImagePipeline, pARN, testRegion, attrs)
	if err := resolveImageBuilderPipelineRefs(acct, st); err != nil {
		t.Fatalf("resolveImageBuilderPipelineRefs: %v", err)
	}
	rels := ibRelsFrom(t, st, pID)
	assertRelationship(t, rels, pID, irID, store.RelUses)
	assertRelationship(t, rels, pID, crID, store.RelUses)
	assertRelationship(t, rels, pID, dcID, store.RelUses)
	assertRelationship(t, rels, pID, icID, store.RelUses)
	assertRelationship(t, rels, pID, rID, store.RelAssumes)
}

func TestResolveImageBuilderInfraInstanceProfile(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	ipARN := fmt.Sprintf("arn:aws:iam::%s:instance-profile/MyProf", acct.ID)
	ipID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMInstanceProfile, ipARN, testRegion, "{}")
	icARN := fmt.Sprintf("arn:aws:imagebuilder:%s:%s:infrastructure-configuration/inf1", testRegion, acct.ID)
	attrs := mustJSON(ibtypes.InfrastructureConfigurationSummary{Arn: &icARN, InstanceProfileName: ptrStr("MyProf")})
	icID := upsertTestResource(t, st, "aws", acct.ID, TypeImageBuilderInfrastructureConfig, icARN, testRegion, attrs)
	if err := resolveImageBuilderInfraInstanceProfile(acct, st); err != nil {
		t.Fatalf("resolveImageBuilderInfraInstanceProfile: %v", err)
	}
	assertRelationship(t, ibRelsFrom(t, st, icID), icID, ipID, store.RelAssumes)
}

func TestResolveImageBuilderLifecycleRole(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/Lifecycle", acct.ID)
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, testRegion, "{}")
	lpARN := fmt.Sprintf("arn:aws:imagebuilder:%s:%s:lifecycle-policy/lp1", testRegion, acct.ID)
	attrs := mustJSON(ibtypes.LifecyclePolicySummary{Arn: &lpARN, ExecutionRole: &roleARN})
	lpID := upsertTestResource(t, st, "aws", acct.ID, TypeImageBuilderLifecyclePolicy, lpARN, testRegion, attrs)
	if err := resolveImageBuilderLifecycleRole(acct, st); err != nil {
		t.Fatalf("resolveImageBuilderLifecycleRole: %v", err)
	}
	assertRelationship(t, ibRelsFrom(t, st, lpID), lpID, rID, store.RelAssumes)
}

// TestImageBuilderResolvers_DecodeErrorKeepsOtherRows pins, per resolver,
// that a row whose attributes do not decode is reported as a
// *json.UnmarshalTypeError and costs the other rows none of their edges, and
// that a row with no references gets none.
func TestImageBuilderResolvers_DecodeErrorKeepsOtherRows(t *testing.T) {
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/R", testAccountID)
	for _, c := range []struct {
		name, srcType, srcKind, field string
		resolve                       func(*account, *store.Store) error
		target, targetType, goodAttrs string
	}{
		{
			"pipeline", TypeImageBuilderImagePipeline, "image-pipeline", "ExecutionRole", resolveImageBuilderPipelineRefs,
			roleARN, TypeIAMRole, mustJSON(ibtypes.ImagePipeline{ExecutionRole: &roleARN}),
		},
		{
			"infrastructure", TypeImageBuilderInfrastructureConfig, "infrastructure-configuration", "InstanceProfileName", resolveImageBuilderInfraInstanceProfile,
			fmt.Sprintf("arn:aws:iam::%s:instance-profile/P", testAccountID), TypeIAMInstanceProfile,
			mustJSON(ibtypes.InfrastructureConfigurationSummary{InstanceProfileName: ptrStr("P")}),
		},
		{
			"lifecycle", TypeImageBuilderLifecyclePolicy, "lifecycle-policy", "ExecutionRole", resolveImageBuilderLifecycleRole,
			roleARN, TypeIAMRole, mustJSON(ibtypes.LifecyclePolicySummary{ExecutionRole: &roleARN}),
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			tgtID := upsertTestResource(t, st, "aws", acct.ID, c.targetType, c.target, testRegion, "{}")
			// Named so ListResources (ordered by name) reads a good row on each
			// side of the bad one: an early return on the bad row loses the second.
			row := func(name, attrs string) string {
				return upsertTestResourceNamed(t, st, c.srcType, ibTestARN(c.srcKind, name), testRegion, attrs, name)
			}
			goodBefore := row("a-good", c.goodAttrs)
			bad := row("b-bad", `{"`+c.field+`":7}`)
			goodAfter := row("c-good", c.goodAttrs)
			empty := row("d-empty", "{}")
			err := c.resolve(acct, st)
			var typeErr *json.UnmarshalTypeError
			if !errors.As(err, &typeErr) {
				t.Fatalf("err = %v, want a *json.UnmarshalTypeError", err)
			}
			for _, good := range []string{goodBefore, goodAfter} {
				assertRelationship(t, ibRelsFrom(t, st, good), good, tgtID, store.RelAssumes)
			}
			for _, id := range []string{bad, empty} {
				if rels := ibRelsFrom(t, st, id); len(rels) != 0 {
					t.Errorf("row %s has edges %v, want none", id, rels)
				}
			}
		})
	}
}
