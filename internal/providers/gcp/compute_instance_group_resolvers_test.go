package gcp

import (
	"testing"

	compute "google.golang.org/api/compute/v1"
)

func TestResolveInstanceGroupRelationships_NetworkSubnet(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	netID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeNetwork, "projects/proj-1/global/networks/net-1", "",
		marshalAttrs(t, &compute.Network{SelfLink: "projects/proj-1/global/networks/net-1", Name: "net-1"}))
	subnetID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeSubnet, "projects/proj-1/regions/us-central1/subnetworks/sub-1", "us-central1",
		marshalAttrs(t, &compute.Subnetwork{SelfLink: "projects/proj-1/regions/us-central1/subnetworks/sub-1", Name: "sub-1"}))

	groupID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeInstanceGroup, "projects/proj-1/zones/us-central1-a/instanceGroups/ig-1", "us-central1-a",
		marshalAttrs(t, &compute.InstanceGroup{
			SelfLink:   "projects/proj-1/zones/us-central1-a/instanceGroups/ig-1",
			Name:       "ig-1",
			Network:    "projects/proj-1/global/networks/net-1",
			Subnetwork: "projects/proj-1/regions/us-central1/subnetworks/sub-1",
		}))

	if err := resolveInstanceGroupRelationships(p, st); err != nil {
		t.Fatalf("resolveInstanceGroupRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(groupID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	want := map[string]bool{netID: false, subnetID: false}
	for _, r := range rels {
		if r.Kind != "attached-to" {
			t.Errorf("unexpected edge kind %q", r.Kind)
			continue
		}
		if _, ok := want[r.ToID]; ok {
			want[r.ToID] = true
		}
	}
	for id, seen := range want {
		if !seen {
			t.Errorf("missing edge %s -[attached-to]-> %s", groupID, id)
		}
	}
}

func TestResolveInstanceGroupRelationships_UnscannedTargetSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	groupID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeInstanceGroup, "projects/proj-1/zones/us-central1-a/instanceGroups/ig-1", "us-central1-a",
		marshalAttrs(t, &compute.InstanceGroup{
			SelfLink: "projects/proj-1/zones/us-central1-a/instanceGroups/ig-1",
			Name:     "ig-1",
			Network:  "projects/other-proj/global/networks/not-scanned",
		}))

	if err := resolveInstanceGroupRelationships(p, st); err != nil {
		t.Fatalf("resolveInstanceGroupRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(groupID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edges for unscanned network reference, got %+v", rels)
	}
}

func TestResolveInstanceGroupRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")
	if err := resolveInstanceGroupRelationships(p, st); err != nil {
		t.Fatalf("resolveInstanceGroupRelationships on empty project: %v", err)
	}
}

func TestResolveInstanceTemplateRelationships_FullChain(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	netID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeNetwork, "projects/proj-1/global/networks/net-1", "",
		marshalAttrs(t, &compute.Network{SelfLink: "projects/proj-1/global/networks/net-1", Name: "net-1"}))
	imageID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeImage, "projects/proj-1/global/images/img-1", "",
		marshalAttrs(t, &compute.Image{SelfLink: "projects/proj-1/global/images/img-1", Name: "img-1"}))
	rpID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeResourcePolicy, "projects/proj-1/regions/us-central1/resourcePolicies/rp-1", "us-central1",
		marshalAttrs(t, map[string]string{"selfLink": "projects/proj-1/regions/us-central1/resourcePolicies/rp-1"}))
	saID := upsertTestResource(t, st, "gcp", p.ID, TypeIAMServiceAccount, "projects/proj-1/serviceAccounts/sa-1@proj-1.iam.gserviceaccount.com", "",
		marshalAttrs(t, map[string]string{"email": "sa-1@proj-1.iam.gserviceaccount.com"}))

	tplID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeInstanceTemplate, "projects/proj-1/global/instanceTemplates/tpl-1", "",
		marshalAttrs(t, &compute.InstanceTemplate{
			SelfLink: "projects/proj-1/global/instanceTemplates/tpl-1",
			Name:     "tpl-1",
			Properties: &compute.InstanceProperties{
				NetworkInterfaces: []*compute.NetworkInterface{{Network: "projects/proj-1/global/networks/net-1"}},
				Disks: []*compute.AttachedDisk{{
					InitializeParams: &compute.AttachedDiskInitializeParams{SourceImage: "projects/proj-1/global/images/img-1"},
				}},
				// ResourcePolicies is a bare name on InstanceProperties, not a
				// self-link like every other field here — matches the real API.
				ResourcePolicies: []string{"rp-1"},
				ServiceAccounts:  []*compute.ServiceAccount{{Email: "sa-1@proj-1.iam.gserviceaccount.com"}},
			},
		}))

	if err := resolveInstanceTemplateRelationships(p, st); err != nil {
		t.Fatalf("resolveInstanceTemplateRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(tplID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	want := map[string]string{netID: "attached-to", imageID: "attached-to", rpID: "uses", saID: "uses"}
	got := map[string]string{}
	for _, r := range rels {
		got[r.ToID] = r.Kind
	}
	for id, kind := range want {
		if got[id] != kind {
			t.Errorf("edge %s -[%s]-> %s missing (got kind %q)", tplID, kind, id, got[id])
		}
	}
	if len(rels) != len(want) {
		t.Errorf("want exactly %d edges, got %d: %+v", len(want), len(rels), rels)
	}
}

func TestResolveInstanceTemplateRelationships_UnscannedTargetsSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	tplID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeInstanceTemplate, "projects/proj-1/global/instanceTemplates/tpl-1", "",
		marshalAttrs(t, &compute.InstanceTemplate{
			SelfLink: "projects/proj-1/global/instanceTemplates/tpl-1",
			Name:     "tpl-1",
			Properties: &compute.InstanceProperties{
				NetworkInterfaces: []*compute.NetworkInterface{{Network: "projects/other-proj/global/networks/not-scanned"}},
				ResourcePolicies:  []string{"not-scanned-rp"},
				ServiceAccounts:   []*compute.ServiceAccount{{Email: "not-scanned@other-proj.iam.gserviceaccount.com"}},
			},
		}))

	if err := resolveInstanceTemplateRelationships(p, st); err != nil {
		t.Fatalf("resolveInstanceTemplateRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(tplID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edges when every reference target is unscanned, got %+v", rels)
	}
}

func TestResolveInstanceTemplateRelationships_NoPropertiesNoPanic(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	tplID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeInstanceTemplate, "projects/proj-1/global/instanceTemplates/tpl-1", "",
		marshalAttrs(t, &compute.InstanceTemplate{SelfLink: "projects/proj-1/global/instanceTemplates/tpl-1", Name: "tpl-1"}))

	if err := resolveInstanceTemplateRelationships(p, st); err != nil {
		t.Fatalf("resolveInstanceTemplateRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(tplID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edges for template with nil Properties, got %+v", rels)
	}
}
