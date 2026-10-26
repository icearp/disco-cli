package aws

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	lftypes "github.com/aws/aws-sdk-go-v2/service/lakeformation/types"
	"github.com/icearp/disco-cli/store"
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

// lfOptInRow stores an opt-in for principal on resource, as the scanner does.
func lfOptInRow(t *testing.T, st *store.Store, principal string, resource *lftypes.Resource) string {
	t.Helper()
	o := lftypes.LakeFormationOptInsInfo{
		Principal: &lftypes.DataLakePrincipal{DataLakePrincipalIdentifier: sdkaws.String(principal)},
		Resource:  resource,
	}
	return upsertTestResource(t, st, "aws", testAccountID, TypeLakeFormationOptIn,
		mustLFOptInARN(t, o), testRegion, mustJSON(o))
}

func TestResolveLakeFormationOptInTargets_EdgePerTargetKind(t *testing.T) {
	const (
		roleARN = "arn:aws:iam::123456789012:role/analyst"
		userARN = "arn:aws:iam::123456789012:user/bob"
	)
	dbResource := &lftypes.Resource{Database: &lftypes.DatabaseResource{Name: sdkaws.String("sales")}}
	cases := []struct {
		name       string
		principal  string
		resource   *lftypes.Resource
		targetType string
		targetID   string // NativeID the target's scanner stores; "" when no edge may exist
	}{
		{"iam role principal", roleARN, dbResource, TypeIAMRole, roleARN},
		{"iam user principal", userARN, dbResource, TypeIAMUser, userARN},
		{"glue database", "IAM_ALLOWED_PRINCIPALS", dbResource, TypeGlueDatabase, glueDatabaseARN("aws", testRegion, testAccountID, "sales")},
		{
			"glue table", "IAM_ALLOWED_PRINCIPALS",
			&lftypes.Resource{Table: &lftypes.TableResource{DatabaseName: sdkaws.String("sales"), Name: sdkaws.String("orders")}},
			TypeGlueTable, glueTableARN("aws", testRegion, testAccountID, "sales", "orders"),
		},
		{
			"glue table with columns", "IAM_ALLOWED_PRINCIPALS",
			&lftypes.Resource{TableWithColumns: &lftypes.TableWithColumnsResource{DatabaseName: sdkaws.String("sales"), Name: sdkaws.String("orders"), ColumnNames: []string{"id"}}},
			TypeGlueTable, glueTableARN("aws", testRegion, testAccountID, "sales", "orders"),
		},
		{
			"data location", "IAM_ALLOWED_PRINCIPALS",
			&lftypes.Resource{DataLocation: &lftypes.DataLocationResource{ResourceArn: sdkaws.String("arn:aws:s3:::lake/raw")}},
			TypeLakeFormationResource, "arn:aws:s3:::lake/raw",
		},
		{
			"data cells filter", "IAM_ALLOWED_PRINCIPALS",
			&lftypes.Resource{DataCellsFilter: &lftypes.DataCellsFilterResource{DatabaseName: sdkaws.String("sales"), TableName: sdkaws.String("orders"), Name: sdkaws.String("eu-only")}},
			TypeLakeFormationDataCellsFilter, lfDataCellsFilterARN("aws", testRegion, testAccountID, "sales", "orders", "eu-only"),
		},
		{
			"lf tag", "IAM_ALLOWED_PRINCIPALS",
			&lftypes.Resource{LFTag: &lftypes.LFTagKeyResource{TagKey: sdkaws.String("tier"), TagValues: []string{"gold"}}},
			TypeLakeFormationTag, lfTagARN("aws", testRegion, testAccountID, "tier"),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			targetID := upsertTestResource(t, st, "aws", testAccountID, c.targetType, c.targetID, testRegion, "{}")
			optInID := lfOptInRow(t, st, c.principal, c.resource)
			if err := resolveLakeFormationOptInTargets(newTestAccount(testAccountID), st); err != nil {
				t.Fatalf("resolveLakeFormationOptInTargets: %v", err)
			}
			rels, err := st.RelationshipsFrom(optInID)
			if err != nil {
				t.Fatalf("RelationshipsFrom: %v", err)
			}
			if len(rels) != 1 {
				t.Fatalf("edges = %v, want exactly one", rels)
			}
			assertRelationship(t, rels, optInID, targetID, store.RelAttachedTo)
		})
	}
}

