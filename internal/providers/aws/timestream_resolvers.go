package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveTSDatabaseKMS,
		EdgeDecl{TypeTimestreamDatabase, TypeKMSKey, store.RelUses},
	)
	registerResolver(
		resolveTSTableMagneticS3,
		EdgeDecl{TypeTimestreamTable, TypeS3Bucket, store.RelUses},
		EdgeDecl{TypeTimestreamTable, TypeKMSKey, store.RelUses},
	)
	registerResolver(
		resolveTSScheduledQueryRefs,
		EdgeDecl{TypeTimestreamScheduledQuery, TypeIAMRole, store.RelAssumes},
		EdgeDecl{TypeTimestreamScheduledQuery, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeTimestreamScheduledQuery, TypeSNSTopic, store.RelRoutesTo},
		EdgeDecl{TypeTimestreamScheduledQuery, TypeS3Bucket, store.RelUses},
	)
	registerResolver(
		resolveTSInfluxRefs,
		EdgeDecl{TypeTimestreamInfluxDBCluster, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeTimestreamInfluxDBCluster, TypeEC2SecurityGroup, store.RelUses},
		EdgeDecl{TypeTimestreamInfluxDBCluster, TypeSecretsManagerSecret, store.RelUses},
		EdgeDecl{TypeTimestreamInfluxDBCluster, TypeS3Bucket, store.RelUses},
		EdgeDecl{TypeTimestreamInfluxDBInstance, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeTimestreamInfluxDBInstance, TypeEC2SecurityGroup, store.RelUses},
		EdgeDecl{TypeTimestreamInfluxDBInstance, TypeSecretsManagerSecret, store.RelUses},
		EdgeDecl{TypeTimestreamInfluxDBInstance, TypeS3Bucket, store.RelUses},
	)
	registerResolver(
		resolveTSInfluxParameterGroup,
		EdgeDecl{TypeTimestreamInfluxDBCluster, TypeTimestreamInfluxDBParameterGroup, store.RelUses},
		EdgeDecl{TypeTimestreamInfluxDBInstance, TypeTimestreamInfluxDBParameterGroup, store.RelUses},
	)
}

// resolveTSInfluxParameterGroup wires each Timestream-for-InfluxDB cluster +
// instance to its referenced DB parameter group (DbParameterGroupIdentifier).
// Parameter groups are indexed by Id, not ARN — that's what the reference uses.
func resolveTSInfluxParameterGroup(acct *account, st *store.Store) error {
	pgRows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeTimestreamInfluxDBParameterGroup}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(pgRows) == 0 {
		return nil
	}
	pgByID := make(map[string]string, len(pgRows))
	for _, pg := range pgRows {
		var a struct {
			ID *string `json:"Id"`
		}
		if err := json.Unmarshal([]byte(pg.AttributesJSON), &a); err != nil {
			continue
		}
		if id := sv(a.ID); id != "" {
			pgByID[id] = pg.ID
		}
	}
	for _, ttyp := range []string{TypeTimestreamInfluxDBCluster, TypeTimestreamInfluxDBInstance} {
		rows, err := st.ListResources(store.ResourceFilter{
			Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{ttyp}, Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			var attrs struct {
				DbParameterGroupIdentifier *string `json:"DbParameterGroupIdentifier"`
			}
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
				continue
			}
			tgt, ok := pgByID[sv(attrs.DbParameterGroupIdentifier)]
			if !ok {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert ts-influx→parameter-group: %w", err)
			}
		}
	}
	return nil
}

