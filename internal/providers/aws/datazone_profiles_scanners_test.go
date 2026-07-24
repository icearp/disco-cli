package aws

import (
	"context"
	"testing"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/datazone"
	dztypes "github.com/aws/aws-sdk-go-v2/service/datazone/types"
)

// stubDZProfiles embeds dataZoneAPI so only the two Search ops the profile
// scanners call need implementing; any other call would nil-panic (none occur).
type stubDZProfiles struct {
	dataZoneAPI
	groups        []dztypes.GroupProfileSummary
	users         []dztypes.UserProfileSummary
	queriedGroups map[dztypes.GroupSearchType]bool
	queriedUsers  map[dztypes.UserSearchType]bool
}

func (s *stubDZProfiles) SearchGroupProfiles(_ context.Context, in *datazone.SearchGroupProfilesInput, _ ...func(*datazone.Options)) (*datazone.SearchGroupProfilesOutput, error) {
	s.queriedGroups[in.GroupType] = true
	if in.GroupType != dztypes.GroupSearchTypeIamRoleSessionGroup {
		return &datazone.SearchGroupProfilesOutput{}, nil // only one type returns data
	}
	return &datazone.SearchGroupProfilesOutput{Items: s.groups}, nil
}

func (s *stubDZProfiles) SearchUserProfiles(_ context.Context, in *datazone.SearchUserProfilesInput, _ ...func(*datazone.Options)) (*datazone.SearchUserProfilesOutput, error) {
	s.queriedUsers[in.UserType] = true
	if in.UserType != dztypes.UserSearchTypeSsoUser {
		return &datazone.SearchUserProfilesOutput{}, nil
	}
	return &datazone.SearchUserProfilesOutput{Items: s.users}, nil
}

func TestScanDataZoneProfiles(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	stub := &stubDZProfiles{
		groups:        []dztypes.GroupProfileSummary{{Id: ptrStr("gp-1"), GroupName: ptrStr("analysts")}},
		users:         []dztypes.UserProfileSummary{{Id: ptrStr("up-1")}},
		queriedGroups: map[dztypes.GroupSearchType]bool{},
		queriedUsers:  map[dztypes.UserSearchType]bool{},
	}
	domains := []*dzDomain{{id: "dom-1"}}

	gt, _, err := scanDataZoneGroupProfiles(context.Background(), stub, acct, testRegion, st, testScanID, domains)
	if err != nil || gt != 1 {
		t.Fatalf("scanDataZoneGroupProfiles: total=%d err=%v", gt, err)
	}
	ut, _, err := scanDataZoneUserProfiles(context.Background(), stub, acct, testRegion, st, testScanID, domains)
	if err != nil || ut != 1 {
		t.Fatalf("scanDataZoneUserProfiles: total=%d err=%v", ut, err)
	}
	// Guard against the SDK adding a search-type the scanner forgets to enumerate.
	for _, gt := range dztypes.GroupSearchType("").Values() {
		if !stub.queriedGroups[gt] {
			t.Errorf("GroupSearchType %q never queried — add it to dzGroupTypes", gt)
		}
	}
	for _, ut := range dztypes.UserSearchType("").Values() {
		if !stub.queriedUsers[ut] {
			t.Errorf("UserSearchType %q never queried — add it to dzUserTypes", ut)
		}
	}
	for _, tc := range []struct {
		typ, want string
	}{
		{TypeDataZoneGroupProfile, dzARN(testRegion, acct.ID, "dom-1", "group-profile", "gp-1")},
		{TypeDataZoneUserProfile, dzARN(testRegion, acct.ID, "dom-1", "user-profile", "up-1")},
	} {
		rows, err := st.ListResources(store.ResourceFilter{Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{tc.typ}, Limit: util.AllResources})
		if err != nil {
			t.Fatalf("ListResources %s: %v", tc.typ, err)
		}
		if len(rows) != 1 || rows[0].NativeID != tc.want {
			t.Errorf("%s: rows=%+v, want one with NativeID %s", tc.typ, rows, tc.want)
		}
	}
}
