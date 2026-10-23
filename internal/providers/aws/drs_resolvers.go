package aws

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveDRSRecoveryInstanceRefs,
		EdgeDecl{TypeDRSRecoveryInstanceResource, TypeEC2Instance, store.RelUses},
		EdgeDecl{TypeDRSRecoveryInstanceResource, TypeDRSSourceServerResource, store.RelAttachedTo},
	)
	registerResolver(
		resolveDRSSourceServerRefs,
		EdgeDecl{TypeDRSSourceServerResource, TypeDRSSourceNetworkResource, store.RelAttachedTo},
	)
	registerResolver(
		resolveDRSSourceNetworkRefs,
		EdgeDecl{TypeDRSSourceNetworkResource, TypeEC2VPC, store.RelAttachedTo},
	)
	registerResolver(
		resolveDRSReplicationSettingsRefs,
		EdgeDecl{TypeDRSReplicationConfigurationTemplateResource, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeDRSReplicationConfigurationTemplateResource, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeDRSReplicationConfigurationTemplateResource, TypeEC2SecurityGroup, store.RelAttachedTo},
		EdgeDecl{TypeDRSReplicationConfiguration, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeDRSReplicationConfiguration, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeDRSReplicationConfiguration, TypeEC2SecurityGroup, store.RelAttachedTo},
	)
	registerResolver(
		resolveDRSLaunchTemplateRefs,
		EdgeDecl{TypeDRSLaunchConfigurationTemplateResource, TypeS3Bucket, store.RelUses},
	)
	registerResolver(
		resolveDRSRecoveryPlanStepRefs,
		EdgeDecl{TypeDRSRecoveryPlanStep, TypeDRSSourceServerResource, store.RelUses},
	)
	registerResolver(
		noopDRSHierarchy,
		EdgeDecl{TypeDRSRecoveryPlanResource, TypeDRSRecoveryPlanStep, store.RelContains},
		EdgeDecl{TypeDRSLaunchConfigurationTemplateResource, TypeDRSLaunchAction, store.RelContains},
		EdgeDecl{TypeDRSSourceServerResource, TypeDRSLaunchAction, store.RelContains},
		EdgeDecl{TypeDRSSourceServerResource, TypeDRSReplicationConfiguration, store.RelContains},
	)
}

// noopDRSHierarchy emits nothing: the recovery-plan → step,
// launch-configuration-template / source-server → launch-action and
// source-server → replication-configuration contains edges are written at scan
// time by the child phases of scanDRS. It is registered only so those
// edges are declared to coverage tooling.
func noopDRSHierarchy(_ *account, _ *store.Store) error {
	return nil
}

// resolveDRSRecoveryPlanStepRefs wires each server step to the source servers
// it recovers. A row whose attributes do not parse is reported in the returned
// error after the remaining rows are wired.
func resolveDRSRecoveryPlanStepRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeDRSRecoveryPlanStep}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	serverSet, err := scannedIDSet(acct, st, TypeDRSSourceServerResource)
	if err != nil {
		return err
	}
	var bad []error
	for _, r := range rows {
		var attrs struct {
			Configuration struct {
				ServerStepConfiguration *struct {
					Servers []struct {
						ServerArn *string `json:"ServerArn"`
					} `json:"Servers"`
				} `json:"ServerStepConfiguration"`
			} `json:"Configuration"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			bad = append(bad, fmt.Errorf("drs recovery plan step %s attributes: %w", r.NativeID, err))
			continue
		}
		if attrs.Configuration.ServerStepConfiguration == nil {
			continue
		}
		for _, srv := range attrs.Configuration.ServerStepConfiguration.Servers {
			tgtID := store.ResourceID("aws", acct.ID, sv(srv.ServerArn))
			if !serverSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert drs recovery-plan-step→source-server: %w", err)
			}
		}
	}
	return errors.Join(bad...)
}

// drsIDKey is a DRS id in the region of the row that holds it: the ids are
// not documented as unique across regions.
type drsIDKey struct{ region, id string }

// drsIDIndex maps the DRS id of each row of rtype to its resource ID, so
// resolvers can resolve a bare id reference without reconstructing ARNs. The
// scanner names every source server and source network row by its id.
func drsIDIndex(acct *account, st *store.Store, rtype string) (map[drsIDKey]string, error) {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{rtype}, Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	idx := make(map[drsIDKey]string, len(rows))
	for _, r := range rows {
		if id := sv(r.Name); id != "" {
			idx[drsIDKey{sv(r.Region), id}] = r.ID
		}
	}
	return idx, nil
}

// resolveDRSRecoveryInstanceRefs wires each recovery instance to the EC2
// instance it launched and to the source server it recovers.
// A row whose attributes do not parse is reported in the returned error
// after the remaining rows are wired.
func resolveDRSRecoveryInstanceRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeDRSRecoveryInstanceResource}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	ec2Set, err := scannedIDSet(acct, st, TypeEC2Instance)
	if err != nil {
		return err
	}
	srcServerIdx, err := drsIDIndex(acct, st, TypeDRSSourceServerResource)
	if err != nil {
		return err
	}
	var bad []error
	for _, r := range rows {
		var attrs struct {
			Ec2InstanceID  *string `json:"Ec2InstanceID"`
			SourceServerID *string `json:"SourceServerID"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			bad = append(bad, fmt.Errorf("%s %s attributes: %w", r.Type, r.NativeID, err))
			continue
		}
		if id := sv(attrs.Ec2InstanceID); id != "" {
			tgtID := store.ResourceID("aws", acct.ID, ec2ARN(sv(r.Region), acct.ID, "instance", id))
			if ec2Set[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert drs recovery-instance→ec2: %w", err)
				}
			}
		}
		if id := sv(attrs.SourceServerID); id != "" {
			if tgtID, ok := srcServerIdx[drsIDKey{sv(r.Region), id}]; ok {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert drs recovery-instance→source-server: %w", err)
				}
			}
		}
	}
	return errors.Join(bad...)
}

