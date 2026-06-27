package aws

import (
	"context"
	"testing"
	"time"

	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/applicationsignals"
	applicationsignalstypes "github.com/aws/aws-sdk-go-v2/service/applicationsignals/types"
)

// stubApplicationSignals is a deterministic in-memory replacement for
// applicationSignalsAPI used by the scanner tests.
type stubApplicationSignals struct {
	sloPages      []*applicationsignals.ListServiceLevelObjectivesOutput
	sloIdx        int
	groupingPages []*applicationsignals.ListGroupingAttributeDefinitionsOutput
	groupingIdx   int
	sloErr        error
	groupingErr   error
}

func (s *stubApplicationSignals) ListServiceLevelObjectives(_ context.Context, _ *applicationsignals.ListServiceLevelObjectivesInput, _ ...func(*applicationsignals.Options)) (*applicationsignals.ListServiceLevelObjectivesOutput, error) {
	if s.sloErr != nil {
		return nil, s.sloErr
	}
	if s.sloIdx >= len(s.sloPages) {
		return &applicationsignals.ListServiceLevelObjectivesOutput{}, nil
	}
	out := s.sloPages[s.sloIdx]
	s.sloIdx++
	return out, nil
}

func (s *stubApplicationSignals) ListGroupingAttributeDefinitions(_ context.Context, _ *applicationsignals.ListGroupingAttributeDefinitionsInput, _ ...func(*applicationsignals.Options)) (*applicationsignals.ListGroupingAttributeDefinitionsOutput, error) {
	if s.groupingErr != nil {
		return nil, s.groupingErr
	}
	if s.groupingIdx >= len(s.groupingPages) {
		return &applicationsignals.ListGroupingAttributeDefinitionsOutput{}, nil
	}
	out := s.groupingPages[s.groupingIdx]
	s.groupingIdx++
	return out, nil
}

func TestApplicationSignalsGroupingConfigurationNativeID(t *testing.T) {
	got := applicationSignalsGroupingConfigurationNativeID("us-east-1", "111111111111")
	want := "arn:aws:application-signals:us-east-1:111111111111:grouping-configuration"
	if got != want {
		t.Errorf("native id = %q, want %q", got, want)
	}
}

func TestScanApplicationSignalsGroupingConfiguration_Populated(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := testRegion

	gname := "BusinessUnit"
	updated := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	stub := &stubApplicationSignals{
		groupingPages: []*applicationsignals.ListGroupingAttributeDefinitionsOutput{{
			GroupingAttributeDefinitions: []applicationsignalstypes.GroupingAttributeDefinition{{
				GroupingName:       &gname,
				GroupingSourceKeys: []string{"team"},
			}},
			UpdatedAt: &updated,
		}},
	}
	total, inserted, err := scanApplicationSignalsGroupingConfiguration(context.Background(), stub, acct, region, st, testScanID)
	if err != nil {
		t.Fatalf("scanApplicationSignalsGroupingConfiguration: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Errorf("total=%d inserted=%d, want 1/1", total, inserted)
	}
	want := applicationSignalsGroupingConfigurationNativeID(region, acct.ID)
	rows, err := st.ListResources(store.ResourceFilter{Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeApplicationSignalsGroupingConfiguration}, Limit: 100})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(rows) != 1 || rows[0].NativeID != want {
		t.Errorf("rows=%+v, want one row with NativeID %s", rows, want)
	}
}

func TestScanApplicationSignalsGroupingConfiguration_Empty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	stub := &stubApplicationSignals{}
	total, inserted, err := scanApplicationSignalsGroupingConfiguration(context.Background(), stub, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanApplicationSignalsGroupingConfiguration: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Errorf("total=%d inserted=%d, want 0/0 (skip empty)", total, inserted)
	}
	rows, err := st.ListResources(store.ResourceFilter{Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeApplicationSignalsGroupingConfiguration}, Limit: 100})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected no rows; got %+v", rows)
	}
}
