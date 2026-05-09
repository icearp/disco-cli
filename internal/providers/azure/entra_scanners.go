package azure

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
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

const (
	graphScope      = "https://graph.microsoft.com/.default"
	graphDefaultURL = "https://graph.microsoft.com/v1.0"
	graphPageSize   = 500
)

// tokenIssuer narrows azcore.TokenCredential to the one method scanEntra
// uses. Lets tests stub token issuance without standing up a full
// azidentity credential graph.
type tokenIssuer interface {
	GetToken(ctx context.Context, opts policy.TokenRequestOptions) (azcore.AccessToken, error)
}

// graphClient is a tiny REST client for the four Microsoft Graph list
// endpoints disco scans. Replaces msgraph-sdk-go (kiota-generated, ~9 MB
// of named symbols + 88 transitive subpkgs) — the curated *Attrs structs
// already model exactly the JSON shape Graph returns, so the SDK's
// discriminator-driven model graph was pure overhead.
type graphClient struct {
	cred    tokenIssuer
	http    *http.Client
	baseURL string
}

func newGraphClient(cred tokenIssuer) *graphClient {
	return &graphClient{cred: cred, http: http.DefaultClient, baseURL: graphDefaultURL}
}

// graphErr surfaces Graph error bodies so reportEntraErr's substring
// classifier (Authorization_RequestDenied, Insufficient privileges, 401,
// 403) still matches against raw HTTP responses.
type graphErr struct {
	status int
	body   string
}

func (e *graphErr) Error() string {
	return fmt.Sprintf("graph: %d: %s", e.status, e.body)
}

// graphPage is the Graph collection envelope. Value is the per-call slice
// of entities; @odata.nextLink carries forward when results are paginated.
type graphPage[T any] struct {
	Value    []T    `json:"value"`
	NextLink string `json:"@odata.nextLink"`
}

func (g *graphClient) get(ctx context.Context, fullURL string, out any) error {
	tok, err := g.cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{graphScope}})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok.Token)
	req.Header.Set("Accept", "application/json")
	resp, err := g.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return &graphErr{status: resp.StatusCode, body: string(body)}
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// iterateGraph paginates through @odata.nextLink, calling fn for every
// entity. Returning false from fn stops the iteration. nextLink is
// absolute when present so we use it verbatim — no rewriting needed.
func iterateGraph[T any](ctx context.Context, g *graphClient, startURL string, fn func(T) bool) error {
	cur := startURL
	for cur != "" {
		var page graphPage[T]
		if err := g.get(ctx, cur, &page); err != nil {
			return err
		}
		for _, item := range page.Value {
			if !fn(item) {
				return nil
			}
		}
		cur = page.NextLink
	}
	return nil
}

// graphURL builds a Graph list-endpoint URL with $select + $top set so the
// response carries only the curated attribute fields and pages predictably.
func (g *graphClient) listURL(path string, fields []string) string {
	q := url.Values{}
	q.Set("$select", strings.Join(fields, ","))
	q.Set("$top", fmt.Sprintf("%d", graphPageSize))
	return g.baseURL + "/" + path + "?" + q.Encode()
}

// scanEntra discovers Entra ID (Azure AD) directory objects via Microsoft
// Graph: users, groups, service principals, and application registrations.
// Uses raw REST against graph.microsoft.com — the curated *Attrs structs
// below model exactly the JSON keys we ask for via $select.
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
	g := newGraphClient(cred)

	t, n := scanEntraUsers(ctx, g, tenantID, st, scanID)
	total += t
	inserted += n
	t, n = scanEntraGroups(ctx, g, tenantID, st, scanID)
	total += t
	inserted += n
	t, n = scanEntraServicePrincipals(ctx, g, tenantID, st, scanID)
	total += t
	inserted += n
	t, n = scanEntraApplications(ctx, g, tenantID, st, scanID)
	total += t
	inserted += n
	return total, inserted, nil
}

// userAttrs is the curated subset persisted under attributes for a Graph
// User. Stable shape so resolvers can index by upn / object id without
// depending on the full Graph payload. Field tags match Graph JSON keys
// 1:1 — same struct doubles as the unmarshal target for the REST response.
type userAttrs struct {
	ID                string `json:"id"`
	DisplayName       string `json:"displayName"`
	UserPrincipalName string `json:"userPrincipalName,omitempty"`
	Mail              string `json:"mail,omitempty"`
	AccountEnabled    *bool  `json:"accountEnabled,omitempty"`
	UserType          string `json:"userType,omitempty"`
}

