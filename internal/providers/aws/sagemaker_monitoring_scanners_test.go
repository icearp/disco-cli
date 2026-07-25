package aws

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/sagemaker"
	smtypes "github.com/aws/aws-sdk-go-v2/service/sagemaker/types"
	"github.com/icearp/disco-cli/store"
)

type stubSageMakerMonitoring struct {
	schedules   []smtypes.MonitoringScheduleSummary
	scheduleOut map[string]*sagemaker.DescribeMonitoringScheduleOutput
	dataQ       []smtypes.MonitoringJobDefinitionSummary
	dataQOut    map[string]*sagemaker.DescribeDataQualityJobDefinitionOutput
	bias        []smtypes.MonitoringJobDefinitionSummary
	biasOut     map[string]*sagemaker.DescribeModelBiasJobDefinitionOutput
	expl        []smtypes.MonitoringJobDefinitionSummary
	explOut     map[string]*sagemaker.DescribeModelExplainabilityJobDefinitionOutput
	quality     []smtypes.MonitoringJobDefinitionSummary
	qualityOut  map[string]*sagemaker.DescribeModelQualityJobDefinitionOutput
}

func (s *stubSageMakerMonitoring) ListMonitoringSchedules(_ context.Context, _ *sagemaker.ListMonitoringSchedulesInput, _ ...func(*sagemaker.Options)) (*sagemaker.ListMonitoringSchedulesOutput, error) {
	return &sagemaker.ListMonitoringSchedulesOutput{MonitoringScheduleSummaries: s.schedules}, nil
}

func (s *stubSageMakerMonitoring) DescribeMonitoringSchedule(_ context.Context, in *sagemaker.DescribeMonitoringScheduleInput, _ ...func(*sagemaker.Options)) (*sagemaker.DescribeMonitoringScheduleOutput, error) {
	return s.scheduleOut[*in.MonitoringScheduleName], nil
}

func (s *stubSageMakerMonitoring) ListDataQualityJobDefinitions(_ context.Context, _ *sagemaker.ListDataQualityJobDefinitionsInput, _ ...func(*sagemaker.Options)) (*sagemaker.ListDataQualityJobDefinitionsOutput, error) {
	return &sagemaker.ListDataQualityJobDefinitionsOutput{JobDefinitionSummaries: s.dataQ}, nil
}

func (s *stubSageMakerMonitoring) DescribeDataQualityJobDefinition(_ context.Context, in *sagemaker.DescribeDataQualityJobDefinitionInput, _ ...func(*sagemaker.Options)) (*sagemaker.DescribeDataQualityJobDefinitionOutput, error) {
	return s.dataQOut[*in.JobDefinitionName], nil
}

func (s *stubSageMakerMonitoring) ListModelBiasJobDefinitions(_ context.Context, _ *sagemaker.ListModelBiasJobDefinitionsInput, _ ...func(*sagemaker.Options)) (*sagemaker.ListModelBiasJobDefinitionsOutput, error) {
	return &sagemaker.ListModelBiasJobDefinitionsOutput{JobDefinitionSummaries: s.bias}, nil
}

func (s *stubSageMakerMonitoring) DescribeModelBiasJobDefinition(_ context.Context, in *sagemaker.DescribeModelBiasJobDefinitionInput, _ ...func(*sagemaker.Options)) (*sagemaker.DescribeModelBiasJobDefinitionOutput, error) {
	return s.biasOut[*in.JobDefinitionName], nil
}

func (s *stubSageMakerMonitoring) ListModelExplainabilityJobDefinitions(_ context.Context, _ *sagemaker.ListModelExplainabilityJobDefinitionsInput, _ ...func(*sagemaker.Options)) (*sagemaker.ListModelExplainabilityJobDefinitionsOutput, error) {
	return &sagemaker.ListModelExplainabilityJobDefinitionsOutput{JobDefinitionSummaries: s.expl}, nil
}

func (s *stubSageMakerMonitoring) DescribeModelExplainabilityJobDefinition(_ context.Context, in *sagemaker.DescribeModelExplainabilityJobDefinitionInput, _ ...func(*sagemaker.Options)) (*sagemaker.DescribeModelExplainabilityJobDefinitionOutput, error) {
	return s.explOut[*in.JobDefinitionName], nil
}

func (s *stubSageMakerMonitoring) ListModelQualityJobDefinitions(_ context.Context, _ *sagemaker.ListModelQualityJobDefinitionsInput, _ ...func(*sagemaker.Options)) (*sagemaker.ListModelQualityJobDefinitionsOutput, error) {
	return &sagemaker.ListModelQualityJobDefinitionsOutput{JobDefinitionSummaries: s.quality}, nil
}

