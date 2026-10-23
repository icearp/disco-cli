package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentity"
	cognitoidentitytypes "github.com/aws/aws-sdk-go-v2/service/cognitoidentity/types"
	cognitoidp "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	cognitoidptypes "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeCognitoUserPool, Service: "cognito"})
	registerType(restype.Descriptor{
		Type: TypeCognitoAppClient, Service: "cognito",
		Redact: []redact.Rule{{Path: "ClientSecret", Mode: redact.RedactScalar}},
	})
	registerType(restype.Descriptor{Type: TypeCognitoIdentityPool, Service: "cognito"})
	registerType(restype.Descriptor{Type: TypeCognitoUserPoolDomain, Service: "cognito"})
	registerType(restype.Descriptor{Type: TypeCognitoUserPoolGroup, Service: "cognito"})
	registerType(restype.Descriptor{Type: TypeCognitoUserPoolIdentityProvider, Service: "cognito"})
	registerType(restype.Descriptor{Type: TypeCognitoUserPoolResourceServer, Service: "cognito"})
	registerType(restype.Descriptor{Type: TypeCognitoUserPoolRiskConfigurationAttachment, Service: "cognito"})
	registerType(restype.Descriptor{Type: TypeCognitoUserPoolUICustomizationAttachment, Service: "cognito"})
	registerType(restype.Descriptor{Type: TypeCognitoLogDeliveryConfiguration, Service: "cognito"})
	registerType(restype.Descriptor{Type: TypeCognitoTerms, Service: "cognito"})
	registerType(restype.Descriptor{Type: TypeCognitoIdentityPoolRoleAttachment, Service: "cognito"})
	registerType(restype.Descriptor{Type: TypeCognitoManagedLoginBranding, Service: "cognito"})
	registerType(restype.Descriptor{Type: TypeCognitoUserPoolReplica, Service: "cognito"})
	registerType(restype.Descriptor{
		Type: TypeCognitoUserPoolClientSecret, Service: "cognito",
		Redact: []redact.Rule{{Path: "ClientSecretValue", Mode: redact.RedactScalar}},
	})
	registerService(serviceEntry{
		name: "aws:cognito",
		fn:   scanCognito,
	})
}

// cognitoidpAPI is the narrow set of Cognito Identity Provider operations
// called by scanCognito. Distinct from cognitoidentityAPI — Cognito uses
// two separate SDK packages with disjoint method sets.
type cognitoidpAPI interface {
	ListUserPools(context.Context, *cognitoidp.ListUserPoolsInput, ...func(*cognitoidp.Options)) (*cognitoidp.ListUserPoolsOutput, error)
	DescribeUserPool(context.Context, *cognitoidp.DescribeUserPoolInput, ...func(*cognitoidp.Options)) (*cognitoidp.DescribeUserPoolOutput, error)
	ListUserPoolClients(context.Context, *cognitoidp.ListUserPoolClientsInput, ...func(*cognitoidp.Options)) (*cognitoidp.ListUserPoolClientsOutput, error)
	DescribeUserPoolClient(context.Context, *cognitoidp.DescribeUserPoolClientInput, ...func(*cognitoidp.Options)) (*cognitoidp.DescribeUserPoolClientOutput, error)
	DescribeManagedLoginBrandingByClient(context.Context, *cognitoidp.DescribeManagedLoginBrandingByClientInput, ...func(*cognitoidp.Options)) (*cognitoidp.DescribeManagedLoginBrandingByClientOutput, error)
	DescribeUserPoolDomain(context.Context, *cognitoidp.DescribeUserPoolDomainInput, ...func(*cognitoidp.Options)) (*cognitoidp.DescribeUserPoolDomainOutput, error)
	ListGroups(context.Context, *cognitoidp.ListGroupsInput, ...func(*cognitoidp.Options)) (*cognitoidp.ListGroupsOutput, error)
	ListIdentityProviders(context.Context, *cognitoidp.ListIdentityProvidersInput, ...func(*cognitoidp.Options)) (*cognitoidp.ListIdentityProvidersOutput, error)
	ListResourceServers(context.Context, *cognitoidp.ListResourceServersInput, ...func(*cognitoidp.Options)) (*cognitoidp.ListResourceServersOutput, error)
	DescribeRiskConfiguration(context.Context, *cognitoidp.DescribeRiskConfigurationInput, ...func(*cognitoidp.Options)) (*cognitoidp.DescribeRiskConfigurationOutput, error)
	GetUICustomization(context.Context, *cognitoidp.GetUICustomizationInput, ...func(*cognitoidp.Options)) (*cognitoidp.GetUICustomizationOutput, error)
	GetLogDeliveryConfiguration(context.Context, *cognitoidp.GetLogDeliveryConfigurationInput, ...func(*cognitoidp.Options)) (*cognitoidp.GetLogDeliveryConfigurationOutput, error)
	ListTerms(context.Context, *cognitoidp.ListTermsInput, ...func(*cognitoidp.Options)) (*cognitoidp.ListTermsOutput, error)
	ListUserPoolReplicas(context.Context, *cognitoidp.ListUserPoolReplicasInput, ...func(*cognitoidp.Options)) (*cognitoidp.ListUserPoolReplicasOutput, error)
	ListUserPoolClientSecrets(context.Context, *cognitoidp.ListUserPoolClientSecretsInput, ...func(*cognitoidp.Options)) (*cognitoidp.ListUserPoolClientSecretsOutput, error)
}

