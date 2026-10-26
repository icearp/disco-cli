package aws

import (
	"context"
	"errors"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/inspector2"
	i2types "github.com/aws/aws-sdk-go-v2/service/inspector2/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeInspector2Filter, Service: "inspectorv2"})
	registerType(restype.Descriptor{Type: TypeInspector2Member, Service: "inspectorv2"})
	registerType(restype.Descriptor{Type: TypeInspector2CisScanConfiguration, Service: "inspectorv2"})
	registerType(restype.Descriptor{Type: TypeInspector2CodeSecurityIntegration, Service: "inspectorv2"})
	registerType(restype.Descriptor{Type: TypeInspector2CodeSecurityScanConfiguration, Service: "inspectorv2"})
	registerType(restype.Descriptor{Type: TypeInspector2Connector, Service: "inspectorv2"})
	registerType(restype.Descriptor{Type: TypeInspector2DelegatedAdminAccount, Service: "inspectorv2"})
	registerService(serviceEntry{
		name: "aws:inspector2",
		fn:   scanInspector2,
	})
}

// inspector2API is the narrow set of Inspector v2 operations called by the
// scanInspector2 sub-phases.
type inspector2API interface {
	ListFilters(context.Context, *inspector2.ListFiltersInput, ...func(*inspector2.Options)) (*inspector2.ListFiltersOutput, error)
	ListMembers(context.Context, *inspector2.ListMembersInput, ...func(*inspector2.Options)) (*inspector2.ListMembersOutput, error)
	ListCisScanConfigurations(context.Context, *inspector2.ListCisScanConfigurationsInput, ...func(*inspector2.Options)) (*inspector2.ListCisScanConfigurationsOutput, error)
	ListCodeSecurityIntegrations(context.Context, *inspector2.ListCodeSecurityIntegrationsInput, ...func(*inspector2.Options)) (*inspector2.ListCodeSecurityIntegrationsOutput, error)
	ListCodeSecurityScanConfigurations(context.Context, *inspector2.ListCodeSecurityScanConfigurationsInput, ...func(*inspector2.Options)) (*inspector2.ListCodeSecurityScanConfigurationsOutput, error)
	ListConnectors(context.Context, *inspector2.ListConnectorsInput, ...func(*inspector2.Options)) (*inspector2.ListConnectorsOutput, error)
	ListDelegatedAdminAccounts(context.Context, *inspector2.ListDelegatedAdminAccountsInput, ...func(*inspector2.Options)) (*inspector2.ListDelegatedAdminAccountsOutput, error)
}

// Op labels of the Inspector v2 phases, each warned and returned under its own.
const (
	inspector2FiltersOp                  = "inspector2:ListFilters"
	inspector2MembersOp                  = "inspector2:ListMembers"
	inspector2CisConfigsOp               = "inspector2:ListCisScanConfigurations"
	inspector2CodeSecurityIntegrationsOp = "inspector2:ListCodeSecurityIntegrations"
	inspector2CodeSecurityScanConfigsOp  = "inspector2:ListCodeSecurityScanConfigurations"
	inspector2ConnectorsOp               = "inspector2:ListConnectors"
	inspector2DelegatedAdminsOp          = "inspector2:ListDelegatedAdminAccounts"
)

// inspector2PageLimit is the maxResults cap ListFilters and
// ListCisScanConfigurations model.
const inspector2PageLimit = 100

// inspector2Phase is one listing scanInspector2 runs. enabledOnly marks the
// phases not called once the CIS phase reported the account not enabled.
type inspector2Phase struct {
	op          string
	enabledOnly bool
	run         func(context.Context, inspector2API, *account, string, *store.Store, string) (int, int, error)
}

