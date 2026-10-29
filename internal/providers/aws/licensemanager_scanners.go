package aws

import (
	"context"
	"errors"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/service/licensemanager"
	lmtypes "github.com/aws/aws-sdk-go-v2/service/licensemanager/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

// isLicenseManagerNotSetUp distinguishes the "Service role not found" setup gap
// from a real IAM denial. License Manager requires the
// AWSServiceRoleForAWSLicenseManagerRole; its absence surfaces as
// AccessDeniedException with that same message.
func isLicenseManagerNotSetUp(err error) bool {
	return isAccessDeniedWithMessage(err, "Service role not found")
}

func init() {
	registerType(restype.Descriptor{Type: TypeLicenseManagerLicense, Service: "license-manager"})
	registerType(restype.Descriptor{Type: TypeLicenseManagerGrant, Service: "license-manager"})
	registerType(restype.Descriptor{Type: TypeLicenseManagerLicenseConfiguration, Service: "license-manager"})
	registerType(restype.Descriptor{Type: TypeLicenseManagerReportGenerator, Service: "license-manager"})
	registerType(restype.Descriptor{Type: TypeLicenseManagerLicenseAssetGroup, Service: "license-manager"})
	registerType(restype.Descriptor{Type: TypeLicenseManagerLicenseAssetRuleset, Service: "license-manager"})
	registerType(restype.Descriptor{Type: TypeLicenseManagerReceivedLicense, Service: "license-manager"})
	registerType(restype.Descriptor{Type: TypeLicenseManagerToken, Service: "license-manager"})
	registerService(serviceEntry{
		name:   "aws:license-manager",
		global: true,
		fn:     scanLicenseManager,
	})
}

type licenseManagerAPI interface {
	ListLicenses(context.Context, *licensemanager.ListLicensesInput, ...func(*licensemanager.Options)) (*licensemanager.ListLicensesOutput, error)
	ListDistributedGrants(context.Context, *licensemanager.ListDistributedGrantsInput, ...func(*licensemanager.Options)) (*licensemanager.ListDistributedGrantsOutput, error)
	ListLicenseConfigurations(context.Context, *licensemanager.ListLicenseConfigurationsInput, ...func(*licensemanager.Options)) (*licensemanager.ListLicenseConfigurationsOutput, error)
	ListLicenseManagerReportGenerators(context.Context, *licensemanager.ListLicenseManagerReportGeneratorsInput, ...func(*licensemanager.Options)) (*licensemanager.ListLicenseManagerReportGeneratorsOutput, error)
	ListLicenseAssetGroups(context.Context, *licensemanager.ListLicenseAssetGroupsInput, ...func(*licensemanager.Options)) (*licensemanager.ListLicenseAssetGroupsOutput, error)
	ListLicenseAssetRulesets(context.Context, *licensemanager.ListLicenseAssetRulesetsInput, ...func(*licensemanager.Options)) (*licensemanager.ListLicenseAssetRulesetsOutput, error)
	ListReceivedLicenses(context.Context, *licensemanager.ListReceivedLicensesInput, ...func(*licensemanager.Options)) (*licensemanager.ListReceivedLicensesOutput, error)
	ListTokens(context.Context, *licensemanager.ListTokensInput, ...func(*licensemanager.Options)) (*licensemanager.ListTokensOutput, error)
	ListTagsForResource(context.Context, *licensemanager.ListTagsForResourceInput, ...func(*licensemanager.Options)) (*licensemanager.ListTagsForResourceOutput, error)
}

// lmPageSize is the maximum MaxResults (1-100) of ListLicenses,
// ListDistributedGrants, ListLicenseManagerReportGenerators,
// ListReceivedLicenses and ListTokens. ListLicenseConfigurations,
// ListLicenseAssetGroups and ListLicenseAssetRulesets model no range for it,
// so they are sent none.
const lmPageSize = 100

// scanLicenseManager discovers License Manager licenses, grants, license
// configurations, report generators, asset groups and rulesets, received
// licenses and tokens. Global service; called once, in its home region
// (homeRegion; us-east-1 for aws), to avoid duplicate scans across regions.
//
// Every listing runs as its own phase, one at a time: a phase's error does
// not stop the later ones; a failed write and ctx's end do, and so does an
// NXDOMAIN on the first listing with nothing stored, which says the region
// does not serve License Manager (runHostPhases). Apart from the setup gap
// below, a listing refused with AuthorizationException or
// AccessDeniedException is warned about and keeps the pages read before it.
// Licenses, grants, license configurations, asset groups and asset rulesets
// are written a page at a time with each row's tags read (lmTags); a report
// generator carries its tags in the listed element. The tags of received
// licenses and tokens are not read: the Service Reference lists neither among
// the types ListTagsForResource accepts. The tag denials of the whole scan are one
// warning.
func scanLicenseManager(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (int, int, error) {
	region, err := acct.homeRegion("licensemanager")
	if err != nil {
		return 0, 0, err
	}
	// The SDK neither lists RateLimitExceededException, License Manager's HTTP
	// 429 throttle, among its throttle codes nor retries a 429 by status, so
	// without this a single throttle fails the call unretried. It gets standard
	// backoff only: adaptive mode's client-side rate limiting does not learn
	// from a code added this way.
	client := licensemanager.NewFromConfig(acct.cfg, func(o *licensemanager.Options) {
		o.Region = region
		o.Retryer = retry.AddWithErrorCodes(o.Retryer, "RateLimitExceededException")
	})

	// A "Service role not found" denial on a listing's first page means
	// License Manager is not set up; the phase records it in notSetUp and
	// stores nothing. It is reported as the disabled sentinel, which discards
	// the scan's counts, only when nothing was stored and no phase failed
	// otherwise; else each such phase is warned about as the partial IAM
	// denial it then is. The listings read through firstPageGapPages record
	// it themselves (lmNotSetUpGap); judged records it for the two read
	// through lmListPhase, which returns it.
	var notSetUp phaseErrs
	judged := func(op string, run func() (int, int, error)) hostPhase {
		return countedPhase(0, op, func() (int, int, error) {
			t, i, err := run()
			if isLicenseManagerNotSetUp(err) {
				notSetUp.add(op, err)
				return t, i, nil
			}
			return t, i, err
		})
	}
	tags := lmTags(client).sharingDenials()
	total, inserted, err := runHostPhases(ctx, st, acct, region, "license-manager", 0, []hostPhase{
		{op: lmListLicensesOp, run: func(p *phaseTotals) bool {
			return scanLMLicenses(ctx, client, acct, region, st, scanID, &notSetUp, tags, p)
		}},
		{op: lmListGrantsOp, run: func(p *phaseTotals) bool {
			return scanLMGrants(ctx, client, acct, region, st, scanID, &notSetUp, tags, p)
		}},
		{op: lmListConfigurationsOp, run: func(p *phaseTotals) bool {
			return scanLMLicenseConfigurations(ctx, client, acct, region, st, scanID, &notSetUp, tags, p)
		}},
		countedPhase(0, lmListReportGeneratorsOp, func() (int, int, error) {
			return scanLMReportGenerators(ctx, client, acct, region, st, scanID, &notSetUp)
		}),
		{op: lmListAssetGroupsOp, run: func(p *phaseTotals) bool {
			return scanLMLicenseAssetGroups(ctx, client, acct, region, st, scanID, &notSetUp, tags, p)
		}},
		{op: lmListAssetRulesetsOp, run: func(p *phaseTotals) bool {
			return scanLMLicenseAssetRulesets(ctx, client, acct, region, st, scanID, &notSetUp, tags, p)
		}},
		judged(lmListReceivedLicensesOp, func() (int, int, error) {
			return scanLMReceivedLicenses(ctx, client, acct, region, st, scanID)
		}),
		judged(lmListTokensOp, func() (int, int, error) { return scanLMTokens(ctx, client, acct, region, st, scanID) }),
	})
	tags.warnDenials(st, acct, region)
	if len(notSetUp) > 0 && total == 0 && err == nil {
		return 0, 0, markServiceDisabled(notSetUp[0].err)
	}
	for _, n := range notSetUp {
		_ = skipIfAccessDenied(st, n.op, acct.ID, region, n.err)
	}
	return total, inserted, err
}

const (
	lmListLicensesOp         = "license-manager:ListLicenses"
	lmListGrantsOp           = "license-manager:ListDistributedGrants"
	lmListConfigurationsOp   = "license-manager:ListLicenseConfigurations"
	lmListReportGeneratorsOp = "license-manager:ListLicenseManagerReportGenerators"
	lmListAssetGroupsOp      = "license-manager:ListLicenseAssetGroups"
	lmListAssetRulesetsOp    = "license-manager:ListLicenseAssetRulesets"
	lmListReceivedLicensesOp = "license-manager:ListReceivedLicenses"
	lmListTokensOp           = "license-manager:ListTokens"
	lmTagsOp                 = "license-manager:ListTagsForResource"
)

// lmAuthorizationDenied is License Manager's own code for a caller without
// permission. Every op this scan calls models it beside AccessDeniedException,
// and isAccessDenied does not match it, so each listing and the tag read
// count it as a denial themselves.
const lmAuthorizationDenied = "AuthorizationException"

func isLMAuthorizationDenied(err error) bool { return isAPIErrorCode(err, lmAuthorizationDenied) }

// lmTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID, the ARN its listing returned; the op returns every tag in one
// response. lmAuthorizationDenied is a denial beside AccessDeniedException.
// The op models no not-found code, so a resource deleted between its listing
// and the read keeps its row untagged and fails the read with whatever the
// service answers.
func lmTags(client licenseManagerAPI) rowTagReader {
	return rowTagReader{
		op:     lmTagsOp,
		denied: []string{lmAuthorizationDenied},
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			arn := r.NativeID
			out, err := client.ListTagsForResource(ctx, &licensemanager.ListTagsForResourceInput{ResourceArn: &arn})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(out.Tags, lmTagKV), nil
		},
	}
}

func lmTagKV(t lmtypes.Tag) (key, value *string) { return t.Key, t.Value }

// lmUntaggedIfOtherAccount is tags for a listing that can return another
// account's resources: a read refused for a row whose ARN names another
// account (otherAccountGap) gives no tags and no error, so the row is kept
// untagged without a warning. It is untaggedIfOtherAccount with
// lmAuthorizationDenied counted as a refusal too: otherAccountGap matches
// only isAccessDenied as a denial and counts the codes it is passed (its
// notFound) beside it, so the code is passed there.
func lmUntaggedIfOtherAccount(tags rowTagReader, acctID string) rowTagReader {
	read := tags.read
	tags.read = func(ctx context.Context, r *store.Resource) (*string, error) {
		got, err := read(ctx, r)
		if otherAccountGap(r.NativeID, acctID, err, []string{lmAuthorizationDenied}) {
			return nil, nil
		}
		return got, err
	}
	return tags
}

// lmNotSetUpGap reports whether err is the "Service role not found" denial,
// and records it under op in notSetUp when it is. Through firstPageGapPages,
// a listing whose first page gets it reads as empty, leaving scanLicenseManager
// to judge the denial once every phase ran.
func lmNotSetUpGap(notSetUp *phaseErrs, op string) func(error) bool {
	return func(err error) bool {
		if !isLicenseManagerNotSetUp(err) {
			return false
		}
		notSetUp.add(op, err)
		return true
	}
}

// storeLMTagged stores one ARN-keyed License Manager listing a page at a
// time, each row's tags read through tags (storeTokenPagesTags). A "Service
// role not found" denial on its first page is recorded in notSetUp
// (lmNotSetUpGap). An lmAuthorizationDenied refusal ends the listing,
// keeping the pages read before it, with one warning (listDenial); what
// names the listed items. It reports whether a write failed.
func storeLMTagged[Item any](
	ctx context.Context, st *store.Store, acct *account, region, op, what string, notSetUp *phaseErrs,
	page func(ctx context.Context, token *string) ([]Item, *string, error),
	row func(Item) (*store.Resource, string, string),
	tags rowTagReader, p *phaseTotals,
) (storeWriteFailed bool) {
	d := listDenial{isDenied: isLMAuthorizationDenied, unread: what + " on later pages"}
	storeWriteFailed = storeTokenPagesTags(ctx, st, acct, region, op, deniedPages(&d, firstPageGapPages(lmNotSetUpGap(notSetUp, op), page)), row, tags, p)
	// No storeWriteFailed guard: each page is written before the next is
	// asked for, so a failed write ends the listing before any denial, and a
	// denial yields an empty last page that writes nothing.
	d.warn(st, op, acct, region)
	return storeWriteFailed
}

// lmARNRow is the row of a listed item keyed by its ARN, or no row and the
// reason when the item has no ARN (arnRow). what names the listing's items.
func lmARNRow(acct *account, scanID, what, rtype string, arn, name, status *string, attrs any) (*store.Resource, string, string) {
	return arnRow(what, arn, name, &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: rtype, NativeID: sv(arn),
		Name: name, Region: regionGlobal, Status: status,
		AttributesJSON: mustJSON(attrs), DiscoveredBy: scanID,
	})
}

