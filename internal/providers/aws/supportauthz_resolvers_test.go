package aws

import (
	"encoding/json"
	"errors"
	"testing"

	satypes "github.com/aws/aws-sdk-go-v2/service/supportauthz/types"
	"github.com/icearp/disco-cli/store"
)

// saPermitAttrs renders a permit signed by keyARN the way scanSupportPermits
// stores it; an empty keyARN leaves SigningKeyInfo absent.
func saPermitAttrs(keyARN string) string {
	out := saPermitOutput(saPermitB)
	out.SigningKeyInfo = nil
	if keyARN != "" {
		out.SigningKeyInfo = &satypes.SigningKeyInfoMemberKmsKey{Value: keyARN}
	}
	return mustJSON(flattenSupportPermit(out))
}

// A permit uses the scanned key that signs it. A permit with no key, or whose
// key is not scanned, has no edge.
func TestResolveSupportPermitKMS_KeyEdge(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	keyID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, saKeyARN, saRegion, "{}")
	signed := upsertTestResource(t, st, "aws", acct.ID, TypeSupportAuthZSupportPermit, saPermitA, saRegion, saPermitAttrs(saKeyARN))
	noKey := upsertTestResource(t, st, "aws", acct.ID, TypeSupportAuthZSupportPermit, saPermitB, saRegion, saPermitAttrs(""))
	unscanned := upsertTestResource(t, st, "aws", acct.ID, TypeSupportAuthZSupportPermit, saPermitA+"x", saRegion,
		saPermitAttrs("arn:aws:kms:"+saRegion+":"+testAccountID+":key/2222bbbb-0000-0000-0000-000000000000"))
	noAttrs := upsertTestResource(t, st, "aws", acct.ID, TypeSupportAuthZSupportPermit, saPermitA+"y", saRegion, "{}")

	if err := resolveSupportPermitKMS(acct, st); err != nil {
		t.Fatalf("resolveSupportPermitKMS: %v", err)
	}
	if got := outEdges(t, st, signed); len(got) != 1 || got[keyID] != store.RelUses {
		t.Errorf("signed permit edges = %v, want uses → %s", got, keyID)
	}
	for name, id := range map[string]string{"no key": noKey, "unscanned key": unscanned, "no attrs": noAttrs} {
		if got := outEdges(t, st, id); len(got) != 0 {
			t.Errorf("%s permit edges = %v, want none", name, got)
		}
	}
}

// A permit whose attributes do not decode is reported, and costs the other
// permits none of their edges.
func TestResolveSupportPermitKMS_DecodeError(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	keyID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, saKeyARN, saRegion, "{}")
	upsertTestResource(t, st, "aws", acct.ID, TypeSupportAuthZSupportPermit, saPermitB, saRegion, "[]")
	good := upsertTestResource(t, st, "aws", acct.ID, TypeSupportAuthZSupportPermit, saPermitA, saRegion, saPermitAttrs(saKeyARN))

	err := resolveSupportPermitKMS(acct, st)
	var ute *json.UnmarshalTypeError
	if !errors.As(err, &ute) {
		t.Fatalf("err = %v, want the JSON decode error", err)
	}
	if got := outEdges(t, st, good); len(got) != 1 || got[keyID] != store.RelUses {
		t.Errorf("decodable permit edges = %v, want uses → %s", got, keyID)
	}
}
