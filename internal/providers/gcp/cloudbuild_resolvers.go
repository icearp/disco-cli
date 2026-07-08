package gcp

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(resolveCloudBuildRelationships,
		EdgeDecl{TypeCloudBuildTrigger, TypeIAMServiceAccount, store.RelUses},
	)
	registerResolver(resolveCloudBuildWorkerPoolRelationships,
		EdgeDecl{TypeCloudBuildWorkerPool, TypeComputeNetwork, store.RelAttachedTo},
		EdgeDecl{TypeCloudBuildWorkerPool, TypeComputeNetworkAttachment, store.RelAttachedTo},
	)
	registerResolver(resolveCloudBuildConnectionRelationships,
		EdgeDecl{TypeCloudBuildConnection, TypeSecretVersion, store.RelUses},
	)
	registerResolver(resolveCloudBuildGithubEnterpriseConfigRelationships,
		EdgeDecl{TypeCloudBuildGithubEnterpriseConfig, TypeComputeNetwork, store.RelAttachedTo},
		EdgeDecl{TypeCloudBuildGithubEnterpriseConfig, TypeSecret, store.RelUses},
		EdgeDecl{TypeCloudBuildGithubEnterpriseConfig, TypeSecretVersion, store.RelUses},
	)
}

// resolveCloudBuildRelationships derives trigger -[uses]-> service-account
// edges. The trigger's `serviceAccount` field
// (`projects/{projectId}/serviceAccounts/{ACCOUNT_EMAIL_OR_UNIQUEID}`)
// matches the SA NativeID format directly. Email-form fallback handled the
// same way as R4.10's serverless resolver.
//
// Worker pool edges deferred (worker-pool scanner not landed). GitHub /
// repo connection edges deferred — connection scanner landing alongside.
func resolveCloudBuildRelationships(p *project, st *store.Store) error {
	trs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeCloudBuildTrigger},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(trs) == 0 {
		return nil
	}

	sas, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeIAMServiceAccount},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	saIDByNative := make(map[string]string, len(sas))
	saIDByEmail := make(map[string]string, len(sas))
	for _, sa := range sas {
		saIDByNative[sa.NativeID] = sa.ID
		if i := strings.LastIndex(sa.NativeID, "/"); i >= 0 {
			saIDByEmail[sa.NativeID[i+1:]] = sa.ID
		}
	}

	for _, tr := range trs {
		var a struct {
			ServiceAccount string `json:"serviceAccount"`
		}
		if err := json.Unmarshal([]byte(tr.AttributesJSON), &a); err != nil {
			continue
		}
		if a.ServiceAccount == "" {
			continue
		}
		saID, ok := saIDByNative[a.ServiceAccount]
		if !ok {
			// Try email-only form (some triggers store just the email).
			saID, ok = saIDByEmail[a.ServiceAccount]
			if !ok {
				continue
			}
		}
		if err := st.UpsertRelationship(tr.ID, saID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert trigger→SA: %w", err)
		}
	}
	return nil
}