// lmListPhase stores one License Manager listing. page reads the page after
// token and returns its rows and the next token. The pages read before an
// error are stored. A "Service role not found" denial on the first page is
// returned for scanLicenseManager to judge; any other denial, an
// lmAuthorizationDenied refusal included, warns. Any other
// error, and a repeated token, is returned with the stored pages' counts.
func lmListPhase(st *store.Store, acct *account, region, op string, page func(token *string) ([]*store.Resource, *string, error)) (int, int, error) {
	var (
		batch   []*store.Resource
		token   *string
		listErr error
	)
	for {
		rows, next, err := page(token)
		if token == nil && isLicenseManagerNotSetUp(err) {
			return 0, 0, err
		}
		if isAccessDenied(err) || isLMAuthorizationDenied(err) {
			_ = skipIfAccessDenied(st, op, acct.ID, region, err)
			break
		}
		if err != nil {
			listErr = err
			break
		}
		batch = append(batch, rows...)
		// No License Manager list op has a paginator; a repeated token would
		// loop forever.
		var done bool
		if done, listErr = lastPage(token, next); done {
			break
		}
		token = next
	}
	return upsertListed(st, batch, op, listErr)
}

// lmReceivedLicenseNativeID keys a received license apart from the license
// itself. ListReceivedLicenses returns the issuer's LicenseArn, which is the
// NativeID ListLicenses gives the license in the issuing account, and nothing
// in the API stops an account granting its own license to itself. A license
// ARN ends in its l- id, so no license ARN ends in "/received".
func lmReceivedLicenseNativeID(licenseArn string) string {
	return licenseArn + "/received"
}

