package azure

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveDatabaseToElasticPool)
	registerResolver(resolveReplicationLinkToPartner)
	registerResolver(resolveFailoverGroupToPartnerServer)
	registerResolver(resolveSyncGroupToSyncAgent)
}

// resolveDatabaseToElasticPool adds a uses edge from each database that belongs
// to an elastic pool, derived from properties.elasticPoolId in the database's
// stored attributes JSON.
func resolveDatabaseToElasticPool(sub *subscription, st *store.Store) error {
	dbs, err := st.ListResources(store.ResourceFilter{
		Provider:  "azure",
		AccountID: sub.ID,
		Types:     []string{TypeSQLDatabase},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}

	var attrs struct {
		Properties *struct {
			ElasticPoolID *string `json:"elasticPoolId"`
		} `json:"properties"`
	}

	for _, r := range dbs {
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Properties == nil || attrs.Properties.ElasticPoolID == nil {
			continue
		}
		poolID := store.ResourceID("azure", sub.ID, TypeSQLElasticPool, *attrs.Properties.ElasticPoolID)
		if err := st.UpsertRelationship(r.ID, poolID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert database→elasticPool relationship: %w", err)
		}
	}
	return nil
}

// resolveReplicationLinkToPartner adds a peer edge from each replication link
// to the partner database, derived from properties.partnerDatabase and
// properties.partnerServer in the link's stored attributes JSON.
func resolveReplicationLinkToPartner(sub *subscription, st *store.Store) error {
	links, err := st.ListResources(store.ResourceFilter{
		Provider:  "azure",
		AccountID: sub.ID,
		Types:     []string{TypeSQLReplicationLink},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}

	var attrs struct {
		Properties *struct {
			PartnerServer   *string `json:"partnerServer"`
			PartnerDatabase *string `json:"partnerDatabase"`
		} `json:"properties"`
	}

	for _, r := range links {
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Properties == nil || attrs.Properties.PartnerDatabase == nil || attrs.Properties.PartnerServer == nil {
			continue
		}
		// Construct the partner database native ID from its ARM path convention.
		// The native ID is the ARM resource ID of the partner database:
		// /subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.Sql/servers/{server}/databases/{db}
		// We don't know the partner RG from attrs alone, so we derive it by
		// looking up any database named partnerDatabase on partnerServer.
		partnerNativeID := partnerDBNativeID(r.NativeID, *attrs.Properties.PartnerServer, *attrs.Properties.PartnerDatabase)
		if partnerNativeID == "" {
			continue
		}
		partnerID := store.ResourceID("azure", sub.ID, TypeSQLDatabase, partnerNativeID)
		if err := st.UpsertRelationship(r.ID, partnerID, store.RelPeer, "undirected", nil); err != nil {
			return fmt.Errorf("upsert replicationLink→partnerDatabase relationship: %w", err)
		}
	}
	return nil
}

// resolveFailoverGroupToPartnerServer adds a peer edge from each failover group
// to each of its partner servers, derived from properties.partnerServers[].id
// in the failover group's stored attributes JSON.
func resolveFailoverGroupToPartnerServer(sub *subscription, st *store.Store) error {
	groups, err := st.ListResources(store.ResourceFilter{
		Provider:  "azure",
		AccountID: sub.ID,
		Types:     []string{TypeSQLFailoverGroup},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}

	var attrs struct {
		Properties *struct {
			PartnerServers []struct {
				ID *string `json:"id"`
			} `json:"partnerServers"`
		} `json:"properties"`
	}

	for _, r := range groups {
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Properties == nil {
			continue
		}
		for _, ps := range attrs.Properties.PartnerServers {
			if ps.ID == nil {
				continue
			}
			partnerServerID := store.ResourceID("azure", sub.ID, TypeSQLServer, *ps.ID)
			if err := st.UpsertRelationship(r.ID, partnerServerID, store.RelPeer, "undirected", nil); err != nil {
				return fmt.Errorf("upsert failoverGroup→partnerServer relationship: %w", err)
			}
		}
	}
	return nil
}

// resolveSyncGroupToSyncAgent adds a uses edge from each sync group to the
// sync agent it uses, derived from properties.syncAgentId in the sync group's
// stored attributes JSON.
func resolveSyncGroupToSyncAgent(sub *subscription, st *store.Store) error {
	groups, err := st.ListResources(store.ResourceFilter{
		Provider:  "azure",
		AccountID: sub.ID,
		Types:     []string{TypeSQLSyncGroup},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}

	var attrs struct {
		Properties *struct {
			SyncAgentID *string `json:"syncAgentId"`
		} `json:"properties"`
	}

	for _, r := range groups {
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Properties == nil || attrs.Properties.SyncAgentID == nil {
			continue
		}
		agentID := store.ResourceID("azure", sub.ID, TypeSQLSyncAgent, *attrs.Properties.SyncAgentID)
		if err := st.UpsertRelationship(r.ID, agentID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert syncGroup→syncAgent relationship: %w", err)
		}
	}
	return nil
}

// partnerDBNativeID reconstructs the native ARM ID of a partner database from a
// replication link's own native ID, replacing the server and database name segments.
// Returns "" if the link native ID doesn't have the expected structure.
//
// Link native ID format:
//
//	/subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.Sql/servers/{server}/databases/{db}/replicationLinks/{id}
//
// Partner native ID format:
//
//	/subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.Sql/servers/{partnerServer}/databases/{partnerDB}
func partnerDBNativeID(linkNativeID, partnerServer, partnerDB string) string {
	lower := strings.ToLower(linkNativeID)
	idx := strings.Index(lower, "/providers/microsoft.sql/servers/")
	if idx < 0 {
		return ""
	}
	// Keep everything up to and including "/providers/microsoft.sql/"
	prefix := linkNativeID[:idx+len("/providers/microsoft.sql/")]
	return prefix + "servers/" + partnerServer + "/databases/" + partnerDB
}