// A database, table or data location in another account's catalog must not
// link to this account's same-named resource.
func TestResolveLakeFormationOptInTargets_ForeignCatalogNoEdge(t *testing.T) {
	foreign := sdkaws.String("999999999999")
	cases := []struct {
		name       string
		resource   *lftypes.Resource
		targetType string
		targetID   string
	}{
		{"database", &lftypes.Resource{Database: &lftypes.DatabaseResource{CatalogId: foreign, Name: sdkaws.String("sales")}}, TypeGlueDatabase, glueDatabaseARN("aws", testRegion, testAccountID, "sales")},
		{"table", &lftypes.Resource{Table: &lftypes.TableResource{CatalogId: foreign, DatabaseName: sdkaws.String("sales"), Name: sdkaws.String("orders")}}, TypeGlueTable, glueTableARN("aws", testRegion, testAccountID, "sales", "orders")},
		{"table with columns", &lftypes.Resource{TableWithColumns: &lftypes.TableWithColumnsResource{CatalogId: foreign, DatabaseName: sdkaws.String("sales"), Name: sdkaws.String("orders")}}, TypeGlueTable, glueTableARN("aws", testRegion, testAccountID, "sales", "orders")},
		{"data location", &lftypes.Resource{DataLocation: &lftypes.DataLocationResource{CatalogId: foreign, ResourceArn: sdkaws.String("arn:aws:s3:::lake")}}, TypeLakeFormationResource, "arn:aws:s3:::lake"},
		{"data cells filter", &lftypes.Resource{DataCellsFilter: &lftypes.DataCellsFilterResource{TableCatalogId: foreign, DatabaseName: sdkaws.String("d"), TableName: sdkaws.String("t"), Name: sdkaws.String("f")}}, TypeLakeFormationDataCellsFilter, lfDataCellsFilterARN("aws", testRegion, testAccountID, "d", "t", "f")},
		{"lf tag", &lftypes.Resource{LFTag: &lftypes.LFTagKeyResource{CatalogId: foreign, TagKey: sdkaws.String("tier")}}, TypeLakeFormationTag, lfTagARN("aws", testRegion, testAccountID, "tier")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			upsertTestResource(t, st, "aws", testAccountID, c.targetType, c.targetID, testRegion, "{}")
			optInID := lfOptInRow(t, st, "IAM_ALLOWED_PRINCIPALS", c.resource)
			if err := resolveLakeFormationOptInTargets(newTestAccount(testAccountID), st); err != nil {
				t.Fatalf("resolveLakeFormationOptInTargets: %v", err)
			}
			rels, err := st.RelationshipsFrom(optInID)
			if err != nil {
				t.Fatalf("RelationshipsFrom: %v", err)
			}
			if len(rels) != 0 {
				t.Errorf("edges = %v, want none for a foreign-catalog resource", rels)
			}
		})
	}
}

// An opt-in whose targets were not scanned, or whose attributes carry no
// principal or resource, emits no edge and no error.
func TestResolveLakeFormationOptInTargets_NoAttrsOrUnscannedNoEdge(t *testing.T) {
	st := newTestStore(t)
	empty := upsertTestResource(t, st, "aws", testAccountID, TypeLakeFormationOptIn,
		"arn:aws:lakeformation:us-east-1:123456789012:lake-formation-opt-in/empty", testRegion, "{}")
	unscanned := lfOptInRow(t, st, "arn:aws:iam::123456789012:role/ghost",
		&lftypes.Resource{Database: &lftypes.DatabaseResource{Name: sdkaws.String("ghost")}})
	if err := resolveLakeFormationOptInTargets(newTestAccount(testAccountID), st); err != nil {
		t.Fatalf("resolveLakeFormationOptInTargets: %v", err)
	}
	for _, id := range []string{empty, unscanned} {
		rels, err := st.RelationshipsFrom(id)
		if err != nil {
			t.Fatalf("RelationshipsFrom: %v", err)
		}
		if len(rels) != 0 {
			t.Errorf("edges from %s = %v, want none", id, rels)
		}
	}
}

func TestResolveLakeFormationOptInTargets_DecodeErrorReturned(t *testing.T) {
	st := newTestStore(t)
	upsertTestResource(t, st, "aws", testAccountID, TypeLakeFormationOptIn,
		"arn:aws:lakeformation:us-east-1:123456789012:lake-formation-opt-in/bad", testRegion, `not json`)
	var syntaxErr *json.SyntaxError
	if err := resolveLakeFormationOptInTargets(newTestAccount(testAccountID), st); !errors.As(err, &syntaxErr) {
		t.Errorf("err = %v, want the JSON decode error", err)
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