// lmTokenNativeID keys a token under its license. A token has no ARN, and
// CreateToken requires the license the token is for.
func lmTokenNativeID(licenseArn, tokenID string) string {
	return licenseArn + "/token/" + tokenID
}

// scanLMReceivedLicenses stores the licenses granted to this account, with
// this account's received status (PENDING_ACCEPT, ACTIVE, REJECTED, …) as the
// row status. A DELETED license is skipped.
func scanLMReceivedLicenses(ctx context.Context, client licenseManagerAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var arnless skipTally
	total, inserted, err := lmListPhase(st, acct, region, lmListReceivedLicensesOp, func(token *string) ([]*store.Resource, *string, error) {
		out, err := client.ListReceivedLicenses(ctx, &licensemanager.ListReceivedLicensesInput{MaxResults: sdkaws.Int32(lmPageSize), NextToken: token})
		if err != nil {
			return nil, nil, err
		}
		var rows []*store.Resource
		for _, l := range out.Licenses {
			// Status is the issuer's; a license its issuer deleted is gone for
			// every receiver whatever this account's received status says.
			if l.Status == lmtypes.LicenseStatusDeleted {
				continue
			}
			arn := sv(l.LicenseArn)
			if arn == "" {
				arnless.skipped(fmt.Sprintf("%q", sv(l.LicenseName)), nil)
				continue
			}
			var received string
			if l.ReceivedMetadata != nil {
				received = string(l.ReceivedMetadata.ReceivedStatus)
			}
			rows = append(rows, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLicenseManagerReceivedLicense, NativeID: lmReceivedLicenseNativeID(arn),
				Name: l.LicenseName, Region: regionGlobal, Status: nonEmptyPtr(received),
				AttributesJSON: mustJSON(l), DiscoveredBy: scanID,
			})
		}
		return rows, out.NextToken, nil
	})
	warnNotStored(st, lmListReceivedLicensesOp, acct, region, &arnless, "received licenses have no LicenseArn")
	return total, inserted, err
}

