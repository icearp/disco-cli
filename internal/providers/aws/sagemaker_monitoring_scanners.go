package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/sagemaker"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeSageMakerMonitoringSchedule, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerDataQualityJobDefinition, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerModelBiasJobDefinition, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerModelExplainabilityJobDefinition, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerModelQualityJobDefinition, Service: "sagemaker"})
}

// sagemakerMonitoringAPI is the narrow surface for the Monitoring family.
// Each phase List+fan-out Describes so attrs carry the full body
// (MonitoringScheduleConfig + EndpointName for schedules, RoleArn +
// JobResources + NetworkConfig + StoppingCondition for job definitions).
type sagemakerMonitoringAPI interface {
	ListMonitoringSchedules(context.Context, *sagemaker.ListMonitoringSchedulesInput, ...func(*sagemaker.Options)) (*sagemaker.ListMonitoringSchedulesOutput, error)
	DescribeMonitoringSchedule(context.Context, *sagemaker.DescribeMonitoringScheduleInput, ...func(*sagemaker.Options)) (*sagemaker.DescribeMonitoringScheduleOutput, error)
	ListDataQualityJobDefinitions(context.Context, *sagemaker.ListDataQualityJobDefinitionsInput, ...func(*sagemaker.Options)) (*sagemaker.ListDataQualityJobDefinitionsOutput, error)
	DescribeDataQualityJobDefinition(context.Context, *sagemaker.DescribeDataQualityJobDefinitionInput, ...func(*sagemaker.Options)) (*sagemaker.DescribeDataQualityJobDefinitionOutput, error)
	ListModelBiasJobDefinitions(context.Context, *sagemaker.ListModelBiasJobDefinitionsInput, ...func(*sagemaker.Options)) (*sagemaker.ListModelBiasJobDefinitionsOutput, error)
	DescribeModelBiasJobDefinition(context.Context, *sagemaker.DescribeModelBiasJobDefinitionInput, ...func(*sagemaker.Options)) (*sagemaker.DescribeModelBiasJobDefinitionOutput, error)
	ListModelExplainabilityJobDefinitions(context.Context, *sagemaker.ListModelExplainabilityJobDefinitionsInput, ...func(*sagemaker.Options)) (*sagemaker.ListModelExplainabilityJobDefinitionsOutput, error)
	DescribeModelExplainabilityJobDefinition(context.Context, *sagemaker.DescribeModelExplainabilityJobDefinitionInput, ...func(*sagemaker.Options)) (*sagemaker.DescribeModelExplainabilityJobDefinitionOutput, error)
	ListModelQualityJobDefinitions(context.Context, *sagemaker.ListModelQualityJobDefinitionsInput, ...func(*sagemaker.Options)) (*sagemaker.ListModelQualityJobDefinitionsOutput, error)
	DescribeModelQualityJobDefinition(context.Context, *sagemaker.DescribeModelQualityJobDefinitionInput, ...func(*sagemaker.Options)) (*sagemaker.DescribeModelQualityJobDefinitionOutput, error)
}

// scanSageMakerMonitoring runs all Monitoring family phases for one region.
func scanSageMakerMonitoring(ctx context.Context, client sagemakerMonitoringAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	for _, phase := range []func(context.Context, sagemakerMonitoringAPI, *account, string, *store.Store, string) (int, int, error){
		scanSageMakerMonitoringSchedules,
		scanSageMakerDataQualityJobDefinitions,
		scanSageMakerModelBiasJobDefinitions,
		scanSageMakerModelExplainabilityJobDefinitions,
		scanSageMakerModelQualityJobDefinitions,
	} {
		t, i, ferr := phase(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanSageMakerMonitoringSchedules(ctx context.Context, client sagemakerMonitoringAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := sagemaker.NewListMonitoringSchedulesPaginator(client, &sagemaker.ListMonitoringSchedulesInput{})
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sagemaker:ListMonitoringSchedules", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("sagemaker:ListMonitoringSchedules: %w", perr)
		}
		for _, s := range out.MonitoringScheduleSummaries {
			if s.MonitoringScheduleName != nil {
				names = append(names, *s.MonitoringScheduleName)
			}
		}
	}
	return sagemakerDescribeFanout(ctx, names, fanoutMed, func(gctx context.Context, name string) (*store.Resource, error) {
		out, derr := client.DescribeMonitoringSchedule(gctx, &sagemaker.DescribeMonitoringScheduleInput{MonitoringScheduleName: &name})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("sagemaker:DescribeMonitoringSchedule %s: %w", name, derr)
		}
		arn := sv(out.MonitoringScheduleArn)
		if arn == "" {
			return nil, nil
		}
		sname := sv(out.MonitoringScheduleName)
		status := string(out.MonitoringScheduleStatus)
		return &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeSageMakerMonitoringSchedule,
			NativeID:       arn,
			Name:           &sname,
			Region:         &region,
			Status:         &status,
			CreatedAt:      tp(out.CreationTime),
			AttributesJSON: mustJSON(out),
			DiscoveredBy:   scanID,
		}, nil
	}, st, "sagemaker monitoring schedules")
}

