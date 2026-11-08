package aws

import (
	"context"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/codestarconnections"
	"github.com/aws/aws-sdk-go-v2/service/codestarconnections/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeCodeStarConnectionsConnection, Service: "codestar-connections"})
	registerType(restype.Descriptor{Type: TypeCodeStarConnectionsHost, Service: "codestar-connections"})
	registerType(restype.Descriptor{Type: TypeCodeStarConnectionsRepositoryLink, Service: "codestar-connections"})
	registerType(restype.Descriptor{Type: TypeCodeStarConnectionsSyncConfiguration, Service: "codestar-connections"})
	registerService(serviceEntry{
		name: "aws:codestar-connections",
		fn:   scanCodeStarConnections,
	})
}

const (
	cscListConnectionsOp        = "codestar-connections:ListConnections"
	cscListHostsOp              = "codestar-connections:ListHosts"
	cscListRepositoryLinksOp    = "codestar-connections:ListRepositoryLinks"
	cscListSyncConfigurationsOp = "codestar-connections:ListSyncConfigurations"
	cscListTagsForResourceOp    = "codestar-connections:ListTagsForResource"
)

// cscPageSize is the MaxResults every listing sends: the model's maximum
// (range 0-100) for all four.
const cscPageSize = 100

// cscGone is the not-found code ListTagsForResource models, for a resource
// deleted since it was listed, and the one ListSyncConfigurations models for
// a repository link deleted since it was listed.
var cscGone = []string{"ResourceNotFoundException"}

type codeStarConnectionsAPI interface {
	ListConnections(context.Context, *codestarconnections.ListConnectionsInput, ...func(*codestarconnections.Options)) (*codestarconnections.ListConnectionsOutput, error)
	ListHosts(context.Context, *codestarconnections.ListHostsInput, ...func(*codestarconnections.Options)) (*codestarconnections.ListHostsOutput, error)
	ListRepositoryLinks(context.Context, *codestarconnections.ListRepositoryLinksInput, ...func(*codestarconnections.Options)) (*codestarconnections.ListRepositoryLinksOutput, error)
	ListSyncConfigurations(context.Context, *codestarconnections.ListSyncConfigurationsInput, ...func(*codestarconnections.Options)) (*codestarconnections.ListSyncConfigurationsOutput, error)
	ListTagsForResource(context.Context, *codestarconnections.ListTagsForResourceInput, ...func(*codestarconnections.Options)) (*codestarconnections.ListTagsForResourceOutput, error)
}

func scanCodeStarConnections(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, error) {
	client := codestarconnections.NewFromConfig(acct.cfg, func(o *codestarconnections.Options) { o.Region = region })
	return scanCodeStarConnectionsAll(ctx, client, acct, region, st, scanID)
}