// resolveDRSSourceServerRefs wires each source server to the source network it
// belongs to.
// A row whose attributes do not parse is reported in the returned error
// after the remaining rows are wired.
func resolveDRSSourceServerRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeDRSSourceServerResource}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	srcNetIdx, err := drsIDIndex(acct, st, TypeDRSSourceNetworkResource)
	if err != nil {
		return err
	}
	var bad []error
	for _, r := range rows {
		var attrs struct {
			SourceNetworkID *string `json:"SourceNetworkID"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			bad = append(bad, fmt.Errorf("%s %s attributes: %w", r.Type, r.NativeID, err))
			continue
		}
		if id := sv(attrs.SourceNetworkID); id != "" {
			if tgtID, ok := srcNetIdx[drsIDKey{sv(r.Region), id}]; ok {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert drs source-server→source-network: %w", err)
				}
			}
		}
	}
	return errors.Join(bad...)
}

// resolveDRSSourceNetworkRefs wires each source network to the VPC it was
// recovered into (LaunchedVpcID — the source VPC lives in another account/region
// and is not scanned here).
// A row whose attributes do not parse is reported in the returned error
// after the remaining rows are wired.
func resolveDRSSourceNetworkRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeDRSSourceNetworkResource}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	vpcSet, err := scannedIDSet(acct, st, TypeEC2VPC)
	if err != nil {
		return err
	}
	var bad []error
	for _, r := range rows {
		var attrs struct {
			LaunchedVpcID *string `json:"LaunchedVpcID"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			bad = append(bad, fmt.Errorf("%s %s attributes: %w", r.Type, r.NativeID, err))
			continue
		}
		if id := sv(attrs.LaunchedVpcID); id != "" {
			tgtID := store.ResourceID("aws", acct.ID, ec2ARN(sv(r.Region), acct.ID, "vpc", id))
			if vpcSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert drs source-network→vpc: %w", err)
				}
			}
		}
	}
	return errors.Join(bad...)
}

// resolveDRSReplicationSettingsRefs wires each replication-configuration
// template and each source server's replication configuration to its EBS
// encryption key, staging subnet, and replication-server security groups; the
// SDK gives both shapes the same three fields. A row whose attributes do not
// parse is reported in the returned error after the remaining rows are wired.
func resolveDRSReplicationSettingsRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Limit: util.AllResources,
		Types: []string{TypeDRSReplicationConfigurationTemplateResource, TypeDRSReplicationConfiguration},
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	kmsIdx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	subSet, err := scannedIDSet(acct, st, TypeEC2Subnet)
	if err != nil {
		return err
	}
	sgSet, err := scannedIDSet(acct, st, TypeEC2SecurityGroup)
	if err != nil {
		return err
	}
	var bad []error
	for _, r := range rows {
		var attrs struct {
			EbsEncryptionKeyArn                 *string  `json:"EbsEncryptionKeyArn"`
			StagingAreaSubnetID                 *string  `json:"StagingAreaSubnetId"`
			ReplicationServersSecurityGroupsIDs []string `json:"ReplicationServersSecurityGroupsIDs"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			bad = append(bad, fmt.Errorf("%s %s attributes: %w", r.Type, r.NativeID, err))
			continue
		}
		region := sv(r.Region)
		if ref := sv(attrs.EbsEncryptionKeyArn); ref != "" {
			if keyID, ok := kmsIdx.resolveKMSKeyID(ref, region, acct.ID); ok {
				if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert %s→kms: %w", r.Type, err)
				}
			}
		}
		if sn := sv(attrs.StagingAreaSubnetID); sn != "" {
			tgtID := store.ResourceID("aws", acct.ID, ec2ARN(region, acct.ID, "subnet", sn))
			if subSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert %s→subnet: %w", r.Type, err)
				}
			}
		}
		for _, sg := range attrs.ReplicationServersSecurityGroupsIDs {
			if sg == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, ec2ARN(region, acct.ID, "security-group", sg))
			if sgSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert %s→sg: %w", r.Type, err)
				}
			}
		}
	}
	return errors.Join(bad...)
}

// resolveDRSLaunchTemplateRefs wires each launch-configuration template to the
// S3 bucket it exports recovery artifacts to.
// A row whose attributes do not parse is reported in the returned error
// after the remaining rows are wired.
func resolveDRSLaunchTemplateRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeDRSLaunchConfigurationTemplateResource}, Limit: util.AllResources,
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
	var bad []error
	for _, r := range rows {
		var attrs struct {
			ExportBucketArn *string `json:"ExportBucketArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			bad = append(bad, fmt.Errorf("%s %s attributes: %w", r.Type, r.NativeID, err))
			continue
		}
		if b := sv(attrs.ExportBucketArn); b != "" {
			tgtID := store.ResourceID("aws", acct.ID, b)
			if bucketSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert drs launch-template→s3: %w", err)
				}
			}
		}
	}
	return errors.Join(bad...)
}
