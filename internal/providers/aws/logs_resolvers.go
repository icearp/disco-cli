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
		resolveLogsRelationships,
		EdgeDecl{TypeLogsDelivery, TypeLogsDeliverySource, store.RelUses},
		EdgeDecl{TypeLogsDelivery, TypeLogsDeliveryDest, store.RelUses},
		EdgeDecl{TypeLogsLogAnomalyDetector, TypeLogsLogGroup, store.RelUses},
		EdgeDecl{TypeLogsDeliveryDest, TypeLogsLogGroup, store.RelUses},
		EdgeDecl{TypeLogsDeliveryDest, TypeS3Bucket, store.RelUses},
		EdgeDecl{TypeLogsDeliveryDest, TypeFirehoseDeliveryStream, store.RelUses},
	)
}

// resolveLogsRelationships runs all CloudWatch Logs relationship passes.
func resolveLogsRelationships(acct *account, st *store.Store) error {
	if err := resolveLogsDeliveryLinks(acct, st); err != nil {
		return err
	}
	if err := resolveLogsDeliveryDestTarget(acct, st); err != nil {
		return err
	}
	return resolveLogsGroupAnomalyDetectors(acct, st)
}

// resolveLogsDeliveryLinks links each delivery to its source and destination.
// Delivery → DeliverySource: uses
// Delivery → DeliveryDestination: uses
func resolveLogsDeliveryLinks(acct *account, st *store.Store) error {
	deliveries, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"},
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
		Providers: []string{"aws"},
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
		Providers: []string{"aws"},
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

// resolveLogsDeliveryDestTarget wires each delivery-destination to its
// underlying receive resource — log group, S3 bucket, or Firehose delivery
// stream — via DeliveryDestinationConfiguration.DestinationResourceArn.
func resolveLogsDeliveryDestTarget(acct *account, st *store.Store) error {
	dests, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"},
		AccountID: acct.ID,
		Types:     []string{TypeLogsDeliveryDest},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(dests) == 0 {
		return nil
	}
	lgSet, err := scannedIDSet(acct, st, TypeLogsLogGroup)
	if err != nil {
		return err
	}
	bktSet, err := scannedIDSet(acct, st, TypeS3Bucket)
	if err != nil {
		return err
	}
	fhSet, err := scannedIDSet(acct, st, TypeFirehoseDeliveryStream)
	if err != nil {
		return err
	}
	for _, d := range dests {
		var attrs struct {
			DeliveryDestinationConfiguration *struct {
				DestinationResourceArn *string `json:"DestinationResourceArn"`
			} `json:"DeliveryDestinationConfiguration"`
		}
		if err := json.Unmarshal([]byte(d.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.DeliveryDestinationConfiguration == nil {
			continue
		}
		raw := sv(attrs.DeliveryDestinationConfiguration.DestinationResourceArn)
		if raw == "" {
			continue
		}
		var tgtType, tgtARN string
		switch {
		case strings.HasPrefix(raw, "arn:aws:logs:"):
			tgtType = TypeLogsLogGroup
			tgtARN = strings.TrimSuffix(raw, ":*")
		case strings.HasPrefix(raw, "arn:aws:s3:"):
			tgtType = TypeS3Bucket
			tgtARN = raw
		case strings.HasPrefix(raw, "arn:aws:firehose:"):
			tgtType = TypeFirehoseDeliveryStream
			tgtARN = raw
		default:
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, tgtType, tgtARN)
		var present bool
		switch tgtType {
		case TypeLogsLogGroup:
			present = lgSet[tgtID]
		case TypeS3Bucket:
			present = bktSet[tgtID]
		case TypeFirehoseDeliveryStream:
			present = fhSet[tgtID]
		}
		if !present {
			continue
		}
		if err := st.UpsertRelationship(d.ID, tgtID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert delivery-dest→target: %w", err)
		}
	}
	return nil
}

// resolveLogsGroupAnomalyDetectors links each anomaly detector to the log
// groups it monitors.
// LogAnomalyDetector → LogGroup: uses
func resolveLogsGroupAnomalyDetectors(acct *account, st *store.Store) error {
	detectors, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"},
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
		Providers: []string{"aws"},
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
