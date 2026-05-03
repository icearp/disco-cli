package aws

import (
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveQBusinessChildrenToApp,
		EdgeDecl{TypeQBusinessIndex, TypeQBusinessApplication, store.RelAttachedTo},
		EdgeDecl{TypeQBusinessPlugin, TypeQBusinessApplication, store.RelAttachedTo},
		EdgeDecl{TypeQBusinessRetriever, TypeQBusinessApplication, store.RelAttachedTo},
		EdgeDecl{TypeQBusinessWebExperience, TypeQBusinessApplication, store.RelAttachedTo},
		EdgeDecl{TypeQBusinessDataSource, TypeQBusinessApplication, store.RelAttachedTo},
	)
	registerResolver(resolveQBusinessDataSourceToIndex,
		EdgeDecl{TypeQBusinessDataSource, TypeQBusinessIndex, store.RelAttachedTo},
	)
}

// qbusinessAppARNFromChild extracts `arn:aws:qbusiness:r:a:application/{id}`
// from any per-application child NativeID of shape
// `…:application/{id}/<kind>/...`.
func qbusinessAppARNFromChild(arn string) string {
	const prefix = ":application/"
	i := strings.Index(arn, prefix)
	if i < 0 {
		return ""
	}
	tail := arn[i+len(prefix):]
	end := strings.IndexByte(tail, '/')
	if end < 0 {
		return ""
	}
	return arn[:i] + prefix + tail[:end]
}

func resolveQBusinessChildrenToApp(acct *account, st *store.Store) error {
	appSet, err := scannedIDSet(acct, st, TypeQBusinessApplication)
	if err != nil {
		return err
	}
	childTypes := []string{
		TypeQBusinessIndex,
		TypeQBusinessPlugin,
		TypeQBusinessRetriever,
		TypeQBusinessWebExperience,
		TypeQBusinessDataSource,
	}
	for _, ctype := range childTypes {
		rows, err := st.ListResources(store.ResourceFilter{
			Provider: "aws", AccountID: acct.ID, Types: []string{ctype}, Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			parent := qbusinessAppARNFromChild(r.NativeID)
			if parent == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, TypeQBusinessApplication, parent)
			if !appSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert qbusiness %s→app: %w", ctype, err)
			}
		}
	}
	return nil
}

// resolveQBusinessDataSourceToIndex wires data-source to its parent index by
// parsing the `/index/{iid}/data-source/{did}` segment out of the NativeID.
func resolveQBusinessDataSourceToIndex(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeQBusinessDataSource}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	idxSet, err := scannedIDSet(acct, st, TypeQBusinessIndex)
	if err != nil {
		return err
	}
	for _, r := range rows {
		const seg = "/data-source/"
		i := strings.LastIndex(r.NativeID, seg)
		if i < 0 {
			continue
		}
		idxARN := r.NativeID[:i]
		tgtID := store.ResourceID("aws", acct.ID, TypeQBusinessIndex, idxARN)
		if !idxSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert qbusiness ds→idx: %w", err)
		}
	}
	return nil
}
