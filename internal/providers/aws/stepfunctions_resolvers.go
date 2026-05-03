package aws

import (
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveSFNChildrenToStateMachine,
		EdgeDecl{TypeSFNStateMachineAlias, TypeSFNStateMachine, store.RelAttachedTo},
		EdgeDecl{TypeSFNStateMachineVersion, TypeSFNStateMachine, store.RelAttachedTo},
	)
}

// sfnStateMachineARNFromChild slices an alias / version ARN
// (`arn:aws:states:r:a:stateMachine:NAME:ALIAS_OR_VERSION`) back to its parent
// state-machine ARN (`arn:aws:states:r:a:stateMachine:NAME`) by counting to
// the seventh colon.
func sfnStateMachineARNFromChild(arn string) string {
	colons := 0
	for i := 0; i < len(arn); i++ {
		if arn[i] == ':' {
			colons++
			if colons == 7 {
				return arn[:i]
			}
		}
	}
	return ""
}

func resolveSFNChildrenToStateMachine(acct *account, st *store.Store) error {
	smSet, err := scannedIDSet(acct, st, TypeSFNStateMachine)
	if err != nil {
		return err
	}
	for _, t := range []string{TypeSFNStateMachineAlias, TypeSFNStateMachineVersion} {
		rows, err := st.ListResources(store.ResourceFilter{
			Provider: "aws", AccountID: acct.ID, Types: []string{t}, Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			parent := sfnStateMachineARNFromChild(r.NativeID)
			if parent == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, TypeSFNStateMachine, parent)
			if !smSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert sfn %s→sm: %w", t, err)
			}
		}
	}
	return nil
}
