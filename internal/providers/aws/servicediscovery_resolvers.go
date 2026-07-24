package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveServiceDiscoveryServiceNamespace,
		EdgeDecl{TypeServiceDiscoveryService, TypeServiceDiscoveryHTTPNamespace, store.RelAttachedTo},
		EdgeDecl{TypeServiceDiscoveryService, TypeServiceDiscoveryPrivateDNSNamespace, store.RelAttachedTo},
		EdgeDecl{TypeServiceDiscoveryService, TypeServiceDiscoveryPublicDNSNamespace, store.RelAttachedTo},
	)
	registerResolver(
		resolveServiceDiscoveryNamespaceHostedZone,
		EdgeDecl{TypeServiceDiscoveryPrivateDNSNamespace, TypeRoute53HostedZone, store.RelUses},
		EdgeDecl{TypeServiceDiscoveryPublicDNSNamespace, TypeRoute53HostedZone, store.RelUses},
	)
	registerResolver(
		resolveServiceDiscoveryInstanceService,
		EdgeDecl{TypeServiceDiscoveryInstance, TypeServiceDiscoveryService, store.RelAttachedTo},
	)
}

// sdNamespaceARN rebuilds a Cloud Map namespace ARN from its raw ID.
func sdNamespaceARN(region, acct, nsID string) string {
	return fmt.Sprintf("arn:aws:servicediscovery:%s:%s:namespace/%s", region, acct, nsID)
}

// resolveServiceDiscoveryServiceNamespace wires each Cloud Map service to
// its parent namespace via DNSConfig.NamespaceId (ServiceSummary has no
// top-level NamespaceId — the field is on DNSConfig). FK-safe across all
// three namespace flavours.
func resolveServiceDiscoveryServiceNamespace(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeServiceDiscoveryService},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	nsTypes := []string{
		TypeServiceDiscoveryHTTPNamespace,
		TypeServiceDiscoveryPrivateDNSNamespace,
		TypeServiceDiscoveryPublicDNSNamespace,
	}
	nsSets := map[string]map[string]bool{}
	for _, t := range nsTypes {
		set, err := scannedIDSet(acct, st, t)
		if err != nil {
			return err
		}
		nsSets[t] = set
	}
	for _, r := range rows {
		var attrs struct {
			DNSConfig *struct {
				NamespaceID *string `json:"NamespaceId"`
			} `json:"DnsConfig"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs.DNSConfig == nil {
			continue
		}
		nsID := sv(attrs.DNSConfig.NamespaceID)
		if nsID == "" {
			continue
		}
		nsARN := sdNamespaceARN(sv(r.Region), acct.ID, nsID)
		for _, t := range nsTypes {
			tgt := store.ResourceID("aws", acct.ID, nsARN)
			if !nsSets[t][tgt] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert servicediscovery service→%s: %w", t, err)
			}
			break
		}
	}
	return nil
}

// resolveServiceDiscoveryNamespaceHostedZone wires private/public DNS
// namespaces to the Route 53 hosted zone Cloud Map auto-creates
// (Properties.DNSProperties.HostedZoneId). HTTP namespaces have no zone.
func resolveServiceDiscoveryNamespaceHostedZone(acct *account, st *store.Store) error {
	hzSet, err := scannedIDSet(acct, st, TypeRoute53HostedZone)
	if err != nil {
		return err
	}
	for _, ttyp := range []string{TypeServiceDiscoveryPrivateDNSNamespace, TypeServiceDiscoveryPublicDNSNamespace} {
		rows, err := st.ListResources(store.ResourceFilter{
			Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{ttyp},
			Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			var attrs struct {
				Properties *struct {
					DNSProperties *struct {
						HostedZoneID *string `json:"HostedZoneId"`
					} `json:"DnsProperties"`
				} `json:"Properties"`
			}
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil ||
				attrs.Properties == nil || attrs.Properties.DNSProperties == nil {
				continue
			}
			hz := sv(attrs.Properties.DNSProperties.HostedZoneID)
			if hz == "" {
				continue
			}
			// Route53 hosted-zone NativeID = `arn:aws:route53:::hostedzone/{id}`.
			// HostedZoneId may carry `/hostedzone/` prefix; strip.
			hz = strings.TrimPrefix(hz, "/hostedzone/")
			hzARN := "arn:aws:route53:::hostedzone/" + hz
			tgt := store.ResourceID("aws", acct.ID, hzARN)
			if !hzSet[tgt] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert servicediscovery %s→hosted-zone: %w", ttyp, err)
			}
		}
	}
	return nil
}

// resolveServiceDiscoveryInstanceService wires each Cloud Map instance to
// its parent service via the synthetic NativeID shape
// `arn:aws:servicediscovery:r:a:service/{sid}/instance/{iid}`.
func resolveServiceDiscoveryInstanceService(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeServiceDiscoveryInstance},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	svcSet, err := scannedIDSet(acct, st, TypeServiceDiscoveryService)
	if err != nil {
		return err
	}
	for _, r := range rows {
		i := strings.Index(r.NativeID, "/instance/")
		if i < 0 {
			continue
		}
		parent := r.NativeID[:i]
		tgt := store.ResourceID("aws", acct.ID, parent)
		if !svcSet[tgt] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert servicediscovery instance→service: %w", err)
		}
	}
	return nil
}
