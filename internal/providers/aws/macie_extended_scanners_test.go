package aws

import (
	"context"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/macie2"
	macie2types "github.com/aws/aws-sdk-go-v2/service/macie2/types"
	"github.com/aws/smithy-go"
)

func TestScanMacieFindingsFilters_ReadsListedTags(t *testing.T) {
	const (
		tagged   = "arn:aws:macie2:us-east-1:123456789012:findings-filter/f1"
		untagged = "arn:aws:macie2:us-east-1:123456789012:findings-filter/f2"
	)
	st := newTestStore(t)
	upsertTestResource(t, st, "aws", testAccountID, TypeMacieSession, macieSessionNativeID(testAccountID, testRegion), testRegion, "{}")
	client := macie2.NewFromConfig(opStubConfig(map[string]func(any) (any, error){
		"ListFindingsFilters": func(any) (any, error) {
			return &macie2.ListFindingsFiltersOutput{FindingsFilterListItems: []macie2types.FindingsFilterListItem{
				{Arn: sdkaws.String(tagged), Name: sdkaws.String("f1"), Action: macie2types.FindingsFilterActionArchive, Tags: map[string]string{"owner": "sec"}},
				{Arn: sdkaws.String(untagged), Name: sdkaws.String("f2"), Action: macie2types.FindingsFilterActionNoop},
			}}, nil
		},
	}, &smithy.GenericAPIError{Code: "AccessDeniedException"}))

	total, inserted, err := scanMacieFindingsFilters(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 2 || inserted != 2 {
		t.Errorf("total, inserted = %d, %d; want 2, 2", total, inserted)
	}
	rows := rowsByNativeID(t, st, TypeMacieFindingsFilter)
	assertTagsMap(t, rows[tagged].TagsJSON, map[string]string{"owner": "sec"})
	if got := rows[untagged].TagsJSON; got != nil {
		t.Errorf("untagged filter TagsJSON = %q, want nil", *got)
	}
	assertContainsNative(t, st, macieSessionNativeID(testAccountID, testRegion), tagged)
}
