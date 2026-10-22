package aws

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dlm"
	dlmtypes "github.com/aws/aws-sdk-go-v2/service/dlm/types"
)

// TestScanDLM_ReadsTags pins that a lifecycle-policy row carries the Tags of
// its GetLifecyclePolicies summary, and that an untagged policy stores none.
func TestScanDLM_ReadsTags(t *testing.T) {
	handlers := map[string]func(in any) (any, error){
		"GetLifecyclePolicies": func(any) (any, error) {
			return &dlm.GetLifecyclePoliciesOutput{Policies: []dlmtypes.LifecyclePolicySummary{
				{PolicyId: ptrStr("policy-1"), State: dlmtypes.GettablePolicyStateValuesEnabled, Tags: map[string]string{"team": "backup"}},
				{PolicyId: ptrStr("policy-2"), State: dlmtypes.GettablePolicyStateValuesEnabled},
			}}, nil
		},
	}
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, errors.New("unexpected op"))
	total, inserted, err := scanDLM(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanDLM: %v", err)
	}
	if total != 2 || inserted != 2 {
		t.Errorf("total=%d inserted=%d, want 2/2", total, inserted)
	}

	tagged := "arn:aws:dlm:" + testRegion + ":" + testAccountID + ":policy/policy-1"
	untagged := "arn:aws:dlm:" + testRegion + ":" + testAccountID + ":policy/policy-2"
	rows := rowsByNativeID(t, st, TypeDLMLifecyclePolicy)
	for _, arn := range []string{tagged, untagged} {
		if _, ok := rows[arn]; !ok {
			t.Fatalf("lifecycle policy %s not stored", arn)
		}
	}
	assertTagsMap(t, rows[tagged].TagsJSON, map[string]string{"team": "backup"})
	if got := rows[untagged].TagsJSON; got != nil {
		t.Errorf("untagged lifecycle policy TagsJSON = %q, want nil", *got)
	}
}
