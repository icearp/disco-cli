package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() { registerResolver(resolveLogsRelationships) }

// resolveLogsRelationships runs all CloudWatch Logs relationship passes.
func resolveLogsRelationships(acct *account, st *store.Store) error {
	if err := resolveLogsDeliveryLinks(acct, st); err != nil {
		return err
	}
	return resolveLogsGroupAnomalyDetectors(acct, st)
}

// resolveLogsDeliveryLinks links each delivery to its source and destination.
// Delivery → DeliverySource: uses
// Delivery → DeliveryDestination: uses
func resolveLogsDeliveryLinks(acct *account, st *store.Store) error {
	deliveries, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeLogsDelivery},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(deliveries) == 0 {
		return nil
	}

	// Build name→ID map for delivery sources. Delivery source ARNs have the
	// format arn:aws:logs:{region}:{account}:delivery-source:{name}, so the
	// name is the last colon-separated component of the NativeID (ARN).
	sources, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeLogsDeliverySource},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	bySourceName := make(map[string]string, len(sources))
	for _, s := range sources {
		// Use Name column when set; otherwise extract from NativeID.
		name := ""
		if s.Name != nil {
			name = *s.Name
		} else if idx := strings.LastIndex(s.NativeID, ":"); idx >= 0 {
			name = s.NativeID[idx+1:]
		}
		if name != "" {
			bySourceName[name] = s.ID
		}
	}

	// Build ARN→ID map for delivery destinations.
	dests, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeLogsDeliveryDest},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	byDestARN := make(map[string]string, len(dests))
	for _, d := range dests {
		byDestARN[d.NativeID] = d.ID
	}

	for _, d := range deliveries {
		var attrs struct {
			DeliverySourceName     *string `json:"DeliverySourceName"`
			DeliveryDestinationArn *string `json:"DeliveryDestinationArn"`
		}
		if err := json.Unmarshal([]byte(d.AttributesJSON), &attrs); err != nil {
			continue
		}
		if srcName := sv(attrs.DeliverySourceName); srcName != "" {
			if srcID, ok := bySourceName[srcName]; ok {
				if err := st.UpsertRelationship(d.ID, srcID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert delivery→source relationship: %w", err)
				}
			}
		}
		if destARN := sv(attrs.DeliveryDestinationArn); destARN != "" {
			if destID, ok := byDestARN[destARN]; ok {
				if err := st.UpsertRelationship(d.ID, destID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert delivery→destination relationship: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveLogsGroupAnomalyDetectors links each anomaly detector to the log
// groups it monitors.
// LogAnomalyDetector → LogGroup: uses
func resolveLogsGroupAnomalyDetectors(acct *account, st *store.Store) error {
	detectors, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeLogsLogAnomalyDetector},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(detectors) == 0 {
		return nil
	}

	// Build ARN→ID map for log groups. Log group NativeID is the clean ARN
	// (without trailing ":*"), matching what anomaly detectors store in
	// LogGroupArnList.
	groups, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeLogsLogGroup},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	byGroupARN := make(map[string]string, len(groups))
	for _, g := range groups {
		byGroupARN[g.NativeID] = g.ID
	}

	for _, d := range detectors {
		var attrs struct {
			LogGroupArnList []string `json:"LogGroupArnList"`
		}
		if err := json.Unmarshal([]byte(d.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, arn := range attrs.LogGroupArnList {
			// Strip trailing ":*" to match stored NativeID.
			cleanARN := logGroupARN(&arn)
			if groupID, ok := byGroupARN[cleanARN]; ok {
				if err := st.UpsertRelationship(d.ID, groupID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert anomaly-detector→log-group relationship: %w", err)
				}
			}
		}
	}
	return nil
}
