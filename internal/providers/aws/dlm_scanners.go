package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/dlm"
)

func init() {
	registerType(restype.Descriptor{Type: TypeDLMLifecyclePolicy, Service: "dlm", Upstream: "AWS::DLM::LifecyclePolicy", Leaf: true})
	registerService(serviceEntry{
		name: "aws:dlm",
		fn:   scanDLM,
	})
}

// scanDLM discovers Data Lifecycle Manager lifecycle policies. Synth ARN:
// arn:aws:dlm:{r}:{a}:policy/{policyId}.
func scanDLM(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := dlm.NewFromConfig(acct.cfg, func(o *dlm.Options) { o.Region = region })

	out, err := client.GetLifecyclePolicies(ctx, &dlm.GetLifecyclePoliciesInput{})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "dlm:GetLifecyclePolicies", acct.ID, region, err)
		}
		return 0, 0, fmt.Errorf("dlm:GetLifecyclePolicies: %w", err)
	}
	var batch []*store.Resource
	for _, p := range out.Policies {
		id := sv(p.PolicyId)
		if id == "" {
			continue
		}
		arn := fmt.Sprintf("arn:aws:dlm:%s:%s:policy/%s", region, acct.ID, id)
		status := string(p.State)
		batch = append(batch, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeDLMLifecyclePolicy, NativeID: arn,
			Name: &id, Region: &region, Status: &status,
			AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
		})
	}
	return upsertBatch(st, batch, "dlm lifecycle-policies")
}