// Resolver Wave R20 of the resolver-implementation backlog (ROADMAP.md
// "Resolver buildout"): WorkerPool, Connection, GithubEnterpriseConfig — the
// 3 remaining cloudbuild orphans, deferred at scanner-landing time per this
// file's original comment ("Worker pool edges deferred... GitHub / repo
// connection edges deferred").
//
// WorkerPool.privatePoolV1Config carries two independent network bindings —
// networkConfig.peeredNetwork (a bare `projects/{num}/global/networks/{net}`
// name; project may be a *number*, not the caller's project ID, but bare-name
// matching only ever compares the trailing segment so this doesn't matter)
// and privateServiceConnect.networkAttachment (same bare-name shape, against
// the already-scanned NetworkAttachment type) — both verified via
// `go doc cloudbuild/v1.NetworkConfig`/`PrivateServiceConnect`.
//
// Connection (v2 API) is a oneof over 5 VCS configs (github/githubEnterprise/
// gitlab/bitbucketCloud/bitbucketDataCenter); every populated one carries at
// least one `projects/*/secrets/*/versions/*` SecretManager reference
// (verified per-config via `go doc`) — an exact-match NativeID (SecretVersion
// rows are upserted with NativeID = the SDK's own `v.Name` in that same
// format, see secretmanager_scanners.go). gitlab/bitbucketCloud/
// bitbucketDataCenter share an identical 3-field credential shape
// (authorizerCredential + readAuthorizerCredential + webhookSecretSecretVersion),
// factored into the shared cloudBuildUserCredConfig type.
//
// GithubEnterpriseConfig (the legacy v1 top-level type, distinct from
// Connection's nested v2 githubEnterpriseConfig) has its own peeredNetwork
// (same bare-name Network binding as WorkerPool) plus a Secrets block with 4
// paired Secret/SecretVersion resource-name fields (`*Name` → the Secret
// itself, `*VersionName` → a specific version) — both wired since both are
// independently scanned disco types.
func resolveCloudBuildWorkerPoolRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeCloudBuildWorkerPool},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	netByName, err := bareNameIndex(p, st, TypeComputeNetwork)
	if err != nil {
		return err
	}
	naByName, err := bareNameIndex(p, st, TypeComputeNetworkAttachment)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			PrivatePoolV1Config *struct {
				NetworkConfig *struct {
					PeeredNetwork string `json:"peeredNetwork"`
				} `json:"networkConfig"`
				PrivateServiceConnect *struct {
					NetworkAttachment string `json:"networkAttachment"`
				} `json:"privateServiceConnect"`
			} `json:"privatePoolV1Config"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		cfg := attrs.PrivatePoolV1Config
		if cfg == nil {
			continue
		}
		if nc := cfg.NetworkConfig; nc != nil && nc.PeeredNetwork != "" {
			if toID, ok := netByName[lastSegment(nc.PeeredNetwork)]; ok {
				if err := st.UpsertRelationship(r.ID, toID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert workerPool→network: %w", err)
				}
			}
		}
		if psc := cfg.PrivateServiceConnect; psc != nil && psc.NetworkAttachment != "" {
			if toID, ok := naByName[lastSegment(psc.NetworkAttachment)]; ok {
				if err := st.UpsertRelationship(r.ID, toID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert workerPool→networkAttachment: %w", err)
				}
			}
		}
	}
	return nil
}

// cloudBuildUserCredConfig mirrors the identical 3-field JSON shape shared by
// cloudbuild/v2's GitLabConfig, BitbucketCloudConfig, and
// BitbucketDataCenterConfig.
type cloudBuildUserCredConfig struct {
	AuthorizerCredential *struct {
		UserTokenSecretVersion string `json:"userTokenSecretVersion"`
	} `json:"authorizerCredential"`
	ReadAuthorizerCredential *struct {
		UserTokenSecretVersion string `json:"userTokenSecretVersion"`
	} `json:"readAuthorizerCredential"`
	WebhookSecretSecretVersion string `json:"webhookSecretSecretVersion"`
}

func (c *cloudBuildUserCredConfig) secretVersionRefs() []string {
	if c == nil {
		return nil
	}
	var out []string
	if c.AuthorizerCredential != nil && c.AuthorizerCredential.UserTokenSecretVersion != "" {
		out = append(out, c.AuthorizerCredential.UserTokenSecretVersion)
	}
	if c.ReadAuthorizerCredential != nil && c.ReadAuthorizerCredential.UserTokenSecretVersion != "" {
		out = append(out, c.ReadAuthorizerCredential.UserTokenSecretVersion)
	}
	if c.WebhookSecretSecretVersion != "" {
		out = append(out, c.WebhookSecretSecretVersion)
	}
	return out
}

// cloudBuildConnectionAttrs mirrors cloudbuild/v2 Connection's oneof VCS
// config shape — only one of these 5 is ever populated per real connection.
type cloudBuildConnectionAttrs struct {
	GithubConfig *struct {
		AuthorizerCredential *struct {
			OauthTokenSecretVersion string `json:"oauthTokenSecretVersion"`
		} `json:"authorizerCredential"`
	} `json:"githubConfig"`
	GithubEnterpriseConfig *struct {
		PrivateKeySecretVersion    string `json:"privateKeySecretVersion"`
		WebhookSecretSecretVersion string `json:"webhookSecretSecretVersion"`
	} `json:"githubEnterpriseConfig"`
	GitlabConfig              *cloudBuildUserCredConfig `json:"gitlabConfig"`
	BitbucketCloudConfig      *cloudBuildUserCredConfig `json:"bitbucketCloudConfig"`
	BitbucketDataCenterConfig *cloudBuildUserCredConfig `json:"bitbucketDataCenterConfig"`
}

func (a *cloudBuildConnectionAttrs) secretVersionRefs() []string {
	var out []string
	if gc := a.GithubConfig; gc != nil && gc.AuthorizerCredential != nil && gc.AuthorizerCredential.OauthTokenSecretVersion != "" {
		out = append(out, gc.AuthorizerCredential.OauthTokenSecretVersion)
	}
	if gec := a.GithubEnterpriseConfig; gec != nil {
		if gec.PrivateKeySecretVersion != "" {
			out = append(out, gec.PrivateKeySecretVersion)
		}
		if gec.WebhookSecretSecretVersion != "" {
			out = append(out, gec.WebhookSecretSecretVersion)
		}
	}
	out = append(out, a.GitlabConfig.secretVersionRefs()...)
	out = append(out, a.BitbucketCloudConfig.secretVersionRefs()...)
	out = append(out, a.BitbucketDataCenterConfig.secretVersionRefs()...)
	return out
}

func resolveCloudBuildConnectionRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeCloudBuildConnection},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	scannedVersions, err := scannedIDSet(p, st, TypeSecretVersion)
	if err != nil {
		return err
	}
	if len(scannedVersions) == 0 {
		return nil
	}
	for _, r := range rows {
		var attrs cloudBuildConnectionAttrs
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, ref := range attrs.secretVersionRefs() {
			if err := upsertIfScanned(st, scannedVersions, r.ID, "gcp", p.ID, TypeSecretVersion, ref, store.RelUses); err != nil {
				return fmt.Errorf("upsert connection→secretVersion: %w", err)
			}
		}
	}
	return nil
}

func resolveCloudBuildGithubEnterpriseConfigRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeCloudBuildGithubEnterpriseConfig},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	netByName, err := bareNameIndex(p, st, TypeComputeNetwork)
	if err != nil {
		return err
	}
	scannedSecrets, err := scannedIDSet(p, st, TypeSecret)
	if err != nil {
		return err
	}
	scannedVersions, err := scannedIDSet(p, st, TypeSecretVersion)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			PeeredNetwork string `json:"peeredNetwork"`
			Secrets       *struct {
				OauthClientIdName        string `json:"oauthClientIdName"`
				OauthClientIdVersionName string `json:"oauthClientIdVersionName"`
				OauthSecretName          string `json:"oauthSecretName"`
				OauthSecretVersionName   string `json:"oauthSecretVersionName"`
				PrivateKeyName           string `json:"privateKeyName"`
				PrivateKeyVersionName    string `json:"privateKeyVersionName"`
				WebhookSecretName        string `json:"webhookSecretName"`
				WebhookSecretVersionName string `json:"webhookSecretVersionName"`
			} `json:"secrets"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.PeeredNetwork != "" {
			if toID, ok := netByName[lastSegment(attrs.PeeredNetwork)]; ok {
				if err := st.UpsertRelationship(r.ID, toID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert gheConfig→network: %w", err)
				}
			}
		}
		s := attrs.Secrets
		if s == nil {
			continue
		}
		if len(scannedSecrets) > 0 {
			for _, name := range []string{s.OauthClientIdName, s.OauthSecretName, s.PrivateKeyName, s.WebhookSecretName} {
				if name == "" {
					continue
				}
				if err := upsertIfScanned(st, scannedSecrets, r.ID, "gcp", p.ID, TypeSecret, name, store.RelUses); err != nil {
					return fmt.Errorf("upsert gheConfig→secret: %w", err)
				}
			}
		}
		if len(scannedVersions) > 0 {
			for _, name := range []string{s.OauthClientIdVersionName, s.OauthSecretVersionName, s.PrivateKeyVersionName, s.WebhookSecretVersionName} {
				if name == "" {
					continue
				}
				if err := upsertIfScanned(st, scannedVersions, r.ID, "gcp", p.ID, TypeSecretVersion, name, store.RelUses); err != nil {
					return fmt.Errorf("upsert gheConfig→secretVersion: %w", err)
				}
			}
		}
	}
	return nil
}
