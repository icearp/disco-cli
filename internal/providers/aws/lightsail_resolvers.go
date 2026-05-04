package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveLightsailDatabaseSnapshotParent,
		EdgeDecl{TypeLightsailDatabaseSnapshot, TypeLightsailDatabase, store.RelAttachedTo},
	)
	registerResolver(resolveLightsailDiskSnapshotParent,
		EdgeDecl{TypeLightsailDiskSnapshot, TypeLightsailDisk, store.RelAttachedTo},
	)
	registerResolver(resolveLightsailInstanceSnapshotParent,
		EdgeDecl{TypeLightsailInstanceSnapshot, TypeLightsailInstance, store.RelAttachedTo},
	)
	registerResolver(resolveLightsailLoadBalancerTlsCertParent,
		EdgeDecl{TypeLightsailLoadBalancerTlsCertificate, TypeLightsailLoadBalancer, store.RelAttachedTo},
	)
	registerResolver(resolveLightsailAlarmTarget,
		EdgeDecl{TypeLightsailAlarm, TypeLightsailInstance, store.RelUses},
		EdgeDecl{TypeLightsailAlarm, TypeLightsailDatabase, store.RelUses},
		EdgeDecl{TypeLightsailAlarm, TypeLightsailLoadBalancer, store.RelUses},
	)
	registerResolver(resolveLightsailInstanceDisks,
		EdgeDecl{TypeLightsailInstance, TypeLightsailDisk, store.RelAttachedTo},
	)
	registerResolver(resolveLightsailDiskAttachedInstance,
		EdgeDecl{TypeLightsailDisk, TypeLightsailInstance, store.RelAttachedTo},
	)
	registerResolver(resolveLightsailStaticIPAttachedInstance,
		EdgeDecl{TypeLightsailStaticIp, TypeLightsailInstance, store.RelAttachedTo},
	)
	registerResolver(resolveLightsailLoadBalancerRefs,
		EdgeDecl{TypeLightsailLoadBalancer, TypeLightsailInstance, store.RelAttachedTo},
		EdgeDecl{TypeLightsailLoadBalancer, TypeLightsailCertificate, store.RelUses},
	)
	registerResolver(resolveLightsailDistributionOrigin,
		EdgeDecl{TypeLightsailDistribution, TypeLightsailInstance, store.RelUses},
		EdgeDecl{TypeLightsailDistribution, TypeLightsailLoadBalancer, store.RelUses},
		EdgeDecl{TypeLightsailDistribution, TypeLightsailContainerService, store.RelUses},
		EdgeDecl{TypeLightsailDistribution, TypeLightsailBucket, store.RelUses},
		EdgeDecl{TypeLightsailDistribution, TypeLightsailCertificate, store.RelUses},
	)
	registerResolver(resolveLightsailCertificateDomain,
		EdgeDecl{TypeLightsailCertificate, TypeLightsailDomain, store.RelUses},
	)
}

// lightsailByNameIndex builds a per-(region, name) → resource-id map for the
// given Lightsail resource type. Lightsail cross-references are bare names;
// the region pins which row to resolve to since names aren't globally unique.
func lightsailByNameIndex(acct *account, st *store.Store, rtype string) (map[string]string, error) {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{rtype},
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	idx := make(map[string]string, len(rows))
	for _, r := range rows {
		name := sv(r.Name)
		if name == "" {
			continue
		}
		idx[sv(r.Region)+"|"+name] = r.ID
	}
	return idx, nil
}

// resolveLightsailParentByNameField is the shared shape for snapshot →
// parent-by-name resolvers. fieldName is the JSON key carrying the parent
// resource's bare Name.
func resolveLightsailParentByNameField(acct *account, st *store.Store, ctype, parentType, fieldName, label string) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{ctype},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	parentIdx, err := lightsailByNameIndex(acct, st, parentType)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs map[string]json.RawMessage
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		raw, ok := attrs[fieldName]
		if !ok {
			continue
		}
		var name string
		if err := json.Unmarshal(raw, &name); err != nil || name == "" {
			continue
		}
		tgtID, ok := parentIdx[sv(r.Region)+"|"+name]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert lightsail-%s→%s: %w", label, parentType, err)
		}
	}
	return nil
}

