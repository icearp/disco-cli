package aws

import (
	"context"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"

	"github.com/aws/aws-sdk-go-v2/service/sagemaker"
	smtypes "github.com/aws/aws-sdk-go-v2/service/sagemaker/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeSageMakerMonitoringSchedule, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerDataQualityJobDefinition, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerModelBiasJobDefinition, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerModelExplainabilityJobDefinition, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerModelQualityJobDefinition, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerMonitoringAlert, Service: "sagemaker"})
}

// sagemakerMonitoringAPI is the narrow surface for the Monitoring family.
// Each phase List+fan-out Describes so attrs carry the full body
// (MonitoringScheduleConfig + EndpointName for schedules, RoleArn +
// JobResources + NetworkConfig + StoppingCondition for job definitions).
type sagemakerMonitoringAPI interface {
	sagemakerTagsAPI
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
	ListMonitoringAlerts(context.Context, *sagemaker.ListMonitoringAlertsInput, ...func(*sagemaker.Options)) (*sagemaker.ListMonitoringAlertsOutput, error)
}

// scanSageMakerMonitoring runs all Monitoring family phases for one region.
// The alert phase fans out per scanned schedule, so it runs after schedules.
func scanSageMakerMonitoring(ctx context.Context, client sagemakerMonitoringAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return runSageMakerPhases(
		sagemakerPhase{"sagemaker:ListMonitoringSchedules", func() (int, int, error) {
			return scanSageMakerMonitoringSchedules(ctx, client, acct, region, st, scanID)
		}},
		sagemakerPhase{"sagemaker:ListDataQualityJobDefinitions", func() (int, int, error) {
			return scanSageMakerDataQualityJobDefinitions(ctx, client, acct, region, st, scanID)
		}},
		sagemakerPhase{"sagemaker:ListModelBiasJobDefinitions", func() (int, int, error) {
			return scanSageMakerModelBiasJobDefinitions(ctx, client, acct, region, st, scanID)
		}},
		sagemakerPhase{"sagemaker:ListModelExplainabilityJobDefinitions", func() (int, int, error) {
			return scanSageMakerModelExplainabilityJobDefinitions(ctx, client, acct, region, st, scanID)
		}},
		sagemakerPhase{"sagemaker:ListModelQualityJobDefinitions", func() (int, int, error) {
			return scanSageMakerModelQualityJobDefinitions(ctx, client, acct, region, st, scanID)
		}},
		sagemakerPhase{"sagemaker:ListMonitoringAlerts", func() (int, int, error) { return scanSageMakerMonitoringAlerts(ctx, client, acct, region, st, scanID) }},
	)
}

