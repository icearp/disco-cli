package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/rolesanywhere"
)

func init() {
	registerType(restype.Descriptor{Type: TypeRolesAnywhereCRL, Service: "roles-anywhere"})
	registerType(restype.Descriptor{Type: TypeRolesAnywhereProfile, Service: "roles-anywhere"})
	registerType(restype.Descriptor{Type: TypeRolesAnywhereTrustAnchor, Service: "roles-anywhere", Leaf: true})
	registerType(restype.Descriptor{Type: TypeRolesAnywhereSubject, Service: "roles-anywhere", Leaf: true})
	registerService(serviceEntry{
		name: "aws:roles-anywhere",
		fn:   scanRolesAnywhere,
	})
}

type rolesAnywhereAPI interface {
	ListCrls(context.Context, *rolesanywhere.ListCrlsInput, ...func(*rolesanywhere.Options)) (*rolesanywhere.ListCrlsOutput, error)
	ListProfiles(context.Context, *rolesanywhere.ListProfilesInput, ...func(*rolesanywhere.Options)) (*rolesanywhere.ListProfilesOutput, error)
	ListTrustAnchors(context.Context, *rolesanywhere.ListTrustAnchorsInput, ...func(*rolesanywhere.Options)) (*rolesanywhere.ListTrustAnchorsOutput, error)
	ListSubjects(context.Context, *rolesanywhere.ListSubjectsInput, ...func(*rolesanywhere.Options)) (*rolesanywhere.ListSubjectsOutput, error)
}

// scanRolesAnywhere discovers IAM Roles Anywhere CRLs, profiles, and trust
// anchors. ARNs native on every type.
func scanRolesAnywhere(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := rolesanywhere.NewFromConfig(acct.cfg, func(o *rolesanywhere.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanRACRLs(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanRAProfiles(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanRATrustAnchors(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanRASubjects(ctx, client, acct, region, st, scanID) },
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

func scanRACRLs(ctx context.Context, client rolesAnywhereAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := rolesanywhere.NewListCrlsPaginator(client, &rolesanywhere.ListCrlsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "rolesanywhere:ListCrls", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("rolesanywhere:ListCrls: %w", err)
		}
		for _, c := range out.Crls {
			arn := sv(c.CrlArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRolesAnywhereCRL, NativeID: arn,
				Name: c.Name, Region: &region,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "rolesanywhere crls")
}

func scanRAProfiles(ctx context.Context, client rolesAnywhereAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := rolesanywhere.NewListProfilesPaginator(client, &rolesanywhere.ListProfilesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "rolesanywhere:ListProfiles", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("rolesanywhere:ListProfiles: %w", err)
		}
		for _, p := range out.Profiles {
			arn := sv(p.ProfileArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRolesAnywhereProfile, NativeID: arn,
				Name: p.Name, Region: &region,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "rolesanywhere profiles")
}

func scanRATrustAnchors(ctx context.Context, client rolesAnywhereAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := rolesanywhere.NewListTrustAnchorsPaginator(client, &rolesanywhere.ListTrustAnchorsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "rolesanywhere:ListTrustAnchors", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("rolesanywhere:ListTrustAnchors: %w", err)
		}
		for _, a := range out.TrustAnchors {
			arn := sv(a.TrustAnchorArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRolesAnywhereTrustAnchor, NativeID: arn,
				Name: a.Name, Region: &region,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "rolesanywhere trust-anchors")
}

func scanRASubjects(ctx context.Context, client rolesAnywhereAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := rolesanywhere.NewListSubjectsPaginator(client, &rolesanywhere.ListSubjectsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "rolesanywhere:ListSubjects", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("rolesanywhere:ListSubjects: %w", err)
		}
		for _, s := range out.Subjects {
			arn := sv(s.SubjectArn)
			if arn == "" {
				continue
			}
			label := sv(s.SubjectId)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRolesAnywhereSubject, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "rolesanywhere subjects")
}
