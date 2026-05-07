package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
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
	idx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	imgSet, err := scannedIDSet(acct, st, TypeEC2Image)
	if err != nil {
		return err
	}
	ipSet, err := scannedIDSet(acct, st, TypeIAMInstanceProfile)
	if err != nil {
		return err
	}
	sgSet, err := scannedIDSet(acct, st, TypeEC2SecurityGroup)
	if err != nil {
		return err
	}
	subnetSet, err := scannedIDSet(acct, st, TypeEC2Subnet)
	if err != nil {
		return err
	}
	// Build (region, key-name) → resource id index for FK-safe key-pair lookup.
	kpRows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2KeyPair}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	keyByNameRegion := make(map[string]string, len(kpRows))
	for _, kp := range kpRows {
		if kp.Name == nil {
			continue
		}
		keyByNameRegion[sv(kp.Region)+"\x00"+*kp.Name] = kp.ID
	}
	for _, r := range rows {
		var attrs struct {
			DefaultVersion *struct {
				LaunchTemplateData *struct {
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
				} `json:"LaunchTemplateData"`
			} `json:"DefaultVersion"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.DefaultVersion == nil || attrs.DefaultVersion.LaunchTemplateData == nil {
			continue
		}
		region := sv(r.Region)
		d := attrs.DefaultVersion.LaunchTemplateData
		if id := sv(d.ImageID); strings.HasPrefix(id, "ami-") {
			tgt := store.ResourceID("aws", acct.ID, TypeEC2Image, ec2ARN(region, acct.ID, "image", id))
			if imgSet[tgt] {
				if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert lt→ami: %w", err)
				}
			}
		}
		if d.IamInstanceProfile != nil {
			ipARN := sv(d.IamInstanceProfile.Arn)
			if ipARN == "" {
				if n := sv(d.IamInstanceProfile.Name); n != "" {
					ipARN = "arn:aws:iam::" + acct.ID + ":instance-profile/" + n
				}
			}
			if strings.Contains(ipARN, ":instance-profile/") {
				tgt := store.ResourceID("aws", acct.ID, TypeIAMInstanceProfile, ipARN)
				if ipSet[tgt] {
					if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert lt→instance-profile: %w", err)
					}
				}
			}
		}
		if kn := sv(d.KeyName); kn != "" {
			if kID, ok := keyByNameRegion[region+"\x00"+kn]; ok {
				if err := st.UpsertRelationship(r.ID, kID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert lt→keypair: %w", err)
				}
			}
		}
		sgIDs := append([]string(nil), d.SecurityGroupIDs...)
		for _, ni := range d.NetworkInterfaces {
			if sn := sv(ni.SubnetID); sn != "" {
				tgt := store.ResourceID("aws", acct.ID, TypeEC2Subnet, ec2ARN(region, acct.ID, "subnet", sn))
				if subnetSet[tgt] {
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
			if !sgSet[tgt] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert lt→sg: %w", err)
			}
		}
		for _, bdm := range d.BlockDeviceMappings {
			if bdm.Ebs == nil {
				continue
			}
			if ref := sv(bdm.Ebs.KmsKeyID); ref != "" {
				if keyID, ok := idx.resolveKMSKeyID(ref, region, acct.ID); ok {
					if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert lt→kms: %w", err)
					}
				}
			}
		}
	}
	return nil
}
