package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveAppRunnerServiceTargets)
	registerResolver(resolveAppRunnerVPCConnectorTargets)
}

// apprunnerServiceAttrs mirrors the verbatim Service fields used by the
// resolver. PascalCase tags match mustJSON of the SDK v2 struct.
type apprunnerServiceAttrs struct {
	InstanceConfiguration *struct {
		InstanceRoleArn *string `json:"InstanceRoleArn"`
	} `json:"InstanceConfiguration"`
	NetworkConfiguration *struct {
		EgressConfiguration *struct {
			VpcConnectorArn *string `json:"VpcConnectorArn"`
		} `json:"EgressConfiguration"`
	} `json:"NetworkConfiguration"`
	SourceConfiguration *struct {
		AuthenticationConfiguration *struct {
			AccessRoleArn *string `json:"AccessRoleArn"`
		} `json:"AuthenticationConfiguration"`
		ImageRepository *struct {
			ImageIdentifier *string `json:"ImageIdentifier"`
		} `json:"ImageRepository"`
	} `json:"SourceConfiguration"`
	EncryptionConfiguration *struct {
		KmsKey *string `json:"KmsKey"`
	} `json:"EncryptionConfiguration"`
}

// resolveAppRunnerServiceTargets emits service outbound edges:
//   - service → VPC connector (uses) via NetworkConfiguration.EgressConfiguration.VpcConnectorArn
//   - service → IAM instance role (assumes) via InstanceConfiguration.InstanceRoleArn
//   - service → IAM access role (assumes) via SourceConfiguration.AuthenticationConfiguration.AccessRoleArn
//   - service → ECR repository (uses) via SourceConfiguration.ImageRepository.ImageIdentifier (ARN parsed to repo)
//   - service → KMS key (uses) via EncryptionConfiguration.KmsKey
//
// FK-safe via per-type id sets + KMS resolve index. Cross-account refs
// and AWS-managed default keys (`alias/aws/*`) skip silently.
func resolveAppRunnerServiceTargets(acct *account, st *store.Store) error {
	services, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeAppRunnerService},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(services) == 0 {
		return nil
	}

	connectorIDs, err := resourceIDSet(st, acct.ID, TypeAppRunnerVPCConnector)
	if err != nil {
		return err
	}
	roleIDs, err := resourceIDSet(st, acct.ID, TypeIAMRole)
	if err != nil {
		return err
	}
	repoIDs, err := resourceIDSet(st, acct.ID, TypeECRRepository)
	if err != nil {
		return err
	}
	kmsIdx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}

	for _, s := range services {
		var attrs apprunnerServiceAttrs
		if err := json.Unmarshal([]byte(s.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := ""
		if s.Region != nil {
			region = *s.Region
		}

		if attrs.NetworkConfiguration != nil && attrs.NetworkConfiguration.EgressConfiguration != nil {
			if vcArn := sv(attrs.NetworkConfiguration.EgressConfiguration.VpcConnectorArn); vcArn != "" {
				cID := store.ResourceID("aws", acct.ID, TypeAppRunnerVPCConnector, vcArn)
				if _, ok := connectorIDs[cID]; ok {
					if err := st.UpsertRelationship(s.ID, cID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert apprunner service→vpc-connector: %w", err)
					}
				}
			}
		}

		for _, roleARN := range []string{
			func() string {
				if attrs.InstanceConfiguration != nil {
					return sv(attrs.InstanceConfiguration.InstanceRoleArn)
				}
				return ""
			}(),
			func() string {
				if attrs.SourceConfiguration != nil && attrs.SourceConfiguration.AuthenticationConfiguration != nil {
					return sv(attrs.SourceConfiguration.AuthenticationConfiguration.AccessRoleArn)
				}
				return ""
			}(),
		} {
			if roleARN == "" {
				continue
			}
			rID := store.ResourceID("aws", acct.ID, TypeIAMRole, roleARN)
			if _, ok := roleIDs[rID]; ok {
				if err := st.UpsertRelationship(s.ID, rID, store.RelAssumes, "directed", nil); err != nil {
					return fmt.Errorf("upsert apprunner service→iam role: %w", err)
				}
			}
		}

		if attrs.SourceConfiguration != nil && attrs.SourceConfiguration.ImageRepository != nil {
			if repoARN := apprunnerImageToRepoARN(sv(attrs.SourceConfiguration.ImageRepository.ImageIdentifier)); repoARN != "" {
				rID := store.ResourceID("aws", acct.ID, TypeECRRepository, repoARN)
				if _, ok := repoIDs[rID]; ok {
					if err := st.UpsertRelationship(s.ID, rID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert apprunner service→ecr repo: %w", err)
					}
				}
			}
		}

		if attrs.EncryptionConfiguration != nil {
			if keyRef := sv(attrs.EncryptionConfiguration.KmsKey); keyRef != "" {
				if keyID, ok := kmsIdx.resolveKMSKeyID(keyRef, region, acct.ID); ok {
					if err := st.UpsertRelationship(s.ID, keyID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert apprunner service→kms: %w", err)
					}
				}
			}
		}
	}
	return nil
}

// apprunnerImageToRepoARN strips the image-tag suffix from an ECR image
// identifier to recover the repository ARN. App Runner's
// ImageIdentifier shape:
//
//	{acct}.dkr.ecr.{region}.amazonaws.com/{repo}:{tag}     (URL form)
//	public.ecr.aws/{namespace}/{repo}:{tag}                (public ECR — no edge)
//
// ECR repository NativeID per ecr scanner is the canonical ARN
// `arn:aws:ecr:{region}:{acct}:repository/{name}`. Reconstruct from
// the URL form. Returns empty string for public-ECR images and other
// non-ECR registries.
func apprunnerImageToRepoARN(imageID string) string {
	if imageID == "" || strings.HasPrefix(imageID, "public.ecr.aws/") {
		return ""
	}
	// Trim tag.
	if i := strings.LastIndexByte(imageID, ':'); i >= 0 {
		imageID = imageID[:i]
	}
	// Split host / repo path.
	host, repo, ok := strings.Cut(imageID, "/")
	if !ok || repo == "" {
		return ""
	}
	// Host: <acct>.dkr.ecr.<region>.amazonaws.com
	if !strings.Contains(host, ".dkr.ecr.") {
		return ""
	}
	acct, rest, ok := strings.Cut(host, ".dkr.ecr.")
	if !ok {
		return ""
	}
	region, _, ok := strings.Cut(rest, ".amazonaws.com")
	if !ok || region == "" || acct == "" {
		return ""
	}
	return fmt.Sprintf("arn:aws:ecr:%s:%s:repository/%s", region, acct, repo)
}

// apprunnerVPCConnectorAttrs mirrors the verbatim VpcConnector fields.
type apprunnerVPCConnectorAttrs struct {
	Subnets        []string `json:"Subnets"`
	SecurityGroups []string `json:"SecurityGroups"`
}

// resolveAppRunnerVPCConnectorTargets emits vpc-connector → subnet (uses)
// + vpc-connector → security group (uses) per Subnets[]/SecurityGroups[].
// FK-safe via subnet + SG id sets.
func resolveAppRunnerVPCConnectorTargets(acct *account, st *store.Store) error {
	connectors, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeAppRunnerVPCConnector},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(connectors) == 0 {
		return nil
	}

	subnetIDs, err := resourceIDSet(st, acct.ID, TypeEC2Subnet)
	if err != nil {
		return err
	}
	sgIDs, err := resourceIDSet(st, acct.ID, TypeEC2SecurityGroup)
	if err != nil {
		return err
	}

	for _, c := range connectors {
		var attrs apprunnerVPCConnectorAttrs
		if err := json.Unmarshal([]byte(c.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := ""
		if c.Region != nil {
			region = *c.Region
		}

		for _, snID := range attrs.Subnets {
			if snID == "" {
				continue
			}
			snARN := ec2ARN(region, acct.ID, "subnet", snID)
			sID := store.ResourceID("aws", acct.ID, TypeEC2Subnet, snARN)
			if _, ok := subnetIDs[sID]; ok {
				if err := st.UpsertRelationship(c.ID, sID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert apprunner vpc-connector→subnet: %w", err)
				}
			}
		}
		for _, sgID := range attrs.SecurityGroups {
			if sgID == "" {
				continue
			}
			sgARN := ec2ARN(region, acct.ID, "security-group", sgID)
			rID := store.ResourceID("aws", acct.ID, TypeEC2SecurityGroup, sgARN)
			if _, ok := sgIDs[rID]; ok {
				if err := st.UpsertRelationship(c.ID, rID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert apprunner vpc-connector→sg: %w", err)
				}
			}
		}
	}
	return nil
}
