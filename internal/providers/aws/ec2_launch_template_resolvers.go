package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(
		resolveEC2LaunchTemplateRefs,
		EdgeDecl{TypeEC2LaunchTemplate, TypeEC2Image, store.RelUses},
		EdgeDecl{TypeEC2LaunchTemplate, TypeIAMInstanceProfile, store.RelUses},
		EdgeDecl{TypeEC2LaunchTemplate, TypeEC2KeyPair, store.RelUses},
		EdgeDecl{TypeEC2LaunchTemplate, TypeEC2SecurityGroup, store.RelAttachedTo},
		EdgeDecl{TypeEC2LaunchTemplate, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeEC2LaunchTemplate, TypeKMSKey, store.RelUses},
	)
}

// ec2LaunchTemplateData mirrors the DefaultVersion.LaunchTemplateData
// fields the resolver walks. PascalCase tags match SDK marshal output.
type ec2LaunchTemplateData struct {
	ImageID            *string `json:"ImageId"`
	KeyName            *string `json:"KeyName"`
	IamInstanceProfile *struct {
		Arn  *string `json:"Arn"`
		Name *string `json:"Name"`
	} `json:"IamInstanceProfile"`
	SecurityGroupIDs  []string `json:"SecurityGroupIds"`
	NetworkInterfaces []struct {
		SubnetID *string  `json:"SubnetId"`
		Groups   []string `json:"Groups"`
	} `json:"NetworkInterfaces"`
	BlockDeviceMappings []struct {
		Ebs *struct {
			KmsKeyID *string `json:"KmsKeyId"`
		} `json:"Ebs"`
	} `json:"BlockDeviceMappings"`
}

// ec2LaunchTemplateAttrs wraps the scanner-enriched shape:
// {"LaunchTemplate":..., "DefaultVersion":...}.
type ec2LaunchTemplateAttrs struct {
	DefaultVersion *struct {
		LaunchTemplateData *ec2LaunchTemplateData `json:"LaunchTemplateData"`
	} `json:"DefaultVersion"`
}

// ec2LaunchTemplateTargetSets bundles FK-safe id sets + KMS index +
// (region, key-name) → key-pair id index for the resolver helpers.
type ec2LaunchTemplateTargetSets struct {
	imgSet          map[string]bool
	ipSet           map[string]bool
	sgSet           map[string]bool
	subnetSet       map[string]bool
	keyByNameRegion map[string]string
	kmsIdx          *kmsResolveIndex
}

// resolveEC2LaunchTemplateRefs walks the embedded DefaultVersion's
// LaunchTemplateData and emits AMI / IAM-instance-profile / key-pair /
// security-group / subnet / KMS edges. The wrapped attrs shape is
// `{"LaunchTemplate":..., "DefaultVersion":...}` (per scanner enrichment).
// FK-safe via scannedIDSet; refs to public AMIs / shared keys / etc. skip.
func resolveEC2LaunchTemplateRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2LaunchTemplate}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	sets, err := loadEC2LaunchTemplateTargetSets(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs ec2LaunchTemplateAttrs
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.DefaultVersion == nil || attrs.DefaultVersion.LaunchTemplateData == nil {
			continue
		}
		region := sv(r.Region)
		d := attrs.DefaultVersion.LaunchTemplateData
		if err := emitLTAMIEdge(st, acct, r, region, d, sets); err != nil {
			return err
		}
		if err := emitLTInstanceProfileEdge(st, acct, r, d, sets); err != nil {
			return err
		}
		if err := emitLTKeyPairEdge(st, r, region, d, sets); err != nil {
			return err
		}
		if err := emitLTNetworkEdges(st, acct, r, region, d, sets); err != nil {
			return err
		}
		if err := emitLTKMSEdges(st, acct, r, region, d, sets); err != nil {
			return err
		}
	}
	return nil
}

