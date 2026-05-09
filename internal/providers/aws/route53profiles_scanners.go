package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/route53profiles"
)

func init() {
	registerService(serviceEntry{
		name: "aws:route53-profiles",
		fn:   scanRoute53Profiles,
		emits: []coverage.TypeDecl{
			{Service: "route53-profiles", DiscoType: TypeRoute53ProfilesProfile, Leaf: true},
			{Service: "route53-profiles", DiscoType: TypeRoute53ProfilesProfileAssociation, Leaf: true},
			{Service: "route53-profiles", DiscoType: TypeRoute53ProfilesProfileResourceAssociation, Leaf: true},
		},
	})
}

type route53ProfilesAPI interface {
	ListProfiles(context.Context, *route53profiles.ListProfilesInput, ...func(*route53profiles.Options)) (*route53profiles.ListProfilesOutput, error)
	ListProfileAssociations(context.Context, *route53profiles.ListProfileAssociationsInput, ...func(*route53profiles.Options)) (*route53profiles.ListProfileAssociationsOutput, error)
	ListProfileResourceAssociations(context.Context, *route53profiles.ListProfileResourceAssociationsInput, ...func(*route53profiles.Options)) (*route53profiles.ListProfileResourceAssociationsOutput, error)
}

// scanRoute53Profiles discovers Route53 Profiles, profile associations, and
// per-profile resource associations. Profile carries native ARN; associations
// synthesize ARN from parent profile path.
func scanRoute53Profiles(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := route53profiles.NewFromConfig(acct.cfg, func(o *route53profiles.Options) { o.Region = region })

	profiles, t, i, ferr := scanR53PProfiles(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanR53PProfileAssociations(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	for _, p := range profiles {
		t, i, ferr = scanR53PProfileResourceAssociations(ctx, client, acct, region, st, scanID, p.id, p.arn)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

type r53pProfile struct{ id, arn string }

func scanR53PProfiles(ctx context.Context, client route53ProfilesAPI, acct *account, region string, st *store.Store, scanID string) ([]r53pProfile, int, int, error) {
	pager := route53profiles.NewListProfilesPaginator(client, &route53profiles.ListProfilesInput{})
	var batch []*store.Resource
	var profs []r53pProfile
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "route53profiles:ListProfiles", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("route53profiles:ListProfiles: %w", err)
		}
		for _, p := range out.ProfileSummaries {
			arn := sv(p.Arn)
			id := sv(p.Id)
			if arn == "" || id == "" {
				continue
			}
			profs = append(profs, r53pProfile{id, arn})
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRoute53ProfilesProfile, NativeID: arn,
				Name: p.Name, Region: &region,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "route53profiles profiles")
	return profs, t, i, err
}

func scanR53PProfileAssociations(ctx context.Context, client route53ProfilesAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := route53profiles.NewListProfileAssociationsPaginator(client, &route53profiles.ListProfileAssociationsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "route53profiles:ListProfileAssociations", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("route53profiles:ListProfileAssociations: %w", err)
		}
		for _, a := range out.ProfileAssociations {
			id := sv(a.Id)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:route53profiles:%s:%s:profile-association/%s", region, acct.ID, id)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRoute53ProfilesProfileAssociation, NativeID: arn,
				Name: a.Name, Region: &region,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "route53profiles profile-associations")
}

func scanR53PProfileResourceAssociations(ctx context.Context, client route53ProfilesAPI, acct *account, region string, st *store.Store, scanID string, profileID, profileARN string) (int, int, error) {
	pid := profileID
	pager := route53profiles.NewListProfileResourceAssociationsPaginator(client, &route53profiles.ListProfileResourceAssociationsInput{ProfileId: &pid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "route53profiles:ListProfileResourceAssociations", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("route53profiles:ListProfileResourceAssociations: %w", err)
		}
		for _, a := range out.ProfileResourceAssociations {
			id := sv(a.Id)
			if id == "" {
				continue
			}
			arn := profileARN + "/resource-association/" + id
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRoute53ProfilesProfileResourceAssociation, NativeID: arn,
				Name: a.Name, Region: &region,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "route53profiles profile-resource-associations")
}