func scanSageMakerMonitoringSchedules(ctx context.Context, client sagemakerMonitoringAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return sagemakerDescribe[smtypes.MonitoringScheduleSummary]{
		listOp: "sagemaker:ListMonitoringSchedules", describeOp: "sagemaker:DescribeMonitoringSchedule", notFound: []string{"ResourceNotFound"},
		list: func(ctx context.Context, token *string) ([]smtypes.MonitoringScheduleSummary, *string, error) {
			out, err := client.ListMonitoringSchedules(ctx, &sagemaker.ListMonitoringSchedulesInput{MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.MonitoringScheduleSummaries, out.NextToken, nil
		},
		name: func(s smtypes.MonitoringScheduleSummary) string { return sv(s.MonitoringScheduleName) },
		listed: func(s smtypes.MonitoringScheduleSummary) *store.Resource {
			return sagemakerListedRow(acct, region, scanID, TypeSageMakerMonitoringSchedule, s.MonitoringScheduleArn, s.MonitoringScheduleName, s.CreationTime, string(s.MonitoringScheduleStatus), s)
		},
		describe: func(ctx context.Context, s smtypes.MonitoringScheduleSummary) (*store.Resource, error) {
			name := sv(s.MonitoringScheduleName)
			out, err := client.DescribeMonitoringSchedule(ctx, &sagemaker.DescribeMonitoringScheduleInput{MonitoringScheduleName: &name})
			if err != nil {
				return nil, err
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
		},
	}.scan(ctx, st, acct, region, sagemakerStoreTagged(ctx, client, acct, region, st, "sagemaker:ListMonitoringSchedules"))
}

func scanSageMakerDataQualityJobDefinitions(ctx context.Context, client sagemakerMonitoringAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return sagemakerDescribe[smtypes.MonitoringJobDefinitionSummary]{
		listOp: "sagemaker:ListDataQualityJobDefinitions", describeOp: "sagemaker:DescribeDataQualityJobDefinition", notFound: []string{"ResourceNotFound"},
		list: func(ctx context.Context, token *string) ([]smtypes.MonitoringJobDefinitionSummary, *string, error) {
			out, err := client.ListDataQualityJobDefinitions(ctx, &sagemaker.ListDataQualityJobDefinitionsInput{MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.JobDefinitionSummaries, out.NextToken, nil
		},
		name: func(s smtypes.MonitoringJobDefinitionSummary) string { return sv(s.MonitoringJobDefinitionName) },
		listed: func(s smtypes.MonitoringJobDefinitionSummary) *store.Resource {
			return sagemakerListedRow(acct, region, scanID, TypeSageMakerDataQualityJobDefinition, s.MonitoringJobDefinitionArn, s.MonitoringJobDefinitionName, s.CreationTime, "", s)
		},
		describe: func(ctx context.Context, s smtypes.MonitoringJobDefinitionSummary) (*store.Resource, error) {
			name := sv(s.MonitoringJobDefinitionName)
			out, err := client.DescribeDataQualityJobDefinition(ctx, &sagemaker.DescribeDataQualityJobDefinitionInput{JobDefinitionName: &name})
			if err != nil {
				return nil, err
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
		},
	}.scan(ctx, st, acct, region, sagemakerStoreTagged(ctx, client, acct, region, st, "sagemaker:ListDataQualityJobDefinitions"))
}

func scanSageMakerModelBiasJobDefinitions(ctx context.Context, client sagemakerMonitoringAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return sagemakerDescribe[smtypes.MonitoringJobDefinitionSummary]{
		listOp: "sagemaker:ListModelBiasJobDefinitions", describeOp: "sagemaker:DescribeModelBiasJobDefinition", notFound: []string{"ResourceNotFound"},
		list: func(ctx context.Context, token *string) ([]smtypes.MonitoringJobDefinitionSummary, *string, error) {
			out, err := client.ListModelBiasJobDefinitions(ctx, &sagemaker.ListModelBiasJobDefinitionsInput{MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.JobDefinitionSummaries, out.NextToken, nil
		},
		name: func(s smtypes.MonitoringJobDefinitionSummary) string { return sv(s.MonitoringJobDefinitionName) },
		listed: func(s smtypes.MonitoringJobDefinitionSummary) *store.Resource {
			return sagemakerListedRow(acct, region, scanID, TypeSageMakerModelBiasJobDefinition, s.MonitoringJobDefinitionArn, s.MonitoringJobDefinitionName, s.CreationTime, "", s)
		},
		describe: func(ctx context.Context, s smtypes.MonitoringJobDefinitionSummary) (*store.Resource, error) {
			name := sv(s.MonitoringJobDefinitionName)
			out, err := client.DescribeModelBiasJobDefinition(ctx, &sagemaker.DescribeModelBiasJobDefinitionInput{JobDefinitionName: &name})
			if err != nil {
				return nil, err
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
		},
	}.scan(ctx, st, acct, region, sagemakerStoreTagged(ctx, client, acct, region, st, "sagemaker:ListModelBiasJobDefinitions"))
}

func scanSageMakerModelExplainabilityJobDefinitions(ctx context.Context, client sagemakerMonitoringAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return sagemakerDescribe[smtypes.MonitoringJobDefinitionSummary]{
		listOp: "sagemaker:ListModelExplainabilityJobDefinitions", describeOp: "sagemaker:DescribeModelExplainabilityJobDefinition", notFound: []string{"ResourceNotFound"},
		list: func(ctx context.Context, token *string) ([]smtypes.MonitoringJobDefinitionSummary, *string, error) {
			out, err := client.ListModelExplainabilityJobDefinitions(ctx, &sagemaker.ListModelExplainabilityJobDefinitionsInput{MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.JobDefinitionSummaries, out.NextToken, nil
		},
		name: func(s smtypes.MonitoringJobDefinitionSummary) string { return sv(s.MonitoringJobDefinitionName) },
		listed: func(s smtypes.MonitoringJobDefinitionSummary) *store.Resource {
			return sagemakerListedRow(acct, region, scanID, TypeSageMakerModelExplainabilityJobDefinition, s.MonitoringJobDefinitionArn, s.MonitoringJobDefinitionName, s.CreationTime, "", s)
		},
		describe: func(ctx context.Context, s smtypes.MonitoringJobDefinitionSummary) (*store.Resource, error) {
			name := sv(s.MonitoringJobDefinitionName)
			out, err := client.DescribeModelExplainabilityJobDefinition(ctx, &sagemaker.DescribeModelExplainabilityJobDefinitionInput{JobDefinitionName: &name})
			if err != nil {
				return nil, err
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
		},
	}.scan(ctx, st, acct, region, sagemakerStoreTagged(ctx, client, acct, region, st, "sagemaker:ListModelExplainabilityJobDefinitions"))
}

func scanSageMakerModelQualityJobDefinitions(ctx context.Context, client sagemakerMonitoringAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return sagemakerDescribe[smtypes.MonitoringJobDefinitionSummary]{
		listOp: "sagemaker:ListModelQualityJobDefinitions", describeOp: "sagemaker:DescribeModelQualityJobDefinition", notFound: []string{"ResourceNotFound"},
		list: func(ctx context.Context, token *string) ([]smtypes.MonitoringJobDefinitionSummary, *string, error) {
			out, err := client.ListModelQualityJobDefinitions(ctx, &sagemaker.ListModelQualityJobDefinitionsInput{MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.JobDefinitionSummaries, out.NextToken, nil
		},
		name: func(s smtypes.MonitoringJobDefinitionSummary) string { return sv(s.MonitoringJobDefinitionName) },
		listed: func(s smtypes.MonitoringJobDefinitionSummary) *store.Resource {
			return sagemakerListedRow(acct, region, scanID, TypeSageMakerModelQualityJobDefinition, s.MonitoringJobDefinitionArn, s.MonitoringJobDefinitionName, s.CreationTime, "", s)
		},
		describe: func(ctx context.Context, s smtypes.MonitoringJobDefinitionSummary) (*store.Resource, error) {
			name := sv(s.MonitoringJobDefinitionName)
			out, err := client.DescribeModelQualityJobDefinition(ctx, &sagemaker.DescribeModelQualityJobDefinitionInput{JobDefinitionName: &name})
			if err != nil {
				return nil, err
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
		},
	}.scan(ctx, st, acct, region, sagemakerStoreTagged(ctx, client, acct, region, st, "sagemaker:ListModelQualityJobDefinitions"))
}

func scanSageMakerMonitoringAlerts(ctx context.Context, client sagemakerMonitoringAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return scanSageMakerChildren(ctx, acct, region, st, scanID, TypeSageMakerMonitoringSchedule, "sagemaker:ListMonitoringAlerts", []string{"ResourceNotFound"},
		sagemakerChildSkips{emptyWhy: "monitoring alerts have an empty name", namelessWhat: "stored monitoring schedule(s)"},
		func(ctx context.Context, schedule store.Resource) []sagemakerChildList[*store.Resource] {
			if sv(schedule.Name) == "" {
				return []sagemakerChildList[*store.Resource]{{key: schedule.NativeID, nameless: true}}
			}
			alerts, err := readTokenPages(ctx, func(ctx context.Context, token *string) ([]smtypes.MonitoringAlertSummary, *string, error) {
				out, err := client.ListMonitoringAlerts(ctx, &sagemaker.ListMonitoringAlertsInput{
					MonitoringScheduleName: schedule.Name, MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token,
				})
				if err != nil {
					return nil, nil, err
				}
				return out.MonitoringAlertSummaries, out.NextToken, nil
			})
			l := sagemakerChildList[*store.Resource]{key: schedule.NativeID, err: err}
			for i, a := range alerts {
				name := sv(a.MonitoringAlertName)
				if name == "" {
					l.skipped = append(l.skipped, fmt.Sprintf("%s #%d", schedule.NativeID, i+1))
					continue
				}
				res := &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeSageMakerMonitoringAlert,
					NativeID:       schedule.NativeID + "/alert/" + name,
					Name:           &name,
					Region:         &region,
					CreatedAt:      tp(a.CreationTime),
					AttributesJSON: mustJSON(a),
					DiscoveredBy:   scanID,
				}
				if status := string(a.AlertStatus); status != "" {
					res.Status = &status
				}
				l.items = append(l.items, res)
			}
			return []sagemakerChildList[*store.Resource]{l}
		})
}
