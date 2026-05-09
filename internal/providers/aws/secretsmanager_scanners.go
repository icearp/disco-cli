package aws

import (
	"context"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
)

func init() {
	registerService(serviceEntry{
		name: "aws:secretsmanager",
		fn:   scanSecretsManager,
		emits: []coverage.TypeDecl{
			{Service: "secretsmanager", DiscoType: TypeSecretsManagerSecret},
			{Service: "secretsmanager", DiscoType: TypeSecretsManagerResourcePolicy},
			{Service: "secretsmanager", DiscoType: TypeSecretsManagerRotationSchedule},
		},
	})
}

// secretsManagerAPI is the narrow set of Secrets Manager operations called by
// scanSecretsManagerSecrets.
type secretsManagerAPI interface {
	ListSecrets(context.Context, *secretsmanager.ListSecretsInput, ...func(*secretsmanager.Options)) (*secretsmanager.ListSecretsOutput, error)
	GetResourcePolicy(context.Context, *secretsmanager.GetResourcePolicyInput, ...func(*secretsmanager.Options)) (*secretsmanager.GetResourcePolicyOutput, error)
}

// scanSecretsManager discovers Secrets Manager secrets in one region. Secret
// values are never fetched — only metadata (rotation config, last-rotated date,
// KMS key binding) suitable for posture rules.
func scanSecretsManager(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := secretsmanager.NewFromConfig(acct.cfg, func(o *secretsmanager.Options) { o.Region = region })
	t, i, ferr := scanSecretsManagerSecrets(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	t, i, ferr = scanSecretsManagerExtended(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

// scanSecretsManagerSecrets holds the testable scan body.
func scanSecretsManagerSecrets(ctx context.Context, client secretsManagerAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	p := secretsmanager.NewListSecretsPaginator(client, &secretsmanager.ListSecretsInput{})
	return pageScan(ctx, "secretsmanager:ListSecrets", acct, region, st,
		p.HasMorePages,
		func(c context.Context) (*secretsmanager.ListSecretsOutput, error) { return p.NextPage(c) },
		func(p *secretsmanager.ListSecretsOutput) []smtypes.SecretListEntry { return p.SecretList },
		func(s smtypes.SecretListEntry) *store.Resource {
			tags := make(map[string]string, len(s.Tags))
			for _, t := range s.Tags {
				if t.Key != nil && t.Value != nil {
					tags[*t.Key] = *t.Value
				}
			}
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeSecretsManagerSecret,
				NativeID:       sv(s.ARN),
				Name:           s.Name,
				Region:         &region,
				CreatedAt:      tp(s.CreatedDate),
				TagsJSON:       mapTagsJSON(tags),
				AttributesJSON: mustJSON(s),
				DiscoveredBy:   scanID,
			}
		})
}
