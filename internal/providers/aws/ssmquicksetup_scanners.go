package aws

import (
	"cmp"
	"context"
	"errors"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssmquicksetup"
	qstypes "github.com/aws/aws-sdk-go-v2/service/ssmquicksetup/types"
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
	ssmQuickSetupManagersOp       = "ssm-quicksetup:ListConfigurationManagers"
	ssmQuickSetupGetManagerOp     = "ssm-quicksetup:GetConfigurationManager"
	ssmQuickSetupConfigurationsOp = "ssm-quicksetup:ListConfigurations"
	// ssmQuickSetupManagerPageLimit and ssmQuickSetupConfigurationPageLimit
	// are the model's caps on ListConfigurationManagers and ListConfigurations
	// MaxItems (range 1..100 each).
	ssmQuickSetupManagerPageLimit       int32 = 100
	ssmQuickSetupConfigurationPageLimit int32 = 100
)

// scanSSMQuickSetup discovers SSM Quick Setup configuration managers, then the
// region's configurations. LifecycleAutomation is a ConfigurationManager
// sub-resource that has no listable endpoint and is not scanned. The
// configuration phase runs whatever the manager phase returned, except a
// failed store write, so its failure costs it nothing, and the counts from
// both sides are summed. The errors are collected per op (the manager
// listing, the manager reads, the configuration listing) and joined once the
// phases have stored their rows: an op's transient errors are demoted to a
// warning under that op, but only beside a hard error, which would otherwise
// bury them.
func scanSSMQuickSetup(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := ssmquicksetup.NewFromConfig(acct.cfg, func(o *ssmquicksetup.Options) { o.Region = region })
	var p phaseTotals
	managers, stop := scanSSMQuickSetupManagers(ctx, client, acct, region, st, scanID, &p)
	if stop {
		return p.result(st, acct, region)
	}
	total, inserted, err := scanSSMQuickSetupConfigurations(ctx, client, acct, region, st, scanID, managers)
	p.addPhase(ssmQuickSetupConfigurationsOp, total, inserted, err)
	return p.result(st, acct, region)
}

// ssmQuickSetupManagerKept describes the configuration managers of one cause
// stored as their list summary.
const ssmQuickSetupManagerKept = "%d configuration manager(s) stored as their list summary, without ConfigurationDefinitions or Tags; first %s: %w"

// ssmQuickSetupManagerRead is one manager's GetConfigurationManager result;
// both nil when the manager was not read.
type ssmQuickSetupManagerRead struct {
	out *ssmquicksetup.GetConfigurationManagerOutput
	err error
}

