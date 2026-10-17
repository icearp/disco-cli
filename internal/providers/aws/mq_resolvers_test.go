package aws

import (
	"encoding/json"
	"errors"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	mqtypes "github.com/aws/aws-sdk-go-v2/service/mq/types"
	"github.com/icearp/disco-cli/store"
)

const mqTestResourceConfigARN = "arn:aws:vpc-lattice:" + testRegion + ":" + testAccountID + ":resourceconfiguration/rcfg-1"

// seedMQSharedResource stores a shared resource named name under broker b-1,
// with the contains pair the scanner records.
func seedMQSharedResource(t *testing.T, st *store.Store, resourceARN, attrs, name string) string {
	t.Helper()
	brokerID := upsertTestResource(t, st, "aws", testAccountID, TypeMQBroker, mqTestBrokerARN("b-1"), testRegion, "{}")
	id := upsertTestResourceNamed(t, st, TypeMQSharedResource, mqSharedResourceNativeID(mqTestBrokerARN("b-1"), resourceARN), testRegion, attrs, name)
	if err := st.RecordHierarchyBatch([][2]string{{id, brokerID}}); err != nil {
		t.Fatalf("RecordHierarchyBatch: %v", err)
	}
	return id
}

func usesTargets(t *testing.T, st *store.Store, fromID string) map[string]bool {
	t.Helper()
	rels, err := st.RelationshipsFrom(fromID, store.RelUses)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	out := map[string]bool{}
	for _, r := range rels {
		out[r.ToID] = true
	}
	return out
}

func TestResolveMQSharedResourceRefs_LinksScannedTargets(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	shareID := upsertTestResource(t, st, "aws", acct.ID, TypeRAMResourceShare, mqTestShareARN("rs-1"), testRegion, "{}")
	cfgID := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeResourceConfiguration, mqTestResourceConfigARN, testRegion, "{}")
	srID := seedMQSharedResource(t, st, mqTestResourceConfigARN, mustJSON(mqtypes.SharedResource{
		ResourceArn:       sdkaws.String(mqTestResourceConfigARN),
		Type:              mqtypes.SharedResourceTypeResource,
		ResourceShareArns: []string{mqTestShareARN("rs-1"), mqTestShareARN("rs-unscanned")},
	}), mqTestResourceConfigARN)

	if err := resolveMQSharedResourceRefs(acct, st); err != nil {
		t.Fatalf("resolveMQSharedResourceRefs: %v", err)
	}
	got := usesTargets(t, st, srID)
	if len(got) != 2 || !got[shareID] || !got[cfgID] {
		t.Errorf("uses targets = %v; want only the scanned share %s and resource configuration %s", got, shareID, cfgID)
	}
}

func TestResolveMQSharedResourceRefs_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	upsertTestResource(t, st, "aws", acct.ID, TypeRAMResourceShare, mqTestShareARN("rs-1"), testRegion, "{}")
	srID := seedMQSharedResource(t, st, mqTestShareARN("rs-1"), "{}", mqTestShareARN("rs-1"))

	if err := resolveMQSharedResourceRefs(acct, st); err != nil {
		t.Fatalf("resolveMQSharedResourceRefs: %v", err)
	}
	if got := usesTargets(t, st, srID); len(got) != 0 {
		t.Errorf("uses targets = %v; want none", got)
	}
}

// An undecodable row is returned as an error and costs no other row its edge.
func TestResolveMQSharedResourceRefs_DecodeErrorKeepsOtherRows(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	shareID := upsertTestResource(t, st, "aws", acct.ID, TypeRAMResourceShare, mqTestShareARN("rs-1"), testRegion, "{}")
	// ListResources orders by name, so the bad row "a" is read before the
	// valid row "b", whose edge must survive the decode error.
	seedMQSharedResource(t, st, mqTestShareARN("rs-bad"), `["not an object"]`, "a")
	okID := seedMQSharedResource(t, st, mqTestShareARN("rs-1"), mustJSON(mqSharedResource("rs-1")), "b")

	err := resolveMQSharedResourceRefs(acct, st)
	var typeErr *json.UnmarshalTypeError
	if !errors.As(err, &typeErr) {
		t.Errorf("err = %v; want the decode error", err)
	}
	if got := usesTargets(t, st, okID); !got[shareID] {
		t.Errorf("uses targets of the good row = %v; want %s", got, shareID)
	}
}