func loadEC2LaunchTemplateTargetSets(acct *account, st *store.Store) (ec2LaunchTemplateTargetSets, error) {
	var sets ec2LaunchTemplateTargetSets
	var err error
	if sets.imgSet, err = scannedIDSet(acct, st, TypeEC2Image); err != nil {
		return sets, err
	}
	if sets.ipSet, err = scannedIDSet(acct, st, TypeIAMInstanceProfile); err != nil {
		return sets, err
	}
	if sets.sgSet, err = scannedIDSet(acct, st, TypeEC2SecurityGroup); err != nil {
		return sets, err
	}
	if sets.subnetSet, err = scannedIDSet(acct, st, TypeEC2Subnet); err != nil {
		return sets, err
	}
	if sets.kmsIdx, err = loadKMSResolveIndex(acct, st); err != nil {
		return sets, err
	}
	kpRows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2KeyPair}, Limit: util.AllResources,
	})
	if err != nil {
		return sets, err
	}
	sets.keyByNameRegion = make(map[string]string, len(kpRows))
	for _, kp := range kpRows {
		if kp.Name == nil {
			continue
		}
		sets.keyByNameRegion[sv(kp.Region)+"\x00"+*kp.Name] = kp.ID
	}
	return sets, nil
}

func emitLTAMIEdge(st *store.Store, acct *account, r store.Resource, region string, d *ec2LaunchTemplateData, sets ec2LaunchTemplateTargetSets) error {
	id := sv(d.ImageID)
	if !strings.HasPrefix(id, "ami-") {
		return nil
	}
	tgt := store.ResourceID("aws", acct.ID, TypeEC2Image, ec2ARN(region, acct.ID, "image", id))
	if !sets.imgSet[tgt] {
		return nil
	}
	if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
		return fmt.Errorf("upsert lt→ami: %w", err)
	}
	return nil
}

func emitLTInstanceProfileEdge(st *store.Store, acct *account, r store.Resource, d *ec2LaunchTemplateData, sets ec2LaunchTemplateTargetSets) error {
	if d.IamInstanceProfile == nil {
		return nil
	}
	ipARN := sv(d.IamInstanceProfile.Arn)
	if ipARN == "" {
		if n := sv(d.IamInstanceProfile.Name); n != "" {
			ipARN = "arn:aws:iam::" + acct.ID + ":instance-profile/" + n
		}
	}
	if !strings.Contains(ipARN, ":instance-profile/") {
		return nil
	}
	tgt := store.ResourceID("aws", acct.ID, TypeIAMInstanceProfile, ipARN)
	if !sets.ipSet[tgt] {
		return nil
	}
	if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
		return fmt.Errorf("upsert lt→instance-profile: %w", err)
	}
	return nil
}

func emitLTKeyPairEdge(st *store.Store, r store.Resource, region string, d *ec2LaunchTemplateData, sets ec2LaunchTemplateTargetSets) error {
	kn := sv(d.KeyName)
	if kn == "" {
		return nil
	}
	kID, ok := sets.keyByNameRegion[region+"\x00"+kn]
	if !ok {
		return nil
	}
	if err := st.UpsertRelationship(r.ID, kID, store.RelUses, "directed", nil); err != nil {
		return fmt.Errorf("upsert lt→keypair: %w", err)
	}
	return nil
}

func emitLTNetworkEdges(st *store.Store, acct *account, r store.Resource, region string, d *ec2LaunchTemplateData, sets ec2LaunchTemplateTargetSets) error {
	sgIDs := append([]string(nil), d.SecurityGroupIDs...)
	for _, ni := range d.NetworkInterfaces {
		if sn := sv(ni.SubnetID); sn != "" {
			tgt := store.ResourceID("aws", acct.ID, TypeEC2Subnet, ec2ARN(region, acct.ID, "subnet", sn))
			if sets.subnetSet[tgt] {
				if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert lt→subnet: %w", err)
				}
			}
		}
		sgIDs = append(sgIDs, ni.Groups...)
	}
	for _, sg := range sgIDs {
		if sg == "" {
			continue
		}
		tgt := store.ResourceID("aws", acct.ID, TypeEC2SecurityGroup, ec2ARN(region, acct.ID, "security-group", sg))
		if !sets.sgSet[tgt] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert lt→sg: %w", err)
		}
	}
	return nil
}

func emitLTKMSEdges(st *store.Store, acct *account, r store.Resource, region string, d *ec2LaunchTemplateData, sets ec2LaunchTemplateTargetSets) error {
	for _, bdm := range d.BlockDeviceMappings {
		if bdm.Ebs == nil {
			continue
		}
		ref := sv(bdm.Ebs.KmsKeyID)
		if ref == "" {
			continue
		}
		keyID, ok := sets.kmsIdx.resolveKMSKeyID(ref, region, acct.ID)
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert lt→kms: %w", err)
		}
	}
	return nil
}