func scanEntraUsers(ctx context.Context, g *graphClient, tenantID string, st *store.Store, scanID string) (total, inserted int) {
	startURL := g.listURL("users", []string{"id", "displayName", "userPrincipalName", "mail", "accountEnabled", "userType"})
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
	err := iterateGraph(ctx, g, startURL, func(u userAttrs) bool {
		if u.ID == "" {
			return true
		}
		name := u.DisplayName
		batch = append(batch, &store.Resource{
			Provider: "azure", AccountID: tenantID,
			Region: regionGlobal,
			Type:   TypeEntraUser, NativeID: u.ID,
			Name: &name, AttributesJSON: jsonOrEmpty(u),
			DiscoveredBy: scanID,
		})
		if len(batch) >= graphPageSize {
			flush()
		}
		return true
	})
	if err != nil {
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

func scanEntraGroups(ctx context.Context, g *graphClient, tenantID string, st *store.Store, scanID string) (total, inserted int) {
	startURL := g.listURL("groups", []string{"id", "displayName", "description", "mailEnabled", "securityEnabled", "groupTypes"})
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
	err := iterateGraph(ctx, g, startURL, func(gr groupAttrs) bool {
		if gr.ID == "" {
			return true
		}
		name := gr.DisplayName
		batch = append(batch, &store.Resource{
			Provider: "azure", AccountID: tenantID,
			Region: regionGlobal,
			Type:   TypeEntraGroup, NativeID: gr.ID,
			Name: &name, AttributesJSON: jsonOrEmpty(gr),
			DiscoveredBy: scanID,
		})
		if len(batch) >= graphPageSize {
			flush()
		}
		return true
	})
	if err != nil {
		reportEntraErr(st, "groups", err)
	}
	flush()
	return total, inserted
}

type spAttrs struct {
	ID                     string `json:"id"`
	AppID                  string `json:"appId,omitempty"`
	DisplayName            string `json:"displayName"`
	ServicePrincipalType   string `json:"servicePrincipalType,omitempty"`
	AccountEnabled         *bool  `json:"accountEnabled,omitempty"`
	AppOwnerOrganizationID string `json:"appOwnerOrganizationId,omitempty"`
}

// microsoftFirstPartyTenants are the Entra tenants Microsoft uses to host
// first-party / built-in service principals (Microsoft Graph, Azure CLI,
// Office 365 apps, etc.). A service-principal whose appOwnerOrganizationId
// matches is provider-managed: it appears automatically when the customer
// tenant consents to or uses the corresponding Microsoft service. Customer-
// authored apps (in their own tenant) and managed-identities (no
// appOwnerOrganizationId) fall through unmanaged.
var microsoftFirstPartyTenants = map[string]bool{
	"f8cdef31-a31e-4b4a-93e4-5f571e91255a": true, // Microsoft Services tenant — most first-party app SPs
	"72f988bf-86f1-41af-91ab-2d7cd011db47": true, // Microsoft corporate tenant
}

func isMicrosoftFirstPartySP(appOwnerTenantID string) bool {
	return microsoftFirstPartyTenants[strings.ToLower(appOwnerTenantID)]
}

func scanEntraServicePrincipals(ctx context.Context, g *graphClient, tenantID string, st *store.Store, scanID string) (total, inserted int) {
	startURL := g.listURL("servicePrincipals", []string{"id", "appId", "displayName", "servicePrincipalType", "accountEnabled", "appOwnerOrganizationId"})
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
	err := iterateGraph(ctx, g, startURL, func(sp spAttrs) bool {
		if sp.ID == "" {
			return true
		}
		name := sp.DisplayName
		batch = append(batch, &store.Resource{
			Provider: "azure", AccountID: tenantID,
			Region: regionGlobal,
			Type:   TypeEntraServicePrincipal, NativeID: sp.ID,
			Name: &name, AttributesJSON: jsonOrEmpty(sp),
			DiscoveredBy:      scanID,
			ManagedByProvider: isMicrosoftFirstPartySP(sp.AppOwnerOrganizationID),
		})
		if len(batch) >= graphPageSize {
			flush()
		}
		return true
	})
	if err != nil {
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

func scanEntraApplications(ctx context.Context, g *graphClient, tenantID string, st *store.Store, scanID string) (total, inserted int) {
	startURL := g.listURL("applications", []string{"id", "appId", "displayName", "signInAudience"})
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
	err := iterateGraph(ctx, g, startURL, func(a appAttrs) bool {
		if a.ID == "" {
			return true
		}
		name := a.DisplayName
		batch = append(batch, &store.Resource{
			Provider: "azure", AccountID: tenantID,
			Region: regionGlobal,
			Type:   TypeEntraApplication, NativeID: a.ID,
			Name: &name, AttributesJSON: jsonOrEmpty(a),
			DiscoveredBy: scanID,
		})
		if len(batch) >= graphPageSize {
			flush()
		}
		return true
	})
	if err != nil {
		reportEntraErr(st, "applications", err)
	}
	flush()
	return total, inserted
}

// reportEntraErr classifies a Graph error: missing-permission paths surface
// as ScanWarning + abort the entity, hard errors surface as ScanError. The
// substring classifier matches against the raw HTTP body that *graphErr.
// Error() exposes (Authorization_RequestDenied / Insufficient privileges
// JSON live there); for transport / parse errors it falls through to the
// hard-error branch.
func reportEntraErr(st *store.Store, scope string, err error) {
	raw := err.Error()
	if strings.Contains(raw, "Authorization_RequestDenied") ||
		strings.Contains(raw, "Insufficient privileges") ||
		strings.Contains(raw, " 401") || strings.Contains(raw, " 403") ||
		strings.Contains(raw, "graph: 401") || strings.Contains(raw, "graph: 403") {
		st.ReportWarning(store.ScanWarning{
			Provider: "azure", Service: "azure:microsoft.entra", Scope: scope, Message: raw,
		})
		return
	}
	st.ReportError(store.ScanError{
		Provider: "azure", Service: "azure:microsoft.entra", Scope: scope, Message: raw,
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
