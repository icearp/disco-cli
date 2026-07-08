package gcp

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

// Resolver Wave R18 of the resolver-implementation backlog (ROADMAP.md
// "Resolver buildout"): the two Tags-service types that have a real outbound
// reference (`crm_tags_scanners.go` also emits TagKey/TagValue/TagHold, all
// already Leaf — no outbound field of their own). TagBinding.Parent /
// TagBinding's own scan scope is always the host project (see
// scanCRMLiensAndBindings — Tags are only scanned at project scope today),
// so that reference is already covered by the scanner's own
// RecordHierarchyBatch closure; only the TagValue reference is new here.
func init() {
	registerResolver(resolveCRMTagBindingRelationships,
		EdgeDecl{TypeTagBinding, TypeTagValue, store.RelUses},
	)
	registerResolver(resolveCRMEffectiveTagRelationships,
		EdgeDecl{TypeEffectiveTag, TypeTagValue, store.RelUses},
	)
}

// resolveCRMTagBindingRelationships wires TagBinding -> the TagValue it
// applies (`tagValue`, in the form "tagValues/{id}" — TagValue's own
// NativeID convention).
func resolveCRMTagBindingRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeTagBinding},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	scannedValues, err := scannedIDSet(p, st, TypeTagValue)
	if err != nil {
		return err
	}
	if len(scannedValues) == 0 {
		return nil
	}
	for _, r := range rows {
		var attrs struct {
			TagValue string `json:"tagValue"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if err := upsertIfScanned(st, scannedValues, r.ID, "gcp", p.ID, TypeTagValue, attrs.TagValue, store.RelUses); err != nil {
			return fmt.Errorf("upsert tagBinding→tagValue: %w", err)
		}
	}
	return nil
}

// resolveCRMEffectiveTagRelationships wires EffectiveTag -> the TagValue it
// reflects (`tagValue`, same "tagValues/{id}" form as TagBinding's).
func resolveCRMEffectiveTagRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeEffectiveTag},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	scannedValues, err := scannedIDSet(p, st, TypeTagValue)
	if err != nil {
		return err
	}
	if len(scannedValues) == 0 {
		return nil
	}
	for _, r := range rows {
		var attrs struct {
			TagValue string `json:"tagValue"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if err := upsertIfScanned(st, scannedValues, r.ID, "gcp", p.ID, TypeTagValue, attrs.TagValue, store.RelUses); err != nil {
			return fmt.Errorf("upsert effectiveTag→tagValue: %w", err)
		}
	}
	return nil
}
