package aws

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/b2bi"
)

func init() {
	registerType(restype.Descriptor{Type: TypeB2BICapability, Service: "b2bi"})
	registerType(restype.Descriptor{Type: TypeB2BIPartnership, Service: "b2bi"})
	registerType(restype.Descriptor{Type: TypeB2BIProfile, Service: "b2bi"})
	registerType(restype.Descriptor{Type: TypeB2BITransformer, Service: "b2bi", Leaf: true})
	registerService(serviceEntry{
		name: "aws:b2bi",
		fn:   scanB2BI,
	})
}

type b2biAPI interface {
	ListCapabilities(context.Context, *b2bi.ListCapabilitiesInput, ...func(*b2bi.Options)) (*b2bi.ListCapabilitiesOutput, error)
	GetCapability(context.Context, *b2bi.GetCapabilityInput, ...func(*b2bi.Options)) (*b2bi.GetCapabilityOutput, error)
	ListPartnerships(context.Context, *b2bi.ListPartnershipsInput, ...func(*b2bi.Options)) (*b2bi.ListPartnershipsOutput, error)
	ListProfiles(context.Context, *b2bi.ListProfilesInput, ...func(*b2bi.Options)) (*b2bi.ListProfilesOutput, error)
	ListTransformers(context.Context, *b2bi.ListTransformersInput, ...func(*b2bi.Options)) (*b2bi.ListTransformersOutput, error)
}

// scanB2BI discovers B2B Data Interchange capabilities, partnerships,
// profiles, and transformers via paginated List* calls. List APIs return
// only IDs — synthesize ARN per (account, region, kind, id).
func scanB2BI(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := b2bi.NewFromConfig(acct.cfg, func(o *b2bi.Options) { o.Region = region })

	if ferr := gateB2BI(ctx, client); ferr != nil {
		return 0, 0, ferr
	}

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanB2BICapabilities(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanB2BIPartnerships(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanB2BIProfiles(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanB2BITransformers(ctx, client, acct, region, st, scanID) },
	} {
		t, i, perr := phase()
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

// gateB2BI probes the cheapest list op once. B2BI deploys to a region subset;
// unsupported regions return empty-message AccessDeniedException. B2BI is
// closed to new customers, so the account can't self-enable — short-circuit
// via markServiceNotEntitled so the dispatcher renders (account: not entitled) once.
func gateB2BI(ctx context.Context, client b2biAPI) error {
	mr := int32(1)
	_, err := client.ListProfiles(ctx, &b2bi.ListProfilesInput{MaxResults: &mr})
	if err != nil && isClosedToNewCustomers(err) {
		return markServiceNotEntitled(err)
	}
	return nil
}

func scanB2BICapabilities(ctx context.Context, client b2biAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := b2bi.NewListCapabilitiesPaginator(client, &b2bi.ListCapabilitiesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isClosedToNewCustomers(err) {
				return 0, 0, nil
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "b2bi:ListCapabilities", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("b2bi:ListCapabilities: %w", err)
		}
		for _, c := range out.Capabilities {
			id := sv(c.CapabilityId)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:b2bi:%s:%s:capability/%s", region, acct.ID, id)
			// Enrich with GetCapability body — InstructionsDocuments[].BucketName
			// (S3 source) is not on the list-summary shape. Fall back to summary
			// on per-row failure.
			attrs := mustJSON(c)
			cid := id
			gout, gerr := client.GetCapability(ctx, &b2bi.GetCapabilityInput{CapabilityId: &cid})
			if gerr != nil {
				if isAccessDenied(gerr) {
					_ = skipIfAccessDenied(st, "b2bi:GetCapability", acct.ID, region, gerr)
				}
			} else if gout != nil {
				attrs = mustJSON(gout)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeB2BICapability, NativeID: arn,
				Name: c.Name, Region: &region,
				AttributesJSON: attrs, DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "b2bi capabilities")
}

func scanB2BIPartnerships(ctx context.Context, client b2biAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := b2bi.NewListPartnershipsPaginator(client, &b2bi.ListPartnershipsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isClosedToNewCustomers(err) {
				return 0, 0, nil
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "b2bi:ListPartnerships", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("b2bi:ListPartnerships: %w", err)
		}
		for _, p := range out.Partnerships {
			id := sv(p.PartnershipId)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:b2bi:%s:%s:partnership/%s", region, acct.ID, id)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeB2BIPartnership, NativeID: arn,
				Name: p.Name, Region: &region,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "b2bi partnerships")
}

func scanB2BIProfiles(ctx context.Context, client b2biAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := b2bi.NewListProfilesPaginator(client, &b2bi.ListProfilesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isClosedToNewCustomers(err) {
				return 0, 0, nil
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "b2bi:ListProfiles", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("b2bi:ListProfiles: %w", err)
		}
		for _, p := range out.Profiles {
			id := sv(p.ProfileId)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:b2bi:%s:%s:profile/%s", region, acct.ID, id)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeB2BIProfile, NativeID: arn,
				Name: p.Name, Region: &region,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "b2bi profiles")
}

func scanB2BITransformers(ctx context.Context, client b2biAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := b2bi.NewListTransformersPaginator(client, &b2bi.ListTransformersInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isClosedToNewCustomers(err) {
				return 0, 0, nil
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "b2bi:ListTransformers", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("b2bi:ListTransformers: %w", err)
		}
		for _, t := range out.Transformers {
			id := sv(t.TransformerId)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:b2bi:%s:%s:transformer/%s", region, acct.ID, id)
			status := string(t.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeB2BITransformer, NativeID: arn,
				Name: t.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(t), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "b2bi transformers")
}
