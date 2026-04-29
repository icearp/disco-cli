package azure

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	abstractions "github.com/microsoft/kiota-abstractions-go"
	msgraphsdk "github.com/microsoftgraph/msgraph-sdk-go"
	msgraphcore "github.com/microsoftgraph/msgraph-sdk-go-core"
	graphapps "github.com/microsoftgraph/msgraph-sdk-go/applications"
	graphgroups "github.com/microsoftgraph/msgraph-sdk-go/groups"
	graphmodels "github.com/microsoftgraph/msgraph-sdk-go/models"
	graphsps "github.com/microsoftgraph/msgraph-sdk-go/serviceprincipals"
	graphusers "github.com/microsoftgraph/msgraph-sdk-go/users"
)

func init() {
	// Entra ID types come from Microsoft Graph — ARM Providers/List doesn't
	// surface them, so they're synthetic from the coverage matrix
	// perspective (no upstream ARM resource type).
	registerTenantService(tenantServiceEntry{
		name: "azure:microsoft.entra",
		fn:   scanEntra,
		emits: []coverage.TypeDecl{
			{Service: "graph", DiscoType: TypeEntraUser, Synthetic: true},
			{Service: "graph", DiscoType: TypeEntraGroup, Synthetic: true},
			{Service: "graph", DiscoType: TypeEntraServicePrincipal, Synthetic: true},
			{Service: "graph", DiscoType: TypeEntraApplication, Synthetic: true},
		},
	})
}

const graphScope = "https://graph.microsoft.com/.default"

// scanEntra discovers Entra ID (Azure AD) directory objects via Microsoft
// Graph: users, groups, service principals, and application registrations.
// Uses the official msgraph-sdk-go (kiota-generated) — typed models with
// built-in retry, batching, and odata pagination via PageIterator.
//
// Tenant scope: NativeID is the object's `id` GUID. AccountID is the tenant
// ID resolved from the JWT `tid` claim of a Graph token. Closure is empty —
// these objects sit above the subscription/RG hierarchy.
//
// Permission failures (Directory.Read.All not granted) degrade to a single
// scan warning and a partial scan; later sub-scope scanners proceed normally.
func scanEntra(ctx context.Context, subs []subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	if len(subs) == 0 {
		return 0, 0, nil
	}
	tenantID, terr := tenantIDFromCred(ctx, cred)
	if terr != nil {
		st.ReportWarning(store.ScanWarning{
			Provider: "azure", Service: "azure:microsoft.entra", Scope: "tenant",
			Message: "could not resolve tenant id: " + formatAzureError(terr),
		})
		return 0, 0, nil
	}
	client, gerr := msgraphsdk.NewGraphServiceClientWithCredentials(cred, []string{graphScope})
	if gerr != nil {
		return 0, 0, fmt.Errorf("graph client: %w", gerr)
	}

	t, n := scanEntraUsers(ctx, client, tenantID, st, scanID)
	total += t
	inserted += n
	t, n = scanEntraGroups(ctx, client, tenantID, st, scanID)
	total += t
	inserted += n
	t, n = scanEntraServicePrincipals(ctx, client, tenantID, st, scanID)
	total += t
	inserted += n
	t, n = scanEntraApplications(ctx, client, tenantID, st, scanID)
	total += t
	inserted += n
	return total, inserted, nil
}

// userAttrs is the curated subset persisted under attributes for a Graph
// User. Stable shape so resolvers can index by upn / object id without
// depending on the full Graph payload. Extend as new resolvers need fields.
type userAttrs struct {
	ID                string `json:"id"`
	DisplayName       string `json:"displayName"`
	UserPrincipalName string `json:"userPrincipalName,omitempty"`
	Mail              string `json:"mail,omitempty"`
	AccountEnabled    *bool  `json:"accountEnabled,omitempty"`
	UserType          string `json:"userType,omitempty"`
}

