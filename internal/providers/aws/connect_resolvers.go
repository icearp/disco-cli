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
		resolveConnectQueueRefs,
		EdgeDecl{TypeConnectQueue, TypeConnectHoursOfOperation, store.RelAttachedTo},
		EdgeDecl{TypeConnectQueue, TypeConnectPhoneNumber, store.RelUses},
		EdgeDecl{TypeConnectQueue, TypeConnectContactFlow, store.RelUses},
	)
	registerResolver(
		resolveConnectRoutingProfileRefs,
		EdgeDecl{TypeConnectRoutingProfile, TypeConnectQueue, store.RelAttachedTo},
	)
	registerResolver(
		resolveConnectIntegrationAssociationTarget,
		EdgeDecl{TypeConnectIntegrationAssociation, TypeLambdaFunction, store.RelUses},
		EdgeDecl{TypeConnectIntegrationAssociation, TypeLexBot, store.RelUses},
		EdgeDecl{TypeConnectIntegrationAssociation, TypeWisdomAssistant, store.RelUses},
	)
	registerResolver(
		resolveConnectInstanceStorageConfigRefs,
		EdgeDecl{TypeConnectInstanceStorageConfig, TypeS3Bucket, store.RelUses},
		EdgeDecl{TypeConnectInstanceStorageConfig, TypeKinesisStream, store.RelUses},
		EdgeDecl{TypeConnectInstanceStorageConfig, TypeFirehoseDeliveryStream, store.RelUses},
		EdgeDecl{TypeConnectInstanceStorageConfig, TypeKMSKey, store.RelUses},
	)
	registerResolver(
		resolveConnectUserRefs,
		EdgeDecl{TypeConnectUser, TypeConnectSecurityProfile, store.RelAttachedTo},
		EdgeDecl{TypeConnectUser, TypeConnectRoutingProfile, store.RelAttachedTo},
		EdgeDecl{TypeConnectUser, TypeConnectUserHierarchyGroup, store.RelAttachedTo},
	)
	registerResolver(
		resolveConnectQuickConnectRefs,
		EdgeDecl{TypeConnectQuickConnect, TypeConnectQueue, store.RelUses},
		EdgeDecl{TypeConnectQuickConnect, TypeConnectContactFlow, store.RelUses},
	)
	registerResolver(
		resolveConnectTrafficDistributionGroupInstance,
		EdgeDecl{TypeConnectTrafficDistributionGroup, TypeConnectInstance, store.RelAttachedTo},
	)
	registerResolver(
		resolveConnectContactFlowVersionParent,
		EdgeDecl{TypeConnectContactFlowVersion, TypeConnectContactFlow, store.RelAttachedTo},
	)
	registerResolver(
		resolveConnectContactFlowModuleVersionAndAliasParent,
		EdgeDecl{TypeConnectContactFlowModuleVersion, TypeConnectContactFlowModule, store.RelAttachedTo},
		EdgeDecl{TypeConnectContactFlowModuleAlias, TypeConnectContactFlowModule, store.RelAttachedTo},
	)
	registerResolver(
		resolveConnectPhoneNumberTarget,
		EdgeDecl{TypeConnectPhoneNumber, TypeConnectInstance, store.RelAttachedTo},
		EdgeDecl{TypeConnectPhoneNumber, TypeConnectTrafficDistributionGroup, store.RelAttachedTo},
	)
}

// connectInstanceIDFromARN extracts the instance ID from a Connect resource
// ARN of shape `arn:aws:connect:{r}:{a}:instance/{instId}/<kind>/<id>`.
// Returns "" for non-instance-nested ARNs (e.g. account-level PhoneNumber).
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

