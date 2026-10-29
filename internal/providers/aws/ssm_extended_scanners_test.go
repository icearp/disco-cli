package aws

import (
	"context"
	"strings"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

// stubSSMHybrid serves DescribeActivations pages in order; every other
// ssmAPI method panics via the nil embedded interface.
type stubSSMHybrid struct {
	ssmAPI
	activationPages []*ssm.DescribeActivationsOutput
	activationIns   []*ssm.DescribeActivationsInput
}

func (s *stubSSMHybrid) DescribeActivations(_ context.Context, in *ssm.DescribeActivationsInput, _ ...func(*ssm.Options)) (*ssm.DescribeActivationsOutput, error) {
	s.activationIns = append(s.activationIns, in)
	return s.activationPages[len(s.activationIns)-1], nil
}

func listSSMRows(t *testing.T, st *store.Store, rtype string) map[string]store.Resource {
	t.Helper()
	rows, err := st.ListResources(store.ResourceFilter{Types: []string{rtype}, Limit: util.AllResources, IncludeManaged: true})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	byID := make(map[string]store.Resource, len(rows))
	for _, r := range rows {
		byID[r.NativeID] = r
	}
	return byID
}

func TestScanSSMActivations_PaginatesAndStores(t *testing.T) {
	const (
		arn1 = "arn:aws:ssm:us-east-1:123456789012:activation/act-1"
		arn2 = "arn:aws:ssm:us-east-1:123456789012:activation/act-2"
	)
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	stub := &stubSSMHybrid{activationPages: []*ssm.DescribeActivationsOutput{
		{
			ActivationList: []ssmtypes.Activation{
				{
					ActivationId: sdkaws.String("act-1"), CreatedDate: &created,
					IamRole: sdkaws.String("SSMServiceRole"),
					Tags:    []ssmtypes.Tag{{Key: sdkaws.String("env"), Value: sdkaws.String("prod")}},
				},
				{IamRole: sdkaws.String("no-id")},
			},
			NextToken: sdkaws.String("page2"),
		},
		{ActivationList: []ssmtypes.Activation{{ActivationId: sdkaws.String("act-2"), Expired: true}}},
	}}
	st := newTestStore(t)
	warns := rhWarnRecorder(st)

	total, _, err := scanSSMActivations(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanSSMActivations: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2 (empty-id element skipped)", total)
	}
	if w := warns(); len(w) != 1 || w[0].Service != ssmDescribeActivationsOp {
		t.Errorf("warnings = %v; want one under %s for the activation without an id", w, ssmDescribeActivationsOp)
	}
	if len(stub.activationIns) != 2 || sv(stub.activationIns[1].NextToken) != "page2" {
		t.Fatalf("DescribeActivations inputs = %+v, want 2 calls, second with NextToken page2", stub.activationIns)
	}

	byID := listSSMRows(t, st, TypeSSMActivation)
	if len(byID) != 2 {
		t.Fatalf("stored %d rows, want 2: %v", len(byID), byID)
	}
	r1, ok := byID[arn1]
	if !ok {
		t.Fatalf("row %s missing", arn1)
	}
	if r1.Region == nil || *r1.Region != testRegion {
		t.Errorf("Region = %v, want %s", r1.Region, testRegion)
	}
	if sv(r1.Name) != "act-1" {
		t.Errorf("Name = %q, want act-1", sv(r1.Name))
	}
	if sv(r1.Status) != "active" {
		t.Errorf("Status = %q, want active", sv(r1.Status))
	}
	if sv(r1.CreatedAt) != created.Format(time.RFC3339) {
		t.Errorf("CreatedAt = %q, want %s", sv(r1.CreatedAt), created.Format(time.RFC3339))
	}
	if !strings.Contains(r1.AttributesJSON, `"IamRole":"SSMServiceRole"`) {
		t.Errorf("attrs missing IamRole: %s", r1.AttributesJSON)
	}
	if sv(r1.TagsJSON) != `{"env":"prod"}` {
		t.Errorf("TagsJSON = %q, want {\"env\":\"prod\"}", sv(r1.TagsJSON))
	}
	r2, ok := byID[arn2]
	if !ok {
		t.Fatalf("row %s missing (second page)", arn2)
	}
	if sv(r2.Status) != "expired" {
		t.Errorf("Status = %q, want expired", sv(r2.Status))
	}
	if r2.TagsJSON != nil {
		t.Errorf("TagsJSON = %q, want unset for an untagged activation", *r2.TagsJSON)
	}
}
