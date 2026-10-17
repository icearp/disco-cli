package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/transfer"
	transfertypes "github.com/aws/aws-sdk-go-v2/service/transfer/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeTransferAccess, Service: "transfer"})
	registerType(restype.Descriptor{Type: TypeTransferAgreement, Service: "transfer"})
	registerType(restype.Descriptor{Type: TypeTransferCertificate, Service: "transfer"})
	registerType(restype.Descriptor{Type: TypeTransferConnector, Service: "transfer"})
	registerType(restype.Descriptor{Type: TypeTransferHostKey, Service: "transfer"})
	registerType(restype.Descriptor{Type: TypeTransferProfile, Service: "transfer"})
	registerType(restype.Descriptor{Type: TypeTransferServer, Service: "transfer"})
	registerType(restype.Descriptor{Type: TypeTransferUser, Service: "transfer"})
	registerType(restype.Descriptor{Type: TypeTransferWebApp, Service: "transfer"})
	registerType(restype.Descriptor{Type: TypeTransferWorkflow, Service: "transfer"})
	registerService(serviceEntry{
		name: "aws:transfer",
		fn:   scanTransfer,
	})
}

type transferAPI interface {
	DescribeAccess(context.Context, *transfer.DescribeAccessInput, ...func(*transfer.Options)) (*transfer.DescribeAccessOutput, error)
	ListAccesses(context.Context, *transfer.ListAccessesInput, ...func(*transfer.Options)) (*transfer.ListAccessesOutput, error)
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

	// Phase 1: servers.
	servers, t, i, ferr := scanTransferServers(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	// Per-server: agreements, users.
	for _, srv := range servers {
		sid := srv.id
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

	// Accesses run last so that their failure cannot keep an older phase from running.
	t, i, err = scanTransferAccesses(ctx, client, acct, region, st, scanID, transferDirectoryServers(servers))
	return total + t, inserted + i, err
}

// transferServer is a listed server with the identity provider type that
// decides whether it can have accesses.
type transferServer struct {
	childParent
	identityProvider transfertypes.IdentityProviderType
}

// scanTransferServers stores the listed servers and returns those with a
// ServerId, which the per-server phases key on. Every returned server is in
// the batch it upserts, so its row is stored when it returns without error.
func scanTransferServers(ctx context.Context, client transferAPI, acct *account, region string, st *store.Store, scanID string) ([]transferServer, int, int, error) {
	pager := transfer.NewListServersPaginator(client, &transfer.ListServersInput{})
	var batch []*store.Resource
	var servers []transferServer
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
				servers = append(servers, transferServer{childParent{id: id, arn: arn}, s.IdentityProviderType})
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeTransferServer, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "transfer servers")
	return servers, t, i, err
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

const (
	transferListAccessesOp   = "transfer:ListAccesses"
	transferDescribeAccessOp = "transfer:DescribeAccess"
	// transferAccessPageLimit is the model's MaxResults cap for ListAccesses.
	transferAccessPageLimit = 1000
)

// transferAccessSkipCodes are the codes the access phase skips per server with
// one warning. ListServers also lists servers that are OFFLINE, STARTING,
// STOPPING, START_FAILED or STOP_FAILED; whether ListAccesses refuses such a
// server has not been observed, and failing the phase would fail it on every
// scan while the server stays in that state. The inputs disco sets are the
// listed server id, an ExternalId ListAccesses returned and a page size pinned
// to the model cap by a test, so the code is not expected to name a disco input.
var transferAccessSkipCodes = []skipCode{
	{code: "InvalidRequestException", why: "a server that is not ONLINE may be refused"},
}

// transferDirectoryServers returns the servers that may have accesses: those
// whose identity provider is AWS_DIRECTORY_SERVICE, and those whose type the
// listing omitted. An access grants a directory group, keyed by its SID,
// access to a server (CreateAccess and ExternalId docs), so a server with any
// other identity provider has no accesses to list. IdentityProviderType is not
// required on ListedServer, so an omitted type is unknown rather than
// non-directory. Listing such a server is expected (not observed) to return an
// empty list or a skipped code when it is not a directory server, while
// leaving it out would drop any accesses it has without a warning.
func transferDirectoryServers(servers []transferServer) []childParent {
	var out []childParent
	for _, s := range servers {
		switch s.identityProvider {
		case transfertypes.IdentityProviderTypeAwsDirectoryService, "":
			out = append(out, s.childParent)
		}
	}
	return out
}

// transferAccessNativeID keys an access, which has no ARN, under its server's
// ARN by its ExternalId: a group SID, which the model's pattern
// (^S-1-[\d-]+$) keeps free of "/".
func transferAccessNativeID(serverARN, externalID string) string {
	return serverARN + "/access/" + externalID
}

// isTransferGone reports whether err says the server or access no longer
// exists, i.e. it was deleted after the list call that named it.
func isTransferGone(err error) bool {
	return isAPIErrorCode(err, "ResourceNotFoundException")
}

// scanTransferAccesses stores the accesses of each server, linked under it.
// servers must already be stored. A gone server is skipped silently, a denial
// warns once, a transient error after the SDK's retries or a
// transferAccessSkipCodes code skips the server with one warning, and anything
// else fails the phase. Rows a server stored before its error stay and are
// counted.
func scanTransferAccesses(ctx context.Context, client transferAPI, acct *account, region string, st *store.Store, scanID string, servers []childParent) (int, int, error) {
	describeErrs := &fanOutErrs{st: st, acct: acct, region: region, op: transferDescribeAccessOp, isSkip: isTransferGone}
	return childFanOutStoredSkipCodes(ctx, st, acct, region, transferListAccessesOp, servers, isTransferGone, byParentID,
		"Transfer Family server(s)", transferAccessSkipCodes,
		func(ctx context.Context, srv childParent) (int, int, error) {
			return storeTransferAccesses(ctx, client, acct, region, st, scanID, srv, describeErrs)
		})
}

// storeTransferAccesses stores one server's accesses a page at a time.
// ListAccesses omits the session policy, POSIX profile and logical directory
// mappings, so each access is described; an access that is gone or denied at
// DescribeAccess is dropped per describeErrs.
//
// Any other DescribeAccess error ends the server after the accesses described
// before it are stored. scanTransferAccesses then skips or fails the server as
// for a ListAccesses error, so a skip warning is filed under
// transfer:ListAccesses (the fan-out whose servers it counts); its message
// carries the error, which describeErrs has prefixed with
// transfer:DescribeAccess and the access key.
func storeTransferAccesses(ctx context.Context, client transferAPI, acct *account, region string, st *store.Store, scanID string,
	srv childParent, describeErrs *fanOutErrs,
) (total, inserted int, err error) {
	pager := transfer.NewListAccessesPaginator(client, &transfer.ListAccessesInput{ServerId: &srv.id},
		func(o *transfer.ListAccessesPaginatorOptions) {
			o.Limit = transferAccessPageLimit
			o.StopOnDuplicateToken = true
		})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			return total, inserted, err
		}
		children, derr := describeTransferAccesses(ctx, client, acct, region, scanID, srv, out.Accesses, describeErrs)
		t, i, uerr := upsertContained(st, transferListAccessesOp, children)
		total += t
		inserted += i
		// A failed write is returned alone: joined with a transient describe
		// error, the fan-out would skip it as transient.
		if uerr != nil {
			return total, inserted, uerr
		}
		if derr != nil {
			return total, inserted, derr
		}
	}
	return total, inserted, nil
}

// describeTransferAccesses describes the listed accesses of one server and
// returns their rows. It stops at the first error describeErrs does not drop
// and returns that error with the rows built before it.
func describeTransferAccesses(ctx context.Context, client transferAPI, acct *account, region, scanID string,
	srv childParent, listed []transfertypes.ListedAccess, describeErrs *fanOutErrs,
) ([]containedChild, error) {
	parentID := store.ResourceID("aws", acct.ID, srv.arn)
	var children []containedChild
	for _, l := range listed {
		externalID := sv(l.ExternalId)
		if externalID == "" {
			continue
		}
		d, err := client.DescribeAccess(ctx, &transfer.DescribeAccessInput{ServerId: &srv.id, ExternalId: &externalID})
		if err != nil {
			if kerr := describeErrs.keyErr(srv.id+"/"+externalID, err); kerr != nil {
				return children, kerr
			}
			continue
		}
		children = append(children, containedChild{row: &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeTransferAccess, NativeID: transferAccessNativeID(srv.arn, externalID),
			Name: &externalID, Region: &region, AttributesJSON: mustJSON(d.Access), DiscoveredBy: scanID,
		}, parentID: parentID})
	}
	return children, nil
}
