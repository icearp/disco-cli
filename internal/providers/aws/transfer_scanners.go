package aws

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/transfer"
)

func init() {
	registerType(restype.Descriptor{Type: TypeTransferAgreement, Service: "transfer"})
	registerType(restype.Descriptor{Type: TypeTransferCertificate, Service: "transfer", Leaf: true})
	registerType(restype.Descriptor{Type: TypeTransferConnector, Service: "transfer", Leaf: true})
	registerType(restype.Descriptor{Type: TypeTransferHostKey, Service: "transfer", Upstream: "AWS::transfer::host-key"})
	registerType(restype.Descriptor{Type: TypeTransferProfile, Service: "transfer", Leaf: true})
	registerType(restype.Descriptor{Type: TypeTransferServer, Service: "transfer"})
	registerType(restype.Descriptor{Type: TypeTransferUser, Service: "transfer"})
	registerType(restype.Descriptor{Type: TypeTransferWebApp, Service: "transfer", Leaf: true})
	registerType(restype.Descriptor{Type: TypeTransferWorkflow, Service: "transfer", Leaf: true})
	registerService(serviceEntry{
		name: "aws:transfer",
		fn:   scanTransfer,
	})
}

type transferAPI interface {
	ListAgreements(context.Context, *transfer.ListAgreementsInput, ...func(*transfer.Options)) (*transfer.ListAgreementsOutput, error)
	ListCertificates(context.Context, *transfer.ListCertificatesInput, ...func(*transfer.Options)) (*transfer.ListCertificatesOutput, error)
	ListConnectors(context.Context, *transfer.ListConnectorsInput, ...func(*transfer.Options)) (*transfer.ListConnectorsOutput, error)
	ListHostKeys(context.Context, *transfer.ListHostKeysInput, ...func(*transfer.Options)) (*transfer.ListHostKeysOutput, error)
	ListProfiles(context.Context, *transfer.ListProfilesInput, ...func(*transfer.Options)) (*transfer.ListProfilesOutput, error)
	ListServers(context.Context, *transfer.ListServersInput, ...func(*transfer.Options)) (*transfer.ListServersOutput, error)
	ListUsers(context.Context, *transfer.ListUsersInput, ...func(*transfer.Options)) (*transfer.ListUsersOutput, error)
	ListWebApps(context.Context, *transfer.ListWebAppsInput, ...func(*transfer.Options)) (*transfer.ListWebAppsOutput, error)
	ListWorkflows(context.Context, *transfer.ListWorkflowsInput, ...func(*transfer.Options)) (*transfer.ListWorkflowsOutput, error)
}

