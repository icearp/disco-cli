package aws

import (
	"context"
	"fmt"
	"testing"
	"time"

	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/sagemaker"
	smtypes "github.com/aws/aws-sdk-go-v2/service/sagemaker/types"
)

type stubSageMakerInference struct {
	endpoints       []smtypes.EndpointSummary
	endpointOut     map[string]*sagemaker.DescribeEndpointOutput
	endpointConfigs []smtypes.EndpointConfigSummary
	endpointCfgOut  map[string]*sagemaker.DescribeEndpointConfigOutput
	models          []smtypes.ModelSummary
	modelOut        map[string]*sagemaker.DescribeModelOutput
	components      []smtypes.InferenceComponentSummary
	componentOut    map[string]*sagemaker.DescribeInferenceComponentOutput
	experiments     []smtypes.InferenceExperimentSummary
	experimentOut   map[string]*sagemaker.DescribeInferenceExperimentOutput
}

func (s *stubSageMakerInference) ListEndpoints(_ context.Context, _ *sagemaker.ListEndpointsInput, _ ...func(*sagemaker.Options)) (*sagemaker.ListEndpointsOutput, error) {
	return &sagemaker.ListEndpointsOutput{Endpoints: s.endpoints}, nil
}

func (s *stubSageMakerInference) DescribeEndpoint(_ context.Context, in *sagemaker.DescribeEndpointInput, _ ...func(*sagemaker.Options)) (*sagemaker.DescribeEndpointOutput, error) {
	return s.endpointOut[*in.EndpointName], nil
}

func (s *stubSageMakerInference) ListEndpointConfigs(_ context.Context, _ *sagemaker.ListEndpointConfigsInput, _ ...func(*sagemaker.Options)) (*sagemaker.ListEndpointConfigsOutput, error) {
	return &sagemaker.ListEndpointConfigsOutput{EndpointConfigs: s.endpointConfigs}, nil
}

func (s *stubSageMakerInference) DescribeEndpointConfig(_ context.Context, in *sagemaker.DescribeEndpointConfigInput, _ ...func(*sagemaker.Options)) (*sagemaker.DescribeEndpointConfigOutput, error) {
	return s.endpointCfgOut[*in.EndpointConfigName], nil
}

func (s *stubSageMakerInference) ListModels(_ context.Context, _ *sagemaker.ListModelsInput, _ ...func(*sagemaker.Options)) (*sagemaker.ListModelsOutput, error) {
	return &sagemaker.ListModelsOutput{Models: s.models}, nil
}

func (s *stubSageMakerInference) DescribeModel(_ context.Context, in *sagemaker.DescribeModelInput, _ ...func(*sagemaker.Options)) (*sagemaker.DescribeModelOutput, error) {
	return s.modelOut[*in.ModelName], nil
}

func (s *stubSageMakerInference) ListInferenceComponents(_ context.Context, _ *sagemaker.ListInferenceComponentsInput, _ ...func(*sagemaker.Options)) (*sagemaker.ListInferenceComponentsOutput, error) {
	return &sagemaker.ListInferenceComponentsOutput{InferenceComponents: s.components}, nil
}

func (s *stubSageMakerInference) DescribeInferenceComponent(_ context.Context, in *sagemaker.DescribeInferenceComponentInput, _ ...func(*sagemaker.Options)) (*sagemaker.DescribeInferenceComponentOutput, error) {
	return s.componentOut[*in.InferenceComponentName], nil
}

func (s *stubSageMakerInference) ListInferenceExperiments(_ context.Context, _ *sagemaker.ListInferenceExperimentsInput, _ ...func(*sagemaker.Options)) (*sagemaker.ListInferenceExperimentsOutput, error) {
	return &sagemaker.ListInferenceExperimentsOutput{InferenceExperiments: s.experiments}, nil
}

