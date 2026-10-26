package aws

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/grafana"
	grafanatypes "github.com/aws/aws-sdk-go-v2/service/grafana/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeGrafanaWorkspace, Service: "grafana"})
	registerType(restype.Descriptor{Type: TypeGrafanaWorkspaceServiceAccount, Service: "grafana"})
	registerType(restype.Descriptor{Type: TypeGrafanaWorkspaceServiceAccountToken, Service: "grafana"})
	registerType(restype.Descriptor{Type: TypeGrafanaPermission, Service: "grafana"})
	registerService(serviceEntry{
		name: "aws:grafana",
		fn:   scanGrafana,
	})
}

type grafanaAPI interface {
	ListWorkspaces(context.Context, *grafana.ListWorkspacesInput, ...func(*grafana.Options)) (*grafana.ListWorkspacesOutput, error)
	ListWorkspaceServiceAccounts(context.Context, *grafana.ListWorkspaceServiceAccountsInput, ...func(*grafana.Options)) (*grafana.ListWorkspaceServiceAccountsOutput, error)
	ListWorkspaceServiceAccountTokens(context.Context, *grafana.ListWorkspaceServiceAccountTokensInput, ...func(*grafana.Options)) (*grafana.ListWorkspaceServiceAccountTokensOutput, error)
	ListPermissions(context.Context, *grafana.ListPermissionsInput, ...func(*grafana.Options)) (*grafana.ListPermissionsOutput, error)
}

const (
	grafanaListPermissionsOp = "grafana:ListPermissions"
	// grafanaPermissionPageLimit is ListPermissions maxResults' model maximum.
	grafanaPermissionPageLimit = 100
)

// grafanaServiceAccount is a service account stored this scan: the token list
// op keys on both ids, nativeID is its row's NativeID.
type grafanaServiceAccount struct{ workspaceID, id, nativeID string }

// scanGrafana discovers Amazon Managed Grafana workspaces, their service
// accounts, the service accounts' tokens (metadata only: the list API never
// returns a token's key) and the workspaces' role grants.
//
// The workspace, service account and token phases stop at their first hard
// error. The permission phase runs whatever they returned, over the workspaces
// stored before a failure, so its failure costs them no rows and a service
// account or token failure does not skip it. The older phases' transient error
// is demoted to a warning only beside a hard error: joined, it would be buried
// in the hard error instead of reported under its op; the permission phase's
// error is joined as is, because its fan-out already drops each workspace's
// transient error. The counts are summed.
func scanGrafana(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := grafana.NewFromConfig(acct.cfg, func(o *grafana.Options) { o.Region = region })
	total, inserted, permWorkspaces, err := scanGrafanaOlderPhases(ctx, client, acct, region, st, scanID)
	t, i, perr := scanGrafanaPermissions(ctx, client, acct, region, st, scanID, permWorkspaces)
	return total + t, inserted + i, demoteBeside(st, "grafana", acct, region, err, perr)
}

// grafanaParents are the workspaces the workspace phase stored, split by the
// child phase that fans out over them.
type grafanaParents struct {
	serviceAccounts []childParent
	permissions     []childParent
}

// scanGrafanaOlderPhases runs the workspace, service account and token
// phases, stopping at the first hard error. It also returns the stored
// workspaces that use, or may use, IAM Identity Center (see
// grafanaMayUseIdentityCenter), which are empty when the workspace phase
// failed.
func scanGrafanaOlderPhases(ctx context.Context, client grafanaAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, permWorkspaces []childParent, err error) {
	var (
		workspaces grafanaParents
		accounts   []grafanaServiceAccount
	)
	for _, phase := range []func() (int, int, error){
		func() (t, i int, err error) {
			workspaces, t, i, err = scanGrafanaWorkspaces(ctx, client, acct, region, st, scanID)
			return t, i, err
		},
		func() (t, i int, err error) {
			accounts, t, i, err = scanGrafanaServiceAccounts(ctx, client, acct, region, st, scanID, workspaces.serviceAccounts)
			return t, i, err
		},
		func() (int, int, error) {
			return scanGrafanaServiceAccountTokens(ctx, client, acct, region, st, scanID, accounts)
		},
	} {
		t, i, ferr := phase()
		total += t
		inserted += i
		if ferr != nil {
			return total, inserted, workspaces.permissions, ferr
		}
	}
	return total, inserted, workspaces.permissions, nil
}

