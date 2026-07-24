package aws

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/elasticbeanstalk"
)

func init() {
	registerType(restype.Descriptor{Type: TypeBeanstalkApplication, Service: "elasticbeanstalk", Upstream: "AWS::ElasticBeanstalk::Application"})
	registerType(restype.Descriptor{Type: TypeBeanstalkEnvironment, Service: "elasticbeanstalk", Upstream: "AWS::ElasticBeanstalk::Environment", Leaf: true})
	registerType(restype.Descriptor{Type: TypeBeanstalkApplicationVersion, Service: "elasticbeanstalk"})
	registerType(restype.Descriptor{Type: TypeBeanstalkPlatform, Service: "elasticbeanstalk", Leaf: true})
	registerService(serviceEntry{
		name: "aws:elasticbeanstalk",
		fn:   scanElasticBeanstalk,
	})
}

// elasticbeanstalkAPI is the narrow set of Elastic Beanstalk operations
// called by the scanElasticBeanstalk sub-phases.
type elasticbeanstalkAPI interface {
	DescribeApplications(context.Context, *elasticbeanstalk.DescribeApplicationsInput, ...func(*elasticbeanstalk.Options)) (*elasticbeanstalk.DescribeApplicationsOutput, error)
	DescribeEnvironments(context.Context, *elasticbeanstalk.DescribeEnvironmentsInput, ...func(*elasticbeanstalk.Options)) (*elasticbeanstalk.DescribeEnvironmentsOutput, error)
	DescribeApplicationVersions(context.Context, *elasticbeanstalk.DescribeApplicationVersionsInput, ...func(*elasticbeanstalk.Options)) (*elasticbeanstalk.DescribeApplicationVersionsOutput, error)
	ListPlatformVersions(context.Context, *elasticbeanstalk.ListPlatformVersionsInput, ...func(*elasticbeanstalk.Options)) (*elasticbeanstalk.ListPlatformVersionsOutput, error)
}

// scanElasticBeanstalk discovers Beanstalk applications, environments,
// application versions, and custom platform versions in one region.
// DescribeApplications returns the full list in a single call (no
// pagination — small per-account quota); environments use manual NextToken
// pagination (no SDK paginator). Per-phase AccessDenied tolerated.
// Configuration templates are skipped (sub-resource: names embedded in the
// application's ConfigurationTemplates[], no standalone list API); solution
// stacks are an AWS catalog (aws_skips.go). The AWS-managed platform
// catalogue is excluded by filtering ListPlatformVersions to
// PlatformOwner=self (custom platforms only).
func scanElasticBeanstalk(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := elasticbeanstalk.NewFromConfig(acct.cfg, func(o *elasticbeanstalk.Options) { o.Region = region })

	for _, scan := range []func(context.Context, elasticbeanstalkAPI, *account, string, *store.Store, string) (int, int, error){
		scanBeanstalkApplications,
		scanBeanstalkEnvironments,
		scanBeanstalkApplicationVersions,
		scanBeanstalkPlatforms,
	} {
		t, i, ferr := scan(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanBeanstalkApplications(ctx context.Context, client elasticbeanstalkAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	out, perr := client.DescribeApplications(ctx, &elasticbeanstalk.DescribeApplicationsInput{})
	if perr != nil {
		if isAccessDenied(perr) {
			_ = skipIfAccessDenied(st, "elasticbeanstalk:DescribeApplications", acct.ID, region, perr)
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("elasticbeanstalk:DescribeApplications: %w", perr)
	}
	if len(out.Applications) == 0 {
		return 0, 0, nil
	}
	batch := make([]*store.Resource, 0, len(out.Applications))
	for _, a := range out.Applications {
		arn := sv(a.ApplicationArn)
		if arn == "" {
			continue
		}
		name := sv(a.ApplicationName)
		batch = append(batch, &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeBeanstalkApplication,
			NativeID:       arn,
			Name:           &name,
			Region:         &region,
			AttributesJSON: mustJSON(a),
			DiscoveredBy:   scanID,
		})
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert beanstalk applications: %w", uerr)
	}
	return len(batch), n, nil
}

func scanBeanstalkEnvironments(ctx context.Context, client elasticbeanstalkAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, perr := client.DescribeEnvironments(ctx, &elasticbeanstalk.DescribeEnvironmentsInput{NextToken: nextToken})
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "elasticbeanstalk:DescribeEnvironments", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("elasticbeanstalk:DescribeEnvironments: %w", perr)
		}
		for _, e := range out.Environments {
			arn := sv(e.EnvironmentArn)
			if arn == "" {
				continue
			}
			name := sv(e.EnvironmentName)
			status := string(e.Status)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeBeanstalkEnvironment,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				Status:         &status,
				AttributesJSON: mustJSON(e),
				DiscoveredBy:   scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert beanstalk environments: %w", uerr)
	}
	return len(batch), n, nil
}
