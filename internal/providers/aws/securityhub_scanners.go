package aws

import (
	"context"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/securityhub"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeSecurityHubHub, Service: "securityhub"})
	registerType(restype.Descriptor{Type: TypeSecurityHubInsight, Service: "securityhub"})
	registerType(restype.Descriptor{Type: TypeSecurityHubProductSubscription, Service: "securityhub"})
	registerType(restype.Descriptor{Type: TypeSecurityHubStandardsSubscription, Service: "securityhub"})
	registerType(restype.Descriptor{Type: TypeSecurityHubActionTarget, Service: "securityhub"})
	registerType(restype.Descriptor{Type: TypeSecurityHubMember, Service: "securityhub"})
	registerType(restype.Descriptor{Type: TypeSecurityHubAggregatorV2, Service: "securityhub"})
	registerType(restype.Descriptor{Type: TypeSecurityHubAutomationRule, Service: "securityhub"})
	registerType(restype.Descriptor{Type: TypeSecurityHubAutomationRuleV2, Service: "securityhub"})
	registerType(restype.Descriptor{Type: TypeSecurityHubConfigurationPolicy, Service: "securityhub"})
	registerType(restype.Descriptor{Type: TypeSecurityHubConnectorV2, Service: "securityhub"})
	registerType(restype.Descriptor{Type: TypeSecurityHubFindingAggregator, Service: "securityhub", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeSecurityHubHubV2, Service: "securityhub"})
	registerType(restype.Descriptor{Type: TypeSecurityHubOrganizationConfiguration, Service: "securityhub"})
	registerType(restype.Descriptor{Type: TypeSecurityHubPolicyAssociation, Service: "securityhub"})
	registerType(restype.Descriptor{Type: TypeSecurityHubSecurityControl, Service: "securityhub", Managed: true})
	registerType(restype.Descriptor{Type: TypeSecurityHubStandard, Service: "securityhub", Managed: true})
	// Enabling a standard creates its control associations, and no API deletes
	// one; only their status changes. Still not Managed: the customer sets that
	// status, and Managed rows are left out of default `disco check` input.
	registerType(restype.Descriptor{Type: TypeSecurityHubStandardsControlAssociation, Service: "securityhub", Untaggable: true})
	registerService(serviceEntry{
		name: "aws:securityhub",
		fn:   scanSecurityHub,
	})
}

// securityhubAPI is the narrow set of Security Hub operations called by the
// scanSecurityHub sub-phases.
type securityhubAPI interface {
	DescribeHub(context.Context, *securityhub.DescribeHubInput, ...func(*securityhub.Options)) (*securityhub.DescribeHubOutput, error)
	GetInsights(context.Context, *securityhub.GetInsightsInput, ...func(*securityhub.Options)) (*securityhub.GetInsightsOutput, error)
	GetEnabledStandards(context.Context, *securityhub.GetEnabledStandardsInput, ...func(*securityhub.Options)) (*securityhub.GetEnabledStandardsOutput, error)
	ListEnabledProductsForImport(context.Context, *securityhub.ListEnabledProductsForImportInput, ...func(*securityhub.Options)) (*securityhub.ListEnabledProductsForImportOutput, error)
	ListAggregatorsV2(context.Context, *securityhub.ListAggregatorsV2Input, ...func(*securityhub.Options)) (*securityhub.ListAggregatorsV2Output, error)
	ListAutomationRules(context.Context, *securityhub.ListAutomationRulesInput, ...func(*securityhub.Options)) (*securityhub.ListAutomationRulesOutput, error)
	ListAutomationRulesV2(context.Context, *securityhub.ListAutomationRulesV2Input, ...func(*securityhub.Options)) (*securityhub.ListAutomationRulesV2Output, error)
	ListConfigurationPolicies(context.Context, *securityhub.ListConfigurationPoliciesInput, ...func(*securityhub.Options)) (*securityhub.ListConfigurationPoliciesOutput, error)
	ListConnectorsV2(context.Context, *securityhub.ListConnectorsV2Input, ...func(*securityhub.Options)) (*securityhub.ListConnectorsV2Output, error)
	ListFindingAggregators(context.Context, *securityhub.ListFindingAggregatorsInput, ...func(*securityhub.Options)) (*securityhub.ListFindingAggregatorsOutput, error)
	DescribeSecurityHubV2(context.Context, *securityhub.DescribeSecurityHubV2Input, ...func(*securityhub.Options)) (*securityhub.DescribeSecurityHubV2Output, error)
	DescribeOrganizationConfiguration(context.Context, *securityhub.DescribeOrganizationConfigurationInput, ...func(*securityhub.Options)) (*securityhub.DescribeOrganizationConfigurationOutput, error)
	ListConfigurationPolicyAssociations(context.Context, *securityhub.ListConfigurationPolicyAssociationsInput, ...func(*securityhub.Options)) (*securityhub.ListConfigurationPolicyAssociationsOutput, error)
	ListSecurityControlDefinitions(context.Context, *securityhub.ListSecurityControlDefinitionsInput, ...func(*securityhub.Options)) (*securityhub.ListSecurityControlDefinitionsOutput, error)
	DescribeStandards(context.Context, *securityhub.DescribeStandardsInput, ...func(*securityhub.Options)) (*securityhub.DescribeStandardsOutput, error)
	DescribeActionTargets(context.Context, *securityhub.DescribeActionTargetsInput, ...func(*securityhub.Options)) (*securityhub.DescribeActionTargetsOutput, error)
	ListMembers(context.Context, *securityhub.ListMembersInput, ...func(*securityhub.Options)) (*securityhub.ListMembersOutput, error)
	ListStandardsControlAssociations(context.Context, *securityhub.ListStandardsControlAssociationsInput, ...func(*securityhub.Options)) (*securityhub.ListStandardsControlAssociationsOutput, error)
}

