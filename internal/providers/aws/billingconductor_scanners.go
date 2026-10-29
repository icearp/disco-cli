package aws

import (
	"context"
	"regexp"
	"strings"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/billingconductor"
	bctypes "github.com/aws/aws-sdk-go-v2/service/billingconductor/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

// isBillingConductorPayerOnly disambiguates the "Only payer account is
// authorized" state from a real IAM denial. A member account can't self-enable
// Billing Conductor (only the payer can), so callers mark it not-entitled.
func isBillingConductorPayerOnly(err error) bool {
	return isAccessDeniedWithMessage(err, "Only payer account is authorized")
}

func init() {
	registerType(restype.Descriptor{Type: TypeBillingConductorBillingGroup, Service: "billingconductor"})
	registerType(restype.Descriptor{Type: TypeBillingConductorCustomLineItem, Service: "billingconductor"})
	registerType(restype.Descriptor{Type: TypeBillingConductorPricingPlan, Service: "billingconductor"})
	registerType(restype.Descriptor{Type: TypeBillingConductorPricingRule, Service: "billingconductor"})
	registerService(serviceEntry{
		name:   "aws:billingconductor",
		global: true,
		fn: func(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (total, inserted int, err error) {
			region, err := acct.homeRegion("billingconductor")
			if err != nil {
				return 0, 0, err
			}
			client := billingconductor.NewFromConfig(acct.cfg, func(o *billingconductor.Options) { o.Region = region })
			return scanBillingConductor(ctx, client, acct, region, st, scanID)
		},
	})
}

// billingConductorAPI is the narrow surface scanBillingConductor uses. All
// four list ops return elements that already carry the canonical ARN — no Get
// fan-out needed for identity.
type billingConductorAPI interface {
	ListBillingGroups(context.Context, *billingconductor.ListBillingGroupsInput, ...func(*billingconductor.Options)) (*billingconductor.ListBillingGroupsOutput, error)
	ListCustomLineItems(context.Context, *billingconductor.ListCustomLineItemsInput, ...func(*billingconductor.Options)) (*billingconductor.ListCustomLineItemsOutput, error)
	ListPricingPlans(context.Context, *billingconductor.ListPricingPlansInput, ...func(*billingconductor.Options)) (*billingconductor.ListPricingPlansOutput, error)
	ListPricingRules(context.Context, *billingconductor.ListPricingRulesInput, ...func(*billingconductor.Options)) (*billingconductor.ListPricingRulesOutput, error)
	ListTagsForResource(context.Context, *billingconductor.ListTagsForResourceInput, ...func(*billingconductor.Options)) (*billingconductor.ListTagsForResourceOutput, error)
}

const (
	bcTagsOp = "billingconductor:ListTagsForResource"
	// bcPageCap is the MaxResults maximum of every Billing Conductor listing
	// read here.
	bcPageCap = 100
)

// scanBillingConductor discovers billing groups, custom line items, pricing
// plans and pricing rules, each with its tags (bcTags). Every listing runs as
// its own phase, one at a time and in that order: a phase's error does not
// stop the later ones; a failed write and ctx's end do (runHostPhases). The
// tag reads' denials are one warning for the whole scan.
//
// The payer-only refusal (isBillingConductorPayerOnly) of a listing's first
// page while the scan has stored no row (bcKeepPayerOnly) reports the account
// not entitled, and no later listing runs. An earlier listing's denial is
// still warned about; any other error of it is returned instead of the
// sentinel, and the refusal is then warned about under its listing
// (payerOnlyOp), so the scan record says why the later listings did not run.
// The same refusal of a later page, or after a row was stored, is warned
// about as a denial, and the stored rows keep their counts.
func scanBillingConductor(ctx context.Context, client billingConductorAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var payerOnly error
	var payerOnlyOp string
	tags := bcTags(client).sharingDenials()
	phase := func(op string, run func(p *phaseTotals) bool) hostPhase {
		return hostPhase{op: op, run: func(p *phaseTotals) bool {
			if payerOnly != nil {
				return false
			}
			stop := run(p)
			if payerOnly != nil {
				payerOnlyOp = op
			}
			return stop
		}}
	}
	total, inserted, err := runHostPhases(ctx, st, acct, region, "billingconductor", 0, []hostPhase{
		phase("billingconductor:ListBillingGroups", func(p *phaseTotals) bool {
			return scanBillingConductorBillingGroups(ctx, client, acct, region, st, scanID, &payerOnly, tags, p)
		}),
		phase("billingconductor:ListCustomLineItems", func(p *phaseTotals) bool {
			return scanBillingConductorCustomLineItems(ctx, client, acct, region, st, scanID, &payerOnly, tags, p)
		}),
		phase("billingconductor:ListPricingPlans", func(p *phaseTotals) bool {
			return scanBillingConductorPricingPlans(ctx, client, acct, region, st, scanID, &payerOnly, tags, p)
		}),
		phase("billingconductor:ListPricingRules", func(p *phaseTotals) bool {
			return scanBillingConductorPricingRules(ctx, client, acct, region, st, scanID, &payerOnly, tags, p)
		}),
	})
	// The sentinel is never joined with an error: an earlier listing's
	// failure is returned alone, and the scan is partial.
	if payerOnly != nil {
		if err == nil {
			return total, inserted, markServiceNotEntitled(payerOnly)
		}
		_ = skipIfAccessDenied(st, payerOnlyOp, acct.ID, region, payerOnly)
	}
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// bcTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID: the ARN its listing returned. The Service Reference lists
// billinggroup, customlineitem, pricingplan and pricingrule under
// ListTagsForResource, and each one's Create op takes Tags. The op returns
// every tag at once. ResourceNotFoundException, its modeled not-found, drops
// a resource deleted after it was listed.
//
// An AWS-managed pricing plan (ManagedByProvider) is not read: its ARN
// carries "aws" as the account (…::aws:pricingplan/BasicPricingPlan), which
// the op's ResourceArn pattern, a 12-digit account and a 10-character plan id,
// does not accept, so the read would be refused on every scan. Such a row is
// stored without tags. So is a billing group whose id is not 12 digits
// (bcGroupTagReadable): the listing's BillingGroupArn admits a 10 to 12
// character alphanumeric id, which the same pattern refuses.
func bcTags(client billingConductorAPI) rowTagReader {
	return rowTagReader{
		op:       bcTagsOp,
		notFound: []string{"ResourceNotFoundException"},
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			if r.ManagedByProvider || (r.Type == TypeBillingConductorBillingGroup && !bcGroupTagReadable.MatchString(r.NativeID)) {
				return nil, nil
			}
			arn := r.NativeID
			out, err := client.ListTagsForResource(ctx, &billingconductor.ListTagsForResourceInput{ResourceArn: &arn})
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

// bcGroupTagReadable matches the billing group ARNs ListTagsForResource's
// ResourceArn pattern admits: …:billinggroup/ and a 12-digit id.
var bcGroupTagReadable = regexp.MustCompile(`:billinggroup/[0-9]{12}$`)

// bcKeepPayerOnly reports whether err is the payer-only refusal of a
// listing's first page (token nil) while the scan has stored no row (p), and
// then keeps it in payerOnly: the account is not entitled
// (scanBillingConductor). Any other refusal is left for the listing to warn
// about.
func bcKeepPayerOnly(payerOnly *error, p *phaseTotals, token *string, err error) bool {
	if token != nil || p.total != 0 || !isBillingConductorPayerOnly(err) {
		return false
	}
	*payerOnly = err
	return true
}

// epochMillisToTime converts a billingconductor int64 epoch-millis timestamp
// to *time.Time for store.Resource.CreatedAt. Zero values stay nil.
func epochMillisToTime(ms int64) *time.Time {
	if ms == 0 {
		return nil
	}
	t := time.UnixMilli(ms).UTC()
	return &t
}

// bcRow is the row of a listed element stored under its ARN, named name (""
// when it has none) and created at createdMs; an element without an ARN is
// not stored (arnRow), and what names such elements in the listing's warning.
func bcRow(acct *account, scanID, typ, what string, arn, name *string, createdMs int64, managed bool, element any) (*store.Resource, string, string) {
	n := sv(name)
	return arnRow(what, arn, name, &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: sv(arn), Name: &n, Region: regionGlobal,
		ManagedByProvider: managed,
		CreatedAt:         tp(epochMillisToTime(createdMs)),
		AttributesJSON:    mustJSON(element), DiscoveredBy: scanID,
	})
}

// scanBillingConductorBillingGroups stores the billing groups, each page
// written as soon as its rows' tags are read. A refusal bcKeepPayerOnly keeps
// is read as an empty listing. It reports whether a write failed, after which
// the scan must stop.
func scanBillingConductorBillingGroups(ctx context.Context, client billingConductorAPI, acct *account, region string, st *store.Store, scanID string, payerOnly *error, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "billingconductor:ListBillingGroups",
		func(ctx context.Context, token *string) ([]bctypes.BillingGroupListElement, *string, error) {
			out, err := client.ListBillingGroups(ctx, &billingconductor.ListBillingGroupsInput{MaxResults: sdkaws.Int32(bcPageCap), NextToken: token})
			if bcKeepPayerOnly(payerOnly, p, token, err) {
				return nil, nil, nil
			}
			if err != nil {
				return nil, nil, err
			}
			return out.BillingGroups, out.NextToken, nil
		},
		func(bg bctypes.BillingGroupListElement) (*store.Resource, string, string) {
			return bcRow(acct, scanID, TypeBillingConductorBillingGroup, "billing groups", bg.Arn, bg.Name, bg.CreationTime, false, bg)
		},
		tags, p)
}

// scanBillingConductorCustomLineItems stores the custom line items, each page
// written as soon as its rows' tags are read. A refusal bcKeepPayerOnly keeps
// is read as an empty listing. It reports whether a write failed, after which
// the scan must stop.
func scanBillingConductorCustomLineItems(ctx context.Context, client billingConductorAPI, acct *account, region string, st *store.Store, scanID string, payerOnly *error, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "billingconductor:ListCustomLineItems",
		func(ctx context.Context, token *string) ([]bctypes.CustomLineItemListElement, *string, error) {
			out, err := client.ListCustomLineItems(ctx, &billingconductor.ListCustomLineItemsInput{MaxResults: sdkaws.Int32(bcPageCap), NextToken: token})
			if bcKeepPayerOnly(payerOnly, p, token, err) {
				return nil, nil, nil
			}
			if err != nil {
				return nil, nil, err
			}
			return out.CustomLineItems, out.NextToken, nil
		},
		func(cli bctypes.CustomLineItemListElement) (*store.Resource, string, string) {
			return bcRow(acct, scanID, TypeBillingConductorCustomLineItem, "custom line items", cli.Arn, cli.Name, cli.CreationTime, false, cli)
		},
		tags, p)
}

// scanBillingConductorPricingPlans stores the pricing plans, each page written
// as soon as its rows' tags are read. A refusal bcKeepPayerOnly keeps is read
// as an empty listing. It reports whether a write failed, after which the
// scan must stop.
func scanBillingConductorPricingPlans(ctx context.Context, client billingConductorAPI, acct *account, region string, st *store.Store, scanID string, payerOnly *error, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "billingconductor:ListPricingPlans",
		func(ctx context.Context, token *string) ([]bctypes.PricingPlanListElement, *string, error) {
			out, err := client.ListPricingPlans(ctx, &billingconductor.ListPricingPlansInput{MaxResults: sdkaws.Int32(bcPageCap), NextToken: token})
			if bcKeepPayerOnly(payerOnly, p, token, err) {
				return nil, nil, nil
			}
			if err != nil {
				return nil, nil, err
			}
			return out.PricingPlans, out.NextToken, nil
		},
		func(pp bctypes.PricingPlanListElement) (*store.Resource, string, string) {
			// AWS-managed default plans (BasicPricingPlan, Passthrough) carry the
			// "aws" account segment (…:billingconductor::aws:pricingplan/…);
			// customer plans carry a 12-digit account id.
			managed := strings.Contains(sv(pp.Arn), "::aws:pricingplan/")
			return bcRow(acct, scanID, TypeBillingConductorPricingPlan, "pricing plans", pp.Arn, pp.Name, pp.CreationTime, managed, pp)
		},
		tags, p)
}

// scanBillingConductorPricingRules stores the pricing rules, each page written
// as soon as its rows' tags are read. A refusal bcKeepPayerOnly keeps is read
// as an empty listing. It reports whether a write failed, after which the
// scan must stop.
func scanBillingConductorPricingRules(ctx context.Context, client billingConductorAPI, acct *account, region string, st *store.Store, scanID string, payerOnly *error, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "billingconductor:ListPricingRules",
		func(ctx context.Context, token *string) ([]bctypes.PricingRuleListElement, *string, error) {
			out, err := client.ListPricingRules(ctx, &billingconductor.ListPricingRulesInput{MaxResults: sdkaws.Int32(bcPageCap), NextToken: token})
			if bcKeepPayerOnly(payerOnly, p, token, err) {
				return nil, nil, nil
			}
			if err != nil {
				return nil, nil, err
			}
			return out.PricingRules, out.NextToken, nil
		},
		func(pr bctypes.PricingRuleListElement) (*store.Resource, string, string) {
			return bcRow(acct, scanID, TypeBillingConductorPricingRule, "pricing rules", pr.Arn, pr.Name, pr.CreationTime, false, pr)
		},
		tags, p)
}
