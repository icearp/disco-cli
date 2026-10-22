package aws

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/ram"
	"github.com/aws/aws-sdk-go-v2/service/ram/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeRAMResourceShare, Service: "ram"})
	registerType(restype.Descriptor{Type: TypeRAMPermission, Service: "ram"})
	registerType(restype.Descriptor{Type: TypeRAMResourceShareAssociation, Service: "ram"})
	registerService(serviceEntry{
		name: "aws:ram",
		fn:   scanRAM,
	})
}

type ramAPI interface {
	GetResourceShares(context.Context, *ram.GetResourceSharesInput, ...func(*ram.Options)) (*ram.GetResourceSharesOutput, error)
	ListPermissions(context.Context, *ram.ListPermissionsInput, ...func(*ram.Options)) (*ram.ListPermissionsOutput, error)
	GetResourceShareAssociations(context.Context, *ram.GetResourceShareAssociationsInput, ...func(*ram.Options)) (*ram.GetResourceShareAssociationsOutput, error)
}

const (
	ramResourceSharesOp = "ram:GetResourceShares"
	ramPermissionsOp    = "ram:ListPermissions"
	ramAssociationsOp   = "ram:GetResourceShareAssociations"
	// ramAssociationsPageCap is the model's MaxResults maximum for
	// GetResourceShareAssociations.
	ramAssociationsPageCap = 500
)

func scanRAM(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := ram.NewFromConfig(acct.cfg, func(o *ram.Options) { o.Region = region })
	return scanRAMAll(ctx, client, acct, region, st, scanID)
}

// scanRAMAll discovers Resource Access Manager resource shares
// (ResourceOwner=SELF only — OTHER-ACCOUNTS are inbound shares we don't own),
// the managed permission catalogue (AWS-managed permissions flagged
// ManagedByProvider), and the principals and resources each owned share covers.
// The phases are independent: each runs whatever another returns, and every
// error is joined by phaseErrs, which demotes a transient one to a warning only
// beside a hard one, so a throttle on one phase is reported under its op
// instead of buried in the other's hard error.
func scanRAMAll(ctx context.Context, client ramAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	shares, t1, i1, serr := scanRAMResourceShares(ctx, client, acct, region, st, scanID)
	t2, i2, perr := scanRAMPermissions(ctx, client, acct, region, st, scanID)
	t3, i3, aerr := scanRAMResourceShareAssociations(ctx, client, acct, region, st, scanID, shares)
	var errs phaseErrs
	errs.add(ramResourceSharesOp, serr)
	errs.add(ramPermissionsOp, perr)
	errs.add(ramAssociationsOp, aerr)
	return t1 + t2 + t3, i1 + i2 + i3, errs.join(st, acct, region)
}

// scanRAMResourceShares stores the shares the account owns and returns the
// ARNs of the shares it stored; it returns none when the listing or the
// upsert fails.
func scanRAMResourceShares(ctx context.Context, client ramAPI, acct *account, region string, st *store.Store, scanID string) (map[string]bool, int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.GetResourceShares(ctx, &ram.GetResourceSharesInput{
			ResourceOwner: types.ResourceOwnerSelf,
			NextToken:     nextToken,
		})
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, ramResourceSharesOp, acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("%s: %w", ramResourceSharesOp, err)
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
	total, inserted, err := upsertBatch(st, batch, "ram resource-shares")
	if err != nil {
		return nil, 0, 0, err
	}
	shares := make(map[string]bool, len(batch))
	for _, r := range batch {
		shares[r.NativeID] = true
	}
	return shares, total, inserted, nil
}

