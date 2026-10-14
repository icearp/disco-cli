package aws

import (
	"encoding/json"
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveWorkMailAvailabilityLambda,
		EdgeDecl{TypeWorkMailAvailabilityConfiguration, TypeLambdaFunction, store.RelUses},
	)
	registerResolver(
		noopWorkMailHierarchy,
		EdgeDecl{TypeWorkMailOrganization, TypeWorkMailUser, store.RelContains},
		EdgeDecl{TypeWorkMailOrganization, TypeWorkMailGroup, store.RelContains},
		EdgeDecl{TypeWorkMailOrganization, TypeWorkMailResource, store.RelContains},
		EdgeDecl{TypeWorkMailOrganization, TypeWorkMailMailDomain, store.RelContains},
		EdgeDecl{TypeWorkMailOrganization, TypeWorkMailAccessControlRule, store.RelContains},
		EdgeDecl{TypeWorkMailOrganization, TypeWorkMailAvailabilityConfiguration, store.RelContains},
		EdgeDecl{TypeWorkMailOrganization, TypeWorkMailImpersonationRole, store.RelContains},
		EdgeDecl{TypeWorkMailOrganization, TypeWorkMailMobileDeviceAccessRule, store.RelContains},
		EdgeDecl{TypeWorkMailOrganization, TypeWorkMailMobileDeviceAccessOverride, store.RelContains},
		EdgeDecl{TypeWorkMailOrganization, TypeWorkMailPersonalAccessToken, store.RelContains},
	)
}

// noopWorkMailHierarchy only declares the organization→child contains edges,
// which scanWorkMailIn records through childFanOut.
func noopWorkMailHierarchy(_ *account, _ *store.Store) error {
	return nil
}

// resolveWorkMailAvailabilityLambda links each Lambda-backed availability
// configuration to the function it invokes (LambdaProvider.LambdaArn), when
// that function was scanned. A qualified ARN links to its function.
func resolveWorkMailAvailabilityLambda(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeWorkMailAvailabilityConfiguration}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	functions, err := scannedIDSet(acct, st, TypeLambdaFunction)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			LambdaProvider *struct {
				LambdaArn *string `json:"LambdaArn"`
			} `json:"LambdaProvider"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			return fmt.Errorf("decode workmail availability configuration %s: %w", r.NativeID, err)
		}
		if attrs.LambdaProvider == nil || sv(attrs.LambdaProvider.LambdaArn) == "" {
			continue
		}
		fnID := store.ResourceID("aws", acct.ID, lambdaStripQualifier(sv(attrs.LambdaProvider.LambdaArn)))
		if !functions[fnID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, fnID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert workmail availability configuration→lambda: %w", err)
		}
	}
	return nil
}