// cognitoidentityAPI is the narrow set of Cognito Identity (federated
// identity pools) operations called by scanCognito.
type cognitoidentityAPI interface {
	ListIdentityPools(context.Context, *cognitoidentity.ListIdentityPoolsInput, ...func(*cognitoidentity.Options)) (*cognitoidentity.ListIdentityPoolsOutput, error)
	DescribeIdentityPool(context.Context, *cognitoidentity.DescribeIdentityPoolInput, ...func(*cognitoidentity.Options)) (*cognitoidentity.DescribeIdentityPoolOutput, error)
	GetIdentityPoolRoles(context.Context, *cognitoidentity.GetIdentityPoolRolesInput, ...func(*cognitoidentity.Options)) (*cognitoidentity.GetIdentityPoolRolesOutput, error)
}

// scanCognito discovers Cognito user pools, user-pool app clients, and
// identity pools in one region. User pools and their app clients are
// list-then-described concurrently. Identity pools are paired with their role
// mappings via GetIdentityPoolRoles for the resolver.
func scanCognito(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	idpClient := cognitoidp.NewFromConfig(acct.cfg, func(o *cognitoidp.Options) { o.Region = region })
	idClient := cognitoidentity.NewFromConfig(acct.cfg, func(o *cognitoidentity.Options) { o.Region = region })
	return scanCognitoAll(ctx, idpClient, idClient, acct, region, st, scanID)
}

const (
	cognitoListPoolsOp      = "cognito-idp:ListUserPools"
	cognitoDescribePoolOp   = "cognito-idp:DescribeUserPool"
	cognitoListClientsOp    = "cognito-idp:ListUserPoolClients"
	cognitoListIDPoolsOp    = "cognito-identity:ListIdentityPools"
	cognitoDescribeIDPoolOp = "cognito-identity:DescribeIdentityPool"
	cognitoIDPoolRolesOp    = "cognito-identity:GetIdentityPoolRoles"
)

// scanCognitoAll holds the testable scan body — accepts both narrow ifaces
// so unit tests can swap in stubs without touching production wiring.
//
// Every phase runs whatever an earlier one returned, except after a failed
// store write: the later phases get the parents the earlier ones stored. The
// counts are summed and the errors joined through phaseTotals, which demotes
// a transient error to a warning only beside a hard one.
func scanCognitoAll(ctx context.Context, idpClient cognitoidpAPI, idClient cognitoidentityAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var p phaseTotals
	pools, t, i, err := scanCognitoUserPoolsAndClients(ctx, idpClient, acct, region, st, scanID)
	if p.addPhase("cognito-idp", t, i, err) {
		return p.result(st, acct, region)
	}
	idPoolIDs, t, i, err := scanCognitoIdentityPools(ctx, idClient, acct, region, st, scanID)
	if p.addPhase("cognito-identity", t, i, err) {
		return p.result(st, acct, region)
	}
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) {
			return scanCognitoExtended(ctx, idpClient, idClient, acct, region, st, scanID, pools.poolIDs(), idPoolIDs)
		},
		func() (int, int, error) {
			return scanCognitoUserPoolReplicas(ctx, idpClient, acct, region, st, scanID, pools.replicated)
		},
		func() (int, int, error) {
			return scanCognitoUserPoolClientSecrets(ctx, idpClient, acct, region, st, scanID, pools.confidentialClients)
		},
	} {
		t, i, err := phase()
		if p.addPhase("cognito-idp", t, i, err) {
			break
		}
	}
	return p.result(st, acct, region)
}

