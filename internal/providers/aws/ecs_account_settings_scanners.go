package aws

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeECSAccountSetting, Service: "ecs"})
}

const (
	ecsListAccountSettingsOp = "ecs:ListAccountSettings"
	// ecsAccountSettingsPageLimit is the model's maxResults cap (1-10).
	ecsAccountSettingsPageLimit = 10
)

type ecsAccountSettingsAPI interface {
	ListAccountSettings(context.Context, *ecs.ListAccountSettingsInput, ...func(*ecs.Options)) (*ecs.ListAccountSettingsOutput, error)
}

// ecsAccountSettingNativeID builds the NativeID of an account setting. ECS
// issues no ARN for a setting; the region and the setting name identify the
// effective value within an account.
func ecsAccountSettingNativeID(region, accountID string, name ecstypes.SettingName) string {
	return fmt.Sprintf("arn:aws:ecs:%s:%s:account-setting/%s", region, accountID, name)
}

// scanECSAccountSettings stores one row per account setting in the region, as
// ListAccountSettings reports it with EffectiveSettings=true for the scanning
// principal.
//
// Why effective: with EffectiveSettings=false and no PrincipalArn, the API
// returns only settings set explicitly on the calling principal, so a scanner
// role with no overrides would see nothing, not the account defaults
// (PutAccountSettingDefault). Asking for the account defaults directly
// (PrincipalArn = the root user) is documented as allowed only to the root user
// or that principal.
//
// An effective value may come from the scanning identity's own override, the
// account default, or the AWS default. PrincipalArn shows which principal the
// value applies to; whether an inherited value reports the root user is
// unverified. Values follow the scanning identity, so two scan configs under
// different identities can flip these rows between scans.
//
// The NativeID is keyed by setting name alone, not by the returned
// PrincipalArn: an effective listing holds one value per name, and keying by
// principal would rename every row whenever the scan runs under a different
// identity. PrincipalArn stays in the attributes.
func scanECSAccountSettings(ctx context.Context, client ecsAccountSettingsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := ecs.NewListAccountSettingsPaginator(client, &ecs.ListAccountSettingsInput{EffectiveSettings: true},
		func(o *ecs.ListAccountSettingsPaginatorOptions) { o.Limit = ecsAccountSettingsPageLimit })
	var batch []*store.Resource
	var listErr error
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				listErr = skipIfAccessDenied(st, ecsListAccountSettingsOp, acct.ID, region, err)
			} else {
				listErr = fmt.Errorf("%s: %w", ecsListAccountSettingsOp, err)
			}
			break
		}
		for _, s := range page.Settings {
			if s.Name == "" {
				continue
			}
			name := string(s.Name)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeECSAccountSetting, NativeID: ecsAccountSettingNativeID(region, acct.ID, s.Name),
				Name: &name, Region: &region,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	total, inserted, err := upsertBatch(st, batch, "ecs account-settings")
	return total, inserted, errors.Join(listErr, err)
}
