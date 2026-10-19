package aws

import (
	"encoding/json"
	"errors"
	"fmt"

	rhtypes "github.com/aws/aws-sdk-go-v2/service/resiliencehub/types"
	rhv2types "github.com/aws/aws-sdk-go-v2/service/resiliencehubv2/types"
	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveRHAppAssessmentToApp,
		EdgeDecl{TypeResilienceHubAppAssessment, TypeResilienceHubApp, store.RelAttachedTo},
	)
	registerResolver(
		noopResilienceHubHierarchy,
		EdgeDecl{TypeResilienceHubService, TypeResilienceHubTest, store.RelContains},
		EdgeDecl{TypeResilienceHubSystem, TypeResilienceHubUserJourney, store.RelContains},
		EdgeDecl{TypeResilienceHubApp, TypeResilienceHubAppInputSource, store.RelContains},
		EdgeDecl{TypeResilienceHubService, TypeResilienceHubServiceFunction, store.RelContains},
		EdgeDecl{TypeResilienceHubService, TypeResilienceHubInputSource, store.RelContains},
	)
	registerResolver(
		resolveRHInputSourceTargets,
		EdgeDecl{TypeResilienceHubAppInputSource, TypeCloudFormationStack, store.RelUses},
		EdgeDecl{TypeResilienceHubAppInputSource, TypeSCARApplication, store.RelUses},
		EdgeDecl{TypeResilienceHubAppInputSource, TypeResourceGroupsGroup, store.RelUses},
		EdgeDecl{TypeResilienceHubAppInputSource, TypeEKSCluster, store.RelUses},
		EdgeDecl{TypeResilienceHubInputSource, TypeCloudFormationStack, store.RelUses},
		EdgeDecl{TypeResilienceHubInputSource, TypeEKSCluster, store.RelUses},
	)
}

// noopResilienceHubHierarchy emits nothing: the service → test,
// system → user-journey, app → app-input-source, service → service-function
// and service → input-source contains edges are written at scan time by
// childFanOut (scanRHTests, scanRHUserJourneys, scanRHAppInputSources,
// scanRHServiceFunctions, scanRHInputSources). It is registered only so those
// edges are declared to coverage tooling.
func noopResilienceHubHierarchy(_ *account, _ *store.Store) error {
	return nil
}

// resolveRHAppAssessmentToApp wires each app-assessment to the app it assesses
// (AppArn).
func resolveRHAppAssessmentToApp(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{TypeResilienceHubAppAssessment}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	appSet, err := scannedIDSet(acct, st, TypeResilienceHubApp)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			AppArn *string `json:"AppArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if a := sv(attrs.AppArn); a != "" {
			tgtID := store.ResourceID("aws", acct.ID, a)
			if appSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert rh app-assessment→app: %w", err)
				}
			}
		}
	}
	return nil
}

// rhInputSourceTargetTypes maps an input source's ImportType to the disco
// type its SourceArn names. Terraform sources name an S3 state file and
// Resource sources an arbitrary resource, so neither has an entry; EKS
// sources name their cluster in EksSourceClusterNamespace instead.
var rhInputSourceTargetTypes = map[rhtypes.ResourceMappingType]string{
	rhtypes.ResourceMappingTypeCfnStack:       TypeCloudFormationStack,
	rhtypes.ResourceMappingTypeAppRegistryApp: TypeSCARApplication,
	rhtypes.ResourceMappingTypeResourceGroup:  TypeResourceGroupsGroup,
}

// resolveRHInputSourceTargets wires each v1 app input source and v2 service
// input source to the stack, AppRegistry application, resource group or EKS
// cluster it imports from, when that target was scanned.
func resolveRHInputSourceTargets(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{TypeResilienceHubAppInputSource, TypeResilienceHubInputSource}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	targetSets := map[string]map[string]bool{}
	for _, typ := range []string{TypeCloudFormationStack, TypeSCARApplication, TypeResourceGroupsGroup, TypeEKSCluster} {
		if targetSets[typ], err = scannedIDSet(acct, st, typ); err != nil {
			return err
		}
	}
	// A row that fails to decode is reported at the end so that it costs the
	// other rows none of their edges.
	var decodeErrs []error
	for _, r := range rows {
		targetType, targetARN, err := rhInputSourceTarget(r)
		if err != nil {
			decodeErrs = append(decodeErrs, fmt.Errorf("decode %s %s attributes: %w", r.Type, r.NativeID, err))
			continue
		}
		if targetType == "" || targetARN == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, targetARN)
		if !targetSets[targetType][tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert rh %s→%s: %w", r.Type, targetType, err)
		}
	}
	return errors.Join(decodeErrs...)
}

// rhInputSourceTarget decodes an input source row of either API and returns
// the type and ARN of the resource it imports from; "" when it names none
// disco scans (Terraform state files, design files, tag queries).
func rhInputSourceTarget(r store.Resource) (targetType, targetARN string, err error) {
	if r.Type == TypeResilienceHubInputSource {
		var src rhv2types.InputSourceSummary
		if err := json.Unmarshal([]byte(r.AttributesJSON), &src); err != nil {
			return "", "", err
		}
		switch src.Type {
		case rhv2types.InputSourceTypeCfnStack:
			return TypeCloudFormationStack, sv(src.CfnStackArn), nil
		case rhv2types.InputSourceTypeEks:
			if src.Eks == nil {
				return "", "", nil
			}
			return TypeEKSCluster, sv(src.Eks.ClusterArn), nil
		default:
			return "", "", nil
		}
	}
	var src rhtypes.AppInputSource
	if err := json.Unmarshal([]byte(r.AttributesJSON), &src); err != nil {
		return "", "", err
	}
	if src.ImportType == rhtypes.ResourceMappingTypeEks {
		if src.EksSourceClusterNamespace == nil {
			return "", "", nil
		}
		return TypeEKSCluster, sv(src.EksSourceClusterNamespace.EksClusterArn), nil
	}
	return rhInputSourceTargetTypes[src.ImportType], sv(src.SourceArn), nil
}
