package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

// iotThingARN is a tiny helper for test-fixture brevity.
func iotTestARN(kind, name string) string {
	return fmt.Sprintf("arn:aws:iot:%s:%s:%s/%s", testRegion, testAccountID, kind, name)
}

func TestResolveIoTThingRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	thingARN := iotTestARN("thing", "th1")
	thingID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTThing, thingARN, testRegion,
		`{"ThingName":"th1","ThingTypeName":"tt1","BillingGroupName":"bg1"}`)
	ttID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTThingType, iotTestARN("thingtype", "tt1"), testRegion, "{}")
	bgID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTBillingGroup, iotTestARN("billinggroup", "bg1"), testRegion, "{}")

	if err := resolveIoTThingRefs(acct, st); err != nil {
		t.Fatalf("resolveIoTThingRefs: %v", err)
	}
	rels, err := st.RelationshipsFrom(thingID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 2 {
		t.Fatalf("expected 2 relationships, got %d", len(rels))
	}
	assertRelationship(t, rels, thingID, ttID, store.RelAttachedTo)
	assertRelationship(t, rels, thingID, bgID, store.RelAttachedTo)
}

func TestResolveIoTThingRefs_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	thingID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTThing, iotTestARN("thing", "bare"), testRegion, "{}")
	if err := resolveIoTThingRefs(acct, st); err != nil {
		t.Fatalf("resolveIoTThingRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(thingID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveIoTThingRefs_UnscannedTargetsSkipped(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	thingID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTThing, iotTestARN("thing", "th1"), testRegion,
		`{"ThingTypeName":"missing","BillingGroupName":"missing"}`)
	if err := resolveIoTThingRefs(acct, st); err != nil {
		t.Fatalf("resolveIoTThingRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(thingID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveIoTThingGroupParent(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	parentARN := iotTestARN("thinggroup", "parent")
	childARN := iotTestARN("thinggroup", "child")
	parentID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTThingGroup, parentARN, testRegion, "{}")
	childAttrs := fmt.Sprintf(`{"ThingGroupMetadata":{"RootToParentThingGroups":[{"GroupName":"parent","GroupArn":%q}]}}`, parentARN)
	childID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTThingGroup, childARN, testRegion, childAttrs)

	if err := resolveIoTThingGroupParent(acct, st); err != nil {
		t.Fatalf("resolveIoTThingGroupParent: %v", err)
	}
	// RecordHierarchyBatch emits parent → child contains row.
	rels, err := st.RelationshipsFrom(parentID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, parentID, childID, store.RelContains)
}

func TestResolveIoTThingGroupParent_RootGroupNoEdge(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	groupID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTThingGroup, iotTestARN("thinggroup", "root"), testRegion, "{}")
	if err := resolveIoTThingGroupParent(acct, st); err != nil {
		t.Fatalf("resolveIoTThingGroupParent: %v", err)
	}
	rels, _ := st.RelationshipsFrom(groupID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships for root group, got %d", len(rels))
	}
}

func TestResolveIoTAuthorizerLambda(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	fnARN := fmt.Sprintf("arn:aws:lambda:%s:%s:function:auth-fn", testRegion, testAccountID)
	authARN := iotTestARN("authorizer", "myauth")
	attrs := fmt.Sprintf(`{"AuthorizerDescription":{"AuthorizerArn":%q,"AuthorizerName":"myauth","AuthorizerFunctionArn":%q}}`, authARN, fnARN)
	authID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTAuthorizer, authARN, testRegion, attrs)
	fnID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, fnARN, testRegion, "{}")

	if err := resolveIoTAuthorizerLambda(acct, st); err != nil {
		t.Fatalf("resolveIoTAuthorizerLambda: %v", err)
	}
	rels, _ := st.RelationshipsFrom(authID)
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, authID, fnID, store.RelUses)
}

func TestResolveIoTAuthorizerLambda_QualifiedARNStripped(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	fnARN := fmt.Sprintf("arn:aws:lambda:%s:%s:function:auth-fn", testRegion, testAccountID)
	qualified := fnARN + ":PROD"
	authARN := iotTestARN("authorizer", "myauth")
	attrs := fmt.Sprintf(`{"AuthorizerDescription":{"AuthorizerFunctionArn":%q}}`, qualified)
	authID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTAuthorizer, authARN, testRegion, attrs)
	fnID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, fnARN, testRegion, "{}")

	if err := resolveIoTAuthorizerLambda(acct, st); err != nil {
		t.Fatalf("resolveIoTAuthorizerLambda: %v", err)
	}
	rels, _ := st.RelationshipsFrom(authID)
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, authID, fnID, store.RelUses)
}

