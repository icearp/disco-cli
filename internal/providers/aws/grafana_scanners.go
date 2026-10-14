package aws

import (
	"context"
	"fmt"
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
	registerService(serviceEntry{
		name: "aws:grafana",
		fn:   scanGrafana,
	})
}

type grafanaAPI interface {
	ListWorkspaces(context.Context, *grafana.ListWorkspacesInput, ...func(*grafana.Options)) (*grafana.ListWorkspacesOutput, error)
	ListWorkspaceServiceAccounts(context.Context, *grafana.ListWorkspaceServiceAccountsInput, ...func(*grafana.Options)) (*grafana.ListWorkspaceServiceAccountsOutput, error)
	ListWorkspaceServiceAccountTokens(context.Context, *grafana.ListWorkspaceServiceAccountTokensInput, ...func(*grafana.Options)) (*grafana.ListWorkspaceServiceAccountTokensOutput, error)
}

// grafanaServiceAccount is a service account stored this scan: the token list
// op keys on both ids, nativeID is its row's NativeID.
type grafanaServiceAccount struct{ workspaceID, id, nativeID string }

// scanGrafana discovers Amazon Managed Grafana workspaces, their service
// accounts and the service accounts' tokens (metadata only: the list API
// never returns a token's key).
//
// The service account phases run after the workspace phase, so their failure
// cannot cost the workspace rows.
func scanGrafana(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := grafana.NewFromConfig(acct.cfg, func(o *grafana.Options) { o.Region = region })

	var (
		workspaces []childParent
		accounts   []grafanaServiceAccount
	)
	for _, phase := range []func() (int, int, error){
		func() (t, i int, err error) {
			workspaces, t, i, err = scanGrafanaWorkspaces(ctx, client, acct, region, st, scanID)
			return t, i, err
		},
		func() (t, i int, err error) {
			accounts, t, i, err = scanGrafanaServiceAccounts(ctx, client, acct, region, st, scanID, workspaces)
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
			return total, inserted, ferr
		}
	}
	return total, inserted, nil
}

// scanGrafanaWorkspaces stores every workspace and returns, as service
// account fan-out parents, those whose Grafana version has service accounts.
func scanGrafanaWorkspaces(ctx context.Context, client grafanaAPI, acct *account, region string, st *store.Store, scanID string) ([]childParent, int, int, error) {
	var (
		batch   []*store.Resource
		parents []childParent
	)
	var nextToken *string
	for {
		out, err := client.ListWorkspaces(ctx, &grafana.ListWorkspacesInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "grafana:ListWorkspaces", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("grafana:ListWorkspaces: %w", err)
		}
		for _, w := range out.Workspaces {
			id := sv(w.Id)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:grafana:%s:%s:/workspaces/%s", region, acct.ID, id)
			status := string(w.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGrafanaWorkspace, NativeID: arn,
				Name: w.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(w), DiscoveredBy: scanID,
			})
			if grafanaHasServiceAccounts(sv(w.GrafanaVersion)) && grafanaHasInstance(w.Status) {
				parents = append(parents, childParent{id: id, arn: arn})
			}
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	total, inserted, err := upsertBatch(st, batch, "grafana workspaces")
	if err != nil {
		return nil, 0, 0, err
	}
	return parents, total, inserted, nil
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
