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
		resolveVpcLatticeALSRefs,
		EdgeDecl{TypeVpcLatticeAccessLogSubscription, TypeVpcLatticeService, store.RelAttachedTo},
		EdgeDecl{TypeVpcLatticeAccessLogSubscription, TypeVpcLatticeServiceNetwork, store.RelAttachedTo},
		EdgeDecl{TypeVpcLatticeAccessLogSubscription, TypeLogsLogGroup, store.RelUses},
		EdgeDecl{TypeVpcLatticeAccessLogSubscription, TypeS3Bucket, store.RelUses},
		EdgeDecl{TypeVpcLatticeAccessLogSubscription, TypeFirehoseDeliveryStream, store.RelUses},
	)
	registerResolver(
		resolveVpcLatticeREARefs,
		EdgeDecl{TypeVpcLatticeResourceEndpointAssociation, TypeVpcLatticeResourceConfiguration, store.RelAttachedTo},
	)
}

// resolveVpcLatticeREARefs wires each resource-endpoint-association to its
// parent resource-configuration via the ResourceConfigurationArn attr.
func resolveVpcLatticeREARefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{TypeVpcLatticeResourceEndpointAssociation}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	rcSet, err := scannedIDSet(acct, st, TypeVpcLatticeResourceConfiguration)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			ResourceConfigurationArn *string `json:"ResourceConfigurationArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		rcARN := sv(attrs.ResourceConfigurationArn)
		if rcARN == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, rcARN)
		if !rcSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert vpcl rea→rc: %w", err)
		}
	}
	return nil
}

// resolveVpcLatticeALSRefs wires each access-log-subscription to its source
// (service or service-network via ResourceArn) and to the log destination
// (log-group / S3 bucket / Firehose) via DestinationArn.
func resolveVpcLatticeALSRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeVpcLatticeAccessLogSubscription}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	svcSet, err := scannedIDSet(acct, st, TypeVpcLatticeService)
	if err != nil {
		return err
	}
	snSet, err := scannedIDSet(acct, st, TypeVpcLatticeServiceNetwork)
	if err != nil {
		return err
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
	for _, r := range rows {
		var attrs struct {
			ResourceArn    *string `json:"ResourceArn"`
			DestinationArn *string `json:"DestinationArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if src := sv(attrs.ResourceArn); src != "" {
			switch {
			case strings.Contains(src, ":service/"):
				tgtID := store.ResourceID("aws", acct.ID, src)
				if svcSet[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
						return fmt.Errorf("upsert vpcl als→svc: %w", err)
					}
				}
			case strings.Contains(src, ":servicenetwork/"):
				tgtID := store.ResourceID("aws", acct.ID, src)
				if snSet[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
						return fmt.Errorf("upsert vpcl als→sn: %w", err)
					}
				}
			}
		}
		if dest := sv(attrs.DestinationArn); dest != "" {
			var tgtARN string
			var present bool
			switch {
			case strings.HasPrefix(dest, "arn:aws:logs:"):
				tgtARN = strings.TrimSuffix(dest, ":*")
				present = lgSet[store.ResourceID("aws", acct.ID, tgtARN)]
			case strings.HasPrefix(dest, "arn:aws:s3:"):
				tgtARN = dest
				present = bktSet[store.ResourceID("aws", acct.ID, tgtARN)]
			case strings.HasPrefix(dest, "arn:aws:firehose:"):
				tgtARN = dest
				present = fhSet[store.ResourceID("aws", acct.ID, tgtARN)]
			default:
				continue
			}
			if !present {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, tgtARN)
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert vpcl als→destination: %w", err)
			}
		}
	}
	return nil
}
