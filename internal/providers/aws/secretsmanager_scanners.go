package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

func init() { registerService(serviceEntry{name: "aws:secretsmanager", fn: scanSecretsManager}) }

// scanSecretsManager discovers Secrets Manager secrets in one region. Secret
// values are never fetched — only metadata (rotation config, last-rotated date,
// KMS key binding) suitable for posture rules.
func scanSecretsManager(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := secretsmanager.NewFromConfig(acct.cfg, func(o *secretsmanager.Options) { o.Region = region })

	pager := secretsmanager.NewListSecretsPaginator(client, &secretsmanager.ListSecretsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied("secretsmanager:ListSecrets", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("secretsmanager:ListSecrets: %w", err)
		}
		var batch []*store.Resource
		for _, s := range page.SecretList {
			arn := sv(s.ARN)
			tags := make(map[string]string, len(s.Tags))
			for _, t := range s.Tags {
				if t.Key != nil && t.Value != nil {
					tags[*t.Key] = *t.Value
				}
			}
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeSecretsManagerSecret,
				NativeID:       arn,
				Name:           s.Name,
				Region:         &region,
				CreatedAt:      tp(s.CreatedDate),
				TagsJSON:       mapTagsJSON(tags),
				AttributesJSON: mustJSON(s),
				DiscoveredBy:   scanID,
			}
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert secrets: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}