func scanEntraUsers(ctx context.Context, client *msgraphsdk.GraphServiceClient, tenantID string, st *store.Store, scanID string) (total, inserted int) {
	cfg := &graphusers.UsersRequestBuilderGetRequestConfiguration{
		QueryParameters: &graphusers.UsersRequestBuilderGetQueryParameters{
			Select: []string{"id", "displayName", "userPrincipalName", "mail", "accountEnabled", "userType"},
		},
	}
	resp, err := client.Users().Get(ctx, cfg)
	if err != nil {
		reportEntraErr(st, "users", err)
		return 0, 0
	}
	pi, err := msgraphcore.NewPageIterator[graphmodels.Userable](resp, client.GetAdapter(), graphmodels.CreateUserCollectionResponseFromDiscriminatorValue)
	if err != nil {
		reportEntraErr(st, "users", err)
		return 0, 0
	}
	pi.SetHeaders(abstractions.NewRequestHeaders())
	var batch []*store.Resource
	flush := func() {
		if len(batch) == 0 {
			return
		}
		n, uerr := st.UpsertResources(batch)
		if uerr != nil {
			st.ReportError(store.ScanError{Provider: "azure", Service: "azure:microsoft.entra", Scope: "users", Message: formatAzureError(uerr)})
			batch = batch[:0]
			return
		}
		total += len(batch)
		inserted += n
		batch = batch[:0]
	}
	if err := pi.Iterate(ctx, func(u graphmodels.Userable) bool {
		id := strDeref(u.GetId())
		if id == "" {
			return true
		}
		name := strDeref(u.GetDisplayName())
		attrs := userAttrs{
			ID:                id,
			DisplayName:       name,
			UserPrincipalName: strDeref(u.GetUserPrincipalName()),
			Mail:              strDeref(u.GetMail()),
			AccountEnabled:    u.GetAccountEnabled(),
			UserType:          strDeref(u.GetUserType()),
		}
		batch = append(batch, &store.Resource{
			Provider: "azure", AccountID: tenantID,
			Type: TypeEntraUser, NativeID: id,
			Name: &name, AttributesJSON: jsonOrEmpty(attrs),
			DiscoveredBy: scanID,
		})
		if len(batch) >= 500 {
			flush()
		}
		return true
	}); err != nil {
		reportEntraErr(st, "users", err)
	}
	flush()
	return total, inserted
}

type groupAttrs struct {
	ID              string   `json:"id"`
	DisplayName     string   `json:"displayName"`
	Description     string   `json:"description,omitempty"`
	MailEnabled     *bool    `json:"mailEnabled,omitempty"`
	SecurityEnabled *bool    `json:"securityEnabled,omitempty"`
	GroupTypes      []string `json:"groupTypes,omitempty"`
}

func scanEntraGroups(ctx context.Context, client *msgraphsdk.GraphServiceClient, tenantID string, st *store.Store, scanID string) (total, inserted int) {
	cfg := &graphgroups.GroupsRequestBuilderGetRequestConfiguration{
		QueryParameters: &graphgroups.GroupsRequestBuilderGetQueryParameters{
			Select: []string{"id", "displayName", "description", "mailEnabled", "securityEnabled", "groupTypes"},
		},
	}
	resp, err := client.Groups().Get(ctx, cfg)
	if err != nil {
		reportEntraErr(st, "groups", err)
		return 0, 0
	}
	pi, err := msgraphcore.NewPageIterator[graphmodels.Groupable](resp, client.GetAdapter(), graphmodels.CreateGroupCollectionResponseFromDiscriminatorValue)
	if err != nil {
		reportEntraErr(st, "groups", err)
		return 0, 0
	}
	pi.SetHeaders(abstractions.NewRequestHeaders())
	var batch []*store.Resource
	flush := func() {
		if len(batch) == 0 {
			return
		}
		n, uerr := st.UpsertResources(batch)
		if uerr != nil {
			st.ReportError(store.ScanError{Provider: "azure", Service: "azure:microsoft.entra", Scope: "groups", Message: formatAzureError(uerr)})
			batch = batch[:0]
			return
		}
		total += len(batch)
		inserted += n
		batch = batch[:0]
	}
	if err := pi.Iterate(ctx, func(g graphmodels.Groupable) bool {
		id := strDeref(g.GetId())
		if id == "" {
			return true
		}
		name := strDeref(g.GetDisplayName())
		attrs := groupAttrs{
			ID:              id,
			DisplayName:     name,
			Description:     strDeref(g.GetDescription()),
			MailEnabled:     g.GetMailEnabled(),
			SecurityEnabled: g.GetSecurityEnabled(),
			GroupTypes:      g.GetGroupTypes(),
		}
		batch = append(batch, &store.Resource{
			Provider: "azure", AccountID: tenantID,
			Type: TypeEntraGroup, NativeID: id,
			Name: &name, AttributesJSON: jsonOrEmpty(attrs),
			DiscoveredBy: scanID,
		})
		if len(batch) >= 500 {
			flush()
		}
		return true
	}); err != nil {
		reportEntraErr(st, "groups", err)
	}
	flush()
	return total, inserted
}

