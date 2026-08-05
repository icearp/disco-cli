package aws

import (
	"context"
	"slices"
	"strconv"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/appstream"
	astypes "github.com/aws/aws-sdk-go-v2/service/appstream/types"
	"github.com/icearp/disco-cli/store"
)

// stubAppStreamUsers records every AuthenticationType DescribeUsers is asked
// for. The interface is embedded so the 13 methods this test does not exercise
// stay unimplemented — calling one panics, which is the intent.
type stubAppStreamUsers struct {
	appStreamAPI
	asked []astypes.AuthenticationType
	// pages are returned one per call; every page but the last hands back a
	// NextToken, so the scanner's pagination loop is what advances through them.
	pages [][]astypes.User
	calls int
}

func (s *stubAppStreamUsers) DescribeUsers(_ context.Context, in *appstream.DescribeUsersInput, _ ...func(*appstream.Options)) (*appstream.DescribeUsersOutput, error) {
	s.asked = append(s.asked, in.AuthenticationType)
	if s.calls >= len(s.pages) {
		return &appstream.DescribeUsersOutput{}, nil
	}
	out := &appstream.DescribeUsersOutput{Users: s.pages[s.calls]}
	s.calls++
	if s.calls < len(s.pages) {
		tok := "page" + strconv.Itoa(s.calls)
		out.NextToken = &tok
	}
	return out, nil
}

func asUser(accountID, name string) astypes.User {
	arn := "arn:aws:appstream:" + testRegion + ":" + accountID + ":user/USERPOOL/" + name
	return astypes.User{Arn: &arn, UserName: &name}
}

// TestScanASUsersAsksUserpoolOnly pins the one AuthenticationType DescribeUsers
// accepts. The SDK field doc says "You must specify USERPOOL"; the enum's other
// values belong to the shape shared with CreateUser/EnableUser and name nothing
// a user pool holds, so asking for one is a guaranteed 400.
//
// This regressed invisibly once: the scanner iterated USERPOOL + API and
// swallowed the resulting InvalidParameterValueException, so a doomed call per
// region per scan looked like success until an SDK change stopped the swallow
// from matching.
func TestScanASUsersAsksUserpoolOnly(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	// Two pages, so the assertion below counts pagination calls rather than
	// auth-type calls and cannot pass by conflating the two.
	stub := &stubAppStreamUsers{pages: [][]astypes.User{
		{asUser(testAccountID, "one@example.com")},
		{asUser(testAccountID, "two@example.com")},
	}}

	total, _, err := scanASUsers(context.Background(), stub, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 2 {
		t.Fatalf("total=%d want 2 (both pages)", total)
	}
	for _, n := range []string{"one@example.com", "two@example.com"} {
		arn := *asUser(testAccountID, n).Arn
		if _, err := st.GetResource(store.ResourceID("aws", acct.ID, arn)); err != nil {
			t.Errorf("user %s missing: %v", n, err)
		}
	}

	// One call per page, all USERPOOL. An extra auth type would show up here as
	// a third entry.
	want := []astypes.AuthenticationType{astypes.AuthenticationTypeUserpool, astypes.AuthenticationTypeUserpool}
	if !slices.Equal(stub.asked, want) {
		t.Errorf("DescribeUsers asked for %v, want exactly %v", stub.asked, want)
	}
}

// TestScanASUsersEmpty guards the empty-pool path: no rows, no error.
func TestScanASUsersEmpty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	total, _, err := scanASUsers(context.Background(), &stubAppStreamUsers{}, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 0 {
		t.Errorf("empty total=%d want 0", total)
	}
}
