package aws

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/savingsplans"
	savingsplanstypes "github.com/aws/aws-sdk-go-v2/service/savingsplans/types"
)

// TestScanSavingsPlans_ReadsTags pins that a savings-plan row carries its
// listed element's Tags, and that an untagged plan stores none.
func TestScanSavingsPlans_ReadsTags(t *testing.T) {
	const (
		tagged   = "arn:aws:savingsplans::123456789012:savingsplan/sp-1"
		untagged = "arn:aws:savingsplans::123456789012:savingsplan/sp-2"
	)
	handlers := map[string]func(in any) (any, error){
		"DescribeSavingsPlans": func(any) (any, error) {
			return &savingsplans.DescribeSavingsPlansOutput{SavingsPlans: []savingsplanstypes.SavingsPlan{
				{SavingsPlanArn: ptrStr(tagged), SavingsPlanId: ptrStr("sp-1"), Tags: map[string]string{"cost-center": "42"}},
				{SavingsPlanArn: ptrStr(untagged), SavingsPlanId: ptrStr("sp-2")},
			}}, nil
		},
	}
	st := newTestStore(t)
	client := savingsplans.NewFromConfig(opStubConfig(handlers, errors.New("unexpected op")))
	total, _, err := scanSavingsPlans(context.Background(), client, newTestAccount(testAccountID), st, testScanID)
	if err != nil {
		t.Fatalf("scanSavingsPlans: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}

	rows := rowsByNativeID(t, st, TypeSavingsPlansSavingsPlan)
	assertTagsMap(t, rows[tagged].TagsJSON, map[string]string{"cost-center": "42"})
	if got := rows[untagged].TagsJSON; got != nil {
		t.Errorf("untagged plan TagsJSON = %q, want nil", *got)
	}
}