// resolveTSInfluxRefs wires each Timestream-for-InfluxDB cluster + instance to
// its VPC subnets, security groups, auth-parameters secret, and log-delivery S3
// bucket. Cluster and instance share the same fields, so it loops both types.
func resolveTSInfluxRefs(acct *account, st *store.Store) error {
	subnetSet, err := scannedIDSet(acct, st, TypeEC2Subnet)
	if err != nil {
		return err
	}
	sgSet, err := scannedIDSet(acct, st, TypeEC2SecurityGroup)
	if err != nil {
		return err
	}
	secretSet, err := scannedIDSet(acct, st, TypeSecretsManagerSecret)
	if err != nil {
		return err
	}
	bucketSet, err := scannedIDSet(acct, st, TypeS3Bucket)
	if err != nil {
		return err
	}
	for _, ttyp := range []string{TypeTimestreamInfluxDBCluster, TypeTimestreamInfluxDBInstance} {
		rows, err := st.ListResources(store.ResourceFilter{
			Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{ttyp}, Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			var attrs struct {
				VpcSubnetIDs                  []string `json:"VpcSubnetIds"`
				VpcSecurityGroupIDs           []string `json:"VpcSecurityGroupIds"`
				InfluxAuthParametersSecretArn *string  `json:"InfluxAuthParametersSecretArn"`
				LogDeliveryConfiguration      *struct {
					S3Configuration *struct {
						BucketName *string `json:"BucketName"`
					} `json:"S3Configuration"`
				} `json:"LogDeliveryConfiguration"`
			}
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
				continue
			}
			region := sv(r.Region)
			for _, sub := range attrs.VpcSubnetIDs {
				sARN := ec2ARN(region, acct.ID, "subnet", sub)
				tgt := store.ResourceID("aws", acct.ID, TypeEC2Subnet, sARN)
				if !subnetSet[tgt] {
					continue
				}
				if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert ts-influx→subnet: %w", err)
				}
			}
			for _, sg := range attrs.VpcSecurityGroupIDs {
				gARN := ec2ARN(region, acct.ID, "security-group", sg)
				tgt := store.ResourceID("aws", acct.ID, TypeEC2SecurityGroup, gARN)
				if !sgSet[tgt] {
					continue
				}
				if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert ts-influx→sg: %w", err)
				}
			}
			if sa := sv(attrs.InfluxAuthParametersSecretArn); sa != "" {
				tgt := store.ResourceID("aws", acct.ID, TypeSecretsManagerSecret, sa)
				if secretSet[tgt] {
					if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert ts-influx→secret: %w", err)
					}
				}
			}
			if attrs.LogDeliveryConfiguration != nil && attrs.LogDeliveryConfiguration.S3Configuration != nil {
				if b := sv(attrs.LogDeliveryConfiguration.S3Configuration.BucketName); b != "" {
					tgt := store.ResourceID("aws", acct.ID, TypeS3Bucket, "arn:aws:s3:::"+b)
					if bucketSet[tgt] {
						if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
							return fmt.Errorf("upsert ts-influx→s3: %w", err)
						}
					}
				}
			}
		}
	}
	return nil
}