// lmTokenDeleted is the TokenData.Status of a deleted token; the model
// documents AVAILABLE and DELETED but types the field as a plain string.
const lmTokenDeleted = "DELETED"

// scanLMTokens stores the account's license tokens. ListTokens returns token
// metadata only; the JWT itself is returned once, by CreateToken. A DELETED
// token is skipped.
func scanLMTokens(ctx context.Context, client licenseManagerAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var unkeyed skipTally
	total, inserted, err := lmListPhase(st, acct, region, lmListTokensOp, func(token *string) ([]*store.Resource, *string, error) {
		out, err := client.ListTokens(ctx, &licensemanager.ListTokensInput{MaxResults: sdkaws.Int32(lmPageSize), NextToken: token})
		if err != nil {
			return nil, nil, err
		}
		var rows []*store.Resource
		for _, tok := range out.Tokens {
			if sv(tok.Status) == lmTokenDeleted {
				continue
			}
			id, licenseArn := sv(tok.TokenId), sv(tok.LicenseArn)
			if id == "" || licenseArn == "" {
				unkeyed.skipped(fmt.Sprintf("token %q of license %q", id, licenseArn), nil)
				continue
			}
			rows = append(rows, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLicenseManagerToken, NativeID: lmTokenNativeID(licenseArn, id),
				Region: regionGlobal, Status: nonEmptyPtr(sv(tok.Status)),
				AttributesJSON: mustJSON(tok), DiscoveredBy: scanID,
			})
		}
		return rows, out.NextToken, nil
	})
	warnNotStored(st, lmListTokensOp, acct, region, &unkeyed, "tokens have no TokenId or LicenseArn")
	return total, inserted, err
}

