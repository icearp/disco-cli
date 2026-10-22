package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/datazone"
	dztypes "github.com/aws/aws-sdk-go-v2/service/datazone/types"
	"github.com/icearp/disco-cli/store"
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
	f := &dzFanout{st: st, acct: acct, region: region, op: "datazone:SearchGroupProfiles", what: "group profiles"}
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
					f.record(fmt.Sprintf("%s/%s", d.id, gt), perr)
					break
				}
				for _, g := range out.Items {
					id := sv(g.Id)
					if id == "" {
						f.noID(d.id)
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
	return f.finish(batch)
}

func scanDataZoneUserProfiles(ctx context.Context, client dataZoneAPI, acct *account, region string, st *store.Store, scanID string, domains []*dzDomain) (int, int, error) {
	f := &dzFanout{st: st, acct: acct, region: region, op: "datazone:SearchUserProfiles", what: "user profiles"}
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
					f.record(fmt.Sprintf("%s/%s", d.id, ut), perr)
					break
				}
				for _, u := range out.Items {
					id := sv(u.Id)
					if id == "" {
						f.noID(d.id)
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
	return f.finish(batch)
}
