package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/kinesisanalytics"
)

func init() {
	registerService(serviceEntry{
		name: "aws:kinesis-analytics",
		fn:   scanKinesisAnalyticsV1,
		emits: []coverage.TypeDecl{
			{Service: "kinesis-analytics", DiscoType: TypeKinesisAnalyticsApplication},
			{Service: "kinesis-analytics", DiscoType: TypeKinesisAnalyticsApplicationOutput},
			{Service: "kinesis-analytics", DiscoType: TypeKinesisAnalyticsApplicationReferenceData},
		},
	})
}

// scanKinesisAnalyticsV1 discovers v1 Kinesis Analytics SQL applications and
// their embedded outputs and reference data sources. ARN native on
// applications; outputs and reference data sources synthesize off the
// parent application ARN.
func scanKinesisAnalyticsV1(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := kinesisanalytics.NewFromConfig(acct.cfg, func(o *kinesisanalytics.Options) { o.Region = region })

	var appNames []string
	var appBatch []*store.Resource
	var exclusiveStart *string
	for {
		out, perr := client.ListApplications(ctx, &kinesisanalytics.ListApplicationsInput{ExclusiveStartApplicationName: exclusiveStart})
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "kinesisanalytics:ListApplications", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("kinesisanalytics:ListApplications: %w", perr)
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
				Type: TypeKinesisAnalyticsApplication, NativeID: arn,
				Name: s.ApplicationName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
		if out.HasMoreApplications == nil || !*out.HasMoreApplications || len(out.ApplicationSummaries) == 0 {
			break
		}
		last := out.ApplicationSummaries[len(out.ApplicationSummaries)-1].ApplicationName
		exclusiveStart = last
	}
	t, i, ferr := upsertBatch(st, appBatch, "kinesisanalytics applications")
	if ferr != nil {
		return 0, 0, ferr
	}
	total += t
	inserted += i

	for _, name := range appNames {
		nm := name
		out, derr := client.DescribeApplication(ctx, &kinesisanalytics.DescribeApplicationInput{ApplicationName: &nm})
		if derr != nil {
			if isAccessDenied(derr) {
				continue
			}
			return total, inserted, fmt.Errorf("kinesisanalytics:DescribeApplication: %w", derr)
		}
		d := out.ApplicationDetail
		if d == nil {
			continue
		}
		appARN := sv(d.ApplicationARN)
		if appARN == "" {
			continue
		}
		// Re-upsert parent row with detail body so resolvers see
		// CloudWatchLoggingOptionDescriptions[]. UpsertResources ON CONFLICT
		// updates the attributes column.
		appStatus := string(d.ApplicationStatus)
		var subBatch []*store.Resource
		subBatch = append(subBatch, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeKinesisAnalyticsApplication, NativeID: appARN,
			Name: d.ApplicationName, Region: &region, Status: &appStatus,
			AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
		})
		for _, o := range d.OutputDescriptions {
			id := sv(o.OutputId)
			if id == "" {
				continue
			}
			arn := appARN + "/output/" + id
			subBatch = append(subBatch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeKinesisAnalyticsApplicationOutput, NativeID: arn,
				Name: o.Name, Region: &region,
				AttributesJSON: mustJSON(o), DiscoveredBy: scanID,
			})
		}
		for _, r := range d.ReferenceDataSourceDescriptions {
			id := sv(r.ReferenceId)
			if id == "" {
				continue
			}
			arn := appARN + "/reference-data-source/" + id
			subBatch = append(subBatch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeKinesisAnalyticsApplicationReferenceData, NativeID: arn,
				Name: r.TableName, Region: &region,
				AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
		t, i, ferr := upsertBatch(st, subBatch, "kinesisanalytics application-children")
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}