func (s *stubSageMakerInference) DescribeInferenceExperiment(_ context.Context, in *sagemaker.DescribeInferenceExperimentInput, _ ...func(*sagemaker.Options)) (*sagemaker.DescribeInferenceExperimentOutput, error) {
	return s.experimentOut[*in.Name], nil
}

func TestScanSageMakerInference(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	now := time.Unix(1700000000, 0).UTC()

	epName := "ep-1"
	epARN := fmt.Sprintf("arn:aws:sagemaker:%s:%s:endpoint/%s", testRegion, acct.ID, epName)
	cfgName := "cfg-1"
	cfgARN := fmt.Sprintf("arn:aws:sagemaker:%s:%s:endpoint-config/%s", testRegion, acct.ID, cfgName)
	mName := "model-1"
	mARN := fmt.Sprintf("arn:aws:sagemaker:%s:%s:model/%s", testRegion, acct.ID, mName)
	icName := "ic-1"
	icARN := fmt.Sprintf("arn:aws:sagemaker:%s:%s:inference-component/%s", testRegion, acct.ID, icName)
	expName := "exp-1"
	expARN := fmt.Sprintf("arn:aws:sagemaker:%s:%s:inference-experiment/%s", testRegion, acct.ID, expName)

	stub := &stubSageMakerInference{
		endpoints: []smtypes.EndpointSummary{{EndpointArn: &epARN, EndpointName: &epName, CreationTime: &now}},
		endpointOut: map[string]*sagemaker.DescribeEndpointOutput{
			epName: {EndpointArn: &epARN, EndpointName: &epName, EndpointStatus: smtypes.EndpointStatusInService, CreationTime: &now},
		},
		endpointConfigs: []smtypes.EndpointConfigSummary{{EndpointConfigArn: &cfgARN, EndpointConfigName: &cfgName, CreationTime: &now}},
		endpointCfgOut: map[string]*sagemaker.DescribeEndpointConfigOutput{
			cfgName: {EndpointConfigArn: &cfgARN, EndpointConfigName: &cfgName, CreationTime: &now},
		},
		models: []smtypes.ModelSummary{{ModelArn: &mARN, ModelName: &mName, CreationTime: &now}},
		modelOut: map[string]*sagemaker.DescribeModelOutput{
			mName: {ModelArn: &mARN, ModelName: &mName, CreationTime: &now},
		},
		components: []smtypes.InferenceComponentSummary{{InferenceComponentArn: &icARN, InferenceComponentName: &icName, CreationTime: &now}},
		componentOut: map[string]*sagemaker.DescribeInferenceComponentOutput{
			icName: {InferenceComponentArn: &icARN, InferenceComponentName: &icName, InferenceComponentStatus: smtypes.InferenceComponentStatusInService, CreationTime: &now},
		},
		experiments: []smtypes.InferenceExperimentSummary{{Name: &expName, CreationTime: &now}},
		experimentOut: map[string]*sagemaker.DescribeInferenceExperimentOutput{
			expName: {Arn: &expARN, Name: &expName, Status: smtypes.InferenceExperimentStatusRunning, CreationTime: &now},
		},
	}

	total, inserted, err := scanSageMakerInference(context.Background(), stub, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 5 || inserted != 5 {
		t.Fatalf("total=%d inserted=%d want 5/5", total, inserted)
	}
	for _, want := range []struct{ typ, id string }{
		{TypeSageMakerEndpoint, epARN},
		{TypeSageMakerEndpointConfig, cfgARN},
		{TypeSageMakerModel, mARN},
		{TypeSageMakerInferenceComponent, icARN},
		{TypeSageMakerInferenceExperiment, expARN},
	} {
		if _, err := st.GetResource(store.ResourceID("aws", acct.ID, want.typ, want.id)); err != nil {
			t.Errorf("%s missing: %v", want.typ, err)
		}
	}
}

func TestScanSageMakerInferenceEmpty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	stub := &stubSageMakerInference{}
	total, inserted, err := scanSageMakerInference(context.Background(), stub, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("total=%d inserted=%d want 0/0", total, inserted)
	}
}
