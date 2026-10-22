package aws

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/accessanalyzer"
	aatypes "github.com/aws/aws-sdk-go-v2/service/accessanalyzer/types"
)

// TestScanAccessAnalyzerAnalyzers_ReadsTags pins that an analyzer row carries
// the Tags of its ListAnalyzers element, and that an untagged analyzer stores
// none.
func TestScanAccessAnalyzerAnalyzers_ReadsTags(t *testing.T) {
	const (
		tagged   = "arn:aws:access-analyzer:us-east-1:123456789012:analyzer/a1"
		untagged = "arn:aws:access-analyzer:us-east-1:123456789012:analyzer/a2"
	)
	handlers := map[string]func(in any) (any, error){
		"ListAnalyzers": func(any) (any, error) {
			return &accessanalyzer.ListAnalyzersOutput{Analyzers: []aatypes.AnalyzerSummary{
				{Arn: ptrStr(tagged), Name: ptrStr("a1"), Tags: map[string]string{"team": "security"}},
				{Arn: ptrStr(untagged), Name: ptrStr("a2")},
			}}, nil
		},
	}
	st := newTestStore(t)
	client := accessanalyzer.NewFromConfig(opStubConfig(handlers, errors.New("unexpected op")))
	total, inserted, refs, err := scanAccessAnalyzerAnalyzers(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanAccessAnalyzerAnalyzers: %v", err)
	}
	if total != 2 || inserted != 2 || len(refs) != 2 {
		t.Errorf("total=%d inserted=%d refs=%d, want 2/2/2", total, inserted, len(refs))
	}

	rows := rowsByNativeID(t, st, TypeAccessAnalyzerAnalyzer)
	for _, arn := range []string{tagged, untagged} {
		if _, ok := rows[arn]; !ok {
			t.Fatalf("analyzer %s not stored", arn)
		}
	}
	assertTagsMap(t, rows[tagged].TagsJSON, map[string]string{"team": "security"})
	if got := rows[untagged].TagsJSON; got != nil {
		t.Errorf("untagged analyzer TagsJSON = %q, want nil", *got)
	}
}
