package aws

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/workspaces"
	"github.com/icearp/disco-cli/store"
)

// stubWorkSpaces answers DescribeWorkspacesPools with a canned error and
// nothing else — the other methods exist only to satisfy workSpacesAPI.
type stubWorkSpaces struct{ poolsErr error }

func (s *stubWorkSpaces) DescribeWorkspacesPools(_ context.Context, _ *workspaces.DescribeWorkspacesPoolsInput, _ ...func(*workspaces.Options)) (*workspaces.DescribeWorkspacesPoolsOutput, error) {
	return nil, s.poolsErr
}

func (s *stubWorkSpaces) DescribeWorkspaces(context.Context, *workspaces.DescribeWorkspacesInput, ...func(*workspaces.Options)) (*workspaces.DescribeWorkspacesOutput, error) {
	return &workspaces.DescribeWorkspacesOutput{}, nil
}

func (s *stubWorkSpaces) DescribeConnectionAliases(context.Context, *workspaces.DescribeConnectionAliasesInput, ...func(*workspaces.Options)) (*workspaces.DescribeConnectionAliasesOutput, error) {
	return &workspaces.DescribeConnectionAliasesOutput{}, nil
}

func (s *stubWorkSpaces) DescribeWorkspaceDirectories(context.Context, *workspaces.DescribeWorkspaceDirectoriesInput, ...func(*workspaces.Options)) (*workspaces.DescribeWorkspaceDirectoriesOutput, error) {
	return &workspaces.DescribeWorkspaceDirectoriesOutput{}, nil
}

func (s *stubWorkSpaces) DescribeWorkspaceBundles(context.Context, *workspaces.DescribeWorkspaceBundlesInput, ...func(*workspaces.Options)) (*workspaces.DescribeWorkspaceBundlesOutput, error) {
	return &workspaces.DescribeWorkspaceBundlesOutput{}, nil
}

func (s *stubWorkSpaces) DescribeWorkspaceImages(context.Context, *workspaces.DescribeWorkspaceImagesInput, ...func(*workspaces.Options)) (*workspaces.DescribeWorkspaceImagesOutput, error) {
	return &workspaces.DescribeWorkspaceImagesOutput{}, nil
}

//nolint:revive // method name must match the SDK op (DescribeIpGroups) to satisfy workSpacesAPI.
func (s *stubWorkSpaces) DescribeIpGroups(context.Context, *workspaces.DescribeIpGroupsInput, ...func(*workspaces.Options)) (*workspaces.DescribeIpGroupsOutput, error) {
	return &workspaces.DescribeIpGroupsOutput{}, nil
}

func (s *stubWorkSpaces) DescribeApplications(context.Context, *workspaces.DescribeApplicationsInput, ...func(*workspaces.Options)) (*workspaces.DescribeApplicationsOutput, error) {
	return &workspaces.DescribeApplicationsOutput{}, nil
}

// The predicate decides whether a DescribeWorkspacesPools refusal reaches the
// operator, so assert the OUTCOME on the scan rather than the predicate again:
// both canned availability shapes are silent, a genuine denial warns, and
// neither aborts the scan.
func TestScanWSWorkspacesPools_SkipsAvailabilityGapsAndWarnsOnDenial(t *testing.T) {
	const denial = "User: arn:aws:sts::123456789012:assumed-role/disco/scan is not " +
		"authorized to perform: workspaces:DescribeWorkspacesPools on resource: *"

	for _, tc := range []struct {
		name     string
		msg      string
		wantWarn bool
	}{
		{
			"feature not launched in this region",
			"You do not have the permissions required to perform this action. Refer to " +
				"https://docs.aws.amazon.com/workspaces/latest/adminguide/workspaces-access-control.html",
			false,
		},
		{
			"closed to new customers as of 7/30/2026",
			"Amazon WorkSpaces Pools is no longer available to new customers as of 7/30/2026. " +
				"Refer to https://docs.aws.amazon.com/workspaces/latest/adminguide/wsp-pools-end-of-support.html",
			false,
		},
		{"genuine IAM denial", denial, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			var warnings []store.ScanWarning
			st.OnWarn = func(w store.ScanWarning) { warnings = append(warnings, w) }

			client := &stubWorkSpaces{poolsErr: apiErr("AccessDeniedException", tc.msg)}
			_, _, err := scanWSWorkspacesPools(context.Background(), client,
				newTestAccount(testAccountID), "us-east-1", st, "scan-1")
			if err != nil {
				t.Fatalf("scanWSWorkspacesPools returned %v; want nil — an access denial must never abort the scan", err)
			}
			if got := len(warnings) > 0; got != tc.wantWarn {
				t.Errorf("warned = %v (%+v); want %v", got, warnings, tc.wantWarn)
			}
		})
	}
}
