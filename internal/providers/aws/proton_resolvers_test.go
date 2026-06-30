package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	protontypes "github.com/aws/aws-sdk-go-v2/service/proton/types"
)

func protonEnvTemplateARN(name string) string {
	return fmt.Sprintf("arn:aws:proton:%s:%s:environment-template/%s", testRegion, testAccountID, name)
}

func protonServiceTemplateARN(name string) string {
	return fmt.Sprintf("arn:aws:proton:%s:%s:service-template/%s", testRegion, testAccountID, name)
}

func protonServiceARN(name string) string {
	return fmt.Sprintf("arn:aws:proton:%s:%s:service/%s", testRegion, testAccountID, name)
}

func TestResolveProtonServiceInstanceTargets(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	svcID := upsertTestResourceNamed(t, st, TypeProtonService,
		protonServiceARN("web"), testRegion, "{}", "web")

	instARN := protonServiceARN("web") + "/service-instance/inst-1"
	siID := upsertTestResource(t, st, "aws", acct.ID, TypeProtonServiceInstance,
		instARN, testRegion, mustJSON(protontypes.ServiceInstanceSummary{
			Arn: &instARN, Name: sdkaws.String("inst-1"), ServiceName: sdkaws.String("web"),
		}))

	if err := resolveProtonServiceInstanceTargets(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels, err := st.RelationshipsFrom(siID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, siID, svcID, store.RelAttachedTo)
}

func TestResolveProtonServiceInstanceTargets_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	instARN := protonServiceARN("web") + "/service-instance/inst-1"
	siID := upsertTestResource(t, st, "aws", acct.ID, TypeProtonServiceInstance, instARN, testRegion, "{}")
	if err := resolveProtonServiceInstanceTargets(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels, _ := st.RelationshipsFrom(siID)
	if len(rels) != 0 {
		t.Errorf("expected no edges, got %d", len(rels))
	}
}

func TestResolveProtonEnvTemplateVersionTargets(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	tmplID := upsertTestResourceNamed(t, st, TypeProtonEnvironmentTemplate,
		protonEnvTemplateARN("env-tmpl"), testRegion, "{}", "env-tmpl")

	verARN := protonEnvTemplateARN("env-tmpl") + "/version/1.0"
	verID := upsertTestResource(t, st, "aws", acct.ID, TypeProtonEnvironmentTemplateVersion,
		verARN, testRegion, mustJSON(protontypes.EnvironmentTemplateVersionSummary{
			Arn: &verARN, TemplateName: sdkaws.String("env-tmpl"), MajorVersion: sdkaws.String("1"), MinorVersion: sdkaws.String("0"),
		}))

	if err := resolveProtonEnvTemplateVersionTargets(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels, _ := st.RelationshipsFrom(verID)
	assertRelationship(t, rels, verID, tmplID, store.RelAttachedTo)
}

func TestResolveProtonEnvTemplateVersionTargets_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	verARN := protonEnvTemplateARN("env-tmpl") + "/version/1.0"
	verID := upsertTestResource(t, st, "aws", acct.ID, TypeProtonEnvironmentTemplateVersion, verARN, testRegion, "{}")
	if err := resolveProtonEnvTemplateVersionTargets(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels, _ := st.RelationshipsFrom(verID)
	if len(rels) != 0 {
		t.Errorf("expected no edges, got %d", len(rels))
	}
}

func TestResolveProtonServiceTemplateVersionTargets(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	tmplID := upsertTestResourceNamed(t, st, TypeProtonServiceTemplate,
		protonServiceTemplateARN("svc-tmpl"), testRegion, "{}", "svc-tmpl")

	verARN := protonServiceTemplateARN("svc-tmpl") + "/version/2.1"
	verID := upsertTestResource(t, st, "aws", acct.ID, TypeProtonServiceTemplateVersion,
		verARN, testRegion, mustJSON(protontypes.ServiceTemplateVersionSummary{
			Arn: &verARN, TemplateName: sdkaws.String("svc-tmpl"), MajorVersion: sdkaws.String("2"), MinorVersion: sdkaws.String("1"),
		}))

	if err := resolveProtonServiceTemplateVersionTargets(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels, _ := st.RelationshipsFrom(verID)
	assertRelationship(t, rels, verID, tmplID, store.RelAttachedTo)
}

func TestResolveProtonServiceTemplateVersionTargets_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	verARN := protonServiceTemplateARN("svc-tmpl") + "/version/2.1"
	verID := upsertTestResource(t, st, "aws", acct.ID, TypeProtonServiceTemplateVersion, verARN, testRegion, "{}")
	if err := resolveProtonServiceTemplateVersionTargets(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels, _ := st.RelationshipsFrom(verID)
	if len(rels) != 0 {
		t.Errorf("expected no edges, got %d", len(rels))
	}
}

func TestResolveProtonComponentTargets(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	instARN := protonServiceARN("web") + "/service-instance/inst-1"
	siID := upsertTestResourceNamed(t, st, TypeProtonServiceInstance, instARN, testRegion,
		mustJSON(protontypes.ServiceInstanceSummary{
			Arn: &instARN, Name: sdkaws.String("inst-1"), ServiceName: sdkaws.String("web"),
		}), "inst-1")

	compARN := fmt.Sprintf("arn:aws:proton:%s:%s:component/my-comp", testRegion, testAccountID)
	compID := upsertTestResource(t, st, "aws", acct.ID, TypeProtonComponent, compARN, testRegion,
		mustJSON(protontypes.ComponentSummary{
			Arn: &compARN, Name: sdkaws.String("my-comp"), ServiceName: sdkaws.String("web"), ServiceInstanceName: sdkaws.String("inst-1"),
		}))

	if err := resolveProtonComponentTargets(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels, _ := st.RelationshipsFrom(compID)
	assertRelationship(t, rels, compID, siID, store.RelAttachedTo)
}

func TestResolveProtonComponentTargets_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	compARN := fmt.Sprintf("arn:aws:proton:%s:%s:component/my-comp", testRegion, testAccountID)
	compID := upsertTestResource(t, st, "aws", acct.ID, TypeProtonComponent, compARN, testRegion, "{}")
	if err := resolveProtonComponentTargets(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels, _ := st.RelationshipsFrom(compID)
	if len(rels) != 0 {
		t.Errorf("expected no edges, got %d", len(rels))
	}
}

func TestResolveProtonEnvironmentTargets(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	tmplID := upsertTestResourceNamed(t, st, TypeProtonEnvironmentTemplate,
		protonEnvTemplateARN("env-tmpl"), testRegion, "{}", "env-tmpl")

	envARN := fmt.Sprintf("arn:aws:proton:%s:%s:environment/my-env", testRegion, testAccountID)
	envID := upsertTestResource(t, st, "aws", acct.ID, TypeProtonEnvironment, envARN, testRegion,
		mustJSON(protontypes.EnvironmentSummary{
			Arn: &envARN, Name: sdkaws.String("my-env"), TemplateName: sdkaws.String("env-tmpl"),
		}))

	if err := resolveProtonEnvironmentTargets(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels, _ := st.RelationshipsFrom(envID)
	assertRelationship(t, rels, envID, tmplID, store.RelUses)
}

func TestResolveProtonEnvironmentTargets_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	envARN := fmt.Sprintf("arn:aws:proton:%s:%s:environment/my-env", testRegion, testAccountID)
	envID := upsertTestResource(t, st, "aws", acct.ID, TypeProtonEnvironment, envARN, testRegion, "{}")
	if err := resolveProtonEnvironmentTargets(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels, _ := st.RelationshipsFrom(envID)
	if len(rels) != 0 {
		t.Errorf("expected no edges, got %d", len(rels))
	}
}
