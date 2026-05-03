package aws

import (
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveDeadlineFarmChildren,
		EdgeDecl{TypeDeadlineFleet, TypeDeadlineFarm, store.RelAttachedTo},
		EdgeDecl{TypeDeadlineQueue, TypeDeadlineFarm, store.RelAttachedTo},
		EdgeDecl{TypeDeadlineLimit, TypeDeadlineFarm, store.RelAttachedTo},
		EdgeDecl{TypeDeadlineStorageProfile, TypeDeadlineFarm, store.RelAttachedTo},
		EdgeDecl{TypeDeadlineQueueFleetAssociation, TypeDeadlineFarm, store.RelAttachedTo},
		EdgeDecl{TypeDeadlineQueueLimitAssociation, TypeDeadlineFarm, store.RelAttachedTo},
	)
	registerResolver(resolveDeadlineQueueEnvParent,
		EdgeDecl{TypeDeadlineQueueEnvironment, TypeDeadlineQueue, store.RelAttachedTo},
	)
	registerResolver(resolveDeadlineMeteredProductParent,
		EdgeDecl{TypeDeadlineMeteredProduct, TypeDeadlineLicenseEndpoint, store.RelAttachedTo},
	)
	registerResolver(resolveDeadlineQueueFleetAssoc,
		EdgeDecl{TypeDeadlineQueueFleetAssociation, TypeDeadlineQueue, store.RelAttachedTo},
		EdgeDecl{TypeDeadlineQueueFleetAssociation, TypeDeadlineFleet, store.RelAttachedTo},
	)
	registerResolver(resolveDeadlineQueueLimitAssoc,
		EdgeDecl{TypeDeadlineQueueLimitAssociation, TypeDeadlineQueue, store.RelAttachedTo},
		EdgeDecl{TypeDeadlineQueueLimitAssociation, TypeDeadlineLimit, store.RelAttachedTo},
	)
}

// deadlineFarmARNFromChild extracts the parent farm ARN from a deadline
// child NativeID containing `/farm/{id}/...`. Returns "" when the input has
// no farm/{id} segment.
func deadlineFarmARNFromChild(arn string) string {
	const prefix = "farm/"
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

// resolveDeadlineFarmChildren attaches direct farm children (fleet, queue,
// limit, storage-profile, queue-fleet-assoc, queue-limit-assoc) to the
// parent farm via NativeID parse.
func resolveDeadlineFarmChildren(acct *account, st *store.Store) error {
	farmSet, err := scannedIDSet(acct, st, TypeDeadlineFarm)
	if err != nil {
		return err
	}
	childTypes := []string{
		TypeDeadlineFleet,
		TypeDeadlineQueue,
		TypeDeadlineLimit,
		TypeDeadlineStorageProfile,
		TypeDeadlineQueueFleetAssociation,
		TypeDeadlineQueueLimitAssociation,
	}
	for _, ctype := range childTypes {
		rows, err := st.ListResources(store.ResourceFilter{
			Provider: "aws", AccountID: acct.ID, Types: []string{ctype},
			Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			parent := deadlineFarmARNFromChild(r.NativeID)
			if parent == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, TypeDeadlineFarm, parent)
			if !farmSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert deadline %s→farm: %w", ctype, err)
			}
		}
	}
	return nil
}

// resolveDeadlineQueueEnvParent links each queue-environment to its parent
// queue. NativeID shape: `{queueARN}/queue-environment/{id}`.
func resolveDeadlineQueueEnvParent(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeDeadlineQueueEnvironment},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	queueSet, err := scannedIDSet(acct, st, TypeDeadlineQueue)
	if err != nil {
		return err
	}
	for _, r := range rows {
		i := strings.LastIndex(r.NativeID, "/queue-environment/")
		if i < 0 {
			continue
		}
		parent := r.NativeID[:i]
		tgtID := store.ResourceID("aws", acct.ID, TypeDeadlineQueue, parent)
		if !queueSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert deadline-queue-env→queue: %w", err)
		}
	}
	return nil
}

