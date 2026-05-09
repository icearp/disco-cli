package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/s3control"
)

// scanS3ObjectLambdaAccessPoints discovers S3 Object Lambda access points
// (with embedded resource policies). Both list under s3control with
// AccountId scoping.
func scanS3ObjectLambdaAccessPoints(ctx context.Context, acct *account, region string, client s3controlAPI, st *store.Store, scanID string) (total, inserted int, err error) {
	var apBatch []*store.Resource
	var policyBatch []*store.Resource
	var nextToken *string
	for {
		out, lerr := client.ListAccessPointsForObjectLambda(ctx, &s3control.ListAccessPointsForObjectLambdaInput{
			AccountId: &acct.ID,
			NextToken: nextToken,
		})
		if lerr != nil {
			if isAccessDenied(lerr) {
				return 0, 0, skipIfAccessDenied(st, "s3control:ListAccessPointsForObjectLambda", acct.ID, region, lerr)
			}
			return 0, 0, fmt.Errorf("s3control:ListAccessPointsForObjectLambda: %w", lerr)
		}
		for _, ap := range out.ObjectLambdaAccessPointList {
			arn := sv(ap.ObjectLambdaAccessPointArn)
			name := sv(ap.Name)
			if arn == "" || name == "" {
				continue
			}
			apBatch = append(apBatch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeS3ObjectLambdaAccessPoint, NativeID: arn,
				Name: &name, Region: &region,
				AttributesJSON: mustJSON(ap), DiscoveredBy: scanID,
			})
			polOut, perr := client.GetAccessPointPolicyForObjectLambda(ctx, &s3control.GetAccessPointPolicyForObjectLambdaInput{
				AccountId: &acct.ID,
				Name:      &name,
			})
			if perr != nil {
				if isAccessDenied(perr) || isAPIErrorCode(perr, "NoSuchAccessPointPolicy") {
					continue
				}
				return 0, 0, fmt.Errorf("s3control:GetAccessPointPolicyForObjectLambda %s: %w", name, perr)
			}
			if sv(polOut.Policy) == "" {
				continue
			}
			polArn := arn + "/policy"
			polLabel := name
			policyBatch = append(policyBatch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeS3ObjectLambdaAccessPointPolicy, NativeID: polArn,
				Name: &polLabel, Region: &region,
				AttributesJSON: mustJSON(polOut), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	t1, i1, err := upsertBatch(st, apBatch, "s3-object-lambda access-points")
	if err != nil {
		return total, inserted, err
	}
	total += t1
	inserted += i1
	t2, i2, err := upsertBatch(st, policyBatch, "s3-object-lambda access-point-policies")
	total += t2
	inserted += i2
	return total, inserted, err
}