// listCognitoUserPools reads every ListUserPools page; on a failed page it
// returns the pools read before it with the error.
func listCognitoUserPools(ctx context.Context, idpClient cognitoidpAPI) ([]cognitoidptypes.UserPoolDescriptionType, error) {
	return readTokenPages(ctx, func(ctx context.Context, token *string) ([]cognitoidptypes.UserPoolDescriptionType, *string, error) {
		out, err := idpClient.ListUserPools(ctx, &cognitoidp.ListUserPoolsInput{MaxResults: aws.Int32(60), NextToken: token})
		if err != nil {
			return nil, nil, laterPageErr(token, "user pools", err)
		}
		return out.UserPools, out.NextToken, nil
	})
}

// laterPageErr says, for a listing that failed on a page after the first,
// that what and their children on that page and later ones were not read.
func laterPageErr(token *string, what string, err error) error {
	if token == nil {
		return err
	}
	return fmt.Errorf("the listing stopped at a page after the first; %s on it and later pages, and their children, were not read: %w", what, err)
}

// cognitoPoolCreatedIn reports whether the pool ID's "<region>_" prefix names
// region. ReplicaRegions may also be reported on a secondary replica's pool,
// whose ListUserPoolReplicas may be refused; only a pool created in this
// region is taken to be a primary. Inferred from the pool ID format, pending
// a live check of how a secondary's ID and ListUserPools entry look.
func cognitoPoolCreatedIn(poolID, region string) bool {
	prefix, _, ok := strings.Cut(poolID, "_")
	return ok && prefix == region
}

// cognitoStoredParents is what the pool phase stored: each user pool (id =
// pool ID, arn = its NativeID), the stored pools that ListUserPools reported
// replicas for, and each app client that has a secret. Only those clients are
// asked for client secrets: the docs describe secrets as belonging to
// confidential clients, inferred here from a non-empty ClientSecret on
// DescribeUserPoolClient. Unverified for clients managed with the
// multi-secret APIs, which might not populate that field.
type cognitoStoredParents struct {
	pools               []childParent
	replicated          []childParent
	confidentialClients []cognitoAppClientRef
}

func (s cognitoStoredParents) poolIDs() []string {
	ids := make([]string, len(s.pools))
	for i, p := range s.pools {
		ids[i] = p.id
	}
	return ids
}

// cognitoAppClientRef is a stored app client: ListUserPoolClientSecrets keys
// on both the pool ID and the client ID.
type cognitoAppClientRef struct {
	poolID   string
	clientID string
	arn      string
}

// cognitoPoolBatches collects the app-client and branding rows from the
// pool phase's per-pool goroutines.
type cognitoPoolBatches struct {
	mu                  sync.Mutex
	clients             []*store.Resource
	brandings           []*store.Resource
	confidentialClients []cognitoAppClientRef
}

// cognitoKeptReads tallies, for one per-item op, the failed reads whose item
// was still stored, without what the op would have added: denials and
// transient failures each in one tally, any other error kept with its item.
type cognitoKeptReads struct {
	op                string
	denied, transient skipTally
	errs              []error
}

func (k *cognitoKeptReads) add(key string, err error) {
	switch {
	case err == nil:
	case isAccessDenied(err):
		k.denied.skipped(key, err)
	case isTransientNetworkError(err):
		k.transient.skipped(key, err)
	default:
		k.errs = append(k.errs, fmt.Errorf("%s %s: %w", k.op, key, err))
	}
}

// warnDenied warns once for the denied reads; kept says how their items were
// stored. Call it only after the items were written.
func (k *cognitoKeptReads) warnDenied(st *store.Store, acct *account, region, kept string) {
	if k.denied.count > 0 {
		_ = skipIfAccessDenied(st, k.op, acct.ID, region, fmt.Errorf("%d %s, first %s: %w", k.denied.count, kept, k.denied.first, k.denied.err))
	}
}