func (s *stubSageMakerMonitoring) DescribeModelQualityJobDefinition(_ context.Context, in *sagemaker.DescribeModelQualityJobDefinitionInput, _ ...func(*sagemaker.Options)) (*sagemaker.DescribeModelQualityJobDefinitionOutput, error) {
	return s.qualityOut[*in.JobDefinitionName], nil
}

func TestScanSageMakerMonitoring(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	now := time.Unix(1700000000, 0).UTC()

	schedName := "sched-1"
	schedARN := fmt.Sprintf("arn:aws:sagemaker:%s:%s:monitoring-schedule/%s", testRegion, acct.ID, schedName)
	dqName := "dq-1"
	dqARN := fmt.Sprintf("arn:aws:sagemaker:%s:%s:data-quality-job-definition/%s", testRegion, acct.ID, dqName)
	biasName := "bias-1"
	biasARN := fmt.Sprintf("arn:aws:sagemaker:%s:%s:model-bias-job-definition/%s", testRegion, acct.ID, biasName)
	explName := "expl-1"
	explARN := fmt.Sprintf("arn:aws:sagemaker:%s:%s:model-explainability-job-definition/%s", testRegion, acct.ID, explName)
	qName := "q-1"
	qARN := fmt.Sprintf("arn:aws:sagemaker:%s:%s:model-quality-job-definition/%s", testRegion, acct.ID, qName)

	stub := &stubSageMakerMonitoring{
		schedules: []smtypes.MonitoringScheduleSummary{{MonitoringScheduleArn: &schedARN, MonitoringScheduleName: &schedName, CreationTime: &now}},
		scheduleOut: map[string]*sagemaker.DescribeMonitoringScheduleOutput{
			schedName: {MonitoringScheduleArn: &schedARN, MonitoringScheduleName: &schedName, MonitoringScheduleStatus: smtypes.ScheduleStatusScheduled, CreationTime: &now},
		},
		dataQ: []smtypes.MonitoringJobDefinitionSummary{{MonitoringJobDefinitionArn: &dqARN, MonitoringJobDefinitionName: &dqName, CreationTime: &now}},
		dataQOut: map[string]*sagemaker.DescribeDataQualityJobDefinitionOutput{
			dqName: {JobDefinitionArn: &dqARN, JobDefinitionName: &dqName, CreationTime: &now},
		},
		bias: []smtypes.MonitoringJobDefinitionSummary{{MonitoringJobDefinitionArn: &biasARN, MonitoringJobDefinitionName: &biasName, CreationTime: &now}},
		biasOut: map[string]*sagemaker.DescribeModelBiasJobDefinitionOutput{
			biasName: {JobDefinitionArn: &biasARN, JobDefinitionName: &biasName, CreationTime: &now},
		},
		expl: []smtypes.MonitoringJobDefinitionSummary{{MonitoringJobDefinitionArn: &explARN, MonitoringJobDefinitionName: &explName, CreationTime: &now}},
		explOut: map[string]*sagemaker.DescribeModelExplainabilityJobDefinitionOutput{
			explName: {JobDefinitionArn: &explARN, JobDefinitionName: &explName, CreationTime: &now},
		},
		quality: []smtypes.MonitoringJobDefinitionSummary{{MonitoringJobDefinitionArn: &qARN, MonitoringJobDefinitionName: &qName, CreationTime: &now}},
		qualityOut: map[string]*sagemaker.DescribeModelQualityJobDefinitionOutput{
			qName: {JobDefinitionArn: &qARN, JobDefinitionName: &qName, CreationTime: &now},
		},
	}

	total, inserted, err := scanSageMakerMonitoring(context.Background(), stub, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 5 || inserted != 5 {
		t.Fatalf("total=%d inserted=%d want 5/5", total, inserted)
	}
	for _, want := range []struct{ typ, id string }{
		{TypeSageMakerMonitoringSchedule, schedARN},
		{TypeSageMakerDataQualityJobDefinition, dqARN},
		{TypeSageMakerModelBiasJobDefinition, biasARN},
		{TypeSageMakerModelExplainabilityJobDefinition, explARN},
		{TypeSageMakerModelQualityJobDefinition, qARN},
	} {
		if _, err := st.GetResource(store.ResourceID("aws", acct.ID, want.id)); err != nil {
			t.Errorf("%s missing: %v", want.typ, err)
		}
	}
}

func TestScanSageMakerMonitoringEmpty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	stub := &stubSageMakerMonitoring{}
	total, inserted, err := scanSageMakerMonitoring(context.Background(), stub, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("total=%d inserted=%d want 0/0", total, inserted)
	}
}
