package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/rbin"
	"github.com/aws/aws-sdk-go-v2/service/rbin/types"
)

func init() {
	registerService(serviceEntry{
		name: "aws:rbin",
		fn:   scanRbin,
		emits: []coverage.TypeDecl{
			{Service: "rbin", DiscoType: TypeRbinRule, Leaf: true},
		},
	})
}

// scanRbin discovers Recycle Bin retention rules. ListRules requires a
// ResourceType filter; iterate all known types. Synth ARN:
// arn:aws:rbin:{r}:{a}:rule/{identifier}.
func scanRbin(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := rbin.NewFromConfig(acct.cfg, func(o *rbin.Options) { o.Region = region })

	seen := map[string]bool{}
	var batch []*store.Resource
	for _, rt := range types.ResourceType("").Values() {
		resourceType := rt
		var nextToken *string
		for {
			out, err := client.ListRules(ctx, &rbin.ListRulesInput{
				ResourceType: resourceType,
				NextToken:    nextToken,
			})
			if err != nil {
				if isAccessDenied(err) {
					_ = skipIfAccessDenied(st, "rbin:ListRules", acct.ID, region, err)
					break
				}
				return 0, 0, fmt.Errorf("rbin:ListRules type=%s: %w", resourceType, err)
			}
			for _, r := range out.Rules {
				id := sv(r.Identifier)
				if id == "" || seen[id] {
					continue
				}
				seen[id] = true
				arn := sv(r.RuleArn)
				if arn == "" {
					arn = fmt.Sprintf("arn:aws:rbin:%s:%s:rule/%s", region, acct.ID, id)
				}
				status := string(r.LockState)
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeRbinRule, NativeID: arn,
					Name: &id, Region: &region, Status: &status,
					AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
				})
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			nextToken = out.NextToken
		}
	}
	return upsertBatch(st, batch, "rbin rules")
}
