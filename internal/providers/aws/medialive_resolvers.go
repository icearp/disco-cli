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
		resolveMediaLiveChannelRefs,
		EdgeDecl{TypeMediaLiveChannel, TypeMediaLiveInput, store.RelUses},
		EdgeDecl{TypeMediaLiveChannel, TypeIAMRole, store.RelAssumes},
	)
	registerResolver(
		resolveMediaLiveInputSecurityGroups,
		EdgeDecl{TypeMediaLiveInput, TypeMediaLiveInputSecurityGroup, store.RelUses},
	)
	registerResolver(
		resolveMediaLiveChannelPlacementGroupCluster,
		EdgeDecl{TypeMediaLiveChannelPlacementGroup, TypeMediaLiveCluster, store.RelAttachedTo},
	)
	registerResolver(
		resolveMediaLiveMultiplexProgramParent,
		EdgeDecl{TypeMediaLiveMultiplexProgram, TypeMediaLiveMultiplex, store.RelAttachedTo},
	)
}

// medialiveByIDIndex builds a map keyed on the SDK ID field — necessary
// because some children (multiplex-program, channel-placement-group) only
// carry the parent's bare ID, not its full ARN.
func medialiveByIDIndex(acct *account, st *store.Store, rtype string) (map[string]string, error) {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{rtype},
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	idx := make(map[string]string, len(rows))
	for _, r := range rows {
		var attrs struct {
			ID *string `json:"Id"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		id := sv(attrs.ID)
		if id == "" {
			continue
		}
		idx[id] = r.ID
	}
	return idx, nil
}

// resolveMediaLiveChannelRefs walks each channel's InputAttachments[].InputID
// + RoleArn and emits the corresponding edges.
func resolveMediaLiveChannelRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeMediaLiveChannel},
		Limit: util.AllResources,
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
	inputIdx, err := medialiveByIDIndex(acct, st, TypeMediaLiveInput)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			RoleArn          *string `json:"RoleArn"`
			InputAttachments []struct {
				InputID *string `json:"InputId"`
			} `json:"InputAttachments"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if arn := sv(attrs.RoleArn); arn != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeIAMRole, arn)
			if roleSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAssumes, "directed", nil); err != nil {
					return fmt.Errorf("upsert medialive-channel→role: %w", err)
				}
			}
		}
		seen := map[string]bool{}
		for _, ia := range attrs.InputAttachments {
			id := sv(ia.InputID)
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			tgtID, ok := inputIdx[id]
			if !ok {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert medialive-channel→input: %w", err)
			}
		}
	}
	return nil
}

// resolveMediaLiveInputSecurityGroups walks each input's
// SecurityGroups[] (string IDs) and emits uses edges to the named ISG.
func resolveMediaLiveInputSecurityGroups(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeMediaLiveInput},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	isgIdx, err := medialiveByIDIndex(acct, st, TypeMediaLiveInputSecurityGroup)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			SecurityGroups []string `json:"SecurityGroups"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		seen := map[string]bool{}
		for _, id := range attrs.SecurityGroups {
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			tgtID, ok := isgIdx[id]
			if !ok {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert medialive-input→isg: %w", err)
			}
		}
	}
	return nil
}

// resolveMediaLiveChannelPlacementGroupCluster links each placement group to
// its parent cluster via ClusterID.
func resolveMediaLiveChannelPlacementGroupCluster(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeMediaLiveChannelPlacementGroup},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	clusterIdx, err := medialiveByIDIndex(acct, st, TypeMediaLiveCluster)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			ClusterID *string `json:"ClusterId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		id := sv(attrs.ClusterID)
		if id == "" {
			continue
		}
		tgtID, ok := clusterIdx[id]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert medialive-cpg→cluster: %w", err)
		}
	}
	return nil
}

// resolveMediaLiveMultiplexProgramParent links each multiplex-program to its
// parent multiplex. Program NativeID shape:
// `arn:aws:medialive:r:a:multiplexprogram/{multiplexId}/{programName}`.
func resolveMediaLiveMultiplexProgramParent(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeMediaLiveMultiplexProgram},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	multiplexIdx, err := medialiveByIDIndex(acct, st, TypeMediaLiveMultiplex)
	if err != nil {
		return err
	}
	for _, r := range rows {
		const seg = "multiplexprogram/"
		i := strings.Index(r.NativeID, seg)
		if i < 0 {
			continue
		}
		tail := r.NativeID[i+len(seg):]
		end := strings.IndexByte(tail, '/')
		if end < 0 {
			continue
		}
		mid := tail[:end]
		tgtID, ok := multiplexIdx[mid]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert medialive-mp→multiplex: %w", err)
		}
	}
	return nil
}
