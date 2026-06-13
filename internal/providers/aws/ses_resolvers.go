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
		resolveSESEmailIdentityConfigSet,
		EdgeDecl{TypeSESEmailIdentity, TypeSESConfigurationSet, store.RelUses},
	)
	registerResolver(
		resolveSESEventDestinationTargets,
		EdgeDecl{TypeSESConfigurationSetEventDestination, TypeSESConfigurationSet, store.RelAttachedTo},
		EdgeDecl{TypeSESConfigurationSetEventDestination, TypeSNSTopic, store.RelUses},
		EdgeDecl{TypeSESConfigurationSetEventDestination, TypeFirehoseDeliveryStream, store.RelUses},
		EdgeDecl{TypeSESConfigurationSetEventDestination, TypePinpointApp, store.RelUses},
	)
	registerResolver(
		resolveSESReceiptRuleTargets,
		EdgeDecl{TypeSESReceiptRule, TypeSESReceiptRuleSet, store.RelAttachedTo},
		EdgeDecl{TypeSESReceiptRule, TypeS3Bucket, store.RelUses},
		EdgeDecl{TypeSESReceiptRule, TypeSNSTopic, store.RelUses},
		EdgeDecl{TypeSESReceiptRule, TypeLambdaFunction, store.RelUses},
		EdgeDecl{TypeSESReceiptRule, TypeKMSKey, store.RelUses},
	)
}

// sesEmailIdentityAttrs mirrors the verbatim GetEmailIdentityOutput fields
// used by the resolver. PascalCase tags match mustJSON of the SDK v2 struct.
type sesEmailIdentityAttrs struct {
	ConfigurationSetName *string `json:"ConfigurationSetName"`
}

// resolveSESEmailIdentityConfigSet emits a `uses` edge from each email
// identity to its default configuration set, when the config set is also
// scanned in the same (account, region). Identities without a default
// config-set or referencing an unscanned name skip silently. FK-safe via
// scanned config-set id set; cross-region refs intentionally not supported
// (config sets are region-scoped and SES v2 enforces same-region).
func resolveSESEmailIdentityConfigSet(acct *account, st *store.Store) error {
	identities, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeSESEmailIdentity},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(identities) == 0 {
		return nil
	}

	cfgIDs, err := resourceIDSet(st, acct.ID, TypeSESConfigurationSet)
	if err != nil {
		return err
	}
	if len(cfgIDs) == 0 {
		return nil
	}

	for _, ident := range identities {
		var attrs sesEmailIdentityAttrs
		if err := json.Unmarshal([]byte(ident.AttributesJSON), &attrs); err != nil {
			continue
		}
		cfgName := sv(attrs.ConfigurationSetName)
		if cfgName == "" || ident.Region == nil {
			continue
		}
		cfgARN := sesConfigurationSetARN(*ident.Region, acct.ID, cfgName)
		cfgID := store.ResourceID("aws", acct.ID, TypeSESConfigurationSet, cfgARN)
		if _, ok := cfgIDs[cfgID]; !ok {
			continue
		}
		if err := st.UpsertRelationship(ident.ID, cfgID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert ses email-identity→config-set: %w", err)
		}
	}
	return nil
}

// sesEventDestinationConfigSetName extracts the configuration-set name from
// a SES event-destination NativeID of the shape:
//
//	arn:aws:ses:{r}:{a}:configuration-set/{set}/event-destination/{dest}
//
// Returns "" if the shape doesn't match. Used to wire the parent edge.
func sesEventDestinationConfigSetName(nativeID string) string {
	const prefix = "configuration-set/"
	_, rest, ok := strings.Cut(nativeID, prefix)
	if !ok {
		return ""
	}
	name, _, _ := strings.Cut(rest, "/")
	return name
}

// sesEventDestinationAttrs mirrors the verbatim sesv2types.EventDestination
// shape: only the destination sub-structs the resolver walks. PascalCase
// matches mustJSON output.
type sesEventDestinationAttrs struct {
	KinesisFirehoseDestination *struct {
		DeliveryStreamArn *string `json:"DeliveryStreamArn"`
	} `json:"KinesisFirehoseDestination"`
	SnsDestination *struct {
		TopicArn *string `json:"TopicArn"`
	} `json:"SnsDestination"`
	PinpointDestination *struct {
		ApplicationArn *string `json:"ApplicationArn"`
	} `json:"PinpointDestination"`
}

