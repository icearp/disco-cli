package aws

import (
	"encoding/json"
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
	aftypes "github.com/aws/aws-sdk-go-v2/service/appfabric/types"
)

func TestResolveAppFabricChildToBundle(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	bundleARN := fmt.Sprintf("arn:aws:appfabric:%s:%s:appbundle/ab-1", testRegion, acct.ID)
	bundleID := upsertTestResource(t, st, "aws", acct.ID, TypeAppFabricAppBundle, bundleARN, testRegion, "{}")

	authARN := fmt.Sprintf("arn:aws:appfabric:%s:%s:appbundle/ab-1/appauthorization/au-1", testRegion, acct.ID)
	authAttrs, _ := json.Marshal(aftypes.AppAuthorizationSummary{AppBundleArn: ptrStr(bundleARN), App: ptrStr("SLACK")})
	authID := upsertTestResource(t, st, "aws", acct.ID, TypeAppFabricAppAuthorization, authARN, testRegion, string(authAttrs))

	ingARN := fmt.Sprintf("arn:aws:appfabric:%s:%s:appbundle/ab-1/ingestion/in-1", testRegion, acct.ID)
	ingAttrs, _ := json.Marshal(aftypes.IngestionSummary{Arn: ptrStr(ingARN), App: ptrStr("SLACK")})
	ingID := upsertTestResource(t, st, "aws", acct.ID, TypeAppFabricIngestion, ingARN, testRegion, string(ingAttrs))

	if err := resolveAppFabricChildToBundle(acct, st); err != nil {
		t.Fatalf("resolveAppFabricChildToBundle: %v", err)
	}
	for _, src := range []string{authID, ingID} {
		rels, _ := st.RelationshipsFrom(src)
		assertRelationship(t, rels, src, bundleID, store.RelAttachedTo)
	}
}

func TestResolveAppFabricChildToBundle_NoEdge(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	// Bundle present so the resolver runs, but the children point elsewhere.
	missingBundle := fmt.Sprintf("arn:aws:appfabric:%s:%s:appbundle/gone", testRegion, acct.ID)
	upsertTestResource(t, st, "aws", acct.ID, TypeAppFabricAppBundle, fmt.Sprintf("arn:aws:appfabric:%s:%s:appbundle/real", testRegion, acct.ID), testRegion, "{}")

	authARN := fmt.Sprintf("arn:aws:appfabric:%s:%s:appbundle/gone/appauthorization/au-1", testRegion, acct.ID)
	authAttrs, _ := json.Marshal(aftypes.AppAuthorizationSummary{AppBundleArn: ptrStr(missingBundle)})
	authID := upsertTestResource(t, st, "aws", acct.ID, TypeAppFabricAppAuthorization, authARN, testRegion, string(authAttrs))

	if err := resolveAppFabricChildToBundle(acct, st); err != nil {
		t.Fatalf("resolveAppFabricChildToBundle: %v", err)
	}
	rels, _ := st.RelationshipsFrom(authID)
	if len(rels) != 0 {
		t.Errorf("emitted %d edges, want 0", len(rels))
	}
}

func TestResolveAppFabricDestination(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	ingARN := fmt.Sprintf("arn:aws:appfabric:%s:%s:appbundle/ab-1/ingestion/in-1", testRegion, acct.ID)
	ingID := upsertTestResource(t, st, "aws", acct.ID, TypeAppFabricIngestion, ingARN, testRegion, "{}")

	bucketID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, "arn:aws:s3:::logs-bkt", testRegion, "{}")

	destARN := ingARN + "/ingestiondestination/id-1"
	// Build attrs via the production flattening over a fully-populated
	// destination union — mirrors what the scanner stores, guarding the union
	// type-switch + the resolver's flat-key read.
	attrs := flattenIngestionDest(&aftypes.IngestionDestination{
		Arn: ptrStr(destARN), IngestionArn: ptrStr(ingARN),
		DestinationConfiguration: &aftypes.DestinationConfigurationMemberAuditLog{
			Value: aftypes.AuditLogDestinationConfiguration{
				Destination: &aftypes.DestinationMemberS3Bucket{
					Value: aftypes.S3Bucket{BucketName: ptrStr("logs-bkt")},
				},
			},
		},
	})
	if sv(attrs.S3BucketName) != "logs-bkt" {
		t.Fatalf("flattenIngestionDest did not lift S3 bucket: %+v", attrs.S3BucketName)
	}
	raw, _ := json.Marshal(attrs)
	destID := upsertTestResource(t, st, "aws", acct.ID, TypeAppFabricIngestionDestination, destARN, testRegion, string(raw))

	if err := resolveAppFabricDestination(acct, st); err != nil {
		t.Fatalf("resolveAppFabricDestination: %v", err)
	}
	rels, _ := st.RelationshipsFrom(destID)
	assertRelationship(t, rels, destID, ingID, store.RelAttachedTo)
	assertRelationship(t, rels, destID, bucketID, store.RelUses)
}

// TestFlattenIngestionDest_Firehose covers the Firehose arm of the union.
func TestFlattenIngestionDest_Firehose(t *testing.T) {
	out := flattenIngestionDest(&aftypes.IngestionDestination{
		Arn: ptrStr("arn:aws:appfabric:us-east-1:111122223333:appbundle/a/ingestion/b/ingestiondestination/c"),
		DestinationConfiguration: &aftypes.DestinationConfigurationMemberAuditLog{
			Value: aftypes.AuditLogDestinationConfiguration{
				Destination: &aftypes.DestinationMemberFirehoseStream{
					Value: aftypes.FirehoseStream{StreamName: ptrStr("sec-stream")},
				},
			},
		},
	})
	if sv(out.FirehoseStreamName) != "sec-stream" {
		t.Errorf("FirehoseStreamName = %v; want sec-stream", out.FirehoseStreamName)
	}
	if out.S3BucketName != nil {
		t.Errorf("S3BucketName should be nil for a Firehose destination, got %v", out.S3BucketName)
	}
}

// A destination whose ingestion/bucket targets are unscanned emits no edges.
func TestResolveAppFabricDestination_NoEdge(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	ingARN := fmt.Sprintf("arn:aws:appfabric:%s:%s:appbundle/ab-1/ingestion/gone", testRegion, acct.ID)
	destARN := ingARN + "/ingestiondestination/id-1"
	attrs := ingestionDestAttrs{
		IngestionDestination: aftypes.IngestionDestination{Arn: ptrStr(destARN)},
		S3BucketName:         ptrStr("missing-bkt"),
	}
	raw, _ := json.Marshal(attrs)
	destID := upsertTestResource(t, st, "aws", acct.ID, TypeAppFabricIngestionDestination, destARN, testRegion, string(raw))

	if err := resolveAppFabricDestination(acct, st); err != nil {
		t.Fatalf("resolveAppFabricDestination: %v", err)
	}
	rels, _ := st.RelationshipsFrom(destID)
	if len(rels) != 0 {
		t.Errorf("emitted %d edges, want 0", len(rels))
	}
}