// addFailed adds the reads' errors other than denials to errs under op, the
// transient ones as one error of their own, so that errs.join demotes it to a
// warning beside a hard error instead of burying it in the join; kept says how
// their items were stored. Call it only after the items were written.
func (k *cognitoKeptReads) addFailed(errs *phaseErrs, kept string) {
	errs.add(k.op, errors.Join(k.errs...))
	if k.transient.count > 0 {
		errs.add(k.op, fmt.Errorf("%d %s, first %s: %w", k.transient.count, kept, k.transient.first, k.transient.err))
	}
}

// cognitoPoolRead is what one listed pool's reads returned: its row (nil when
// DescribeUserPool no longer finds the pool), the DescribeUserPool error the
// row was built despite, whether DescribeUserPool succeeded with no UserPool,
// and the app-client listing's error.
type cognitoPoolRead struct {
	row                     *store.Resource
	describeErr, clientsErr error
	noBody                  bool
}

// scanCognitoUserPoolsAndClients stores every listed user pool with its app
// clients and their branding, those read before a failed ListUserPools page
// included, and returns the stored parents (none after a failed write). Each
// pool is stored from its DescribeUserPool body, the only source of its tags.
// A pool DescribeUserPool no longer finds (ResourceNotFoundException, in the
// op's model) was deleted since it was listed: no row, no error. A pool whose
// DescribeUserPool fails any other way is stored as its ListUserPools element,
// without tags, and its app clients are still read: a denial is warned about
// once; every other error is returned after the rows are stored, except that a
// transient one beside a hard error is warned about under its op. A pool whose
// DescribeUserPool returns no UserPool is stored the same way and warned
// about. A listed pool with no Id is not stored and is warned about.
func scanCognitoUserPoolsAndClients(ctx context.Context, idpClient cognitoidpAPI, acct *account, region string, st *store.Store, scanID string) (cognitoStoredParents, int, int, error) {
	listed, listErr := listCognitoUserPools(ctx, idpClient)
	var (
		keyed   []cognitoidptypes.UserPoolDescriptionType
		unkeyed skipTally
	)
	for _, p := range listed {
		if sv(p.Id) == "" {
			unkeyed.skipped(sv(p.Name), nil)
			continue
		}
		keyed = append(keyed, p)
	}
	var b cognitoPoolBatches
	reads, stopErr := readEachKey(ctx, keyed, func(ctx context.Context, p cognitoidptypes.UserPoolDescriptionType) cognitoPoolRead {
		return readCognitoUserPool(ctx, idpClient, acct, region, scanID, p, &b)
	})
	var (
		stored     cognitoStoredParents
		rows       []*store.Resource
		errs       []error
		clientErrs []error
	)
	if stopErr != nil {
		errs = append(errs, fmt.Errorf("%s: %w", cognitoDescribePoolOp, stopErr))
	}
	described := cognitoKeptReads{op: cognitoDescribePoolOp}
	var noBody skipTally
	for i, r := range reads {
		if r.row == nil {
			continue
		}
		p := keyed[i]
		rows = append(rows, r.row)
		parent := childParent{id: sv(p.Id), arn: r.row.NativeID}
		stored.pools = append(stored.pools, parent)
		if len(p.ReplicaRegions) > 0 && cognitoPoolCreatedIn(parent.id, region) {
			stored.replicated = append(stored.replicated, parent)
		}
		described.add(parent.id, r.describeErr)
		if r.noBody {
			noBody.skipped(parent.id, nil)
		}
		clientErrs = append(clientErrs, r.clientsErr)
	}
	rows = append(append(rows, b.clients...), b.brandings...)
	const kept = "user pool(s) stored as their ListUserPools element, without tags"
	total, inserted, err := storeReadRows(st, acct, region, cognitoListPoolsOp, rows, listErr, func() {
		described.warnDenied(st, acct, region, kept)
		if noBody.count > 0 {
			st.ReportWarning(store.ScanWarning{
				Provider: "aws", Service: cognitoDescribePoolOp, Scope: acct.ID + "/" + region,
				Message: fmt.Sprintf("%d %s: DescribeUserPool returned no UserPool, first %s", noBody.count, kept, noBody.first),
			})
		}
		warnNotStored(st, cognitoListPoolsOp, acct, region, &unkeyed, "user pools have an empty Id")
	}, errs...)
	if errors.Is(err, store.ErrStoreWrite) {
		return cognitoStoredParents{}, total, inserted, errors.Join(append([]error{err}, clientErrs...)...)
	}
	stored.confidentialClients = b.confidentialClients
	var phase phaseErrs
	phase.add(cognitoListPoolsOp, err)
	phase.add(cognitoListClientsOp, errors.Join(clientErrs...))
	described.addFailed(&phase, kept)
	return stored, total, inserted, phase.join(st, acct, region)
}