// resolveConnectQueueRefs walks each queue's HoursOfOperationID,
// OutboundCallerConfig.OutboundCallerIDNumberID, and
// OutboundCallerConfig.OutboundFlowID fields and emits the corresponding
// edges. Scanner stores `DescribeQueueOutput`; top-level shape is
// `{Queue: {...}}`, so unmarshal under that key.
func resolveConnectQueueRefs(acct *account, st *store.Store) error {
	queues, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeConnectQueue},
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
				HoursOfOperationID   *string `json:"HoursOfOperationId"`
				OutboundCallerConfig *struct {
					OutboundCallerIDNumberID *string `json:"OutboundCallerIdNumberId"`
					OutboundFlowID           *string `json:"OutboundFlowId"`
				} `json:"OutboundCallerConfig"`
			} `json:"Queue"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		// HoursOfOperation lives under the same instance.
		if id := sv(attrs.Queue.HoursOfOperationID); id != "" {
			tgtARN := connectInstanceChildARN(r.NativeID, "hours-of-operation", id)
			if tgtARN != "" {
				tgtID := store.ResourceID("aws", acct.ID, tgtARN)
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
		if id := sv(attrs.Queue.OutboundCallerConfig.OutboundCallerIDNumberID); id != "" {
			tgtARN := connectAccountResourceARN(region, acct.ID, "phone-number", id)
			tgtID := store.ResourceID("aws", acct.ID, tgtARN)
			if phoneSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert connect-queue→phone: %w", err)
				}
			}
		}
		// Outbound flow lives under the same instance.
		if id := sv(attrs.Queue.OutboundCallerConfig.OutboundFlowID); id != "" {
			tgtARN := connectInstanceChildARN(r.NativeID, "contact-flow", id)
			if tgtARN != "" {
				tgtID := store.ResourceID("aws", acct.ID, tgtARN)
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
// AssociatedQueueIDs + DefaultOutboundQueueID and emits attached-to edges
// to the local queue rows. Scanner wraps DescribeRoutingProfileOutput, so
// attrs root is `{RoutingProfile: ...}`.
func resolveConnectRoutingProfileRefs(acct *account, st *store.Store) error {
	profiles, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeConnectRoutingProfile},
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
				AssociatedQueueIDs                 []string `json:"AssociatedQueueIds"`
				AssociatedManualAssignmentQueueIDs []string `json:"AssociatedManualAssignmentQueueIds"`
				DefaultOutboundQueueID             *string  `json:"DefaultOutboundQueueId"`
			} `json:"RoutingProfile"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		seen := map[string]bool{}
		ids := append([]string{}, attrs.RoutingProfile.AssociatedQueueIDs...)
		ids = append(ids, attrs.RoutingProfile.AssociatedManualAssignmentQueueIDs...)
		if id := sv(attrs.RoutingProfile.DefaultOutboundQueueID); id != "" {
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
			tgtID := store.ResourceID("aws", acct.ID, tgtARN)
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

// resolveConnectIntegrationAssociationTarget reads `IntegrationArn` from each
// IntegrationAssociationSummary and dispatches by ARN service segment to a
// lambda function, lex bot, or wisdom assistant. FK-safe.
func resolveConnectIntegrationAssociationTarget(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeConnectIntegrationAssociation},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	lambdaSet, err := scannedIDSet(acct, st, TypeLambdaFunction)
	if err != nil {
		return err
	}
	lexSet, err := scannedIDSet(acct, st, TypeLexBot)
	if err != nil {
		return err
	}
	wisdomSet, err := scannedIDSet(acct, st, TypeWisdomAssistant)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			IntegrationArn *string `json:"IntegrationArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		ref := sv(attrs.IntegrationArn)
		if ref == "" {
			continue
		}
		var tgtType string
		var set map[string]bool
		switch {
		case strings.HasPrefix(ref, "arn:aws:lambda:"):
			tgtType, set = TypeLambdaFunction, lambdaSet
		case strings.HasPrefix(ref, "arn:aws:lex:"):
			tgtType, set = TypeLexBot, lexSet
		case strings.HasPrefix(ref, "arn:aws:wisdom:"):
			tgtType, set = TypeWisdomAssistant, wisdomSet
		default:
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, ref)
		if !set[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert connect-integration-association→%s: %w", tgtType, err)
		}
	}
	return nil
}

// resolveConnectInstanceStorageConfigRefs walks each storage config's
// per-shape sub-struct (S3Config / KinesisStreamConfig / KinesisFirehoseConfig
// / KinesisVideoStreamConfig) and emits edges to the underlying storage and
// optional KMS encryption key. FK-safe.
func resolveConnectInstanceStorageConfigRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeConnectInstanceStorageConfig},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	bucketSet, err := scannedIDSet(acct, st, TypeS3Bucket)
	if err != nil {
		return err
	}
	streamSet, err := scannedIDSet(acct, st, TypeKinesisStream)
	if err != nil {
		return err
	}
	firehoseSet, err := scannedIDSet(acct, st, TypeFirehoseDeliveryStream)
	if err != nil {
		return err
	}
	kmsIdx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			S3Config *struct {
				BucketName       *string `json:"BucketName"`
				EncryptionConfig *struct {
					KeyID *string `json:"KeyId"`
				} `json:"EncryptionConfig"`
			} `json:"S3Config"`
			KinesisStreamConfig *struct {
				StreamArn *string `json:"StreamArn"`
			} `json:"KinesisStreamConfig"`
			KinesisFirehoseConfig *struct {
				FirehoseDeliveryStreamArn *string `json:"FirehoseDeliveryStreamArn"`
			} `json:"KinesisFirehoseConfig"`
			KinesisVideoStreamConfig *struct {
				EncryptionConfig *struct {
					KeyID *string `json:"KeyId"`
				} `json:"EncryptionConfig"`
			} `json:"KinesisVideoStreamConfig"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if attrs.S3Config != nil {
			if name := sv(attrs.S3Config.BucketName); name != "" {
				tgtARN := s3BucketARNFromName(name)
				tgtID := store.ResourceID("aws", acct.ID, tgtARN)
				if bucketSet[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert connect-storage→s3: %w", err)
					}
				}
			}
			if attrs.S3Config.EncryptionConfig != nil {
				if id, ok := kmsIdx.resolveKMSKeyID(sv(attrs.S3Config.EncryptionConfig.KeyID), region, acct.ID); ok {
					if err := st.UpsertRelationship(r.ID, id, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert connect-storage→kms (s3): %w", err)
					}
				}
			}
		}
		if attrs.KinesisStreamConfig != nil {
			if arn := sv(attrs.KinesisStreamConfig.StreamArn); arn != "" {
				tgtID := store.ResourceID("aws", acct.ID, arn)
				if streamSet[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert connect-storage→kinesis: %w", err)
					}
				}
			}
		}
		if attrs.KinesisFirehoseConfig != nil {
			if arn := sv(attrs.KinesisFirehoseConfig.FirehoseDeliveryStreamArn); arn != "" {
				tgtID := store.ResourceID("aws", acct.ID, arn)
				if firehoseSet[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert connect-storage→firehose: %w", err)
					}
				}
			}
		}
		if attrs.KinesisVideoStreamConfig != nil && attrs.KinesisVideoStreamConfig.EncryptionConfig != nil {
			if id, ok := kmsIdx.resolveKMSKeyID(sv(attrs.KinesisVideoStreamConfig.EncryptionConfig.KeyID), region, acct.ID); ok {
				if err := st.UpsertRelationship(r.ID, id, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert connect-storage→kms (kvs): %w", err)
				}
			}
		}
	}
	return nil
}

