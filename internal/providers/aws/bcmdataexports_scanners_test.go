package aws

import (
	"context"
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/bcmdataexports"
	bcmtypes "github.com/aws/aws-sdk-go-v2/service/bcmdataexports/types"
)

type stubBCM struct {
	refs    []bcmtypes.ExportReference
	exports map[string]*bcmtypes.Export // keyed by ARN
}

func (s *stubBCM) ListExports(_ context.Context, _ *bcmdataexports.ListExportsInput, _ ...func(*bcmdataexports.Options)) (*bcmdataexports.ListExportsOutput, error) {
	return &bcmdataexports.ListExportsOutput{Exports: s.refs}, nil
}

func (s *stubBCM) GetExport(_ context.Context, in *bcmdataexports.GetExportInput, _ ...func(*bcmdataexports.Options)) (*bcmdataexports.GetExportOutput, error) {
	a := ""
	if in.ExportArn != nil {
		a = *in.ExportArn
	}
	return &bcmdataexports.GetExportOutput{Export: s.exports[a]}, nil
}

func TestScanBCMDataExportsExports(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	arn := fmt.Sprintf("arn:aws:bcm-data-exports::%s:export/exp-1", acct.ID)
	name := "monthly-cur"
	bucket := "my-billing-bucket"
	prefix := "exports/"
	regionName := testRegion
	stub := &stubBCM{
		refs: []bcmtypes.ExportReference{{ExportArn: &arn, ExportName: &name}},
		exports: map[string]*bcmtypes.Export{
			arn: {
				ExportArn: &arn,
				Name:      &name,
				DestinationConfigurations: &bcmtypes.DestinationConfigurations{
					S3Destination: &bcmtypes.S3Destination{
						S3Bucket: &bucket,
						S3Prefix: &prefix,
						S3Region: &regionName,
					},
				},
			},
		},
	}
	total, _, err := scanBCMDataExportsExports(context.Background(), stub, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 1 {
		t.Fatalf("total=%d want 1", total)
	}
	if _, err := st.GetResource(store.ResourceID("aws", acct.ID, arn)); err != nil {
		t.Errorf("export missing: %v", err)
	}
}