// readCognitoUserPool describes one listed pool and builds its row, then
// collects its app clients into b unless the pool is gone.
func readCognitoUserPool(ctx context.Context, idpClient cognitoidpAPI, acct *account, region, scanID string, listed cognitoidptypes.UserPoolDescriptionType, b *cognitoPoolBatches) cognitoPoolRead {
	poolID := sv(listed.Id)
	out, err := idpClient.DescribeUserPool(ctx, &cognitoidp.DescribeUserPoolInput{UserPoolId: &poolID})
	if isAPIErrorCode(err, "ResourceNotFoundException") {
		return cognitoPoolRead{}
	}
	var described *cognitoidptypes.UserPoolType
	if err == nil {
		described = out.UserPool
	}
	return cognitoPoolRead{
		row:         cognitoUserPoolRow(acct, region, scanID, listed, described),
		describeErr: err,
		noBody:      err == nil && described == nil,
		clientsErr:  collectCognitoAppClients(ctx, idpClient, acct, region, scanID, poolID, b),
	}
}

// cognitoUserPoolRow builds a pool's row from its DescribeUserPool body, or
// from its ListUserPools element when described is nil.
func cognitoUserPoolRow(acct *account, region, scanID string, listed cognitoidptypes.UserPoolDescriptionType, described *cognitoidptypes.UserPoolType) *store.Resource {
	r := &store.Resource{
		Provider:       "aws",
		AccountID:      acct.ID,
		AccountName:    &acct.Name,
		Type:           TypeCognitoUserPool,
		NativeID:       fmt.Sprintf("arn:aws:cognito-idp:%s:%s:userpool/%s", region, acct.ID, sv(listed.Id)),
		Name:           listed.Name,
		Region:         &region,
		AttributesJSON: mustJSON(listed),
		DiscoveredBy:   scanID,
	}
	if described == nil {
		return r
	}
	if arn := sv(described.Arn); arn != "" {
		r.NativeID = arn
	}
	r.Name, r.AttributesJSON, r.TagsJSON = described.Name, mustJSON(described), mapTagsJSON(described.UserPoolTags)
	return r
}

func collectCognitoAppClients(ctx context.Context, idpClient cognitoidpAPI, acct *account, region, scanID, poolID string, b *cognitoPoolBatches) error {
	cPager := cognitoidp.NewListUserPoolClientsPaginator(idpClient, &cognitoidp.ListUserPoolClientsInput{
		UserPoolId: &poolID,
		MaxResults: aws.Int32(60),
	})
	for cPager.HasMorePages() {
		cOut, err := cPager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil
			}
			return fmt.Errorf("cognito-idp:ListUserPoolClients pool=%s: %w", poolID, err)
		}
		for _, c := range cOut.UserPoolClients {
			clientID := sv(c.ClientId)
			descOut, err := idpClient.DescribeUserPoolClient(ctx, &cognitoidp.DescribeUserPoolClientInput{
				UserPoolId: &poolID,
				ClientId:   &clientID,
			})
			if err != nil {
				continue
			}
			arn := fmt.Sprintf("arn:aws:cognito-idp:%s:%s:userpool/%s/client/%s", region, acct.ID, poolID, clientID)
			cr := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeCognitoAppClient,
				NativeID:       arn,
				Name:           descOut.UserPoolClient.ClientName,
				Region:         &region,
				AttributesJSON: mustJSON(descOut.UserPoolClient),
				DiscoveredBy:   scanID,
			}
			b.mu.Lock()
			b.clients = append(b.clients, cr)
			// Inferred confidential-client gate; see cognitoStoredParents.
			if sv(descOut.UserPoolClient.ClientSecret) != "" {
				b.confidentialClients = append(b.confidentialClients, cognitoAppClientRef{poolID: poolID, clientID: clientID, arn: arn})
			}
			b.mu.Unlock()
			collectCognitoManagedLoginBranding(ctx, idpClient, acct, region, scanID, poolID, clientID, b)
		}
	}
	return nil
}