// resolveConnectUserRefs walks each user's SecurityProfileIDs[],
// RoutingProfileID, and HierarchyGroupID and emits attached-to edges. The
// scanner stores DescribeUserOutput so attrs root is `{"User": {...}}`.
func resolveConnectUserRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeConnectUser},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	spSet, err := scannedIDSet(acct, st, TypeConnectSecurityProfile)
	if err != nil {
		return err
	}
	rpSet, err := scannedIDSet(acct, st, TypeConnectRoutingProfile)
	if err != nil {
		return err
	}
	hgSet, err := scannedIDSet(acct, st, TypeConnectUserHierarchyGroup)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			User struct {
				SecurityProfileIDs []string `json:"SecurityProfileIds"`
				RoutingProfileID   *string  `json:"RoutingProfileId"`
				HierarchyGroupID   *string  `json:"HierarchyGroupId"`
			} `json:"User"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, spID := range attrs.User.SecurityProfileIDs {
			tgtARN := connectInstanceChildARN(r.NativeID, "security-profile", spID)
			if tgtARN == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, tgtARN)
			if !spSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert connect-user→security-profile: %w", err)
			}
		}
		if id := sv(attrs.User.RoutingProfileID); id != "" {
			tgtARN := connectInstanceChildARN(r.NativeID, "routing-profile", id)
			if tgtARN != "" {
				tgtID := store.ResourceID("aws", acct.ID, tgtARN)
				if rpSet[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
						return fmt.Errorf("upsert connect-user→routing-profile: %w", err)
					}
				}
			}
		}
		if id := sv(attrs.User.HierarchyGroupID); id != "" {
			tgtARN := connectInstanceChildARN(r.NativeID, "agent-group", id)
			if tgtARN != "" {
				tgtID := store.ResourceID("aws", acct.ID, tgtARN)
				if hgSet[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
						return fmt.Errorf("upsert connect-user→hierarchy-group: %w", err)
					}
				}
			}
		}
	}
	return nil
}

// resolveConnectQuickConnectRefs walks each quick-connect's QueueConfig.QueueID
// and {UserConfig|QueueConfig}.ContactFlowID and emits uses edges. Scanner
// stores DescribeQuickConnectOutput so attrs root is `{"QuickConnect": {...}}`.
func resolveConnectQuickConnectRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeConnectQuickConnect},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	queueSet, err := scannedIDSet(acct, st, TypeConnectQueue)
	if err != nil {
		return err
	}
	flowSet, err := scannedIDSet(acct, st, TypeConnectContactFlow)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			QuickConnect struct {
				QuickConnectConfig struct {
					UserConfig *struct {
						ContactFlowID *string `json:"ContactFlowId"`
					} `json:"UserConfig"`
					QueueConfig *struct {
						ContactFlowID *string `json:"ContactFlowId"`
						QueueID       *string `json:"QueueId"`
					} `json:"QueueConfig"`
				} `json:"QuickConnectConfig"`
			} `json:"QuickConnect"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		cfg := attrs.QuickConnect.QuickConnectConfig
		emit := func(kind, id, tgtType string, set map[string]bool) error {
			if id == "" {
				return nil
			}
			tgtARN := connectInstanceChildARN(r.NativeID, kind, id)
			if tgtARN == "" {
				return nil
			}
			tgtID := store.ResourceID("aws", acct.ID, tgtARN)
			if !set[tgtID] {
				return nil
			}
			return st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil)
		}
		if cfg.QueueConfig != nil {
			if err := emit("queue", sv(cfg.QueueConfig.QueueID), TypeConnectQueue, queueSet); err != nil {
				return fmt.Errorf("upsert connect-quick-connect→queue: %w", err)
			}
			if err := emit("contact-flow", sv(cfg.QueueConfig.ContactFlowID), TypeConnectContactFlow, flowSet); err != nil {
				return fmt.Errorf("upsert connect-quick-connect→flow: %w", err)
			}
		}
		if cfg.UserConfig != nil {
			if err := emit("contact-flow", sv(cfg.UserConfig.ContactFlowID), TypeConnectContactFlow, flowSet); err != nil {
				return fmt.Errorf("upsert connect-quick-connect→flow (user): %w", err)
			}
		}
	}
	return nil
}