// scanCodeStarConnectionsAll discovers third-party SCM connections, hosts,
// repository links and the sync configurations of the links stored this
// scan, one phase at a time. A phase's error does not stop the later ones; a
// failed write and ctx's end do, and so does an NXDOMAIN of the first listing
// with nothing stored (the region does not serve the service)
// (runHostPhases). Connections, hosts and repository links have their tags
// read per row (cscTags), whose denials are one warning for the scan.
func scanCodeStarConnectionsAll(ctx context.Context, client codeStarConnectionsAPI, acct *account, region string, st *store.Store, scanID string) (int, error) {
	tags := cscTags(client).sharingDenials()
	var links []childParent
	total, err := runHostPhases(ctx, st, acct, region, "codestar-connections", 0, []hostPhase{
		{op: cscListConnectionsOp, run: func(p *phaseTotals) bool {
			return scanCSCConnections(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: cscListHostsOp, run: func(p *phaseTotals) bool {
			return scanCSCHosts(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: cscListRepositoryLinksOp, run: func(p *phaseTotals) (failed bool) {
			links, failed = scanCSCRepositoryLinks(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		countedPhase(0, cscListSyncConfigurationsOp, func() (int, error) {
			return scanCSCSyncConfigurations(ctx, client, acct, region, st, scanID, links)
		}),
	})
	tags.warnDenials(st, acct, region)
	return total, err
}

// cscTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID: the ARN its listing returned. The Service Reference lists
// Connection, Host and RepositoryLink, the three types read here, under
// ListTagsForResource and TagResource. A sync configuration has no ARN, no
// Service Reference resource and no Tags on CreateSyncConfiguration, so it
// is not read. The op does not page its tags. ResourceNotFoundException, its
// only modeled error, drops a resource deleted after it was listed.
func cscTags(client codeStarConnectionsAPI) rowTagReader {
	return rowTagReader{
		op:       cscListTagsForResourceOp,
		notFound: cscGone,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			out, err := client.ListTagsForResource(ctx, &codestarconnections.ListTagsForResourceInput{ResourceArn: sdkaws.String(r.NativeID)})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(out.Tags, func(t types.Tag) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}

// scanCSCConnections stores the connections with their tags, a page at a
// time. A connection's OwnerAccountId is not an owner signal: the model
// documents it as the third-party provider's identifier (for Bitbucket, the
// repository owner's account id), not an AWS account. The model documents no
// cross-account sharing of connections and RAM lists no codestar-connections
// resource type, so no listed connection is gated as another account's. It
// reports whether a write failed, after which the scan must stop.
func scanCSCConnections(ctx context.Context, client codeStarConnectionsAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, cscListConnectionsOp,
		func(ctx context.Context, token *string) ([]types.Connection, *string, error) {
			out, err := client.ListConnections(ctx, &codestarconnections.ListConnectionsInput{MaxResults: cscPageSize, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Connections, out.NextToken, nil
		},
		func(c types.Connection) (*store.Resource, string, string) {
			status := string(c.ConnectionStatus)
			return arnRow("connections", c.ConnectionArn, c.ConnectionName, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCodeStarConnectionsConnection, NativeID: sv(c.ConnectionArn),
				Name: c.ConnectionName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		},
		tags, p)
}

// scanCSCHosts stores the hosts with their tags, a page at a time: install
// endpoints for self-managed SCM providers (GitHub Enterprise Server, GitLab
// self-managed, Bitbucket Data Center). A host inside a private network
// carries a VpcConfiguration wired by resolveCSCHostNetwork. It reports
// whether a write failed, after which the scan must stop.
func scanCSCHosts(ctx context.Context, client codeStarConnectionsAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, cscListHostsOp,
		func(ctx context.Context, token *string) ([]types.Host, *string, error) {
			out, err := client.ListHosts(ctx, &codestarconnections.ListHostsInput{MaxResults: cscPageSize, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Hosts, out.NextToken, nil
		},
		func(h types.Host) (*store.Resource, string, string) {
			status := sv(h.Status)
			return arnRow("hosts", h.HostArn, h.Name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCodeStarConnectionsHost, NativeID: sv(h.HostArn),
				Name: h.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(h), DiscoveredBy: scanID,
			})
		},
		tags, p)
}

// scanCSCRepositoryLinks stores the repository links with their tags, a page
// at a time, and returns those stored as the parents of the sync
// configuration phase, each keyed by its RepositoryLinkId. It reports
// whether a write failed, after which the scan must stop.
func scanCSCRepositoryLinks(ctx context.Context, client codeStarConnectionsAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentPagesTags(ctx, st, acct, region, cscListRepositoryLinksOp, "repository links", "RepositoryLinkId",
		func(ctx context.Context, token *string) ([]types.RepositoryLinkInfo, *string, error) {
			out, err := client.ListRepositoryLinks(ctx, &codestarconnections.ListRepositoryLinksInput{MaxResults: cscPageSize, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.RepositoryLinks, out.NextToken, nil
		},
		func(r types.RepositoryLinkInfo) (*store.Resource, string, string) {
			return arnRow("repository links", r.RepositoryLinkArn, r.RepositoryName, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCodeStarConnectionsRepositoryLink, NativeID: sv(r.RepositoryLinkArn),
				Name: r.RepositoryName, Region: &region,
				AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		},
		func(r types.RepositoryLinkInfo) string { return sv(r.RepositoryLinkId) },
		tags, p)
}

// cscSyncListing is one ListSyncConfigurations listing: the op requires both
// a repository link and a sync type.
type cscSyncListing struct {
	linkID   string
	syncType types.SyncConfigurationType
}

// scanCSCSyncConfigurations stores the sync configurations of every
// repository link in links (those stored this scan), one listing per link and
// sync type, the listings read concurrently (storeTokenEachParent). A link
// deleted since it was listed (ResourceNotFoundException, modeled) is skipped;
// a denial warns once; any other error, InvalidInputException included, is
// returned after the rows are stored. A sync configuration has no ARN; its
// NativeID is built as
// arn:{partition}:codestar-connections:{region}:{account}:sync-configuration/{linkId}/{syncType}/{resourceName}.
func scanCSCSyncConfigurations(ctx context.Context, client codeStarConnectionsAPI, acct *account, region string, st *store.Store, scanID string, links []childParent) (int, error) {
	listings := map[string]cscSyncListing{}
	var keys []string
	for _, l := range links {
		for _, syncType := range types.SyncConfigurationType("").Values() {
			key := l.id + "/" + string(syncType)
			listings[key] = cscSyncListing{linkID: l.id, syncType: syncType}
			keys = append(keys, key)
		}
	}
	return storeTokenEachParent(ctx, st, acct, region, cscListSyncConfigurationsOp, cscGone, keys,
		func(ctx context.Context, key string, token *string) ([]types.SyncConfiguration, *string, error) {
			l := listings[key]
			out, err := client.ListSyncConfigurations(ctx, &codestarconnections.ListSyncConfigurationsInput{
				RepositoryLinkId: &l.linkID, SyncType: l.syncType, MaxResults: cscPageSize, NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.SyncConfigurations, out.NextToken, nil
		},
		func(key string, sc types.SyncConfiguration) (*store.Resource, string, string) {
			resName := sv(sc.ResourceName)
			if resName == "" {
				return nil, "sync configurations have no ResourceName", key
			}
			l := listings[key]
			arn := arnFor(acct.partition(), "codestar-connections", region, acct.ID, "sync-configuration/"+l.linkID+"/"+string(l.syncType)+"/"+resName)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCodeStarConnectionsSyncConfiguration, NativeID: arn,
				Name: &resName, Region: &region,
				AttributesJSON: mustJSON(sc), DiscoveredBy: scanID,
			}, "", ""
		})
}