// collectCognitoManagedLoginBranding fetches the per-app-client branding
// row, silently skipping NoSuchManagedLoginBranding (default state when
// the client has no branding configured).
func collectCognitoManagedLoginBranding(ctx context.Context, idpClient cognitoidpAPI, acct *account, region, scanID, poolID, clientID string, b *cognitoPoolBatches) {
	mlbOut, mlbErr := idpClient.DescribeManagedLoginBrandingByClient(ctx, &cognitoidp.DescribeManagedLoginBrandingByClientInput{
		UserPoolId: &poolID,
		ClientId:   &clientID,
	})
	if mlbErr != nil || mlbOut.ManagedLoginBranding == nil {
		return
	}
	mlbID := sv(mlbOut.ManagedLoginBranding.ManagedLoginBrandingId)
	if mlbID == "" {
		return
	}
	mlbArn := fmt.Sprintf("arn:aws:cognito-idp:%s:%s:userpool/%s/branding/%s", region, acct.ID, poolID, mlbID)
	mlbLabel := mlbID
	b.mu.Lock()
	b.brandings = append(b.brandings, &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeCognitoManagedLoginBranding, NativeID: mlbArn,
		Name: &mlbLabel, Region: &region,
		AttributesJSON: mustJSON(mlbOut.ManagedLoginBranding), DiscoveredBy: scanID,
	})
	b.mu.Unlock()
}

// idPoolAttrs is an identity pool's AttributesJSON: Pool is its
// DescribeIdentityPool body, or its ListIdentityPools element when that read
// failed, and Roles its GetIdentityPoolRoles role map.
type idPoolAttrs struct {
	Pool  any               `json:"Pool"`
	Roles map[string]string `json:"Roles"`
}

// cognitoIDPoolRead is what one listed identity pool's reads returned; gone
// is set when either read no longer found the pool.
type cognitoIDPoolRead struct {
	described             *cognitoidentity.DescribeIdentityPoolOutput
	roles                 map[string]string
	describeErr, rolesErr error
	gone                  bool
}

