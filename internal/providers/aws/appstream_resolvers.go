package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveAppStreamFleetRefs,
		EdgeDecl{TypeAppStreamFleet, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeAppStreamFleet, TypeEC2SecurityGroup, store.RelUses},
		EdgeDecl{TypeAppStreamFleet, TypeIAMRole, store.RelAssumes},
		EdgeDecl{TypeAppStreamFleet, TypeAppStreamDirectoryConfig, store.RelUses},
	)
	registerResolver(resolveAppStreamImageBuilderRefs,
		EdgeDecl{TypeAppStreamImageBuilder, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeAppStreamImageBuilder, TypeEC2SecurityGroup, store.RelUses},
		EdgeDecl{TypeAppStreamImageBuilder, TypeIAMRole, store.RelAssumes},
		EdgeDecl{TypeAppStreamImageBuilder, TypeAppStreamDirectoryConfig, store.RelUses},
	)
	registerResolver(resolveAppStreamAppBlockBuilderRefs,
		EdgeDecl{TypeAppStreamAppBlockBuilder, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeAppStreamAppBlockBuilder, TypeEC2SecurityGroup, store.RelUses},
	)
	registerResolver(resolveAppStreamApplicationAppBlock,
		EdgeDecl{TypeAppStreamApplication, TypeAppStreamAppBlock, store.RelUses},
	)
	registerResolver(resolveAppStreamApplicationFleetAssoc,
		EdgeDecl{TypeAppStreamApplicationFleetAssociation, TypeAppStreamFleet, store.RelAttachedTo},
		EdgeDecl{TypeAppStreamApplicationFleetAssociation, TypeAppStreamApplication, store.RelAttachedTo},
	)
	registerResolver(resolveAppStreamEntitlementStack,
		EdgeDecl{TypeAppStreamEntitlement, TypeAppStreamStack, store.RelAttachedTo},
	)
	registerResolver(resolveAppStreamStackFleetAssoc,
		EdgeDecl{TypeAppStreamStackFleetAssociation, TypeAppStreamStack, store.RelAttachedTo},
		EdgeDecl{TypeAppStreamStackFleetAssociation, TypeAppStreamFleet, store.RelAttachedTo},
	)
	registerResolver(resolveAppStreamStackUserAssoc,
		EdgeDecl{TypeAppStreamStackUserAssociation, TypeAppStreamStack, store.RelAttachedTo},
	)
	registerResolver(resolveAppStreamApplicationEntitlementAssoc,
		EdgeDecl{TypeAppStreamApplicationEntitlementAssociation, TypeAppStreamStack, store.RelAttachedTo},
		EdgeDecl{TypeAppStreamApplicationEntitlementAssociation, TypeAppStreamEntitlement, store.RelAttachedTo},
	)
}

type appstreamVpcRoleAttrs struct {
	VpcConfig *struct {
		SubnetIds        []string `json:"SubnetIds"`
		SecurityGroupIds []string `json:"SecurityGroupIds"`
	} `json:"VpcConfig"`
	IamRoleArn     *string `json:"IamRoleArn"`
	DomainJoinInfo *struct {
		DirectoryName *string `json:"DirectoryName"`
	} `json:"DomainJoinInfo"`
}

func resolveAppStreamFleetRefs(acct *account, st *store.Store) error {
	return resolveAppStreamVpcRoleDir(acct, st, TypeAppStreamFleet)
}

func resolveAppStreamImageBuilderRefs(acct *account, st *store.Store) error {
	return resolveAppStreamVpcRoleDir(acct, st, TypeAppStreamImageBuilder)
}

func resolveAppStreamAppBlockBuilderRefs(acct *account, st *store.Store) error {
	return resolveAppStreamVpcRoleDir(acct, st, TypeAppStreamAppBlockBuilder)
}

