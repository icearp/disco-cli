package gcp

import (
	"testing"

	"codeberg.org/icearp/disco/store"
	cloudbuild "google.golang.org/api/cloudbuild/v1"
	cloudbuildv2 "google.golang.org/api/cloudbuild/v2"
	compute "google.golang.org/api/compute/v1"
	secretmanager "google.golang.org/api/secretmanager/v1"
)

func TestResolveCloudBuildRelationships(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	saEmail := "build-sa@my-project.iam.gserviceaccount.com"
	saNative := "projects/my-project/serviceAccounts/" + saEmail
	saID := upsertTestResource(t, st, "gcp", p.ID, TypeIAMServiceAccount, saNative, "", "{}")

	// Full resource-name form.
	trFull := upsertTestResource(t, st, "gcp", p.ID, TypeCloudBuildTrigger,
		"projects/my-project/locations/global/triggers/abc", "",
		`{"serviceAccount": "`+saNative+`"}`)
	// Email-only form.
	trEmail := upsertTestResource(t, st, "gcp", p.ID, TypeCloudBuildTrigger,
		"projects/my-project/locations/global/triggers/def", "",
		`{"serviceAccount": "`+saEmail+`"}`)
	// Cross-project SA — must be skipped.
	upsertTestResource(t, st, "gcp", p.ID, TypeCloudBuildTrigger,
		"projects/my-project/locations/global/triggers/orphan", "",
		`{"serviceAccount": "other@other-project.iam.gserviceaccount.com"}`)

	if err := resolveCloudBuildRelationships(p, st); err != nil {
		t.Fatalf("resolveCloudBuildRelationships: %v", err)
	}
	for _, fromID := range []string{trFull, trEmail} {
		rels, _ := st.RelationshipsFrom(fromID)
		if len(rels) != 1 || rels[0].ToID != saID || rels[0].Kind != store.RelUses {
			t.Errorf("from %s: got %+v, want →SA uses", fromID, rels)
		}
	}
}

func TestResolveCloudBuildWorkerPoolRelationships_AllTargets(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	netSelfLink := "https://www.googleapis.com/compute/v1/projects/proj-1/global/networks/net-1"
	netID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeNetwork, netSelfLink, "",
		marshalAttrs(t, &compute.Network{SelfLink: netSelfLink}))

	naSelfLink := "https://www.googleapis.com/compute/v1/projects/proj-1/regions/us-central1/networkAttachments/na-1"
	naID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeNetworkAttachment, naSelfLink, "us-central1",
		marshalAttrs(t, &compute.NetworkAttachment{SelfLink: naSelfLink}))

	wpID := upsertTestResource(t, st, "gcp", p.ID, TypeCloudBuildWorkerPool, "projects/proj-1/locations/us-central1/workerPools/wp-1", "us-central1",
		marshalAttrs(t, &cloudbuild.WorkerPool{
			Name: "projects/proj-1/locations/us-central1/workerPools/wp-1",
			PrivatePoolV1Config: &cloudbuild.PrivatePoolV1Config{
				NetworkConfig: &cloudbuild.NetworkConfig{
					PeeredNetwork: "projects/123456789/global/networks/net-1",
				},
				PrivateServiceConnect: &cloudbuild.PrivateServiceConnect{
					NetworkAttachment: "projects/proj-1/regions/us-central1/networkAttachments/na-1",
				},
			},
		}))

	if err := resolveCloudBuildWorkerPoolRelationships(p, st); err != nil {
		t.Fatalf("resolveCloudBuildWorkerPoolRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(wpID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 2 {
		t.Fatalf("expected 2 edges, got %d: %+v", len(rels), rels)
	}
	want := map[string]bool{netID: false, naID: false}
	for _, r := range rels {
		if r.Kind != store.RelAttachedTo {
			t.Errorf("unexpected kind %q", r.Kind)
		}
		if _, ok := want[r.ToID]; !ok {
			t.Errorf("unexpected edge target %q", r.ToID)
			continue
		}
		want[r.ToID] = true
	}
	for id, hit := range want {
		if !hit {
			t.Errorf("missing edge to %q", id)
		}
	}
}

func TestResolveCloudBuildWorkerPoolRelationships_NilConfigSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	wpID := upsertTestResource(t, st, "gcp", p.ID, TypeCloudBuildWorkerPool, "projects/proj-1/locations/us-central1/workerPools/wp-1", "us-central1",
		marshalAttrs(t, &cloudbuild.WorkerPool{Name: "projects/proj-1/locations/us-central1/workerPools/wp-1"}))

	if err := resolveCloudBuildWorkerPoolRelationships(p, st); err != nil {
		t.Fatalf("resolveCloudBuildWorkerPoolRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(wpID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edges when privatePoolV1Config is nil, got %+v", rels)
	}
}

