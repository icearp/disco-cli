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
		resolveCustomerProfilesChildrenToDomain,
		EdgeDecl{TypeCPCalculatedAttributeDefinition, TypeCPDomain, store.RelAttachedTo},
		EdgeDecl{TypeCPEventStream, TypeCPDomain, store.RelAttachedTo},
		EdgeDecl{TypeCPEventTrigger, TypeCPDomain, store.RelAttachedTo},
		EdgeDecl{TypeCPIntegration, TypeCPDomain, store.RelAttachedTo},
		EdgeDecl{TypeCPObjectType, TypeCPDomain, store.RelAttachedTo},
		EdgeDecl{TypeCPRecommender, TypeCPDomain, store.RelAttachedTo},
		EdgeDecl{TypeCPSegmentDefinition, TypeCPDomain, store.RelAttachedTo},
	)
	registerResolver(
		resolveCPDomainRefs,
		EdgeDecl{TypeCPDomain, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeCPDomain, TypeSQSQueue, store.RelRoutesTo},
	)
}

// resolveCPDomainRefs wires each domain to its CMEK (DefaultEncryptionKey)
// and SQS dead-letter queue (DeadLetterQueueUrl). GetDomain body shape.
func resolveCPDomainRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeCPDomain}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	idx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	sqsSet, err := scannedIDSet(acct, st, TypeSQSQueue)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			DefaultEncryptionKey *string `json:"DefaultEncryptionKey"`
			DeadLetterQueueURL   *string `json:"DeadLetterQueueUrl"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if ref := sv(attrs.DefaultEncryptionKey); ref != "" {
			if keyID, ok := idx.resolveKMSKeyID(ref, sv(r.Region), acct.ID); ok {
				if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert cp-domain→kms: %w", err)
				}
			}
		}
		if dlq := sv(attrs.DeadLetterQueueURL); dlq != "" {
			if qarn := sqsQueueARNFromURL(dlq); qarn != "" {
				tgt := store.ResourceID("aws", acct.ID, TypeSQSQueue, qarn)
				if sqsSet[tgt] {
					if err := st.UpsertRelationship(r.ID, tgt, store.RelRoutesTo, "directed", nil); err != nil {
						return fmt.Errorf("upsert cp-domain→sqs-dlq: %w", err)
					}
				}
			}
		}
	}
	return nil
}

// cpDomainARNFromChild extracts `arn:aws:profile:r:a:domains/{name}` from any
// child NativeID of shape `…:domains/{name}/<kind>/<id>`.
func cpDomainARNFromChild(arn string) string {
	const prefix = "domains/"
	i := strings.Index(arn, prefix)
	if i < 0 {
		return ""
	}
	tail := arn[i+len(prefix):]
	end := strings.IndexByte(tail, '/')
	if end < 0 {
		return ""
	}
	return arn[:i] + prefix + tail[:end]
}

func resolveCustomerProfilesChildrenToDomain(acct *account, st *store.Store) error {
	domSet, err := scannedIDSet(acct, st, TypeCPDomain)
	if err != nil {
		return err
	}
	childTypes := []string{
		TypeCPCalculatedAttributeDefinition,
		TypeCPEventStream,
		TypeCPEventTrigger,
		TypeCPIntegration,
		TypeCPObjectType,
		TypeCPRecommender,
		TypeCPSegmentDefinition,
	}
	for _, ctype := range childTypes {
		rows, err := st.ListResources(store.ResourceFilter{
			Provider: "aws", AccountID: acct.ID, Types: []string{ctype}, Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			parent := cpDomainARNFromChild(r.NativeID)
			if parent == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, TypeCPDomain, parent)
			if !domSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert customer-profiles %s→domain: %w", ctype, err)
			}
		}
	}
	return nil
}
