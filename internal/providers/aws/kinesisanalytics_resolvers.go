package aws

import (
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveKAV1ChildrenToApp,
		EdgeDecl{TypeKinesisAnalyticsApplicationOutput, TypeKinesisAnalyticsApplication, store.RelAttachedTo},
		EdgeDecl{TypeKinesisAnalyticsApplicationReferenceData, TypeKinesisAnalyticsApplication, store.RelAttachedTo},
	)
	registerResolver(resolveKAV2ChildrenToApp,
		EdgeDecl{TypeKAV2ApplicationOutput, TypeKAV2Application, store.RelAttachedTo},
		EdgeDecl{TypeKAV2ApplicationReferenceData, TypeKAV2Application, store.RelAttachedTo},
		EdgeDecl{TypeKAV2ApplicationCloudWatchLogOpt, TypeKAV2Application, store.RelAttachedTo},
	)
}

// kaParentApp recovers the parent application ARN from a child NativeID of
// shape `{appARN}/<segment>/{id}`.
func kaParentApp(nativeID, segment string) string {
	i := strings.Index(nativeID, "/"+segment+"/")
	if i < 0 {
		return ""
	}
	return nativeID[:i]
}

func wireKAChildren(acct *account, st *store.Store, parentType string, kids []struct{ typ, seg string }) error {
	parentSet, err := scannedIDSet(acct, st, parentType)
	if err != nil {
		return err
	}
	for _, k := range kids {
		rows, err := st.ListResources(store.ResourceFilter{
			Provider: "aws", AccountID: acct.ID, Types: []string{k.typ}, Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			parent := kaParentApp(r.NativeID, k.seg)
			if parent == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, parentType, parent)
			if !parentSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert ka %s→app: %w", k.typ, err)
			}
		}
	}
	return nil
}

func resolveKAV1ChildrenToApp(acct *account, st *store.Store) error {
	return wireKAChildren(acct, st, TypeKinesisAnalyticsApplication, []struct{ typ, seg string }{
		{TypeKinesisAnalyticsApplicationOutput, "output"},
		{TypeKinesisAnalyticsApplicationReferenceData, "reference-data-source"},
	})
}

func resolveKAV2ChildrenToApp(acct *account, st *store.Store) error {
	return wireKAChildren(acct, st, TypeKAV2Application, []struct{ typ, seg string }{
		{TypeKAV2ApplicationOutput, "output"},
		{TypeKAV2ApplicationReferenceData, "reference-data-source"},
		{TypeKAV2ApplicationCloudWatchLogOpt, "cloud-watch-logging-option"},
	})
}