// scanInspector2 discovers Inspector v2 resources and the organization's
// delegated administrator in one region.
// Findings, coverage rows, and the singleton org-level configuration are
// deliberately out of scope: findings are event data (matches Macie /
// Detective / Security Hub precedent), coverage explodes the row count
// (one entry per scanned resource × scan-type), and configuration is a
// singleton config rather than a graph-edge target.
//
// Phases run in order, each whatever the others return; only a failed store
// write stops them, since every later write would fail too. When the CIS
// phase reports the account not enabled, the code-security and connector
// phases are not called: their not-enabled denial would be an IAM-style
// warning no policy change can fix. The delegated-admin phase runs anyway,
// because a management account can delegate Inspector without enabling it
// itself. The not-enabled sentinel is returned only when no phase stored a
// row and no phase failed: the dispatcher discards every count on a sentinel
// and would hide that failure behind "(account: disabled)".
func scanInspector2(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := inspector2.NewFromConfig(acct.cfg, func(o *inspector2.Options) { o.Region = region })
	phases := []inspector2Phase{
		{op: inspector2FiltersOp, run: scanInspector2Filters},
		{op: inspector2MembersOp, run: scanInspector2Members},
		{op: inspector2CisConfigsOp, run: scanInspector2CisConfigs},
		{op: inspector2CodeSecurityIntegrationsOp, enabledOnly: true, run: scanInspector2CodeSecurityIntegrations},
		{op: inspector2CodeSecurityScanConfigsOp, enabledOnly: true, run: scanInspector2CodeSecurityScanConfigs},
		{op: inspector2ConnectorsOp, enabledOnly: true, run: scanInspector2Connectors},
		{op: inspector2DelegatedAdminsOp, run: scanInspector2DelegatedAdmins},
	}
	var (
		p          phaseTotals
		notEnabled error
	)
	for _, ph := range phases {
		if ph.enabledOnly && notEnabled != nil {
			continue
		}
		t, i, err := ph.run(ctx, client, acct, region, st, scanID)
		if errors.Is(err, errServiceDisabled) {
			notEnabled = err
			continue
		}
		if p.addPhase(ph.op, t, i, err) {
			break
		}
	}
	total, inserted, err := p.result(st, acct, region)
	if notEnabled != nil && total == 0 && err == nil {
		return 0, 0, notEnabled
	}
	return total, inserted, err
}

// inspector2MemberNativeID synthesises an identifier for a member-account
// row. Inspector v2 does not issue an ARN per (admin, member) tuple; the
// shape mirrors Detective members (`{parentARN}/member/{accountId}`) but
// uses the delegated-admin's pseudo-ARN as the parent so multi-admin
// rescans dedupe.
func inspector2MemberNativeID(partition, region, adminAccountID, memberAccountID string) string {
	return arnFor(partition, "inspector2", region, adminAccountID, "member/"+memberAccountID)
}

// scanInspector2Filters stores the finding filters ListFilters returns,
// those read before a failed page included.
func scanInspector2Filters(ctx context.Context, client inspector2API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, inspector2FiltersOp,
		func(ctx context.Context, token *string) ([]i2types.Filter, *string, error) {
			out, err := client.ListFilters(ctx, &inspector2.ListFiltersInput{NextToken: token, MaxResults: sdkaws.Int32(inspector2PageLimit)})
			if err != nil {
				return nil, nil, err
			}
			return out.Filters, out.NextToken, nil
		},
		func(f i2types.Filter) (*store.Resource, string, string) {
			if sv(f.Arn) == "" {
				return nil, "finding filters have an empty Arn", sv(f.Name)
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeInspector2Filter, NativeID: *f.Arn, Name: f.Name, Region: &region,
				TagsJSON: mapTagsJSON(f.Tags), AttributesJSON: mustJSON(f), DiscoveredBy: scanID,
			}, "", ""
		})
}

func scanInspector2Members(ctx context.Context, client inspector2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := inspector2.NewListMembersPaginator(client, &inspector2.ListMembersInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, inspector2MembersOp, acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("inspector2:ListMembers: %w", perr)
		}
		for _, m := range out.Members {
			memberAcct := sv(m.AccountId)
			if memberAcct == "" {
				continue
			}
			nativeID := inspector2MemberNativeID(acct.partition(), region, acct.ID, memberAcct)
			status := string(m.RelationshipStatus)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeInspector2Member,
				NativeID:       nativeID,
				Region:         &region,
				Status:         &status,
				AttributesJSON: mustJSON(m),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert inspector2 members: %w", uerr)
	}
	return len(batch), n, nil
}