// scanSecurityHub discovers the per-region hub, enabled standards, imported
// products, and saved insights. Security Hub is regional; accounts without
// the hub enabled here return InvalidAccessException on every API, so phase
// 1 sets a `present` flag (mirrors scanMacie) and sibling phases short-circuit
// when not enabled, avoiding N redundant denied errors.
func scanSecurityHub(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := securityhub.NewFromConfig(acct.cfg, func(o *securityhub.Options) { o.Region = region })

	hubARN, present, t, i, ferr := scanSecurityHubHub(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return 0, 0, ferr
	}
	total += t
	inserted += i
	if !present {
		return total, inserted, nil
	}

	{
		t, i, ferr := scanSecurityHubInsights(ctx, client, acct, region, hubARN, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	var standards []string
	{
		t, i, stored, ferr := scanSecurityHubStandards(ctx, client, acct, region, hubARN, st, scanID)
		standards = stored
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	{
		t, i, ferr := scanSecurityHubProducts(ctx, client, acct, region, hubARN, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	var controls []childParent
	{
		t, i, stored, ferr := scanSecurityHubExtended(ctx, client, acct, region, st, scanID)
		controls = stored
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	{
		t, i, ferr := scanSecurityHubActionTargets(ctx, client, acct, region, hubARN, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	{
		t, i, ferr := scanSecurityHubMembers(ctx, client, acct, region, hubARN, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	{
		t, i, ferr := scanSecurityHubControlAssociations(ctx, client, acct, region, standards, controls, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	return total, inserted, nil
}

// isSecurityHubNotEnabled reports whether err means Security Hub isn't
// enabled in the calling region — DescribeHub and downstream APIs return
// InvalidAccessException in that case; treat as a soft skip like AccessDenied
// so non-enabled regions don't pollute scan errors.
func isSecurityHubNotEnabled(err error) bool {
	return isAPIErrorCode(err, "InvalidAccessException", "ResourceNotFoundException")
}

// securityHubHubNativeID synthesises the canonical hub ARN when DescribeHub
// returns no HubArn. Hub is a singleton per (acct, region); shape matches
// the AWS-issued ARN.
func securityHubHubNativeID(partition, region, accountID string) string {
	return arnFor(partition, "securityhub", region, accountID, "hub/default")
}

func scanSecurityHubHub(ctx context.Context, client securityhubAPI, acct *account, region string, st *store.Store, scanID string) (hubARN string, present bool, total, inserted int, err error) {
	out, derr := client.DescribeHub(ctx, &securityhub.DescribeHubInput{})
	if derr != nil {
		if isAccessDenied(derr) {
			_ = skipIfAccessDenied(st, "securityhub:DescribeHub", acct.ID, region, derr)
			return "", false, 0, 0, nil
		}
		if isSecurityHubNotEnabled(derr) {
			return "", false, 0, 0, markServiceDisabled(derr)
		}
		return "", false, 0, 0, fmt.Errorf("securityhub:DescribeHub: %w", derr)
	}
	if out == nil {
		return "", false, 0, 0, nil
	}
	nid := sv(out.HubArn)
	if nid == "" {
		nid = securityHubHubNativeID(acct.partition(), region, acct.ID)
	}
	r := &store.Resource{
		Provider:       "aws",
		AccountID:      acct.ID,
		AccountName:    &acct.Name,
		Type:           TypeSecurityHubHub,
		NativeID:       nid,
		Region:         &region,
		AttributesJSON: mustJSON(out),
		DiscoveredBy:   scanID,
	}
	n, uerr := st.UpsertResources([]*store.Resource{r})
	if uerr != nil {
		return "", false, 0, 0, fmt.Errorf("upsert securityhub hub: %w", uerr)
	}
	return nid, true, 1, n, nil
}

func scanSecurityHubInsights(ctx context.Context, client securityhubAPI, acct *account, region, hubARN string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := securityhub.NewGetInsightsPaginator(client, &securityhub.GetInsightsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "securityhub:GetInsights", acct.ID, region, perr)
				return 0, 0, nil
			}
			if isSecurityHubNotEnabled(perr) {
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("securityhub:GetInsights: %w", perr)
		}
		for _, in := range out.Insights {
			arn := sv(in.InsightArn)
			if arn == "" {
				continue
			}
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeSecurityHubInsight,
				NativeID:       arn,
				Name:           in.Name,
				Region:         &region,
				AttributesJSON: mustJSON(in),
				DiscoveredBy:   scanID,
			}
			batch = append(batch, r)
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := upsertSecurityHubChildren(st, hubARN, acct, batch, "insights")
	if uerr != nil {
		return 0, 0, uerr
	}
	return len(batch), n, nil
}

// scanSecurityHubStandards stores the account's standards subscriptions and
// returns the StandardsArn of each one stored, for
// scanSecurityHubControlAssociations.
func scanSecurityHubStandards(ctx context.Context, client securityhubAPI, acct *account, region, hubARN string, st *store.Store, scanID string) (total, inserted int, standards []string, err error) {
	pager := securityhub.NewGetEnabledStandardsPaginator(client, &securityhub.GetEnabledStandardsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "securityhub:GetEnabledStandards", acct.ID, region, perr)
				return 0, 0, nil, nil
			}
			if isSecurityHubNotEnabled(perr) {
				return 0, 0, nil, nil
			}
			return 0, 0, nil, fmt.Errorf("securityhub:GetEnabledStandards: %w", perr)
		}
		for _, s := range out.StandardsSubscriptions {
			arn := sv(s.StandardsSubscriptionArn)
			if arn == "" {
				continue
			}
			status := string(s.StandardsStatus)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeSecurityHubStandardsSubscription,
				NativeID:       arn,
				Region:         &region,
				Status:         &status,
				AttributesJSON: mustJSON(s),
				DiscoveredBy:   scanID,
			}
			batch = append(batch, r)
			if standard := sv(s.StandardsArn); standard != "" {
				standards = append(standards, standard)
			}
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil, nil
	}
	n, uerr := upsertSecurityHubChildren(st, hubARN, acct, batch, "standards subscriptions")
	if uerr != nil {
		return 0, 0, nil, uerr
	}
	return len(batch), n, standards, nil
}

func scanSecurityHubProducts(ctx context.Context, client securityhubAPI, acct *account, region, hubARN string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := securityhub.NewListEnabledProductsForImportPaginator(client, &securityhub.ListEnabledProductsForImportInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "securityhub:ListEnabledProductsForImport", acct.ID, region, perr)
				return 0, 0, nil
			}
			if isSecurityHubNotEnabled(perr) {
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("securityhub:ListEnabledProductsForImport: %w", perr)
		}
		for _, arn := range out.ProductSubscriptions {
			if arn == "" {
				continue
			}
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeSecurityHubProductSubscription,
				NativeID:       arn,
				Region:         &region,
				AttributesJSON: mustJSON(map[string]string{"ProductSubscriptionArn": arn}),
				DiscoveredBy:   scanID,
			}
			batch = append(batch, r)
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := upsertSecurityHubChildren(st, hubARN, acct, batch, "product subscriptions")
	if uerr != nil {
		return 0, 0, uerr
	}
	return len(batch), n, nil
}

// scanSecurityHubActionTargets stores the custom actions that send findings to
// EventBridge. A denial, or the hub found disabled, partway through pagination
// keeps the pages already read.
func scanSecurityHubActionTargets(ctx context.Context, client securityhubAPI, acct *account, region, hubARN string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := securityhub.NewDescribeActionTargetsPaginator(client, &securityhub.DescribeActionTargetsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "securityhub:DescribeActionTargets", acct.ID, region, perr)
				break
			}
			if isSecurityHubNotEnabled(perr) {
				break
			}
			return 0, 0, fmt.Errorf("securityhub:DescribeActionTargets: %w", perr)
		}
		for _, a := range out.ActionTargets {
			arn := sv(a.ActionTargetArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeSecurityHubActionTarget,
				NativeID:       arn,
				Name:           a.Name,
				Region:         &region,
				AttributesJSON: mustJSON(a),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := upsertSecurityHubChildren(st, hubARN, acct, batch, "action targets")
	if uerr != nil {
		return 0, 0, uerr
	}
	return len(batch), n, nil
}

// securityHubMemberNativeID keys a member account under the administrator's
// hub: a member has no ARN, only its AccountId. Same shape as
// detectiveMemberNativeID.
func securityHubMemberNativeID(hubARN, memberAccountID string) string {
	return hubARN + "/member/" + memberAccountID
}

// scanSecurityHubMembers stores the member accounts of this administrator's
// hub. The default OnlyAssociated=true returns only Enabled members;
// OnlyAssociated=false returns every existing member, so members the
// administrator added or invited but that have not accepted are stored too,
// with MemberStatus as Status. Phase 1 already returns markServiceDisabled
// when the hub is not enabled, so the not-enabled skip here only covers a hub
// disabled mid-scan. A denial or that skip partway through pagination keeps
// the pages already read.
func scanSecurityHubMembers(ctx context.Context, client securityhubAPI, acct *account, region, hubARN string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := securityhub.NewListMembersPaginator(client, &securityhub.ListMembersInput{OnlyAssociated: sdkaws.Bool(false)})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "securityhub:ListMembers", acct.ID, region, perr)
				break
			}
			if isSecurityHubNotEnabled(perr) {
				break
			}
			return 0, 0, fmt.Errorf("securityhub:ListMembers: %w", perr)
		}
		for _, m := range out.Members {
			memberAcct := sv(m.AccountId)
			if memberAcct == "" {
				continue
			}
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeSecurityHubMember,
				NativeID:       securityHubMemberNativeID(hubARN, memberAcct),
				Name:           &memberAcct,
				Region:         &region,
				AttributesJSON: mustJSON(m),
				DiscoveredBy:   scanID,
			}
			if status := sv(m.MemberStatus); status != "" {
				r.Status = &status
			}
			batch = append(batch, r)
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := upsertSecurityHubChildren(st, hubARN, acct, batch, "members")
	if uerr != nil {
		return 0, 0, uerr
	}
	return len(batch), n, nil
}

const (
	shListControlAssociationsOp = "securityhub:ListStandardsControlAssociations"
	shListSubscribedControlsOp  = "securityhub:ListSecurityControlDefinitions"
	// shControlAssociationPageLimit is the model's MaxResults cap for both ops.
	shControlAssociationPageLimit = 100
)

// securityHubControlAssociationNativeID keys a control's association with one
// standard under the control's row. The API returns no association ARN, and
// StandardsArn alone is not region-scoped for every standard
// (arn:aws:securityhub:::ruleset/cis-aws-foundations-benchmark/v/1.2.0), so the
// region-scoped control NativeID leads and StandardsArn, which contains "/",
// goes last.
func securityHubControlAssociationNativeID(controlNativeID, standardsARN string) string {
	return controlNativeID + "/standard/" + standardsARN
}

// shControlFanOutSkips drops a key (a standard or a control) of the
// control-association phase whose call
// still failed after the SDK's retries with a transient error, Security Hub's
// modelled throttle LimitExceededException (not in isTransientNetworkError),
// or an InvalidAccessException after DescribeHub answered (not observed; it
// may mean consolidated control findings is off). Each reason gets one
// warning, and the other keys' rows are kept.
type shControlFanOutSkips struct {
	transient     transientTally
	limit         skipTally
	invalidAccess skipTally
}

func (k *shControlFanOutSkips) drop(key string, err error) bool {
	return k.transient.drop(key, err) ||
		k.limit.dropCode("LimitExceededException", key, err) ||
		k.invalidAccess.dropCode("InvalidAccessException", key, err)
}

// warn reports one warning per reason that dropped a key. what names the keys
// ("standard(s)"), outcome what happened to them ("skipped or partly listed"
// for a paginated listing that keeps the pages read before the error), and
// consequence what the reader loses.
func (k *shControlFanOutSkips) warn(st *store.Store, op string, acct *account, region, what, outcome, consequence string) {
	if t := &k.transient.skipTally; t.count > 0 {
		_ = skipIfTransient(st, op, acct.ID, region,
			fmt.Errorf("%d %s %s after the SDK's retries; %s, first %s: %w", t.count, what, outcome, consequence, t.first, t.err))
	}
	for _, r := range []struct {
		tally     *skipTally
		code, why string
	}{
		{&k.limit, "LimitExceededException", "Security Hub throttled it after the SDK's retries"},
		{&k.invalidAccess, "InvalidAccessException", "Security Hub refused it after DescribeHub answered, which may mean consolidated control findings is off"},
	} {
		if r.tally.count == 0 {
			continue
		}
		st.ReportWarning(store.ScanWarning{
			Provider: "aws", Service: op, Scope: acct.ID + "/" + region,
			Message: fmt.Sprintf("%d %s %s with %s (%s); %s, first %s: %v", r.tally.count, what, outcome, r.code, r.why, consequence, r.tally.first, r.tally.err),
		})
	}
}

// scanSecurityHubControlAssociations stores whether each security control is
// enabled in each standard the account has enabled, with the reason given for
// the last change, under its control's row. standards are the StandardsArns of
// the subscriptions scanSecurityHubStandards stored this scan; controls are
// the security-control rows scanSHSecurityControls stored.
//
// ListStandardsControlAssociations takes one SecurityControlId and no
// StandardsArn, so only the controls of subscribed standards are asked:
// per region, sum over standards of ceil(controls in it / 100) definition
// calls, plus one association call per distinct control in their union. No
// subscription means no call. A control id missing from the stored control
// rows has no parent to sit under and is skipped with one warning. The op
// lists nothing for a subscription that is NOT_READY_FOR_UPDATES.
func scanSecurityHubControlAssociations(ctx context.Context, client securityhubAPI, acct *account, region string, standards []string, controls []childParent, st *store.Store, scanID string) (total, inserted int, err error) {
	ids, err := listSecurityHubSubscribedControlIDs(ctx, client, st, acct, region, standards)
	if err != nil {
		return 0, 0, err
	}
	stored := make(map[string]childParent, len(controls))
	for _, c := range controls {
		stored[c.id] = c
	}
	var parents []childParent
	var missing []string
	for _, id := range ids {
		c, ok := stored[id]
		if !ok {
			missing = append(missing, id)
			continue
		}
		parents = append(parents, c)
	}
	if len(missing) > 0 {
		st.ReportWarning(store.ScanWarning{
			Provider: "aws", Service: shListControlAssociationsOp, Scope: acct.ID + "/" + region,
			Message: fmt.Sprintf("%d security control(s) of subscribed standards skipped: no security-control row was stored for them this scan (the unfiltered ListSecurityControlDefinitions may have been denied or not listed them), first %s", len(missing), missing[0]),
		})
	}
	var skips shControlFanOutSkips
	total, inserted, err = childFanOut(ctx, st, acct, region, shListControlAssociationsOp, parents, neverSkip,
		func(ctx context.Context, control childParent) ([]*store.Resource, error) {
			rows, err := listSecurityHubControlAssociations(ctx, client, acct, region, control, scanID)
			if skips.drop(control.id, err) {
				return nil, nil
			}
			return rows, err
		})
	skips.warn(st, shListControlAssociationsOp, acct, region, "security control(s)", "skipped", "their association rows are not stored")
	return total, inserted, err
}

// listSecurityHubSubscribedControlIDs returns the distinct SecurityControlIds
// of the given standards, in first-listed order. A standard denied or dropped
// per shControlFanOutSkips keeps the ids of the pages read before the error;
// any other error is returned.
func listSecurityHubSubscribedControlIDs(ctx context.Context, client securityhubAPI, st *store.Store, acct *account, region string, standards []string) ([]string, error) {
	var (
		ids   []string
		seen  = map[string]bool{}
		skips shControlFanOutSkips
	)
	defer skips.warn(st, shListSubscribedControlsOp, acct, region, "standard(s)", "skipped or partly listed", "controls not listed get no association rows")
	errs := &fanOutErrs{st: st, acct: acct, region: region, op: shListSubscribedControlsOp, isSkip: neverSkip}
	for _, standard := range standards {
		listed, err := listSecurityHubStandardControlIDs(ctx, client, standard)
		if err != nil && !skips.drop(standard, err) {
			if kerr := errs.keyErr(standard, err); kerr != nil {
				return nil, kerr
			}
		}
		for _, id := range listed {
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	return ids, nil
}

// listSecurityHubStandardControlIDs returns the SecurityControlIds of one
// standard; on an error it returns the ids of the pages read before it.
func listSecurityHubStandardControlIDs(ctx context.Context, client securityhubAPI, standard string) ([]string, error) {
	pager := securityhub.NewListSecurityControlDefinitionsPaginator(client,
		&securityhub.ListSecurityControlDefinitionsInput{StandardsArn: &standard},
		func(o *securityhub.ListSecurityControlDefinitionsPaginatorOptions) {
			o.Limit = shControlAssociationPageLimit
		})
	var ids []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			return ids, err
		}
		for _, c := range out.SecurityControlDefinitions {
			if id := sv(c.SecurityControlId); id != "" {
				ids = append(ids, id)
			}
		}
	}
	return ids, nil
}

func listSecurityHubControlAssociations(ctx context.Context, client securityhubAPI, acct *account, region string, control childParent, scanID string) ([]*store.Resource, error) {
	pager := securityhub.NewListStandardsControlAssociationsPaginator(client,
		&securityhub.ListStandardsControlAssociationsInput{SecurityControlId: &control.id},
		func(o *securityhub.ListStandardsControlAssociationsPaginatorOptions) {
			o.Limit = shControlAssociationPageLimit
		})
	var rows []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, a := range out.StandardsControlAssociationSummaries {
			standard := sv(a.StandardsArn)
			if standard == "" {
				continue
			}
			rows = append(rows, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeSecurityHubStandardsControlAssociation,
				NativeID:       securityHubControlAssociationNativeID(control.arn, standard),
				Name:           a.SecurityControlId,
				Region:         &region,
				Status:         nonEmptyPtr(string(a.AssociationStatus)),
				AttributesJSON: mustJSON(a),
				DiscoveredBy:   scanID,
			})
		}
	}
	return rows, nil
}

// upsertSecurityHubChildren persists hub children and links each to the
// per-region hub via the contains closure. Mirrors upsertMacieChildren.
func upsertSecurityHubChildren(st *store.Store, hubARN string, acct *account, batch []*store.Resource, kind string) (int, error) {
	n, err := st.UpsertResources(batch)
	if err != nil {
		return 0, fmt.Errorf("upsert securityhub %s: %w", kind, err)
	}
	parentID := store.ResourceID("aws", acct.ID, hubARN)
	pairs := make([][2]string, len(batch))
	for i, c := range batch {
		pairs[i] = [2]string{c.ID, parentID}
	}
	if err := st.RecordHierarchyBatch(pairs); err != nil {
		return 0, fmt.Errorf("closure securityhub %s: %w", kind, err)
	}
	return n, nil
}
