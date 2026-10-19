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
		noopVPCLatticeChildHierarchy,
		EdgeDecl{TypeVpcLatticeServiceNetwork, TypeVpcLatticeServiceNetworkVpcEndpointAssociation, store.RelContains},
		EdgeDecl{TypeVpcLatticeTargetGroup, TypeVpcLatticeTarget, store.RelContains},
	)
	registerResolver(
		resolveVPCLatticeSNVpcEndpointAssocs,
		EdgeDecl{TypeVpcLatticeServiceNetworkVpcEndpointAssociation, TypeEC2VPCEndpoint, store.RelAttachedTo},
		EdgeDecl{TypeVpcLatticeServiceNetworkVpcEndpointAssociation, TypeEC2VPC, store.RelAttachedTo},
	)
	registerResolver(
		resolveVPCLatticeTargets,
		EdgeDecl{TypeVpcLatticeTarget, TypeEC2Instance, store.RelRoutesTo},
		EdgeDecl{TypeVpcLatticeTarget, TypeLambdaFunction, store.RelRoutesTo},
		EdgeDecl{TypeVpcLatticeTarget, TypeELBv2LoadBalancer, store.RelRoutesTo},
	)
}

// noopVPCLatticeChildHierarchy emits nothing: the service-network → VPC
// endpoint association and target-group → target contains edges are written
// at scan time by childFanOut. It is registered only so those edges are
// declared to coverage tooling.
func noopVPCLatticeChildHierarchy(_ *account, _ *store.Store) error {
	return nil
}

// resolveVPCLatticeSNVpcEndpointAssocs links each service-network VPC endpoint
// association to its VPC endpoint and VPC, both given as bare ids and built
// into this account's ec2 ARNs: an endpoint another account owns is not
// scanned here, so its lookup misses and no edge is written.
func resolveVPCLatticeSNVpcEndpointAssocs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{TypeVpcLatticeServiceNetworkVpcEndpointAssociation}, Limit: util.AllResources,
	})
	if err != nil || len(rows) == 0 {
		return err
	}
	vpceSet, err := scannedIDSet(acct, st, TypeEC2VPCEndpoint)
	if err != nil {
		return err
	}
	vpcSet, err := scannedIDSet(acct, st, TypeEC2VPC)
	if err != nil {
		return err
	}
	var decodeErrs []error
	for _, r := range rows {
		var attrs struct {
			VpcEndpointID *string `json:"VpcEndpointId"`
			VpcID         *string `json:"VpcId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			decodeErrs = append(decodeErrs, fmt.Errorf("decode vpclattice vpc-endpoint association %s: %w", r.NativeID, err))
			continue
		}
		region := sv(r.Region)
		for _, ref := range []struct {
			kind, id string
			set      map[string]bool
		}{
			{"vpc-endpoint", sv(attrs.VpcEndpointID), vpceSet},
			{"vpc", sv(attrs.VpcID), vpcSet},
		} {
			if ref.id == "" {
				continue
			}
			tgt := store.ResourceID("aws", acct.ID, ec2ARN(region, acct.ID, ref.kind, ref.id))
			if !ref.set[tgt] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert vpclattice vpc-endpoint association→%s: %w", ref.kind, err)
			}
		}
	}
	return errors.Join(decodeErrs...)
}

// vlTargetGroupOfTarget returns the target-group ARN a target's NativeID
// (vlTargetNativeID) starts with.
func vlTargetGroupOfTarget(nativeID string) (string, bool) {
	tgARN, _, ok := strings.Cut(nativeID, "/target/")
	return tgARN, ok
}

// resolveVPCLatticeTargets links each target to the resource its id names,
// which its target group's Type says: an EC2 instance id (INSTANCE), a Lambda
// function ARN (LAMBDA, linked to the unqualified function) or an Application
// Load Balancer ARN (ALB). An IP target names an address, not a scanned
// resource, so it gets no edge.
func resolveVPCLatticeTargets(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{TypeVpcLatticeTarget}, Limit: util.AllResources,
	})
	if err != nil || len(rows) == 0 {
		return err
	}
	tgs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{TypeVpcLatticeTargetGroup}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	tgTypes, decodeErrs := vlTargetGroupTypes(tgs)
	sets := map[string]map[string]bool{}
	for tgType, rtype := range map[string]string{"INSTANCE": TypeEC2Instance, "LAMBDA": TypeLambdaFunction, "ALB": TypeELBv2LoadBalancer} {
		if sets[tgType], err = scannedIDSet(acct, st, rtype); err != nil {
			return err
		}
	}
	for _, r := range rows {
		tgARN, ok := vlTargetGroupOfTarget(r.NativeID)
		if !ok {
			decodeErrs = append(decodeErrs, fmt.Errorf("vpclattice target %s: NativeID has no /target/ segment", r.NativeID))
			continue
		}
		var attrs struct {
			ID *string `json:"Id"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			decodeErrs = append(decodeErrs, fmt.Errorf("decode vpclattice target %s: %w", r.NativeID, err))
			continue
		}
		id := sv(attrs.ID)
		if id == "" {
			continue
		}
		tgType := tgTypes[store.ResourceID("aws", acct.ID, tgARN)]
		var tgtNative string
		switch tgType {
		case "INSTANCE":
			tgtNative = ec2ARN(sv(r.Region), acct.ID, "instance", id)
		case "LAMBDA":
			tgtNative = lambdaStripQualifier(id)
		case "ALB":
			tgtNative = id
		default:
			continue
		}
		tgt := store.ResourceID("aws", acct.ID, tgtNative)
		if !sets[tgType][tgt] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgt, store.RelRoutesTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert vpclattice target→%s: %w", strings.ToLower(tgType), err)
		}
	}
	return errors.Join(decodeErrs...)
}

// vlTargetGroupTypes maps each target group's ResourceID to its Type, and
// returns an error for each target group whose attributes do not decode.
func vlTargetGroupTypes(tgs []store.Resource) (map[string]string, []error) {
	types := make(map[string]string, len(tgs))
	var decodeErrs []error
	for _, tg := range tgs {
		var attrs struct {
			Type string `json:"Type"`
		}
		if err := json.Unmarshal([]byte(tg.AttributesJSON), &attrs); err != nil {
			decodeErrs = append(decodeErrs, fmt.Errorf("decode vpclattice target group %s: %w", tg.NativeID, err))
			continue
		}
		types[tg.ID] = attrs.Type
	}
	return types, decodeErrs
}
