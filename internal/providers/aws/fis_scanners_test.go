package aws

import (
	"context"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/fis"
	fistypes "github.com/aws/aws-sdk-go-v2/service/fis/types"
	"github.com/aws/smithy-go"
)

func TestScanFISExperimentTemplates_ReadsListedTags(t *testing.T) {
	const (
		tagged   = "arn:aws:fis:us-east-1:123456789012:experiment-template/EXT1"
		untagged = "arn:aws:fis:us-east-1:123456789012:experiment-template/EXT2"
	)
	st := newTestStore(t)
	client := fis.NewFromConfig(opStubConfig(map[string]func(any) (any, error){
		"ListExperimentTemplates": func(any) (any, error) {
			return &fis.ListExperimentTemplatesOutput{ExperimentTemplates: []fistypes.ExperimentTemplateSummary{
				{Arn: sdkaws.String(tagged), Id: sdkaws.String("EXT1"), Tags: map[string]string{"env": "prod"}},
				{Arn: sdkaws.String(untagged), Id: sdkaws.String("EXT2")},
			}}, nil
		},
	}, &smithy.GenericAPIError{Code: "AccessDeniedException"}))

	_, total, inserted, err := scanFISExperimentTemplates(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 2 || inserted != 2 {
		t.Errorf("total, inserted = %d, %d; want 2, 2", total, inserted)
	}
	rows := rowsByNativeID(t, st, TypeFISExperimentTemplate)
	assertTagsMap(t, rows[tagged].TagsJSON, map[string]string{"env": "prod"})
	if got := rows[untagged].TagsJSON; got != nil {
		t.Errorf("untagged template TagsJSON = %q, want nil", *got)
	}
}
