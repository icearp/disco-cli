package aws

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/ssmquicksetup"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeSSMQuickSetupConfigurationManager, Service: "ssm-quick-setup"})
	registerType(restype.Descriptor{Type: TypeSSMQuickSetupConfiguration, Service: "ssm-quick-setup"})
	registerService(serviceEntry{
		name: "aws:ssm-quick-setup",
		fn:   scanSSMQuickSetup,
	})
}

const (
	ssmQuickSetupConfigurationsOp = "ssm-quicksetup:ListConfigurations"
	// ssmQuickSetupConfigurationPageLimit is the model's cap on
	// ListConfigurations MaxItems (range 1..100).
	ssmQuickSetupConfigurationPageLimit int32 = 100
)

// scanSSMQuickSetup discovers SSM Quick Setup configuration managers, then the
// region's configurations. LifecycleAutomation is a ConfigurationManager
// sub-resource that has no listable endpoint and is not scanned.
// The configuration phase runs whatever the manager phase returned, so its
// failure costs it nothing, and the counts from both sides are summed. Each
// side's transient error is demoted to a warning, since one transient member
// would make the dispatcher read the whole join as transient.
func scanSSMQuickSetup(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := ssmquicksetup.NewFromConfig(acct.cfg, func(o *ssmquicksetup.Options) { o.Region = region })
	total, inserted, managers, err := scanSSMQuickSetupManagers(ctx, client, acct, region, st, scanID)
	t, i, cerr := scanSSMQuickSetupConfigurations(ctx, client, acct, region, st, scanID, managers)
	return total + t, inserted + i, errors.Join(
		demoteTransient(st, "ssm-quicksetup:ListConfigurationManagers", acct, region, err),
		demoteTransient(st, ssmQuickSetupConfigurationsOp, acct, region, cerr),
	)
}

// scanSSMQuickSetupManagers stores the region's configuration managers and
// returns the ARNs of those stored.
func scanSSMQuickSetupManagers(ctx context.Context, client *ssmquicksetup.Client, acct *account, region string, st *store.Store, scanID string) (int, int, map[string]bool, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListConfigurationManagers(ctx, &ssmquicksetup.ListConfigurationManagersInput{StartingToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, nil, skipIfAccessDenied(st, "ssm-quicksetup:ListConfigurationManagers", acct.ID, region, err)
			}
			return 0, 0, nil, fmt.Errorf("ssm-quicksetup:ListConfigurationManagers: %w", err)
		}
		for _, c := range out.ConfigurationManagersList {
			arn := sv(c.ManagerArn)
			if arn == "" {
				continue
			}
			// Enrich with GetConfigurationManager body — ConfigurationDefinitions[]
			// carries LocalDeploymentAdministrationRoleArn refs absent from the
			// list-summary shape. Fall back to summary on per-row failure.
			attrs := mustJSON(c)
			marn := arn
			gout, gerr := client.GetConfigurationManager(ctx, &ssmquicksetup.GetConfigurationManagerInput{ManagerArn: &marn})
			if gerr != nil {
				if isAccessDenied(gerr) {
					_ = skipIfAccessDenied(st, "ssm-quicksetup:GetConfigurationManager", acct.ID, region, gerr)
				}
			} else if gout != nil {
				attrs = mustJSON(gout)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSSMQuickSetupConfigurationManager, NativeID: arn,
				Name: c.Name, Region: &region,
				AttributesJSON: attrs, DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	total, inserted, err := upsertBatch(st, batch, "ssm-quick-setup configuration-managers")
	if err != nil {
		return 0, 0, nil, err
	}
	managers := make(map[string]bool, len(batch))
	for _, r := range batch {
		managers[r.NativeID] = true
	}
	return total, inserted, managers, nil
}

// ssmQuickSetupConfigurationNativeID keys a configuration under the manager
// that deployed it: the API returns no ARN for one and the Service Reference
// defines none. The Id goes last because the GetConfiguration ConfigurationId
// pattern admits "/".
func ssmQuickSetupConfigurationNativeID(managerARN, id string) string {
	return managerARN + "/configuration/" + id
}

// scanSSMQuickSetupConfigurations stores the configurations the region's
// unfiltered ListConfigurations returns — one per deployment of a manager's
// configuration definition to a target account and region — and links each
// under its manager when managers holds it. managers holds only managers
// stored this scan. Rows read before an error are stored.
//
// Only configurations deployed to the scanned account and region are stored.
// The model documents the listing as exactly those, but whether other
// accounts' or regions' deployments also come back is unverified; a
// deployment has one (Account, Region), so the filter lets at most one
// region's scan store it, rather than several scans upserting one NativeID.
// The rows it drops are counted in one warning. Account and Region are
// model-optional, so a row lacking either is skipped like one lacking an Id.
func scanSSMQuickSetupConfigurations(ctx context.Context, client *ssmquicksetup.Client, acct *account, region string, st *store.Store, scanID string, managers map[string]bool) (int, int, error) {
	pager := ssmquicksetup.NewListConfigurationsPaginator(client, &ssmquicksetup.ListConfigurationsInput{},
		func(o *ssmquicksetup.ListConfigurationsPaginatorOptions) {
			o.Limit = ssmQuickSetupConfigurationPageLimit
			o.StopOnDuplicateToken = true
		})
	var children []containedChild
	foreign := 0
	var err error
	for pager.HasMorePages() {
		var out *ssmquicksetup.ListConfigurationsOutput
		out, err = pager.NextPage(ctx)
		if err != nil {
			break
		}
		for _, c := range out.ConfigurationsList {
			managerARN, id, deployedAccount, deployedRegion := sv(c.ManagerArn), sv(c.Id), sv(c.Account), sv(c.Region)
			if managerARN == "" || id == "" || deployedAccount == "" || deployedRegion == "" {
				continue
			}
			if deployedAccount != acct.ID || deployedRegion != region {
				foreign++
				continue
			}
			var parentID string
			if managers[managerARN] {
				parentID = store.ResourceID("aws", acct.ID, managerARN)
			}
			children = append(children, containedChild{parentID: parentID, row: &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSSMQuickSetupConfiguration, NativeID: ssmQuickSetupConfigurationNativeID(managerARN, id),
				// The filter above makes the scanned region the deployment's region.
				Name: &id, Region: &region,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			}})
		}
	}
	if foreign > 0 {
		st.ReportWarning(store.ScanWarning{
			Provider: "aws", Service: ssmQuickSetupConfigurationsOp, Scope: acct.ID + "/" + region,
			Message: fmt.Sprintf("%d configuration(s) deployed to another account or region were not stored by this region's scan; "+
				"they belong to the scan of their own account and region (listing other deployments is unverified API behaviour)", foreign),
		})
	}
	return upsertRegionChildren(st, acct, region, ssmQuickSetupConfigurationsOp, children, err)
}
