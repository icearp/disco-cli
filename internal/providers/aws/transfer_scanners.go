package aws

import (
	"cmp"
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
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
	DescribeWebApp(context.Context, *transfer.DescribeWebAppInput, ...func(*transfer.Options)) (*transfer.DescribeWebAppOutput, error)
	ListAccesses(context.Context, *transfer.ListAccessesInput, ...func(*transfer.Options)) (*transfer.ListAccessesOutput, error)
	ListAgreements(context.Context, *transfer.ListAgreementsInput, ...func(*transfer.Options)) (*transfer.ListAgreementsOutput, error)
	ListCertificates(context.Context, *transfer.ListCertificatesInput, ...func(*transfer.Options)) (*transfer.ListCertificatesOutput, error)
	ListConnectors(context.Context, *transfer.ListConnectorsInput, ...func(*transfer.Options)) (*transfer.ListConnectorsOutput, error)
	ListHostKeys(context.Context, *transfer.ListHostKeysInput, ...func(*transfer.Options)) (*transfer.ListHostKeysOutput, error)
	ListProfiles(context.Context, *transfer.ListProfilesInput, ...func(*transfer.Options)) (*transfer.ListProfilesOutput, error)
	ListServers(context.Context, *transfer.ListServersInput, ...func(*transfer.Options)) (*transfer.ListServersOutput, error)
	ListUsers(context.Context, *transfer.ListUsersInput, ...func(*transfer.Options)) (*transfer.ListUsersOutput, error)
	ListTagsForResource(context.Context, *transfer.ListTagsForResourceInput, ...func(*transfer.Options)) (*transfer.ListTagsForResourceOutput, error)
	ListWebApps(context.Context, *transfer.ListWebAppsInput, ...func(*transfer.Options)) (*transfer.ListWebAppsOutput, error)
	ListWorkflows(context.Context, *transfer.ListWorkflowsInput, ...func(*transfer.Options)) (*transfer.ListWorkflowsOutput, error)
}