// scanCognitoIdentityPools stores every listed identity pool, those read
// before a failed ListIdentityPools page included, and returns the IDs of the
// pools stored (none after a failed write). Each pool is stored from its
// DescribeIdentityPool body, the only source of its tags, with its
// GetIdentityPoolRoles role map. A pool either read no longer finds
// (ResourceNotFoundException, in both ops' models) was deleted since it was
// listed: no row, no error. A pool whose DescribeIdentityPool fails any other
// way is stored as its ListIdentityPools element, without tags; one whose
// GetIdentityPoolRoles fails is stored without Roles. A denial is warned about
// once per op; every other error is returned after the rows are stored, except
// that a transient one beside a hard error is warned about under its op. A
// listed pool with no IdentityPoolId is not stored and is warned about.
func scanCognitoIdentityPools(ctx context.Context, idClient cognitoidentityAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	listed, listErr := readTokenPages(ctx, func(ctx context.Context, token *string) ([]cognitoidentitytypes.IdentityPoolShortDescription, *string, error) {
		out, err := idClient.ListIdentityPools(ctx, &cognitoidentity.ListIdentityPoolsInput{MaxResults: aws.Int32(60), NextToken: token})
		if err != nil {
			return nil, nil, laterPageErr(token, "identity pools", err)
		}
		return out.IdentityPools, out.NextToken, nil
	})
	var (
		keyed   []cognitoidentitytypes.IdentityPoolShortDescription
		unkeyed skipTally
	)
	for _, p := range listed {
		if sv(p.IdentityPoolId) == "" {
			unkeyed.skipped(sv(p.IdentityPoolName), nil)
			continue
		}
		keyed = append(keyed, p)
	}
	reads, stopErr := readEachKey(ctx, keyed, func(ctx context.Context, p cognitoidentitytypes.IdentityPoolShortDescription) cognitoIDPoolRead {
		return readCognitoIdentityPool(ctx, idClient, sv(p.IdentityPoolId))
	})
	var (
		ids  []string
		rows []*store.Resource
		errs []error
	)
	if stopErr != nil {
		errs = append(errs, fmt.Errorf("%s: %w", cognitoDescribeIDPoolOp, stopErr))
	}
	described := cognitoKeptReads{op: cognitoDescribeIDPoolOp}
	roles := cognitoKeptReads{op: cognitoIDPoolRolesOp}
	for i, r := range reads {
		if r.gone {
			continue
		}
		p := keyed[i]
		ipID := sv(p.IdentityPoolId)
		ids = append(ids, ipID)
		rows = append(rows, cognitoIdentityPoolRow(acct, region, scanID, p, r))
		described.add(ipID, r.describeErr)
		roles.add(ipID, r.rolesErr)
	}
	const (
		keptListed  = "identity pool(s) stored as their ListIdentityPools element, without tags"
		keptNoRoles = "identity pool(s) stored without Roles"
	)
	total, inserted, err := storeReadRows(st, acct, region, cognitoListIDPoolsOp, rows, listErr, func() {
		described.warnDenied(st, acct, region, keptListed)
		roles.warnDenied(st, acct, region, keptNoRoles)
		warnNotStored(st, cognitoListIDPoolsOp, acct, region, &unkeyed, "identity pools have an empty IdentityPoolId")
	}, errs...)
	if errors.Is(err, store.ErrStoreWrite) {
		return nil, total, inserted, err
	}
	var phase phaseErrs
	phase.add(cognitoListIDPoolsOp, err)
	described.addFailed(&phase, keptListed)
	roles.addFailed(&phase, keptNoRoles)
	return ids, total, inserted, phase.join(st, acct, region)
}

// readCognitoIdentityPool reads one listed identity pool's description and
// role map, stopping once either read no longer finds the pool.
func readCognitoIdentityPool(ctx context.Context, idClient cognitoidentityAPI, ipID string) cognitoIDPoolRead {
	var r cognitoIDPoolRead
	r.described, r.describeErr = idClient.DescribeIdentityPool(ctx, &cognitoidentity.DescribeIdentityPoolInput{IdentityPoolId: &ipID})
	if isAPIErrorCode(r.describeErr, "ResourceNotFoundException") {
		return cognitoIDPoolRead{gone: true}
	}
	out, err := idClient.GetIdentityPoolRoles(ctx, &cognitoidentity.GetIdentityPoolRolesInput{IdentityPoolId: &ipID})
	switch {
	case err == nil:
		r.roles = out.Roles
	case isAPIErrorCode(err, "ResourceNotFoundException"):
		return cognitoIDPoolRead{gone: true}
	default:
		r.rolesErr = err
	}
	return r
}

// cognitoIdentityPoolRow builds a pool's row from its reads, falling back to
// its ListIdentityPools element when DescribeIdentityPool failed.
func cognitoIdentityPoolRow(acct *account, region, scanID string, listed cognitoidentitytypes.IdentityPoolShortDescription, r cognitoIDPoolRead) *store.Resource {
	attrs := idPoolAttrs{Pool: listed, Roles: r.roles}
	name := listed.IdentityPoolName
	var tags *string
	if r.describeErr == nil {
		attrs.Pool = json.RawMessage(mustJSON(r.described))
		name, tags = r.described.IdentityPoolName, mapTagsJSON(r.described.IdentityPoolTags)
	}
	return &store.Resource{
		Provider:       "aws",
		AccountID:      acct.ID,
		AccountName:    &acct.Name,
		Type:           TypeCognitoIdentityPool,
		NativeID:       fmt.Sprintf("arn:aws:cognito-identity:%s:%s:identitypool/%s", region, acct.ID, sv(listed.IdentityPoolId)),
		Name:           name,
		Region:         &region,
		AttributesJSON: mustJSON(attrs),
		TagsJSON:       tags,
		DiscoveredBy:   scanID,
	}
}
