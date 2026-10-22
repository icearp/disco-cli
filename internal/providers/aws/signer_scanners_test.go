package aws

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/signer"
	signertypes "github.com/aws/aws-sdk-go-v2/service/signer/types"
)

// TestScanSigner_ProfileReadsTags pins that a signing-profile row carries its
// listed element's Tags, and that an untagged profile stores none.
func TestScanSigner_ProfileReadsTags(t *testing.T) {
	const (
		tagged   = "arn:aws:signer:us-east-1:123456789012:/signing-profiles/p1"
		untagged = "arn:aws:signer:us-east-1:123456789012:/signing-profiles/p2"
	)
	handlers := map[string]func(in any) (any, error){
		"ListSigningProfiles": func(any) (any, error) {
			return &signer.ListSigningProfilesOutput{Profiles: []signertypes.SigningProfile{
				{Arn: ptrStr(tagged), ProfileName: ptrStr("p1"), Tags: map[string]string{"team": "sec"}},
				{Arn: ptrStr(untagged), ProfileName: ptrStr("p2")},
			}}, nil
		},
		"ListProfilePermissions": func(any) (any, error) {
			return &signer.ListProfilePermissionsOutput{}, nil
		},
	}
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, errors.New("unexpected op"))
	total, _, err := scanSigner(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanSigner: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}

	rows := rowsByNativeID(t, st, TypeSignerSigningProfile)
	assertTagsMap(t, rows[tagged].TagsJSON, map[string]string{"team": "sec"})
	if got := rows[untagged].TagsJSON; got != nil {
		t.Errorf("untagged profile TagsJSON = %q, want nil", *got)
	}
}