// resolveTSDatabaseKMS wires each Timestream LiveAnalytics database to its
// CMK (KmsKeyID — present on the ListDatabases summary so no Describe needed).
func resolveTSDatabaseKMS(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeTimestreamDatabase}, Limit: util.AllResources,
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
			KmsKeyID *string `json:"KmsKeyId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		k := sv(attrs.KmsKeyID)
		if k == "" {
			continue
		}
		if keyID, ok := idx.resolveKMSKeyID(k, sv(r.Region), acct.ID); ok {
			if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert timestream database→kms: %w", err)
			}
		}
	}
	return nil
}

// resolveTSTableMagneticS3 wires each Timestream LiveAnalytics table to the
// S3 bucket + KMS key holding its rejected-records error report
// (MagneticStoreWriteProperties.MagneticStoreRejectedDataLocation.S3Configuration).
func resolveTSTableMagneticS3(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeTimestreamTable}, Limit: util.AllResources,
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
	idx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			MagneticStoreWriteProperties *struct {
				MagneticStoreRejectedDataLocation *struct {
					S3Configuration *struct {
						BucketName *string `json:"BucketName"`
						KmsKeyID   *string `json:"KmsKeyId"`
					} `json:"S3Configuration"`
				} `json:"MagneticStoreRejectedDataLocation"`
			} `json:"MagneticStoreWriteProperties"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.MagneticStoreWriteProperties == nil ||
			attrs.MagneticStoreWriteProperties.MagneticStoreRejectedDataLocation == nil ||
			attrs.MagneticStoreWriteProperties.MagneticStoreRejectedDataLocation.S3Configuration == nil {
			continue
		}
		s3c := attrs.MagneticStoreWriteProperties.MagneticStoreRejectedDataLocation.S3Configuration
		if b := sv(s3c.BucketName); b != "" {
			tgt := store.ResourceID("aws", acct.ID, TypeS3Bucket, "arn:aws:s3:::"+b)
			if bucketSet[tgt] {
				if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert timestream table→s3: %w", err)
				}
			}
		}
		if k := sv(s3c.KmsKeyID); k != "" {
			if keyID, ok := idx.resolveKMSKeyID(k, sv(r.Region), acct.ID); ok {
				if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert timestream table→kms: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveTSScheduledQueryRefs wires each scheduled query to its execution
// role, CMK, SNS notification topic, and error-report S3 bucket. All four
// fields land on the DescribeScheduledQuery body (scanner enriches per row).
func resolveTSScheduledQueryRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeTimestreamScheduledQuery}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	topicSet, err := scannedIDSet(acct, st, TypeSNSTopic)
	if err != nil {
		return err
	}
	bucketSet, err := scannedIDSet(acct, st, TypeS3Bucket)
	if err != nil {
		return err
	}
	idx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			ScheduledQueryExecutionRoleArn *string `json:"ScheduledQueryExecutionRoleArn"`
			KmsKeyID                       *string `json:"KmsKeyId"`
			NotificationConfiguration      *struct {
				SnsConfiguration *struct {
					TopicArn *string `json:"TopicArn"`
				} `json:"SnsConfiguration"`
			} `json:"NotificationConfiguration"`
			ErrorReportConfiguration *struct {
				S3Configuration *struct {
					BucketName *string `json:"BucketName"`
				} `json:"S3Configuration"`
			} `json:"ErrorReportConfiguration"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if ra := sv(attrs.ScheduledQueryExecutionRoleArn); ra != "" {
			tgt := store.ResourceID("aws", acct.ID, TypeIAMRole, ra)
			if roleSet[tgt] {
				if err := st.UpsertRelationship(r.ID, tgt, store.RelAssumes, "directed", nil); err != nil {
					return fmt.Errorf("upsert timestream sq→role: %w", err)
				}
			}
		}
		if k := sv(attrs.KmsKeyID); k != "" {
			if keyID, ok := idx.resolveKMSKeyID(k, sv(r.Region), acct.ID); ok {
				if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert timestream sq→kms: %w", err)
				}
			}
		}
		if attrs.NotificationConfiguration != nil && attrs.NotificationConfiguration.SnsConfiguration != nil {
			if ta := sv(attrs.NotificationConfiguration.SnsConfiguration.TopicArn); ta != "" {
				tgt := store.ResourceID("aws", acct.ID, TypeSNSTopic, ta)
				if topicSet[tgt] {
					if err := st.UpsertRelationship(r.ID, tgt, store.RelRoutesTo, "directed", nil); err != nil {
						return fmt.Errorf("upsert timestream sq→sns: %w", err)
					}
				}
			}
		}
		if attrs.ErrorReportConfiguration != nil && attrs.ErrorReportConfiguration.S3Configuration != nil {
			if b := sv(attrs.ErrorReportConfiguration.S3Configuration.BucketName); b != "" {
				tgt := store.ResourceID("aws", acct.ID, TypeS3Bucket, "arn:aws:s3:::"+b)
				if bucketSet[tgt] {
					if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert timestream sq→s3: %w", err)
					}
				}
			}
		}
	}
	return nil
}