func scanLMLicenses(ctx context.Context, client licenseManagerAPI, acct *account, region string, st *store.Store, scanID string, notSetUp *phaseErrs, tags rowTagReader, p *phaseTotals) bool {
	return storeLMTagged(ctx, st, acct, region, lmListLicensesOp, "licenses", notSetUp,
		func(ctx context.Context, token *string) ([]lmtypes.License, *string, error) {
			out, err := client.ListLicenses(ctx, &licensemanager.ListLicensesInput{MaxResults: sdkaws.Int32(lmPageSize), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Licenses, out.NextToken, nil
		},
		func(l lmtypes.License) (*store.Resource, string, string) {
			status := string(l.Status)
			return lmARNRow(acct, scanID, "licenses", TypeLicenseManagerLicense, l.LicenseArn, l.LicenseName, &status, l)
		}, tags, p)
}

func scanLMGrants(ctx context.Context, client licenseManagerAPI, acct *account, region string, st *store.Store, scanID string, notSetUp *phaseErrs, tags rowTagReader, p *phaseTotals) bool {
	return storeLMTagged(ctx, st, acct, region, lmListGrantsOp, "grants", notSetUp,
		func(ctx context.Context, token *string) ([]lmtypes.Grant, *string, error) {
			out, err := client.ListDistributedGrants(ctx, &licensemanager.ListDistributedGrantsInput{MaxResults: sdkaws.Int32(lmPageSize), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Grants, out.NextToken, nil
		},
		func(g lmtypes.Grant) (*store.Resource, string, string) {
			status := string(g.GrantStatus)
			return lmARNRow(acct, scanID, "grants", TypeLicenseManagerGrant, g.GrantArn, g.GrantName, &status, g)
		}, tags, p)
}

// scanLMLicenseConfigurations stores the license configurations. A license
// configuration can be shared through AWS RAM and carries its
// OwnerAccountId, so the listing may return another account's (unverified
// live); such a row's tag read refused with AccessDeniedException or
// AuthorizationException keeps it untagged without a warning
// (lmUntaggedIfOtherAccount).
func scanLMLicenseConfigurations(ctx context.Context, client licenseManagerAPI, acct *account, region string, st *store.Store, scanID string, notSetUp *phaseErrs, tags rowTagReader, p *phaseTotals) bool {
	return storeLMTagged(ctx, st, acct, region, lmListConfigurationsOp, "license configurations", notSetUp,
		func(ctx context.Context, token *string) ([]lmtypes.LicenseConfiguration, *string, error) {
			out, err := client.ListLicenseConfigurations(ctx, &licensemanager.ListLicenseConfigurationsInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.LicenseConfigurations, out.NextToken, nil
		},
		func(c lmtypes.LicenseConfiguration) (*store.Resource, string, string) {
			status := sv(c.Status)
			return lmARNRow(acct, scanID, "license configurations", TypeLicenseManagerLicenseConfiguration, c.LicenseConfigurationArn, c.Name, &status, c)
		}, lmUntaggedIfOtherAccount(tags, acct.ID), p)
}

// scanLMReportGenerators stores the report generators with the tags their
// listed element carries, so no tag read is made. An lmAuthorizationDenied
// refusal ends the listing, keeping the pages read before it, and is warned
// about once they are written (listDenial).
func scanLMReportGenerators(ctx context.Context, client licenseManagerAPI, acct *account, region string, st *store.Store, scanID string, notSetUp *phaseErrs) (int, int, error) {
	d := listDenial{isDenied: isLMAuthorizationDenied, unread: "report generators on later pages"}
	total, inserted, err := storeTokenList(ctx, st, acct, region, lmListReportGeneratorsOp,
		deniedPages(&d, firstPageGapPages(lmNotSetUpGap(notSetUp, lmListReportGeneratorsOp), func(ctx context.Context, token *string) ([]lmtypes.ReportGenerator, *string, error) {
			out, err := client.ListLicenseManagerReportGenerators(ctx, &licensemanager.ListLicenseManagerReportGeneratorsInput{MaxResults: sdkaws.Int32(lmPageSize), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ReportGenerators, out.NextToken, nil
		})),
		func(g lmtypes.ReportGenerator) (*store.Resource, string, string) {
			r, why, key := lmARNRow(acct, scanID, "report generators", TypeLicenseManagerReportGenerator, g.LicenseManagerReportGeneratorArn, g.ReportGeneratorName, nil, g)
			if r != nil {
				r.TagsJSON = kvTagsJSON(g.Tags, lmTagKV)
			}
			return r, why, key
		})
	if !errors.Is(err, store.ErrStoreWrite) {
		d.warn(st, lmListReportGeneratorsOp, acct, region)
	}
	return total, inserted, err
}

func scanLMLicenseAssetGroups(ctx context.Context, client licenseManagerAPI, acct *account, region string, st *store.Store, scanID string, notSetUp *phaseErrs, tags rowTagReader, p *phaseTotals) bool {
	return storeLMTagged(ctx, st, acct, region, lmListAssetGroupsOp, "license asset groups", notSetUp,
		func(ctx context.Context, token *string) ([]lmtypes.LicenseAssetGroup, *string, error) {
			out, err := client.ListLicenseAssetGroups(ctx, &licensemanager.ListLicenseAssetGroupsInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.LicenseAssetGroups, out.NextToken, nil
		},
		func(g lmtypes.LicenseAssetGroup) (*store.Resource, string, string) {
			return lmARNRow(acct, scanID, "license asset groups", TypeLicenseManagerLicenseAssetGroup, g.LicenseAssetGroupArn, g.Name, nil, g)
		}, tags, p)
}

func scanLMLicenseAssetRulesets(ctx context.Context, client licenseManagerAPI, acct *account, region string, st *store.Store, scanID string, notSetUp *phaseErrs, tags rowTagReader, p *phaseTotals) bool {
	return storeLMTagged(ctx, st, acct, region, lmListAssetRulesetsOp, "license asset rulesets", notSetUp,
		func(ctx context.Context, token *string) ([]lmtypes.LicenseAssetRuleset, *string, error) {
			out, err := client.ListLicenseAssetRulesets(ctx, &licensemanager.ListLicenseAssetRulesetsInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.LicenseAssetRulesets, out.NextToken, nil
		},
		func(rs lmtypes.LicenseAssetRuleset) (*store.Resource, string, string) {
			return lmARNRow(acct, scanID, "license asset rulesets", TypeLicenseManagerLicenseAssetRuleset, rs.LicenseAssetRulesetArn, rs.Name, nil, rs)
		}, tags, p)
}