// resolveAppStreamVpcRoleDir wires fleets / image-builders / app-block-builders
// to their VPC subnets, security groups, IAM role and (where present) the
// referenced AppStream directory-config. Targets all FK-safe.
func resolveAppStreamVpcRoleDir(acct *account, st *store.Store, rtype string) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{rtype}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	subnetSet, err := scannedIDSet(acct, st, TypeEC2Subnet)
	if err != nil {
		return err
	}
	sgSet, err := scannedIDSet(acct, st, TypeEC2SecurityGroup)
	if err != nil {
		return err
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	dirSet, err := scannedIDSet(acct, st, TypeAppStreamDirectoryConfig)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs appstreamVpcRoleAttrs
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if attrs.VpcConfig != nil {
			for _, sid := range attrs.VpcConfig.SubnetIds {
				sARN := ec2ARN(region, acct.ID, "subnet", sid)
				tgtID := store.ResourceID("aws", acct.ID, TypeEC2Subnet, sARN)
				if subnetSet[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
						return fmt.Errorf("upsert appstream %s→subnet: %w", rtype, err)
					}
				}
			}
			for _, sg := range attrs.VpcConfig.SecurityGroupIds {
				sgARN := ec2ARN(region, acct.ID, "security-group", sg)
				tgtID := store.ResourceID("aws", acct.ID, TypeEC2SecurityGroup, sgARN)
				if sgSet[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert appstream %s→sg: %w", rtype, err)
					}
				}
			}
		}
		if role := sv(attrs.IamRoleArn); role != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeIAMRole, role)
			if roleSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAssumes, "directed", nil); err != nil {
					return fmt.Errorf("upsert appstream %s→role: %w", rtype, err)
				}
			}
		}
		if attrs.DomainJoinInfo != nil {
			if dn := sv(attrs.DomainJoinInfo.DirectoryName); dn != "" {
				dARN := fmt.Sprintf("arn:aws:appstream:%s:%s:directory-config/%s", region, acct.ID, dn)
				tgtID := store.ResourceID("aws", acct.ID, TypeAppStreamDirectoryConfig, dARN)
				if dirSet[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert appstream %s→directory: %w", rtype, err)
					}
				}
			}
		}
	}
	return nil
}

// resolveAppStreamApplicationAppBlock links each application to its backing
// app-block via `AppBlockArn`.
func resolveAppStreamApplicationAppBlock(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeAppStreamApplication}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	abSet, err := scannedIDSet(acct, st, TypeAppStreamAppBlock)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			AppBlockArn *string `json:"AppBlockArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		arn := sv(attrs.AppBlockArn)
		if arn == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, TypeAppStreamAppBlock, arn)
		if !abSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert appstream application→app-block: %w", err)
		}
	}
	return nil
}

func appstreamFleetARN(region, acct, name string) string {
	return fmt.Sprintf("arn:aws:appstream:%s:%s:fleet/%s", region, acct, name)
}

func appstreamStackARN(region, acct, name string) string {
	return fmt.Sprintf("arn:aws:appstream:%s:%s:stack/%s", region, acct, name)
}

// resolveAppStreamApplicationFleetAssoc wires association rows to their
// fleet (FleetName, lookup by name) and application (ApplicationArn).
func resolveAppStreamApplicationFleetAssoc(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeAppStreamApplicationFleetAssociation},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	fleetSet, err := scannedIDSet(acct, st, TypeAppStreamFleet)
	if err != nil {
		return err
	}
	appSet, err := scannedIDSet(acct, st, TypeAppStreamApplication)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			FleetName      *string `json:"FleetName"`
			ApplicationArn *string `json:"ApplicationArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if fn := sv(attrs.FleetName); fn != "" {
			fARN := appstreamFleetARN(region, acct.ID, fn)
			tgtID := store.ResourceID("aws", acct.ID, TypeAppStreamFleet, fARN)
			if fleetSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert appstream app-fleet→fleet: %w", err)
				}
			}
		}
		if appARN := sv(attrs.ApplicationArn); appARN != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeAppStreamApplication, appARN)
			if appSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert appstream app-fleet→app: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveAppStreamEntitlementStack links each entitlement to its parent
