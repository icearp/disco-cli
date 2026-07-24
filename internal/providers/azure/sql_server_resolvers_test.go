package azure

import (
	"testing"

	"github.com/icearp/disco-cli/store"
)

const subID = "sub-sql-test"

// ----- resolveDatabaseToElasticPool -----

func TestResolveDatabaseToElasticPool(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(subID)

	poolNativeID := "/subscriptions/" + subID + "/resourceGroups/rg/providers/Microsoft.Sql/servers/srv/elasticPools/pool1"
	dbNativeID := "/subscriptions/" + subID + "/resourceGroups/rg/providers/Microsoft.Sql/servers/srv/databases/db1"

	poolID := upsertTestResource(t, st, "azure", sub.ID, TypeSQLElasticPool, poolNativeID, "eastus", "{}")
	dbID := upsertTestResource(t, st, "azure", sub.ID, TypeSQLDatabase, dbNativeID, "eastus",
		`{"properties":{"elasticPoolId":"`+poolNativeID+`"}}`)

	if err := resolveDatabaseToElasticPool(sub, st); err != nil {
		t.Fatalf("resolveDatabaseToElasticPool: %v", err)
	}

	rels, err := st.RelationshipsFrom(dbID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	if rels[0].ToID != poolID || rels[0].Kind != store.RelUses {
		t.Errorf("expected db -[uses]-> pool, got %+v", rels[0])
	}
}

func TestResolveDatabaseToElasticPool_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(subID)

	upsertTestResource(t, st, "azure", sub.ID, TypeSQLDatabase,
		"/subscriptions/"+subID+"/resourceGroups/rg/providers/Microsoft.Sql/servers/srv/databases/db1",
		"eastus", "{}")

	if err := resolveDatabaseToElasticPool(sub, st); err != nil {
		t.Fatalf("resolveDatabaseToElasticPool (empty): %v", err)
	}
}

// ----- resolveReplicationLinkToPartner -----

func TestResolveReplicationLinkToPartner(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(subID)

	partnerServer := "srv-partner"
	partnerDB := "db-partner"
	partnerNativeID := "/subscriptions/" + subID + "/resourceGroups/rg/providers/Microsoft.Sql/servers/" + partnerServer + "/databases/" + partnerDB

	linkNativeID := "/subscriptions/" + subID + "/resourceGroups/rg/providers/Microsoft.Sql/servers/srv/databases/db1/replicationLinks/link1"
	dbAttrs := `{"properties":{"partnerServer":"` + partnerServer + `","partnerDatabase":"` + partnerDB + `"}}`

	partnerID := upsertTestResource(t, st, "azure", sub.ID, TypeSQLDatabase, partnerNativeID, "westus", "{}")
	linkID := upsertTestResource(t, st, "azure", sub.ID, TypeSQLReplicationLink, linkNativeID, "eastus", dbAttrs)

	if err := resolveReplicationLinkToPartner(sub, st); err != nil {
		t.Fatalf("resolveReplicationLinkToPartner: %v", err)
	}

	rels, err := st.RelationshipsFrom(linkID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	if rels[0].ToID != partnerID || rels[0].Kind != store.RelPeer {
		t.Errorf("expected link -[peer]-> partnerDB, got %+v", rels[0])
	}
}

func TestResolveReplicationLinkToPartner_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(subID)

	upsertTestResource(t, st, "azure", sub.ID, TypeSQLReplicationLink,
		"/subscriptions/"+subID+"/resourceGroups/rg/providers/Microsoft.Sql/servers/srv/databases/db1/replicationLinks/link1",
		"eastus", "{}")

	if err := resolveReplicationLinkToPartner(sub, st); err != nil {
		t.Fatalf("resolveReplicationLinkToPartner (empty): %v", err)
	}
}

// ----- resolveFailoverGroupToPartnerServer -----

