package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveAppStreamFleetRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	subARN := ec2ARN(testRegion, acct.ID, "subnet", "subnet-1")
	subID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, subARN, testRegion, "{}")
	sgARN := ec2ARN(testRegion, acct.ID, "security-group", "sg-1")
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroup, sgARN, testRegion, "{}")
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/Stream", acct.ID)
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, testRegion, "{}")
	dARN := fmt.Sprintf("arn:aws:appstream:%s:%s:directory-config/corp.example.com", testRegion, acct.ID)
	dID := upsertTestResource(t, st, "aws", acct.ID, TypeAppStreamDirectoryConfig, dARN, testRegion, "{}")
	fARN := fmt.Sprintf("arn:aws:appstream:%s:%s:fleet/F1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"VpcConfig":{"SubnetIds":["subnet-1"],"SecurityGroupIds":["sg-1"]},"IamRoleArn":%q,"DomainJoinInfo":{"DirectoryName":"corp.example.com"}}`, roleARN)
	fID := upsertTestResource(t, st, "aws", acct.ID, TypeAppStreamFleet, fARN, testRegion, attrs)
	if err := resolveAppStreamFleetRefs(acct, st); err != nil {
		t.Fatalf("resolveAppStreamFleetRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(fID)
	assertRelationship(t, rels, fID, subID, store.RelAttachedTo)
	assertRelationship(t, rels, fID, sgID, store.RelUses)
	assertRelationship(t, rels, fID, rID, store.RelAssumes)
	assertRelationship(t, rels, fID, dID, store.RelUses)
}

func TestResolveAppStreamApplicationAppBlock(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	abARN := fmt.Sprintf("arn:aws:appstream:%s:%s:app-block/B1", testRegion, acct.ID)
	abID := upsertTestResource(t, st, "aws", acct.ID, TypeAppStreamAppBlock, abARN, testRegion, "{}")
	appARN := fmt.Sprintf("arn:aws:appstream:%s:%s:application/App1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"AppBlockArn":%q}`, abARN)
	appID := upsertTestResource(t, st, "aws", acct.ID, TypeAppStreamApplication, appARN, testRegion, attrs)
	if err := resolveAppStreamApplicationAppBlock(acct, st); err != nil {
		t.Fatalf("resolveAppStreamApplicationAppBlock: %v", err)
	}
	rels, _ := st.RelationshipsFrom(appID)
	assertRelationship(t, rels, appID, abID, store.RelUses)
}

func TestResolveAppStreamApplicationFleetAssoc(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	fARN := fmt.Sprintf("arn:aws:appstream:%s:%s:fleet/F1", testRegion, acct.ID)
	fID := upsertTestResource(t, st, "aws", acct.ID, TypeAppStreamFleet, fARN, testRegion, "{}")
	appARN := fmt.Sprintf("arn:aws:appstream:%s:%s:application/App1", testRegion, acct.ID)
	appID := upsertTestResource(t, st, "aws", acct.ID, TypeAppStreamApplication, appARN, testRegion, "{}")
	aARN := fmt.Sprintf("arn:aws:appstream:%s:%s:application-fleet-association/F1/%s", testRegion, acct.ID, appARN)
	attrs := fmt.Sprintf(`{"FleetName":"F1","ApplicationArn":%q}`, appARN)
	aID := upsertTestResource(t, st, "aws", acct.ID, TypeAppStreamApplicationFleetAssociation, aARN, testRegion, attrs)
	if err := resolveAppStreamApplicationFleetAssoc(acct, st); err != nil {
		t.Fatalf("resolveAppStreamApplicationFleetAssoc: %v", err)
	}
	rels, _ := st.RelationshipsFrom(aID)
	assertRelationship(t, rels, aID, fID, store.RelAttachedTo)
	assertRelationship(t, rels, aID, appID, store.RelAttachedTo)
}

func TestResolveAppStreamEntitlementStack(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	sARN := fmt.Sprintf("arn:aws:appstream:%s:%s:stack/S1", testRegion, acct.ID)
	sID := upsertTestResource(t, st, "aws", acct.ID, TypeAppStreamStack, sARN, testRegion, "{}")
	eARN := fmt.Sprintf("arn:aws:appstream:%s:%s:entitlement/S1/E1", testRegion, acct.ID)
	attrs := `{"StackName":"S1"}`
	eID := upsertTestResource(t, st, "aws", acct.ID, TypeAppStreamEntitlement, eARN, testRegion, attrs)
	if err := resolveAppStreamEntitlementStack(acct, st); err != nil {
		t.Fatalf("resolveAppStreamEntitlementStack: %v", err)
	}
	rels, _ := st.RelationshipsFrom(eID)
	assertRelationship(t, rels, eID, sID, store.RelAttachedTo)
}

func TestResolveAppStreamStackFleetAssoc(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	sARN := fmt.Sprintf("arn:aws:appstream:%s:%s:stack/S1", testRegion, acct.ID)
	sID := upsertTestResource(t, st, "aws", acct.ID, TypeAppStreamStack, sARN, testRegion, "{}")
	fARN := fmt.Sprintf("arn:aws:appstream:%s:%s:fleet/F1", testRegion, acct.ID)
	fID := upsertTestResource(t, st, "aws", acct.ID, TypeAppStreamFleet, fARN, testRegion, "{}")
	aARN := fmt.Sprintf("arn:aws:appstream:%s:%s:stack-fleet-association/S1/F1", testRegion, acct.ID)
	attrs := `{"StackName":"S1","FleetName":"F1"}`
	aID := upsertTestResource(t, st, "aws", acct.ID, TypeAppStreamStackFleetAssociation, aARN, testRegion, attrs)
	if err := resolveAppStreamStackFleetAssoc(acct, st); err != nil {
		t.Fatalf("resolveAppStreamStackFleetAssoc: %v", err)
	}
	rels, _ := st.RelationshipsFrom(aID)
	assertRelationship(t, rels, aID, sID, store.RelAttachedTo)
	assertRelationship(t, rels, aID, fID, store.RelAttachedTo)
}

func TestResolveAppStreamApplicationEntitlementAssoc(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	sARN := fmt.Sprintf("arn:aws:appstream:%s:%s:stack/S1", testRegion, acct.ID)
	sID := upsertTestResource(t, st, "aws", acct.ID, TypeAppStreamStack, sARN, testRegion, "{}")
	eARN := fmt.Sprintf("arn:aws:appstream:%s:%s:entitlement/S1/E1", testRegion, acct.ID)
	eID := upsertTestResource(t, st, "aws", acct.ID, TypeAppStreamEntitlement, eARN, testRegion, "{}")
	aARN := fmt.Sprintf("arn:aws:appstream:%s:%s:application-entitlement-association/S1/E1/App1", testRegion, acct.ID)
	aID := upsertTestResource(t, st, "aws", acct.ID, TypeAppStreamApplicationEntitlementAssociation, aARN, testRegion, "{}")
	if err := resolveAppStreamApplicationEntitlementAssoc(acct, st); err != nil {
		t.Fatalf("resolveAppStreamApplicationEntitlementAssoc: %v", err)
	}
	rels, _ := st.RelationshipsFrom(aID)
	assertRelationship(t, rels, aID, sID, store.RelAttachedTo)
	assertRelationship(t, rels, aID, eID, store.RelAttachedTo)
}

func TestResolveAppStreamAppBlockS3(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	abARN := "arn:aws:appstream:us-east-1:" + testAccountID + ":app-block/myBlock"
	bARN := "arn:aws:s3:::appstream-source"
	attrs := `{"SourceS3Location":{"S3Bucket":"appstream-source"}}`

	abID := upsertTestResource(t, st, "aws", acct.ID, TypeAppStreamAppBlock, abARN, testRegion, attrs)
	bID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, bARN, testRegion, "{}")

	if err := resolveAppStreamAppBlockS3(acct, st); err != nil {
		t.Fatalf("resolveAppStreamAppBlockS3: %v", err)
	}
	rels, _ := st.RelationshipsFrom(abID)
	assertRelationship(t, rels, abID, bID, store.RelUses)
}

func TestResolveAppStreamDirectoryConfigCA(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	dcARN := fmt.Sprintf("arn:aws:appstream:%s:%s:directory-config/corp.example.com", testRegion, acct.ID)
	caARN := fmt.Sprintf("arn:aws:acm-pca:%s:%s:certificate-authority/abc-123", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"CertificateBasedAuthProperties":{"CertificateAuthorityArn":%q,"Status":"ENABLED"}}`, caARN)

	dcID := upsertTestResource(t, st, "aws", acct.ID, TypeAppStreamDirectoryConfig, dcARN, testRegion, attrs)
	caID := upsertTestResource(t, st, "aws", acct.ID, TypeACMPrivateCA, caARN, testRegion, "{}")

	if err := resolveAppStreamDirectoryConfigCA(acct, st); err != nil {
		t.Fatalf("resolveAppStreamDirectoryConfigCA: %v", err)
	}
	rels, _ := st.RelationshipsFrom(dcID)
	assertRelationship(t, rels, dcID, caID, store.RelUses)
}

func TestResolveAppStreamStackAccessEndpoints(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	stackARN := fmt.Sprintf("arn:aws:appstream:%s:%s:stack/S1", testRegion, acct.ID)
	vpceARN := ec2ARN(testRegion, acct.ID, "vpc-endpoint", "vpce-abc123")
	attrs := `{"AccessEndpoints":[{"EndpointType":"STREAMING","VpceId":"vpce-abc123"}]}`

	sID := upsertTestResource(t, st, "aws", acct.ID, TypeAppStreamStack, stackARN, testRegion, attrs)
	vID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPCEndpoint, vpceARN, testRegion, "{}")

	if err := resolveAppStreamStackAccessEndpoints(acct, st); err != nil {
		t.Fatalf("resolveAppStreamStackAccessEndpoints: %v", err)
	}
	rels, _ := st.RelationshipsFrom(sID)
	assertRelationship(t, rels, sID, vID, store.RelUses)
}

func TestResolveAppStreamImageRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	imgARN := fmt.Sprintf("arn:aws:appstream:%s:%s:image/my-image", testRegion, acct.ID)
	imgID := upsertTestResource(t, st, "aws", acct.ID, TypeAppStreamImage, imgARN, testRegion, "{}")

	fleetARN := fmt.Sprintf("arn:aws:appstream:%s:%s:fleet/F1", testRegion, acct.ID)
	fleetID := upsertTestResource(t, st, "aws", acct.ID, TypeAppStreamFleet, fleetARN, testRegion,
		fmt.Sprintf(`{"ImageArn":%q}`, imgARN))

	ibARN := fmt.Sprintf("arn:aws:appstream:%s:%s:image-builder/IB1", testRegion, acct.ID)
	ibID := upsertTestResource(t, st, "aws", acct.ID, TypeAppStreamImageBuilder, ibARN, testRegion,
		fmt.Sprintf(`{"ImageArn":%q}`, imgARN))

	// A fleet on an AWS-managed PUBLIC base image disco did not scan → no edge.
	pubFleetARN := fmt.Sprintf("arn:aws:appstream:%s:%s:fleet/F2", testRegion, acct.ID)
	pubFleetID := upsertTestResource(t, st, "aws", acct.ID, TypeAppStreamFleet, pubFleetARN, testRegion,
		fmt.Sprintf(`{"ImageArn":%q}`, "arn:aws:appstream:"+testRegion+"::image/AWS-Base-Image"))

	if err := resolveAppStreamImageRefs(acct, st); err != nil {
		t.Fatalf("resolveAppStreamImageRefs: %v", err)
	}
	frels, _ := st.RelationshipsFrom(fleetID)
	assertRelationship(t, frels, fleetID, imgID, store.RelUses)
	ibrels, _ := st.RelationshipsFrom(ibID)
	assertRelationship(t, ibrels, ibID, imgID, store.RelUses)

	pubRels, _ := st.RelationshipsFrom(pubFleetID)
	if len(pubRels) != 0 {
		t.Errorf("fleet on unscanned PUBLIC base image emitted %d edges, want 0", len(pubRels))
	}
}
