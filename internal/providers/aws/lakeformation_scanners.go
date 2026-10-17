package aws

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/lakeformation"
	lftypes "github.com/aws/aws-sdk-go-v2/service/lakeformation/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeLakeFormationResource, Service: "lakeformation"})
	registerType(restype.Descriptor{Type: TypeLakeFormationDataCellsFilter, Service: "lakeformation"})
	registerType(restype.Descriptor{Type: TypeLakeFormationDataLakeSettings, Service: "lakeformation", Managed: true})
	registerType(restype.Descriptor{Type: TypeLakeFormationPrincipalPermissions, Service: "lakeformation"})
	registerType(restype.Descriptor{Type: TypeLakeFormationTag, Service: "lakeformation"})
	registerType(restype.Descriptor{Type: TypeLakeFormationOptIn, Service: "lakeformation"})
	registerService(serviceEntry{
		name: "aws:lakeformation",
		fn:   scanLakeFormation,
	})
}

// lakeformationAPI is the narrow set of Lake Formation operations called by
// scanLakeFormationResources.
type lakeformationAPI interface {
	ListResources(context.Context, *lakeformation.ListResourcesInput, ...func(*lakeformation.Options)) (*lakeformation.ListResourcesOutput, error)
	ListDataCellsFilter(context.Context, *lakeformation.ListDataCellsFilterInput, ...func(*lakeformation.Options)) (*lakeformation.ListDataCellsFilterOutput, error)
	GetDataLakeSettings(context.Context, *lakeformation.GetDataLakeSettingsInput, ...func(*lakeformation.Options)) (*lakeformation.GetDataLakeSettingsOutput, error)
	ListPermissions(context.Context, *lakeformation.ListPermissionsInput, ...func(*lakeformation.Options)) (*lakeformation.ListPermissionsOutput, error)
	ListLFTags(context.Context, *lakeformation.ListLFTagsInput, ...func(*lakeformation.Options)) (*lakeformation.ListLFTagsOutput, error)
	ListLakeFormationOptIns(context.Context, *lakeformation.ListLakeFormationOptInsInput, ...func(*lakeformation.Options)) (*lakeformation.ListLakeFormationOptInsOutput, error)
}

// scanLakeFormation discovers Lake Formation registered data locations,
// data-cells filters, data-lake settings, permissions, LF-tags and opt-ins in
// one region. ListResources returns the full ResourceInfo body (RoleArn,
// ResourceArn, federation flags) so no Describe fan-out needed.
func scanLakeFormation(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := lakeformation.NewFromConfig(acct.cfg, func(o *lakeformation.Options) { o.Region = region })
	return scanLakeFormationAll(ctx, client, acct, region, st, scanID)
}

// scanLakeFormationAll runs the catalog phases, which stop at their first hard
// error, then the opt-in phase, so a failure on either side costs the other no
// rows. Each side's transient error is demoted to a warning: one transient
// member would make the dispatcher read the whole join as transient, hiding the
// other side's hard error and discarding every phase's counts.
func scanLakeFormationAll(ctx context.Context, client lakeformationAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	total, inserted, err := scanLakeFormationCatalog(ctx, client, acct, region, st, scanID)
	t, i, oerr := scanLFOptIns(ctx, client, acct, region, st, scanID)
	return total + t, inserted + i, errors.Join(
		demoteTransient(st, "lakeformation", acct, region, err),
		demoteTransient(st, lfOptInOp, acct, region, oerr),
	)
}

