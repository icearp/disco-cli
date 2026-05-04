package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveSSMRelationships_SecureStringToKMS(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	keyID := "abcd-1234"
	keyARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/%s", testRegion, acct.ID, keyID)
	paramARN := fmt.Sprintf("arn:aws:ssm:%s:%s:parameter/app/db/password", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"Type":"SecureString","KeyId":%q}`, keyID)

	pID := upsertTestResource(t, st, "aws", acct.ID, TypeSSMParameter, paramARN, testRegion, attrs)
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")

	if err := resolveSSMRelationships(acct, st); err != nil {
		t.Fatalf("resolveSSMRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(pID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, pID, kID, store.RelUses)
}

func TestResolveSSMRelationships_SkipsAWSManagedAlias(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	paramARN := fmt.Sprintf("arn:aws:ssm:%s:%s:parameter/default", testRegion, acct.ID)
	attrs := `{"Type":"SecureString","KeyId":"alias/aws/ssm"}`
	pID := upsertTestResource(t, st, "aws", acct.ID, TypeSSMParameter, paramARN, testRegion, attrs)

	if err := resolveSSMRelationships(acct, st); err != nil {
		t.Fatalf("resolveSSMRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(pID)
	if len(rels) != 0 {
		t.Errorf("expected 0 rels, got %d", len(rels))
	}
}

func TestResolveSSMRelationships_String_NoEdge(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	paramARN := fmt.Sprintf("arn:aws:ssm:%s:%s:parameter/plain", testRegion, acct.ID)
	attrs := `{"Type":"String"}`
	pID := upsertTestResource(t, st, "aws", acct.ID, TypeSSMParameter, paramARN, testRegion, attrs)

	if err := resolveSSMRelationships(acct, st); err != nil {
		t.Fatalf("resolveSSMRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(pID)
	if len(rels) != 0 {
		t.Errorf("unexpected rels: %+v", rels)
	}
}

func TestResolveSSMAssociationDocument(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	docName := "AWS-RunShellScript"
	docARN := fmt.Sprintf("arn:aws:ssm:%s:%s:document/%s", testRegion, acct.ID, docName)
	regionVal := testRegion
	docNameVal := docName
	if _, err := st.UpsertResource(&store.Resource{
		Provider: "aws", AccountID: acct.ID, Type: TypeSSMDocument,
		NativeID: docARN, Name: &docNameVal, Region: &regionVal,
		AttributesJSON: "{}", DiscoveredBy: testScanID,
	}); err != nil {
		t.Fatalf("upsert document: %v", err)
	}
	docID := store.ResourceID("aws", acct.ID, TypeSSMDocument, docARN)
	asARN := fmt.Sprintf("arn:aws:ssm:%s:%s:association/asoc-1", testRegion, acct.ID)
	asAttrs := fmt.Sprintf(`{"Name":%q}`, docName)
	asID := upsertTestResource(t, st, "aws", acct.ID, TypeSSMAssociation, asARN, testRegion, asAttrs)
	if err := resolveSSMAssociationDocument(acct, st); err != nil {
		t.Fatalf("resolveSSMAssociationDocument: %v", err)
	}
	rels, _ := st.RelationshipsFrom(asID)
	assertRelationship(t, rels, asID, docID, store.RelUses)
}

func TestResolveSSMDocumentRequires(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	regionVal := testRegion
	parentName, parentNameVal := "ParentDoc", "ParentDoc"
	parentARN := fmt.Sprintf("arn:aws:ssm:%s:%s:document/%s", testRegion, acct.ID, parentName)
	childName, childNameVal := "ChildDoc", "ChildDoc"
	childARN := fmt.Sprintf("arn:aws:ssm:%s:%s:document/%s", testRegion, acct.ID, childName)
	if _, err := st.UpsertResource(&store.Resource{
		Provider: "aws", AccountID: acct.ID, Type: TypeSSMDocument,
		NativeID: childARN, Name: &childNameVal, Region: &regionVal,
		AttributesJSON: "{}", DiscoveredBy: testScanID,
	}); err != nil {
		t.Fatalf("upsert child: %v", err)
	}
	parentAttrs := fmt.Sprintf(`{"Requires":[{"Name":%q}]}`, childName)
	if _, err := st.UpsertResource(&store.Resource{
		Provider: "aws", AccountID: acct.ID, Type: TypeSSMDocument,
		NativeID: parentARN, Name: &parentNameVal, Region: &regionVal,
		AttributesJSON: parentAttrs, DiscoveredBy: testScanID,
	}); err != nil {
		t.Fatalf("upsert parent: %v", err)
	}
	parentID := store.ResourceID("aws", acct.ID, TypeSSMDocument, parentARN)
	childID := store.ResourceID("aws", acct.ID, TypeSSMDocument, childARN)
	if err := resolveSSMDocumentRequires(acct, st); err != nil {
		t.Fatalf("resolveSSMDocumentRequires: %v", err)
	}
	rels, _ := st.RelationshipsFrom(parentID)
	assertRelationship(t, rels, parentID, childID, store.RelUses)
}

func TestResolveSSMMaintenanceWindowTargetParent(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	wid := "mw-aaa"
	mwARN := fmt.Sprintf("arn:aws:ssm:%s:%s:maintenancewindow/%s", testRegion, acct.ID, wid)
	mwID := upsertTestResource(t, st, "aws", acct.ID, TypeSSMMaintenanceWindow, mwARN, testRegion, "{}")
	mwtARN := fmt.Sprintf("arn:aws:ssm:%s:%s:windowtarget/%s/wt-1", testRegion, acct.ID, wid)
	mwtAttrs := fmt.Sprintf(`{"WindowId":%q}`, wid)
	mwtID := upsertTestResource(t, st, "aws", acct.ID, TypeSSMMaintenanceWindowTarget, mwtARN, testRegion, mwtAttrs)
	if err := resolveSSMMaintenanceWindowTargetParent(acct, st); err != nil {
		t.Fatalf("resolveSSMMaintenanceWindowTargetParent: %v", err)
	}
	rels, _ := st.RelationshipsFrom(mwtID)
	assertRelationship(t, rels, mwtID, mwID, store.RelAttachedTo)
}

func TestResolveSSMMaintenanceWindowTaskRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	wid := "mw-aaa"
	mwARN := fmt.Sprintf("arn:aws:ssm:%s:%s:maintenancewindow/%s", testRegion, acct.ID, wid)
	mwID := upsertTestResource(t, st, "aws", acct.ID, TypeSSMMaintenanceWindow, mwARN, testRegion, "{}")
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/mw-svc", acct.ID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	lambdaARN := fmt.Sprintf("arn:aws:lambda:%s:%s:function:my-fn", testRegion, acct.ID)
	lambdaID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, lambdaARN, testRegion, "{}")
	mwtARN := fmt.Sprintf("arn:aws:ssm:%s:%s:windowtask/%s/wt-1", testRegion, acct.ID, wid)
	mwtAttrs := fmt.Sprintf(`{"WindowId":%q,"ServiceRoleArn":%q,"TaskArn":%q,"Type":"LAMBDA"}`, wid, roleARN, lambdaARN)
	mwtID := upsertTestResource(t, st, "aws", acct.ID, TypeSSMMaintenanceWindowTask, mwtARN, testRegion, mwtAttrs)
	if err := resolveSSMMaintenanceWindowTaskRefs(acct, st); err != nil {
		t.Fatalf("resolveSSMMaintenanceWindowTaskRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(mwtID)
	assertRelationship(t, rels, mwtID, mwID, store.RelAttachedTo)
	assertRelationship(t, rels, mwtID, roleID, store.RelAssumes)
	assertRelationship(t, rels, mwtID, lambdaID, store.RelRoutesTo)
}

func TestResolveSSMResourceDataSyncRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	bName := "ssm-sync-bucket"
	bID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, "arn:aws:s3:::"+bName, testRegion, "{}")
	keyARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/sync-key", testRegion, acct.ID)
	keyID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")
	rdsARN := fmt.Sprintf("arn:aws:ssm:%s:%s:resource-data-sync/sync-1", testRegion, acct.ID)
	rdsAttrs := fmt.Sprintf(`{"S3Destination":{"BucketName":%q,"AWSKMSKeyARN":%q}}`, bName, keyARN)
	rdsID := upsertTestResource(t, st, "aws", acct.ID, TypeSSMResourceDataSync, rdsARN, testRegion, rdsAttrs)
	if err := resolveSSMResourceDataSyncRefs(acct, st); err != nil {
		t.Fatalf("resolveSSMResourceDataSyncRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(rdsID)
	assertRelationship(t, rels, rdsID, bID, store.RelUses)
	assertRelationship(t, rels, rdsID, keyID, store.RelUses)
}