// resolveDeadlineMeteredProductParent links each metered-product to its
// parent license-endpoint. NativeID shape:
// `arn:aws:deadline:r:a:license-endpoint/{leID}/metered-product/{pid}`.
func resolveDeadlineMeteredProductParent(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeDeadlineMeteredProduct},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	leSet, err := scannedIDSet(acct, st, TypeDeadlineLicenseEndpoint)
	if err != nil {
		return err
	}
	for _, r := range rows {
		i := strings.LastIndex(r.NativeID, "/metered-product/")
		if i < 0 {
			continue
		}
		parent := r.NativeID[:i]
		tgtID := store.ResourceID("aws", acct.ID, TypeDeadlineLicenseEndpoint, parent)
		if !leSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert deadline-mp→license-endpoint: %w", err)
		}
	}
	return nil
}

// deadlineAssocComponents extracts the parent farm ARN, child queue ARN, and
// associated peer ARN (fleet or limit) from a queue-{fleet,limit}-association
// NativeID of shape `{farmARN}/queue/{qid}/{kind}/{pid}/association`.
func deadlineAssocComponents(arn, peerKind string) (queueARN, peerARN string) {
	farm := deadlineFarmARNFromChild(arn)
	if farm == "" {
		return "", ""
	}
	suffix := strings.TrimPrefix(arn, farm+"/queue/")
	if suffix == arn {
		return "", ""
	}
	parts := strings.SplitN(suffix, "/", 4)
	if len(parts) != 4 || parts[1] != peerKind || parts[3] != "association" {
		return "", ""
	}
	return farm + "/queue/" + parts[0], farm + "/" + peerKind + "/" + parts[2]
}

// resolveDeadlineQueueFleetAssoc links each queue-fleet-association to its
// queue and fleet via NativeID parse.
func resolveDeadlineQueueFleetAssoc(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeDeadlineQueueFleetAssociation},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	queueSet, err := scannedIDSet(acct, st, TypeDeadlineQueue)
	if err != nil {
		return err
	}
	fleetSet, err := scannedIDSet(acct, st, TypeDeadlineFleet)
	if err != nil {
		return err
	}
	for _, r := range rows {
		queueARN, fleetARN := deadlineAssocComponents(r.NativeID, "fleet")
		if queueARN == "" {
			continue
		}
		qID := store.ResourceID("aws", acct.ID, TypeDeadlineQueue, queueARN)
		if queueSet[qID] {
			if err := st.UpsertRelationship(r.ID, qID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert deadline-qfa→queue: %w", err)
			}
		}
		fID := store.ResourceID("aws", acct.ID, TypeDeadlineFleet, fleetARN)
		if fleetSet[fID] {
			if err := st.UpsertRelationship(r.ID, fID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert deadline-qfa→fleet: %w", err)
			}
		}
	}
	return nil
}

// resolveDeadlineQueueLimitAssoc links each queue-limit-association to its
// queue and limit via NativeID parse.
func resolveDeadlineQueueLimitAssoc(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeDeadlineQueueLimitAssociation},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	queueSet, err := scannedIDSet(acct, st, TypeDeadlineQueue)
	if err != nil {
		return err
	}
	limitSet, err := scannedIDSet(acct, st, TypeDeadlineLimit)
	if err != nil {
		return err
	}
	for _, r := range rows {
		queueARN, limitARN := deadlineAssocComponents(r.NativeID, "limit")
		if queueARN == "" {
			continue
		}
		qID := store.ResourceID("aws", acct.ID, TypeDeadlineQueue, queueARN)
		if queueSet[qID] {
			if err := st.UpsertRelationship(r.ID, qID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert deadline-qla→queue: %w", err)
			}
		}
		lID := store.ResourceID("aws", acct.ID, TypeDeadlineLimit, limitARN)
		if limitSet[lID] {
			if err := st.UpsertRelationship(r.ID, lID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert deadline-qla→limit: %w", err)
			}
		}
	}
	return nil
}