// resolveConnectTrafficDistributionGroupInstance reads InstanceArn from each
// TDG and emits attached-to → connect:instance. Scanner attrs root:
// `{"TrafficDistributionGroup": {...}}`.
func resolveConnectTrafficDistributionGroupInstance(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeConnectTrafficDistributionGroup},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	instSet, err := scannedIDSet(acct, st, TypeConnectInstance)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			TrafficDistributionGroup struct {
				InstanceArn *string `json:"InstanceArn"`
			} `json:"TrafficDistributionGroup"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		arn := sv(attrs.TrafficDistributionGroup.InstanceArn)
		if arn == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, arn)
		if !instSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert connect-tdg→instance: %w", err)
		}
	}
	return nil
}

// connectVersionParentARN strips a trailing `:version` segment from a
// version-shaped Connect ARN, recovering the parent (flow / module) ARN.
// The version segment is the substring after the ARN's last `:`, which
// falls after the last `/` — earlier prefix colons (region/account) are
// unaffected.
func connectVersionParentARN(versionARN string) string {
	slash := strings.LastIndexByte(versionARN, '/')
	if slash < 0 {
		return ""
	}
	tail := versionARN[slash:]
	i := strings.LastIndexByte(tail, ':')
	if i <= 0 {
		return ""
	}
	return versionARN[:slash] + tail[:i]
}

// resolveConnectContactFlowVersionParent links each contact-flow-version row
// to its parent contact-flow by stripping the `:version` suffix from the
// version's ARN. FK-safe.
func resolveConnectContactFlowVersionParent(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeConnectContactFlowVersion},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	flowSet, err := scannedIDSet(acct, st, TypeConnectContactFlow)
	if err != nil {
		return err
	}
	for _, r := range rows {
		parent := connectVersionParentARN(r.NativeID)
		if parent == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, parent)
		if !flowSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert connect-flow-version→flow: %w", err)
		}
	}
	return nil
}

// resolveConnectContactFlowModuleVersionAndAliasParent links module versions
// (via stripped `:version` suffix) and module aliases (via attrs
// `ContactFlowModuleID` from the wrapped DescribeContactFlowModuleAliasOutput)
// to their parent contact-flow-module. FK-safe.
func resolveConnectContactFlowModuleVersionAndAliasParent(acct *account, st *store.Store) error {
	moduleSet, err := scannedIDSet(acct, st, TypeConnectContactFlowModule)
	if err != nil {
		return err
	}
	versions, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeConnectContactFlowModuleVersion},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range versions {
		parent := connectVersionParentARN(r.NativeID)
		if parent == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, parent)
		if !moduleSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert connect-module-version→module: %w", err)
		}
	}
	aliases, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeConnectContactFlowModuleAlias},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range aliases {
		var attrs struct {
			ContactFlowModuleAlias struct {
				ContactFlowModuleID *string `json:"ContactFlowModuleId"`
			} `json:"ContactFlowModuleAlias"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		id := sv(attrs.ContactFlowModuleAlias.ContactFlowModuleID)
		if id == "" {
			continue
		}
		tgtARN := connectInstanceChildARN(r.NativeID, "flow-module", id)
		if tgtARN == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, tgtARN)
		if !moduleSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert connect-module-alias→module: %w", err)
		}
	}
	return nil
}

