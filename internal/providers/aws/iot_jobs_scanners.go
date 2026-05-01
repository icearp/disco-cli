package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/iot"
)

func init() {
	registerExtraEmits(
		coverage.TypeDecl{Service: "iot", DiscoType: TypeIoTCommand},
		coverage.TypeDecl{Service: "iot", DiscoType: TypeIoTJobTemplate},
		coverage.TypeDecl{Service: "iot", DiscoType: TypeIoTFleetMetric},
		coverage.TypeDecl{Service: "iot", DiscoType: TypeIoTProvisioningTemplate},
	)
}

type iotJobsAPI interface {
	ListCommands(context.Context, *iot.ListCommandsInput, ...func(*iot.Options)) (*iot.ListCommandsOutput, error)
	GetCommand(context.Context, *iot.GetCommandInput, ...func(*iot.Options)) (*iot.GetCommandOutput, error)
	ListJobTemplates(context.Context, *iot.ListJobTemplatesInput, ...func(*iot.Options)) (*iot.ListJobTemplatesOutput, error)
	DescribeJobTemplate(context.Context, *iot.DescribeJobTemplateInput, ...func(*iot.Options)) (*iot.DescribeJobTemplateOutput, error)
	ListFleetMetrics(context.Context, *iot.ListFleetMetricsInput, ...func(*iot.Options)) (*iot.ListFleetMetricsOutput, error)
	DescribeFleetMetric(context.Context, *iot.DescribeFleetMetricInput, ...func(*iot.Options)) (*iot.DescribeFleetMetricOutput, error)
	ListProvisioningTemplates(context.Context, *iot.ListProvisioningTemplatesInput, ...func(*iot.Options)) (*iot.ListProvisioningTemplatesOutput, error)
	DescribeProvisioningTemplate(context.Context, *iot.DescribeProvisioningTemplateInput, ...func(*iot.Options)) (*iot.DescribeProvisioningTemplateOutput, error)
}

func scanIoTJobs(ctx context.Context, client iotJobsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanIoTCommands(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIoTJobTemplates(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIoTFleetMetrics(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) {
			return scanIoTProvisioningTemplates(ctx, client, acct, region, st, scanID)
		},
	} {
		t, i, ferr := phase()
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanIoTCommands(ctx context.Context, client iotJobsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := iot.NewListCommandsPaginator(client, &iot.ListCommandsInput{})
	var ids []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iot:ListCommands", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iot:ListCommands: %w", perr)
		}
		for _, c := range out.Commands {
			if c.CommandId != nil {
				ids = append(ids, *c.CommandId)
			}
		}
	}
	return iotDescribeFanout(ctx, ids, fanoutMed, func(gctx context.Context, id string) (*store.Resource, error) {
		out, derr := client.GetCommand(gctx, &iot.GetCommandInput{CommandId: &id})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("iot:GetCommand %s: %w", id, derr)
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
	}, st, "iot commands")
}

func scanIoTJobTemplates(ctx context.Context, client iotJobsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := iot.NewListJobTemplatesPaginator(client, &iot.ListJobTemplatesInput{})
	var ids []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iot:ListJobTemplates", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iot:ListJobTemplates: %w", perr)
		}
		for _, j := range out.JobTemplates {
			if j.JobTemplateId != nil {
				ids = append(ids, *j.JobTemplateId)
			}
		}
	}
	return iotDescribeFanout(ctx, ids, fanoutMed, func(gctx context.Context, id string) (*store.Resource, error) {
		out, derr := client.DescribeJobTemplate(gctx, &iot.DescribeJobTemplateInput{JobTemplateId: &id})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("iot:DescribeJobTemplate %s: %w", id, derr)
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
	}, st, "iot job templates")
}

func scanIoTFleetMetrics(ctx context.Context, client iotJobsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := iot.NewListFleetMetricsPaginator(client, &iot.ListFleetMetricsInput{})
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iot:ListFleetMetrics", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iot:ListFleetMetrics: %w", perr)
		}
		for _, m := range out.FleetMetrics {
			if m.MetricName != nil {
				names = append(names, *m.MetricName)
			}
		}
	}
	return iotDescribeFanout(ctx, names, fanoutMed, func(gctx context.Context, name string) (*store.Resource, error) {
		out, derr := client.DescribeFleetMetric(gctx, &iot.DescribeFleetMetricInput{MetricName: &name})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("iot:DescribeFleetMetric %s: %w", name, derr)
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
	}, st, "iot fleet metrics")
}

func scanIoTProvisioningTemplates(ctx context.Context, client iotJobsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := iot.NewListProvisioningTemplatesPaginator(client, &iot.ListProvisioningTemplatesInput{})
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iot:ListProvisioningTemplates", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iot:ListProvisioningTemplates: %w", perr)
		}
		for _, t := range out.Templates {
			if t.TemplateName != nil {
				names = append(names, *t.TemplateName)
			}
		}
	}
	return iotDescribeFanout(ctx, names, fanoutMed, func(gctx context.Context, name string) (*store.Resource, error) {
		out, derr := client.DescribeProvisioningTemplate(gctx, &iot.DescribeProvisioningTemplateInput{TemplateName: &name})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("iot:DescribeProvisioningTemplate %s: %w", name, derr)
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
	}, st, "iot provisioning templates")
}
