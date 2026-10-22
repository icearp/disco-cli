package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/elasticbeanstalk"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeBeanstalkApplication, Service: "elasticbeanstalk"})
	registerType(restype.Descriptor{Type: TypeBeanstalkEnvironment, Service: "elasticbeanstalk"})
	registerType(restype.Descriptor{Type: TypeBeanstalkApplicationVersion, Service: "elasticbeanstalk"})
	registerType(restype.Descriptor{Type: TypeBeanstalkPlatform, Service: "elasticbeanstalk"})
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
	DescribeConfigurationSettings(context.Context, *elasticbeanstalk.DescribeConfigurationSettingsInput, ...func(*elasticbeanstalk.Options)) (*elasticbeanstalk.DescribeConfigurationSettingsOutput, error)
}

// scanElasticBeanstalk discovers Beanstalk applications, environments,
// application versions, custom platform versions and saved configuration
// templates in one region.
// DescribeApplications returns the full list in a single call (no
// pagination — small per-account quota); environments use manual NextToken
// pagination (no SDK paginator). Per-phase AccessDenied tolerated.
// Configuration templates have no list API: their names come from each
// application's ConfigurationTemplates[] (scanBeanstalkConfigurationTemplates).
// Solution stacks are an AWS catalog. The AWS-managed platform
// catalogue is excluded by filtering ListPlatformVersions to
// PlatformOwner=self (custom platforms only).
func scanElasticBeanstalk(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := elasticbeanstalk.NewFromConfig(acct.cfg, func(o *elasticbeanstalk.Options) { o.Region = region })
	return scanElasticBeanstalkAll(ctx, client, acct, region, st, scanID)
}

// scanElasticBeanstalkAll runs the older phases, which stop at their first hard
// error, then the configuration-template phase over the applications stored
// before that error, so a failure on either side costs the other no rows. The
// older side's transient error is demoted to a warning only beside a hard
// error: joined, it would be buried in the hard error instead of reported under
// its op. The template phase tallies its own transient errors per template.
func scanElasticBeanstalkAll(ctx context.Context, client elasticbeanstalkAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	total, inserted, apps, err := scanBeanstalkOlderPhases(ctx, client, acct, region, st, scanID)
	t, i, terr := scanBeanstalkConfigurationTemplates(ctx, client, acct, region, st, scanID, apps)
	return total + t, inserted + i, demoteBeside(st, "elasticbeanstalk", acct, region, err, terr)
}

// scanBeanstalkOlderPhases runs the phases that predate configuration
// templates, stopping at the first hard error, and returns the applications
// scanBeanstalkApplications stored.
func scanBeanstalkOlderPhases(ctx context.Context, client elasticbeanstalkAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, apps []beanstalkApplication, err error) {
	total, inserted, apps, err = scanBeanstalkApplications(ctx, client, acct, region, st, scanID)
	if err != nil {
		return total, inserted, apps, err
	}
	for _, scan := range []func(context.Context, elasticbeanstalkAPI, *account, string, *store.Store, string) (int, int, error){
		scanBeanstalkEnvironments,
		scanBeanstalkApplicationVersions,
		scanBeanstalkPlatforms,
	} {
		t, i, ferr := scan(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, apps, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, apps, nil
}

// beanstalkApplication is an application row stored this scan, with the
// names of its saved configuration templates.
type beanstalkApplication struct {
	arn, name string
	templates []string
}

// scanBeanstalkApplications stores the region's applications and returns
// them; it returns none unless the upsert succeeded.
func scanBeanstalkApplications(ctx context.Context, client elasticbeanstalkAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, apps []beanstalkApplication, err error) {
	out, perr := client.DescribeApplications(ctx, &elasticbeanstalk.DescribeApplicationsInput{})
	if perr != nil {
		if isAccessDenied(perr) {
			_ = skipIfAccessDenied(st, "elasticbeanstalk:DescribeApplications", acct.ID, region, perr)
			return 0, 0, nil, nil
		}
		return 0, 0, nil, fmt.Errorf("elasticbeanstalk:DescribeApplications: %w", perr)
	}
	if len(out.Applications) == 0 {
		return 0, 0, nil, nil
	}
	batch := make([]*store.Resource, 0, len(out.Applications))
	for _, a := range out.Applications {
		arn := sv(a.ApplicationArn)
		if arn == "" {
			continue
		}
		name := sv(a.ApplicationName)
		apps = append(apps, beanstalkApplication{arn: arn, name: name, templates: a.ConfigurationTemplates})
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
		return 0, 0, nil, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, nil, fmt.Errorf("upsert beanstalk applications: %w", uerr)
	}
	return len(batch), n, apps, nil
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
