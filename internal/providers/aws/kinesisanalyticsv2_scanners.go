package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/kinesisanalyticsv2"
)

func init() {
	registerService(serviceEntry{
		name: "aws:kinesis-analytics-v2",
		fn:   scanKinesisAnalyticsV2,
		emits: []coverage.TypeDecl{
			{Service: "kinesis-analytics-v2", DiscoType: TypeKAV2Application},
			{Service: "kinesis-analytics-v2", DiscoType: TypeKAV2ApplicationCloudWatchLogOpt},
			{Service: "kinesis-analytics-v2", DiscoType: TypeKAV2ApplicationOutput},
			{Service: "kinesis-analytics-v2", DiscoType: TypeKAV2ApplicationReferenceData},
		},
	})
}

// scanKinesisAnalyticsV2 discovers Kinesis Data Analytics v2 applications and
// their config sub-resources (CloudWatch logging options, outputs, reference
// data sources), embedded in each app's DescribeApplication response.
func scanKinesisAnalyticsV2(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := kinesisanalyticsv2.NewFromConfig(acct.cfg, func(o *kinesisanalyticsv2.Options) { o.Region = region })

	pager := kinesisanalyticsv2.NewListApplicationsPaginator(client, &kinesisanalyticsv2.ListApplicationsInput{})
	var appNames []string
	var appBatch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "kinesisanalyticsv2:ListApplications", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("kinesisanalyticsv2:ListApplications: %w", perr)
		}
		for _, s := range out.ApplicationSummaries {
			arn := sv(s.ApplicationARN)
			name := sv(s.ApplicationName)
			if arn == "" || name == "" {
				continue
			}
			appNames = append(appNames, name)
			status := string(s.ApplicationStatus)
			appBatch = append(appBatch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeKAV2Application, NativeID: arn,
				Name: s.ApplicationName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	t, i, ferr := upsertBatch(st, appBatch, "kinesisanalyticsv2 applications")
	if ferr != nil {
		return 0, 0, ferr
	}
	total += t
	inserted += i

	for _, name := range appNames {
		nm := name
		out, derr := client.DescribeApplication(ctx, &kinesisanalyticsv2.DescribeApplicationInput{ApplicationName: &nm})
		if derr != nil {
			if isAccessDenied(derr) {
				continue
			}
			return total, inserted, fmt.Errorf("kinesisanalyticsv2:DescribeApplication: %w", derr)
		}
		d := out.ApplicationDetail
		if d == nil {
			continue
		}
		appARN := sv(d.ApplicationARN)
		if appARN == "" {
			continue
		}
		// Re-upsert parent with detail body so resolvers see ServiceExecutionRole +
		// CloudWatchLoggingOptionDescriptions[]; UpsertResources ON CONFLICT updates
		// the attributes column.
		appStatus := string(d.ApplicationStatus)
		var subBatch []*store.Resource
		subBatch = append(subBatch, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeKAV2Application, NativeID: appARN,
			Name: d.ApplicationName, Region: &region, Status: &appStatus,
			AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
		})
		for _, lo := range d.CloudWatchLoggingOptionDescriptions {
			id := sv(lo.CloudWatchLoggingOptionId)
			if id == "" {
				continue
			}
			arn := appARN + "/cloud-watch-logging-option/" + id
			label := id
			subBatch = append(subBatch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeKAV2ApplicationCloudWatchLogOpt, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(lo), DiscoveredBy: scanID,
			})
		}
		if cfg := d.ApplicationConfigurationDescription; cfg != nil && cfg.SqlApplicationConfigurationDescription != nil {
			sqlcfg := cfg.SqlApplicationConfigurationDescription
			for _, o := range sqlcfg.OutputDescriptions {
				id := sv(o.OutputId)
				if id == "" {
					continue
				}
				arn := appARN + "/output/" + id
				subBatch = append(subBatch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeKAV2ApplicationOutput, NativeID: arn,
					Name: o.Name, Region: &region,
					AttributesJSON: mustJSON(o), DiscoveredBy: scanID,
				})
			}
			for _, r := range sqlcfg.ReferenceDataSourceDescriptions {
				id := sv(r.ReferenceId)
				if id == "" {
					continue
				}
				arn := appARN + "/reference-data-source/" + id
				subBatch = append(subBatch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeKAV2ApplicationReferenceData, NativeID: arn,
					Name: r.TableName, Region: &region,
					AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
				})
			}
		}
		t, i, ferr = upsertBatch(st, subBatch, "kinesisanalyticsv2 application-children")
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}