func TestResolveFailoverGroupToPartnerServer(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(subID)

	partnerServerNativeID := "/subscriptions/" + subID + "/resourceGroups/rg/providers/Microsoft.Sql/servers/srv-partner"
	fgNativeID := "/subscriptions/" + subID + "/resourceGroups/rg/providers/Microsoft.Sql/servers/srv/failoverGroups/fg1"
	fgAttrs := `{"properties":{"partnerServers":[{"id":"` + partnerServerNativeID + `"}]}}`

	partnerID := upsertTestResource(t, st, "azure", sub.ID, TypeSQLServer, partnerServerNativeID, "westus", "{}")
	fgID := upsertTestResource(t, st, "azure", sub.ID, TypeSQLFailoverGroup, fgNativeID, "eastus", fgAttrs)

	if err := resolveFailoverGroupToPartnerServer(sub, st); err != nil {
		t.Fatalf("resolveFailoverGroupToPartnerServer: %v", err)
	}

	rels, err := st.RelationshipsFrom(fgID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	if rels[0].ToID != partnerID || rels[0].Kind != store.RelPeer {
		t.Errorf("expected fg -[peer]-> partnerServer, got %+v", rels[0])
	}
}

func TestResolveFailoverGroupToPartnerServer_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(subID)

	upsertTestResource(t, st, "azure", sub.ID, TypeSQLFailoverGroup,
		"/subscriptions/"+subID+"/resourceGroups/rg/providers/Microsoft.Sql/servers/srv/failoverGroups/fg1",
		"eastus", "{}")

	if err := resolveFailoverGroupToPartnerServer(sub, st); err != nil {
		t.Fatalf("resolveFailoverGroupToPartnerServer (empty): %v", err)
	}
}

// ----- resolveSyncGroupToSyncAgent -----

func TestResolveSyncGroupToSyncAgent(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(subID)

	agentNativeID := "/subscriptions/" + subID + "/resourceGroups/rg/providers/Microsoft.Sql/servers/srv/syncAgents/agent1"
	sgNativeID := "/subscriptions/" + subID + "/resourceGroups/rg/providers/Microsoft.Sql/servers/srv/databases/db1/syncGroups/sg1"
	sgAttrs := `{"properties":{"syncAgentId":"` + agentNativeID + `"}}`

	agentID := upsertTestResource(t, st, "azure", sub.ID, TypeSQLSyncAgent, agentNativeID, "eastus", "{}")
	sgID := upsertTestResource(t, st, "azure", sub.ID, TypeSQLSyncGroup, sgNativeID, "eastus", sgAttrs)

	if err := resolveSyncGroupToSyncAgent(sub, st); err != nil {
		t.Fatalf("resolveSyncGroupToSyncAgent: %v", err)
	}

	rels, err := st.RelationshipsFrom(sgID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	if rels[0].ToID != agentID || rels[0].Kind != store.RelUses {
		t.Errorf("expected syncGroup -[uses]-> syncAgent, got %+v", rels[0])
	}
}

func TestResolveSyncGroupToSyncAgent_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(subID)

	upsertTestResource(t, st, "azure", sub.ID, TypeSQLSyncGroup,
		"/subscriptions/"+subID+"/resourceGroups/rg/providers/Microsoft.Sql/servers/srv/databases/db1/syncGroups/sg1",
		"eastus", "{}")

	if err := resolveSyncGroupToSyncAgent(sub, st); err != nil {
		t.Fatalf("resolveSyncGroupToSyncAgent (empty): %v", err)
	}
}

// ----- partnerDBNativeID -----

func TestPartnerDBNativeID(t *testing.T) {
	linkID := "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Sql/servers/srv/databases/db1/replicationLinks/link1"
	want := "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Sql/servers/srv-partner/databases/db-partner"
	got := partnerDBNativeID(linkID, "srv-partner", "db-partner")
	if got != want {
		t.Errorf("partnerDBNativeID:\n  got:  %q\n  want: %q", got, want)
	}
}

func TestPartnerDBNativeID_InvalidID(t *testing.T) {
	got := partnerDBNativeID("not-an-arm-id", "srv", "db")
	if got != "" {
		t.Errorf("partnerDBNativeID(invalid) = %q, want empty", got)
	}
}
