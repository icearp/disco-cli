package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveEMRChildrenToCluster,
		EdgeDecl{TypeEMRStep, TypeEMRCluster, store.RelAttachedTo},
		EdgeDecl{TypeEMRInstanceFleet, TypeEMRCluster, store.RelAttachedTo},
		EdgeDecl{TypeEMRInstanceGroup, TypeEMRCluster, store.RelAttachedTo},
	)
	registerResolver(resolveEMRStudioSessionMappingToStudio,
		EdgeDecl{TypeEMRStudioSessionMapping, TypeEMRStudio, store.RelAttachedTo},
	)
	registerResolver(resolveEMRStudioVPC,
		EdgeDecl{TypeEMRStudio, TypeEC2VPC, store.RelAttachedTo},
	)
}

// resolveEMRStudioVPC wires each EMR Studio to its VPC (StudioSummary.VpcId).
// FK-safe.
func resolveEMRStudioVPC(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEMRStudio}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	vpcSet, err := scannedIDSet(acct, st, TypeEC2VPC)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			VpcID *string `json:"VpcId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		v := sv(attrs.VpcID)
		if v == "" {
			continue
		}
		tgt := store.ResourceID("aws", acct.ID, TypeEC2VPC, ec2ARN(sv(r.Region), acct.ID, "vpc", v))
		if !vpcSet[tgt] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert emr studio→vpc: %w", err)
		}
	}
	return nil
}

// emrParentARN trims a `/segment/...` tail off a child NativeID to recover
// the parent ARN.
func emrParentARN(arn, segment string) string {
	i := strings.Index(arn, "/"+segment+"/")
	if i < 0 {
		return ""
	}
	return arn[:i]
}

// resolveEMRChildrenToCluster wires steps, instance-fleets and instance-groups
// to the cluster they belong to via NativeID parent extract.
func resolveEMRChildrenToCluster(acct *account, st *store.Store) error {
	clSet, err := scannedIDSet(acct, st, TypeEMRCluster)
	if err != nil {
		return err
	}
	for _, ct := range []struct{ typ, seg string }{
		{TypeEMRStep, "step"},
		{TypeEMRInstanceFleet, "instance-fleet"},
		{TypeEMRInstanceGroup, "instance-group"},
	} {
		rows, err := st.ListResources(store.ResourceFilter{
			Provider: "aws", AccountID: acct.ID, Types: []string{ct.typ}, Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			parent := emrParentARN(r.NativeID, ct.seg)
			if parent == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, TypeEMRCluster, parent)
			if !clSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert emr %s→cluster: %w", ct.typ, err)
			}
		}
	}
	return nil
}

// resolveEMRStudioSessionMappingToStudio wires each session-mapping back to
// its parent studio via the `/identity/...` NativeID tail.
func resolveEMRStudioSessionMappingToStudio(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEMRStudioSessionMapping}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	stSet, err := scannedIDSet(acct, st, TypeEMRStudio)
	if err != nil {
		return err
	}
	for _, r := range rows {
		parent := emrParentARN(r.NativeID, "identity")
		if parent == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, TypeEMRStudio, parent)
		if !stSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert emr session-mapping→studio: %w", err)
		}
	}
	return nil
}
