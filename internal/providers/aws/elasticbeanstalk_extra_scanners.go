package aws

import (
	"context"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/elasticbeanstalk"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/elasticbeanstalk/types"
	"github.com/icearp/disco-cli/store"
)

// scanBeanstalkApplicationVersions discovers all application versions in the
// region (account-wide, manual NextToken — no SDK paginator), writing a page
// at a time. Each version's resolver wires it to its owning application by
// name.
func scanBeanstalkApplicationVersions(ctx context.Context, client elasticbeanstalkAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenPagesTags(ctx, st, acct, region, "elasticbeanstalk:DescribeApplicationVersions",
		func(ctx context.Context, token *string) ([]ebtypes.ApplicationVersionDescription, *string, error) {
			out, err := client.DescribeApplicationVersions(ctx, &elasticbeanstalk.DescribeApplicationVersionsInput{
				MaxRecords: sdkaws.Int32(beanstalkMaxRecords), NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.ApplicationVersions, out.NextToken, nil
		},
		func(v ebtypes.ApplicationVersionDescription) (*store.Resource, string, string) {
			return arnRow("application versions", v.ApplicationVersionArn, v.VersionLabel, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBeanstalkApplicationVersion, NativeID: sv(v.ApplicationVersionArn),
				Name: v.VersionLabel, Region: &region, Status: sp(string(v.Status)),
				CreatedAt: tp(v.DateCreated), AttributesJSON: mustJSON(v), DiscoveredBy: scanID,
			})
		}, tags, p)
}

// scanBeanstalkPlatforms discovers custom platform versions (PlatformOwner=self).
// The AWS-managed platform catalogue is intentionally excluded — it is a
// read-only catalog, not an account resource. edge-less. The model gives
// MaxRecords no maximum, so none is sent. InsufficientPrivilegesException,
// the op's modeled 403, ends the listing as a denial, keeping the pages read
// before it, with one warning (listDenial), as the tag reader counts it.
func scanBeanstalkPlatforms(ctx context.Context, client elasticbeanstalkAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	const listOp = "elasticbeanstalk:ListPlatformVersions"
	filterType, op := "PlatformOwner", "="
	filters := []ebtypes.PlatformFilter{{Type: &filterType, Operator: &op, Values: []string{"self"}}}
	d := listDenial{
		isDenied: func(err error) bool { return isAPIErrorCode(err, beanstalkInsufficientPrivileges) },
		unread:   "platform versions on later pages",
	}
	storeWriteFailed = storeTokenPagesTags(ctx, st, acct, region, listOp,
		deniedPages(&d, func(ctx context.Context, token *string) ([]ebtypes.PlatformSummary, *string, error) {
			out, err := client.ListPlatformVersions(ctx, &elasticbeanstalk.ListPlatformVersionsInput{Filters: filters, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.PlatformSummaryList, out.NextToken, nil
		}),
		func(s ebtypes.PlatformSummary) (*store.Resource, string, string) {
			return arnRow("platform versions", s.PlatformArn, nil, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBeanstalkPlatform, NativeID: sv(s.PlatformArn),
				Region: &region, Status: sp(string(s.PlatformStatus)),
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}, tags, p)
	// No storeWriteFailed guard: each page is written before the next is
	// asked for, so a failed write ends the listing before any denial, and a
	// denial yields an empty last page that writes nothing.
	d.warn(st, listOp, acct, region)
	return storeWriteFailed
}
