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
		resolveDeviceFarmProjectChildren,
		EdgeDecl{TypeDeviceFarmDevicePool, TypeDeviceFarmProject, store.RelAttachedTo},
		EdgeDecl{TypeDeviceFarmNetworkProfile, TypeDeviceFarmProject, store.RelAttachedTo},
	)
	registerResolver(
		resolveDeviceFarmDeviceInstanceProfile,
		EdgeDecl{TypeDeviceFarmDeviceInstance, TypeDeviceFarmInstanceProfile, store.RelUses},
	)
	registerResolver(
		resolveDeviceFarmTestGridProjectVPC,
		EdgeDecl{TypeDeviceFarmTestGridProject, TypeEC2VPC, store.RelAttachedTo},
		EdgeDecl{TypeDeviceFarmTestGridProject, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeDeviceFarmTestGridProject, TypeEC2SecurityGroup, store.RelAttachedTo},
	)
	registerResolver(
		resolveDeviceFarmProjectRefs,
		EdgeDecl{TypeDeviceFarmProject, TypeIAMRole, store.RelUses},
		EdgeDecl{TypeDeviceFarmProject, TypeEC2VPC, store.RelAttachedTo},
		EdgeDecl{TypeDeviceFarmProject, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeDeviceFarmProject, TypeEC2SecurityGroup, store.RelAttachedTo},
	)
}

// dfVPCConfig mirrors the VpcConfig SDK shape shared by Project and
// TestGridProject (PascalCase SDK marshal keys).
type dfVPCConfig struct {
	VpcID            *string  `json:"VpcId"`
	SubnetIDs        []string `json:"SubnetIds"`
	SecurityGroupIDs []string `json:"SecurityGroupIds"`
}

// dfVPCSets holds the FK-safe id sets for the three EC2 network targets.
type dfVPCSets struct {
	vpc, sub, sg map[string]bool
}

func loadDFVPCSets(acct *account, st *store.Store) (dfVPCSets, error) {
	var s dfVPCSets
	var err error
	if s.vpc, err = scannedIDSet(acct, st, TypeEC2VPC); err != nil {
		return s, err
	}
	if s.sub, err = scannedIDSet(acct, st, TypeEC2Subnet); err != nil {
		return s, err
	}
	if s.sg, err = scannedIDSet(acct, st, TypeEC2SecurityGroup); err != nil {
		return s, err
	}
	return s, nil
}

// dfWireVPCConfig emits attached-to edges from r to the VPC, subnets and SGs of
// cfg, FK-safe against the scanned id sets. label names the source for errors.
func dfWireVPCConfig(st *store.Store, acctID string, r store.Resource, cfg *dfVPCConfig, sets dfVPCSets, label string) error {
	if cfg == nil {
		return nil
	}
	region := sv(r.Region)
	if v := sv(cfg.VpcID); v != "" {
		tgtID := store.ResourceID("aws", acctID, ec2ARN(region, acctID, "vpc", v))
		if sets.vpc[tgtID] {
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert devicefarm %s→vpc: %w", label, err)
			}
		}
	}
	for _, sid := range cfg.SubnetIDs {
		if sid == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acctID, ec2ARN(region, acctID, "subnet", sid))
		if sets.sub[tgtID] {
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert devicefarm %s→subnet: %w", label, err)
			}
		}
	}
	for _, gid := range cfg.SecurityGroupIDs {
		if gid == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acctID, ec2ARN(region, acctID, "security-group", gid))
		if sets.sg[tgtID] {
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert devicefarm %s→sg: %w", label, err)
			}
		}
	}
	return nil
}

// dfProjectARNFromChild rebuilds the parent project ARN from a device-pool or
// network-profile ARN. Child ARN shape:
// arn:aws:devicefarm:{r}:{a}:{kind}:{projectGUID}/{childGUID}; the project is
// arn:aws:devicefarm:{r}:{a}:project:{projectGUID}.
func dfProjectARNFromChild(childARN string) string {
	parts := strings.SplitN(childARN, ":", 7)
	if len(parts) < 7 {
		return ""
	}
	guid, _, ok := strings.Cut(parts[6], "/")
	if !ok || guid == "" {
		return ""
	}
	return strings.Join(parts[:5], ":") + ":project:" + guid
}

// resolveDeviceFarmProjectChildren attaches device pools + network profiles to
// their parent project via the project GUID embedded in the child ARN.
func resolveDeviceFarmProjectChildren(acct *account, st *store.Store) error {
	projSet, err := scannedIDSet(acct, st, TypeDeviceFarmProject)
	if err != nil {
		return err
	}
	for _, ctype := range []string{TypeDeviceFarmDevicePool, TypeDeviceFarmNetworkProfile} {
		rows, err := st.ListResources(store.ResourceFilter{
			Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{ctype}, Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			parent := dfProjectARNFromChild(r.NativeID)
			if parent == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, parent)
			if !projSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert devicefarm %s→project: %w", ctype, err)
			}
		}
	}
	return nil
}

// resolveDeviceFarmDeviceInstanceProfile wires each private device instance to
// the instance profile applied to it.
func resolveDeviceFarmDeviceInstanceProfile(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeDeviceFarmDeviceInstance}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	profSet, err := scannedIDSet(acct, st, TypeDeviceFarmInstanceProfile)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			InstanceProfile *struct {
				Arn *string `json:"Arn"`
			} `json:"InstanceProfile"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.InstanceProfile == nil {
			continue
		}
		if p := sv(attrs.InstanceProfile.Arn); p != "" {
			tgtID := store.ResourceID("aws", acct.ID, p)
			if profSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert devicefarm device-instance→instance-profile: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveDeviceFarmTestGridProjectVPC wires each test-grid project to the VPC,
// subnets and security groups of its VpcConfig.
func resolveDeviceFarmTestGridProjectVPC(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeDeviceFarmTestGridProject}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	sets, err := loadDFVPCSets(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			VpcConfig *dfVPCConfig `json:"VpcConfig"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if err := dfWireVPCConfig(st, acct.ID, r, attrs.VpcConfig, sets, "testgrid"); err != nil {
			return err
		}
	}
	return nil
}

// resolveDeviceFarmProjectRefs wires each project to its execution IAM role and
// to the VPC / subnets / security groups of its VpcConfig.
func resolveDeviceFarmProjectRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeDeviceFarmProject}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	sets, err := loadDFVPCSets(acct, st)
	if err != nil {
		return err
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			ExecutionRoleArn *string      `json:"ExecutionRoleArn"`
			VpcConfig        *dfVPCConfig `json:"VpcConfig"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if role := sv(attrs.ExecutionRoleArn); role != "" {
			tgtID := store.ResourceID("aws", acct.ID, role)
			if roleSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert devicefarm project→role: %w", err)
				}
			}
		}
		if err := dfWireVPCConfig(st, acct.ID, r, attrs.VpcConfig, sets, "project"); err != nil {
			return err
		}
	}
	return nil
}
