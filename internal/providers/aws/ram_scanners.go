package aws

import (
	"cmp"
	"context"
	"slices"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"

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
	// ramPageCap is the model's MaxResults maximum (range 1-500) for
	// GetResourceShares, ListPermissions and GetResourceShareAssociations.
	ramPageCap = 500
)

func scanRAM(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := ram.NewFromConfig(acct.cfg, func(o *ram.Options) { o.Region = region })
	return scanRAMAll(ctx, client, acct, region, st, scanID)
}

// scanRAMAll discovers Resource Access Manager resource shares
// (ResourceOwner=SELF only — OTHER-ACCOUNTS are inbound shares we don't own),
// the managed permission catalogue (AWS-managed permissions flagged
// ManagedByProvider), and the principals and resources each owned share covers.
// The phases are independent: each runs whatever another returns, except
// after a store write failure, and every error is joined by phaseErrs, which
// demotes a transient one to a warning only beside a hard one, so a throttle
// on one phase is reported under its op instead of buried in the other's hard
// error.
func scanRAMAll(ctx context.Context, client ramAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var phases phaseTotals
	shares, t, i, err := scanRAMResourceShares(ctx, client, acct, region, st, scanID)
	if phases.addPhase(ramResourceSharesOp, t, i, err) {
		return phases.result(st, acct, region)
	}
	t, i, err = scanRAMPermissions(ctx, client, acct, region, st, scanID)
	if phases.addPhase(ramPermissionsOp, t, i, err) {
		return phases.result(st, acct, region)
	}
	t, i, err = scanRAMResourceShareAssociations(ctx, client, acct, region, st, scanID, shares)
	phases.addPhase(ramAssociationsOp, t, i, err)
	return phases.result(st, acct, region)
}

// scanRAMResourceShares stores the shares the account owns, those read before
// a failed page included, and returns the ARNs of the rows it built; the
// caller stops before using them when their write failed. The listing leaves
// every filter but ResourceOwner unset, so it reads every owned share.
func scanRAMResourceShares(ctx context.Context, client ramAPI, acct *account, region string, st *store.Store, scanID string) (map[string]bool, int, int, error) {
	shares := map[string]bool{}
	total, inserted, err := storeTokenList(ctx, st, acct, region, ramResourceSharesOp,
		func(ctx context.Context, token *string) ([]types.ResourceShare, *string, error) {
			out, err := client.GetResourceShares(ctx, &ram.GetResourceSharesInput{
				ResourceOwner: types.ResourceOwnerSelf,
				MaxResults:    sdkaws.Int32(ramPageCap),
				NextToken:     token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.ResourceShares, out.NextToken, nil
		},
		func(s types.ResourceShare) (*store.Resource, string, string) {
			arn := sv(s.ResourceShareArn)
			if arn == "" {
				return nil, "resource shares have an empty ResourceShareArn", cmp.Or(sv(s.Name), "one without a Name")
			}
			shares[arn] = true
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRAMResourceShare, NativeID: arn,
				Name: s.Name, Region: &region, Status: nonEmptyPtr(string(s.Status)),
				AttributesJSON: mustJSON(s), TagsJSON: kvTagsJSON(s.Tags, ramTagKV), DiscoveredBy: scanID,
			}, "", ""
		})
	return shares, total, inserted, err
}

// scanRAMPermissions stores the region's customer-managed permissions and,
// from one region only (RAM's once-region, onceRegion), the AWS-managed
// catalogue. PermissionType and ResourceType are left unset, so the listing
// reads both kinds for every resource type.
func scanRAMPermissions(ctx context.Context, client ramAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, ramPermissionsOp,
		func(ctx context.Context, token *string) ([]types.ResourceSharePermissionSummary, *string, error) {
			out, err := client.ListPermissions(ctx, &ram.ListPermissionsInput{MaxResults: sdkaws.Int32(ramPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			// The AWS-managed catalogue is partition-global with region-less
			// ARNs; emitted from every region's concurrent upsert it collides
			// on the region-excluded natural key, so only the once-region keeps it.
			if !acct.isOnceRegion("ram", region) {
				return slices.DeleteFunc(out.Permissions, ramManagedPermission), out.NextToken, nil
			}
			return out.Permissions, out.NextToken, nil
		},
		func(p types.ResourceSharePermissionSummary) (*store.Resource, string, string) {
			arn := sv(p.Arn)
			if arn == "" {
				return nil, "permissions have an empty Arn", cmp.Or(sv(p.Name), "one without a Name")
			}
			managed := ramManagedPermission(p)
			permRegion := &region
			if managed {
				permRegion = regionGlobal
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRAMPermission, NativeID: arn,
				Name: p.Name, Region: permRegion,
				AttributesJSON:    mustJSON(p),
				TagsJSON:          kvTagsJSON(p.Tags, ramTagKV),
				DiscoveredBy:      scanID,
				ManagedByProvider: managed,
			}, "", ""
		})
}

// ramManagedPermission reports whether AWS created and manages p.
func ramManagedPermission(p types.ResourceSharePermissionSummary) bool {
	return p.PermissionType == types.PermissionTypeAwsManaged
}

func ramTagKV(t types.Tag) (key, value *string) { return t.Key, t.Value }

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
				o.Limit = ramPageCap
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