func resolveLightsailDatabaseSnapshotParent(acct *account, st *store.Store) error {
	return resolveLightsailParentByNameField(acct, st, TypeLightsailDatabaseSnapshot, TypeLightsailDatabase, "FromRelationalDatabaseName", "db-snapshot")
}

func resolveLightsailDiskSnapshotParent(acct *account, st *store.Store) error {
	return resolveLightsailParentByNameField(acct, st, TypeLightsailDiskSnapshot, TypeLightsailDisk, "FromDiskName", "disk-snapshot")
}

func resolveLightsailInstanceSnapshotParent(acct *account, st *store.Store) error {
	return resolveLightsailParentByNameField(acct, st, TypeLightsailInstanceSnapshot, TypeLightsailInstance, "FromInstanceName", "instance-snapshot")
}

func resolveLightsailLoadBalancerTlsCertParent(acct *account, st *store.Store) error {
	return resolveLightsailParentByNameField(acct, st, TypeLightsailLoadBalancerTlsCertificate, TypeLightsailLoadBalancer, "LoadBalancerName", "lb-tls-cert")
}

// resolveLightsailAlarmTarget walks each alarm's MonitoredResourceInfo
// (`{Name, ResourceType}`) and emits uses → instance / database / load-balancer
// based on ResourceType.
func resolveLightsailAlarmTarget(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeLightsailAlarm},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	instIdx, err := lightsailByNameIndex(acct, st, TypeLightsailInstance)
	if err != nil {
		return err
	}
	dbIdx, err := lightsailByNameIndex(acct, st, TypeLightsailDatabase)
	if err != nil {
		return err
	}
	lbIdx, err := lightsailByNameIndex(acct, st, TypeLightsailLoadBalancer)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			MonitoredResourceInfo *struct {
				Name         *string `json:"Name"`
				ResourceType *string `json:"ResourceType"`
			} `json:"MonitoredResourceInfo"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.MonitoredResourceInfo == nil {
			continue
		}
		name := sv(attrs.MonitoredResourceInfo.Name)
		rtype := sv(attrs.MonitoredResourceInfo.ResourceType)
		if name == "" || rtype == "" {
			continue
		}
		key := sv(r.Region) + "|" + name
		var tgtID string
		switch rtype {
		case "Instance":
			tgtID = instIdx[key]
		case "RelationalDatabase":
			tgtID = dbIdx[key]
		case "LoadBalancer":
			tgtID = lbIdx[key]
		}
		if tgtID == "" {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert lightsail-alarm→%s: %w", rtype, err)
		}
	}
	return nil
}

// resolveLightsailInstanceDisks links each instance to the additional block
// disks listed in Hardware.Disks. The boot disk has IsSystemDisk=true and is
// excluded — it's the same physical row as the instance.
func resolveLightsailInstanceDisks(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeLightsailInstance},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	diskIdx, err := lightsailByNameIndex(acct, st, TypeLightsailDisk)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			Hardware *struct {
				Disks []struct {
					Name         *string `json:"name"`
					NamePascal   *string `json:"Name"`
					IsSystemDisk *bool   `json:"isSystemDisk"`
				} `json:"Disks"`
			} `json:"Hardware"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Hardware == nil {
			continue
		}
		region := sv(r.Region)
		for _, d := range attrs.Hardware.Disks {
			name := sv(d.NamePascal)
			if name == "" {
				name = sv(d.Name)
			}
			if name == "" {
				continue
			}
			tgtID, ok := diskIdx[region+"|"+name]
			if !ok {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert lightsail-instance→disk: %w", err)
			}
		}
	}
	return nil
}

// resolveLightsailDiskAttachedInstance reads each disk's AttachedTo (instance
// name) and emits attached-to → instance.
func resolveLightsailDiskAttachedInstance(acct *account, st *store.Store) error {
	return resolveLightsailParentByNameField(acct, st, TypeLightsailDisk, TypeLightsailInstance, "AttachedTo", "disk")
}

// resolveLightsailStaticIPAttachedInstance reads each static-ip's AttachedTo
// (instance name) and emits attached-to → instance.
func resolveLightsailStaticIPAttachedInstance(acct *account, st *store.Store) error {
	return resolveLightsailParentByNameField(acct, st, TypeLightsailStaticIp, TypeLightsailInstance, "AttachedTo", "static-ip")
}

