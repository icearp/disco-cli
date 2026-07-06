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
		resolveAppFabricBundleKMS,
		EdgeDecl{TypeAppFabricAppBundle, TypeKMSKey, store.RelUses},
	)
	registerResolver(
		resolveAppFabricChildToBundle,
		EdgeDecl{TypeAppFabricAppAuthorization, TypeAppFabricAppBundle, store.RelAttachedTo},
		EdgeDecl{TypeAppFabricIngestion, TypeAppFabricAppBundle, store.RelAttachedTo},
	)
	registerResolver(
		resolveAppFabricDestination,
		EdgeDecl{TypeAppFabricIngestionDestination, TypeAppFabricIngestion, store.RelAttachedTo},
		EdgeDecl{TypeAppFabricIngestionDestination, TypeS3Bucket, store.RelUses},
		EdgeDecl{TypeAppFabricIngestionDestination, TypeFirehoseDeliveryStream, store.RelUses},
	)
}

// resolveAppFabricBundleKMS wires each app-bundle to its customer-managed KMS
// key (GetAppBundle.CustomerManagedKeyArn), FK-safe.
func resolveAppFabricBundleKMS(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeAppFabricAppBundle},
		Limit: util.AllResources,
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
	for _, r := range rows {
		var attrs struct {
			CustomerManagedKeyArn *string `json:"CustomerManagedKeyArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		ref := sv(attrs.CustomerManagedKeyArn)
		if ref == "" {
			continue
		}
		keyID, ok := idx.resolveKMSKeyID(ref, sv(r.Region), acct.ID)
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert appfabric bundle→kms: %w", err)
		}
	}
	return nil
}

// resolveAppFabricChildToBundle wires app-authorizations and ingestions to their
// owning app-bundle. Authorizations carry AppBundleArn directly; ingestion ARNs
// embed the bundle (arn:…:appbundle/{id}/ingestion/{ingId}).
func resolveAppFabricChildToBundle(acct *account, st *store.Store) error {
	bundleSet, err := scannedIDSet(acct, st, TypeAppFabricAppBundle)
	if err != nil {
		return err
	}
	if len(bundleSet) == 0 {
		return nil
	}

	authz, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeAppFabricAppAuthorization},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range authz {
		var attrs struct {
			AppBundleArn *string `json:"AppBundleArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if err := appFabricBundleEdge(st, acct.ID, r.ID, bundleSet, sv(attrs.AppBundleArn)); err != nil {
			return err
		}
	}

	ingestions, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeAppFabricIngestion},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range ingestions {
		if err := appFabricBundleEdge(st, acct.ID, r.ID, bundleSet, appFabricParentARN(r.NativeID, "/ingestion/")); err != nil {
			return err
		}
	}
	return nil
}

func appFabricBundleEdge(st *store.Store, acctID, srcID string, bundleSet map[string]bool, bundleARN string) error {
	if bundleARN == "" {
		return nil
	}
	tgtID := store.ResourceID("aws", acctID, TypeAppFabricAppBundle, bundleARN)
	if !bundleSet[tgtID] {
		return nil
	}
	if err := st.UpsertRelationship(srcID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
		return fmt.Errorf("upsert appfabric child→bundle: %w", err)
	}
	return nil
}

// resolveAppFabricDestination wires each ingestion-destination to its parent
// ingestion (ARN parent-extraction) and to the S3 bucket / Firehose stream the
// audit log flows into.
func resolveAppFabricDestination(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeAppFabricIngestionDestination},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	ingestionSet, err := scannedIDSet(acct, st, TypeAppFabricIngestion)
	if err != nil {
		return err
	}
	bucketSet, err := scannedIDSet(acct, st, TypeS3Bucket)
	if err != nil {
		return err
	}
	streamSet, err := scannedIDSet(acct, st, TypeFirehoseDeliveryStream)
	if err != nil {
		return err
	}
	for _, r := range rows {
		region := sv(r.Region)
		ingARN := appFabricParentARN(r.NativeID, "/ingestiondestination/")
		if ingARN != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeAppFabricIngestion, ingARN)
			if ingestionSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert appfabric dest→ingestion: %w", err)
				}
			}
		}
		// Read only the flattened sibling keys — the embedded SDK body carries
		// non-empty union interfaces that json.Unmarshal cannot rehydrate.
		var attrs struct {
			S3BucketName       *string `json:"s3BucketName"`
			FirehoseStreamName *string `json:"firehoseStreamName"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if name := sv(attrs.S3BucketName); name != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeS3Bucket, "arn:aws:s3:::"+name)
			if bucketSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert appfabric dest→s3: %w", err)
				}
			}
		}
		if name := sv(attrs.FirehoseStreamName); name != "" {
			streamARN := fmt.Sprintf("arn:aws:firehose:%s:%s:deliverystream/%s", region, acct.ID, name)
			tgtID := store.ResourceID("aws", acct.ID, TypeFirehoseDeliveryStream, streamARN)
			if streamSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert appfabric dest→firehose: %w", err)
				}
			}
		}
	}
	return nil
}

// appFabricParentARN truncates a child ARN at the given path segment to
// recover the parent ARN. E.g. sep "/ingestiondestination/": no-op on
// "arn:…:appbundle/x/ingestion/y" (segment absent), but turns
// ".../ingestion/y/ingestiondestination/z" into ".../ingestion/y".
func appFabricParentARN(childARN, sep string) string {
	if i := strings.Index(childARN, sep); i >= 0 {
		return childARN[:i]
	}
	return ""
}
