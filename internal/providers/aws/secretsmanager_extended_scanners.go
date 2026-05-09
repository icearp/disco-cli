package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

// scanSecretsManagerExtended discovers per-secret resource policies and
// rotation schedules. Walks ListSecrets, then GetResourcePolicy per secret
// and synthesizes a rotation-schedule row when RotationEnabled is true.
//
// AWS::SecretsManager::SecretTargetAttachment is skip-logged: it's a
// CloudFormation-only abstraction for linking secrets to RDS/etc, with no
// distinct AWS API surface.
func scanSecretsManagerExtended(ctx context.Context, client secretsManagerAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := secretsmanager.NewListSecretsPaginator(client, &secretsmanager.ListSecretsInput{})
	var policyBatch, rotBatch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "secretsmanager:ListSecrets(extended)", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("secretsmanager:ListSecrets(extended): %w", perr)
		}
		for _, s := range out.SecretList {
			arn := sv(s.ARN)
			if arn == "" {
				continue
			}
			pa, derr := client.GetResourcePolicy(ctx, &secretsmanager.GetResourcePolicyInput{SecretId: &arn})
			if derr == nil && sv(pa.ResourcePolicy) != "" {
				policyArn := arn + "/policy"
				label := "policy"
				policyBatch = append(policyBatch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeSecretsManagerResourcePolicy, NativeID: policyArn,
					Name: &label, Region: &region,
					AttributesJSON: mustJSON(pa), DiscoveredBy: scanID,
				})
			}
			if s.RotationEnabled != nil && *s.RotationEnabled {
				rotArn := arn + "/rotation-schedule"
				label := "rotation-schedule"
				rotBatch = append(rotBatch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeSecretsManagerRotationSchedule, NativeID: rotArn,
					Name: &label, Region: &region,
					AttributesJSON: mustJSON(map[string]any{
						"SecretId":          arn,
						"RotationLambdaARN": s.RotationLambdaARN,
						"RotationRules":     s.RotationRules,
						"LastRotatedDate":   s.LastRotatedDate,
					}), DiscoveredBy: scanID,
				})
			}
		}
	}
	t, i, err := upsertBatch(st, policyBatch, "secretsmanager resource-policies")
	if err != nil {
		return total, inserted, err
	}
	total += t
	inserted += i
	t, i, err = upsertBatch(st, rotBatch, "secretsmanager rotation-schedules")
	if err != nil {
		return total, inserted, err
	}
	total += t
	inserted += i
	return total, inserted, nil
}