// resolveSESEventDestinationTargets emits edges from each configuration-set
// event-destination to (1) its parent config-set, (2) the SNS topic /
// Firehose stream / Pinpoint app referenced by the destination block.
// CloudWatch dimensions intentionally skipped — they are metric refs, not
// resource refs (per mission spec).
func resolveSESEventDestinationTargets(acct *account, st *store.Store) error {
	dests, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID,
		Types: []string{TypeSESConfigurationSetEventDestination},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(dests) == 0 {
		return nil
	}
	cfgSet, err := scannedIDSet(acct, st, TypeSESConfigurationSet)
	if err != nil {
		return err
	}
	snsSet, err := scannedIDSet(acct, st, TypeSNSTopic)
	if err != nil {
		return err
	}
	fhSet, err := scannedIDSet(acct, st, TypeFirehoseDeliveryStream)
	if err != nil {
		return err
	}
	appSet, err := scannedIDSet(acct, st, TypePinpointApp)
	if err != nil {
		return err
	}
	for _, r := range dests {
		region := sv(r.Region)
		// Parent config-set edge (synth from NativeID).
		if name := sesEventDestinationConfigSetName(r.NativeID); name != "" {
			cfgID := store.ResourceID("aws", acct.ID, TypeSESConfigurationSet,
				sesConfigurationSetARN(region, acct.ID, name))
			if cfgSet[cfgID] {
				if err := st.UpsertRelationship(r.ID, cfgID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert ses event-destination→config-set: %w", err)
				}
			}
		}
		var attrs sesEventDestinationAttrs
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.SnsDestination != nil && sv(attrs.SnsDestination.TopicArn) != "" {
			topicID := store.ResourceID("aws", acct.ID, TypeSNSTopic, *attrs.SnsDestination.TopicArn)
			if snsSet[topicID] {
				if err := st.UpsertRelationship(r.ID, topicID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert ses event-destination→sns: %w", err)
				}
			}
		}
		if attrs.KinesisFirehoseDestination != nil && sv(attrs.KinesisFirehoseDestination.DeliveryStreamArn) != "" {
			fhID := store.ResourceID("aws", acct.ID, TypeFirehoseDeliveryStream, *attrs.KinesisFirehoseDestination.DeliveryStreamArn)
			if fhSet[fhID] {
				if err := st.UpsertRelationship(r.ID, fhID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert ses event-destination→firehose: %w", err)
				}
			}
		}
		if attrs.PinpointDestination != nil && sv(attrs.PinpointDestination.ApplicationArn) != "" {
			appID := store.ResourceID("aws", acct.ID, TypePinpointApp, *attrs.PinpointDestination.ApplicationArn)
			if appSet[appID] {
				if err := st.UpsertRelationship(r.ID, appID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert ses event-destination→pinpoint-app: %w", err)
				}
			}
		}
	}
	return nil
}

// sesReceiptAction mirrors sesv1types.ReceiptAction (only the fields the
// resolver walks). PascalCase preserved to match mustJSON output.
type sesReceiptAction struct {
	LambdaAction *struct {
		FunctionArn *string `json:"FunctionArn"`
	} `json:"LambdaAction"`
	S3Action *struct {
		BucketName *string `json:"BucketName"`
		KmsKeyArn  *string `json:"KmsKeyArn"`
		TopicArn   *string `json:"TopicArn"`
	} `json:"S3Action"`
	SNSAction *struct {
		TopicArn *string `json:"TopicArn"`
	} `json:"SNSAction"`
}

// sesReceiptRuleAttrs mirrors sesv1types.ReceiptRule. The classic SES SDK
// uses the upper-case `SNSAction` field name; preserve via PascalCase tag.
type sesReceiptRuleAttrs struct {
	Actions []sesReceiptAction `json:"Actions"`
}

// sesReceiptRuleSetName extracts the rule-set name from a receipt-rule
// NativeID of shape arn:aws:ses:{r}:{a}:receipt-rule-set/{set}/receipt-rule/{rule}.
func sesReceiptRuleSetName(nativeID string) string {
	const prefix = "receipt-rule-set/"
	_, rest, ok := strings.Cut(nativeID, prefix)
	if !ok {
		return ""
	}
	name, _, _ := strings.Cut(rest, "/")
	return name
}

