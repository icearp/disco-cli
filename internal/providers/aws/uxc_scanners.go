package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/uxc"
)

func init() {
	registerService(serviceEntry{
		name:   "aws:uxc",
		global: true,
		fn:     scanUXC,
		emits: []coverage.TypeDecl{
			{Service: "uxc", DiscoType: TypeUXCAccountCustomization},
		},
	})
}

// scanUXC captures the per-account console-customization config (singleton).
// UXC is global; gate to us-east-1 to avoid duplicate scans across regions.
// Synth ARN: arn:aws:uxc::{a}:account-customization.
func scanUXC(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (total, inserted int, err error) {
	region := "us-east-1"
	client := uxc.NewFromConfig(acct.cfg, func(o *uxc.Options) { o.Region = region })

	out, err := client.GetAccountCustomizations(ctx, &uxc.GetAccountCustomizationsInput{})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "uxc:GetAccountCustomizations", acct.ID, region, err)
		}
		return 0, 0, fmt.Errorf("uxc:GetAccountCustomizations: %w", err)
	}
	arn := fmt.Sprintf("arn:aws:uxc::%s:account-customization", acct.ID)
	label := acct.ID
	r := &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeUXCAccountCustomization, NativeID: arn,
		Name: &label, Region: regionGlobal,
		AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
		ManagedByProvider: true,
	}
	return upsertBatch(st, []*store.Resource{r}, "uxc account-customization")
}
