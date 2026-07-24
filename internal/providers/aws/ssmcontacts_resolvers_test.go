package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveSSMContactsChannelToContact(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	cARN := fmt.Sprintf("arn:aws:ssm-contacts:%s:%s:contact/c-1", testRegion, acct.ID)
	cID := upsertTestResource(t, st, "aws", acct.ID, TypeSSMContactsContact, cARN, testRegion, "{}")
	chARN := fmt.Sprintf("arn:aws:ssm-contacts:%s:%s:contact-channel/c-1/email", testRegion, acct.ID)
	chID := upsertTestResource(t, st, "aws", acct.ID, TypeSSMContactsContactChannel, chARN, testRegion,
		fmt.Sprintf(`{"ContactArn":"%s"}`, cARN))
	if err := resolveSSMContactsChannelToContact(acct, st); err != nil {
		t.Fatalf("resolveSSMContactsChannelToContact: %v", err)
	}
	rels, _ := st.RelationshipsFrom(chID)
	assertRelationship(t, rels, chID, cID, store.RelAttachedTo)
}

func TestResolveSSMContactsRotationContacts(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	cARN := fmt.Sprintf("arn:aws:ssm-contacts:%s:%s:contact/c-1", testRegion, acct.ID)
	cID := upsertTestResource(t, st, "aws", acct.ID, TypeSSMContactsContact, cARN, testRegion, "{}")
	rARN := fmt.Sprintf("arn:aws:ssm-contacts:%s:%s:rotation/r-1", testRegion, acct.ID)
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeSSMContactsRotation, rARN, testRegion,
		fmt.Sprintf(`{"ContactIds":["%s"]}`, cARN))
	if err := resolveSSMContactsRotationContacts(acct, st); err != nil {
		t.Fatalf("resolveSSMContactsRotationContacts: %v", err)
	}
	rels, _ := st.RelationshipsFrom(rID)
	assertRelationship(t, rels, rID, cID, store.RelAttachedTo)
}
