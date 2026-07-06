package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveLexChildrenToBot,
		EdgeDecl{TypeLexBotAlias, TypeLexBot, store.RelAttachedTo},
		EdgeDecl{TypeLexBotVersion, TypeLexBot, store.RelAttachedTo},
		EdgeDecl{TypeLexResourcePolicy, TypeLexBot, store.RelAttachedTo},
	)
	registerResolver(
		resolveLexBotRole,
		EdgeDecl{TypeLexBot, TypeIAMRole, store.RelAssumes},
	)
}

// resolveLexBotRole wires each Lex V2 bot to its execution IAM role
// (DescribeBotOutput.RoleArn — populated by Phase-1 enrichment in scanLexBots).
// FK-safe.
func resolveLexBotRole(acct *account, st *store.Store) error {
	bots, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeLexBot}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(bots) == 0 {
		return nil
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	for _, b := range bots {
		var attrs struct {
			RoleArn *string `json:"RoleArn"`
		}
		if err := json.Unmarshal([]byte(b.AttributesJSON), &attrs); err != nil {
			continue
		}
		arn := sv(attrs.RoleArn)
		if arn == "" {
			continue
		}
		tgt := store.ResourceID("aws", acct.ID, TypeIAMRole, arn)
		if !roleSet[tgt] {
			continue
		}
		if err := st.UpsertRelationship(b.ID, tgt, store.RelAssumes, "directed", nil); err != nil {
			return fmt.Errorf("upsert lex bot→role: %w", err)
		}
	}
	return nil
}

// lexBotARNFromChild rebuilds `arn:aws:lex:r:a:bot/{botId}` from any of:
//   - bot-alias  `arn:aws:lex:r:a:bot-alias/{bid}/{aid}`
//   - bot-version `arn:aws:lex:r:a:bot/{bid}/version/{ver}`
//   - resource-policy `arn:aws:lex:r:a:bot/{bid}/policy`
func lexBotARNFromChild(arn string) string {
	if i := strings.Index(arn, ":bot-alias/"); i >= 0 {
		// Replace ":bot-alias/" with ":bot/" and trim alias-id.
		head := arn[:i] + ":bot/"
		tail := arn[i+len(":bot-alias/"):]
		if j := strings.IndexByte(tail, '/'); j >= 0 {
			return head + tail[:j]
		}
		return head + tail
	}
	if i := strings.Index(arn, ":bot/"); i >= 0 {
		// `arn:...:bot/{bid}/(version/...|policy)` — strip after second `/`.
		tail := arn[i+len(":bot/"):]
		if j := strings.IndexByte(tail, '/'); j >= 0 {
			return arn[:i] + ":bot/" + tail[:j]
		}
	}
	return ""
}

func resolveLexChildrenToBot(acct *account, st *store.Store) error {
	botSet, err := scannedIDSet(acct, st, TypeLexBot)
	if err != nil {
		return err
	}
	for _, t := range []string{TypeLexBotAlias, TypeLexBotVersion, TypeLexResourcePolicy} {
		rows, err := st.ListResources(store.ResourceFilter{
			Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{t}, Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			parent := lexBotARNFromChild(r.NativeID)
			if parent == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, TypeLexBot, parent)
			if !botSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert lex %s→bot: %w", t, err)
			}
		}
	}
	return nil
}
