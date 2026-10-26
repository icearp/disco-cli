package aws

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveCodeBuildProjectRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/cb-svc", acct.ID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	keyARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/abc-123", testRegion, acct.ID)
	keyID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, fmt.Sprintf(`{"KeyId":"abc-123","Arn":%q}`, keyARN))
	vpcID := "vpc-aaa"
	vpcARN := ec2ARN(testRegion, acct.ID, "vpc", vpcID)
	vpcRowID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, vpcARN, testRegion, "{}")
	snID := "subnet-bbb"
	snARN := ec2ARN(testRegion, acct.ID, "subnet", snID)
	snRowID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, snARN, testRegion, "{}")
	sgID := "sg-ccc"
	sgARN := ec2ARN(testRegion, acct.ID, "security-group", sgID)
	sgRowID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroup, sgARN, testRegion, "{}")
	bucketARN := "arn:aws:s3:::artifacts-bucket"
	bucketID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, bucketARN, testRegion, "{}")
	lgARN := logGroupNativeIDFromName(acct.ID, testRegion, "/codebuild/proj")
	lgID := upsertTestResource(t, st, "aws", acct.ID, TypeLogsLogGroup, lgARN, testRegion, "{}")

	projARN := fmt.Sprintf("arn:aws:codebuild:%s:%s:project/proj", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{
		"Arn":%q,"Name":"proj",
		"ServiceRole":%q,
		"EncryptionKey":%q,
		"VpcConfig":{"VpcId":%q,"Subnets":[%q],"SecurityGroupIds":[%q]},
		"Artifacts":{"Type":"S3","Location":"artifacts-bucket/path"},
		"LogsConfig":{"CloudWatchLogs":{"Status":"ENABLED","GroupName":"/codebuild/proj"}}
	}`, projARN, roleARN, keyARN, vpcID, snID, sgID)
	projID := upsertTestResource(t, st, "aws", acct.ID, TypeCodeBuildProject, projARN, testRegion, attrs)

	if err := resolveCodeBuildProjectRefs(acct, st); err != nil {
		t.Fatalf("resolveCodeBuildProjectRefs: %v", err)
	}
	rels, err := st.RelationshipsFrom(projID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, projID, roleID, store.RelAssumes)
	assertRelationship(t, rels, projID, keyID, store.RelUses)
	assertRelationship(t, rels, projID, vpcRowID, store.RelAttachedTo)
	assertRelationship(t, rels, projID, snRowID, store.RelAttachedTo)
	assertRelationship(t, rels, projID, sgRowID, store.RelAttachedTo)
	assertRelationship(t, rels, projID, bucketID, store.RelUses)
	assertRelationship(t, rels, projID, lgID, store.RelUses)
}

func TestResolveCodeBuildFleetRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	flARN := "arn:aws:codebuild:us-east-1:" + testAccountID + ":fleet/myfleet"
	roleARN := "arn:aws:iam::" + testAccountID + ":role/cb-fleet"
	vpcARN := ec2ARN(testRegion, acct.ID, "vpc", "vpc-1")
	snARN := ec2ARN(testRegion, acct.ID, "subnet", "subnet-1")
	sgARN := ec2ARN(testRegion, acct.ID, "security-group", "sg-1")
	attrs := `{"FleetServiceRole":"` + roleARN + `","VpcConfig":{"VpcId":"vpc-1","Subnets":["subnet-1"],"SecurityGroupIds":["sg-1"]}}`

	fID := upsertTestResource(t, st, "aws", acct.ID, TypeCodeBuildFleet, flARN, testRegion, attrs)
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, testRegion, "{}")
	vID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, vpcARN, testRegion, "{}")
	snID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, snARN, testRegion, "{}")
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroup, sgARN, testRegion, "{}")

	if err := resolveCodeBuildFleetRefs(acct, st); err != nil {
		t.Fatalf("resolveCodeBuildFleetRefs: %v", err)
	}
	rels, err := st.RelationshipsFrom(fID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, fID, rID, store.RelAssumes)
	assertRelationship(t, rels, fID, vID, store.RelAttachedTo)
	assertRelationship(t, rels, fID, snID, store.RelAttachedTo)
	assertRelationship(t, rels, fID, sgID, store.RelAttachedTo)
}

func TestResolveCodeBuildReportGroupRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	rgARN := "arn:aws:codebuild:us-east-1:" + testAccountID + ":report-group/rg1"
	keyARN := "arn:aws:kms:us-east-1:" + testAccountID + ":key/k-rg"
	bucketARN := "arn:aws:s3:::cb-reports"
	attrs := `{"ExportConfig":{"S3Destination":{"Bucket":"cb-reports","EncryptionKey":"` + keyARN + `"}}}`

	rgID := upsertTestResource(t, st, "aws", acct.ID, TypeCodeBuildReportGroup, rgARN, testRegion, attrs)
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")
	bID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, bucketARN, testRegion, "{}")

	if err := resolveCodeBuildReportGroupRefs(acct, st); err != nil {
		t.Fatalf("resolveCodeBuildReportGroupRefs: %v", err)
	}
	rels, err := st.RelationshipsFrom(rgID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, rgID, kID, store.RelUses)
	assertRelationship(t, rels, rgID, bID, store.RelUses)
}

// A row whose attributes do not decode is returned as an error after the
// walk, and costs no other row its edges; a row with no attributes emits no
// edge.
func TestResolveCodeBuildRefs_DecodeErrorKeepsOtherRows(t *testing.T) {
	roleARN := "arn:aws:iam::" + testAccountID + ":role/r"
	bucketARN := s3BucketARN("aws", "cb-reports")
	for _, tc := range []struct {
		name, typ, kindSeg string
		resolve            func(*account, *store.Store) error
		attrs, target, rel string
	}{
		{"fleet", TypeCodeBuildFleet, "fleet", resolveCodeBuildFleetRefs, `{"FleetServiceRole":"` + roleARN + `"}`, roleARN, store.RelAssumes},
		{"project", TypeCodeBuildProject, "project", resolveCodeBuildProjectRefs, `{"ServiceRole":"` + roleARN + `"}`, roleARN, store.RelAssumes},
		{"report group", TypeCodeBuildReportGroup, "report-group", resolveCodeBuildReportGroupRefs, `{"ExportConfig":{"S3Destination":{"Bucket":"cb-reports"}}}`, bucketARN, store.RelUses},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			arn := func(n string) string {
				return fmt.Sprintf("arn:aws:codebuild:%s:%s:%s/%s", testRegion, testAccountID, tc.kindSeg, n)
			}
			upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
			upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, bucketARN, "", "{}")
			upsertTestResource(t, st, "aws", acct.ID, tc.typ, arn("bad"), testRegion, `{"VpcConfig":"not an object","ExportConfig":"not an object"}`)
			goodID := upsertTestResource(t, st, "aws", acct.ID, tc.typ, arn("good"), testRegion, tc.attrs)
			emptyID := upsertTestResource(t, st, "aws", acct.ID, tc.typ, arn("empty"), testRegion, "{}")

			err := tc.resolve(acct, st)
			var typeErr *json.UnmarshalTypeError
			if !errors.As(err, &typeErr) {
				t.Fatalf("err = %v, want the bad row's decode error", err)
			}
			rels, err := st.RelationshipsFrom(goodID)
			if err != nil {
				t.Fatalf("RelationshipsFrom: %v", err)
			}
			assertRelationship(t, rels, goodID, store.ResourceID("aws", acct.ID, tc.target), tc.rel)
			rels, err = st.RelationshipsFrom(emptyID)
			if err != nil {
				t.Fatalf("RelationshipsFrom: %v", err)
			}
			if len(rels) != 0 {
				t.Errorf("empty row edges = %v, want none", rels)
			}
		})
	}
}

// A project outside the aws partition reaches its log group by the ARN the
// log-group scanner stores, in the project's partition.
func TestResolveCodeBuildProjectRefs_LogGroupInProjectPartition(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-gov-west-1"
	lgARN := "arn:aws-us-gov:logs:" + region + ":" + testAccountID + ":log-group:/codebuild/p"
	lgID := upsertTestResource(t, st, "aws", acct.ID, TypeLogsLogGroup, lgARN, region, "{}")
	projARN := "arn:aws-us-gov:codebuild:" + region + ":" + testAccountID + ":project/p"
	projID := upsertTestResource(t, st, "aws", acct.ID, TypeCodeBuildProject, projARN, region,
		`{"LogsConfig":{"CloudWatchLogs":{"Status":"ENABLED","GroupName":"/codebuild/p"}}}`)

	if err := resolveCodeBuildProjectRefs(acct, st); err != nil {
		t.Fatalf("resolveCodeBuildProjectRefs: %v", err)
	}
	rels, err := st.RelationshipsFrom(projID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, projID, lgID, store.RelUses)
}

// A project row whose NativeID is not an ARN has no partition to rebuild its
// log group in: its error is returned and the other rows keep their edges.
func TestResolveCodeBuildProjectRefs_NativeIDNotARN(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	roleARN := "arn:aws:iam::" + testAccountID + ":role/r"
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	attrs := `{"ServiceRole":"` + roleARN + `"}`
	badID := upsertTestResource(t, st, "aws", acct.ID, TypeCodeBuildProject, "not-an-arn", testRegion, attrs)
	goodID := upsertTestResource(t, st, "aws", acct.ID, TypeCodeBuildProject,
		"arn:aws:codebuild:"+testRegion+":"+testAccountID+":project/good", testRegion, attrs)

	if err := resolveCodeBuildProjectRefs(acct, st); err == nil {
		t.Fatal("err = nil, want the NativeID parse error")
	}
	rels, err := st.RelationshipsFrom(goodID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, goodID, roleID, store.RelAssumes)
	if rels, err = st.RelationshipsFrom(badID); err != nil || len(rels) != 0 {
		t.Errorf("bad row edges = %v (err %v), want none", rels, err)
	}
}
