package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestLexBotARNFromChild(t *testing.T) {
	cases := []struct{ in, want string }{
		{"arn:aws:lex:us-east-1:123:bot-alias/B1/A1", "arn:aws:lex:us-east-1:123:bot/B1"},
		{"arn:aws:lex:us-east-1:123:bot/B1/version/3", "arn:aws:lex:us-east-1:123:bot/B1"},
		{"arn:aws:lex:us-east-1:123:bot/B1/policy", "arn:aws:lex:us-east-1:123:bot/B1"},
		{"arn:aws:lex:us-east-1:123:bot/B1", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := lexBotARNFromChild(c.in); got != c.want {
			t.Errorf("lexBotARNFromChild(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestResolveLexChildrenToBot(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	botARN := fmt.Sprintf("arn:aws:lex:%s:%s:bot/B1", testRegion, acct.ID)
	botID := upsertTestResource(t, st, "aws", acct.ID, TypeLexBot, botARN, testRegion, "{}")
	aliasARN := fmt.Sprintf("arn:aws:lex:%s:%s:bot-alias/B1/A1", testRegion, acct.ID)
	aliasID := upsertTestResource(t, st, "aws", acct.ID, TypeLexBotAlias, aliasARN, testRegion, "{}")
	verARN := botARN + "/version/3"
	verID := upsertTestResource(t, st, "aws", acct.ID, TypeLexBotVersion, verARN, testRegion, "{}")
	rpARN := botARN + "/policy"
	rpID := upsertTestResource(t, st, "aws", acct.ID, TypeLexResourcePolicy, rpARN, testRegion, "{}")
	if err := resolveLexChildrenToBot(acct, st); err != nil {
		t.Fatalf("resolveLexChildrenToBot: %v", err)
	}
	for _, c := range []string{aliasID, verID, rpID} {
		rels, _ := st.RelationshipsFrom(c)
		assertRelationship(t, rels, c, botID, store.RelAttachedTo)
	}
}