func scanSageMakerDataQualityJobDefinitions(ctx context.Context, client sagemakerMonitoringAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := sagemaker.NewListDataQualityJobDefinitionsPaginator(client, &sagemaker.ListDataQualityJobDefinitionsInput{})
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sagemaker:ListDataQualityJobDefinitions", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("sagemaker:ListDataQualityJobDefinitions: %w", perr)
		}
		for _, j := range out.JobDefinitionSummaries {
			if j.MonitoringJobDefinitionName != nil {
				names = append(names, *j.MonitoringJobDefinitionName)
			}
		}
	}
	return sagemakerDescribeFanout(ctx, names, fanoutMed, func(gctx context.Context, name string) (*store.Resource, error) {
		out, derr := client.DescribeDataQualityJobDefinition(gctx, &sagemaker.DescribeDataQualityJobDefinitionInput{JobDefinitionName: &name})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("sagemaker:DescribeDataQualityJobDefinition %s: %w", name, derr)
		}
		arn := sv(out.JobDefinitionArn)
		if arn == "" {
			return nil, nil
		}
		jname := sv(out.JobDefinitionName)
		return &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeSageMakerDataQualityJobDefinition,
			NativeID:       arn,
			Name:           &jname,
			Region:         &region,
			CreatedAt:      tp(out.CreationTime),
			AttributesJSON: mustJSON(out),
			DiscoveredBy:   scanID,
		}, nil
	}, st, "sagemaker data quality job definitions")
}

func scanSageMakerModelBiasJobDefinitions(ctx context.Context, client sagemakerMonitoringAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := sagemaker.NewListModelBiasJobDefinitionsPaginator(client, &sagemaker.ListModelBiasJobDefinitionsInput{})
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sagemaker:ListModelBiasJobDefinitions", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("sagemaker:ListModelBiasJobDefinitions: %w", perr)
		}
		for _, j := range out.JobDefinitionSummaries {
			if j.MonitoringJobDefinitionName != nil {
				names = append(names, *j.MonitoringJobDefinitionName)
			}
		}
	}
	return sagemakerDescribeFanout(ctx, names, fanoutMed, func(gctx context.Context, name string) (*store.Resource, error) {
		out, derr := client.DescribeModelBiasJobDefinition(gctx, &sagemaker.DescribeModelBiasJobDefinitionInput{JobDefinitionName: &name})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("sagemaker:DescribeModelBiasJobDefinition %s: %w", name, derr)
		}
		arn := sv(out.JobDefinitionArn)
		if arn == "" {
			return nil, nil
		}
		jname := sv(out.JobDefinitionName)
		return &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeSageMakerModelBiasJobDefinition,
			NativeID:       arn,
			Name:           &jname,
			Region:         &region,
			CreatedAt:      tp(out.CreationTime),
			AttributesJSON: mustJSON(out),
			DiscoveredBy:   scanID,
		}, nil
	}, st, "sagemaker model bias job definitions")
}

func scanSageMakerModelExplainabilityJobDefinitions(ctx context.Context, client sagemakerMonitoringAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := sagemaker.NewListModelExplainabilityJobDefinitionsPaginator(client, &sagemaker.ListModelExplainabilityJobDefinitionsInput{})
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sagemaker:ListModelExplainabilityJobDefinitions", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("sagemaker:ListModelExplainabilityJobDefinitions: %w", perr)
		}
		for _, j := range out.JobDefinitionSummaries {
			if j.MonitoringJobDefinitionName != nil {
				names = append(names, *j.MonitoringJobDefinitionName)
			}
		}
	}
	return sagemakerDescribeFanout(ctx, names, fanoutMed, func(gctx context.Context, name string) (*store.Resource, error) {
		out, derr := client.DescribeModelExplainabilityJobDefinition(gctx, &sagemaker.DescribeModelExplainabilityJobDefinitionInput{JobDefinitionName: &name})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("sagemaker:DescribeModelExplainabilityJobDefinition %s: %w", name, derr)
		}
		arn := sv(out.JobDefinitionArn)
		if arn == "" {
			return nil, nil
		}
		jname := sv(out.JobDefinitionName)
		return &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeSageMakerModelExplainabilityJobDefinition,
			NativeID:       arn,
			Name:           &jname,
			Region:         &region,
			CreatedAt:      tp(out.CreationTime),
			AttributesJSON: mustJSON(out),
			DiscoveredBy:   scanID,
		}, nil
	}, st, "sagemaker model explainability job definitions")
}

func scanSageMakerModelQualityJobDefinitions(ctx context.Context, client sagemakerMonitoringAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := sagemaker.NewListModelQualityJobDefinitionsPaginator(client, &sagemaker.ListModelQualityJobDefinitionsInput{})
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sagemaker:ListModelQualityJobDefinitions", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("sagemaker:ListModelQualityJobDefinitions: %w", perr)
		}
		for _, j := range out.JobDefinitionSummaries {
			if j.MonitoringJobDefinitionName != nil {
				names = append(names, *j.MonitoringJobDefinitionName)
			}
		}
	}
	return sagemakerDescribeFanout(ctx, names, fanoutMed, func(gctx context.Context, name string) (*store.Resource, error) {
		out, derr := client.DescribeModelQualityJobDefinition(gctx, &sagemaker.DescribeModelQualityJobDefinitionInput{JobDefinitionName: &name})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("sagemaker:DescribeModelQualityJobDefinition %s: %w", name, derr)
		}
		arn := sv(out.JobDefinitionArn)
		if arn == "" {
			return nil, nil
		}
		jname := sv(out.JobDefinitionName)
		return &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeSageMakerModelQualityJobDefinition,
			NativeID:       arn,
			Name:           &jname,
			Region:         &region,
			CreatedAt:      tp(out.CreationTime),
			AttributesJSON: mustJSON(out),
			DiscoveredBy:   scanID,
		}, nil
	}, st, "sagemaker model quality job definitions")
}