// resolveSESReceiptRuleTargets emits edges per rule:
//   - parent receipt-rule-set
//   - S3Action.BucketName → S3 bucket
//   - S3Action.TopicArn / SNSAction.TopicArn → SNS topic
//   - LambdaAction.FunctionArn → Lambda function
//   - S3Action.KmsKeyArn → KMS key (via shared resolve helper)
func resolveSESReceiptRuleTargets(acct *account, st *store.Store) error {
	rules, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID,
		Types: []string{TypeSESReceiptRule},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rules) == 0 {
		return nil
	}
	rsSet, err := scannedIDSet(acct, st, TypeSESReceiptRuleSet)
	if err != nil {
		return err
	}
	bucketSet, err := scannedIDSet(acct, st, TypeS3Bucket)
	if err != nil {
		return err
	}
	snsSet, err := scannedIDSet(acct, st, TypeSNSTopic)
	if err != nil {
		return err
	}
	lambdaSet, err := scannedIDSet(acct, st, TypeLambdaFunction)
	if err != nil {
		return err
	}
	kmsIdx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rules {
		region := sv(r.Region)
		// Parent rule-set.
		if name := sesReceiptRuleSetName(r.NativeID); name != "" {
			rsARN := fmt.Sprintf("arn:aws:ses:%s:%s:receipt-rule-set/%s", region, acct.ID, name)
			rsID := store.ResourceID("aws", acct.ID, TypeSESReceiptRuleSet, rsARN)
			if rsSet[rsID] {
				if err := st.UpsertRelationship(r.ID, rsID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert ses receipt-rule→rule-set: %w", err)
				}
			}
		}
		var attrs sesReceiptRuleAttrs
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, a := range attrs.Actions {
			if err := emitReceiptRuleAction(st, r.ID, region, acct.ID, a, bucketSet, snsSet, lambdaSet, kmsIdx); err != nil {
				return err
			}
		}
	}
	return nil
}

// emitReceiptRuleAction fans out edges for a single action block. Split
// from the loop to keep complexity below the gocognit ceiling.
func emitReceiptRuleAction(st *store.Store, ruleID, region, acctID string, a sesReceiptAction, bucketSet, snsSet, lambdaSet map[string]bool, kmsIdx *kmsResolveIndex) error {
	if a.S3Action != nil {
		if b := sv(a.S3Action.BucketName); b != "" {
			bID := store.ResourceID("aws", acctID, TypeS3Bucket, "arn:aws:s3:::"+b)
			if bucketSet[bID] {
				if err := st.UpsertRelationship(ruleID, bID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert ses receipt-rule→s3: %w", err)
				}
			}
		}
		if t := sv(a.S3Action.TopicArn); t != "" {
			tID := store.ResourceID("aws", acctID, TypeSNSTopic, t)
			if snsSet[tID] {
				if err := st.UpsertRelationship(ruleID, tID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert ses receipt-rule→sns(s3): %w", err)
				}
			}
		}
		if k := sv(a.S3Action.KmsKeyArn); k != "" {
			if keyID, ok := kmsIdx.resolveKMSKeyID(k, region, acctID); ok {
				if err := st.UpsertRelationship(ruleID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert ses receipt-rule→kms: %w", err)
				}
			}
		}
	}
	if a.SNSAction != nil {
		if t := sv(a.SNSAction.TopicArn); t != "" {
			tID := store.ResourceID("aws", acctID, TypeSNSTopic, t)
			if snsSet[tID] {
				if err := st.UpsertRelationship(ruleID, tID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert ses receipt-rule→sns: %w", err)
				}
			}
		}
	}
	if a.LambdaAction != nil {
		if f := sv(a.LambdaAction.FunctionArn); f != "" {
			fID := store.ResourceID("aws", acctID, TypeLambdaFunction, f)
			if lambdaSet[fID] {
				if err := st.UpsertRelationship(ruleID, fID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert ses receipt-rule→lambda: %w", err)
				}
			}
		}
	}
	return nil
}
