package aws

import (
	"encoding/json"
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	// The scanner records these contains pairs itself (childFanOut); this
	// registration only declares them.
	registerResolver(
		noopEVSHierarchy,
		EdgeDecl{TypeEVSEnvironment, TypeEVSEnvironmentConnector, store.RelContains},
		EdgeDecl{TypeEVSEnvironment, TypeEVSEnvironmentHost, store.RelContains},
		EdgeDecl{TypeEVSEnvironment, TypeEVSEnvironmentVlan, store.RelContains},
		EdgeDecl{TypeEVSEnvironmentConnector, TypeEVSVMEntitlement, store.RelContains},
	)
	registerResolver(
		resolveEVSHostRefs,
		EdgeDecl{TypeEVSEnvironmentHost, TypeEC2Instance, store.RelUses},
		EdgeDecl{TypeEVSEnvironmentHost, TypeEC2Host, store.RelAttachedTo},
	)
	registerResolver(
		resolveEVSVlanSubnets,
		EdgeDecl{TypeEVSEnvironmentVlan, TypeEC2Subnet, store.RelUses},
	)
	registerResolver(
		resolveEVSConnectorSecrets,
		EdgeDecl{TypeEVSEnvironmentConnector, TypeSecretsManagerSecret, store.RelUses},
	)
}

func noopEVSHierarchy(_ *account, _ *store.Store) error {
	return nil
}

// evsEdge is one outbound edge an EVS row's attributes name.
type evsEdge struct {
	targetID, kind string
}

// resolveEVSRows emits, for every row of typ, the edges edgesOf derives from
// its attributes, keeping only those whose target is in the scanned set of
// its target type.
func resolveEVSRows(acct *account, st *store.Store, typ string, targetTypes []string, edgesOf func(r store.Resource) ([]evsEdge, error)) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{typ}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	scanned := map[string]bool{}
	for _, tt := range targetTypes {
		set, err := scannedIDSet(acct, st, tt)
		if err != nil {
			return err
		}
		for id := range set {
			scanned[id] = true
		}
	}
	for _, r := range rows {
		edges, err := edgesOf(r)
		if err != nil {
			return fmt.Errorf("decode %s %s: %w", r.Type, r.NativeID, err)
		}
		for _, e := range edges {
			if !scanned[e.targetID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, e.targetID, e.kind, "directed", nil); err != nil {
				return fmt.Errorf("upsert %s→%s: %w", r.Type, e.targetID, err)
			}
		}
	}
	return nil
}

// resolveEVSHostRefs wires each host to the EC2 instance that represents it
// and the Dedicated Host it runs on.
func resolveEVSHostRefs(acct *account, st *store.Store) error {
	return resolveEVSRows(acct, st, TypeEVSEnvironmentHost, []string{TypeEC2Instance, TypeEC2Host}, func(r store.Resource) ([]evsEdge, error) {
		var attrs struct {
			Ec2InstanceID   *string `json:"Ec2InstanceId"`
			DedicatedHostID *string `json:"DedicatedHostId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			return nil, err
		}
		region := sv(r.Region)
		var edges []evsEdge
		if id := sv(attrs.Ec2InstanceID); id != "" {
			edges = append(edges, evsEdge{store.ResourceID("aws", acct.ID, ec2ARN(region, acct.ID, "instance", id)), store.RelUses})
		}
		if id := sv(attrs.DedicatedHostID); id != "" {
			edges = append(edges, evsEdge{store.ResourceID("aws", acct.ID, ec2ARN(region, acct.ID, "dedicated-host", id)), store.RelAttachedTo})
		}
		return edges, nil
	})
}

// resolveEVSVlanSubnets wires each VLAN to the VPC subnet that carries it.
func resolveEVSVlanSubnets(acct *account, st *store.Store) error {
	return resolveEVSRows(acct, st, TypeEVSEnvironmentVlan, []string{TypeEC2Subnet}, func(r store.Resource) ([]evsEdge, error) {
		var attrs struct {
			SubnetID *string `json:"SubnetId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			return nil, err
		}
		id := sv(attrs.SubnetID)
		if id == "" {
			return nil, nil
		}
		return []evsEdge{{store.ResourceID("aws", acct.ID, ec2ARN(sv(r.Region), acct.ID, "subnet", id)), store.RelUses}}, nil
	})
}

// resolveEVSConnectorSecrets wires each connector to the Secrets Manager
// secret holding its appliance credentials. The model lets SecretArn be a
// secret name or partial ARN as well as the full ARN; only the full ARN
// matches the secret's NativeID, so the other forms yield no edge.
func resolveEVSConnectorSecrets(acct *account, st *store.Store) error {
	return resolveEVSRows(acct, st, TypeEVSEnvironmentConnector, []string{TypeSecretsManagerSecret}, func(r store.Resource) ([]evsEdge, error) {
		var attrs struct {
			SecretArn *string `json:"SecretArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			return nil, err
		}
		arn := sv(attrs.SecretArn)
		if arn == "" {
			return nil, nil
		}
		return []evsEdge{{store.ResourceID("aws", acct.ID, arn), store.RelUses}}, nil
	})
}
