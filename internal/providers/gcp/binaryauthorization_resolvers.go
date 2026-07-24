package gcp

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(resolveBinaryAuthorizationRelationships,
		EdgeDecl{TypeBinAuthAttestor, TypeIAMServiceAccount, store.RelUses},
	)
	registerResolver(resolveBinaryAuthorizationPolicyRelationships,
		EdgeDecl{TypeBinAuthPolicy, TypeBinAuthAttestor, store.RelUses},
		EdgeDecl{TypeBinAuthPolicy, TypeGKECluster, store.RelUses},
	)
}

// resolveBinaryAuthorizationRelationships derives attestor -[uses]->
// service-account via `userOwnedGrafeasNote.delegationServiceAccountEmail`.
// The delegation SA is the principal the attestor uses to read attestation
// occurrences from Container Analysis — the security-meaningful pivot for
// "who can validate signatures on behalf of this attestor".
//
// Attestor → KMS public key edges deferred — v1 stores PGP/PKIX public keys
// inline (not a KMS reference); v1beta1 has KMS-backed attestation key
// support, lands when GCP graduates it.
//
// Policy's own outbound edges (cluster admission rules → Attestor/GKE
// Cluster) are handled by resolveBinaryAuthorizationPolicyRelationships below
// (Resolver Wave R25) — the cluster-id shape turned out to be
// `{location}.{clusterId}`, not the ARN-like form this comment originally
// guessed.
func resolveBinaryAuthorizationRelationships(p *project, st *store.Store) error {
	atts, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeBinAuthAttestor},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(atts) == 0 {
		return nil
	}
	saByEmail, err := buildSAEmailIndex(p, st)
	if err != nil {
		return err
	}
	for _, att := range atts {
		var a struct {
			UserOwnedGrafeasNote struct {
				DelegationServiceAccountEmail string `json:"delegationServiceAccountEmail"`
			} `json:"userOwnedGrafeasNote"`
		}
		if err := json.Unmarshal([]byte(att.AttributesJSON), &a); err != nil {
			continue
		}
		email := a.UserOwnedGrafeasNote.DelegationServiceAccountEmail
		if email == "" {
			continue
		}
		saID, ok := saByEmail[email]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(att.ID, saID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert attestor→SA: %w", err)
		}
	}
	return nil
}

type binAuthAdmissionRule struct {
	RequireAttestationsBy []string `json:"requireAttestationsBy"`
}

// resolveBinaryAuthorizationPolicyRelationships derives the per-project
// singleton Policy's outbound edges — the follow-up this file's own header
// comment (above) flagged as deferred at the Attestor resolver's
// landing time:
//
//   - policy -[uses]-> attestor via `defaultAdmissionRule.requireAttestationsBy`
//     and every `clusterAdmissionRules[*].requireAttestationsBy` — each entry
//     is a full `projects/*/attestors/*` resource name (verified via `go
//     doc`), exact-matched against Attestor's own NativeID (`a.Name` at scan
//     time).
//   - policy -[uses]-> GKE cluster via the `clusterAdmissionRules` map's own
//     keys, each in `{location}.{clusterId}` form (per `go doc
//     binaryauthorization.Policy`) — location is always a bare zone/region
//     with no embedded dot, so splitting on the first "." unambiguously
//     recovers both components. Matched via `regionNameIndex`, which builds
//     the same key shape from each scanned GKE Cluster's own Region+Name
//     columns.
//
// The other 3 admission-rule maps (kubernetes namespace / service account /
// istio service identity) key on non-GCP-resource identifiers (K8s
// namespaces, "namespace:serviceaccount" pairs, SPIFFE IDs) — no resolvable
// disco target, left unwired.
func resolveBinaryAuthorizationPolicyRelationships(p *project, st *store.Store) error {
	policies, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeBinAuthPolicy},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(policies) == 0 {
		return nil
	}
	scannedAttestors, err := scannedIDSet(p, st, TypeBinAuthAttestor)
	if err != nil {
		return err
	}
	clusterByRegionName, err := regionNameIndex(p, st, TypeGKECluster)
	if err != nil {
		return err
	}

	emitAttestors := func(fromID string, rule binAuthAdmissionRule) error {
		for _, attestor := range rule.RequireAttestationsBy {
			if err := upsertIfScanned(st, scannedAttestors, fromID, "gcp", p.ID, TypeBinAuthAttestor, attestor, store.RelUses); err != nil {
				return fmt.Errorf("upsert policy→attestor: %w", err)
			}
		}
		return nil
	}

	for _, pol := range policies {
		var a struct {
			DefaultAdmissionRule  binAuthAdmissionRule            `json:"defaultAdmissionRule"`
			ClusterAdmissionRules map[string]binAuthAdmissionRule `json:"clusterAdmissionRules"`
		}
		if err := json.Unmarshal([]byte(pol.AttributesJSON), &a); err != nil {
			continue
		}
		if err := emitAttestors(pol.ID, a.DefaultAdmissionRule); err != nil {
			return err
		}
		for clusterSpec, rule := range a.ClusterAdmissionRules {
			if err := emitAttestors(pol.ID, rule); err != nil {
				return err
			}
			location, clusterID, ok := strings.Cut(clusterSpec, ".")
			if !ok {
				continue
			}
			toID, ok := clusterByRegionName[location+"."+clusterID]
			if !ok {
				continue
			}
			if err := st.UpsertRelationship(pol.ID, toID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert policy→gkeCluster: %w", err)
			}
		}
	}
	return nil
}
