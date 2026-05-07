package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/voiceid"
)

// isVoiceIDNotEnabled disambiguates the "no longer accepting new customers"
// state from a real IAM denial — both surface as AccessDeniedException.
// Precedent: isMacieNotEnabled (per aws/CLAUDE.md "Macie variant").
func isVoiceIDNotEnabled(err error) bool {
	return isAccessDeniedWithMessage(err, "New customer access is no longer available")
}

func init() {
	registerService(serviceEntry{
		name: "aws:voice-id",
		fn:   scanVoiceID,
		emits: []coverage.TypeDecl{
			{Service: "voice-id", DiscoType: TypeVoiceIDDomain},
		},
	})
}

// scanVoiceID discovers Voice ID domains.
func scanVoiceID(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := voiceid.NewFromConfig(acct.cfg, func(o *voiceid.Options) { o.Region = region })

	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListDomains(ctx, &voiceid.ListDomainsInput{NextToken: nextToken})
		if err != nil {
			if isVoiceIDNotEnabled(err) {
				return 0, 0, markServiceDisabled(err)
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "voice-id:ListDomains", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("voice-id:ListDomains: %w", err)
		}
		for _, d := range out.DomainSummaries {
			arn := sv(d.Arn)
			if arn == "" {
				continue
			}
			status := string(d.DomainStatus)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeVoiceIDDomain, NativeID: arn,
				Name: d.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "voice-id domains")
}
