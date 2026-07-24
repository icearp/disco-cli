package aws

import (
	"context"
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/connect"
	cttypes "github.com/aws/aws-sdk-go-v2/service/connect/types"
)

type stubConnectUsers struct {
	users      []cttypes.UserSummary
	userOut    map[string]*connect.DescribeUserOutput
	groups     []cttypes.HierarchyGroupSummary
	groupOut   map[string]*connect.DescribeUserHierarchyGroupOutput
	structure  *cttypes.HierarchyStructure
	profiles   []cttypes.SecurityProfileSummary
	profileOut map[string]*connect.DescribeSecurityProfileOutput
	attrs      []cttypes.PredefinedAttributeSummary
	attrOut    map[string]*connect.DescribePredefinedAttributeOutput
}

func (s *stubConnectUsers) ListUsers(_ context.Context, _ *connect.ListUsersInput, _ ...func(*connect.Options)) (*connect.ListUsersOutput, error) {
	return &connect.ListUsersOutput{UserSummaryList: s.users}, nil
}

func (s *stubConnectUsers) DescribeUser(_ context.Context, in *connect.DescribeUserInput, _ ...func(*connect.Options)) (*connect.DescribeUserOutput, error) {
	return s.userOut[*in.UserId], nil
}

func (s *stubConnectUsers) ListUserHierarchyGroups(_ context.Context, _ *connect.ListUserHierarchyGroupsInput, _ ...func(*connect.Options)) (*connect.ListUserHierarchyGroupsOutput, error) {
	return &connect.ListUserHierarchyGroupsOutput{UserHierarchyGroupSummaryList: s.groups}, nil
}

func (s *stubConnectUsers) DescribeUserHierarchyGroup(_ context.Context, in *connect.DescribeUserHierarchyGroupInput, _ ...func(*connect.Options)) (*connect.DescribeUserHierarchyGroupOutput, error) {
	return s.groupOut[*in.HierarchyGroupId], nil
}

func (s *stubConnectUsers) DescribeUserHierarchyStructure(_ context.Context, _ *connect.DescribeUserHierarchyStructureInput, _ ...func(*connect.Options)) (*connect.DescribeUserHierarchyStructureOutput, error) {
	return &connect.DescribeUserHierarchyStructureOutput{HierarchyStructure: s.structure}, nil
}

func (s *stubConnectUsers) ListSecurityProfiles(_ context.Context, _ *connect.ListSecurityProfilesInput, _ ...func(*connect.Options)) (*connect.ListSecurityProfilesOutput, error) {
	return &connect.ListSecurityProfilesOutput{SecurityProfileSummaryList: s.profiles}, nil
}

func (s *stubConnectUsers) DescribeSecurityProfile(_ context.Context, in *connect.DescribeSecurityProfileInput, _ ...func(*connect.Options)) (*connect.DescribeSecurityProfileOutput, error) {
	return s.profileOut[*in.SecurityProfileId], nil
}

func (s *stubConnectUsers) ListPredefinedAttributes(_ context.Context, _ *connect.ListPredefinedAttributesInput, _ ...func(*connect.Options)) (*connect.ListPredefinedAttributesOutput, error) {
	return &connect.ListPredefinedAttributesOutput{PredefinedAttributeSummaryList: s.attrs}, nil
}

func (s *stubConnectUsers) DescribePredefinedAttribute(_ context.Context, in *connect.DescribePredefinedAttributeInput, _ ...func(*connect.Options)) (*connect.DescribePredefinedAttributeOutput, error) {
	return s.attrOut[*in.Name], nil
}

func TestScanConnectUsers(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	instID := "11111111-1111-1111-1111-111111111111"
	instARN := fmt.Sprintf("arn:aws:connect:%s:%s:instance/%s", testRegion, acct.ID, instID)
	instances := []cttypes.InstanceSummary{{Id: &instID, Arn: &instARN}}

	uID := "u-1"
	uARN := fmt.Sprintf("arn:aws:connect:%s:%s:instance/%s/agent/%s", testRegion, acct.ID, instID, uID)
	uName := "alice"
	gID := "g-1"
	gARN := fmt.Sprintf("arn:aws:connect:%s:%s:instance/%s/agent-group/%s", testRegion, acct.ID, instID, gID)
	gName := "g"
	hsARN := fmt.Sprintf("arn:aws:connect:%s:%s:instance/%s/agent-hierarchy", testRegion, acct.ID, instID)
	pID := "p-1"
	pARN := fmt.Sprintf("arn:aws:connect:%s:%s:instance/%s/security-profile/%s", testRegion, acct.ID, instID, pID)
	pName := "p"
	aName := "color"
	aARN := fmt.Sprintf("arn:aws:connect:%s:%s:instance/%s/predefined-attribute/%s", testRegion, acct.ID, instID, aName)

	stub := &stubConnectUsers{
		users:      []cttypes.UserSummary{{Id: &uID, Arn: &uARN, Username: &uName}},
		userOut:    map[string]*connect.DescribeUserOutput{uID: {User: &cttypes.User{Arn: &uARN, Username: &uName}}},
		groups:     []cttypes.HierarchyGroupSummary{{Id: &gID, Arn: &gARN, Name: &gName}},
		groupOut:   map[string]*connect.DescribeUserHierarchyGroupOutput{gID: {HierarchyGroup: &cttypes.HierarchyGroup{Arn: &gARN, Name: &gName}}},
		structure:  &cttypes.HierarchyStructure{},
		profiles:   []cttypes.SecurityProfileSummary{{Id: &pID, Arn: &pARN, Name: &pName}},
		profileOut: map[string]*connect.DescribeSecurityProfileOutput{pID: {SecurityProfile: &cttypes.SecurityProfile{Arn: &pARN, SecurityProfileName: &pName}}},
		attrs:      []cttypes.PredefinedAttributeSummary{{Name: &aName}},
		attrOut:    map[string]*connect.DescribePredefinedAttributeOutput{aName: {PredefinedAttribute: &cttypes.PredefinedAttribute{Name: &aName}}},
	}

	total, inserted, err := scanConnectUsers(context.Background(), stub, instances, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 5 || inserted != 5 {
		t.Fatalf("total=%d inserted=%d want 5/5", total, inserted)
	}
	for _, want := range []struct{ typ, id string }{
		{TypeConnectUser, uARN},
		{TypeConnectUserHierarchyGroup, gARN},
		{TypeConnectUserHierarchyStructure, hsARN},
		{TypeConnectSecurityProfile, pARN},
		{TypeConnectPredefinedAttribute, aARN},
	} {
		if _, err := st.GetResource(store.ResourceID("aws", acct.ID, want.id)); err != nil {
			t.Errorf("%s missing: %v", want.typ, err)
		}
	}
}

func TestScanConnectUsersEmpty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	stub := &stubConnectUsers{}
	total, inserted, err := scanConnectUsers(context.Background(), stub, nil, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("total=%d inserted=%d want 0/0", total, inserted)
	}
}
