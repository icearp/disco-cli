package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/polly"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypePollyLexicon, Service: "polly", Leaf: true})
	registerService(serviceEntry{
		name: "aws:polly",
		fn:   scanPolly,
	})
}

type pollyAPI interface {
	ListLexicons(context.Context, *polly.ListLexiconsInput, ...func(*polly.Options)) (*polly.ListLexiconsOutput, error)
}

func scanPolly(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := polly.NewFromConfig(acct.cfg, func(o *polly.Options) { o.Region = region })
	return scanPollyLexicons(ctx, client, acct, region, st, scanID)
}

// scanPollyLexicons discovers Polly pronunciation lexicons. ListLexicons has no
// SDK paginator (manual NextToken); lexicons carry no ARN on the list shape, so
// NativeID is synthesized from the name.
func scanPollyLexicons(ctx context.Context, client pollyAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	input := &polly.ListLexiconsInput{}
	var batch []*store.Resource
	for {
		out, err := client.ListLexicons(ctx, input)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "polly:ListLexicons", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("polly:ListLexicons: %w", err)
		}
		for _, l := range out.Lexicons {
			name := sv(l.Name)
			if name == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:polly:%s:%s:lexicon/%s", region, acct.ID, name)
			label := name
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypePollyLexicon, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(l), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		input.NextToken = out.NextToken
	}
	return upsertBatch(st, batch, "polly lexicons")
}