func TestResolveCloudBuildWorkerPoolRelationships_UnscannedTargetsSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	wpID := upsertTestResource(t, st, "gcp", p.ID, TypeCloudBuildWorkerPool, "projects/proj-1/locations/us-central1/workerPools/wp-1", "us-central1",
		marshalAttrs(t, &cloudbuild.WorkerPool{
			Name: "projects/proj-1/locations/us-central1/workerPools/wp-1",
			PrivatePoolV1Config: &cloudbuild.PrivatePoolV1Config{
				NetworkConfig:         &cloudbuild.NetworkConfig{PeeredNetwork: "projects/123456789/global/networks/not-scanned"},
				PrivateServiceConnect: &cloudbuild.PrivateServiceConnect{NetworkAttachment: "projects/proj-1/regions/us-central1/networkAttachments/not-scanned"},
			},
		}))

	if err := resolveCloudBuildWorkerPoolRelationships(p, st); err != nil {
		t.Fatalf("resolveCloudBuildWorkerPoolRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(wpID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edges for unscanned targets, got %+v", rels)
	}
}

func TestResolveCloudBuildWorkerPoolRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")
	if err := resolveCloudBuildWorkerPoolRelationships(p, st); err != nil {
		t.Fatalf("resolveCloudBuildWorkerPoolRelationships on empty project: %v", err)
	}
}

func TestResolveCloudBuildConnectionRelationships_EachVCSKind(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	// mkVersion seeds a TypeSecretVersion row and returns (nativeID, resourceID) —
	// nativeID feeds the connection's attrs JSON, resourceID is the expected edge
	// target (RelationshipsFrom returns resource IDs, not NativeIDs).
	mkVersion := func(id string) (nativeID, resourceID string) {
		nativeID = "projects/proj-1/secrets/" + id + "/versions/1"
		resourceID = upsertTestResource(t, st, "gcp", p.ID, TypeSecretVersion, nativeID, "",
			marshalAttrs(t, &secretmanager.SecretVersion{Name: nativeID}))
		return nativeID, resourceID
	}

	githubVerNative, githubVer := mkVersion("github-oauth")
	githubConnID := upsertTestResource(t, st, "gcp", p.ID, TypeCloudBuildConnection, "projects/proj-1/locations/us-central1/connections/gh-1", "us-central1",
		marshalAttrs(t, &cloudbuildv2.Connection{
			Name:         "projects/proj-1/locations/us-central1/connections/gh-1",
			GithubConfig: &cloudbuildv2.GitHubConfig{AuthorizerCredential: &cloudbuildv2.OAuthCredential{OauthTokenSecretVersion: githubVerNative}},
		}))

	gheKeyVerNative, gheKeyVer := mkVersion("ghe-key")
	gheWebhookVerNative, gheWebhookVer := mkVersion("ghe-webhook")
	gheConnID := upsertTestResource(t, st, "gcp", p.ID, TypeCloudBuildConnection, "projects/proj-1/locations/us-central1/connections/ghe-1", "us-central1",
		marshalAttrs(t, &cloudbuildv2.Connection{
			Name: "projects/proj-1/locations/us-central1/connections/ghe-1",
			GithubEnterpriseConfig: &cloudbuildv2.GoogleDevtoolsCloudbuildV2GitHubEnterpriseConfig{
				PrivateKeySecretVersion:    gheKeyVerNative,
				WebhookSecretSecretVersion: gheWebhookVerNative,
			},
		}))

	gitlabAuthVerNative, gitlabAuthVer := mkVersion("gitlab-auth")
	gitlabReadVerNative, gitlabReadVer := mkVersion("gitlab-read")
	gitlabWebhookVerNative, gitlabWebhookVer := mkVersion("gitlab-webhook")
	gitlabConnID := upsertTestResource(t, st, "gcp", p.ID, TypeCloudBuildConnection, "projects/proj-1/locations/us-central1/connections/gl-1", "us-central1",
		marshalAttrs(t, &cloudbuildv2.Connection{
			Name: "projects/proj-1/locations/us-central1/connections/gl-1",
			GitlabConfig: &cloudbuildv2.GoogleDevtoolsCloudbuildV2GitLabConfig{
				AuthorizerCredential:       &cloudbuildv2.UserCredential{UserTokenSecretVersion: gitlabAuthVerNative},
				ReadAuthorizerCredential:   &cloudbuildv2.UserCredential{UserTokenSecretVersion: gitlabReadVerNative},
				WebhookSecretSecretVersion: gitlabWebhookVerNative,
			},
		}))

	bbCloudAuthVerNative, bbCloudAuthVer := mkVersion("bb-cloud-auth")
	bbCloudReadVerNative, bbCloudReadVer := mkVersion("bb-cloud-read")
	bbCloudWebhookVerNative, bbCloudWebhookVer := mkVersion("bb-cloud-webhook")
	bbCloudConnID := upsertTestResource(t, st, "gcp", p.ID, TypeCloudBuildConnection, "projects/proj-1/locations/us-central1/connections/bbc-1", "us-central1",
		marshalAttrs(t, &cloudbuildv2.Connection{
			Name: "projects/proj-1/locations/us-central1/connections/bbc-1",
			BitbucketCloudConfig: &cloudbuildv2.BitbucketCloudConfig{
				AuthorizerCredential:       &cloudbuildv2.UserCredential{UserTokenSecretVersion: bbCloudAuthVerNative},
				ReadAuthorizerCredential:   &cloudbuildv2.UserCredential{UserTokenSecretVersion: bbCloudReadVerNative},
				WebhookSecretSecretVersion: bbCloudWebhookVerNative,
			},
		}))

	bbDCAuthVerNative, bbDCAuthVer := mkVersion("bb-dc-auth")
	bbDCReadVerNative, bbDCReadVer := mkVersion("bb-dc-read")
	bbDCWebhookVerNative, bbDCWebhookVer := mkVersion("bb-dc-webhook")
	bbDCConnID := upsertTestResource(t, st, "gcp", p.ID, TypeCloudBuildConnection, "projects/proj-1/locations/us-central1/connections/bbdc-1", "us-central1",
		marshalAttrs(t, &cloudbuildv2.Connection{
			Name: "projects/proj-1/locations/us-central1/connections/bbdc-1",
			BitbucketDataCenterConfig: &cloudbuildv2.BitbucketDataCenterConfig{
				AuthorizerCredential:       &cloudbuildv2.UserCredential{UserTokenSecretVersion: bbDCAuthVerNative},
				ReadAuthorizerCredential:   &cloudbuildv2.UserCredential{UserTokenSecretVersion: bbDCReadVerNative},
				WebhookSecretSecretVersion: bbDCWebhookVerNative,
			},
		}))

	if err := resolveCloudBuildConnectionRelationships(p, st); err != nil {
		t.Fatalf("resolveCloudBuildConnectionRelationships: %v", err)
	}

	assertRelTo := func(fromID string, wantTargets ...string) {
		t.Helper()
		rels, err := st.RelationshipsFrom(fromID)
		if err != nil {
			t.Fatalf("RelationshipsFrom: %v", err)
		}
		want := make(map[string]bool, len(wantTargets))
		for _, w := range wantTargets {
			want[w] = false
		}
		if len(rels) != len(want) {
			t.Fatalf("from %s: got %d edges %+v, want %d", fromID, len(rels), rels, len(want))
		}
		for _, r := range rels {
			if r.Kind != store.RelUses {
				t.Errorf("unexpected kind %q", r.Kind)
			}
			if _, ok := want[r.ToID]; !ok {
				t.Errorf("unexpected edge target %q", r.ToID)
				continue
			}
			want[r.ToID] = true
		}
		for id, hit := range want {
			if !hit {
				t.Errorf("missing edge to %q", id)
			}
		}
	}

	assertRelTo(githubConnID, githubVer)
	assertRelTo(gheConnID, gheKeyVer, gheWebhookVer)
	assertRelTo(gitlabConnID, gitlabAuthVer, gitlabReadVer, gitlabWebhookVer)
	assertRelTo(bbCloudConnID, bbCloudAuthVer, bbCloudReadVer, bbCloudWebhookVer)
	assertRelTo(bbDCConnID, bbDCAuthVer, bbDCReadVer, bbDCWebhookVer)
}