// scanGrafanaWorkspaces stores every workspace and returns, as service
// account fan-out parents, those whose Grafana version has service accounts
// and, as permission fan-out parents, those that use IAM Identity Center.
func scanGrafanaWorkspaces(ctx context.Context, client grafanaAPI, acct *account, region string, st *store.Store, scanID string) (grafanaParents, int, int, error) {
	var (
		batch   []*store.Resource
		parents grafanaParents
	)
	var nextToken *string
	for {
		out, err := client.ListWorkspaces(ctx, &grafana.ListWorkspacesInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return grafanaParents{}, 0, 0, skipIfAccessDenied(st, "grafana:ListWorkspaces", acct.ID, region, err)
			}
			return grafanaParents{}, 0, 0, fmt.Errorf("grafana:ListWorkspaces: %w", err)
		}
		for _, w := range out.Workspaces {
			id := sv(w.Id)
			if id == "" {
				continue
			}
			arn := arnFor(acct.partition(), "grafana", region, acct.ID, "/workspaces/"+id)
			status := string(w.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGrafanaWorkspace, NativeID: arn,
				Name: w.Name, Region: &region, Status: &status, TagsJSON: mapTagsJSON(w.Tags),
				AttributesJSON: mustJSON(w), DiscoveredBy: scanID,
			})
			if grafanaHasServiceAccounts(sv(w.GrafanaVersion)) && grafanaHasInstance(w.Status) {
				parents.serviceAccounts = append(parents.serviceAccounts, childParent{id: id, arn: arn})
			}
			if grafanaMayUseIdentityCenter(w.Authentication) {
				parents.permissions = append(parents.permissions, childParent{id: id, arn: arn})
			}
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	total, inserted, err := upsertBatch(st, batch, "grafana workspaces")
	if err != nil {
		return grafanaParents{}, 0, 0, err
	}
	return parents, total, inserted, nil
}

// grafanaMayUseIdentityCenter reports whether a workspace with this
// authentication summary can hold the role grants ListPermissions lists: the
// API documents them as grants to IAM Identity Center users and groups, so a
// SAML-only workspace has none. A workspace whose summary or provider list
// (both model-required) is missing is assumed to use Identity Center, so that
// the list call's own result shows rather than the workspace being skipped
// unseen.
func grafanaMayUseIdentityCenter(auth *grafanatypes.AuthenticationSummary) bool {
	return auth == nil || len(auth.Providers) == 0 ||
		slices.Contains(auth.Providers, grafanatypes.AuthenticationProviderTypesAwsSso)
}

// grafanaHasInstance reports whether a workspace in this status has a running
// Grafana instance to list service accounts from. Only the states before the
// first successful create and after a delete starts lack one; the update,
// upgrade and license states keep the instance, so skipping them would make
// their service accounts look deleted.
func grafanaHasInstance(status grafanatypes.WorkspaceStatus) bool {
	switch status {
	case grafanatypes.WorkspaceStatusCreating, grafanatypes.WorkspaceStatusCreationFailed,
		grafanatypes.WorkspaceStatusDeleting, grafanatypes.WorkspaceStatusDeletionFailed,
		grafanatypes.WorkspaceStatusFailed:
		return false
	}
	return true
}

// grafanaHasServiceAccounts reports whether a workspace of this Grafana
// version can have service accounts: the API documents them for version 9
// and above only. A version that does not parse is assumed to have them, so
// the list call's own error surfaces rather than the workspace being skipped
// unseen.
func grafanaHasServiceAccounts(version string) bool {
	major, _, _ := strings.Cut(version, ".")
	n, err := strconv.Atoi(major)
	return err != nil || n >= 9
}

// scanGrafanaServiceAccounts lists the service accounts of each given
// workspace, keyed {workspaceARN}/serviceaccount/{id} because the API returns
// no ARN, and returns them as token fan-out parents.
func scanGrafanaServiceAccounts(ctx context.Context, client grafanaAPI, acct *account, region string, st *store.Store, scanID string, workspaces []childParent) ([]grafanaServiceAccount, int, int, error) {
	var (
		mu       sync.Mutex
		accounts []grafanaServiceAccount
	)
	total, inserted, err := childFanOut(ctx, st, acct, region, "grafana:ListWorkspaceServiceAccounts", workspaces, isGrafanaNotFound,
		func(ctx context.Context, w childParent) ([]*store.Resource, error) {
			var (
				rows  []*store.Resource
				found []grafanaServiceAccount
			)
			pager := grafana.NewListWorkspaceServiceAccountsPaginator(client, &grafana.ListWorkspaceServiceAccountsInput{WorkspaceId: &w.id})
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if isAPIErrorCode(err, "ConflictException") {
					warnGrafanaConflict(st, "grafana:ListWorkspaceServiceAccounts", acct.ID, region, "workspace "+w.id, err)
					return nil, nil
				}
				if err != nil {
					return nil, err
				}
				for _, sa := range out.ServiceAccounts {
					id := sv(sa.Id)
					if id == "" {
						continue
					}
					nativeID := w.arn + "/serviceaccount/" + id
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeGrafanaWorkspaceServiceAccount, NativeID: nativeID,
						Name: sa.Name, Region: &region,
						AttributesJSON: mustJSON(sa), DiscoveredBy: scanID,
					})
					found = append(found, grafanaServiceAccount{workspaceID: w.id, id: id, nativeID: nativeID})
				}
			}
			mu.Lock()
			accounts = append(accounts, found...)
			mu.Unlock()
			return rows, nil
		})
	if err != nil {
		return nil, total, inserted, err
	}
	return accounts, total, inserted, nil
}

