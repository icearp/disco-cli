package aws

import (
	"context"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iot"
	iottypes "github.com/aws/aws-sdk-go-v2/service/iot/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeIoTCommand, Service: "iot"})
	registerType(restype.Descriptor{Type: TypeIoTJobTemplate, Service: "iot"})
	registerType(restype.Descriptor{Type: TypeIoTFleetMetric, Service: "iot"})
	registerType(restype.Descriptor{Type: TypeIoTProvisioningTemplate, Service: "iot"})
}

type iotJobsAPI interface {
	iotTagsAPI
	ListCommands(context.Context, *iot.ListCommandsInput, ...func(*iot.Options)) (*iot.ListCommandsOutput, error)
	GetCommand(context.Context, *iot.GetCommandInput, ...func(*iot.Options)) (*iot.GetCommandOutput, error)
	ListJobTemplates(context.Context, *iot.ListJobTemplatesInput, ...func(*iot.Options)) (*iot.ListJobTemplatesOutput, error)
	DescribeJobTemplate(context.Context, *iot.DescribeJobTemplateInput, ...func(*iot.Options)) (*iot.DescribeJobTemplateOutput, error)
	ListFleetMetrics(context.Context, *iot.ListFleetMetricsInput, ...func(*iot.Options)) (*iot.ListFleetMetricsOutput, error)
	DescribeFleetMetric(context.Context, *iot.DescribeFleetMetricInput, ...func(*iot.Options)) (*iot.DescribeFleetMetricOutput, error)
	ListProvisioningTemplates(context.Context, *iot.ListProvisioningTemplatesInput, ...func(*iot.Options)) (*iot.ListProvisioningTemplatesOutput, error)
	DescribeProvisioningTemplate(context.Context, *iot.DescribeProvisioningTemplateInput, ...func(*iot.Options)) (*iot.DescribeProvisioningTemplateOutput, error)
}

// scanIoTJobs runs Jobs-family phases, adding each to p, and reports
// whether a write failed.
func scanIoTJobs(ctx context.Context, client iotJobsAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals, tags iotTags) (storeWriteFailed bool) {
	return runIoTPhases(p,
		func(p *phaseTotals) bool { return scanIoTCommands(ctx, client, acct, region, st, scanID, p, tags) },
		func(p *phaseTotals) bool { return scanIoTJobTemplates(ctx, client, acct, region, st, scanID, p, tags) },
		func(p *phaseTotals) bool { return scanIoTFleetMetrics(ctx, client, acct, region, st, scanID, p, tags) },
		func(p *phaseTotals) bool {
			return scanIoTProvisioningTemplates(ctx, client, acct, region, st, scanID, p, tags)
		},
	)
}

// iotCommandsPageCap is ListCommands' maxResults cap (1-100).
const iotCommandsPageCap = 100

