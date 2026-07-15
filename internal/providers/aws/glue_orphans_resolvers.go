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
		resolveGlueDatabaseTargets,
		EdgeDecl{TypeGlueDatabase, TypeGlueCatalog, store.RelAttachedTo},
		EdgeDecl{TypeGlueDatabase, TypeGlueConnection, store.RelUses},
	)
	registerResolver(
		resolveGlueIntegrationRefs,
		EdgeDecl{TypeGlueIntegration, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeGlueIntegration, TypeRDSDBCluster, store.RelRoutesTo},
		EdgeDecl{TypeGlueIntegration, TypeRDSDBInstance, store.RelRoutesTo},
		EdgeDecl{TypeGlueIntegration, TypeRedshiftCluster, store.RelRoutesTo},
		EdgeDecl{TypeGlueIntegration, TypeKinesisStream, store.RelRoutesTo},
		EdgeDecl{TypeGlueIntegration, TypeS3Bucket, store.RelRoutesTo},
		EdgeDecl{TypeGlueIntegration, TypeDynamoDBTable, store.RelRoutesTo},
	)
	registerResolver(
		resolveGlueCatalogTargets,
		EdgeDecl{TypeGlueCatalog, TypeRedshiftCluster, store.RelRoutesTo},
	)
}

// resolveGlueDatabaseTargets emits two source-side edges per Glue database:
//   - TargetDatabase.CatalogId → glue:catalog (cross-account / federated DBs
//     share via Lake Formation; same-account is the common case)
//   - FederatedDatabase.ConnectionName → glue:connection (Hive/Iceberg
//     federation through a Glue connection)
func resolveGlueDatabaseTargets(acct *account, st *store.Store) error {
	dbs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeGlueDatabase},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(dbs) == 0 {
		return nil
	}
	catalogSet, err := scannedIDSet(acct, st, TypeGlueCatalog)
	if err != nil {
		return err
	}
	connSet, err := scannedIDSet(acct, st, TypeGlueConnection)
	if err != nil {
		return err
	}
	for _, d := range dbs {
		var attrs struct {
			TargetDatabase *struct {
				CatalogID *string `json:"CatalogId"`
			} `json:"TargetDatabase"`
			FederatedDatabase *struct {
				ConnectionName *string `json:"ConnectionName"`
			} `json:"FederatedDatabase"`
		}
		if err := json.Unmarshal([]byte(d.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(d.Region)
		if attrs.TargetDatabase != nil && sv(attrs.TargetDatabase.CatalogID) != "" {
			catARN := glueResourceARN(region, acct.ID, "catalog", *attrs.TargetDatabase.CatalogID)
			catID := store.ResourceID("aws", acct.ID, catARN)
			if catalogSet[catID] {
				if err := st.UpsertRelationship(d.ID, catID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert glue database→catalog: %w", err)
				}
			}
		}
		if attrs.FederatedDatabase != nil && sv(attrs.FederatedDatabase.ConnectionName) != "" {
			connARN := glueResourceARN(region, acct.ID, "connection", *attrs.FederatedDatabase.ConnectionName)
			connID := store.ResourceID("aws", acct.ID, connARN)
			if connSet[connID] {
				if err := st.UpsertRelationship(d.ID, connID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert glue database→connection: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveGlueIntegrationRefs wires Zero-ETL integrations to their KMS key
// plus source/target ARNs (RDS, redshift, kinesis, dynamodb, S3) — substring
// dispatch on the canonical ARN, FK-safe per scanned id-set.
func resolveGlueIntegrationRefs(acct *account, st *store.Store) error {
	ints, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeGlueIntegration},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(ints) == 0 {
		return nil
	}
	kidx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	dispatch := []struct {
		match string
		ttyp  string
	}{
		{":rds:", TypeRDSDBCluster},
		{":redshift:", TypeRedshiftCluster},
		{":kinesis:", TypeKinesisStream},
		{":dynamodb:", TypeDynamoDBTable},
		{":s3:", TypeS3Bucket},
	}
	idSets := map[string]map[string]bool{}
	for _, d := range dispatch {
		set, err := scannedIDSet(acct, st, d.ttyp)
		if err != nil {
			return err
		}
		idSets[d.ttyp] = set
	}
	rdsInstSet, err := scannedIDSet(acct, st, TypeRDSDBInstance)
	if err != nil {
		return err
	}
	for _, in := range ints {
		var attrs struct {
			KmsKeyID  *string `json:"KmsKeyId"`
			SourceArn *string `json:"SourceArn"`
			TargetArn *string `json:"TargetArn"`
		}
		if err := json.Unmarshal([]byte(in.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(in.Region)
		if k := sv(attrs.KmsKeyID); k != "" {
			if keyID, ok := kidx.resolveKMSKeyID(k, region, acct.ID); ok {
				if err := st.UpsertRelationship(in.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert glue integration→kms: %w", err)
				}
			}
		}
		for _, arn := range []string{sv(attrs.SourceArn), sv(attrs.TargetArn)} {
			if arn == "" {
				continue
			}
			for _, d := range dispatch {
				if !strings.Contains(arn, d.match) {
					continue
				}
				ttyp := d.ttyp
				set := idSets[ttyp]
				// RDS source/target may name a cluster OR an instance;
				// fall through to instance set on miss.
				tgtID := store.ResourceID("aws", acct.ID, arn)
				if !set[tgtID] && d.match == ":rds:" {
					if rdsInstSet[store.ResourceID("aws", acct.ID, arn)] {
						ttyp = TypeRDSDBInstance
						tgtID = store.ResourceID("aws", acct.ID, arn)
						set = rdsInstSet
					}
				}
				if !set[tgtID] {
					break
				}
				if err := st.UpsertRelationship(in.ID, tgtID, store.RelRoutesTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert glue integration→%s: %w", ttyp, err)
				}
				break
			}
		}
	}
	return nil
}

// resolveGlueCatalogTargets links a Glue catalog to its TargetRedshiftCatalog
// (the redshift cluster a federated catalog points at).
func resolveGlueCatalogTargets(acct *account, st *store.Store) error {
	cats, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeGlueCatalog},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(cats) == 0 {
		return nil
	}
	rsSet, err := scannedIDSet(acct, st, TypeRedshiftCluster)
	if err != nil {
		return err
	}
	for _, c := range cats {
		var attrs struct {
			TargetRedshiftCatalog *struct {
				CatalogArn *string `json:"CatalogArn"`
			} `json:"TargetRedshiftCatalog"`
		}
		if err := json.Unmarshal([]byte(c.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.TargetRedshiftCatalog == nil {
			continue
		}
		arn := sv(attrs.TargetRedshiftCatalog.CatalogArn)
		if arn == "" || !strings.Contains(arn, ":redshift:") {
			continue
		}
		rsID := store.ResourceID("aws", acct.ID, arn)
		if !rsSet[rsID] {
			continue
		}
		if err := st.UpsertRelationship(c.ID, rsID, store.RelRoutesTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert glue catalog→redshift cluster: %w", err)
		}
	}
	return nil
}
