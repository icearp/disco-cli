package aws

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/s3control"
	s3ctypes "github.com/aws/aws-sdk-go-v2/service/s3control/types"
	"github.com/icearp/disco-cli/store"
)

// scanS3ObjectLambdaAccessPoints stores the region's S3 Object Lambda access
// points, listed under s3control and scoped by AccountId, and returns those
// stored, whose policies scanS3ObjectLambdaAccessPointPolicies reads.
func scanS3ObjectLambdaAccessPoints(ctx context.Context, acct *account, region string, client s3controlAPI, st *store.Store, scanID string) (stored []s3ctypes.ObjectLambdaAccessPoint, total, inserted int, err error) {
	var apBatch []*store.Resource
	var nextToken *string
	for {
		out, lerr := client.ListAccessPointsForObjectLambda(ctx, &s3control.ListAccessPointsForObjectLambdaInput{
			AccountId: &acct.ID,
			NextToken: nextToken,
		})
		if lerr != nil {
			if isAccessDenied(lerr) {
				return nil, 0, 0, skipIfAccessDenied(st, "s3control:ListAccessPointsForObjectLambda", acct.ID, region, lerr)
			}
			return nil, 0, 0, fmt.Errorf("s3control:ListAccessPointsForObjectLambda: %w", lerr)
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
			stored = append(stored, ap)
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	total, inserted, err = upsertBatch(st, apBatch, "s3-object-lambda access-points")
	if err != nil {
		return nil, total, inserted, err
	}
	return stored, total, inserted, nil
}

// scanS3ObjectLambdaAccessPointPolicies stores the resource policy of each
// stored Object Lambda access point that has one. A failed read does not
// stop the others: its error is returned once the policies read are stored.
func scanS3ObjectLambdaAccessPointPolicies(ctx context.Context, acct *account, region string, client s3controlAPI, aps []s3ctypes.ObjectLambdaAccessPoint, st *store.Store, scanID string) (int, int, error) {
	var (
		policyBatch []*store.Resource
		errs        []error
	)
	for _, ap := range aps {
		name := sv(ap.Name)
		polOut, perr := client.GetAccessPointPolicyForObjectLambda(ctx, &s3control.GetAccessPointPolicyForObjectLambdaInput{
			AccountId: &acct.ID,
			Name:      &name,
		})
		if perr != nil {
			if !isAccessDenied(perr) && !isAPIErrorCode(perr, "NoSuchAccessPointPolicy") {
				errs = append(errs, fmt.Errorf("s3control:GetAccessPointPolicyForObjectLambda %s: %w", name, perr))
			}
			continue
		}
		if sv(polOut.Policy) == "" {
			continue
		}
		polArn := sv(ap.ObjectLambdaAccessPointArn) + "/policy"
		policyBatch = append(policyBatch, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeS3ObjectLambdaAccessPointPolicy, NativeID: polArn,
			Name: &name, Region: &region,
			AttributesJSON: mustJSON(polOut), DiscoveredBy: scanID,
		})
	}
	total, inserted, err := upsertBatch(st, policyBatch, "s3-object-lambda access-point-policies")
	return total, inserted, errors.Join(append([]error{err}, errs...)...)
}
