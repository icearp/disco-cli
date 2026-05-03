package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveConnectQueueRefs,
		EdgeDecl{TypeConnectQueue, TypeConnectHoursOfOperation, store.RelAttachedTo},
		EdgeDecl{TypeConnectQueue, TypeConnectPhoneNumber, store.RelUses},
		EdgeDecl{TypeConnectQueue, TypeConnectContactFlow, store.RelUses},
	)
	registerResolver(resolveConnectRoutingProfileRefs,
		EdgeDecl{TypeConnectRoutingProfile, TypeConnectQueue, store.RelAttachedTo},
	)
}

// connectInstanceIDFromARN extracts the instance ID from a Connect resource
// ARN of shape `arn:aws:connect:{r}:{a}:instance/{instId}/<kind>/<id>`.
// Returns "" when the ARN does not match the instance-nested form (e.g.
// account-level resources like PhoneNumber).
func connectInstanceIDFromARN(arn string) string {
	const prefix = "instance/"
	i := strings.Index(arn, prefix)
	if i < 0 {
		return ""
	}
	rest := arn[i+len(prefix):]
	end := strings.IndexByte(rest, '/')
	if end < 0 {
		return rest
	}
	return rest[:end]
}

// connectInstanceChildARN rebuilds an instance-scoped child ARN given the
// parent resource's ARN, the child kind segment (e.g. `hours-of-operation`,
// `contact-flow`), and the child ID. Returns "" when the parent ARN
// has no `instance/{id}/...` segment.
func connectInstanceChildARN(parentARN, kind, childID string) string {
	instID := connectInstanceIDFromARN(parentARN)
	if instID == "" {
		return ""
	}
	// Region + account live in the parent ARN's segments — split once.
	parts := strings.SplitN(parentARN, ":", 6)
	if len(parts) < 6 {
		return ""
	}
	return fmt.Sprintf("arn:%s:connect:%s:%s:instance/%s/%s/%s",
		parts[1], parts[3], parts[4], instID, kind, childID)
}

// connectAccountResourceARN builds an account-scoped (non-instance-nested)
// Connect resource ARN. PhoneNumber is the canonical case:
// `arn:aws:connect:{r}:{a}:phone-number/{id}`.
func connectAccountResourceARN(region, acctID, kind, id string) string {
	return fmt.Sprintf("arn:aws:connect:%s:%s:%s/%s", region, acctID, kind, id)
}

// resolveConnectQueueRefs walks each queue's HoursOfOperationId,
// OutboundCallerConfig.OutboundCallerIdNumberId, and
// OutboundCallerConfig.OutboundFlowId fields and emits the corresponding
// edges. The scanner stores `DescribeQueueOutput` whose top-level shape
// is `{Queue: {...}}`, so we unmarshal under that key.
func resolveConnectQueueRefs(acct *account, st *store.Store) error {
	queues, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeConnectQueue},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	hopSet, err := scannedIDSet(acct, st, TypeConnectHoursOfOperation)
	if err != nil {
		return err
	}
	phoneSet, err := scannedIDSet(acct, st, TypeConnectPhoneNumber)
	if err != nil {
		return err
	}
	flowSet, err := scannedIDSet(acct, st, TypeConnectContactFlow)
	if err != nil {
		return err
	}
	for _, r := range queues {
		var attrs struct {
			Queue struct {
				HoursOfOperationId   *string `json:"HoursOfOperationId"`
				OutboundCallerConfig *struct {
					OutboundCallerIdNumberId *string `json:"OutboundCallerIdNumberId"`
					OutboundFlowId           *string `json:"OutboundFlowId"`
				} `json:"OutboundCallerConfig"`
			} `json:"Queue"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		// HoursOfOperation lives under the same instance.
		if id := sv(attrs.Queue.HoursOfOperationId); id != "" {
			tgtARN := connectInstanceChildARN(r.NativeID, "hours-of-operation", id)
			if tgtARN != "" {
				tgtID := store.ResourceID("aws", acct.ID, TypeConnectHoursOfOperation, tgtARN)
				if hopSet[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
						return fmt.Errorf("upsert connect-queue→hours: %w", err)
					}
				}
			}
		}
		if attrs.Queue.OutboundCallerConfig == nil {
			continue
		}
		// PhoneNumber is account-scoped, not instance-nested.
		if id := sv(attrs.Queue.OutboundCallerConfig.OutboundCallerIdNumberId); id != "" {
			tgtARN := connectAccountResourceARN(region, acct.ID, "phone-number", id)
			tgtID := store.ResourceID("aws", acct.ID, TypeConnectPhoneNumber, tgtARN)
			if phoneSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert connect-queue→phone: %w", err)
				}
			}
		}
		// Outbound flow lives under the same instance.
		if id := sv(attrs.Queue.OutboundCallerConfig.OutboundFlowId); id != "" {
			tgtARN := connectInstanceChildARN(r.NativeID, "contact-flow", id)
			if tgtARN != "" {
				tgtID := store.ResourceID("aws", acct.ID, TypeConnectContactFlow, tgtARN)
				if flowSet[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert connect-queue→flow: %w", err)
					}
				}
			}
		}
	}
	return nil
}

// resolveConnectRoutingProfileRefs walks each routing profile's
// AssociatedQueueIds + DefaultOutboundQueueId and emits attached-to edges
// to the local queue rows. The scanner wraps the SDK
// DescribeRoutingProfileOutput, so attrs root is `{RoutingProfile: ...}`.
func resolveConnectRoutingProfileRefs(acct *account, st *store.Store) error {
	profiles, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeConnectRoutingProfile},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	queueSet, err := scannedIDSet(acct, st, TypeConnectQueue)
	if err != nil {
		return err
	}
	for _, r := range profiles {
		var attrs struct {
			RoutingProfile struct {
				AssociatedQueueIds                 []string `json:"AssociatedQueueIds"`
				AssociatedManualAssignmentQueueIds []string `json:"AssociatedManualAssignmentQueueIds"`
				DefaultOutboundQueueId             *string  `json:"DefaultOutboundQueueId"`
			} `json:"RoutingProfile"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		seen := map[string]bool{}
		ids := append([]string{}, attrs.RoutingProfile.AssociatedQueueIds...)
		ids = append(ids, attrs.RoutingProfile.AssociatedManualAssignmentQueueIds...)
		if id := sv(attrs.RoutingProfile.DefaultOutboundQueueId); id != "" {
			ids = append(ids, id)
		}
		for _, id := range ids {
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			tgtARN := connectInstanceChildARN(r.NativeID, "queue", id)
			if tgtARN == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, TypeConnectQueue, tgtARN)
			if !queueSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert connect-routing-profile→queue: %w", err)
			}
		}
	}
	return nil
}
