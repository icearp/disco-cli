package aws

import (
	"context"
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/connect"
	cttypes "github.com/aws/aws-sdk-go-v2/service/connect/types"
)

type stubConnectDataTable struct {
	tables       []cttypes.DataTableSummary
	attrsByTable map[string][]cttypes.DataTableAttribute
	recsByTable  map[string][]cttypes.RecordPrimaryValue
}

func (s *stubConnectDataTable) ListDataTables(_ context.Context, _ *connect.ListDataTablesInput, _ ...func(*connect.Options)) (*connect.ListDataTablesOutput, error) {
	return &connect.ListDataTablesOutput{DataTableSummaryList: s.tables}, nil
}

func (s *stubConnectDataTable) ListDataTableAttributes(_ context.Context, in *connect.ListDataTableAttributesInput, _ ...func(*connect.Options)) (*connect.ListDataTableAttributesOutput, error) {
	return &connect.ListDataTableAttributesOutput{Attributes: s.attrsByTable[*in.DataTableId]}, nil
}

func (s *stubConnectDataTable) ListDataTablePrimaryValues(_ context.Context, in *connect.ListDataTablePrimaryValuesInput, _ ...func(*connect.Options)) (*connect.ListDataTablePrimaryValuesOutput, error) {
	return &connect.ListDataTablePrimaryValuesOutput{PrimaryValuesList: s.recsByTable[*in.DataTableId]}, nil
}

func TestScanConnectDataTable(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	instID := "11111111-1111-1111-1111-111111111111"
	instARN := fmt.Sprintf("arn:aws:connect:%s:%s:instance/%s", testRegion, acct.ID, instID)
	instances := []cttypes.InstanceSummary{{Id: &instID, Arn: &instARN}}

	tID := "t-1"
	tARN := fmt.Sprintf("arn:aws:connect:%s:%s:instance/%s/data-table/%s", testRegion, acct.ID, instID, tID)
	tName := "tbl"
	attrName := "color"
	attrARN := tARN + "/attribute/" + attrName
	recID := "r-1"
	recARN := tARN + "/record/" + recID

	stub := &stubConnectDataTable{
		tables: []cttypes.DataTableSummary{{Id: &tID, Arn: &tARN, Name: &tName}},
		attrsByTable: map[string][]cttypes.DataTableAttribute{
			tID: {{Name: &attrName, ValueType: cttypes.DataTableAttributeValueTypeText}},
		},
		recsByTable: map[string][]cttypes.RecordPrimaryValue{
			tID: {{RecordId: &recID}},
		},
	}

	total, inserted, err := scanConnectDataTable(context.Background(), stub, instances, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 3 || inserted != 3 {
		t.Fatalf("total=%d inserted=%d want 3/3", total, inserted)
	}
	for _, want := range []struct{ typ, id string }{
		{TypeConnectDataTable, tARN},
		{TypeConnectDataTableAttribute, attrARN},
		{TypeConnectDataTableRecord, recARN},
	} {
		if _, err := st.GetResource(store.ResourceID("aws", acct.ID, want.id)); err != nil {
			t.Errorf("%s missing: %v", want.typ, err)
		}
	}
}

func TestScanConnectDataTableEmpty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	stub := &stubConnectDataTable{}
	total, inserted, err := scanConnectDataTable(context.Background(), stub, nil, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("total=%d inserted=%d want 0/0", total, inserted)
	}
}
