package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/rds"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeRDSDBSnapshotAttribute, Service: "rds"})
	registerType(restype.Descriptor{Type: TypeRDSDBClusterSnapshotAttribute, Service: "rds"})
}

const (
	rdsSnapshotAttributesOp        = "rds:DescribeDBSnapshotAttributes"
	rdsClusterSnapshotAttributesOp = "rds:DescribeDBClusterSnapshotAttributes"
)

// rdsManualSnapshot is the SnapshotType of a user-created snapshot. Only a
// manual snapshot has attributes: both attribute ops describe "a manual DB
// (cluster) snapshot", and the RDS User Guide ("Sharing a DB snapshot") says
// an automated or AWS Backup snapshot must be copied to a manual one to be
// shared.
const rdsManualSnapshot = "manual"

// rdsSnapshotAttributeNativeID is {snapshotARN}/attribute/{AttributeName}:
// the attribute has no ARN of its own, and its name is unique per snapshot.
func rdsSnapshotAttributeNativeID(snapshotARN, attributeName string) string {
	return snapshotARN + "/attribute/" + attributeName
}

// scanDBSnapshotAttributes stores the attributes of each manual DB snapshot
// in snapshots; the restore attribute lists the accounts ("all" when public)
// that may copy or restore it.
func scanDBSnapshotAttributes(ctx context.Context, client rdsAPI, acct *account, region string, st *store.Store, scanID string, snapshots []childParent) (int, int, error) {
	return scanRDSSnapshotAttributes(ctx, st, acct, region, scanID, rdsSnapshotAttributesOp, TypeRDSDBSnapshotAttribute, "DBSnapshotNotFound", snapshots,
		func(ctx context.Context, id string) ([]rdstypes.DBSnapshotAttribute, error) {
			out, err := client.DescribeDBSnapshotAttributes(ctx, &rds.DescribeDBSnapshotAttributesInput{DBSnapshotIdentifier: &id})
			if err != nil || out.DBSnapshotAttributesResult == nil {
				return nil, err
			}
			return out.DBSnapshotAttributesResult.DBSnapshotAttributes, nil
		},
		func(a rdstypes.DBSnapshotAttribute) *string { return a.AttributeName })
}

// scanDBClusterSnapshotAttributes is scanDBSnapshotAttributes for manual DB
// cluster snapshots.
func scanDBClusterSnapshotAttributes(ctx context.Context, client rdsAPI, acct *account, region string, st *store.Store, scanID string, snapshots []childParent) (int, int, error) {
	return scanRDSSnapshotAttributes(ctx, st, acct, region, scanID, rdsClusterSnapshotAttributesOp, TypeRDSDBClusterSnapshotAttribute, "DBClusterSnapshotNotFoundFault", snapshots,
		func(ctx context.Context, id string) ([]rdstypes.DBClusterSnapshotAttribute, error) {
			out, err := client.DescribeDBClusterSnapshotAttributes(ctx, &rds.DescribeDBClusterSnapshotAttributesInput{DBClusterSnapshotIdentifier: &id})
			if err != nil || out.DBClusterSnapshotAttributesResult == nil {
				return nil, err
			}
			return out.DBClusterSnapshotAttributesResult.DBClusterSnapshotAttributes, nil
		},
		func(a rdstypes.DBClusterSnapshotAttribute) *string { return a.AttributeName })
}

// scanRDSSnapshotAttributes stores one row per attribute of each snapshot,
// under its snapshot. A snapshot deleted since it was listed (notFoundCode, the
// op's modeled not-found fault) is skipped; one whose call still fails
// transiently after the SDK's retries is skipped, keeping its siblings' rows,
// with one summary warning.
func scanRDSSnapshotAttributes[A any](
	ctx context.Context, st *store.Store, acct *account, region, scanID, op, typ, notFoundCode string, snapshots []childParent,
	describe func(context.Context, string) ([]A, error), attributeName func(A) *string,
) (int, int, error) {
	isGone := func(err error) bool { return isAPIErrorCode(err, notFoundCode) }
	return childFanOutSkipTransient(ctx, st, acct, region, op, snapshots, isGone, byParentID,
		func(ctx context.Context, snapshot childParent) ([]*store.Resource, error) {
			attrs, err := describe(ctx, snapshot.id)
			if err != nil {
				return nil, err
			}
			var rows []*store.Resource
			for _, a := range attrs {
				name := sv(attributeName(a))
				if name == "" {
					continue
				}
				rows = append(rows, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: typ, NativeID: rdsSnapshotAttributeNativeID(snapshot.arn, name),
					Name: &name, Region: &region,
					AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
				})
			}
			return rows, nil
		})
}