// scanTransfer discovers the Transfer Family resources of one region. Every
// listing runs as its own phase, one at a time: a phase's error does not stop
// the later ones; a failed write and ctx's end do (runHostPhases). The
// per-server phases (agreements, users, host keys, accesses) read the servers
// the server phase stored this scan, those servers concurrently. Every type
// but access has its tags read per row: web apps with DescribeWebApp
// (transferWebAppTags), the others with ListTagsForResource (transferTags),
// whose denials are one warning for the whole scan.
func scanTransfer(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := transfer.NewFromConfig(acct.cfg, func(o *transfer.Options) { o.Region = region })
	tags := transferTags(client).sharingDenials()
	var servers []transferServer
	total, inserted, err := runHostPhases(ctx, st, acct, region, "transfer", 0, []hostPhase{
		{op: "transfer:ListServers", run: func(p *phaseTotals) (failed bool) {
			servers, failed = scanTransferServers(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: "transfer:ListAgreements", run: func(p *phaseTotals) bool {
			return scanTransferAgreements(ctx, client, acct, region, st, scanID, transferParents(servers), tags, p)
		}},
		{op: "transfer:ListUsers", run: func(p *phaseTotals) bool {
			return scanTransferUsers(ctx, client, acct, region, st, scanID, transferParents(servers), tags, p)
		}},
		{op: "transfer:ListHostKeys", run: func(p *phaseTotals) bool {
			return scanTransferHostKeys(ctx, client, acct, region, st, scanID, transferParents(servers), tags, p)
		}},
		{op: "transfer:ListCertificates", run: func(p *phaseTotals) bool {
			return scanTransferCertificates(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "transfer:ListConnectors", run: func(p *phaseTotals) bool {
			return scanTransferConnectors(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "transfer:ListProfiles", run: func(p *phaseTotals) bool {
			return scanTransferProfiles(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "transfer:ListWebApps", run: func(p *phaseTotals) bool {
			return scanTransferWebApps(ctx, client, acct, region, st, scanID, p)
		}},
		{op: "transfer:ListWorkflows", run: func(p *phaseTotals) bool {
			return scanTransferWorkflows(ctx, client, acct, region, st, scanID, tags, p)
		}},
		countedPhase(0, transferListAccessesOp, func() (int, int, error) {
			return scanTransferAccesses(ctx, client, acct, region, st, scanID, transferDirectoryServers(servers))
		}),
	})
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

const (
	transferTagsOp = "transfer:ListTagsForResource"
	// transferPageCap is the MaxResults maximum of every Transfer Family
	// listing read here and of ListTagsForResource.
	transferPageCap = 1000
	// transferNoIdentifier names, in a warning, a listed element with neither
	// an ARN nor an id.
	transferNoIdentifier = "(an element with no identifier)"
)

// transferTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID, the ARN its listing returned. The Service Reference lists every
// ARN resource type read here under ListTagsForResource (agreement,
// certificate, connector, host-key, profile, server, user, workflow); it
// does not list webapp (transferWebAppTags). The op pages its tags; every
// page is read, and a repeated NextToken fails the read.
//
// The op models no not-found code, so a resource deleted between its
// listing and its tag read fails the read as an error, and its row is kept
// untagged.
func transferTags(client transferAPI) rowTagReader {
	return rowTagReader{
		op: transferTagsOp,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			arn := r.NativeID
			tags, err := readTokenPages(ctx, func(ctx context.Context, token *string) ([]transfertypes.Tag, *string, error) {
				out, err := client.ListTagsForResource(ctx, &transfer.ListTagsForResourceInput{Arn: &arn, MaxResults: aws.Int32(transferPageCap), NextToken: token})
				if err != nil {
					return nil, nil, err
				}
				return out.Tags, out.NextToken, nil
			})
			if err != nil {
				return nil, err
			}
			return transferTagsJSON(tags), nil
		},
	}
}

// transferWebAppTags reads a web app's tags from DescribeWebApp, sent the
// WebAppId ids holds for its row. The Service Reference lists webapp under
// TagResource but not under ListTagsForResource, while DescribeWebApp is
// authorized on webapp and returns its Tags. The row stays the listed
// element. A web app DescribeWebApp no longer finds (ResourceNotFoundException,
// which it models) was deleted since it was listed, and its row is dropped.
func transferWebAppTags(client transferAPI, ids map[*store.Resource]string) rowTagReader {
	return rowTagReader{
		op:       "transfer:DescribeWebApp",
		notFound: []string{"ResourceNotFoundException"},
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			id := ids[r]
			if id == "" {
				return nil, fmt.Errorf("web app %s has no WebAppId to describe", r.NativeID)
			}
			out, err := client.DescribeWebApp(ctx, &transfer.DescribeWebAppInput{WebAppId: &id})
			if err != nil {
				return nil, err
			}
			if out.WebApp == nil {
				return nil, fmt.Errorf("DescribeWebApp %s returned no WebApp", id)
			}
			return transferTagsJSON(out.WebApp.Tags), nil
		},
	}
}

func transferTagsJSON(tags []transfertypes.Tag) *string {
	return kvTagsJSON(tags, func(t transfertypes.Tag) (*string, *string) { return t.Key, t.Value })
}

// transferRow is the row of a listed element stored under its ARN and named
// by its id, or by its ARN when it has no id. An element without an ARN is
// not stored: one empty NativeID would merge every such
// element into one resource. what names the elements in that warning.
func transferRow(acct *account, region, scanID, typ, what string, arn, id *string, element any) (*store.Resource, string, string) {
	a, name := sv(arn), sv(id)
	if a == "" {
		return nil, what + " have no ARN", cmp.Or(name, transferNoIdentifier)
	}
	label := cmp.Or(name, a)
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: a,
		Name: &label, Region: &region, AttributesJSON: mustJSON(element), DiscoveredBy: scanID,
	}, "", ""
}

// transferServer is a stored server with the identity provider type that
// decides whether it can have accesses.
type transferServer struct {
	childParent
	identityProvider transfertypes.IdentityProviderType
}

// transferParents is the servers as the parents their child listings key on.
func transferParents(servers []transferServer) []childParent {
	parents := make([]childParent, len(servers))
	for i, s := range servers {
		parents[i] = s.childParent
	}
	return parents
}

// scanTransferServers stores the listed servers with their tags and returns
// the stored ones that have a ServerId, which the per-server phases key on.
// The servers are few (a per-region quota), so their listing is held whole.
// It reports whether the write failed, after which the scanner must stop.
func scanTransferServers(ctx context.Context, client transferAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]transferServer, bool) {
	idp := map[string]transfertypes.IdentityProviderType{}
	parents, failed := storeParentListTags(ctx, st, acct, region, "transfer:ListServers", "Transfer Family servers", "ServerId",
		func(ctx context.Context, token *string) ([]transfertypes.ListedServer, *string, error) {
			out, err := client.ListServers(ctx, &transfer.ListServersInput{MaxResults: aws.Int32(transferPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Servers, out.NextToken, nil
		},
		func(s transfertypes.ListedServer) (*store.Resource, string, string) {
			return transferRow(acct, region, scanID, TypeTransferServer, "servers", s.Arn, s.ServerId, s)
		},
		func(s transfertypes.ListedServer) string {
			idp[sv(s.ServerId)] = s.IdentityProviderType
			return sv(s.ServerId)
		},
		tags, p)
	servers := make([]transferServer, len(parents))
	for i, s := range parents {
		servers[i] = transferServer{s, idp[s.id]}
	}
	return servers, failed
}

// transferServerGone is the code ListAgreements, ListUsers and ListHostKeys
// model for a server deleted since the server phase listed it.
var transferServerGone = []string{"ResourceNotFoundException"}

// scanTransferAgreements stores each server's agreements with their tags;
// one server's failure costs the others none of theirs.
func scanTransferAgreements(ctx context.Context, client transferAPI, acct *account, region string, st *store.Store, scanID string, servers []childParent, tags rowTagReader, p *phaseTotals) bool {
	_, failed := storeEachParentTags(ctx, st, acct, region, "transfer:ListAgreements", transferServerGone, servers,
		func(ctx context.Context, serverID string, token *string) ([]transfertypes.ListedAgreement, *string, error) {
			out, err := client.ListAgreements(ctx, &transfer.ListAgreementsInput{ServerId: &serverID, MaxResults: aws.Int32(transferPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Agreements, out.NextToken, nil
		},
		func(_ string, a transfertypes.ListedAgreement) (*store.Resource, string, string) {
			return transferRow(acct, region, scanID, TypeTransferAgreement, "agreements", a.Arn, a.AgreementId, a)
		},
		tags, p)
	return failed
}

// scanTransferUsers stores each server's users with their tags. A server can
// hold many users, so each page is written as soon as its tags are read
// (storeEachParentPagesTags); one server's failure costs the others none of
// theirs.
func scanTransferUsers(ctx context.Context, client transferAPI, acct *account, region string, st *store.Store, scanID string, servers []childParent, tags rowTagReader, p *phaseTotals) bool {
	return storeEachParentPagesTags(ctx, st, acct, region, "transfer:ListUsers", transferServerGone, servers,
		func(ctx context.Context, serverID string, token *string) ([]transfertypes.ListedUser, *string, error) {
			out, err := client.ListUsers(ctx, &transfer.ListUsersInput{ServerId: &serverID, MaxResults: aws.Int32(transferPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Users, out.NextToken, nil
		},
		func(_ string, u transfertypes.ListedUser) (*store.Resource, string, string) {
			return transferRow(acct, region, scanID, TypeTransferUser, "users", u.Arn, u.UserName, u)
		},
		tags, p)
}

// scanTransferHostKeys stores each server's SSH host keys with their tags.
// ListHostKeys is per server (no account-wide list op) and has no SDK
// paginator; one server's failure costs the others none of theirs.
func scanTransferHostKeys(ctx context.Context, client transferAPI, acct *account, region string, st *store.Store, scanID string, servers []childParent, tags rowTagReader, p *phaseTotals) bool {
	_, failed := storeEachParentTags(ctx, st, acct, region, "transfer:ListHostKeys", transferServerGone, servers,
		func(ctx context.Context, serverID string, token *string) ([]transfertypes.ListedHostKey, *string, error) {
			out, err := client.ListHostKeys(ctx, &transfer.ListHostKeysInput{ServerId: &serverID, MaxResults: aws.Int32(transferPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.HostKeys, out.NextToken, nil
		},
		func(_ string, h transfertypes.ListedHostKey) (*store.Resource, string, string) {
			return transferRow(acct, region, scanID, TypeTransferHostKey, "host keys", h.Arn, h.HostKeyId, h)
		},
		tags, p)
	return failed
}

func scanTransferCertificates(ctx context.Context, client transferAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "transfer:ListCertificates",
		func(ctx context.Context, token *string) ([]transfertypes.ListedCertificate, *string, error) {
			out, err := client.ListCertificates(ctx, &transfer.ListCertificatesInput{MaxResults: aws.Int32(transferPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Certificates, out.NextToken, nil
		},
		func(c transfertypes.ListedCertificate) (*store.Resource, string, string) {
			return transferRow(acct, region, scanID, TypeTransferCertificate, "certificates", c.Arn, c.CertificateId, c)
		},
		tags, p)
}

func scanTransferConnectors(ctx context.Context, client transferAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "transfer:ListConnectors",
		func(ctx context.Context, token *string) ([]transfertypes.ListedConnector, *string, error) {
			out, err := client.ListConnectors(ctx, &transfer.ListConnectorsInput{MaxResults: aws.Int32(transferPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Connectors, out.NextToken, nil
		},
		func(c transfertypes.ListedConnector) (*store.Resource, string, string) {
			return transferRow(acct, region, scanID, TypeTransferConnector, "connectors", c.Arn, c.ConnectorId, c)
		},
		tags, p)
}

func scanTransferProfiles(ctx context.Context, client transferAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "transfer:ListProfiles",
		func(ctx context.Context, token *string) ([]transfertypes.ListedProfile, *string, error) {
			out, err := client.ListProfiles(ctx, &transfer.ListProfilesInput{MaxResults: aws.Int32(transferPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Profiles, out.NextToken, nil
		},
		func(pr transfertypes.ListedProfile) (*store.Resource, string, string) {
			return transferRow(acct, region, scanID, TypeTransferProfile, "profiles", pr.Arn, pr.ProfileId, pr)
		},
		tags, p)
}

// scanTransferWebApps stores the web apps with the tags DescribeWebApp
// returns (transferWebAppTags). ids is written while a page's rows are built
// and read only by that page's tag reads, which start after. It is cleared
// at each page fetch, which follows the previous page's write, so it holds no
// row already written.
func scanTransferWebApps(ctx context.Context, client transferAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals) bool {
	ids := map[*store.Resource]string{}
	return storeTokenPagesTags(ctx, st, acct, region, "transfer:ListWebApps",
		func(ctx context.Context, token *string) ([]transfertypes.ListedWebApp, *string, error) {
			clear(ids)
			out, err := client.ListWebApps(ctx, &transfer.ListWebAppsInput{MaxResults: aws.Int32(transferPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.WebApps, out.NextToken, nil
		},
		func(w transfertypes.ListedWebApp) (*store.Resource, string, string) {
			r, why, key := transferRow(acct, region, scanID, TypeTransferWebApp, "web apps", w.Arn, w.WebAppId, w)
			if r != nil {
				ids[r] = sv(w.WebAppId)
			}
			return r, why, key
		},
		transferWebAppTags(client, ids), p)
}

func scanTransferWorkflows(ctx context.Context, client transferAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "transfer:ListWorkflows",
		func(ctx context.Context, token *string) ([]transfertypes.ListedWorkflow, *string, error) {
			out, err := client.ListWorkflows(ctx, &transfer.ListWorkflowsInput{MaxResults: aws.Int32(transferPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Workflows, out.NextToken, nil
		},
		func(w transfertypes.ListedWorkflow) (*store.Resource, string, string) {
			return transferRow(acct, region, scanID, TypeTransferWorkflow, "workflows", w.Arn, w.WorkflowId, w)
		},
		tags, p)
}

const (
	transferListAccessesOp   = "transfer:ListAccesses"
	transferDescribeAccessOp = "transfer:DescribeAccess"
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
			o.Limit = transferPageCap
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
