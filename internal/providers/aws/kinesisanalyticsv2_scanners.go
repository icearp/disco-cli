package aws

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/kinesisanalyticsv2"
	kav2types "github.com/aws/aws-sdk-go-v2/service/kinesisanalyticsv2/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeKAV2Application, Service: "kinesis-analytics-v2"})
	registerType(restype.Descriptor{Type: TypeKAV2ApplicationCloudWatchLogOpt, Service: "kinesis-analytics-v2"})
	registerType(restype.Descriptor{Type: TypeKAV2ApplicationOutput, Service: "kinesis-analytics-v2"})
	registerType(restype.Descriptor{Type: TypeKAV2ApplicationReferenceData, Service: "kinesis-analytics-v2"})
	registerService(serviceEntry{
		name: "aws:kinesis-analytics-v2",
		fn:   scanKinesisAnalyticsV2,
	})
}

// scanKinesisAnalyticsV2 discovers Kinesis Data Analytics v2 applications and
// their config sub-resources (CloudWatch logging options, outputs, reference
// data sources), embedded in each app's DescribeApplication response.
func scanKinesisAnalyticsV2(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := kinesisanalyticsv2.NewFromConfig(acct.cfg, func(o *kinesisanalyticsv2.Options) { o.Region = region })

	pager := kinesisanalyticsv2.NewListApplicationsPaginator(client, &kinesisanalyticsv2.ListApplicationsInput{})
	// Each application is stored once per scan. The store has no in-place
	// update: a second upsert of the same ARN with different attributes is a
	// version split, so writing the summary and then the detail body added two
	// versions per application on every scan. The summary is kept only as the
	// fallback row when the detail read is denied or empty.
	var summaries []*store.Resource
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
			status := string(s.ApplicationStatus)
			summaries = append(summaries, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeKAV2Application, NativeID: arn,
				Name: s.ApplicationName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}

	// A failed detail read still stores the application from its summary and
	// moves on; the failure is returned once every application is stored.
	var describeErrs []error
	for _, summary := range summaries {
		nm := *summary.Name
		out, derr := client.DescribeApplication(ctx, &kinesisanalyticsv2.DescribeApplicationInput{ApplicationName: &nm})
		if derr != nil && !isAccessDenied(derr) {
			describeErrs = append(describeErrs, fmt.Errorf("kinesisanalyticsv2:DescribeApplication %s: %w", nm, derr))
		}
		var d *kav2types.ApplicationDetail
		if derr == nil {
			d = out.ApplicationDetail
		}
		if d == nil || sv(d.ApplicationARN) == "" {
			t, i, ferr := upsertBatch(st, []*store.Resource{summary}, "kinesisanalyticsv2 applications")
			if ferr != nil {
				return total, inserted, ferr
			}
			total += t
			inserted += i
			continue
		}
		appARN := sv(d.ApplicationARN)
		// The detail body is the application row: resolvers read
		// ServiceExecutionRole and CloudWatchLoggingOptionDescriptions from it.
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
		t, i, ferr := upsertBatch(st, subBatch, "kinesisanalyticsv2 application-children")
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, errors.Join(describeErrs...)
}