// stack via `StackName` from the SDK shape.
func resolveAppStreamEntitlementStack(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeAppStreamEntitlement}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	stackSet, err := scannedIDSet(acct, st, TypeAppStreamStack)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			StackName *string `json:"StackName"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		sn := sv(attrs.StackName)
		if sn == "" {
			continue
		}
		region := sv(r.Region)
		sARN := appstreamStackARN(region, acct.ID, sn)
		tgtID := store.ResourceID("aws", acct.ID, TypeAppStreamStack, sARN)
		if !stackSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert appstream entitlement→stack: %w", err)
		}
	}
	return nil
}

// resolveAppStreamStackFleetAssoc wires the synthetic stack-fleet-association
// rows to both stack and fleet by name.
func resolveAppStreamStackFleetAssoc(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeAppStreamStackFleetAssociation},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	stackSet, err := scannedIDSet(acct, st, TypeAppStreamStack)
	if err != nil {
		return err
	}
	fleetSet, err := scannedIDSet(acct, st, TypeAppStreamFleet)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			StackName *string `json:"StackName"`
			FleetName *string `json:"FleetName"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if sn := sv(attrs.StackName); sn != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeAppStreamStack, appstreamStackARN(region, acct.ID, sn))
			if stackSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert appstream stack-fleet→stack: %w", err)
				}
			}
		}
		if fn := sv(attrs.FleetName); fn != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeAppStreamFleet, appstreamFleetARN(region, acct.ID, fn))
			if fleetSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert appstream stack-fleet→fleet: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveAppStreamStackUserAssoc wires user-stack association rows to their
// stack via `StackName`. The User row's NativeID is the user-pool ARN; lookup
// by user name across auth types is not deterministic, so the user edge is
// skipped.
func resolveAppStreamStackUserAssoc(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeAppStreamStackUserAssociation},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	stackSet, err := scannedIDSet(acct, st, TypeAppStreamStack)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			StackName *string `json:"StackName"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		sn := sv(attrs.StackName)
		if sn == "" {
			continue
		}
		region := sv(r.Region)
		tgtID := store.ResourceID("aws", acct.ID, TypeAppStreamStack, appstreamStackARN(region, acct.ID, sn))
		if !stackSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert appstream user-stack→stack: %w", err)
		}
	}
	return nil
}

// resolveAppStreamApplicationEntitlementAssoc parses the synthetic NativeID
// `…:application-entitlement-association/{stack}/{entitlement}/{appID}` and
// wires to stack + entitlement.
func resolveAppStreamApplicationEntitlementAssoc(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeAppStreamApplicationEntitlementAssociation},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	stackSet, err := scannedIDSet(acct, st, TypeAppStreamStack)
	if err != nil {
		return err
	}
	entSet, err := scannedIDSet(acct, st, TypeAppStreamEntitlement)
	if err != nil {
		return err
	}
	for _, r := range rows {
		const seg = "application-entitlement-association/"
		i := strings.Index(r.NativeID, seg)
		if i < 0 {
			continue
		}
		parts := strings.SplitN(r.NativeID[i+len(seg):], "/", 3)
		if len(parts) < 2 {
			continue
		}
		stack, entitlement := parts[0], parts[1]
		region := sv(r.Region)
		stackTgt := store.ResourceID("aws", acct.ID, TypeAppStreamStack, appstreamStackARN(region, acct.ID, stack))
		if stackSet[stackTgt] {
			if err := st.UpsertRelationship(r.ID, stackTgt, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert appstream app-ent→stack: %w", err)
			}
		}
		entARN := fmt.Sprintf("arn:aws:appstream:%s:%s:entitlement/%s/%s", region, acct.ID, stack, entitlement)
		entTgt := store.ResourceID("aws", acct.ID, TypeAppStreamEntitlement, entARN)
		if entSet[entTgt] {
			if err := st.UpsertRelationship(r.ID, entTgt, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert appstream app-ent→entitlement: %w", err)
			}
		}
	}
	return nil
}
