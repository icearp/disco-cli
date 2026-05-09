package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/store"
	sesv1 "github.com/aws/aws-sdk-go-v2/service/ses"
)

// sesV1API — narrow set of classic SES (v1) ops needed for the Receipt
// family. SES v1 has no SDK paginators on receipt ops; ListReceiptFilters
// returns a single page, ListReceiptRuleSets has manual NextToken loop.
type sesV1API interface {
	ListReceiptFilters(context.Context, *sesv1.ListReceiptFiltersInput, ...func(*sesv1.Options)) (*sesv1.ListReceiptFiltersOutput, error)
	ListReceiptRuleSets(context.Context, *sesv1.ListReceiptRuleSetsInput, ...func(*sesv1.Options)) (*sesv1.ListReceiptRuleSetsOutput, error)
	DescribeReceiptRuleSet(context.Context, *sesv1.DescribeReceiptRuleSetInput, ...func(*sesv1.Options)) (*sesv1.DescribeReceiptRuleSetOutput, error)
}

func scanSESReceipt(ctx context.Context, client sesV1API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanSESReceiptFilters(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanSESReceiptRuleSets(ctx, client, acct, region, st, scanID) },
	} {
		t, i, ferr := phase()
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanSESReceiptFilters(ctx context.Context, client sesV1API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	out, err := client.ListReceiptFilters(ctx, &sesv1.ListReceiptFiltersInput{})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "ses:ListReceiptFilters", acct.ID, region, err)
		}
		return 0, 0, fmt.Errorf("ses:ListReceiptFilters: %w", err)
	}
	var batch []*store.Resource
	for _, f := range out.Filters {
		name := sv(f.Name)
		if name == "" {
			continue
		}
		n := name
		arn := fmt.Sprintf("arn:aws:ses:%s:%s:receipt-filter/%s", region, acct.ID, name)
		batch = append(batch, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeSESReceiptFilter, NativeID: arn,
			Name: &n, Region: &region, AttributesJSON: mustJSON(f), DiscoveredBy: scanID,
		})
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert ses receipt-filters: %w", uerr)
	}
	return len(batch), n, nil
}

// scanSESReceiptRuleSets enumerates rule sets (manual NextToken loop) and
// fans out DescribeReceiptRuleSet per-set for embedded rules. Each rule
// emits its own row; rule set is the parent.
func scanSESReceiptRuleSets(ctx context.Context, client sesV1API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var setNames []string
	var rsBatch []*store.Resource
	var token *string
	for {
		out, err := client.ListReceiptRuleSets(ctx, &sesv1.ListReceiptRuleSetsInput{NextToken: token})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "ses:ListReceiptRuleSets", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("ses:ListReceiptRuleSets: %w", err)
		}
		for _, s := range out.RuleSets {
			name := sv(s.Name)
			if name == "" {
				continue
			}
			setNames = append(setNames, name)
			n := name
			rsBatch = append(rsBatch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSESReceiptRuleSet, NativeID: fmt.Sprintf("arn:aws:ses:%s:%s:receipt-rule-set/%s", region, acct.ID, name),
				Name: &n, Region: &region, AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		token = out.NextToken
	}
	total := 0
	inserted := 0
	if len(rsBatch) > 0 {
		n, uerr := st.UpsertResources(rsBatch)
		if uerr != nil {
			return 0, 0, fmt.Errorf("upsert ses receipt-rule-sets: %w", uerr)
		}
		total += len(rsBatch)
		inserted += n
	}
	if len(setNames) == 0 {
		return total, inserted, nil
	}
	var rules []*store.Resource
	for _, sname := range setNames {
		s := sname
		out, derr := client.DescribeReceiptRuleSet(ctx, &sesv1.DescribeReceiptRuleSetInput{RuleSetName: &s})
		if derr != nil {
			if isAccessDenied(derr) {
				continue
			}
			return total, inserted, fmt.Errorf("ses:DescribeReceiptRuleSet %s: %w", sname, derr)
		}
		for _, r := range out.Rules {
			rname := sv(r.Name)
			if rname == "" {
				continue
			}
			rn := rname
			arn := fmt.Sprintf("arn:aws:ses:%s:%s:receipt-rule-set/%s/receipt-rule/%s", region, acct.ID, sname, rname)
			rules = append(rules, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSESReceiptRule, NativeID: arn,
				Name: &rn, Region: &region, AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
	}
	if len(rules) > 0 {
		n, uerr := st.UpsertResources(rules)
		if uerr != nil {
			return total, inserted, fmt.Errorf("upsert ses receipt-rules: %w", uerr)
		}
		total += len(rules)
		inserted += n
	}
	return total, inserted, nil
}
