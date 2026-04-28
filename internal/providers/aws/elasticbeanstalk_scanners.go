package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/elasticbeanstalk"
)

func init() { registerService(serviceEntry{name: "aws:elasticbeanstalk", fn: scanElasticBeanstalk}) }

// elasticbeanstalkAPI is the narrow set of Elastic Beanstalk operations
// called by the scanElasticBeanstalk sub-phases.
type elasticbeanstalkAPI interface {
	DescribeApplications(context.Context, *elasticbeanstalk.DescribeApplicationsInput, ...func(*elasticbeanstalk.Options)) (*elasticbeanstalk.DescribeApplicationsOutput, error)
	DescribeEnvironments(context.Context, *elasticbeanstalk.DescribeEnvironmentsInput, ...func(*elasticbeanstalk.Options)) (*elasticbeanstalk.DescribeEnvironmentsOutput, error)
}

// scanElasticBeanstalk discovers Beanstalk applications and environments
// in one region. Two phases. DescribeApplications returns the full list
// in a single call (no pagination — small per-account quota). Environments
// use manual NextToken pagination (no SDK paginator). Per-phase
// AccessDenied tolerated. Application versions, configuration templates,
// configuration option settings, and platform versions deferred —
// versions explode in volume on long-lived apps; configurations are
// declarative metadata better expressed via CFN stack edges (Beanstalk
// owns an underlying CFN stack per environment, already covered by
// aws:cloudformation:stack scanner).
func scanElasticBeanstalk(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := elasticbeanstalk.NewFromConfig(acct.cfg, func(o *elasticbeanstalk.Options) { o.Region = region })

	if t, i, ferr := scanBeanstalkApplications(ctx, client, acct, region, st, scanID); ferr != nil {
		return total, inserted, ferr
	} else {
		total += t
		inserted += i
	}

	if t, i, ferr := scanBeanstalkEnvironments(ctx, client, acct, region, st, scanID); ferr != nil {
		return total, inserted, ferr
	} else {
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
