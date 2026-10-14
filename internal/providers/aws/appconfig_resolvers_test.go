package aws

import (
	"fmt"
	"testing"

	actypes "github.com/aws/aws-sdk-go-v2/service/appconfig/types"
	"github.com/icearp/disco-cli/store"
)

func TestAppconfigAppARNFromChild(t *testing.T) {
	cases := []struct{ in, want string }{
		{"arn:aws:appconfig:us-east-1:123:application/a1/environment/e1", "arn:aws:appconfig:us-east-1:123:application/a1"},
		{"arn:aws:appconfig:us-east-1:123:application/a1/configurationprofile/c1/hostedconfigurationversion/1", "arn:aws:appconfig:us-east-1:123:application/a1"},
		{"arn:aws:appconfig:us-east-1:123:deploymentstrategy/ds1", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := appconfigAppARNFromChild(c.in); got != c.want {
			t.Errorf("appconfigAppARNFromChild(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestResolveAppConfigChildren(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	appARN := fmt.Sprintf("arn:aws:appconfig:%s:%s:application/a1", testRegion, acct.ID)
	appID := upsertTestResource(t, st, "aws", acct.ID, TypeAppConfigApplication, appARN, testRegion, "{}")
	envARN := appARN + "/environment/e1"
	envID := upsertTestResource(t, st, "aws", acct.ID, TypeAppConfigEnvironment, envARN, testRegion, "{}")
	cpARN := appARN + "/configurationprofile/c1"
	cpID := upsertTestResource(t, st, "aws", acct.ID, TypeAppConfigConfigurationProfile, cpARN, testRegion, "{}")
	depARN := envARN + "/deployment/1"
	depID := upsertTestResource(t, st, "aws", acct.ID, TypeAppConfigDeployment, depARN, testRegion, "{}")
	hcvARN := cpARN + "/hostedconfigurationversion/1"
	hcvID := upsertTestResource(t, st, "aws", acct.ID, TypeAppConfigHostedConfigurationVersion, hcvARN, testRegion, "{}")

	if err := resolveAppConfigChildren(acct, st); err != nil {
		t.Fatalf("resolveAppConfigChildren: %v", err)
	}
	rels, _ := st.RelationshipsFrom(envID)
	assertRelationship(t, rels, envID, appID, store.RelAttachedTo)
	rels, _ = st.RelationshipsFrom(cpID)
	assertRelationship(t, rels, cpID, appID, store.RelAttachedTo)
	rels, _ = st.RelationshipsFrom(depID)
	assertRelationship(t, rels, depID, appID, store.RelAttachedTo)
	assertRelationship(t, rels, depID, envID, store.RelAttachedTo)
	rels, _ = st.RelationshipsFrom(hcvID)
	assertRelationship(t, rels, hcvID, appID, store.RelAttachedTo)
	assertRelationship(t, rels, hcvID, cpID, store.RelAttachedTo)
}

func TestResolveAppConfigExtensionAssociation(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	extARN := fmt.Sprintf("arn:aws:appconfig:%s:%s:extension/x1/1", testRegion, acct.ID)
	extID := upsertTestResource(t, st, "aws", acct.ID, TypeAppConfigExtension, extARN, testRegion, "{}")
	eaARN := fmt.Sprintf("arn:aws:appconfig:%s:%s:extensionassociation/ea1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"ExtensionArn":%q}`, extARN)
	eaID := upsertTestResource(t, st, "aws", acct.ID, TypeAppConfigExtensionAssociation, eaARN, testRegion, attrs)

	if err := resolveAppConfigExtensionAssociation(acct, st); err != nil {
		t.Fatalf("resolveAppConfigExtensionAssociation: %v", err)
	}
	rels, _ := st.RelationshipsFrom(eaID)
	assertRelationship(t, rels, eaID, extID, store.RelUses)
}

// An experiment definition is attached to its application and uses its
// environment and configuration profile. A profile of the same id under
// another application, and an unscanned environment, get no edge.
func TestResolveAppConfigExperimentDefinitions(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	const appARN = "arn:aws:appconfig:us-east-1:123456789012:application/a1"
	appID := upsertTestResource(t, st, "aws", acct.ID, TypeAppConfigApplication, appARN, testRegion, "{}")
	envID := upsertTestResource(t, st, "aws", acct.ID, TypeAppConfigEnvironment, appARN+"/environment/env1", testRegion, "{}")
	cpID := upsertTestResource(t, st, "aws", acct.ID, TypeAppConfigConfigurationProfile, appARN+"/configurationprofile/cp1", testRegion, "{}")
	otherCPID := upsertTestResource(t, st, "aws", acct.ID, TypeAppConfigConfigurationProfile,
		"arn:aws:appconfig:us-east-1:123456789012:application/a2/configurationprofile/cp1", testRegion, "{}")

	linked := mustJSON(actypes.ExperimentDefinitionSummary{
		ApplicationId: ptrStr("a1"), Id: ptrStr("x1"), EnvironmentId: ptrStr("env1"), ConfigurationProfileId: ptrStr("cp1"),
	})
	x1ID := upsertTestResource(t, st, "aws", acct.ID, TypeAppConfigExperimentDefinition, appARN+"/experimentdefinition/x1", testRegion, linked)
	unscannedEnv := mustJSON(actypes.ExperimentDefinitionSummary{
		ApplicationId: ptrStr("a1"), Id: ptrStr("x2"), EnvironmentId: ptrStr("env-gone"),
	})
	x2ID := upsertTestResource(t, st, "aws", acct.ID, TypeAppConfigExperimentDefinition, appARN+"/experimentdefinition/x2", testRegion, unscannedEnv)

	if err := resolveAppConfigChildren(acct, st); err != nil {
		t.Fatalf("resolveAppConfigChildren: %v", err)
	}
	if err := resolveAppConfigExperimentDefinitions(acct, st); err != nil {
		t.Fatalf("resolveAppConfigExperimentDefinitions: %v", err)
	}
	rels, _ := st.RelationshipsFrom(x1ID)
	assertRelationship(t, rels, x1ID, appID, store.RelAttachedTo)
	assertRelationship(t, rels, x1ID, envID, store.RelUses)
	assertRelationship(t, rels, x1ID, cpID, store.RelUses)
	if len(rels) != 3 {
		t.Errorf("x1 edges = %+v, want exactly app, env, profile", rels)
	}
	for _, r := range rels {
		if r.ToID == otherCPID {
			t.Errorf("x1 linked to a2's profile cp1: %+v", r)
		}
	}
	rels, _ = st.RelationshipsFrom(x2ID)
	if len(rels) != 1 || rels[0].ToID != appID {
		t.Errorf("x2 edges = %+v, want only the application", rels)
	}
}

func TestResolveAppConfigExperimentDefinitions_NoRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	upsertTestResource(t, st, "aws", acct.ID, TypeAppConfigEnvironment,
		"arn:aws:appconfig:us-east-1:123456789012:application/a1/environment/env1", testRegion, "{}")
	xID := upsertTestResource(t, st, "aws", acct.ID, TypeAppConfigExperimentDefinition,
		"arn:aws:appconfig:us-east-1:123456789012:application/a1/experimentdefinition/x1", testRegion, "{}")

	if err := resolveAppConfigExperimentDefinitions(acct, st); err != nil {
		t.Fatalf("resolveAppConfigExperimentDefinitions: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(xID); len(rels) != 0 {
		t.Errorf("edges = %+v, want none", rels)
	}
}
