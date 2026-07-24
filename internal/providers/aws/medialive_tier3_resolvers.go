package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveMediaLiveISGRefs,
		EdgeDecl{TypeMediaLiveInputSecurityGroup, TypeMediaLiveChannel, store.RelAttachedTo},
		EdgeDecl{TypeMediaLiveInputSecurityGroup, TypeMediaLiveInput, store.RelAttachedTo},
	)
	registerResolver(
		resolveMediaLiveSdiSourceRefs,
		EdgeDecl{TypeMediaLiveSdiSource, TypeMediaLiveInput, store.RelAttachedTo},
	)
	registerResolver(
		resolveMediaLiveNetworkRefs,
		EdgeDecl{TypeMediaLiveNetwork, TypeMediaLiveCluster, store.RelAttachedTo},
	)
}

func mediaLiveColonARN(region, acct, kind, id string) string {
	return fmt.Sprintf("arn:aws:medialive:%s:%s:%s:%s", region, acct, kind, id)
}

// listOrARN coerces a "channel id or full ARN" entry into an ARN of the given
// kind. ISG.Channels and SdiSource.Inputs are documented as ID lists but
// callers occasionally see ARNs — accept both.
func listOrARN(entry, region, acct, kind string) string {
	if entry == "" {
		return ""
	}
	if strings.HasPrefix(entry, "arn:") {
		return entry
	}
	return mediaLiveColonARN(region, acct, kind, entry)
}

// resolveMediaLiveISGRefs wires each input-security-group to the channels +
// inputs that reference it. ISG.Channels[] and ISG.Inputs[] hold IDs (or
// occasionally ARNs).
func resolveMediaLiveISGRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeMediaLiveInputSecurityGroup}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	chSet, err := scannedIDSet(acct, st, TypeMediaLiveChannel)
	if err != nil {
		return err
	}
	inSet, err := scannedIDSet(acct, st, TypeMediaLiveInput)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			Channels []string `json:"Channels"`
			Inputs   []string `json:"Inputs"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		for _, c := range attrs.Channels {
			arn := listOrARN(c, region, acct.ID, "channel")
			tgtID := store.ResourceID("aws", acct.ID, arn)
			if !chSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert ml isg→channel: %w", err)
			}
		}
		for _, i := range attrs.Inputs {
			arn := listOrARN(i, region, acct.ID, "input")
			tgtID := store.ResourceID("aws", acct.ID, arn)
			if !inSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert ml isg→input: %w", err)
			}
		}
	}
	return nil
}

// resolveMediaLiveSdiSourceRefs wires each SDI source to the inputs that
// reference it via its Inputs[] list.
func resolveMediaLiveSdiSourceRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeMediaLiveSdiSource}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	inSet, err := scannedIDSet(acct, st, TypeMediaLiveInput)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			Inputs []string `json:"Inputs"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		for _, i := range attrs.Inputs {
			arn := listOrARN(i, region, acct.ID, "input")
			tgtID := store.ResourceID("aws", acct.ID, arn)
			if !inSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert ml sdi→input: %w", err)
			}
		}
	}
	return nil
}

// resolveMediaLiveNetworkRefs wires each Network to associated clusters via
// AssociatedClusterIDs[].
func resolveMediaLiveNetworkRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeMediaLiveNetwork}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	clSet, err := scannedIDSet(acct, st, TypeMediaLiveCluster)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			AssociatedClusterIDs []string `json:"AssociatedClusterIds"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		for _, c := range attrs.AssociatedClusterIDs {
			arn := listOrARN(c, region, acct.ID, "cluster")
			tgtID := store.ResourceID("aws", acct.ID, arn)
			if !clSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert ml network→cluster: %w", err)
			}
		}
	}
	return nil
}
