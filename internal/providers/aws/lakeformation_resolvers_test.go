package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

const (
	testLFBucket   = "data-lake-bucket"
	testLFRoleName = "LakeFormationServiceRole"
)

func lfRoleARN() string {
	return fmt.Sprintf("arn:aws:iam::%s:role/%s", testAccountID, testLFRoleName)
}

// TestResolveLakeFormationResourceTargets_HappyPath verifies both edges
// land when bucket + role are scanned. Uses a prefixed S3 location to
// exercise the bucket-suffix-strip path.
func TestResolveLakeFormationResourceTargets_HappyPath(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	bucketARN := "arn:aws:s3:::" + testLFBucket
	bID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, bucketARN, "", "{}")
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, lfRoleARN(), "", "{}")

	// Lake Formation registers a sub-prefix of the bucket.
	locationARN := bucketARN + "/raw/"
	attrs := fmt.Sprintf(`{"ResourceArn":%q,"RoleArn":%q}`, locationARN, lfRoleARN())
	resID := upsertTestResource(t, st, "aws", acct.ID, TypeLakeFormationResource, locationARN, testRegion, attrs)

	if err := resolveLakeFormationResourceTargets(acct, st); err != nil {
		t.Fatalf("resolveLakeFormationResourceTargets: %v", err)
	}
	rels, err := st.RelationshipsFrom(resID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, resID, bID, store.RelUses)
	assertRelationship(t, rels, resID, rID, store.RelAssumes)
}

// TestResolveLakeFormationResourceTargets_FKSafe verifies missing targets
// skip without erroring (cross-account bucket/role).
func TestResolveLakeFormationResourceTargets_FKSafe(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	locationARN := "arn:aws:s3:::ghost-bucket/data/"
	attrs := fmt.Sprintf(`{"ResourceArn":%q,"RoleArn":"arn:aws:iam::999999999999:role/ghost"}`, locationARN)
	resID := upsertTestResource(t, st, "aws", acct.ID, TypeLakeFormationResource, locationARN, testRegion, attrs)

	if err := resolveLakeFormationResourceTargets(acct, st); err != nil {
		t.Fatalf("resolveLakeFormationResourceTargets: %v", err)
	}
	rels, err := st.RelationshipsFrom(resID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected zero edges in FK-safe partial-coverage scan, got %d", len(rels))
	}
}

// TestResolveLakeFormationResourceTargets_MalformedAttrs ensures invalid
// attrs JSON skips the row rather than aborting the resolver.
func TestResolveLakeFormationResourceTargets_MalformedAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	upsertTestResource(t, st, "aws", acct.ID, TypeLakeFormationResource,
		"arn:aws:s3:::x", testRegion, `not json`)

	if err := resolveLakeFormationResourceTargets(acct, st); err != nil {
		t.Fatalf("resolveLakeFormationResourceTargets: %v", err)
	}
}

func TestS3BucketARNFromLocation(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"arn:aws:s3:::bucket", "arn:aws:s3:::bucket"},
		{"arn:aws:s3:::bucket/prefix/", "arn:aws:s3:::bucket"},
		{"arn:aws:s3:::bucket/a/b/c", "arn:aws:s3:::bucket"},
		{"arn:aws:lambda:::function/foo", ""},
		{"", ""},
		{"arn:aws:s3:::", ""},
	}
	for _, c := range cases {
		if got := s3BucketARNFromLocation(c.in); got != c.want {
			t.Errorf("s3BucketARNFromLocation(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
