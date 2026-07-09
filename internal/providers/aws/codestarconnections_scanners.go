package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/codestarconnections"
	"github.com/aws/aws-sdk-go-v2/service/codestarconnections/types"
)

func init() {
	registerType(restype.Descriptor{Type: TypeCodeStarConnectionsConnection, Service: "codestar-connections", Upstream: "AWS::CodeStarConnections::Connection", Leaf: true})
	registerType(restype.Descriptor{Type: TypeCodeStarConnectionsHost, Service: "codestar-connections", Upstream: "AWS::codestar-connections::Host"})
	registerType(restype.Descriptor{Type: TypeCodeStarConnectionsRepositoryLink, Service: "codestar-connections", Upstream: "AWS::CodeStarConnections::RepositoryLink"})
	registerType(restype.Descriptor{Type: TypeCodeStarConnectionsSyncConfiguration, Service: "codestar-connections", Upstream: "AWS::CodeStarConnections::SyncConfiguration"})
	registerService(serviceEntry{
		name: "aws:codestar-connections",
		fn:   scanCodeStarConnections,
	})
}

type codeStarConnectionsAPI interface {
	ListConnections(context.Context, *codestarconnections.ListConnectionsInput, ...func(*codestarconnections.Options)) (*codestarconnections.ListConnectionsOutput, error)
	ListHosts(context.Context, *codestarconnections.ListHostsInput, ...func(*codestarconnections.Options)) (*codestarconnections.ListHostsOutput, error)
	ListRepositoryLinks(context.Context, *codestarconnections.ListRepositoryLinksInput, ...func(*codestarconnections.Options)) (*codestarconnections.ListRepositoryLinksOutput, error)
	ListSyncConfigurations(context.Context, *codestarconnections.ListSyncConfigurationsInput, ...func(*codestarconnections.Options)) (*codestarconnections.ListSyncConfigurationsOutput, error)
}

// scanCodeStarConnections discovers third-party SCM connections, repository
// links, and sync configurations. ListSyncConfigurations requires
// (RepositoryLinkId, SyncType) — fan out per scanned link across all known
// sync types.
func scanCodeStarConnections(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := codestarconnections.NewFromConfig(acct.cfg, func(o *codestarconnections.Options) { o.Region = region })

	t, i, ferr := scanCSCConnections(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanCSCHosts(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	linkIDs, t, i, ferr := scanCSCRepositoryLinks(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanCSCSyncConfigurations(ctx, client, linkIDs, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

func scanCSCConnections(ctx context.Context, client codeStarConnectionsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListConnections(ctx, &codestarconnections.ListConnectionsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "codestar-connections:ListConnections", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("codestar-connections:ListConnections: %w", err)
		}
		for _, c := range out.Connections {
			arn := sv(c.ConnectionArn)
			if arn == "" {
				continue
			}
			status := string(c.ConnectionStatus)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCodeStarConnectionsConnection, NativeID: arn,
				Name: c.ConnectionName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	t, i, err := upsertBatch(st, batch, "codestar-connections connections")
	return t, i, err
}

// scanCSCHosts discovers CodeConnections hosts — install endpoints for
// self-managed SCM providers (GitHub Enterprise Server, GitLab self-managed,
// Bitbucket Data Center). A host inside a private network carries a
// VpcConfiguration wired by resolveCSCHostNetwork.
func scanCSCHosts(ctx context.Context, client codeStarConnectionsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListHosts(ctx, &codestarconnections.ListHostsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "codestar-connections:ListHosts", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("codestar-connections:ListHosts: %w", err)
		}
		for _, h := range out.Hosts {
			arn := sv(h.HostArn)
			if arn == "" {
				continue
			}
			status := sv(h.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCodeStarConnectionsHost, NativeID: arn,
				Name: h.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(h), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "codestar-connections hosts")
}

func scanCSCRepositoryLinks(ctx context.Context, client codeStarConnectionsAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	var batch []*store.Resource
	var ids []string
	var nextToken *string
	for {
		out, err := client.ListRepositoryLinks(ctx, &codestarconnections.ListRepositoryLinksInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "codestar-connections:ListRepositoryLinks", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("codestar-connections:ListRepositoryLinks: %w", err)
		}
		for _, r := range out.RepositoryLinks {
			arn := sv(r.RepositoryLinkArn)
			if arn == "" {
				continue
			}
			id := sv(r.RepositoryLinkId)
			if id != "" {
				ids = append(ids, id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCodeStarConnectionsRepositoryLink, NativeID: arn,
				Name: r.RepositoryName, Region: &region,
				AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	t, i, err := upsertBatch(st, batch, "codestar-connections repository-links")
	return ids, t, i, err
}

func scanCSCSyncConfigurations(ctx context.Context, client codeStarConnectionsAPI, linkIDs []string, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	for _, linkID := range linkIDs {
		id := linkID
		for _, syncType := range types.SyncConfigurationType("").Values() {
			st2 := syncType
			var nextToken *string
			for {
				out, err := client.ListSyncConfigurations(ctx, &codestarconnections.ListSyncConfigurationsInput{
					RepositoryLinkId: &id,
					SyncType:         st2,
					NextToken:        nextToken,
				})
				if err != nil {
					if isAccessDenied(err) {
						_ = skipIfAccessDenied(st, "codestar-connections:ListSyncConfigurations", acct.ID, region, err)
						break
					}
					if isAPIErrorCode(err, "ResourceNotFoundException", "ValidationException") {
						break
					}
					return 0, 0, fmt.Errorf("codestar-connections:ListSyncConfigurations link=%s type=%s: %w", id, st2, err)
				}
				for _, sc := range out.SyncConfigurations {
					resName := sv(sc.ResourceName)
					if resName == "" {
						continue
					}
					arn := fmt.Sprintf("arn:aws:codestar-connections:%s:%s:sync-configuration/%s/%s/%s", region, acct.ID, id, st2, resName)
					label := resName
					batch = append(batch, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeCodeStarConnectionsSyncConfiguration, NativeID: arn,
						Name: &label, Region: &region,
						AttributesJSON: mustJSON(sc), DiscoveredBy: scanID,
					})
				}
				if out.NextToken == nil || *out.NextToken == "" {
					break
				}
				nextToken = out.NextToken
			}
		}
	}
	return upsertBatch(st, batch, "codestar-connections sync-configurations")
}
