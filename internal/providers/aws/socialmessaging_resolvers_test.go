package aws

import (
	"testing"

	"github.com/icearp/disco-cli/store"
	socialmessagingtypes "github.com/aws/aws-sdk-go-v2/service/socialmessaging/types"
)

func TestResolveSocialMessagingPhoneNumberWABA(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	wabaArn := "arn:aws:social-messaging:us-east-1:" + acct.ID + ":waba/waba-1"
	phoneArn := "arn:aws:social-messaging:us-east-1:" + acct.ID + ":phone-number-id/phone-number-id-1"

	wabaID := upsertTestResource(t, st, "aws", acct.ID, TypeSocialMessagingWaba, wabaArn, testRegion, "{}")
	phoneAttrs := mustJSON(socialMessagingPhoneNumberAttrs{
		WhatsAppPhoneNumberSummary: socialmessagingtypes.WhatsAppPhoneNumberSummary{Arn: &phoneArn},
		WabaArn:                    wabaArn,
	})
	phoneID := upsertTestResource(t, st, "aws", acct.ID, TypeSocialMessagingPhoneNumberID, phoneArn, testRegion, phoneAttrs)

	if err := resolveSocialMessagingPhoneNumberWABA(acct, st); err != nil {
		t.Fatalf("resolveSocialMessagingPhoneNumberWABA: %v", err)
	}
	rels, _ := st.RelationshipsFrom(phoneID)
	assertRelationship(t, rels, phoneID, wabaID, store.RelAttachedTo)
}

func TestResolveSocialMessagingPhoneNumberWABA_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	phoneArn := "arn:aws:social-messaging:us-east-1:" + acct.ID + ":phone-number-id/phone-number-id-1"
	phoneID := upsertTestResource(t, st, "aws", acct.ID, TypeSocialMessagingPhoneNumberID, phoneArn, testRegion, "{}")

	if err := resolveSocialMessagingPhoneNumberWABA(acct, st); err != nil {
		t.Fatalf("resolveSocialMessagingPhoneNumberWABA (no attrs): %v", err)
	}
	rels, _ := st.RelationshipsFrom(phoneID)
	if len(rels) != 0 {
		t.Errorf("expected no edges for phone number with no WabaArn, got %d", len(rels))
	}
}