func TestResolveIoTAuthorizerLambda_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	authID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTAuthorizer, iotTestARN("authorizer", "bare"), testRegion, "{}")
	if err := resolveIoTAuthorizerLambda(acct, st); err != nil {
		t.Fatalf("resolveIoTAuthorizerLambda: %v", err)
	}
	rels, _ := st.RelationshipsFrom(authID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveIoTRoleAliasRole(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/iot-creds", testAccountID)
	aliasARN := iotTestARN("rolealias", "myalias")
	attrs := fmt.Sprintf(`{"RoleAliasDescription":{"RoleAlias":"myalias","RoleAliasArn":%q,"RoleArn":%q}}`, aliasARN, roleARN)
	aliasID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTRoleAlias, aliasARN, testRegion, attrs)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")

	if err := resolveIoTRoleAliasRole(acct, st); err != nil {
		t.Fatalf("resolveIoTRoleAliasRole: %v", err)
	}
	rels, _ := st.RelationshipsFrom(aliasID)
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, aliasID, roleID, store.RelAssumes)
}

func TestResolveIoTRoleAliasRole_UnscannedRole(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	aliasARN := iotTestARN("rolealias", "myalias")
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/missing", testAccountID)
	attrs := fmt.Sprintf(`{"RoleAliasDescription":{"RoleArn":%q}}`, roleARN)
	aliasID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTRoleAlias, aliasARN, testRegion, attrs)
	if err := resolveIoTRoleAliasRole(acct, st); err != nil {
		t.Fatalf("resolveIoTRoleAliasRole: %v", err)
	}
	rels, _ := st.RelationshipsFrom(aliasID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveIoTProvisioningTemplateRole(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/prov-role", testAccountID)
	tmplARN := iotTestARN("provisioningtemplate", "tmpl")
	attrs := fmt.Sprintf(`{"TemplateName":"tmpl","TemplateArn":%q,"ProvisioningRoleArn":%q}`, tmplARN, roleARN)
	tmplID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTProvisioningTemplate, tmplARN, testRegion, attrs)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")

	if err := resolveIoTProvisioningTemplateRole(acct, st); err != nil {
		t.Fatalf("resolveIoTProvisioningTemplateRole: %v", err)
	}
	rels, _ := st.RelationshipsFrom(tmplID)
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, tmplID, roleID, store.RelAssumes)
}

func TestResolveIoTProvisioningTemplateRole_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	tmplID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTProvisioningTemplate, iotTestARN("provisioningtemplate", "bare"), testRegion, "{}")
	if err := resolveIoTProvisioningTemplateRole(acct, st); err != nil {
		t.Fatalf("resolveIoTProvisioningTemplateRole: %v", err)
	}
	rels, _ := st.RelationshipsFrom(tmplID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveIoTDomainConfigAuthorizer(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	authARN := iotTestARN("authorizer", "myauth")
	cfgARN := iotTestARN("domainconfiguration", "mydomain")
	attrs := `{"DomainConfigurationName":"mydomain","AuthorizerConfig":{"DefaultAuthorizerName":"myauth"}}`
	cfgID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTDomainConfiguration, cfgARN, testRegion, attrs)
	authID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTAuthorizer, authARN, testRegion, "{}")

	if err := resolveIoTDomainConfigAuthorizer(acct, st); err != nil {
		t.Fatalf("resolveIoTDomainConfigAuthorizer: %v", err)
	}
	rels, _ := st.RelationshipsFrom(cfgID)
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, cfgID, authID, store.RelUses)
}

