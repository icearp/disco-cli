package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/migrationhub"
)

// AWS Migration Hub — migration tracking. Only progress-update-streams are
// persistent, independently-listable resources; migration tasks hung off
// each stream are ephemeral state (skipped in aws_skips.go). Leaf: no
// outbound edges to other scanned AWS types.
func init() {
	registerType(restype.Descriptor{Type: TypeMigrationHubProgressUpdateStream, Service: "migrationhub", Upstream: "AWS::mgh::progressUpdateStream", Leaf: true})
	registerService(serviceEntry{
		name: "aws:migrationhub",
		fn:   scanMigrationHub,
	})
}

type migrationHubAPI interface {
	ListProgressUpdateStreams(context.Context, *migrationhub.ListProgressUpdateStreamsInput, ...func(*migrationhub.Options)) (*migrationhub.ListProgressUpdateStreamsOutput, error)
}

func scanMigrationHub(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := migrationhub.NewFromConfig(acct.cfg, func(o *migrationhub.Options) { o.Region = region })
	return scanMigrationHubProgressUpdateStreams(ctx, client, acct, region, st, scanID)
}

func scanMigrationHubProgressUpdateStreams(ctx context.Context, client migrationHubAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	p := migrationhub.NewListProgressUpdateStreamsPaginator(client, &migrationhub.ListProgressUpdateStreamsInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			// Migration Hub uses a per-account "home region" model; calls from a
			// non-home region get an empty-body AccessDenied — not
			// configured/available here, silent-skip. Real per-action denials
			// carry an action-identifying message and still warn below.
			if isClosedToNewCustomers(err) {
				break
			}
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "migrationhub:ListProgressUpdateStreams", acct.ID, region, err)
				break
			}
			return 0, 0, fmt.Errorf("migrationhub:ListProgressUpdateStreams: %w", err)
		}
		for _, s := range page.ProgressUpdateStreamSummaryList {
			name := sv(s.ProgressUpdateStreamName)
			if name == "" {
				continue
			}
			// Progress-update-stream summaries carry only the name; synthesize the
			// canonical mgh ARN for a stable NativeID.
			arn := fmt.Sprintf("arn:aws:mgh:%s:%s:progressUpdateStream/%s", region, acct.ID, name)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMigrationHubProgressUpdateStream, NativeID: arn, Name: &name, Region: &region,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "migrationhub progress-update-streams")
}
