package aws

import (
	"context"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/elasticbeanstalk"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/elasticbeanstalk/types"
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
	ListTagsForResource(context.Context, *elasticbeanstalk.ListTagsForResourceInput, ...func(*elasticbeanstalk.Options)) (*elasticbeanstalk.ListTagsForResourceOutput, error)
}

const (
	beanstalkTagsOp = "elasticbeanstalk:ListTagsForResource"
	// beanstalkInsufficientPrivileges is the 403 ListTagsForResource and
	// ListPlatformVersions model for a caller without the privileges they
	// need; isAccessDenied does not match it.
	beanstalkInsufficientPrivileges = "InsufficientPrivilegesException"
	// beanstalkMaxRecords is the top of the MaxRecords range the model gives
	// DescribeEnvironments and DescribeApplicationVersions; sent no
	// MaxRecords, they return every element in one response.
	beanstalkMaxRecords int32 = 1000
)

// scanElasticBeanstalk discovers Beanstalk applications, environments,
// application versions, custom platform versions and saved configuration
// templates in one region. Solution stacks are not scanned: they are AWS's
// catalog of platforms, whose ARNs name no account, not account resources.
func scanElasticBeanstalk(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := elasticbeanstalk.NewFromConfig(acct.cfg, func(o *elasticbeanstalk.Options) { o.Region = region })
	return scanElasticBeanstalkAll(ctx, client, acct, region, st, scanID)
}

// scanElasticBeanstalkAll runs each listing as its own phase, one at a time,
// in the order below: a phase's error does not stop the later ones; a failed
// write and ctx's end do (runHostPhases). The configuration-template phase
// runs last, over the applications the first phase stored. Every row has its
// tags read (beanstalkTagReader), and the tag denials of the whole scan are
// one warning.
func scanElasticBeanstalkAll(ctx context.Context, client elasticbeanstalkAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	tags := beanstalkTagReader(client).sharingDenials()
	var apps []beanstalkApplication
	total, inserted, err := runHostPhases(ctx, st, acct, region, "elasticbeanstalk", 0, []hostPhase{
		{op: "elasticbeanstalk:DescribeApplications", run: func(p *phaseTotals) bool {
			var failed bool
			apps, failed = scanBeanstalkApplications(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: "elasticbeanstalk:DescribeEnvironments", run: func(p *phaseTotals) bool {
			return scanBeanstalkEnvironments(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "elasticbeanstalk:DescribeApplicationVersions", run: func(p *phaseTotals) bool {
			return scanBeanstalkApplicationVersions(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "elasticbeanstalk:ListPlatformVersions", run: func(p *phaseTotals) bool {
			return scanBeanstalkPlatforms(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: beanstalkDescribeConfigurationSettingsOp, run: func(p *phaseTotals) bool {
			return scanBeanstalkConfigurationTemplates(ctx, client, acct, region, st, scanID, apps, tags, p)
		}},
	})
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// beanstalkTagReader reads a row's tags with ListTagsForResource, sent the
// row's NativeID: the ARN its listing returned, or for a configuration
// template the ARN beanstalkTemplateARN builds. The Service Reference lists
// every type read here under ListTagsForResource. The op returns every tag in
// one response (it takes no page token). beanstalkInsufficientPrivileges
// counts as a denial.
//
// A terminated environment or a deleted platform version is not read and
// keeps the TagsJSON its builder set: the listings return such rows, and if
// the op answered one with ResourceNotFoundException (unverified live), the
// row would be dropped although its listing returned it.
func beanstalkTagReader(client elasticbeanstalkAPI) rowTagReader {
	return rowTagReader{
		op:       beanstalkTagsOp,
		notFound: []string{"ResourceNotFoundException"},
		denied:   []string{beanstalkInsufficientPrivileges},
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			if beanstalkRowDeleted(r) {
				return r.TagsJSON, nil
			}
			arn := r.NativeID
			out, err := client.ListTagsForResource(ctx, &elasticbeanstalk.ListTagsForResourceInput{ResourceArn: &arn})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(out.ResourceTags, func(t ebtypes.Tag) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}

// beanstalkRowDeleted reports whether r's status says its resource is gone:
// an environment Terminated, a platform version Deleted.
func beanstalkRowDeleted(r *store.Resource) bool {
	switch r.Type {
	case TypeBeanstalkEnvironment:
		return sv(r.Status) == string(ebtypes.EnvironmentStatusTerminated)
	case TypeBeanstalkPlatform:
		return sv(r.Status) == string(ebtypes.PlatformStatusDeleted)
	}
	return false
}

// beanstalkApplication is an application row stored this scan, with the
// names of its saved configuration templates.
type beanstalkApplication struct {
	arn, name string
	templates []string
}

// scanBeanstalkApplications stores the region's applications and returns
// those stored with a name, the parents of the configuration templates.
// DescribeApplications returns every application in one response (it takes
// no page token).
func scanBeanstalkApplications(ctx context.Context, client elasticbeanstalkAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (apps []beanstalkApplication, storeWriteFailed bool) {
	templates := map[string][]string{}
	parents, failed := storeParentPagesTags(ctx, st, acct, region, "elasticbeanstalk:DescribeApplications", "applications", "ApplicationName",
		func(ctx context.Context, _ *string) ([]ebtypes.ApplicationDescription, *string, error) {
			out, err := client.DescribeApplications(ctx, &elasticbeanstalk.DescribeApplicationsInput{})
			if err != nil {
				return nil, nil, err
			}
			return out.Applications, nil, nil
		},
		func(a ebtypes.ApplicationDescription) (*store.Resource, string, string) {
			name := sv(a.ApplicationName)
			r, why, key := arnRow("applications", a.ApplicationArn, a.ApplicationName, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeBeanstalkApplication,
				NativeID:       sv(a.ApplicationArn),
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(a),
				DiscoveredBy:   scanID,
			})
			if r != nil {
				templates[r.NativeID] = a.ConfigurationTemplates
			}
			return r, why, key
		},
		func(a ebtypes.ApplicationDescription) string { return sv(a.ApplicationName) },
		tags, p)
	for _, pa := range parents {
		apps = append(apps, beanstalkApplication{arn: pa.arn, name: pa.id, templates: templates[pa.arn]})
	}
	return apps, failed
}

func scanBeanstalkEnvironments(ctx context.Context, client elasticbeanstalkAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenPagesTags(ctx, st, acct, region, "elasticbeanstalk:DescribeEnvironments",
		func(ctx context.Context, token *string) ([]ebtypes.EnvironmentDescription, *string, error) {
			out, err := client.DescribeEnvironments(ctx, &elasticbeanstalk.DescribeEnvironmentsInput{
				MaxRecords: sdkaws.Int32(beanstalkMaxRecords), NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.Environments, out.NextToken, nil
		},
		func(e ebtypes.EnvironmentDescription) (*store.Resource, string, string) {
			name := sv(e.EnvironmentName)
			status := string(e.Status)
			return arnRow("environments", e.EnvironmentArn, e.EnvironmentName, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeBeanstalkEnvironment,
				NativeID:       sv(e.EnvironmentArn),
				Name:           &name,
				Region:         &region,
				Status:         &status,
				AttributesJSON: mustJSON(e),
				DiscoveredBy:   scanID,
			})
		}, tags, p)
}