func scanTransfer(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := transfer.NewFromConfig(acct.cfg, func(o *transfer.Options) { o.Region = region })

	// Phase 1: servers (collect IDs).
	serverIDs, t, i, ferr := scanTransferServers(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	// Per-server: agreements, users.
	for _, sid := range serverIDs {
		t, i, perr := scanTransferAgreements(ctx, client, acct, region, st, scanID, sid)
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i

		t, i, perr = scanTransferUsers(ctx, client, acct, region, st, scanID, sid)
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i

		t, i, perr = scanTransferHostKeys(ctx, client, acct, region, st, scanID, sid)
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
	}

	// Top-level phases.
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanTransferCertificates(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanTransferConnectors(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanTransferProfiles(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanTransferWebApps(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanTransferWorkflows(ctx, client, acct, region, st, scanID) },
	} {
		t, i, perr := phase()
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanTransferServers(ctx context.Context, client transferAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := transfer.NewListServersPaginator(client, &transfer.ListServersInput{})
	var batch []*store.Resource
	var ids []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "transfer:ListServers", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("transfer:ListServers: %w", perr)
		}
		for _, s := range out.Servers {
			arn := sv(s.Arn)
			if arn == "" {
				continue
			}
			label := sv(s.ServerId)
			if label == "" {
				label = arn
			}
			if id := sv(s.ServerId); id != "" {
				ids = append(ids, id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeTransferServer, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "transfer servers")
	return ids, t, i, err
}

func scanTransferAgreements(ctx context.Context, client transferAPI, acct *account, region string, st *store.Store, scanID, serverID string) (int, int, error) {
	sid := serverID
	pager := transfer.NewListAgreementsPaginator(client, &transfer.ListAgreementsInput{ServerId: &sid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "transfer:ListAgreements", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("transfer:ListAgreements: %w", perr)
		}
		for _, a := range out.Agreements {
			arn := sv(a.Arn)
			if arn == "" {
				continue
			}
			label := sv(a.AgreementId)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeTransferAgreement, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "transfer agreements")
}

func scanTransferUsers(ctx context.Context, client transferAPI, acct *account, region string, st *store.Store, scanID, serverID string) (int, int, error) {
	sid := serverID
	pager := transfer.NewListUsersPaginator(client, &transfer.ListUsersInput{ServerId: &sid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "transfer:ListUsers", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("transfer:ListUsers: %w", perr)
		}
		for _, u := range out.Users {
			arn := sv(u.Arn)
			if arn == "" {
				continue
			}
			label := sv(u.UserName)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeTransferUser, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(u), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "transfer users")
}

func scanTransferCertificates(ctx context.Context, client transferAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := transfer.NewListCertificatesPaginator(client, &transfer.ListCertificatesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "transfer:ListCertificates", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("transfer:ListCertificates: %w", perr)
		}
		for _, c := range out.Certificates {
			arn := sv(c.Arn)
			if arn == "" {
				continue
			}
			label := sv(c.CertificateId)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeTransferCertificate, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "transfer certificates")
}

func scanTransferConnectors(ctx context.Context, client transferAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := transfer.NewListConnectorsPaginator(client, &transfer.ListConnectorsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "transfer:ListConnectors", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("transfer:ListConnectors: %w", perr)
		}
		for _, c := range out.Connectors {
			arn := sv(c.Arn)
			if arn == "" {
				continue
			}
			label := sv(c.ConnectorId)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeTransferConnector, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "transfer connectors")
}

func scanTransferProfiles(ctx context.Context, client transferAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := transfer.NewListProfilesPaginator(client, &transfer.ListProfilesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "transfer:ListProfiles", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("transfer:ListProfiles: %w", perr)
		}
		for _, p := range out.Profiles {
			arn := sv(p.Arn)
			if arn == "" {
				continue
			}
			label := sv(p.ProfileId)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeTransferProfile, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "transfer profiles")
}

func scanTransferWebApps(ctx context.Context, client transferAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := transfer.NewListWebAppsPaginator(client, &transfer.ListWebAppsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "transfer:ListWebApps", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("transfer:ListWebApps: %w", perr)
		}
		for _, w := range out.WebApps {
			arn := sv(w.Arn)
			if arn == "" {
				continue
			}
			label := sv(w.WebAppId)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeTransferWebApp, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(w), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "transfer web-apps")
}

func scanTransferWorkflows(ctx context.Context, client transferAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := transfer.NewListWorkflowsPaginator(client, &transfer.ListWorkflowsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "transfer:ListWorkflows", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("transfer:ListWorkflows: %w", perr)
		}
		for _, w := range out.Workflows {
			arn := sv(w.Arn)
			if arn == "" {
				continue
			}
			label := sv(w.WorkflowId)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeTransferWorkflow, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(w), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "transfer workflows")
}

// scanTransferHostKeys lists the SSH host keys of one server. ListHostKeys is
// per-server (no account-wide list op) with no SDK paginator — pages manually
// via NextToken.
func scanTransferHostKeys(ctx context.Context, client transferAPI, acct *account, region string, st *store.Store, scanID, serverID string) (int, int, error) {
	sid := serverID
	var batch []*store.Resource
	var token *string
	for {
		out, perr := client.ListHostKeys(ctx, &transfer.ListHostKeysInput{ServerId: &sid, NextToken: token})
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "transfer:ListHostKeys", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("transfer:ListHostKeys: %w", perr)
		}
		for _, h := range out.HostKeys {
			arn := sv(h.Arn)
			if arn == "" {
				continue
			}
			label := sv(h.HostKeyId)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeTransferHostKey, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(h), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || sv(out.NextToken) == "" {
			break
		}
		token = out.NextToken
	}
	return upsertBatch(st, batch, "transfer host-keys")
}