func TestResolveIoTDomainConfigAuthorizer_Empty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	cfgID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTDomainConfiguration, iotTestARN("domainconfiguration", "bare"), testRegion, "{}")
	if err := resolveIoTDomainConfigAuthorizer(acct, st); err != nil {
		t.Fatalf("resolveIoTDomainConfigAuthorizer: %v", err)
	}
	rels, _ := st.RelationshipsFrom(cfgID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveIoTPolicyPrincipalAttachmentRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	polARN := iotTestARN("policy", "mypol")
	certARN := iotTestARN("cert", "abc123")
	attARN := fmt.Sprintf("arn:aws:iot:%s:%s:policy/mypol/principal/%s", testRegion, testAccountID, certARN)
	attrs := fmt.Sprintf(`{"PolicyName":"mypol","Principal":%q}`, certARN)
	attID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTPolicyPrincipalAttachment, attARN, testRegion, attrs)
	polID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTPolicy, polARN, testRegion, "{}")
	certID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTCertificate, certARN, testRegion, "{}")

	if err := resolveIoTPolicyPrincipalAttachmentRefs(acct, st); err != nil {
		t.Fatalf("resolveIoTPolicyPrincipalAttachmentRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(attID)
	if len(rels) != 2 {
		t.Fatalf("expected 2 relationships, got %d", len(rels))
	}
	assertRelationship(t, rels, attID, polID, store.RelAttachedTo)
	assertRelationship(t, rels, attID, certID, store.RelAttachedTo)
}

func TestResolveIoTPolicyPrincipalAttachmentRefs_CognitoPrincipalSkipsCert(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	polARN := iotTestARN("policy", "mypol")
	cognito := "us-east-1:abcd-1234"
	attARN := fmt.Sprintf("arn:aws:iot:%s:%s:policy/mypol/principal/%s", testRegion, testAccountID, cognito)
	attrs := fmt.Sprintf(`{"PolicyName":"mypol","Principal":%q}`, cognito)
	attID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTPolicyPrincipalAttachment, attARN, testRegion, attrs)
	polID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTPolicy, polARN, testRegion, "{}")

	if err := resolveIoTPolicyPrincipalAttachmentRefs(acct, st); err != nil {
		t.Fatalf("resolveIoTPolicyPrincipalAttachmentRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(attID)
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship (policy only), got %d", len(rels))
	}
	assertRelationship(t, rels, attID, polID, store.RelAttachedTo)
}

func TestResolveIoTPolicyPrincipalAttachmentRefs_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	attID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTPolicyPrincipalAttachment,
		iotTestARN("policy", "x/principal/y"), testRegion, "{}")
	if err := resolveIoTPolicyPrincipalAttachmentRefs(acct, st); err != nil {
		t.Fatalf("resolveIoTPolicyPrincipalAttachmentRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(attID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveIoTThingPrincipalAttachmentRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	thingARN := iotTestARN("thing", "th1")
	certARN := iotTestARN("cert", "abc123")
	attARN := fmt.Sprintf("arn:aws:iot:%s:%s:thing/th1/principal/%s", testRegion, testAccountID, certARN)
	attrs := fmt.Sprintf(`{"ThingName":"th1","Principal":%q}`, certARN)
	attID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTThingPrincipalAttachment, attARN, testRegion, attrs)
	thingID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTThing, thingARN, testRegion, "{}")
	certID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTCertificate, certARN, testRegion, "{}")

	if err := resolveIoTThingPrincipalAttachmentRefs(acct, st); err != nil {
		t.Fatalf("resolveIoTThingPrincipalAttachmentRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(attID)
	if len(rels) != 2 {
		t.Fatalf("expected 2 relationships, got %d", len(rels))
	}
	assertRelationship(t, rels, attID, thingID, store.RelAttachedTo)
	assertRelationship(t, rels, attID, certID, store.RelAttachedTo)
}

func TestResolveIoTThingPrincipalAttachmentRefs_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	attID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTThingPrincipalAttachment,
		iotTestARN("thing", "x/principal/y"), testRegion, "{}")
	if err := resolveIoTThingPrincipalAttachmentRefs(acct, st); err != nil {
		t.Fatalf("resolveIoTThingPrincipalAttachmentRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(attID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveIoTCertificateCA(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	caID := "ca-abc"
	caARN := "arn:aws:iot:us-east-1:" + testAccountID + ":cacert/" + caID
	certID := "cert-xyz"
	certARN := "arn:aws:iot:us-east-1:" + testAccountID + ":cert/" + certID

	caAttrs := `{"CertificateDescription":{"CertificateId":"` + caID + `"}}`
	cAttrs := `{"CertificateDescription":{"CaCertificateId":"` + caID + `"}}`
	cID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTCertificate, certARN, testRegion, cAttrs)
	caResID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTCACertificate, caARN, testRegion, caAttrs)

	if err := resolveIoTCertificateCA(acct, st); err != nil {
		t.Fatalf("resolveIoTCertificateCA: %v", err)
	}
	rels, _ := st.RelationshipsFrom(cID)
	assertRelationship(t, rels, cID, caResID, store.RelAttachedTo)
}
