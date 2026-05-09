package aws

import (
	"context"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/ram"
	"github.com/aws/aws-sdk-go-v2/service/ram/types"
)

func init() {
	registerService(serviceEntry{
		name: "aws:ram",
		fn:   scanRAM,
		emits: []coverage.TypeDecl{
			{Service: "ram", DiscoType: TypeRAMResourceShare, Leaf: true},
			{Service: "ram", DiscoType: TypeRAMPermission, Leaf: true},
		},
	})
}

type ramAPI interface {
	GetResourceShares(context.Context, *ram.GetResourceSharesInput, ...func(*ram.Options)) (*ram.GetResourceSharesOutput, error)
	ListPermissions(context.Context, *ram.ListPermissionsInput, ...func(*ram.Options)) (*ram.ListPermissionsOutput, error)
}

// scanRAM discovers Resource Access Manager resource shares (ResourceOwner=SELF
// only — OTHER-ACCOUNTS are inbound shares not owned by this account) and
// the managed permission catalogue (AWS-managed permissions flagged
// ManagedByProvider).
func scanRAM(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := ram.NewFromConfig(acct.cfg, func(o *ram.Options) { o.Region = region })

	t, i, ferr := scanRAMResourceShares(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanRAMPermissions(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

func scanRAMResourceShares(ctx context.Context, client ramAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.GetResourceShares(ctx, &ram.GetResourceSharesInput{
			ResourceOwner: types.ResourceOwnerSelf,
			NextToken:     nextToken,
		})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "ram:GetResourceShares", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("ram:GetResourceShares: %w", err)
		}
		for _, s := range out.ResourceShares {
			arn := sv(s.ResourceShareArn)
			if arn == "" {
				continue
			}
			status := string(s.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRAMResourceShare, NativeID: arn,
				Name: s.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "ram resource-shares")
}

func scanRAMPermissions(ctx context.Context, client ramAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListPermissions(ctx, &ram.ListPermissionsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "ram:ListPermissions", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("ram:ListPermissions: %w", err)
		}
		for _, p := range out.Permissions {
			arn := sv(p.Arn)
			if arn == "" {
				continue
			}
			// AWS-managed permission ARNs use account "aws": arn:aws:ram::aws:permission/...
			managed := strings.HasPrefix(arn, "arn:aws:ram::aws:permission/")
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRAMPermission, NativeID: arn,
				Name: p.Name, Region: &region,
				AttributesJSON:    mustJSON(p),
				DiscoveredBy:      scanID,
				ManagedByProvider: managed,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "ram permissions")
}