func scanRAMPermissions(ctx context.Context, client ramAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListPermissions(ctx, &ram.ListPermissionsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, ramPermissionsOp, acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("%s: %w", ramPermissionsOp, err)
		}
		for _, p := range out.Permissions {
			arn := sv(p.Arn)
			if arn == "" {
				continue
			}
			// AWS-managed permission ARNs use account "aws": arn:aws:ram::aws:permission/...
			managed := strings.HasPrefix(arn, "arn:aws:ram::aws:permission/")
			// The AWS-managed permission catalogue is partition-global with
			// region-less ARNs; emitting from every region's concurrent upsert
			// collides on the region-excluded natural key. Emit once (us-east-1),
			// tag global. Customer-managed permissions carry region in the ARN,
			// so stay per-region.
			permRegion := &region
			if managed {
				if region != "us-east-1" {
					continue
				}
				permRegion = regionGlobal
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRAMPermission, NativeID: arn,
				Name: p.Name, Region: permRegion,
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

// ramAssociationKinds are the association types GetResourceShareAssociations
// documents, each with the NativeID segment its rows carry. Source
// associations have their own op (ListSourceAssociations).
var ramAssociationKinds = []struct {
	typ  types.ResourceShareAssociationType
	kind string
}{
	{types.ResourceShareAssociationTypePrincipal, "principal"},
	{types.ResourceShareAssociationTypeResource, "resource"},
}

// ramShareAssociationNativeID builds an association's id from its share, its
// type and the associated entity; the API issues no ARN. The entity goes last
// because a resource or principal ARN can contain "/".
func ramShareAssociationNativeID(shareARN, kind, entity string) string {
	return shareARN + "/" + kind + "/" + entity
}

// scanRAMResourceShareAssociations stores, for the shares the account owns,
// each principal (account, organization, OU, IAM role or user, service
// principal) and each resource a share covers, one row per association. One
// region-wide listing per association type (no ResourceShareArns) returns the
// associations of every owned share; each row is linked under its share when
// shares, the shares stored this scan, holds it. No owner or region check is
// applied: the op's SDK doc says it covers "resource shares that you own", and
// RAM shares are regional, so every row is taken to belong to an owned share
// in this region. Neither is verified against a live account.
func scanRAMResourceShareAssociations(ctx context.Context, client ramAPI, acct *account, region string, st *store.Store, scanID string, shares map[string]bool) (int, int, error) {
	var children []containedChild
	for _, k := range ramAssociationKinds {
		pager := ram.NewGetResourceShareAssociationsPaginator(client,
			&ram.GetResourceShareAssociationsInput{AssociationType: k.typ},
			func(o *ram.GetResourceShareAssociationsPaginatorOptions) {
				o.Limit = ramAssociationsPageCap
				o.StopOnDuplicateToken = true
			})
		for pager.HasMorePages() {
			out, err := pager.NextPage(ctx)
			if err != nil {
				return upsertRegionChildren(st, acct, region, ramAssociationsOp, children, err)
			}
			for _, a := range out.ResourceShareAssociations {
				children = appendRAMAssociation(children, acct, region, scanID, shares, k.kind, a)
			}
		}
	}
	return upsertRegionChildren(st, acct, region, ramAssociationsOp, children, nil)
}

// appendRAMAssociation adds the association's row to children unless it lacks
// a key or is DISASSOCIATED: a disassociated principal or resource is no
// longer covered by the share, and storing it would show revoked access as
// current share state. DISASSOCIATING is kept, since the removal has not
// finished.
func appendRAMAssociation(children []containedChild, acct *account, region, scanID string, shares map[string]bool, kind string, a types.ResourceShareAssociation) []containedChild {
	shareARN, entity := sv(a.ResourceShareArn), sv(a.AssociatedEntity)
	if shareARN == "" || entity == "" || a.Status == types.ResourceShareAssociationStatusDisassociated {
		return children
	}
	var parentID string
	if shares[shareARN] {
		parentID = store.ResourceID("aws", acct.ID, shareARN)
	}
	return append(children, containedChild{
		row: &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeRAMResourceShareAssociation, NativeID: ramShareAssociationNativeID(shareARN, kind, entity),
			Name: &entity, Region: &region, Status: nonEmptyPtr(string(a.Status)),
			AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
		},
		parentID: parentID,
	})
}
