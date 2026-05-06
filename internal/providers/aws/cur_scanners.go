package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/costandusagereportservice"
)

func init() {
	registerService(serviceEntry{
		name:   "aws:cur",
		global: true,
		fn:     scanCUR,
		emits: []coverage.TypeDecl{
			{Service: "cur", DiscoType: TypeCURReportDefinition},
		},
	})
}

type curAPI interface {
	DescribeReportDefinitions(context.Context, *costandusagereportservice.DescribeReportDefinitionsInput, ...func(*costandusagereportservice.Options)) (*costandusagereportservice.DescribeReportDefinitionsOutput, error)
}

// scanCUR discovers Cost and Usage Report definitions. CUR is global; gate
// to us-east-1. Synth ARN: arn:aws:cur::{a}:definition/{name}.
func scanCUR(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (total, inserted int, err error) {
	region := "us-east-1"
	client := costandusagereportservice.NewFromConfig(acct.cfg, func(o *costandusagereportservice.Options) { o.Region = region })

	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.DescribeReportDefinitions(ctx, &costandusagereportservice.DescribeReportDefinitionsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "cur:DescribeReportDefinitions", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("cur:DescribeReportDefinitions: %w", err)
		}
		for _, r := range out.ReportDefinitions {
			name := sv(r.ReportName)
			if name == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:cur::%s:definition/%s", acct.ID, name)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCURReportDefinition, NativeID: arn,
				Name: &name, Region: &region,
				AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "cur report-definitions")
}
