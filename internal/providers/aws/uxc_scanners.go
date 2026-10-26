package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/uxc"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeUXCAccountCustomization, Service: "uxc", Untaggable: true})
	registerService(serviceEntry{
		name:   "aws:uxc",
		global: true,
		fn:     scanUXC,
	})
}

// scanUXC captures the per-account console-customization config (singleton).
// UXC is global; called once, in its home region (homeRegion).
// Synth ARN: arn:{p}:uxc::{a}:account-customization.
func scanUXC(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (total, inserted int, err error) {
	region, err := acct.homeRegion("uxc")
	if err != nil {
		return 0, 0, err
	}
	client := uxc.NewFromConfig(acct.cfg, func(o *uxc.Options) { o.Region = region })

	out, err := client.GetAccountCustomizations(ctx, &uxc.GetAccountCustomizationsInput{})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "uxc:GetAccountCustomizations", acct.ID, region, err)
		}
		return 0, 0, fmt.Errorf("uxc:GetAccountCustomizations: %w", err)
	}
	arn := arnFor(acct.partition(), "uxc", "", acct.ID, "account-customization")
	label := acct.ID
	r := &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeUXCAccountCustomization, NativeID: arn,
		Name: &label, Region: regionGlobal,
		AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
	}
	return upsertBatch(st, []*store.Resource{r}, "uxc account-customization")
}