// scanSSMQuickSetupManagers lists the region's configuration managers, then
// reads each, concurrently through readEachKey, with GetConfigurationManager:
// only the full manager carries ConfigurationDefinitions (with their
// LocalDeploymentAdministrationRoleArn refs) and Tags. A manager the read no
// longer finds was deleted since it was listed and is not stored. One whose
// read fails any other way, or that was not read because ctx ended, is stored
// from its list summary: a denial is warned about once, transient failures
// come back as one error (the rows lost data a rescan recovers), or as one
// warning beside a hard error of either phase, and every other failure is
// returned after storing, or with the write's failure. A failed list page
// keeps the managers read before it: a denial is warned about, any other
// error returned. Counts and errors go to p: the listing's and the write's
// under ListConfigurationManagers, the reads' under GetConfigurationManager,
// the transient reads apart from the hard ones so that they can be demoted.
// It returns the ARNs of the managers stored, or stop when the write failed.
func scanSSMQuickSetupManagers(ctx context.Context, client *ssmquicksetup.Client, acct *account, region string, st *store.Store, scanID string, p *phaseTotals) (managers map[string]bool, stop bool) {
	summaries, listErr := readTokenPages(ctx, func(ctx context.Context, token *string) ([]qstypes.ConfigurationManagerSummary, *string, error) {
		out, err := client.ListConfigurationManagers(ctx, &ssmquicksetup.ListConfigurationManagersInput{
			StartingToken: token, MaxItems: sdkaws.Int32(ssmQuickSetupManagerPageLimit),
		})
		if err != nil {
			return nil, nil, err
		}
		return out.ConfigurationManagersList, out.NextToken, nil
	})
	var (
		listed    []qstypes.ConfigurationManagerSummary
		rows      []*store.Resource
		readErrs  []error
		skips     notStoredSkips
		denied    skipTally
		transient transientTally
	)
	for _, s := range summaries {
		if sv(s.ManagerArn) == "" {
			skips.skipped("configuration managers have an empty ARN", sv(s.Name))
			continue
		}
		listed = append(listed, s)
	}
	reads, stopped := readEachKey(ctx, listed, func(ctx context.Context, s qstypes.ConfigurationManagerSummary) ssmQuickSetupManagerRead {
		out, err := client.GetConfigurationManager(ctx, &ssmquicksetup.GetConfigurationManagerInput{ManagerArn: s.ManagerArn})
		return ssmQuickSetupManagerRead{out, err}
	})
	if stopped != nil {
		readErrs = append(readErrs, fmt.Errorf("%s: %w", ssmQuickSetupGetManagerOp, stopped))
	}
	// A manager not read once ctx ended keeps its summary row.
	reads = append(reads, make([]ssmQuickSetupManagerRead, len(listed)-len(reads))...)
	for i, s := range listed {
		arn, read := *s.ManagerArn, reads[i]
		r := &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeSSMQuickSetupConfigurationManager, NativeID: arn,
			Name: s.Name, Region: &region,
			AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
		}
		switch err := read.err; {
		case read.out == nil && err == nil:
		case err == nil:
			r.AttributesJSON, r.TagsJSON = mustJSON(read.out), mapTagsJSON(read.out.Tags)
		case isAPIErrorCode(err, "ResourceNotFoundException"):
			continue
		case isAccessDenied(err):
			denied.skipped(arn, err)
		case transient.drop(arn, err):
		default:
			readErrs = append(readErrs, fmt.Errorf("%s %s: %w", ssmQuickSetupGetManagerOp, arn, err))
		}
		rows = append(rows, r)
	}
	total, inserted, err := storeReadRows(st, acct, region, ssmQuickSetupManagersOp, rows, listErr, func() {
		skips.warn(st, ssmQuickSetupManagersOp, acct, region)
		if denied.count > 0 {
			_ = skipIfAccessDenied(st, ssmQuickSetupGetManagerOp, acct.ID, region, fmt.Errorf(ssmQuickSetupManagerKept, denied.count, denied.first, denied.err))
		}
	})
	p.errs.add(ssmQuickSetupGetManagerOp, errors.Join(readErrs...))
	if p.addPhase(ssmQuickSetupManagersOp, total, inserted, err) {
		return nil, true
	}
	if t := &transient.skipTally; t.count > 0 {
		p.errs.add(ssmQuickSetupGetManagerOp, fmt.Errorf("%s: "+ssmQuickSetupManagerKept, ssmQuickSetupGetManagerOp, t.count, t.first, t.err))
	}
	managers = make(map[string]bool, len(rows))
	for _, r := range rows {
		managers[r.NativeID] = true
	}
	return managers, false
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
// stored this scan. Rows read before an error, or before a page that repeats
// its token (errRepeatedNextToken), are stored.
//
// Only configurations deployed to the scanned account and region are stored.
// The model documents the listing as exactly those, but whether other
// accounts' or regions' deployments also come back is unverified; a
// deployment has one (Account, Region), so the filter lets at most one
// region's scan store it, rather than several scans upserting one NativeID.
// The rows it drops are counted in one warning. Account and Region are
// model-optional, so a row lacking either is not stored, nor is one lacking
// its ManagerArn or Id; each cause is warned about once, naming the first
// row by the identifying fields it has. Warnings are sent only once the
// write succeeded.
func scanSSMQuickSetupConfigurations(ctx context.Context, client *ssmquicksetup.Client, acct *account, region string, st *store.Store, scanID string, managers map[string]bool) (int, int, error) {
	listed, err := readTokenPages(ctx, func(ctx context.Context, token *string) ([]qstypes.ConfigurationSummary, *string, error) {
		out, err := client.ListConfigurations(ctx, &ssmquicksetup.ListConfigurationsInput{
			StartingToken: token, MaxItems: sdkaws.Int32(ssmQuickSetupConfigurationPageLimit),
		})
		if err != nil {
			return nil, nil, err
		}
		return out.ConfigurationsList, out.NextToken, nil
	})
	var (
		children []containedChild
		skips    notStoredSkips
	)
	foreign := 0
	for _, c := range listed {
		managerARN, id, deployedAccount, deployedRegion := sv(c.ManagerArn), sv(c.Id), sv(c.Account), sv(c.Region)
		switch {
		case managerARN == "" || id == "":
			skips.skipped("configurations have an empty ManagerArn or Id", cmp.Or(id, managerARN, "(a row with no ManagerArn and no Id)"))
			continue
		case deployedAccount == "" || deployedRegion == "":
			skips.skipped("configurations have an empty Account or Region", ssmQuickSetupConfigurationNativeID(managerARN, id))
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
	total, inserted, uerr := upsertRegionChildren(st, acct, region, ssmQuickSetupConfigurationsOp, children, err)
	if errors.Is(uerr, store.ErrStoreWrite) {
		return total, inserted, uerr
	}
	skips.warn(st, ssmQuickSetupConfigurationsOp, acct, region)
	if foreign > 0 {
		st.ReportWarning(store.ScanWarning{
			Provider: "aws", Service: ssmQuickSetupConfigurationsOp, Scope: acct.ID + "/" + region,
			Message: fmt.Sprintf("%d configuration(s) deployed to another account or region were not stored by this region's scan; "+
				"they belong to the scan of their own account and region (listing other deployments is unverified API behaviour)", foreign),
		})
	}
	return total, inserted, uerr
}