func TestResolveCloudBuildConnectionRelationships_UnscannedVersionSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	connID := upsertTestResource(t, st, "gcp", p.ID, TypeCloudBuildConnection, "projects/proj-1/locations/us-central1/connections/gh-1", "us-central1",
		marshalAttrs(t, &cloudbuildv2.Connection{
			Name: "projects/proj-1/locations/us-central1/connections/gh-1",
			GithubConfig: &cloudbuildv2.GitHubConfig{AuthorizerCredential: &cloudbuildv2.OAuthCredential{
				OauthTokenSecretVersion: "projects/proj-1/secrets/not-scanned/versions/1",
			}},
		}))

	if err := resolveCloudBuildConnectionRelationships(p, st); err != nil {
		t.Fatalf("resolveCloudBuildConnectionRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(connID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edge for unscanned secret version, got %+v", rels)
	}
}

func TestResolveCloudBuildConnectionRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")
	if err := resolveCloudBuildConnectionRelationships(p, st); err != nil {
		t.Fatalf("resolveCloudBuildConnectionRelationships on empty project: %v", err)
	}
}

func TestResolveCloudBuildGithubEnterpriseConfigRelationships_AllTargets(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	netSelfLink := "https://www.googleapis.com/compute/v1/projects/proj-1/global/networks/net-1"
	netID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeNetwork, netSelfLink, "",
		marshalAttrs(t, &compute.Network{SelfLink: netSelfLink}))

	// mkSecret/mkVersion return (nativeID, resourceID) — nativeID feeds the
	// GithubEnterpriseConfig's attrs JSON, resourceID is the expected edge
	// target (RelationshipsFrom returns resource IDs, not NativeIDs).
	mkSecret := func(id string) (nativeID, resourceID string) {
		nativeID = "projects/proj-1/secrets/" + id
		resourceID = upsertTestResource(t, st, "gcp", p.ID, TypeSecret, nativeID, "",
			marshalAttrs(t, &secretmanager.Secret{Name: nativeID}))
		return nativeID, resourceID
	}
	mkVersion := func(id string) (nativeID, resourceID string) {
		nativeID = "projects/proj-1/secrets/" + id + "/versions/1"
		resourceID = upsertTestResource(t, st, "gcp", p.ID, TypeSecretVersion, nativeID, "",
			marshalAttrs(t, &secretmanager.SecretVersion{Name: nativeID}))
		return nativeID, resourceID
	}

	oauthClientIDSecretNative, oauthClientIDSecret := mkSecret("oauth-client-id")
	oauthClientIDVerNative, oauthClientIDVer := mkVersion("oauth-client-id")
	oauthSecretNative, oauthSecret := mkSecret("oauth-secret")
	oauthSecretVerNative, oauthSecretVer := mkVersion("oauth-secret")
	privateKeySecretNative, privateKeySecret := mkSecret("private-key")
	privateKeyVerNative, privateKeyVer := mkVersion("private-key")
	webhookSecretNative, webhookSecret := mkSecret("webhook")
	webhookVerNative, webhookVer := mkVersion("webhook")

	gheID := upsertTestResource(t, st, "gcp", p.ID, TypeCloudBuildGithubEnterpriseConfig,
		"projects/proj-1/locations/us-central1/githubEnterpriseConfigs/ghe-1", "us-central1",
		marshalAttrs(t, &cloudbuild.GitHubEnterpriseConfig{
			Name:          "projects/proj-1/locations/us-central1/githubEnterpriseConfigs/ghe-1",
			PeeredNetwork: "projects/123456789/global/networks/net-1",
			Secrets: &cloudbuild.GitHubEnterpriseSecrets{
				OauthClientIdName:        oauthClientIDSecretNative,
				OauthClientIdVersionName: oauthClientIDVerNative,
				OauthSecretName:          oauthSecretNative,
				OauthSecretVersionName:   oauthSecretVerNative,
				PrivateKeyName:           privateKeySecretNative,
				PrivateKeyVersionName:    privateKeyVerNative,
				WebhookSecretName:        webhookSecretNative,
				WebhookSecretVersionName: webhookVerNative,
			},
		}))

	if err := resolveCloudBuildGithubEnterpriseConfigRelationships(p, st); err != nil {
		t.Fatalf("resolveCloudBuildGithubEnterpriseConfigRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(gheID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 9 {
		t.Fatalf("expected 9 edges (1 network + 4 secrets + 4 versions), got %d: %+v", len(rels), rels)
	}
	want := map[string]bool{
		netID: false, oauthClientIDSecret: false, oauthClientIDVer: false,
		oauthSecret: false, oauthSecretVer: false, privateKeySecret: false,
		privateKeyVer: false, webhookSecret: false, webhookVer: false,
	}
	for _, r := range rels {
		if _, ok := want[r.ToID]; !ok {
			t.Errorf("unexpected edge target %q", r.ToID)
			continue
		}
		want[r.ToID] = true
	}
	for id, hit := range want {
		if !hit {
			t.Errorf("missing edge to %q", id)
		}
	}
}

func TestResolveCloudBuildGithubEnterpriseConfigRelationships_NilSecretsSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	gheID := upsertTestResource(t, st, "gcp", p.ID, TypeCloudBuildGithubEnterpriseConfig,
		"projects/proj-1/locations/us-central1/githubEnterpriseConfigs/ghe-1", "us-central1",
		marshalAttrs(t, &cloudbuild.GitHubEnterpriseConfig{
			Name: "projects/proj-1/locations/us-central1/githubEnterpriseConfigs/ghe-1",
		}))

	if err := resolveCloudBuildGithubEnterpriseConfigRelationships(p, st); err != nil {
		t.Fatalf("resolveCloudBuildGithubEnterpriseConfigRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(gheID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edges when secrets/peeredNetwork are unset, got %+v", rels)
	}
}

func TestResolveCloudBuildGithubEnterpriseConfigRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")
	if err := resolveCloudBuildGithubEnterpriseConfigRelationships(p, st); err != nil {
		t.Fatalf("resolveCloudBuildGithubEnterpriseConfigRelationships on empty project: %v", err)
	}
}
