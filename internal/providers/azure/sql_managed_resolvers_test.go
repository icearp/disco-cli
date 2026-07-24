package azure

import (
	"testing"

	"github.com/icearp/disco-cli/store"
)

// ----- resolveMIToSubnet -----

func TestResolveMIToSubnet(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(subID)

	subnetNativeID := "/subscriptions/" + subID + "/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/mi-subnet"
	miNativeID := "/subscriptions/" + subID + "/resourceGroups/rg/providers/Microsoft.Sql/managedInstances/mi1"

	subnetID := upsertTestResource(t, st, "azure", sub.ID, TypeNetworkSubnet, subnetNativeID, "eastus", "{}")
	miID := upsertTestResource(t, st, "azure", sub.ID, TypeSQLManagedInstance, miNativeID, "eastus",
		`{"properties":{"subnetId":"`+subnetNativeID+`"}}`)

	if err := resolveMIToSubnet(sub, st); err != nil {
		t.Fatalf("resolveMIToSubnet: %v", err)
	}

	rels, err := st.RelationshipsFrom(miID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	if rels[0].ToID != subnetID || rels[0].Kind != store.RelUses {
		t.Errorf("expected mi -[uses]-> subnet, got %+v", rels[0])
	}
}

func TestResolveMIToSubnet_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(subID)

	upsertTestResource(t, st, "azure", sub.ID, TypeSQLManagedInstance,
		"/subscriptions/"+subID+"/resourceGroups/rg/providers/Microsoft.Sql/managedInstances/mi1",
		"eastus", "{}")

	if err := resolveMIToSubnet(sub, st); err != nil {
		t.Fatalf("resolveMIToSubnet (empty): %v", err)
	}
}

// ----- resolveMIEncryptionProtectorToKey -----

func TestResolveMIEncryptionProtectorToKey(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(subID)

	keyName := "mykey"
	keyNativeID := "/subscriptions/" + subID + "/resourceGroups/rg/providers/Microsoft.Sql/managedInstances/mi1/keys/" + keyName
	epNativeID := "/subscriptions/" + subID + "/resourceGroups/rg/providers/Microsoft.Sql/managedInstances/mi1/encryptionProtector/current"

	keyID := upsertTestResource(t, st, "azure", sub.ID, TypeSQLManagedInstanceKey, keyNativeID, "eastus", "{}")
	epID := upsertTestResource(t, st, "azure", sub.ID, TypeSQLManagedInstanceEP, epNativeID, "eastus",
		`{"properties":{"serverKeyName":"`+keyName+`"}}`)

	if err := resolveMIEncryptionProtectorToKey(sub, st); err != nil {
		t.Fatalf("resolveMIEncryptionProtectorToKey: %v", err)
	}

	rels, err := st.RelationshipsFrom(epID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	if rels[0].ToID != keyID || rels[0].Kind != store.RelUses {
		t.Errorf("expected ep -[uses]-> key, got %+v", rels[0])
	}
}

func TestResolveMIEncryptionProtectorToKey_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(subID)

	upsertTestResource(t, st, "azure", sub.ID, TypeSQLManagedInstanceEP,
		"/subscriptions/"+subID+"/resourceGroups/rg/providers/Microsoft.Sql/managedInstances/mi1/encryptionProtector/current",
		"eastus", "{}")

	if err := resolveMIEncryptionProtectorToKey(sub, st); err != nil {
		t.Fatalf("resolveMIEncryptionProtectorToKey (empty): %v", err)
	}
}

// ----- resolveManagedDatabaseToSource -----

func TestResolveManagedDatabaseToSource(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(subID)

	srcNativeID := "/subscriptions/" + subID + "/resourceGroups/rg/providers/Microsoft.Sql/managedInstances/mi1/databases/src"
	mdbNativeID := "/subscriptions/" + subID + "/resourceGroups/rg/providers/Microsoft.Sql/managedInstances/mi1/databases/restored"

	srcID := upsertTestResource(t, st, "azure", sub.ID, TypeSQLManagedDatabase, srcNativeID, "eastus", "{}")
	mdbID := upsertTestResource(t, st, "azure", sub.ID, TypeSQLManagedDatabase, mdbNativeID, "eastus",
		`{"properties":{"sourceDatabaseId":"`+srcNativeID+`"}}`)

	if err := resolveManagedDatabaseToSource(sub, st); err != nil {
		t.Fatalf("resolveManagedDatabaseToSource: %v", err)
	}

	rels, err := st.RelationshipsFrom(mdbID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	if rels[0].ToID != srcID || rels[0].Kind != store.RelUses {
		t.Errorf("expected mdb -[uses]-> srcMDB, got %+v", rels[0])
	}
}

func TestResolveManagedDatabaseToSource_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(subID)

	upsertTestResource(t, st, "azure", sub.ID, TypeSQLManagedDatabase,
		"/subscriptions/"+subID+"/resourceGroups/rg/providers/Microsoft.Sql/managedInstances/mi1/databases/db1",
		"eastus", "{}")

	if err := resolveManagedDatabaseToSource(sub, st); err != nil {
		t.Fatalf("resolveManagedDatabaseToSource (empty): %v", err)
	}
}

// ----- resolveServerEncryptionProtectorToKey (server side, lives in sql_resolvers.go but shares logic) -----

func TestResolveServerEncryptionProtectorToKey(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(subID)

	keyName := "srvkey"
	keyNativeID := "/subscriptions/" + subID + "/resourceGroups/rg/providers/Microsoft.Sql/servers/srv/keys/" + keyName
	epNativeID := "/subscriptions/" + subID + "/resourceGroups/rg/providers/Microsoft.Sql/servers/srv/encryptionProtector/current"

	keyID := upsertTestResource(t, st, "azure", sub.ID, TypeSQLServerKey, keyNativeID, "eastus", "{}")
	epID := upsertTestResource(t, st, "azure", sub.ID, TypeSQLEncryptionProtector, epNativeID, "eastus",
		`{"properties":{"serverKeyName":"`+keyName+`"}}`)

	if err := resolveServerEncryptionProtectorToKey(sub, st); err != nil {
		t.Fatalf("resolveServerEncryptionProtectorToKey: %v", err)
	}

	rels, err := st.RelationshipsFrom(epID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	if rels[0].ToID != keyID || rels[0].Kind != store.RelUses {
		t.Errorf("expected ep -[uses]-> key, got %+v", rels[0])
	}
}

// ----- epToKeyNativeID -----

func TestEPToKeyNativeID(t *testing.T) {
	cases := []struct {
		name    string
		epID    string
		keyName string
		want    string
	}{
		{
			"server",
			"/subscriptions/s/resourceGroups/rg/providers/Microsoft.Sql/servers/srv/encryptionProtector/current",
			"k1",
			"/subscriptions/s/resourceGroups/rg/providers/Microsoft.Sql/servers/srv/keys/k1",
		},
		{
			"managedInstance",
			"/subscriptions/s/resourceGroups/rg/providers/Microsoft.Sql/managedInstances/mi/encryptionProtector/current",
			"k2",
			"/subscriptions/s/resourceGroups/rg/providers/Microsoft.Sql/managedInstances/mi/keys/k2",
		},
		{"invalid", "not-an-arm-id", "k", ""},
	}
	for _, tc := range cases {
		got := epToKeyNativeID(tc.epID, tc.keyName)
		if got != tc.want {
			t.Errorf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
}
