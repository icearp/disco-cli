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
