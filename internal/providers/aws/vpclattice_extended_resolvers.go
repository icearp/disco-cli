package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
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
}

// resolveVpcLatticeALSRefs wires each access-log-subscription to its source
// (service or service-network via ResourceArn) and to the log destination
// (log-group / S3 bucket / Firehose) via DestinationArn.
func resolveVpcLatticeALSRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeVpcLatticeAccessLogSubscription}, Limit: util.AllResources,
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
				tgtID := store.ResourceID("aws", acct.ID, TypeVpcLatticeService, src)
				if svcSet[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
						return fmt.Errorf("upsert vpcl als→svc: %w", err)
					}
				}
			case strings.Contains(src, ":servicenetwork/"):
				tgtID := store.ResourceID("aws", acct.ID, TypeVpcLatticeServiceNetwork, src)
				if snSet[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
						return fmt.Errorf("upsert vpcl als→sn: %w", err)
					}
				}
			}
		}
		if dest := sv(attrs.DestinationArn); dest != "" {
			var tgtType, tgtARN string
			var present bool
			switch {
			case strings.HasPrefix(dest, "arn:aws:logs:"):
				tgtType = TypeLogsLogGroup
				tgtARN = strings.TrimSuffix(dest, ":*")
				present = lgSet[store.ResourceID("aws", acct.ID, tgtType, tgtARN)]
			case strings.HasPrefix(dest, "arn:aws:s3:"):
				tgtType = TypeS3Bucket
				tgtARN = dest
				present = bktSet[store.ResourceID("aws", acct.ID, tgtType, tgtARN)]
			case strings.HasPrefix(dest, "arn:aws:firehose:"):
				tgtType = TypeFirehoseDeliveryStream
				tgtARN = dest
				present = fhSet[store.ResourceID("aws", acct.ID, tgtType, tgtARN)]
			default:
				continue
			}
			if !present {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, tgtType, tgtARN)
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert vpcl als→destination: %w", err)
			}
		}
	}
	return nil
}