// resolveLightsailLoadBalancerRefs walks each load balancer's
// InstanceHealthSummary[] (registered instances) and TlsCertificateSummaries[]
// (attached SSL/TLS certs) and emits the corresponding edges.
func resolveLightsailLoadBalancerRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeLightsailLoadBalancer},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	instIdx, err := lightsailByNameIndex(acct, st, TypeLightsailInstance)
	if err != nil {
		return err
	}
	certIdx, err := lightsailByNameIndex(acct, st, TypeLightsailCertificate)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			InstanceHealthSummary []struct {
				InstanceName *string `json:"InstanceName"`
			} `json:"InstanceHealthSummary"`
			TlsCertificateSummaries []struct {
				Name       *string `json:"Name"`
				IsAttached *bool   `json:"IsAttached"`
			} `json:"TlsCertificateSummaries"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		for _, ih := range attrs.InstanceHealthSummary {
			name := sv(ih.InstanceName)
			if name == "" {
				continue
			}
			tgtID, ok := instIdx[region+"|"+name]
			if !ok {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert lightsail-lb→instance: %w", err)
			}
		}
		for _, tc := range attrs.TlsCertificateSummaries {
			if tc.IsAttached == nil || !*tc.IsAttached {
				continue
			}
			name := sv(tc.Name)
			if name == "" {
				continue
			}
			tgtID, ok := certIdx[region+"|"+name]
			if !ok {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert lightsail-lb→certificate: %w", err)
			}
		}
	}
	return nil
}

// resolveLightsailDistributionOrigin walks each distribution's Origin and
// CertificateName and emits uses → instance / load-balancer / container-service
// / bucket (origin) plus uses → certificate. Distributions are us-east-1
// global resources but Origin.RegionName tells where the backend lives.
func resolveLightsailDistributionOrigin(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeLightsailDistribution},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	instIdx, err := lightsailByNameIndex(acct, st, TypeLightsailInstance)
	if err != nil {
		return err
	}
	lbIdx, err := lightsailByNameIndex(acct, st, TypeLightsailLoadBalancer)
	if err != nil {
		return err
	}
	csIdx, err := lightsailByNameIndex(acct, st, TypeLightsailContainerService)
	if err != nil {
		return err
	}
	bucketIdx, err := lightsailByNameIndex(acct, st, TypeLightsailBucket)
	if err != nil {
		return err
	}
	certIdx, err := lightsailByNameIndex(acct, st, TypeLightsailCertificate)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			Origin *struct {
				Name         *string `json:"Name"`
				ResourceType *string `json:"ResourceType"`
				RegionName   *string `json:"RegionName"`
			} `json:"Origin"`
			CertificateName *string `json:"CertificateName"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		distRegion := sv(r.Region)
		if attrs.Origin != nil {
			name := sv(attrs.Origin.Name)
			region := sv(attrs.Origin.RegionName)
			if region == "" {
				region = distRegion
			}
			key := region + "|" + name
			var tgtID string
			switch sv(attrs.Origin.ResourceType) {
			case "Instance":
				tgtID = instIdx[key]
			case "LoadBalancer":
				tgtID = lbIdx[key]
			case "ContainerService":
				tgtID = csIdx[key]
			case "Bucket":
				tgtID = bucketIdx[key]
			}
			if name != "" && tgtID != "" {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert lightsail-distribution→origin: %w", err)
				}
			}
		}
		if cname := sv(attrs.CertificateName); cname != "" {
			tgtID, ok := certIdx[distRegion+"|"+cname]
			if ok {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert lightsail-distribution→cert: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveLightsailCertificateDomain links each Lightsail certificate to the
// Lightsail-managed domain matching its DomainName, when one is registered.
func resolveLightsailCertificateDomain(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeLightsailCertificate},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	domainIdx, err := lightsailByNameIndex(acct, st, TypeLightsailDomain)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			DomainName *string `json:"DomainName"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		dn := sv(attrs.DomainName)
		if dn == "" {
			continue
		}
		tgtID := ""
		// Lightsail domains are us-east-1 global; try cert region first then us-east-1.
		if id, ok := domainIdx[sv(r.Region)+"|"+dn]; ok {
			tgtID = id
		} else if id, ok := domainIdx["us-east-1|"+dn]; ok {
			tgtID = id
		}
		if tgtID == "" {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert lightsail-certificate→domain: %w", err)
		}
	}
	return nil
}