type spAttrs struct {
	ID                   string `json:"id"`
	AppID                string `json:"appId,omitempty"`
	DisplayName          string `json:"displayName"`
	ServicePrincipalType string `json:"servicePrincipalType,omitempty"`
	AccountEnabled       *bool  `json:"accountEnabled,omitempty"`
}

func scanEntraServicePrincipals(ctx context.Context, client *msgraphsdk.GraphServiceClient, tenantID string, st *store.Store, scanID string) (total, inserted int) {
	cfg := &graphsps.ServicePrincipalsRequestBuilderGetRequestConfiguration{
		QueryParameters: &graphsps.ServicePrincipalsRequestBuilderGetQueryParameters{
			Select: []string{"id", "appId", "displayName", "servicePrincipalType", "accountEnabled"},
		},
	}
	resp, err := client.ServicePrincipals().Get(ctx, cfg)
	if err != nil {
		reportEntraErr(st, "servicePrincipals", err)
		return 0, 0
	}
	pi, err := msgraphcore.NewPageIterator[graphmodels.ServicePrincipalable](resp, client.GetAdapter(), graphmodels.CreateServicePrincipalCollectionResponseFromDiscriminatorValue)
	if err != nil {
		reportEntraErr(st, "servicePrincipals", err)
		return 0, 0
	}
	pi.SetHeaders(abstractions.NewRequestHeaders())
	var batch []*store.Resource
	flush := func() {
		if len(batch) == 0 {
			return
		}
		n, uerr := st.UpsertResources(batch)
		if uerr != nil {
			st.ReportError(store.ScanError{Provider: "azure", Service: "azure:microsoft.entra", Scope: "servicePrincipals", Message: formatAzureError(uerr)})
			batch = batch[:0]
			return
		}
		total += len(batch)
		inserted += n
		batch = batch[:0]
	}
	if err := pi.Iterate(ctx, func(sp graphmodels.ServicePrincipalable) bool {
		id := strDeref(sp.GetId())
		if id == "" {
			return true
		}
		name := strDeref(sp.GetDisplayName())
		attrs := spAttrs{
			ID:                   id,
			AppID:                strDeref(sp.GetAppId()),
			DisplayName:          name,
			ServicePrincipalType: strDeref(sp.GetServicePrincipalType()),
			AccountEnabled:       sp.GetAccountEnabled(),
		}
		batch = append(batch, &store.Resource{
			Provider: "azure", AccountID: tenantID,
			Type: TypeEntraServicePrincipal, NativeID: id,
			Name: &name, AttributesJSON: jsonOrEmpty(attrs),
			DiscoveredBy: scanID,
		})
		if len(batch) >= 500 {
			flush()
		}
		return true
	}); err != nil {
		reportEntraErr(st, "servicePrincipals", err)
	}
	flush()
	return total, inserted
}

type appAttrs struct {
	ID             string `json:"id"`
	AppID          string `json:"appId,omitempty"`
	DisplayName    string `json:"displayName"`
	SignInAudience string `json:"signInAudience,omitempty"`
}