// resolveConnectPhoneNumberTarget reads `TargetArn` from each phone number's
// ClaimedPhoneNumberSummary and emits an attached-to edge to either the
// parent connect:instance or connect:traffic-distribution-group, dispatched
// by ARN segment. FK-safe.
func resolveConnectPhoneNumberTarget(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeConnectPhoneNumber},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	instSet, err := scannedIDSet(acct, st, TypeConnectInstance)
	if err != nil {
		return err
	}
	tdgSet, err := scannedIDSet(acct, st, TypeConnectTrafficDistributionGroup)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			ClaimedPhoneNumberSummary struct {
				TargetArn *string `json:"TargetArn"`
			} `json:"ClaimedPhoneNumberSummary"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		arn := sv(attrs.ClaimedPhoneNumberSummary.TargetArn)
		if arn == "" {
			continue
		}
		var tgtType string
		var set map[string]bool
		switch {
		case strings.Contains(arn, ":traffic-distribution-group/"):
			tgtType, set = TypeConnectTrafficDistributionGroup, tdgSet
		case strings.Contains(arn, ":instance/"):
			tgtType, set = TypeConnectInstance, instSet
		default:
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, arn)
		if !set[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert connect-phone-number→%s: %w", tgtType, err)
		}
	}
	return nil
}