func scanLakeFormationCatalog(ctx context.Context, client lakeformationAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	t, i, ferr := scanLakeFormationResources(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	t, i, ferr = scanLakeFormationExtended(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

// scanLakeFormationResources holds the testable scan body.
func scanLakeFormationResources(ctx context.Context, client lakeformationAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := lakeformation.NewListResourcesPaginator(client, &lakeformation.ListResourcesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "lakeformation:ListResources", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("lakeformation:ListResources: %w", perr)
		}
		for _, r := range out.ResourceInfoList {
			arn := sv(r.ResourceArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeLakeFormationResource,
				NativeID:       arn,
				Region:         &region,
				AttributesJSON: mustJSON(r),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert lakeformation resources: %w", uerr)
	}
	return len(batch), n, nil
}

// lfOptInPageLimit is the model's PageSize maximum for ListLakeFormationOptIns.
const lfOptInPageLimit = 1000

// scanLFOptIns lists the principal-resource pairs that opted in to Lake
// Formation permission enforcement. A denial or error after some pages keeps
// the rows of the pages already listed.
func scanLFOptIns(ctx context.Context, client lakeformationAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := lakeformation.NewListLakeFormationOptInsPaginator(client, &lakeformation.ListLakeFormationOptInsInput{},
		func(o *lakeformation.ListLakeFormationOptInsPaginatorOptions) {
			o.Limit = lfOptInPageLimit
			o.StopOnDuplicateToken = true
		})
	var rows []containedChild
	var err error
pages:
	for pager.HasMorePages() {
		var out *lakeformation.ListLakeFormationOptInsOutput
		if out, err = pager.NextPage(ctx); err != nil {
			break
		}
		for _, o := range out.LakeFormationOptInsInfoList {
			if o.Principal == nil || o.Resource == nil {
				continue
			}
			principal := sv(o.Principal.DataLakePrincipalIdentifier)
			if principal == "" {
				continue
			}
			id, kerr := lfOptInARN(region, acct.ID, o)
			if kerr != nil { // unreachable in practice: these SDK types always encode
				err = kerr
				break pages
			}
			rows = append(rows, containedChild{row: &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLakeFormationOptIn, NativeID: id,
				Name: &principal, Region: &region,
				AttributesJSON: mustJSON(o), DiscoveredBy: scanID,
			}})
		}
	}
	return upsertRegionChildren(st, acct, region, lfOptInOp, rows, err)
}

const lfOptInOp = "lakeformation:ListLakeFormationOptIns"

// lfOptInARN synthesizes the NativeID of an opt-in, which has no AWS-issued id
// or ARN. CreateLakeFormationOptIn and DeleteLakeFormationOptIn both address an
// opt-in by Principal, Resource and Condition, and no op updates one, so the
// hash covers exactly those three and leaves out the LastModified and
// LastUpdatedBy metadata. It hashes their canonical JSON (lfCanonical), so an
// optional member a module bump adds, or a reordered list, keeps the id.
func lfOptInARN(region, accountID string, o lftypes.LakeFormationOptInsInfo) (string, error) {
	raw, err := json.Marshal(struct {
		Principal *lftypes.DataLakePrincipal
		Resource  *lftypes.Resource
		Condition *lftypes.Condition
	}{o.Principal, o.Resource, o.Condition})
	if err != nil {
		return "", fmt.Errorf("encode lakeformation opt-in key: %w", err)
	}
	var key any
	if err := json.Unmarshal(raw, &key); err != nil {
		return "", fmt.Errorf("decode lakeformation opt-in key: %w", err)
	}
	if key, err = lfCanonical(key); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(key)
	if err != nil {
		return "", fmt.Errorf("encode canonical lakeformation opt-in key: %w", err)
	}
	sum := sha256.Sum256(canonical)
	return fmt.Sprintf("arn:aws:lakeformation:%s:%s:lake-formation-opt-in/%s", region, accountID, hex.EncodeToString(sum[:8])), nil
}

// lfCanonical rewrites a decoded JSON value in place so that absent members
// and list order do not affect it. Untagged SDK structs marshal every nil
// member as null and every unset optional enum (a non-pointer string in
// smithy-go) as "", so a member added by a module bump would otherwise change
// every id. Nulls, empty strings and empty lists are dropped; false and 0 are
// kept, and so are empty objects, because Lake Formation uses them as markers
// (TableWildcard, ColumnWildcard, Catalog). Every list in the key is a set
// (ColumnNames, ExcludedColumnNames, TagValues) or an AND of conditions
// (LFTagPolicy.Expression), so each is sorted by its elements' canonical JSON;
// json.Marshal already sorts map keys.
func lfCanonical(v any) (any, error) {
	switch t := v.(type) {
	case map[string]any:
		for k, e := range t {
			if lfAbsent(e) {
				delete(t, k)
				continue
			}
			c, err := lfCanonical(e)
			if err != nil {
				return nil, err
			}
			t[k] = c
		}
	case []any:
		type keyed struct {
			key string
			val any
		}
		elems := make([]keyed, len(t))
		for i, e := range t {
			c, err := lfCanonical(e)
			if err != nil {
				return nil, err
			}
			b, err := json.Marshal(c)
			if err != nil {
				return nil, fmt.Errorf("encode lakeformation opt-in key element: %w", err)
			}
			elems[i] = keyed{string(b), c}
		}
		slices.SortFunc(elems, func(a, b keyed) int { return strings.Compare(a.key, b.key) })
		for i, e := range elems {
			t[i] = e.val
		}
	}
	return v, nil
}

func lfAbsent(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return t == ""
	case []any:
		return len(t) == 0
	}
	return false
}
