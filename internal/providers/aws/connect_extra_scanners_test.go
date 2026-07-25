package aws

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/connect"
	cttypes "github.com/aws/aws-sdk-go-v2/service/connect/types"
	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

type stubConnectExtra struct {
	vocabularies []cttypes.VocabularySummary
	authProfiles []cttypes.AuthenticationProfileSummary
}

func (s *stubConnectExtra) SearchVocabularies(_ context.Context, _ *connect.SearchVocabulariesInput, _ ...func(*connect.Options)) (*connect.SearchVocabulariesOutput, error) {
	return &connect.SearchVocabulariesOutput{VocabularySummaryList: s.vocabularies}, nil
}

func (s *stubConnectExtra) ListAuthenticationProfiles(_ context.Context, _ *connect.ListAuthenticationProfilesInput, _ ...func(*connect.Options)) (*connect.ListAuthenticationProfilesOutput, error) {
	return &connect.ListAuthenticationProfilesOutput{AuthenticationProfileSummaryList: s.authProfiles}, nil
}

func TestScanConnectExtra(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	vocARN := "arn:aws:connect:us-east-1:111111111111:instance/i-1/vocabulary/v-1"
	apARN := "arn:aws:connect:us-east-1:111111111111:instance/i-1/authentication-profile/ap-1"
	stub := &stubConnectExtra{
		vocabularies: []cttypes.VocabularySummary{{Arn: ptrStr(vocARN), Id: ptrStr("v-1"), Name: ptrStr("medical"), State: cttypes.VocabularyStateActive}},
		authProfiles: []cttypes.AuthenticationProfileSummary{{Arn: ptrStr(apARN), Id: ptrStr("ap-1"), Name: ptrStr("default")}},
	}
	insts := []cttypes.InstanceSummary{{Id: ptrStr("i-1")}}
	total, _, err := scanConnectExtra(context.Background(), stub, insts, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanConnectExtra: %v", err)
	}
	if total != 2 {
		t.Errorf("total=%d, want 2", total)
	}
	for _, tc := range []struct {
		typ, arn string
	}{{TypeConnectVocabulary, vocARN}, {TypeConnectAuthenticationProfile, apARN}} {
		rows, err := st.ListResources(store.ResourceFilter{Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{tc.typ}, Limit: util.AllResources})
		if err != nil {
			t.Fatalf("ListResources %s: %v", tc.typ, err)
		}
		if len(rows) != 1 || rows[0].NativeID != tc.arn {
			t.Errorf("%s: rows=%+v, want one with NativeID %s", tc.typ, rows, tc.arn)
		}
	}
}

func TestScanConnectExtra_NoInstances(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	total, inserted, err := scanConnectExtra(context.Background(), &stubConnectExtra{}, nil, acct, testRegion, st, testScanID)
	if err != nil || total != 0 || inserted != 0 {
		t.Errorf("got (%d,%d,%v), want (0,0,nil)", total, inserted, err)
	}
}
