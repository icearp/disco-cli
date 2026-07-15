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
		resolveKAV2ChildrenToApp,
		EdgeDecl{TypeKAV2ApplicationOutput, TypeKAV2Application, store.RelAttachedTo},
		EdgeDecl{TypeKAV2ApplicationReferenceData, TypeKAV2Application, store.RelAttachedTo},
		EdgeDecl{TypeKAV2ApplicationCloudWatchLogOpt, TypeKAV2Application, store.RelAttachedTo},
	)
	registerResolver(
		resolveKAV2AppRefs,
		EdgeDecl{TypeKAV2Application, TypeIAMRole, store.RelAssumes},
		EdgeDecl{TypeKAV2Application, TypeLogsLogGroup, store.RelUses},
	)
}

// kaLogStreamARNToGroupARN trims the trailing `:log-stream:<name>` segment
// from a CloudWatch Logs log-stream ARN, recovering the parent log-group ARN
// (disco NativeID for `aws:logs:log-group`).
func kaLogStreamARNToGroupARN(s string) string {
	if i := strings.Index(s, ":log-stream:"); i > 0 {
		return s[:i]
	}
	return s
}

// resolveKAV2AppRefs wires v2 applications to ServiceExecutionRole + the
// CloudWatch log groups declared via CloudWatchLoggingOptionDescriptions[].
// Per-logging-option RoleARN does not exist in v2 (centralized on the parent).
func resolveKAV2AppRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeKAV2Application}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	lgSet, err := scannedIDSet(acct, st, TypeLogsLogGroup)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			ServiceExecutionRole                *string `json:"ServiceExecutionRole"`
			CloudWatchLoggingOptionDescriptions []struct {
				LogStreamARN *string `json:"LogStreamARN"`
			} `json:"CloudWatchLoggingOptionDescriptions"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if ra := sv(attrs.ServiceExecutionRole); ra != "" {
			tgt := store.ResourceID("aws", acct.ID, ra)
			if roleSet[tgt] {
				if err := st.UpsertRelationship(r.ID, tgt, store.RelAssumes, "directed", nil); err != nil {
					return fmt.Errorf("upsert ka v2 app→role: %w", err)
				}
			}
		}
		for _, lo := range attrs.CloudWatchLoggingOptionDescriptions {
			if ls := sv(lo.LogStreamARN); ls != "" {
				lg := kaLogStreamARNToGroupARN(ls)
				tgt := store.ResourceID("aws", acct.ID, lg)
				if lgSet[tgt] {
					if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert ka v2 app→log-group: %w", err)
					}
				}
			}
		}
	}
	return nil
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
			Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{k.typ}, Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			parent := kaParentApp(r.NativeID, k.seg)
			if parent == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, parent)
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

func resolveKAV2ChildrenToApp(acct *account, st *store.Store) error {
	return wireKAChildren(acct, st, TypeKAV2Application, []struct{ typ, seg string }{
		{TypeKAV2ApplicationOutput, "output"},
		{TypeKAV2ApplicationReferenceData, "reference-data-source"},
		{TypeKAV2ApplicationCloudWatchLogOpt, "cloud-watch-logging-option"},
	})
}
