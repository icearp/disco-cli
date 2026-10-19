package aws

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveQuickSightVPCConnectionRefs,
		EdgeDecl{TypeQuickSightVPCConnection, TypeEC2VPC, store.RelAttachedTo},
		EdgeDecl{TypeQuickSightVPCConnection, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeQuickSightVPCConnection, TypeEC2SecurityGroup, store.RelUses},
		EdgeDecl{TypeQuickSightVPCConnection, TypeIAMRole, store.RelAssumes},
	)
	registerResolver(
		resolveQuickSightRefreshScheduleParent,
		EdgeDecl{TypeQuickSightRefreshSchedule, TypeQuickSightDataSet, store.RelAttachedTo},
	)
	registerResolver(
		resolveQuickSightNamespaceMembers,
		EdgeDecl{TypeQuickSightGroup, TypeQuickSightNamespace, store.RelAttachedTo},
		EdgeDecl{TypeQuickSightUser, TypeQuickSightNamespace, store.RelAttachedTo},
		EdgeDecl{TypeQuickSightAssignment, TypeQuickSightNamespace, store.RelAttachedTo},
	)
	registerResolver(
		noopQuickSightHierarchy,
		EdgeDecl{TypeQuickSightTemplate, TypeQuickSightTemplateAlias, store.RelContains},
		EdgeDecl{TypeQuickSightTheme, TypeQuickSightThemeAlias, store.RelContains},
		EdgeDecl{TypeQuickSightTopic, TypeQuickSightTopicRefreshSchedule, store.RelContains},
		EdgeDecl{TypeQuickSightFolder, TypeQuickSightFolderMember, store.RelContains},
		EdgeDecl{TypeQuickSightGroup, TypeQuickSightGroupMembership, store.RelContains},
	)
	registerResolver(
		resolveQuickSightFolderMemberTargets,
		EdgeDecl{TypeQuickSightFolderMember, TypeQuickSightAnalysis, store.RelAttachedTo},
		EdgeDecl{TypeQuickSightFolderMember, TypeQuickSightDashboard, store.RelAttachedTo},
		EdgeDecl{TypeQuickSightFolderMember, TypeQuickSightDataSet, store.RelAttachedTo},
		EdgeDecl{TypeQuickSightFolderMember, TypeQuickSightDataSource, store.RelAttachedTo},
		EdgeDecl{TypeQuickSightFolderMember, TypeQuickSightTopic, store.RelAttachedTo},
	)
	registerResolver(
		resolveQuickSightGroupMembershipUser,
		EdgeDecl{TypeQuickSightGroupMembership, TypeQuickSightUser, store.RelAttachedTo},
	)
}

// noopQuickSightHierarchy emits nothing: the parent → child contains edges it
// declares are written at scan time by childFanOut. It is registered only so
// those edges are declared to coverage tooling.
func noopQuickSightHierarchy(_ *account, _ *store.Store) error {
	return nil
}

// qsFolderMemberTargetTypes are the disco types of the asset kinds a folder
// can hold (the SDK's MemberType enum).
var qsFolderMemberTargetTypes = []string{
	TypeQuickSightAnalysis, TypeQuickSightDashboard, TypeQuickSightDataSet, TypeQuickSightDataSource, TypeQuickSightTopic,
}

// resolveQuickSightFolderMemberTargets links each folder member to the asset
// its MemberArn names.
func resolveQuickSightFolderMemberTargets(acct *account, st *store.Store) error {
	return qsARNFieldEdge(acct, st, TypeQuickSightFolderMember, qsFolderMemberTargetTypes, func(raw []byte) (string, error) {
		var attrs struct {
			MemberArn *string `json:"MemberArn"`
		}
		err := json.Unmarshal(raw, &attrs)
		return sv(attrs.MemberArn), err
	})
}

// resolveQuickSightGroupMembershipUser links each group membership to the
// member user its Arn names.
func resolveQuickSightGroupMembershipUser(acct *account, st *store.Store) error {
	return qsARNFieldEdge(acct, st, TypeQuickSightGroupMembership, []string{TypeQuickSightUser}, func(raw []byte) (string, error) {
		var attrs struct {
			Arn *string `json:"Arn"`
		}
		err := json.Unmarshal(raw, &attrs)
		return sv(attrs.Arn), err
	})
}

// qsARNFieldEdge emits an attached-to edge from each srcType row to the stored
// row, of any of tgtTypes, whose NativeID is the ARN arnOf reads from its
// attributes. A row that fails to decode is reported at the end, so that it
// costs the other rows none of their edges.
func qsARNFieldEdge(acct *account, st *store.Store, srcType string, tgtTypes []string, arnOf func([]byte) (string, error)) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{srcType}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	targets, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: tgtTypes, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	tgtSet := make(map[string]bool, len(targets))
	for _, t := range targets {
		tgtSet[t.ID] = true
	}
	var decodeErrs []error
	for _, r := range rows {
		arn, err := arnOf([]byte(r.AttributesJSON))
		if err != nil {
			decodeErrs = append(decodeErrs, fmt.Errorf("decode %s %s attributes: %w", srcType, r.NativeID, err))
			continue
		}
		if arn == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, arn)
		if !tgtSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert %s→%s: %w", srcType, arn, err)
		}
	}
	return errors.Join(decodeErrs...)
}

