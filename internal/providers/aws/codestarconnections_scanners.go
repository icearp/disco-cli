package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/codestarconnections"
	"github.com/aws/aws-sdk-go-v2/service/codestarconnections/types"
)

func init() {
	registerService(serviceEntry{
		name: "aws:codestar-connections",
		fn:   scanCodeStarConnections,
		emits: []coverage.TypeDecl{
			{Service: "codestar-connections", DiscoType: TypeCodeStarConnectionsConnection, Leaf: true},
			{Service: "codestar-connections", DiscoType: TypeCodeStarConnectionsRepositoryLink},
			{Service: "codestar-connections", DiscoType: TypeCodeStarConnectionsSyncConfiguration},
		},
	})
}

type codeStarConnectionsAPI interface {
	ListConnections(context.Context, *codestarconnections.ListConnectionsInput, ...func(*codestarconnections.Options)) (*codestarconnections.ListConnectionsOutput, error)
	ListRepositoryLinks(context.Context, *codestarconnections.ListRepositoryLinksInput, ...func(*codestarconnections.Options)) (*codestarconnections.ListRepositoryLinksOutput, error)
	ListSyncConfigurations(context.Context, *codestarconnections.ListSyncConfigurationsInput, ...func(*codestarconnections.Options)) (*codestarconnections.ListSyncConfigurationsOutput, error)
}

// scanCodeStarConnections discovers third-party SCM connections, repository
// links, and sync configurations. SyncConfiguration list requires
// (RepositoryLinkId, SyncType) — fan-out per scanned link, all known sync
// types.
func scanCodeStarConnections(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := codestarconnections.NewFromConfig(acct.cfg, func(o *codestarconnections.Options) { o.Region = region })

	t, i, ferr := scanCSCConnections(ctx, client, acct, region, st, scanID)
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