// scanGrafanaServiceAccountTokens lists the tokens of each given service
// account, keyed {serviceAccountNativeID}/token/{id}.
func scanGrafanaServiceAccountTokens(ctx context.Context, client grafanaAPI, acct *account, region string, st *store.Store, scanID string, accounts []grafanaServiceAccount) (int, int, error) {
	parents := make([]childParent, len(accounts))
	workspaceOf := make(map[string]string, len(accounts))
	for i, a := range accounts {
		parents[i] = childParent{id: a.id, arn: a.nativeID}
		workspaceOf[a.nativeID] = a.workspaceID
	}
	return childFanOut(ctx, st, acct, region, "grafana:ListWorkspaceServiceAccountTokens", parents, isGrafanaNotFound,
		func(ctx context.Context, sa childParent) ([]*store.Resource, error) {
			var rows []*store.Resource
			workspaceID := workspaceOf[sa.arn]
			pager := grafana.NewListWorkspaceServiceAccountTokensPaginator(client, &grafana.ListWorkspaceServiceAccountTokensInput{
				WorkspaceId: &workspaceID, ServiceAccountId: &sa.id,
			})
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if isAPIErrorCode(err, "ConflictException") {
					warnGrafanaConflict(st, "grafana:ListWorkspaceServiceAccountTokens", acct.ID, region,
						"service account "+sa.id+" in workspace "+workspaceID, err)
					return nil, nil
				}
				if err != nil {
					// childFanOut prefixes only the service account id, which repeats across workspaces.
					return nil, fmt.Errorf("in workspace %s: %w", workspaceID, err)
				}
				for _, tok := range out.ServiceAccountTokens {
					id := sv(tok.Id)
					if id == "" {
						continue
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeGrafanaWorkspaceServiceAccountToken, NativeID: sa.arn + "/token/" + id,
						Name: tok.Name, Region: &region, CreatedAt: tp(tok.CreatedAt),
						AttributesJSON: mustJSON(tok), DiscoveredBy: scanID,
					})
				}
			}
			return rows, nil
		})
}

// scanGrafanaPermissions stores one row per user or group granted a role in
// each given workspace, keyed by grafanaPermissionNativeID because the API
// returns no ARN. The row carries the role, so a role change shows as a
// change on that row. ListPermissions models ValidationException; whether a
// workspace that is still being created, or has failed or is being deleted,
// answers with it is unverified, so such a workspace is skipped with one
// warning rather than failing the phase on every scan.
func scanGrafanaPermissions(ctx context.Context, client grafanaAPI, acct *account, region string, st *store.Store, scanID string, workspaces []childParent) (int, int, error) {
	return childFanOutSkipNotReady(ctx, st, acct, region, grafanaListPermissionsOp, workspaces, isGrafanaNotFound, byParentID,
		"workspace(s)", "a workspace that is not active may refuse it",
		func(ctx context.Context, w childParent) ([]*store.Resource, error) {
			var rows []*store.Resource
			pager := grafana.NewListPermissionsPaginator(client, &grafana.ListPermissionsInput{WorkspaceId: &w.id},
				func(o *grafana.ListPermissionsPaginatorOptions) { o.Limit = grafanaPermissionPageLimit })
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, p := range out.Permissions {
					if p.User == nil || sv(p.User.Id) == "" || p.User.Type == "" {
						continue
					}
					userID := sv(p.User.Id)
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeGrafanaPermission, NativeID: grafanaPermissionNativeID(w.arn, p.User.Type, userID),
						Name: &userID, Region: &region,
						AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
					})
				}
			}
			return rows, nil
		})
}

// grafanaPermissionNativeID keys a role grant by its workspace and grantee,
// {workspaceARN}/permission/{userType}/{userID}. The role is left out so that
// a role change is a change on the grantee's row; this assumes, unverified,
// that ListPermissions returns one entry per grantee.
func grafanaPermissionNativeID(workspaceARN string, userType grafanatypes.UserType, userID string) string {
	return workspaceARN + "/permission/" + string(userType) + "/" + userID
}

// isGrafanaNotFound matches a workspace or service account deleted between
// the phase that listed it and the call listing its children.
func isGrafanaNotFound(err error) bool {
	return isAPIErrorCode(err, "ResourceNotFoundException")
}

// warnGrafanaConflict records a ConflictException (modeled on both child list
// ops) from listing parent's children. The parent still exists, so this is
// not a deletion to skip silently: its children are absent from this scan,
// and the warning says so. Safe from fan-out goroutines: ReportWarning only
// calls OnWarn, which the scan runner guards with a mutex.
func warnGrafanaConflict(st *store.Store, op, accountID, region, parent string, err error) {
	st.ReportWarning(store.ScanWarning{
		Provider: "aws",
		Service:  op,
		Scope:    accountID + "/" + region,
		Message:  "conflict: children of " + parent + " not listed this scan: " + err.Error(),
	})
}