func scanEntraApplications(ctx context.Context, client *msgraphsdk.GraphServiceClient, tenantID string, st *store.Store, scanID string) (total, inserted int) {
	cfg := &graphapps.ApplicationsRequestBuilderGetRequestConfiguration{
		QueryParameters: &graphapps.ApplicationsRequestBuilderGetQueryParameters{
			Select: []string{"id", "appId", "displayName", "signInAudience"},
		},
	}
	resp, err := client.Applications().Get(ctx, cfg)
	if err != nil {
		reportEntraErr(st, "applications", err)
		return 0, 0
	}
	pi, err := msgraphcore.NewPageIterator[graphmodels.Applicationable](resp, client.GetAdapter(), graphmodels.CreateApplicationCollectionResponseFromDiscriminatorValue)
	if err != nil {
		reportEntraErr(st, "applications", err)
		return 0, 0
	}
	pi.SetHeaders(abstractions.NewRequestHeaders())
	var batch []*store.Resource
	flush := func() {
		if len(batch) == 0 {
			return
		}
		n, uerr := st.UpsertResources(batch)
		if uerr != nil {
			st.ReportError(store.ScanError{Provider: "azure", Service: "azure:microsoft.entra", Scope: "applications", Message: formatAzureError(uerr)})
			batch = batch[:0]
			return
		}
		total += len(batch)
		inserted += n
		batch = batch[:0]
	}
	if err := pi.Iterate(ctx, func(a graphmodels.Applicationable) bool {
		id := strDeref(a.GetId())
		if id == "" {
			return true
		}
		name := strDeref(a.GetDisplayName())
		attrs := appAttrs{
			ID:             id,
			AppID:          strDeref(a.GetAppId()),
			DisplayName:    name,
			SignInAudience: strDeref(a.GetSignInAudience()),
		}
		batch = append(batch, &store.Resource{
			Provider: "azure", AccountID: tenantID,
			Type: TypeEntraApplication, NativeID: id,
			Name: &name, AttributesJSON: jsonOrEmpty(attrs),
			DiscoveredBy: scanID,
		})
		if len(batch) >= 500 {
			flush()
		}
		return true
	}); err != nil {
		reportEntraErr(st, "applications", err)
	}
	flush()
	return total, inserted
}

// reportEntraErr classifies a Graph error: missing-permission paths surface
// as ScanWarning + abort the entity, hard errors surface as ScanError.
// Kiota wraps API errors as graphmodels/odataerrors.* — the message is the
// most useful signal for the simple classifier below.
func reportEntraErr(st *store.Store, scope string, err error) {
	// Classify against the raw error string (SDK error text + JSON body) so
	// substring matches still hit. Persist the narrowed `formatAzureError`
	// shape so end-of-scan rendering matches AWS/GCP brevity.
	raw := err.Error()
	msg := formatAzureError(err)
	if strings.Contains(raw, "Authorization_RequestDenied") ||
		strings.Contains(raw, "Insufficient privileges") ||
		strings.Contains(raw, "401") || strings.Contains(raw, "403") {
		st.ReportWarning(store.ScanWarning{
			Provider: "azure", Service: "azure:microsoft.entra", Scope: scope, Message: msg,
		})
		return
	}
	st.ReportError(store.ScanError{
		Provider: "azure", Service: "azure:microsoft.entra", Scope: scope, Message: msg,
	})
}

// strDeref returns the dereferenced string, "" if nil.
func strDeref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// jsonOrEmpty marshals v; returns "{}" on marshal failure (never panics —
// Resource AttributesJSON must be valid JSON for downstream rule queries).
func jsonOrEmpty(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// tenantIDFromCred resolves the calling tenant by issuing a Graph token and
// reading the `tid` claim from the JWT. azidentity exposes no tenant getter.
func tenantIDFromCred(ctx context.Context, cred *azidentity.DefaultAzureCredential) (string, error) {
	tok, err := cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{graphScope}})
	if err != nil {
		return "", err
	}
	return tenantIDFromJWT(tok.Token)
}

// tenantIDFromJWT decodes the unverified payload of a JWT and pulls the
// `tid` claim. Signature verification is unnecessary — the token came from
// our own credential and is being used by us, not consumed externally.
func tenantIDFromJWT(jwt string) (string, error) {
	parts := strings.Split(jwt, ".")
	if len(parts) < 2 {
		return "", fmt.Errorf("not a JWT")
	}
	payload, err := decodeJWTPayload(parts[1])
	if err != nil {
		return "", fmt.Errorf("decode JWT payload: %w", err)
	}
	var claims struct {
		Tid string `json:"tid"`
	}
	if jerr := json.Unmarshal(payload, &claims); jerr != nil {
		return "", fmt.Errorf("parse JWT claims: %w", jerr)
	}
	if claims.Tid == "" {
		return "", fmt.Errorf("no tid claim in token")
	}
	return claims.Tid, nil
}

// decodeJWTPayload decodes a JWT body segment (base64url, possibly with
// missing padding). RawURLEncoding handles missing padding; URL+padding
// edge cases fall through to URLEncoding.
func decodeJWTPayload(s string) ([]byte, error) {
	if b, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.URLEncoding.DecodeString(s)
}