// quicksightNamespaceARNFromMember recovers the parent namespace ARN from a
// QuickSight group/user/assignment NativeID. Group/user ARNs embed the
// namespace name (`…:group/{ns}/{name}`, `…:user/{ns}/{name}`); assignments
// carry the synthetic `{namespaceARN}/assignment/{name}` shape.
func quicksightNamespaceARNFromMember(nativeID string) string {
	if i := strings.Index(nativeID, "/assignment/"); i >= 0 {
		return nativeID[:i]
	}
	for _, seg := range []string{":group/", ":user/"} {
		i := strings.Index(nativeID, seg)
		if i < 0 {
			continue
		}
		ns, _, ok := strings.Cut(nativeID[i+len(seg):], "/")
		if !ok || ns == "" {
			return ""
		}
		return nativeID[:i] + ":namespace/" + ns
	}
	return ""
}

// resolveQuickSightNamespaceMembers wires each group / user / assignment to its
// parent namespace. FK-safe via the scanned namespace id set.
func resolveQuickSightNamespaceMembers(acct *account, st *store.Store) error {
	nsSet, err := scannedIDSet(acct, st, TypeQuickSightNamespace)
	if err != nil {
		return err
	}
	for _, mtype := range []string{TypeQuickSightGroup, TypeQuickSightUser, TypeQuickSightAssignment} {
		rows, err := st.ListResources(store.ResourceFilter{
			Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{mtype}, Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			nsARN := quicksightNamespaceARNFromMember(r.NativeID)
			if nsARN == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, nsARN)
			if !nsSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert qs %s→namespace: %w", mtype, err)
			}
		}
	}
	return nil
}

// resolveQuickSightVPCConnectionRefs wires VPC connection → vpc + subnets
// (NetworkInterfaces[].SubnetID) + security groups + IAM role.
func resolveQuickSightVPCConnectionRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeQuickSightVPCConnection},
		Limit: util.AllResources,
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
	for _, r := range rows {
		var attrs struct {
			VPCId             *string  `json:"VPCId"`
			SecurityGroupIDs  []string `json:"SecurityGroupIds"`
			RoleArn           *string  `json:"RoleArn"`
			NetworkInterfaces []struct {
				SubnetID *string `json:"SubnetId"`
			} `json:"NetworkInterfaces"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if vpc := sv(attrs.VPCId); vpc != "" {
			vARN := ec2ARN(region, acct.ID, "vpc", vpc)
			tgtID := store.ResourceID("aws", acct.ID, vARN)
			if vpcSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert qs vpc-conn→vpc: %w", err)
				}
			}
		}
		for _, ni := range attrs.NetworkInterfaces {
			sid := sv(ni.SubnetID)
			if sid == "" {
				continue
			}
			sARN := ec2ARN(region, acct.ID, "subnet", sid)
			tgtID := store.ResourceID("aws", acct.ID, sARN)
			if subnetSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert qs vpc-conn→subnet: %w", err)
				}
			}
		}
		for _, sg := range attrs.SecurityGroupIDs {
			sgARN := ec2ARN(region, acct.ID, "security-group", sg)
			tgtID := store.ResourceID("aws", acct.ID, sgARN)
			if sgSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert qs vpc-conn→sg: %w", err)
				}
			}
		}
		if role := sv(attrs.RoleArn); role != "" {
			tgtID := store.ResourceID("aws", acct.ID, role)
			if roleSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAssumes, "directed", nil); err != nil {
					return fmt.Errorf("upsert qs vpc-conn→role: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveQuickSightRefreshScheduleParent wires each refresh-schedule to its
// parent dataset by parsing the NativeID of shape
// `arn:aws:quicksight:r:a:dataset/{datasetID}/refresh-schedule/{id}`.
func resolveQuickSightRefreshScheduleParent(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeQuickSightRefreshSchedule},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	dsSet, err := scannedIDSet(acct, st, TypeQuickSightDataSet)
	if err != nil {
		return err
	}
	for _, r := range rows {
		const seg = "/refresh-schedule/"
		i := strings.Index(r.NativeID, seg)
		if i < 0 {
			continue
		}
		dsARN := r.NativeID[:i]
		tgtID := store.ResourceID("aws", acct.ID, dsARN)
		if !dsSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert qs refresh-schedule→dataset: %w", err)
		}
	}
	return nil
}

func init() {
	registerResolver(
		resolveQSDataSourceRefs,
		EdgeDecl{TypeQuickSightDataSource, TypeSecretsManagerSecret, store.RelUses},
		EdgeDecl{TypeQuickSightDataSource, TypeQuickSightVPCConnection, store.RelAttachedTo},
	)
}

// resolveQSDataSourceRefs wires each QuickSight data source to its Secrets
// Manager auth secret and QS VPC connection (if VPC-routed). Underlying
// data refs (RDS / Redshift / Athena / etc.) live in DataSourceParameters
// union — skipped to avoid SDK-union JSON ambiguity.
func resolveQSDataSourceRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeQuickSightDataSource}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	secretSet, err := scannedIDSet(acct, st, TypeSecretsManagerSecret)
	if err != nil {
		return err
	}
	vpcConnSet, err := scannedIDSet(acct, st, TypeQuickSightVPCConnection)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			SecretArn               *string `json:"SecretArn"`
			VpcConnectionProperties *struct {
				VpcConnectionArn *string `json:"VpcConnectionArn"`
			} `json:"VpcConnectionProperties"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if sa := sv(attrs.SecretArn); strings.Contains(sa, ":secretsmanager:") {
			tgt := store.ResourceID("aws", acct.ID, sa)
			if secretSet[tgt] {
				if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert qs-data-source→secret: %w", err)
				}
			}
		}
		if attrs.VpcConnectionProperties != nil {
			if va := sv(attrs.VpcConnectionProperties.VpcConnectionArn); va != "" {
				tgt := store.ResourceID("aws", acct.ID, va)
				if vpcConnSet[tgt] {
					if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
						return fmt.Errorf("upsert qs-data-source→vpc-conn: %w", err)
					}
				}
			}
		}
	}
	return nil
}
