package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/oam"
)

func init() {
	registerService(serviceEntry{
		name: "aws:oam",
		fn:   scanOAM,
		emits: []coverage.TypeDecl{
			{Service: "oam", DiscoType: TypeOAMLink},
			{Service: "oam", DiscoType: TypeOAMSink},
		},
	})
}

type oamAPI interface {
	ListLinks(context.Context, *oam.ListLinksInput, ...func(*oam.Options)) (*oam.ListLinksOutput, error)
	ListSinks(context.Context, *oam.ListSinksInput, ...func(*oam.Options)) (*oam.ListSinksOutput, error)
}

// scanOAM discovers Observability Access Manager links and sinks.
func scanOAM(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := oam.NewFromConfig(acct.cfg, func(o *oam.Options) { o.Region = region })

	t, i, ferr := scanOAMLinks(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanOAMSinks(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

func scanOAMLinks(ctx context.Context, client oamAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListLinks(ctx, &oam.ListLinksInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "oam:ListLinks", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("oam:ListLinks: %w", err)
		}
		for _, l := range out.Items {
			arn := sv(l.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeOAMLink, NativeID: arn,
				Name: l.Label, Region: &region,
				AttributesJSON: mustJSON(l), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "oam links")
}

func scanOAMSinks(ctx context.Context, client oamAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListSinks(ctx, &oam.ListSinksInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "oam:ListSinks", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("oam:ListSinks: %w", err)
		}
		for _, s := range out.Items {
			arn := sv(s.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeOAMSink, NativeID: arn,
				Name: s.Name, Region: &region,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "oam sinks")
}