func scanIoTCommands(ctx context.Context, client iotJobsAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals, tags iotTags) (storeWriteFailed bool) {
	list := iotList(ctx, "iot:ListCommands", "iot commands", "CommandId",
		func(ctx context.Context, token *string) ([]iottypes.CommandSummary, *string, error) {
			out, err := client.ListCommands(ctx, &iot.ListCommandsInput{NextToken: token, MaxResults: sdkaws.Int32(iotCommandsPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Commands, out.NextToken, nil
		},
		func(x iottypes.CommandSummary) *string { return x.CommandId },
		func(x iottypes.CommandSummary) *store.Resource {
			return iotListedRow(acct, region, scanID, TypeIoTCommand, x.CommandArn, x.CommandId, x)
		})
	d := list.describe(ctx, st, acct, region, "iot:GetCommand", isIoTNotFound,
		func(ctx context.Context, id string) (*store.Resource, error) {
			out, derr := client.GetCommand(ctx, &iot.GetCommandInput{CommandId: &id})
			if derr != nil {
				return nil, derr
			}
			arn := sv(out.CommandArn)
			if arn == "" {
				return nil, nil
			}
			cid := sv(out.CommandId)
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeIoTCommand,
				NativeID:       arn,
				Name:           &cid,
				Region:         &region,
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}, nil
		})
	return iotStoreTagged(ctx, st, acct, region, p, list, d, tags.reader())
}

func scanIoTJobTemplates(ctx context.Context, client iotJobsAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals, tags iotTags) (storeWriteFailed bool) {
	list := iotList(ctx, "iot:ListJobTemplates", "iot job templates", "JobTemplateId",
		func(ctx context.Context, token *string) ([]iottypes.JobTemplateSummary, *string, error) {
			out, err := client.ListJobTemplates(ctx, &iot.ListJobTemplatesInput{NextToken: token, MaxResults: sdkaws.Int32(iotListPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.JobTemplates, out.NextToken, nil
		},
		func(x iottypes.JobTemplateSummary) *string { return x.JobTemplateId },
		func(x iottypes.JobTemplateSummary) *store.Resource {
			return iotListedRow(acct, region, scanID, TypeIoTJobTemplate, x.JobTemplateArn, x.JobTemplateId, x)
		})
	d := list.describe(ctx, st, acct, region, "iot:DescribeJobTemplate", isIoTNotFound,
		func(ctx context.Context, id string) (*store.Resource, error) {
			out, derr := client.DescribeJobTemplate(ctx, &iot.DescribeJobTemplateInput{JobTemplateId: &id})
			if derr != nil {
				return nil, derr
			}
			arn := sv(out.JobTemplateArn)
			if arn == "" {
				return nil, nil
			}
			jid := sv(out.JobTemplateId)
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeIoTJobTemplate,
				NativeID:       arn,
				Name:           &jid,
				Region:         &region,
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}, nil
		})
	return iotStoreTagged(ctx, st, acct, region, p, list, d, tags.reader())
}

func scanIoTFleetMetrics(ctx context.Context, client iotJobsAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals, tags iotTags) (storeWriteFailed bool) {
	list := iotList(ctx, "iot:ListFleetMetrics", "iot fleet metrics", "MetricName",
		func(ctx context.Context, token *string) ([]iottypes.FleetMetricNameAndArn, *string, error) {
			out, err := client.ListFleetMetrics(ctx, &iot.ListFleetMetricsInput{NextToken: token, MaxResults: sdkaws.Int32(iotListPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.FleetMetrics, out.NextToken, nil
		},
		func(x iottypes.FleetMetricNameAndArn) *string { return x.MetricName },
		func(x iottypes.FleetMetricNameAndArn) *store.Resource {
			return iotListedRow(acct, region, scanID, TypeIoTFleetMetric, x.MetricArn, x.MetricName, x)
		})
	d := list.describe(ctx, st, acct, region, "iot:DescribeFleetMetric", isIoTNotFound,
		func(ctx context.Context, name string) (*store.Resource, error) {
			out, derr := client.DescribeFleetMetric(ctx, &iot.DescribeFleetMetricInput{MetricName: &name})
			if derr != nil {
				return nil, derr
			}
			arn := sv(out.MetricArn)
			if arn == "" {
				return nil, nil
			}
			mname := sv(out.MetricName)
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeIoTFleetMetric,
				NativeID:       arn,
				Name:           &mname,
				Region:         &region,
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}, nil
		})
	return iotStoreTagged(ctx, st, acct, region, p, list, d, tags.reader())
}

func scanIoTProvisioningTemplates(ctx context.Context, client iotJobsAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals, tags iotTags) (storeWriteFailed bool) {
	list := iotList(ctx, "iot:ListProvisioningTemplates", "iot provisioning templates", "TemplateName",
		func(ctx context.Context, token *string) ([]iottypes.ProvisioningTemplateSummary, *string, error) {
			out, err := client.ListProvisioningTemplates(ctx, &iot.ListProvisioningTemplatesInput{NextToken: token, MaxResults: sdkaws.Int32(iotListPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Templates, out.NextToken, nil
		},
		func(x iottypes.ProvisioningTemplateSummary) *string { return x.TemplateName },
		func(x iottypes.ProvisioningTemplateSummary) *store.Resource {
			return iotListedRow(acct, region, scanID, TypeIoTProvisioningTemplate, x.TemplateArn, x.TemplateName, x)
		})
	d := list.describe(ctx, st, acct, region, "iot:DescribeProvisioningTemplate", isIoTNotFound,
		func(ctx context.Context, name string) (*store.Resource, error) {
			out, derr := client.DescribeProvisioningTemplate(ctx, &iot.DescribeProvisioningTemplateInput{TemplateName: &name})
			if derr != nil {
				return nil, derr
			}
			arn := sv(out.TemplateArn)
			if arn == "" {
				return nil, nil
			}
			tname := sv(out.TemplateName)
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeIoTProvisioningTemplate,
				NativeID:       arn,
				Name:           &tname,
				Region:         &region,
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}, nil
		})
	return iotStoreTagged(ctx, st, acct, region, p, list, d, tags.reader())
}
