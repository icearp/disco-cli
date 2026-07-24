package aws

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/datazone"
	dztypes "github.com/aws/aws-sdk-go-v2/service/datazone/types"
)

// DataZone group/user profiles are the SSO/IAM principals in a domain. They
// enumerate per (domain, principal-type): Search* ops require both a
// DomainIdentifier and a type enum, so each type must be queried.

var dzGroupTypes = []dztypes.GroupSearchType{
	dztypes.GroupSearchTypeSsoGroup,
	dztypes.GroupSearchTypeDatazoneSsoGroup,
	dztypes.GroupSearchTypeIamRoleSessionGroup,
}

var dzUserTypes = []dztypes.UserSearchType{
	dztypes.UserSearchTypeSsoUser,
	dztypes.UserSearchTypeDatazoneUser,
	dztypes.UserSearchTypeDatazoneSsoUser,
	dztypes.UserSearchTypeDatazoneIamUser,
}

func scanDataZoneGroupProfiles(ctx context.Context, client dataZoneAPI, acct *account, region string, st *store.Store, scanID string, domains []*dzDomain) (int, int, error) {
	var batch []*store.Resource
	for _, d := range domains {
		for _, gt := range dzGroupTypes {
			did := d.id
			pager := datazone.NewSearchGroupProfilesPaginator(client, &datazone.SearchGroupProfilesInput{
				DomainIdentifier: &did, GroupType: gt,
			})
			for pager.HasMorePages() {
				out, perr := pager.NextPage(ctx)
				if perr != nil {
					if isAccessDenied(perr) {
						break
					}
					return 0, 0, fmt.Errorf("datazone:SearchGroupProfiles %s/%s: %w", d.id, gt, perr)
				}
				for _, g := range out.Items {
					id := sv(g.Id)
					if id == "" {
						continue
					}
					name := sv(g.GroupName)
					if name == "" {
						name = id
					}
					status := string(g.Status)
					batch = append(batch, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeDataZoneGroupProfile, NativeID: dzARN(region, acct.ID, d.id, "group-profile", id),
						Name: &name, Region: &region, Status: &status,
						AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
					})
				}
			}
		}
	}
	return upsertBatch(st, batch, "datazone group-profiles")
}

func scanDataZoneUserProfiles(ctx context.Context, client dataZoneAPI, acct *account, region string, st *store.Store, scanID string, domains []*dzDomain) (int, int, error) {
	var batch []*store.Resource
	for _, d := range domains {
		for _, ut := range dzUserTypes {
			did := d.id
			pager := datazone.NewSearchUserProfilesPaginator(client, &datazone.SearchUserProfilesInput{
				DomainIdentifier: &did, UserType: ut,
			})
			for pager.HasMorePages() {
				out, perr := pager.NextPage(ctx)
				if perr != nil {
					if isAccessDenied(perr) {
						break
					}
					return 0, 0, fmt.Errorf("datazone:SearchUserProfiles %s/%s: %w", d.id, ut, perr)
				}
				for _, u := range out.Items {
					id := sv(u.Id)
					if id == "" {
						continue
					}
					status := string(u.Status)
					batch = append(batch, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeDataZoneUserProfile, NativeID: dzARN(region, acct.ID, d.id, "user-profile", id),
						Name: &id, Region: &region, Status: &status,
						AttributesJSON: mustJSON(u), DiscoveredBy: scanID,
					})
				}
			}
		}
	}
	return upsertBatch(st, batch, "datazone user-profiles")
}
