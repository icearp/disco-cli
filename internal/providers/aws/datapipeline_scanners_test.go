package aws

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/datapipeline"
	dptypes "github.com/aws/aws-sdk-go-v2/service/datapipeline/types"
	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

type stubDataPipeline struct {
	ids  []dptypes.PipelineIdName
	desc []dptypes.PipelineDescription
}

func (s *stubDataPipeline) ListPipelines(_ context.Context, _ *datapipeline.ListPipelinesInput, _ ...func(*datapipeline.Options)) (*datapipeline.ListPipelinesOutput, error) {
	return &datapipeline.ListPipelinesOutput{PipelineIdList: s.ids}, nil
}

func (s *stubDataPipeline) DescribePipelines(_ context.Context, _ *datapipeline.DescribePipelinesInput, _ ...func(*datapipeline.Options)) (*datapipeline.DescribePipelinesOutput, error) {
	return &datapipeline.DescribePipelinesOutput{PipelineDescriptionList: s.desc}, nil
}

func TestScanDataPipelinePipelines(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	stub := &stubDataPipeline{
		ids:  []dptypes.PipelineIdName{{Id: ptrStr("df-1"), Name: ptrStr("nightly")}},
		desc: []dptypes.PipelineDescription{{PipelineId: ptrStr("df-1"), Name: ptrStr("nightly")}},
	}
	total, _, err := scanDataPipelinePipelines(context.Background(), stub, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanDataPipelinePipelines: %v", err)
	}
	if total != 1 {
		t.Fatalf("total=%d, want 1", total)
	}
	rows, err := st.ListResources(store.ResourceFilter{Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeDataPipelinePipeline}, Limit: util.AllResources})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	want := "arn:aws:datapipeline:" + testRegion + ":" + acct.ID + ":pipeline/df-1"
	if len(rows) != 1 || rows[0].NativeID != want {
		t.Errorf("rows=%+v, want one with NativeID %s", rows, want)
	}
}

func TestScanDataPipelinePipelines_None(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	total, inserted, err := scanDataPipelinePipelines(context.Background(), &stubDataPipeline{}, acct, testRegion, st, testScanID)
	if err != nil || total != 0 || inserted != 0 {
		t.Errorf("got (%d,%d,%v), want (0,0,nil)", total, inserted, err)
	}
}
