package aws

import (
	"encoding/json"
	"errors"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	wickrtypes "github.com/aws/aws-sdk-go-v2/service/wickr/types"
	"github.com/icearp/disco-cli/store"
)

func wkNetworkAttrs(t *testing.T, keyARN string) string {
	t.Helper()
	b, err := json.Marshal(wickrtypes.Network{NetworkArn: sdkaws.String("unused"), EncryptionKeyArn: nonEmptyPtr(keyARN)})
	if err != nil {
		t.Fatalf("marshal network: %v", err)
	}
	return string(b)
}

// A network uses the scanned key its EncryptionKeyArn names. A network with no
// key, or whose key is not scanned, has no edge.
func TestResolveWickrNetworkKMS_KeyEdge(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	keyARN := "arn:aws:kms:us-east-1:" + testAccountID + ":key/1111aaaa-0000-0000-0000-000000000000"
	keyID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")
	withKey := upsertTestResource(t, st, "aws", acct.ID, TypeWickrNetwork, wkNetA, testRegion, wkNetworkAttrs(t, keyARN))
	noKey := upsertTestResource(t, st, "aws", acct.ID, TypeWickrNetwork, wkNetB, testRegion, wkNetworkAttrs(t, ""))
	unscanned := upsertTestResource(t, st, "aws", acct.ID, TypeWickrNetwork, wkNetNoID, testRegion,
		wkNetworkAttrs(t, "arn:aws:kms:us-east-1:"+testAccountID+":key/2222bbbb-0000-0000-0000-000000000000"))
	noAttrs := upsertTestResource(t, st, "aws", acct.ID, TypeWickrNetwork, wkNetA+"-noattrs", testRegion, "{}")

	if err := resolveWickrNetworkKMS(acct, st); err != nil {
		t.Fatalf("resolveWickrNetworkKMS: %v", err)
	}
	if got := outEdges(t, st, withKey); len(got) != 1 || got[keyID] != store.RelUses {
		t.Errorf("network with key edges = %v, want uses → %s", got, keyID)
	}
	for name, id := range map[string]string{"no key": noKey, "unscanned key": unscanned, "no attrs": noAttrs} {
		if got := outEdges(t, st, id); len(got) != 0 {
			t.Errorf("%s network edges = %v, want none", name, got)
		}
	}
}

// A network whose attributes do not decode is reported, and costs the other
// networks none of their edges.
func TestResolveWickrNetworkKMS_DecodeError(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	keyARN := "arn:aws:kms:us-east-1:" + testAccountID + ":key/1111aaaa-0000-0000-0000-000000000000"
	keyID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")
	upsertTestResource(t, st, "aws", acct.ID, TypeWickrNetwork, wkNetB, testRegion, "[]")
	good := upsertTestResource(t, st, "aws", acct.ID, TypeWickrNetwork, wkNetA, testRegion, wkNetworkAttrs(t, keyARN))

	err := resolveWickrNetworkKMS(acct, st)
	var ute *json.UnmarshalTypeError
	if !errors.As(err, &ute) {
		t.Fatalf("err = %v, want the JSON decode error", err)
	}
	if got := outEdges(t, st, good); len(got) != 1 || got[keyID] != store.RelUses {
		t.Errorf("decodable network edges = %v, want uses → %s", got, keyID)
	}
}
