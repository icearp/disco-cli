package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/elasticbeanstalk"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/elasticbeanstalk/types"
	"github.com/icearp/disco-cli/store"
)

// scanBeanstalkApplicationVersions discovers all application versions in the
// region (account-wide, manual NextToken — no SDK paginator). Each version's
// resolver wires it to its owning application by name.
func scanBeanstalkApplicationVersions(ctx context.Context, client elasticbeanstalkAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	var token *string
	var batch []*store.Resource
	for {
		out, perr := client.DescribeApplicationVersions(ctx, &elasticbeanstalk.DescribeApplicationVersionsInput{NextToken: token})
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "elasticbeanstalk:DescribeApplicationVersions", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("elasticbeanstalk:DescribeApplicationVersions: %w", perr)
		}
		for _, v := range out.ApplicationVersions {
			arn := sv(v.ApplicationVersionArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBeanstalkApplicationVersion, NativeID: arn,
				Name: v.VersionLabel, Region: &region, Status: sp(string(v.Status)),
				CreatedAt: tp(v.DateCreated), AttributesJSON: mustJSON(v), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil {
			break
		}
		token = out.NextToken
	}
	return upsertBatch(st, batch, "elasticbeanstalk application-versions")
}

// scanBeanstalkPlatforms discovers custom platform versions (PlatformOwner=self).
// The AWS-managed platform catalogue is intentionally excluded — it is a
// read-only catalog, not an account resource. Leaf.
func scanBeanstalkPlatforms(ctx context.Context, client elasticbeanstalkAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	filterType, op := "PlatformOwner", "="
	in := &elasticbeanstalk.ListPlatformVersionsInput{
		Filters: []ebtypes.PlatformFilter{{Type: &filterType, Operator: &op, Values: []string{"self"}}},
	}
	pager := elasticbeanstalk.NewListPlatformVersionsPaginator(client, in)
	var batch []*store.Resource
	for pager.HasMorePages() {
		page, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "elasticbeanstalk:ListPlatformVersions", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("elasticbeanstalk:ListPlatformVersions: %w", perr)
		}
		for _, p := range page.PlatformSummaryList {
			arn := sv(p.PlatformArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBeanstalkPlatform, NativeID: arn,
				Region: &region, Status: sp(string(p.PlatformStatus)),
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "elasticbeanstalk platforms")
}
