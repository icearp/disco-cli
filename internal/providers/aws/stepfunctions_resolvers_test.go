package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestSFNStateMachineARNFromChild(t *testing.T) {
	cases := []struct{ in, want string }{
		{"arn:aws:states:us-east-1:123:stateMachine:my-sm:PROD", "arn:aws:states:us-east-1:123:stateMachine:my-sm"},
		{"arn:aws:states:us-east-1:123:stateMachine:my-sm:3", "arn:aws:states:us-east-1:123:stateMachine:my-sm"},
		{"arn:aws:states:us-east-1:123:stateMachine:my-sm", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := sfnStateMachineARNFromChild(c.in); got != c.want {
			t.Errorf("sfnStateMachineARNFromChild(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestResolveSFNChildrenToStateMachine(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	smARN := fmt.Sprintf("arn:aws:states:%s:%s:stateMachine:my-sm", testRegion, acct.ID)
	smID := upsertTestResource(t, st, "aws", acct.ID, TypeSFNStateMachine, smARN, testRegion, "{}")
	aARN := smARN + ":PROD"
	aID := upsertTestResource(t, st, "aws", acct.ID, TypeSFNStateMachineAlias, aARN, testRegion, "{}")
	vARN := smARN + ":3"
	vID := upsertTestResource(t, st, "aws", acct.ID, TypeSFNStateMachineVersion, vARN, testRegion, "{}")
	if err := resolveSFNChildrenToStateMachine(acct, st); err != nil {
		t.Fatalf("resolveSFNChildrenToStateMachine: %v", err)
	}
	for _, c := range []string{aID, vID} {
		rels, _ := st.RelationshipsFrom(c)
		assertRelationship(t, rels, c, smID, store.RelAttachedTo)
	}
}
