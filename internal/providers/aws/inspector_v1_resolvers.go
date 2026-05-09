package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(
		resolveInspectorAssessmentTargetRefs,
		EdgeDecl{TypeInspectorAssessmentTarget, TypeInspectorResourceGroup, store.RelAttachedTo},
	)
	registerResolver(
		resolveInspectorAssessmentTemplateRefs,
		EdgeDecl{TypeInspectorAssessmentTemplate, TypeInspectorAssessmentTarget, store.RelAttachedTo},
	)
}

// resolveInspectorAssessmentTargetRefs wires assessment-target → resource-group
// (ResourceGroupArn).
func resolveInspectorAssessmentTargetRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeInspectorAssessmentTarget}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	rgSet, err := scannedIDSet(acct, st, TypeInspectorResourceGroup)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			ResourceGroupArn *string `json:"ResourceGroupArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if g := sv(attrs.ResourceGroupArn); g != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeInspectorResourceGroup, g)
			if rgSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert inspector at→rg: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveInspectorAssessmentTemplateRefs wires assessment-template →
// assessment-target (AssessmentTargetArn).
func resolveInspectorAssessmentTemplateRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeInspectorAssessmentTemplate}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	atSet, err := scannedIDSet(acct, st, TypeInspectorAssessmentTarget)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			AssessmentTargetArn *string `json:"AssessmentTargetArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if a := sv(attrs.AssessmentTargetArn); a != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeInspectorAssessmentTarget, a)
			if atSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert inspector tpl→at: %w", err)
				}
			}
		}
	}
	return nil
}
