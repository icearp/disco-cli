package aws

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/inspector2"
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

// scanInspector2 discovers Inspector v2 resources and the organization's
// delegated administrator in one region.
// Findings, coverage rows, and the singleton org-level configuration are
// deliberately out of scope: findings are event data (matches Macie /
// Detective / Security Hub precedent), coverage explodes the row count
// (one entry per scanned resource × scan-type), and configuration is a
// singleton config rather than a graph-edge target.
//
// The delegated-admin phase runs whatever the other phases return, because
// a management account can delegate Inspector without enabling it itself;
// the other phases then end in the not-enabled sentinel. The sentinel is
// returned only when no phase stored a row and the delegated-admin phase has
// no error of its own: the dispatcher discards every count on a sentinel and
// would hide that error behind "(account: disabled)".
func scanInspector2(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := inspector2.NewFromConfig(acct.cfg, func(o *inspector2.Options) { o.Region = region })

	total, inserted, err := scanInspector2Resources(ctx, client, acct, region, st, scanID)
	t, i, aerr := scanInspector2DelegatedAdmins(ctx, client, acct, region, st, scanID)
	total += t
	inserted += i
	if errors.Is(err, errServiceDisabled) && (total > 0 || aerr != nil) {
		err = nil
	}
	var errs phaseErrs
	errs.add("inspector2", err)
	errs.add("inspector2:ListDelegatedAdminAccounts", aerr)
	return total, inserted, errs.join(st, acct, region)
}

// scanInspector2Resources lists finding-filters, member accounts
// (delegated-admin perspective) and the scanInspector2Extended types, phase
// by phase; the first failing phase ends the listing, and the rows stored
// before it stay counted.
func scanInspector2Resources(ctx context.Context, client inspector2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	{
		t, i, ferr := scanInspector2Filters(ctx, client, acct, region, st, scanID)
		total += t
		inserted += i
		if ferr != nil {
			return total, inserted, ferr
		}
	}

	{
		t, i, ferr := scanInspector2Members(ctx, client, acct, region, st, scanID)
		total += t
		inserted += i
		if ferr != nil {
			return total, inserted, ferr
		}
	}

	{
		t, i, ferr := scanInspector2Extended(ctx, client, acct, region, st, scanID)
		total += t
		inserted += i
		if ferr != nil {
			return total, inserted, ferr
		}
	}

	return total, inserted, nil
}

// inspector2MemberNativeID synthesises an identifier for a member-account
// row. Inspector v2 does not issue an ARN per (admin, member) tuple; the
// shape mirrors Detective members (`{parentARN}/member/{accountId}`) but
// uses the delegated-admin's pseudo-ARN as the parent so multi-admin
// rescans dedupe.
func inspector2MemberNativeID(region, adminAccountID, memberAccountID string) string {
	return fmt.Sprintf("arn:aws:inspector2:%s:%s:member/%s", region, adminAccountID, memberAccountID)
}

func scanInspector2Filters(ctx context.Context, client inspector2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := inspector2.NewListFiltersPaginator(client, &inspector2.ListFiltersInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "inspector2:ListFilters", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("inspector2:ListFilters: %w", perr)
		}
		for _, f := range out.Filters {
			arn := sv(f.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeInspector2Filter,
				NativeID:       arn,
				Name:           f.Name,
				Region:         &region,
				AttributesJSON: mustJSON(f),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert inspector2 filters: %w", uerr)
	}
	return len(batch), n, nil
}

func scanInspector2Members(ctx context.Context, client inspector2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := inspector2.NewListMembersPaginator(client, &inspector2.ListMembersInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "inspector2:ListMembers", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("inspector2:ListMembers: %w", perr)
		}
		for _, m := range out.Members {
			memberAcct := sv(m.AccountId)
			if memberAcct == "" {
				continue
			}
			nativeID := inspector2MemberNativeID(region, acct.ID, memberAcct)
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
