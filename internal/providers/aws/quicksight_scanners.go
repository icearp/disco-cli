package aws

import (
	"cmp"
	"context"
	"errors"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	awsarn "github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/aws/aws-sdk-go-v2/service/quicksight"
	qstypes "github.com/aws/aws-sdk-go-v2/service/quicksight/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeQuickSightAccount, Service: "quicksight", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeQuickSightActionConnector, Service: "quicksight"})
	registerType(restype.Descriptor{Type: TypeQuickSightAgent, Service: "quicksight", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeQuickSightAnalysis, Service: "quicksight"})
	registerType(restype.Descriptor{Type: TypeQuickSightApp, Service: "quicksight"})
	registerType(restype.Descriptor{Type: TypeQuickSightApprovalPolicy, Service: "quicksight", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeQuickSightAssignment, Service: "quicksight"})
	registerType(restype.Descriptor{Type: TypeQuickSightBrand, Service: "quicksight"})
	registerType(restype.Descriptor{Type: TypeQuickSightCustomization, Service: "quicksight"})
	registerType(restype.Descriptor{Type: TypeQuickSightCustomPermissions, Service: "quicksight"})
	registerType(restype.Descriptor{Type: TypeQuickSightDashboard, Service: "quicksight"})
	registerType(restype.Descriptor{Type: TypeQuickSightDataSet, Service: "quicksight"})
	registerType(restype.Descriptor{Type: TypeQuickSightDataSource, Service: "quicksight"})
	registerType(restype.Descriptor{Type: TypeQuickSightDLPSetting, Service: "quicksight"})
	registerType(restype.Descriptor{Type: TypeQuickSightFlow, Service: "quicksight"})
	registerType(restype.Descriptor{Type: TypeQuickSightFolder, Service: "quicksight"})
	registerType(restype.Descriptor{Type: TypeQuickSightFolderMember, Service: "quicksight"})
	registerType(restype.Descriptor{Type: TypeQuickSightGroup, Service: "quicksight", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeQuickSightGroupMembership, Service: "quicksight"})
	registerType(restype.Descriptor{Type: TypeQuickSightKeyRegistration, Service: "quicksight", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeQuickSightKnowledgeBase, Service: "quicksight"})
	registerType(restype.Descriptor{Type: TypeQuickSightLimitsProfile, Service: "quicksight", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeQuickSightNamespace, Service: "quicksight"})
	registerType(restype.Descriptor{Type: TypeQuickSightOAuthClientApplication, Service: "quicksight"})
	registerType(restype.Descriptor{Type: TypeQuickSightRefreshSchedule, Service: "quicksight", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeQuickSightSpace, Service: "quicksight", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeQuickSightTemplate, Service: "quicksight"})
	registerType(restype.Descriptor{Type: TypeQuickSightTemplateAlias, Service: "quicksight"})
	registerType(restype.Descriptor{Type: TypeQuickSightTheme, Service: "quicksight"})
	registerType(restype.Descriptor{Type: TypeQuickSightThemeAlias, Service: "quicksight"})
	registerType(restype.Descriptor{Type: TypeQuickSightTopic, Service: "quicksight"})
	registerType(restype.Descriptor{Type: TypeQuickSightTopicRefreshSchedule, Service: "quicksight"})
	registerType(restype.Descriptor{Type: TypeQuickSightUser, Service: "quicksight"})
	registerType(restype.Descriptor{Type: TypeQuickSightVPCConnection, Service: "quicksight"})
	registerService(serviceEntry{
		name: "aws:quicksight",
		fn:   scanQuickSight,
	})
}

type quickSightAPI interface {
	ListActionConnectors(context.Context, *quicksight.ListActionConnectorsInput, ...func(*quicksight.Options)) (*quicksight.ListActionConnectorsOutput, error)
	ListAgents(context.Context, *quicksight.ListAgentsInput, ...func(*quicksight.Options)) (*quicksight.ListAgentsOutput, error)
	ListAnalyses(context.Context, *quicksight.ListAnalysesInput, ...func(*quicksight.Options)) (*quicksight.ListAnalysesOutput, error)
	ListBrands(context.Context, *quicksight.ListBrandsInput, ...func(*quicksight.Options)) (*quicksight.ListBrandsOutput, error)
	ListCustomPermissions(context.Context, *quicksight.ListCustomPermissionsInput, ...func(*quicksight.Options)) (*quicksight.ListCustomPermissionsOutput, error)
	ListDashboards(context.Context, *quicksight.ListDashboardsInput, ...func(*quicksight.Options)) (*quicksight.ListDashboardsOutput, error)
	ListDataSets(context.Context, *quicksight.ListDataSetsInput, ...func(*quicksight.Options)) (*quicksight.ListDataSetsOutput, error)
	ListDataSources(context.Context, *quicksight.ListDataSourcesInput, ...func(*quicksight.Options)) (*quicksight.ListDataSourcesOutput, error)
	ListFlows(context.Context, *quicksight.ListFlowsInput, ...func(*quicksight.Options)) (*quicksight.ListFlowsOutput, error)
	ListFolderMembers(context.Context, *quicksight.ListFolderMembersInput, ...func(*quicksight.Options)) (*quicksight.ListFolderMembersOutput, error)
	ListFolders(context.Context, *quicksight.ListFoldersInput, ...func(*quicksight.Options)) (*quicksight.ListFoldersOutput, error)
	ListGroupMemberships(context.Context, *quicksight.ListGroupMembershipsInput, ...func(*quicksight.Options)) (*quicksight.ListGroupMembershipsOutput, error)
	ListGroups(context.Context, *quicksight.ListGroupsInput, ...func(*quicksight.Options)) (*quicksight.ListGroupsOutput, error)
	ListIAMPolicyAssignments(context.Context, *quicksight.ListIAMPolicyAssignmentsInput, ...func(*quicksight.Options)) (*quicksight.ListIAMPolicyAssignmentsOutput, error)
	ListKnowledgeBases(context.Context, *quicksight.ListKnowledgeBasesInput, ...func(*quicksight.Options)) (*quicksight.ListKnowledgeBasesOutput, error)
	ListNamespaces(context.Context, *quicksight.ListNamespacesInput, ...func(*quicksight.Options)) (*quicksight.ListNamespacesOutput, error)
	ListOAuthClientApplications(context.Context, *quicksight.ListOAuthClientApplicationsInput, ...func(*quicksight.Options)) (*quicksight.ListOAuthClientApplicationsOutput, error)
	ListRefreshSchedules(context.Context, *quicksight.ListRefreshSchedulesInput, ...func(*quicksight.Options)) (*quicksight.ListRefreshSchedulesOutput, error)
	ListSpaces(context.Context, *quicksight.ListSpacesInput, ...func(*quicksight.Options)) (*quicksight.ListSpacesOutput, error)
	ListTemplates(context.Context, *quicksight.ListTemplatesInput, ...func(*quicksight.Options)) (*quicksight.ListTemplatesOutput, error)
	ListThemes(context.Context, *quicksight.ListThemesInput, ...func(*quicksight.Options)) (*quicksight.ListThemesOutput, error)
	ListTopics(context.Context, *quicksight.ListTopicsInput, ...func(*quicksight.Options)) (*quicksight.ListTopicsOutput, error)
	ListUsers(context.Context, *quicksight.ListUsersInput, ...func(*quicksight.Options)) (*quicksight.ListUsersOutput, error)
	ListVPCConnections(context.Context, *quicksight.ListVPCConnectionsInput, ...func(*quicksight.Options)) (*quicksight.ListVPCConnectionsOutput, error)
	DescribeAccountSettings(context.Context, *quicksight.DescribeAccountSettingsInput, ...func(*quicksight.Options)) (*quicksight.DescribeAccountSettingsOutput, error)
	DescribeAccountCustomization(context.Context, *quicksight.DescribeAccountCustomizationInput, ...func(*quicksight.Options)) (*quicksight.DescribeAccountCustomizationOutput, error)
	DescribeKeyRegistration(context.Context, *quicksight.DescribeKeyRegistrationInput, ...func(*quicksight.Options)) (*quicksight.DescribeKeyRegistrationOutput, error)
	ListTemplateAliases(context.Context, *quicksight.ListTemplateAliasesInput, ...func(*quicksight.Options)) (*quicksight.ListTemplateAliasesOutput, error)
	ListThemeAliases(context.Context, *quicksight.ListThemeAliasesInput, ...func(*quicksight.Options)) (*quicksight.ListThemeAliasesOutput, error)
	ListTopicRefreshSchedules(context.Context, *quicksight.ListTopicRefreshSchedulesInput, ...func(*quicksight.Options)) (*quicksight.ListTopicRefreshSchedulesOutput, error)
	ListApps(context.Context, *quicksight.ListAppsInput, ...func(*quicksight.Options)) (*quicksight.ListAppsOutput, error)
	ListApprovalPolicies(context.Context, *quicksight.ListApprovalPoliciesInput, ...func(*quicksight.Options)) (*quicksight.ListApprovalPoliciesOutput, error)
	ListDlpSettings(context.Context, *quicksight.ListDlpSettingsInput, ...func(*quicksight.Options)) (*quicksight.ListDlpSettingsOutput, error)
	ListLimitsProfiles(context.Context, *quicksight.ListLimitsProfilesInput, ...func(*quicksight.Options)) (*quicksight.ListLimitsProfilesOutput, error)
	ListTagsForResource(context.Context, *quicksight.ListTagsForResourceInput, ...func(*quicksight.Options)) (*quicksight.ListTagsForResourceOutput, error)
	DescribeAccountSubscription(context.Context, *quicksight.DescribeAccountSubscriptionInput, ...func(*quicksight.Options)) (*quicksight.DescribeAccountSubscriptionOutput, error)
}

// The codes a phase skips silently on its op's first call, when QuickSight
// says the account cannot use the op at all. A phase lists only those of
// them its op's Smithy model lists (decision #14):
//   - qsEditionGap: the error shape's doc in the API model says the
//     subscription's edition does not include the operation.
//   - qsFeatureOff: the error shape's doc says the feature is not activated
//     for the account.
//   - qsNoSubscription, on an account-level call: the account has no
//     QuickSight subscription in the region ("directory not found"). Earlier
//     versions of this scanner skipped it on every op for that reason; it is
//     unverified per op.
//   - qsPrecondition: earlier versions of this scanner recorded it on
//     ListAgents for an account with no subscription; what it means on the
//     other ops that model it is unverified.
//
// A denial is never among them: it is warned about.
const (
	qsEditionGap     = "UnsupportedUserEditionException"
	qsFeatureOff     = "InvalidRequestException"
	qsNoSubscription = "ResourceNotFoundException"
	qsPrecondition   = "PreconditionNotMetException"
)

// qsNotDeployed reports an op the region's endpoint does not serve
// (isOpNotDeployed). Newer QuickSight ops such as ListAgents answer it with
// an HTML 404 the SDK cannot decode, so the status is the only signal; a
// decoded ResourceNotFoundException is a 404 too, and is left to the codes
// the op models.
func qsNotDeployed(err error) bool {
	return isOpNotDeployed(err) && !isAPIErrorCode(err, "ResourceNotFoundException")
}

const (
	qsListTagsOp             = "quicksight:ListTagsForResource"
	qsListDataSetsOp         = "quicksight:ListDataSets"
	qsListNamespacesOp       = "quicksight:ListNamespaces"
	qsListFoldersOp          = "quicksight:ListFolders"
	qsListThemesOp           = "quicksight:ListThemes"
	qsListUsersOp            = "quicksight:ListUsers"
	qsListAppsOp             = "quicksight:ListApps"
	qsListApprovalPoliciesOp = "quicksight:ListApprovalPolicies"
	qsListDlpSettingsOp      = "quicksight:ListDlpSettings"
	qsListLimitsProfilesOp   = "quicksight:ListLimitsProfiles"
	qsListFolderMembersOp    = "quicksight:ListFolderMembers"
	qsListGroupMembershipsOp = "quicksight:ListGroupMemberships"
	qsDescribeCustomization  = "quicksight:DescribeAccountCustomization"
	qsDescribeSubscription   = "quicksight:DescribeAccountSubscription"
	// qsPageLimit is the documented MaxResults cap (1-100) of every QuickSight
	// list op the scanners page with it.
	qsPageLimit = 100
)

// qsSubscriptionGate reads the account's QuickSight subscription before any
// phase, since most accounts never subscribe and every phase would then
// fail. It reports whether the phases should run, and the error to return
// when they should not:
//   - ResourceNotFoundException (modeled) or a response without AccountInfo:
//     the account may not be subscribed, which it can do itself. QuickSight
//     has one subscription per account, chosen in a home region and usable in
//     every supported region, and whether this call in another region returns
//     the subscription or this code is undocumented. A false "disabled" would
//     drop a subscribed account's rows in every such region, so the answer is
//     confirmed by qsUnsubscribedProbe before the service is disabled; when
//     it is not confirmed the phases run, and report what they meet.
//   - an op the region does not serve (qsNotDeployed): region unavailable.
//   - a transient error: returned, so the scan is partial; the phases would
//     meet it too. A DNS error is one (isTransientNetworkError), so an
//     NXDOMAIN is returned as is, the dispatcher's region gap.
//   - the end of ctx, whatever code the cut-short call carries: returned as
//     the stop, so that a cut-short denial neither warns nor probes.
//   - a denial: warned about, then qsUnsubscribedProbe decides as for a
//     "not subscribed" answer, so that a role denied only this call still
//     scans while an unsubscribed one is disabled.
//   - any other error: the phases run, and it is returned with theirs (added
//     to p).
func qsSubscriptionGate(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, p *phaseTotals) (scan bool, err error) {
	id := acct.ID
	out, err := client.DescribeAccountSubscription(ctx, &quicksight.DescribeAccountSubscriptionInput{AwsAccountId: &id})
	switch {
	case err == nil && out.AccountInfo == nil:
		return qsUnsubscribedProbe(ctx, client, acct, fmt.Errorf("%s: no subscription in the response", qsDescribeSubscription))
	case err == nil:
		return true, nil
	case isAPIErrorCode(err, qsNoSubscription):
		return qsUnsubscribedProbe(ctx, client, acct, fmt.Errorf("%s: %w", qsDescribeSubscription, err))
	case ctx.Err() != nil:
		return false, fmt.Errorf("%s: %w", qsDescribeSubscription, ctx.Err())
	case qsNotDeployed(err):
		return false, markServiceUnavailable(fmt.Errorf("%s: %w", qsDescribeSubscription, err))
	case isTransientNetworkError(err):
		return false, fmt.Errorf("%s: %w", qsDescribeSubscription, err)
	case isAccessDenied(err):
		_ = skipIfAccessDenied(st, qsDescribeSubscription, acct.ID, region, err)
		return qsUnsubscribedProbe(ctx, client, acct, fmt.Errorf("%s: %w", qsDescribeSubscription, err))
	}
	p.addPhase(qsDescribeSubscription, 0, 0, fmt.Errorf("%s: %w", qsDescribeSubscription, err))
	return true, nil
}

// qsUnsubscribedProbe confirms the gate's "not subscribed" (gateErr) with one
// ListDataSets call: data sets are regional, and an account with no
// subscription answers it with ResourceNotFoundException ("directory not
// found"). ListDataSets does not model that code (v1.127.0), so it arrives as
// a smithy GenericAPIError and is matched by code (isAPIErrorCode); the
// answer was recorded by earlier versions of this scanner and is unverified.
// Keying on an unmodeled code is an explicit exception to decision #14
// (an unmodeled not-found fails the phase): the code is not skipped here but
// confirms what the gate's modeled ResourceNotFoundException already said,
// and any other answer, success included, runs the phases without a
// warning, so a wrong reading costs a scan of an empty account, never a
// subscribed account's rows. The end of ctx is returned as the stop.
func qsUnsubscribedProbe(ctx context.Context, client quickSightAPI, acct *account, gateErr error) (scan bool, err error) {
	id := acct.ID
	_, perr := client.ListDataSets(ctx, &quicksight.ListDataSetsInput{AwsAccountId: &id, MaxResults: sdkaws.Int32(1)})
	switch {
	case perr != nil && ctx.Err() != nil:
		return false, fmt.Errorf("%s: %w", qsListDataSetsOp, ctx.Err())
	case isAPIErrorCode(perr, qsNoSubscription):
		return false, markServiceDisabled(fmt.Errorf("%w; %s: %w", gateErr, qsListDataSetsOp, perr))
	}
	return true, nil
}

// scanQuickSight runs qsSubscriptionGate, then every phase in order: a failed phase does not skip the
// ones after it, and a phase that lists the parents of a later one hands it
// only the parents it stored. Only a failed write stops the scan, since every
// later write would fail too, and an NXDOMAIN from the first phase before
// anything was stored, the region gap, which every later phase would hit
// too. Each phase's errors are kept under the op that failed, the tag reads'
// under quicksight:ListTagsForResource, so phaseErrs.join demotes a
// transient one beside a hard one under its own op. Once rows were stored an
// NXDOMAIN is a failure of this scan, not a region gap (resultLateNXDOMAIN).
func scanQuickSight(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := quicksight.NewFromConfig(acct.cfg, func(o *quicksight.Options) { o.Region = region })
	var (
		p                                                        phaseTotals
		dataSets, namespaces, folders, templates, themes, topics []childParent
		groups                                                   []qsGroupRef
	)
	if scan, err := qsSubscriptionGate(ctx, client, acct, region, st, &p); !scan {
		return 0, 0, err
	}
	counted := func(op string, run func() (int, int, error)) func() bool {
		return func() bool {
			t, i, err := run()
			return p.addPhase(op, t, i, err)
		}
	}
	phases := []func() bool{
		func() (stop bool) {
			dataSets, stop = scanQSDataSets(ctx, client, acct, region, st, scanID, &p)
			return stop
		},
		func() (stop bool) {
			namespaces, stop = scanQSNamespaces(ctx, client, acct, region, st, scanID, &p)
			return stop
		},
		func() bool { return scanQSAnalyses(ctx, client, acct, region, st, scanID, &p) },
		func() bool { return scanQSDashboards(ctx, client, acct, region, st, scanID, &p) },
		func() bool { return scanQSDataSources(ctx, client, acct, region, st, scanID, &p) },
		func() (stop bool) {
			folders, stop = scanQSFolders(ctx, client, acct, region, st, scanID, &p)
			return stop
		},
		func() (stop bool) {
			templates, stop = scanQSTemplates(ctx, client, acct, region, st, scanID, &p)
			return stop
		},
		func() (stop bool) {
			themes, stop = scanQSThemes(ctx, client, acct, region, st, scanID, &p)
			return stop
		},
		func() (stop bool) {
			topics, stop = scanQSTopics(ctx, client, acct, region, st, scanID, &p)
			return stop
		},
		func() bool { return scanQSVPCConnections(ctx, client, acct, region, st, scanID, &p) },
		func() bool { return scanQSCustomPermissions(ctx, client, acct, region, st, scanID, &p) },
		func() bool { return scanQSActionConnectors(ctx, client, acct, region, st, scanID, &p) },
		counted("quicksight:ListRefreshSchedules", func() (int, int, error) {
			return scanQSRefreshSchedules(ctx, client, acct, region, st, scanID, dataSets)
		}),
		counted("quicksight:ListAgents", func() (int, int, error) { return scanQSAgents(ctx, client, acct, region, st, scanID) }),
		func() bool { return scanQSBrands(ctx, client, acct, region, st, scanID, &p) },
		func() bool { return scanQSFlows(ctx, client, acct, region, st, scanID, &p) },
		counted("quicksight:ListKnowledgeBases", func() (int, int, error) {
			return scanQSKnowledgeBases(ctx, client, acct, region, st, scanID)
		}),
		func() bool { return scanQSOAuthClientApplications(ctx, client, acct, region, st, scanID, &p) },
		counted("quicksight:ListSpaces", func() (int, int, error) { return scanQSSpaces(ctx, client, acct, region, st, scanID) }),
		counted("quicksight:DescribeAccountSettings", func() (int, int, error) {
			return scanQSAccountSettings(ctx, client, acct, region, st, scanID)
		}),
		func() bool { return scanQSAccountCustomization(ctx, client, acct, region, st, scanID, &p) },
		counted("quicksight:ListGroups", func() (t, i int, err error) {
			groups, t, i, err = scanQSGroups(ctx, client, acct, region, st, scanID, namespaces)
			return t, i, err
		}),
		func() bool { return scanQSUsers(ctx, client, acct, region, st, scanID, namespaces, &p) },
		counted("quicksight:ListIAMPolicyAssignments", func() (int, int, error) {
			return scanQSAssignments(ctx, client, acct, region, st, scanID, namespaces)
		}),
		counted("quicksight:ListTemplateAliases", func() (int, int, error) {
			return scanQSTemplateAliases(ctx, client, acct, region, st, scanID, templates)
		}),
		counted("quicksight:ListThemeAliases", func() (int, int, error) {
			return scanQSThemeAliases(ctx, client, acct, region, st, scanID, themes)
		}),
		counted("quicksight:ListTopicRefreshSchedules", func() (int, int, error) {
			return scanQSTopicRefreshSchedules(ctx, client, acct, region, st, scanID, topics)
		}),
		counted("quicksight:DescribeKeyRegistration", func() (int, int, error) {
			return scanQSKeyRegistration(ctx, client, acct, region, st, scanID)
		}),
		func() bool { return scanQSApps(ctx, client, acct, region, st, scanID, &p) },
		counted(qsListApprovalPoliciesOp, func() (int, int, error) {
			return scanQSApprovalPolicies(ctx, client, acct, region, st, scanID)
		}),
		func() bool { return scanQSDLPSettings(ctx, client, acct, region, st, scanID, &p) },
		counted(qsListLimitsProfilesOp, func() (int, int, error) {
			return scanQSLimitsProfiles(ctx, client, acct, region, st, scanID)
		}),
		// The member phases skip a parent whose call is still transient after
		// the SDK's retries, so no transient error leaves them.
		counted(qsListFolderMembersOp, func() (int, int, error) {
			return scanQSFolderMembers(ctx, client, acct, region, st, scanID, folders)
		}),
		counted(qsListGroupMembershipsOp, func() (int, int, error) {
			return scanQSGroupMemberships(ctx, client, acct, region, st, scanID, groups)
		}),
	}
	notRun := 0
	for i, run := range phases {
		if ctx.Err() != nil {
			notRun = len(phases) - i
			break
		}
		if run() {
			break
		}
		if i == 0 && p.total == 0 && len(p.errs) == 1 && isDNSNotFound(p.errs[0].err) {
			break
		}
	}
	p.foldStop(ctx, "quicksight", notRun)
	return p.resultLateNXDOMAIN(st, acct, region)
}

// qsTagReader reads a row's tags with ListTagsForResource, sent the row's
// NativeID: every type read through it is stored under the ARN QuickSight
// returned, and the Service Reference lists each such type as a resource of
// the op. The op takes no page token; ResourceNotFoundException is the only
// not-found code its model lists. A ManagedByProvider row (a starter theme,
// owned by the "aws" account) is not read: whether the op accepts another
// account's ARN is unverified, and a rejection would fail every scan.
//
// The phases run one at a time, so readRowTags' bound of fanoutMed reads at
// once per phase bounds the whole scan. Users are read one call each, so an
// account with many users makes as many calls.
func qsTagReader(client quickSightAPI) rowTagReader {
	return rowTagReader{
		op:       qsListTagsOp,
		notFound: []string{"ResourceNotFoundException"},
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			if r.ManagedByProvider {
				return r.TagsJSON, nil
			}
			out, err := client.ListTagsForResource(ctx, &quicksight.ListTagsForResourceInput{ResourceArn: &r.NativeID})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(out.Tags, func(t qstypes.Tag) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}

// qsPages is page with QuickSight's first-page skip: an error on the first
// page that qsNotDeployed matches, or that has one of skipCodes (the op's
// own codes for an account that cannot use it), ends the listing silently
// with nothing read. Any other first-page error, a denial included, and
// every later page's error are returned to the caller with the pages read
// before them: once a page has succeeded the account can use the op.
func qsPages[Item any](skipCodes []string, page func(context.Context, *string) ([]Item, *string, error)) func(context.Context, *string) ([]Item, *string, error) {
	return func(ctx context.Context, token *string) ([]Item, *string, error) {
		items, next, err := page(ctx, token)
		if err != nil && token == nil && (qsNotDeployed(err) || isAPIErrorCode(err, skipCodes...)) {
			return nil, nil, nil
		}
		return items, next, err
	}
}

// qsRow is the row of a listed QuickSight item, stored under its ARN with
// the item verbatim as the attributes and named label, or the ARN when label
// is empty. An item without an ARN gives no row, with the reason (what names
// the items) and label, or a fallback, as the key of the warning.
func qsRow(acct *account, region, scanID, typ, what string, arn *string, label string, item any) (*store.Resource, string, string) {
	if sv(arn) == "" {
		return nil, what + " have no ARN", cmp.Or(label, "an item without a name")
	}
	name := cmp.Or(label, *arn)
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: *arn, Name: &name, Region: &region,
		AttributesJSON: mustJSON(item), DiscoveredBy: scanID,
	}, "", ""
}

func scanQSAnalyses(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals) (storeWriteFailed bool) {
	id := acct.ID
	return storeTokenListTags(ctx, st, acct, region, "quicksight:ListAnalyses",
		qsPages([]string{qsEditionGap}, func(ctx context.Context, token *string) ([]qstypes.AnalysisSummary, *string, error) {
			out, err := client.ListAnalyses(ctx, &quicksight.ListAnalysesInput{AwsAccountId: &id, MaxResults: sdkaws.Int32(qsPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.AnalysisSummaryList, out.NextToken, nil
		}),
		func(a qstypes.AnalysisSummary) (*store.Resource, string, string) {
			return qsRow(acct, region, scanID, TypeQuickSightAnalysis, "analyses", a.Arn, cmp.Or(sv(a.Name), sv(a.AnalysisId)), a)
		},
		qsTagReader(client), p)
}

func scanQSDashboards(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals) (storeWriteFailed bool) {
	id := acct.ID
	return storeTokenListTags(ctx, st, acct, region, "quicksight:ListDashboards",
		qsPages([]string{qsEditionGap}, func(ctx context.Context, token *string) ([]qstypes.DashboardSummary, *string, error) {
			out, err := client.ListDashboards(ctx, &quicksight.ListDashboardsInput{AwsAccountId: &id, MaxResults: sdkaws.Int32(qsPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.DashboardSummaryList, out.NextToken, nil
		}),
		func(d qstypes.DashboardSummary) (*store.Resource, string, string) {
			return qsRow(acct, region, scanID, TypeQuickSightDashboard, "dashboards", d.Arn, cmp.Or(sv(d.Name), sv(d.DashboardId)), d)
		},
		qsTagReader(client), p)
}

// scanQSDataSets stores the account's data sets with their tags and returns
// the stored ones that carry a DataSetId, as the parents of the
// refresh-schedule phase.
func scanQSDataSets(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals) ([]childParent, bool) {
	id := acct.ID
	return storeParentListTags(ctx, st, acct, region, qsListDataSetsOp, "data sets", "DataSetId",
		qsPages(nil, func(ctx context.Context, token *string) ([]qstypes.DataSetSummary, *string, error) {
			out, err := client.ListDataSets(ctx, &quicksight.ListDataSetsInput{AwsAccountId: &id, MaxResults: sdkaws.Int32(qsPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.DataSetSummaries, out.NextToken, nil
		}),
		func(d qstypes.DataSetSummary) (*store.Resource, string, string) {
			return qsRow(acct, region, scanID, TypeQuickSightDataSet, "data sets", d.Arn, cmp.Or(sv(d.Name), sv(d.DataSetId)), d)
		},
		func(d qstypes.DataSetSummary) string { return sv(d.DataSetId) },
		qsTagReader(client), p)
}

func scanQSDataSources(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals) (storeWriteFailed bool) {
	id := acct.ID
	return storeTokenListTags(ctx, st, acct, region, "quicksight:ListDataSources",
		qsPages(nil, func(ctx context.Context, token *string) ([]qstypes.DataSource, *string, error) {
			out, err := client.ListDataSources(ctx, &quicksight.ListDataSourcesInput{AwsAccountId: &id, MaxResults: sdkaws.Int32(qsPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.DataSources, out.NextToken, nil
		}),
		func(d qstypes.DataSource) (*store.Resource, string, string) {
			return qsRow(acct, region, scanID, TypeQuickSightDataSource, "data sources", d.Arn, cmp.Or(sv(d.Name), sv(d.DataSourceId)), d)
		},
		qsTagReader(client), p)
}

// scanQSFolders stores the account's folders with their tags and returns the
// stored ones that carry a FolderId, as the parents of the folder-member
// phase.
func scanQSFolders(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals) ([]childParent, bool) {
	id := acct.ID
	return storeParentListTags(ctx, st, acct, region, qsListFoldersOp, "folders", "FolderId",
		qsPages([]string{qsNoSubscription, qsEditionGap}, func(ctx context.Context, token *string) ([]qstypes.FolderSummary, *string, error) {
			out, err := client.ListFolders(ctx, &quicksight.ListFoldersInput{AwsAccountId: &id, MaxResults: sdkaws.Int32(qsPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.FolderSummaryList, out.NextToken, nil
		}),
		func(f qstypes.FolderSummary) (*store.Resource, string, string) {
			return qsRow(acct, region, scanID, TypeQuickSightFolder, "folders", f.Arn, cmp.Or(sv(f.Name), sv(f.FolderId)), f)
		},
		func(f qstypes.FolderSummary) string { return sv(f.FolderId) },
		qsTagReader(client), p)
}

// scanQSTemplates stores the account's templates with their tags and returns
// the stored ones that carry a TemplateId, as the parents of the
// template-alias phase.
func scanQSTemplates(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals) ([]childParent, bool) {
	id := acct.ID
	return storeParentListTags(ctx, st, acct, region, "quicksight:ListTemplates", "templates", "TemplateId",
		qsPages([]string{qsNoSubscription, qsEditionGap}, func(ctx context.Context, token *string) ([]qstypes.TemplateSummary, *string, error) {
			out, err := client.ListTemplates(ctx, &quicksight.ListTemplatesInput{AwsAccountId: &id, MaxResults: sdkaws.Int32(qsPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.TemplateSummaryList, out.NextToken, nil
		}),
		func(t qstypes.TemplateSummary) (*store.Resource, string, string) {
			return qsRow(acct, region, scanID, TypeQuickSightTemplate, "templates", t.Arn, sv(t.Name), t)
		},
		func(t qstypes.TemplateSummary) string { return sv(t.TemplateId) },
		qsTagReader(client), p)
}

// qsStarterTheme reports whether arn is a theme QuickSight provides to every
// account: its ARN names the owner account "aws", as the starter themes'
// ARNs do (arn:aws:quicksight::aws:theme/CLASSIC). An ARN that does not
// parse is not one.
func qsStarterTheme(arn string) bool {
	owner, err := awsarn.Parse(arn)
	return err == nil && owner.AccountID == "aws"
}

// scanQSThemes stores the account's themes with their tags and returns the
// account's own stored themes that carry a ThemeId, as the parents of the
// theme-alias phase. ListThemes also returns QuickSight's starter themes,
// owned by the "aws" account: they are stored as ManagedByProvider, without
// a tag read (qsTagReader), and are no alias parents, as aliases exist only
// on the account's own themes.
func scanQSThemes(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals) ([]childParent, bool) {
	id := acct.ID
	listed, stop := storeParentListTags(ctx, st, acct, region, qsListThemesOp, "themes", "ThemeId",
		qsPages([]string{qsNoSubscription, qsEditionGap}, func(ctx context.Context, token *string) ([]qstypes.ThemeSummary, *string, error) {
			out, err := client.ListThemes(ctx, &quicksight.ListThemesInput{AwsAccountId: &id, MaxResults: sdkaws.Int32(qsPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ThemeSummaryList, out.NextToken, nil
		}),
		func(th qstypes.ThemeSummary) (*store.Resource, string, string) {
			r, why, key := qsRow(acct, region, scanID, TypeQuickSightTheme, "themes", th.Arn, sv(th.Name), th)
			if r != nil {
				r.ManagedByProvider = qsStarterTheme(r.NativeID)
			}
			return r, why, key
		},
		func(th qstypes.ThemeSummary) string { return sv(th.ThemeId) },
		qsTagReader(client), p)
	var own []childParent
	for _, t := range listed {
		if !qsStarterTheme(t.arn) {
			own = append(own, t)
		}
	}
	return own, stop
}

// scanQSTopics stores the account's topics with their tags and returns the
// stored ones that carry a TopicId, as the parents of the topic
// refresh-schedule phase.
func scanQSTopics(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals) ([]childParent, bool) {
	id := acct.ID
	return storeParentListTags(ctx, st, acct, region, "quicksight:ListTopics", "topics", "TopicId",
		qsPages(nil, func(ctx context.Context, token *string) ([]qstypes.TopicSummary, *string, error) {
			out, err := client.ListTopics(ctx, &quicksight.ListTopicsInput{AwsAccountId: &id, MaxResults: sdkaws.Int32(qsPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.TopicsSummaries, out.NextToken, nil
		}),
		func(t qstypes.TopicSummary) (*store.Resource, string, string) {
			return qsRow(acct, region, scanID, TypeQuickSightTopic, "topics", t.Arn, cmp.Or(sv(t.Name), sv(t.TopicId)), t)
		},
		func(t qstypes.TopicSummary) string { return sv(t.TopicId) },
		qsTagReader(client), p)
}

func scanQSVPCConnections(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals) (storeWriteFailed bool) {
	id := acct.ID
	return storeTokenListTags(ctx, st, acct, region, "quicksight:ListVPCConnections",
		qsPages([]string{qsEditionGap}, func(ctx context.Context, token *string) ([]qstypes.VPCConnectionSummary, *string, error) {
			out, err := client.ListVPCConnections(ctx, &quicksight.ListVPCConnectionsInput{AwsAccountId: &id, MaxResults: sdkaws.Int32(qsPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.VPCConnectionSummaries, out.NextToken, nil
		}),
		func(v qstypes.VPCConnectionSummary) (*store.Resource, string, string) {
			return qsRow(acct, region, scanID, TypeQuickSightVPCConnection, "VPC connections", v.Arn, sv(v.Name), v)
		},
		qsTagReader(client), p)
}

func scanQSCustomPermissions(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals) (storeWriteFailed bool) {
	id := acct.ID
	return storeTokenListTags(ctx, st, acct, region, "quicksight:ListCustomPermissions",
		qsPages([]string{qsPrecondition, qsNoSubscription}, func(ctx context.Context, token *string) ([]qstypes.CustomPermissions, *string, error) {
			out, err := client.ListCustomPermissions(ctx, &quicksight.ListCustomPermissionsInput{AwsAccountId: &id, MaxResults: sdkaws.Int32(qsPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.CustomPermissionsList, out.NextToken, nil
		}),
		func(c qstypes.CustomPermissions) (*store.Resource, string, string) {
			return qsRow(acct, region, scanID, TypeQuickSightCustomPermissions, "custom permissions profiles", c.Arn, sv(c.CustomPermissionsName), c)
		},
		qsTagReader(client), p)
}

// scanQSActionConnectors stores the account's action connectors with their
// tags. ListActionConnectors raises 500 InternalFailure where the (newish)
// feature isn't deployed, and the global 10-attempt adaptive retryer eats
// ~2m before giving up, so each call's retry budget is clamped and a
// first-page InternalFailure skips the phase. The op models
// InternalFailureException, not InternalFailure: skipping the unmodeled code
// is an explicit exception to decision #14, kept because it is the code the
// op was observed to answer where the feature is absent (aws/CLAUDE.md,
// "Clamp retries per-op for ops that return persistent 5xx" and the
// per-region feature-gap list), and only on the first page.
func scanQSActionConnectors(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals) (storeWriteFailed bool) {
	id := acct.ID
	return storeTokenListTags(ctx, st, acct, region, "quicksight:ListActionConnectors",
		qsPages([]string{"InternalFailure"}, func(ctx context.Context, token *string) ([]qstypes.ActionConnectorSummary, *string, error) {
			out, err := client.ListActionConnectors(ctx, &quicksight.ListActionConnectorsInput{AwsAccountId: &id, MaxResults: sdkaws.Int32(qsPageLimit), NextToken: token},
				func(o *quicksight.Options) { o.RetryMaxAttempts = 2 })
			if err != nil {
				return nil, nil, err
			}
			return out.ActionConnectorSummaries, out.NextToken, nil
		}),
		func(a qstypes.ActionConnectorSummary) (*store.Resource, string, string) {
			return qsRow(acct, region, scanID, TypeQuickSightActionConnector, "action connectors", a.Arn, cmp.Or(sv(a.Name), sv(a.ActionConnectorId)), a)
		},
		qsTagReader(client), p)
}

// scanQSRefreshSchedules stores each stored data set's refresh schedules,
// fanoutMed data sets at a time (storeTokenEachParent). ListRefreshSchedules
// takes no page token. A data set it no longer finds (ResourceNotFoundException,
// modeled) was deleted since it was listed and is skipped; a denial warns once;
// any other error is returned after the schedules read are stored.
func scanQSRefreshSchedules(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string, dataSets []childParent) (int, int, error) {
	id := acct.ID
	ids := make([]string, len(dataSets))
	for i, ds := range dataSets {
		ids[i] = ds.id
	}
	return storeTokenEachParent(ctx, st, acct, region, "quicksight:ListRefreshSchedules", []string{"ResourceNotFoundException"}, ids,
		func(ctx context.Context, ds string, _ *string) ([]qstypes.RefreshSchedule, *string, error) {
			out, err := client.ListRefreshSchedules(ctx, &quicksight.ListRefreshSchedulesInput{AwsAccountId: &id, DataSetId: &ds})
			if err != nil {
				return nil, nil, err
			}
			return out.RefreshSchedules, nil, nil
		},
		func(_ string, s qstypes.RefreshSchedule) (*store.Resource, string, string) {
			return qsRow(acct, region, scanID, TypeQuickSightRefreshSchedule, "refresh schedules", s.Arn, sv(s.ScheduleId), s)
		})
}

// scanQSNamespaces stores the account's namespaces with their tags and
// returns the stored ones that carry a Name, as the parents of the group,
// user and assignment phases: id is the namespace name those ops key on, and
// arn the namespace ARN assignments synthesize their NativeID from, as they
// carry no AWS-issued ARN.
func scanQSNamespaces(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals) ([]childParent, bool) {
	id := acct.ID
	return storeParentListTags(ctx, st, acct, region, qsListNamespacesOp, "namespaces", "Name",
		qsPages([]string{qsPrecondition, qsNoSubscription}, func(ctx context.Context, token *string) ([]qstypes.NamespaceInfoV2, *string, error) {
			out, err := client.ListNamespaces(ctx, &quicksight.ListNamespacesInput{AwsAccountId: &id, MaxResults: sdkaws.Int32(qsPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Namespaces, out.NextToken, nil
		}),
		func(n qstypes.NamespaceInfoV2) (*store.Resource, string, string) {
			return qsRow(acct, region, scanID, TypeQuickSightNamespace, "namespaces", n.Arn, sv(n.Name), n)
		},
		func(n qstypes.NamespaceInfoV2) string { return sv(n.Name) },
		qsTagReader(client), p)
}

func scanQSAgents(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	id := acct.ID
	return storeTokenList(ctx, st, acct, region, "quicksight:ListAgents",
		qsPages([]string{qsEditionGap, qsPrecondition}, func(ctx context.Context, token *string) ([]qstypes.AgentSummary, *string, error) {
			out, err := client.ListAgents(ctx, &quicksight.ListAgentsInput{AwsAccountId: &id, MaxResults: sdkaws.Int32(qsPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.AgentSummaries, out.NextToken, nil
		}),
		func(a qstypes.AgentSummary) (*store.Resource, string, string) {
			return qsRow(acct, region, scanID, TypeQuickSightAgent, "agents", a.Arn, cmp.Or(sv(a.Name), sv(a.AgentId)), a)
		})
}

func scanQSBrands(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals) (storeWriteFailed bool) {
	id := acct.ID
	return storeTokenListTags(ctx, st, acct, region, "quicksight:ListBrands",
		qsPages([]string{qsFeatureOff}, func(ctx context.Context, token *string) ([]qstypes.BrandSummary, *string, error) {
			out, err := client.ListBrands(ctx, &quicksight.ListBrandsInput{AwsAccountId: &id, MaxResults: sdkaws.Int32(qsPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Brands, out.NextToken, nil
		}),
		func(b qstypes.BrandSummary) (*store.Resource, string, string) {
			return qsRow(acct, region, scanID, TypeQuickSightBrand, "brands", b.Arn, cmp.Or(sv(b.BrandName), sv(b.BrandId)), b)
		},
		qsTagReader(client), p)
}

func scanQSFlows(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals) (storeWriteFailed bool) {
	id := acct.ID
	return storeTokenListTags(ctx, st, acct, region, "quicksight:ListFlows",
		qsPages(nil, func(ctx context.Context, token *string) ([]qstypes.FlowSummary, *string, error) {
			out, err := client.ListFlows(ctx, &quicksight.ListFlowsInput{AwsAccountId: &id, MaxResults: sdkaws.Int32(qsPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.FlowSummaryList, out.NextToken, nil
		}),
		func(f qstypes.FlowSummary) (*store.Resource, string, string) {
			return qsRow(acct, region, scanID, TypeQuickSightFlow, "flows", f.Arn, cmp.Or(sv(f.Name), sv(f.FlowId)), f)
		},
		qsTagReader(client), p)
}

// scanQSKnowledgeBases stores the account's knowledge bases without tags:
// CreateKnowledgeBase takes tags, but the Service Reference lists no
// knowledge-base resource for ListTagsForResource, so whether the op accepts
// a knowledge-base ARN is unverified. ListKnowledgeBases models
// InvalidRequestException, whose shape doc says the feature is not activated
// for the account, so it skips the phase on the first page (qsFeatureOff).
func scanQSKnowledgeBases(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	id := acct.ID
	return storeTokenList(ctx, st, acct, region, "quicksight:ListKnowledgeBases",
		qsPages([]string{qsFeatureOff, qsPrecondition}, func(ctx context.Context, token *string) ([]qstypes.KnowledgeBaseSummary, *string, error) {
			out, err := client.ListKnowledgeBases(ctx, &quicksight.ListKnowledgeBasesInput{AwsAccountId: &id, MaxResults: sdkaws.Int32(qsPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.KnowledgeBaseSummaries, out.NextToken, nil
		}),
		func(k qstypes.KnowledgeBaseSummary) (*store.Resource, string, string) {
			return qsRow(acct, region, scanID, TypeQuickSightKnowledgeBase, "knowledge bases", k.KnowledgeBaseArn, cmp.Or(sv(k.Name), sv(k.KnowledgeBaseId)), k)
		})
}

func scanQSOAuthClientApplications(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals) (storeWriteFailed bool) {
	id := acct.ID
	return storeTokenListTags(ctx, st, acct, region, "quicksight:ListOAuthClientApplications",
		qsPages([]string{qsNoSubscription}, func(ctx context.Context, token *string) ([]qstypes.OAuthClientApplicationSummary, *string, error) {
			out, err := client.ListOAuthClientApplications(ctx, &quicksight.ListOAuthClientApplicationsInput{AwsAccountId: &id, MaxResults: sdkaws.Int32(qsPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.OAuthClientApplications, out.NextToken, nil
		}),
		func(o qstypes.OAuthClientApplicationSummary) (*store.Resource, string, string) {
			return qsRow(acct, region, scanID, TypeQuickSightOAuthClientApplication, "OAuth client applications", o.Arn, cmp.Or(sv(o.Name), sv(o.OAuthClientApplicationId)), o)
		},
		qsTagReader(client), p)
}

// qsSpacesPageLimit is ListSpaces' MaxResults cap (1-500).
const qsSpacesPageLimit = 500

func scanQSSpaces(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	id := acct.ID
	return storeTokenList(ctx, st, acct, region, "quicksight:ListSpaces",
		qsPages([]string{qsNoSubscription}, func(ctx context.Context, token *string) ([]qstypes.SpaceSummary, *string, error) {
			out, err := client.ListSpaces(ctx, &quicksight.ListSpacesInput{AwsAccountId: &id, MaxResults: sdkaws.Int32(qsSpacesPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.SpaceSummaries, out.NextToken, nil
		}),
		func(sp qstypes.SpaceSummary) (*store.Resource, string, string) {
			return qsRow(acct, region, scanID, TypeQuickSightSpace, "spaces", sp.SpaceArn, cmp.Or(sv(sp.Name), sv(sp.SpaceId)), sp)
		})
}

// scanQSAccountSettings upserts the per-(account, region) settings singleton.
// ResourceNotFoundException (modeled; the default unsubscribed state) and an
// op the region does not serve (qsNotDeployed) return (0,0,nil) with no
// warning; a denial warns.
func scanQSAccountSettings(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	id := acct.ID
	out, err := client.DescribeAccountSettings(ctx, &quicksight.DescribeAccountSettingsInput{AwsAccountId: &id})
	switch {
	case qsNotDeployed(err) || isAPIErrorCode(err, qsNoSubscription):
		return 0, 0, nil
	case isAccessDenied(err):
		return 0, 0, skipIfAccessDenied(st, "quicksight:DescribeAccountSettings", acct.ID, region, err)
	case err != nil:
		return 0, 0, fmt.Errorf("quicksight:DescribeAccountSettings: %w", err)
	}
	if out.AccountSettings == nil {
		return 0, 0, nil
	}
	arn := fmt.Sprintf("arn:aws:quicksight:%s:%s:account-settings", region, acct.ID)
	label := sv(out.AccountSettings.AccountName)
	if label == "" {
		label = arn
	}
	batch := []*store.Resource{{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeQuickSightAccount, NativeID: arn,
		Name: &label, Region: &region, AttributesJSON: mustJSON(out.AccountSettings),
		DiscoveredBy: scanID,
	}}
	return upsertBatch(st, batch, "quicksight account-settings")
}

// scanQSAccountCustomization stores the per-(account, region) customization
// singleton with its tags. ResourceNotFoundException (modeled; no
// customization configured) is the default state, a silent skip, as is an op
// the region does not serve (qsNotDeployed); a denial warns. A response
// without an ARN is stored under a
// synthesized NativeID and without a tag read, as ListTagsForResource takes
// the customization's own ARN.
func scanQSAccountCustomization(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals) (storeWriteFailed bool) {
	id := acct.ID
	out, err := client.DescribeAccountCustomization(ctx, &quicksight.DescribeAccountCustomizationInput{AwsAccountId: &id})
	switch {
	case qsNotDeployed(err) || isAPIErrorCode(err, qsNoSubscription):
		return false
	case isAccessDenied(err):
		return p.addPhase(qsDescribeCustomization, 0, 0, skipIfAccessDenied(st, qsDescribeCustomization, acct.ID, region, err))
	case err != nil:
		return p.addPhase(qsDescribeCustomization, 0, 0, fmt.Errorf("%s: %w", qsDescribeCustomization, err))
	}
	if out.AccountCustomization == nil {
		return false
	}
	arn := cmp.Or(sv(out.Arn), fmt.Sprintf("arn:aws:quicksight:%s:%s:account-customization", region, acct.ID))
	rows := []*store.Resource{{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeQuickSightCustomization, NativeID: arn,
		Name: &arn, Region: &region, AttributesJSON: mustJSON(out.AccountCustomization),
		DiscoveredBy: scanID,
	}}
	if sv(out.Arn) == "" {
		t, i, err := upsertBatch(st, rows, qsDescribeCustomization)
		return p.addPhase(qsDescribeCustomization, t, i, err)
	}
	return storeRowsTags(ctx, st, acct, region, qsDescribeCustomization, rows, nil, nil, qsTagReader(client), p)
}

// qsGroupRef is a stored group with the namespace it was listed in: the Group
// element names no namespace, and ListGroupMemberships needs it.
type qsGroupRef struct{ namespace, name, arn string }

func scanQSGroups(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string, namespaces []childParent) ([]qsGroupRef, int, int, error) {
	id := acct.ID
	var (
		groups   []qsGroupRef
		unlisted skipTally
	)
	r := qsReadNamespaces(ctx, "quicksight:ListGroups", []string{qsPrecondition, "ResourceUnavailableException"}, namespaces,
		func(ctx context.Context, ns string, token *string) ([]qstypes.Group, *string, error) {
			out, err := client.ListGroups(ctx, &quicksight.ListGroupsInput{AwsAccountId: &id, Namespace: &ns, MaxResults: sdkaws.Int32(qsPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.GroupList, out.NextToken, nil
		},
		func(ns string, g qstypes.Group) (*store.Resource, string, string) {
			row, why, key := qsRow(acct, region, scanID, TypeQuickSightGroup, "groups", g.Arn, sv(g.GroupName), g)
			if row == nil {
				return nil, why, cmp.Or(sv(g.GroupName), "a group in namespace "+ns)
			}
			if gn := sv(g.GroupName); gn != "" {
				groups = append(groups, qsGroupRef{namespace: ns, name: gn, arn: row.NativeID})
			} else {
				unlisted.skipped(row.NativeID, nil)
			}
			return row, why, key
		})
	t, i, err := r.store(st, acct, region, func() {
		warnQSParentsUnlisted(st, acct, region, qsListGroupMembershipsOp, unlisted.count, "group(s)", "GroupName", unlisted.first)
	})
	if errors.Is(err, store.ErrStoreWrite) {
		return nil, t, i, err
	}
	return groups, t, i, err
}

// scanQSUsers stores each namespace's users with their tags, one
// ListTagsForResource call per user, the namespaces read as
// qsReadNamespaces says. When ctx ends while the namespaces are read, the
// users read are stored without a tag read, so that the stop is one error.
func scanQSUsers(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string, namespaces []childParent, p *phaseTotals) (storeWriteFailed bool) {
	id := acct.ID
	r := qsReadNamespaces(ctx, qsListUsersOp, []string{qsPrecondition, "ResourceUnavailableException"}, namespaces,
		func(ctx context.Context, ns string, token *string) ([]qstypes.User, *string, error) {
			out, err := client.ListUsers(ctx, &quicksight.ListUsersInput{AwsAccountId: &id, Namespace: &ns, MaxResults: sdkaws.Int32(qsPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.UserList, out.NextToken, nil
		},
		func(ns string, u qstypes.User) (*store.Resource, string, string) {
			row, why, _ := qsRow(acct, region, scanID, TypeQuickSightUser, "users", u.Arn, sv(u.UserName), u)
			return row, why, cmp.Or(sv(u.UserName), "a user in namespace "+ns)
		})
	var (
		errs    rowTagErrs
		tagWarn func()
	)
	if r.stopped {
		tagWarn = func() {}
	} else {
		r.out.rows, tagWarn, errs.hard, errs.transient = readRowTags(ctx, st, acct, region, qsTagReader(client), r.out.rows)
	}
	total, inserted, err := r.store(st, acct, region, tagWarn)
	return errs.addPhase(p, qsListTagsOp, qsListUsersOp, total, inserted, err)
}

// scanQSAssignments stores each namespace's IAM policy assignments, read as
// qsReadNamespaces says. Assignments carry no AWS-issued ARN, so each is
// keyed {namespaceARN}/assignment/{name}, which the resolver strips back to
// the namespace.
func scanQSAssignments(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string, namespaces []childParent) (int, int, error) {
	id := acct.ID
	nsARN := make(map[string]string, len(namespaces))
	for _, ns := range namespaces {
		nsARN[ns.id] = ns.arn
	}
	r := qsReadNamespaces(ctx, "quicksight:ListIAMPolicyAssignments", nil, namespaces,
		func(ctx context.Context, ns string, token *string) ([]qstypes.IAMPolicyAssignmentSummary, *string, error) {
			out, err := client.ListIAMPolicyAssignments(ctx, &quicksight.ListIAMPolicyAssignmentsInput{AwsAccountId: &id, Namespace: &ns, MaxResults: sdkaws.Int32(qsPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.IAMPolicyAssignments, out.NextToken, nil
		},
		func(ns string, a qstypes.IAMPolicyAssignmentSummary) (*store.Resource, string, string) {
			an := sv(a.AssignmentName)
			if an == "" {
				return nil, "IAM policy assignments have no AssignmentName", "an assignment in namespace " + ns
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeQuickSightAssignment, NativeID: fmt.Sprintf("%s/assignment/%s", nsARN[ns], an),
				Name: &an, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			}, "", ""
		})
	return r.store(st, acct, region, nil)
}

// qsNamespaceRead is what qsReadNamespaces read: the rows and outcomes of
// every namespace, the namespaces skipped per warned code, and whether ctx
// ended before every namespace was read.
type qsNamespaceRead struct {
	out     parentOutcomes
	codes   []string
	skipped map[string]*skipTally
	stopped bool
}

// qsReadNamespaces reads op's items for each namespace in turn and builds
// their rows (parentOutcomes, as storeTokenPerParent does). Per namespace:
//   - ResourceNotFoundException, modeled by every per-namespace op: the
//     namespace was deleted since it was listed, skipped silently when
//     nothing was read from it and with a warning otherwise;
//   - one of warnCodes on the first page: skipped, with one warning per code
//     after the write, as what QuickSight means by it here is unverified;
//   - a denial: warned about once;
//   - any other error, a warned code on a later page included: returned
//     after the rows read before it are stored.
//
// Once ctx ends no further namespace is read, and the stop, the in-flight
// read's included, is one error.
func qsReadNamespaces[Item any](
	ctx context.Context, op string, warnCodes []string, namespaces []childParent,
	page func(ctx context.Context, ns string, token *string) ([]Item, *string, error),
	row func(ns string, it Item) (*store.Resource, string, string),
) *qsNamespaceRead {
	r := &qsNamespaceRead{
		out:   parentOutcomes{op: op, notFound: []string{"ResourceNotFoundException"}},
		codes: warnCodes, skipped: map[string]*skipTally{},
	}
	for _, code := range warnCodes {
		r.skipped[code] = &skipTally{}
	}
	firstPageSkip := func(ctx context.Context, ns string, token *string) ([]Item, *string, error) {
		items, next, err := page(ctx, ns, token)
		if err != nil && token == nil {
			for _, code := range warnCodes {
				if r.skipped[code].dropCode(code, ns, err) {
					return nil, nil, nil
				}
			}
		}
		return items, next, err
	}
	for _, ns := range namespaces {
		if ctx.Err() != nil {
			break
		}
		read := readParentPages(ctx, ns.id, firstPageSkip)
		if read.err != nil && ctx.Err() != nil && errors.Is(read.err, ctx.Err()) {
			read.err = nil
		}
		addParentRows(&r.out, ns.id, read, row)
	}
	if err := ctx.Err(); err != nil {
		r.stopped = true
		r.out.stopped(fmt.Errorf("namespaces not all read: %w", err))
	}
	return r
}

// store writes r's rows, then sends its warnings and warn (nil for none),
// and returns the counts and errors; after a failed write it warns nothing.
func (r *qsNamespaceRead) store(st *store.Store, acct *account, region string, warn func()) (int, int, error) {
	total, inserted, err := r.out.store(st, acct, region)
	if errors.Is(err, store.ErrStoreWrite) {
		return total, inserted, err
	}
	for _, code := range r.codes {
		r.skipped[code].warnCode(st, r.out.op, acct, region, code, "namespace(s)", "what QuickSight means by it on this call is unverified")
	}
	if warn != nil {
		warn()
	}
	return total, inserted, err
}

// qsKeyRegistrationAttrs is DescribeKeyRegistrationOutput minus its response
// envelope (RequestId, Status, ResultMetadata): RequestId differs on every
// call and would version-split the row on every scan. Field names are the SDK's.
type qsKeyRegistrationAttrs struct {
	AwsAccountID    *string `json:"AwsAccountId"`
	KeyRegistration []qstypes.RegisteredCustomerManagedKey
	QDataKey        *qstypes.QDataKey
}

// scanQSKeyRegistration upserts the per-(account, region) customer-managed-key
// registration. Its model lists no code for an account that cannot use it
// (decision #14), so only an op the region does not serve (qsNotDeployed)
// is skipped silently and a denial with a warning. A row is stored only for
// a customer registration: a registered key, or a CMK as the Q data key (the
// AWS_OWNED default is QuickSight's, not the customer's).
func scanQSKeyRegistration(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	id := acct.ID
	out, err := client.DescribeKeyRegistration(ctx, &quicksight.DescribeKeyRegistrationInput{AwsAccountId: &id})
	switch {
	case qsNotDeployed(err):
		return 0, 0, nil
	case isAccessDenied(err):
		return 0, 0, skipIfAccessDenied(st, "quicksight:DescribeKeyRegistration", acct.ID, region, err)
	case err != nil:
		return 0, 0, fmt.Errorf("quicksight:DescribeKeyRegistration: %w", err)
	}
	cmkDataKey := out.QDataKey != nil && out.QDataKey.QDataKeyType == qstypes.QDataKeyTypeCmk
	if len(out.KeyRegistration) == 0 && !cmkDataKey {
		return 0, 0, nil
	}
	arn := fmt.Sprintf("arn:aws:quicksight:%s:%s:key-registration", region, acct.ID)
	label := arn
	batch := []*store.Resource{{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeQuickSightKeyRegistration, NativeID: arn,
		Name: &label, Region: &region, DiscoveredBy: scanID,
		AttributesJSON: mustJSON(qsKeyRegistrationAttrs{
			AwsAccountID: out.AwsAccountId, KeyRegistration: out.KeyRegistration, QDataKey: out.QDataKey,
		}),
	}}
	return upsertBatch(st, batch, "quicksight key-registration")
}

// qsChildNotFound is the code every QuickSight child op models for a parent
// deleted since it was listed (storeChildrenEachParent).
var qsChildNotFound = []string{"ResourceNotFoundException"}

// scanQSTemplateAliases lists each template's aliases (storeChildrenEachParent).
func scanQSTemplateAliases(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string, templates []childParent) (int, int, error) {
	id := acct.ID
	return storeChildrenEachParent(ctx, st, acct, region, "quicksight:ListTemplateAliases", qsChildNotFound, templates, nil, nil,
		func(ctx context.Context, p childParent, token *string) ([]qstypes.TemplateAlias, *string, error) {
			out, err := client.ListTemplateAliases(ctx, &quicksight.ListTemplateAliasesInput{AwsAccountId: &id, TemplateId: &p.id, MaxResults: sdkaws.Int32(qsPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.TemplateAliasList, out.NextToken, nil
		},
		func(_ childParent, a qstypes.TemplateAlias) (*store.Resource, string, string) {
			return qsChildRow(acct, region, scanID, TypeQuickSightTemplateAlias, "template aliases", a.Arn, a.AliasName, a)
		})
}

// qsChildRow is the row of a child stored under its own ARN and named name;
// a child without an ARN gives no row, with the reason (what names the
// children) and its name, or a fallback, as the key of the warning.
func qsChildRow(acct *account, region, scanID, typ, what string, arn, name *string, item any) (*store.Resource, string, string) {
	if sv(arn) == "" {
		return nil, what + " have no ARN", cmp.Or(sv(name), "an item without a name")
	}
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: *arn, Name: name, Region: &region, AttributesJSON: mustJSON(item), DiscoveredBy: scanID,
	}, "", ""
}

// scanQSThemeAliases lists each theme's aliases (storeChildrenEachParent).
func scanQSThemeAliases(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string, themes []childParent) (int, int, error) {
	id := acct.ID
	return storeChildrenEachParent(ctx, st, acct, region, "quicksight:ListThemeAliases", qsChildNotFound, themes, nil, nil,
		func(ctx context.Context, p childParent, token *string) ([]qstypes.ThemeAlias, *string, error) {
			out, err := client.ListThemeAliases(ctx, &quicksight.ListThemeAliasesInput{AwsAccountId: &id, ThemeId: &p.id, MaxResults: sdkaws.Int32(qsPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ThemeAliasList, out.NextToken, nil
		},
		func(_ childParent, a qstypes.ThemeAlias) (*store.Resource, string, string) {
			return qsChildRow(acct, region, scanID, TypeQuickSightThemeAlias, "theme aliases", a.Arn, a.AliasName, a)
		})
}

// scanQSTopicRefreshSchedules lists each topic's per-dataset refresh schedules
// (storeChildrenEachParent; the op takes no page token). A schedule carries no ARN
// and is addressed by (topic, dataset) — the Describe/Update/Delete ops take
// TopicId + DatasetId — so its NativeID is {topicARN}/refresh-schedule/{datasetId}.
// ListTopicRefreshSchedules has no resource in the service reference, so a
// deny there is account-wide and every topic is skipped under one warning.
func scanQSTopicRefreshSchedules(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string, topics []childParent) (int, int, error) {
	id := acct.ID
	return storeChildrenEachParent(ctx, st, acct, region, "quicksight:ListTopicRefreshSchedules", qsChildNotFound, topics, nil, nil,
		func(ctx context.Context, p childParent, _ *string) ([]qstypes.TopicRefreshScheduleSummary, *string, error) {
			out, err := client.ListTopicRefreshSchedules(ctx, &quicksight.ListTopicRefreshSchedulesInput{AwsAccountId: &id, TopicId: &p.id})
			if err != nil {
				return nil, nil, err
			}
			return out.RefreshSchedules, nil, nil
		},
		func(p childParent, s qstypes.TopicRefreshScheduleSummary) (*store.Resource, string, string) {
			dsid := sv(s.DatasetId)
			if dsid == "" {
				return nil, "topic refresh schedules have no DatasetId", cmp.Or(sv(s.DatasetName), "a schedule of topic "+p.id)
			}
			label := cmp.Or(sv(s.DatasetName), dsid)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeQuickSightTopicRefreshSchedule, NativeID: p.arn + "/refresh-schedule/" + dsid,
				Name: &label, Region: &region, AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			}, "", ""
		})
}

// qsARNInOtherRegion reports whether arn names a region other than region. The
// governance lists (ListApprovalPolicies, ListLimitsProfiles) may be
// account-global, which is unverified; dropping another region's rows stores a
// global row once, in its own region, and changes nothing if the ops are
// regional. An ARN that does not parse, or names no region, is kept: it cannot
// be placed, and dropping it in every region would lose the resource, while
// keeping it costs at most a rewrite per scanned region.
func qsARNInOtherRegion(arn, region string) bool {
	parsed, err := awsarn.Parse(arn)
	return err == nil && parsed.Region != "" && parsed.Region != region
}

// qsInRegion returns the items whose ARN (arn reads it) names no other
// region than region (qsARNInOtherRegion): the others are that region's to
// store, so they are dropped without a warning.
func qsInRegion[Item any](items []Item, region string, arn func(Item) *string) []Item {
	var kept []Item
	for _, it := range items {
		if !qsARNInOtherRegion(sv(arn(it)), region) {
			kept = append(kept, it)
		}
	}
	return kept
}

func scanQSApps(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals) (storeWriteFailed bool) {
	id := acct.ID
	return storeTokenListTags(ctx, st, acct, region, qsListAppsOp,
		qsPages(nil, func(ctx context.Context, token *string) ([]qstypes.AppSummary, *string, error) {
			out, err := client.ListApps(ctx, &quicksight.ListAppsInput{AwsAccountId: &id, MaxResults: sdkaws.Int32(qsPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.AppSummaryList, out.NextToken, nil
		}),
		func(a qstypes.AppSummary) (*store.Resource, string, string) {
			r, why, key := qsRow(acct, region, scanID, TypeQuickSightApp, "apps", a.Arn, cmp.Or(sv(a.Name), sv(a.AppId)), a)
			if r != nil {
				r.CreatedAt = tp(a.CreatedTime)
			}
			return r, why, key
		},
		qsTagReader(client), p)
}

// scanQSApprovalPolicies lists the calling account's approval policies;
// ListApprovalPolicies, unlike the other account-level lists, takes no account id.
func scanQSApprovalPolicies(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, qsListApprovalPoliciesOp,
		qsPages([]string{qsNoSubscription}, func(ctx context.Context, token *string) ([]qstypes.ApprovalPolicy, *string, error) {
			out, err := client.ListApprovalPolicies(ctx, &quicksight.ListApprovalPoliciesInput{MaxResults: sdkaws.Int32(qsPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return qsInRegion(out.Policies, region, func(p qstypes.ApprovalPolicy) *string { return p.PolicyArn }), out.NextToken, nil
		}),
		func(pol qstypes.ApprovalPolicy) (*store.Resource, string, string) {
			r, why, key := qsRow(acct, region, scanID, TypeQuickSightApprovalPolicy, "approval policies", pol.PolicyArn, cmp.Or(sv(pol.Name), sv(pol.PolicyId)), pol)
			if r != nil {
				r.CreatedAt = tp(pol.CreatedAt)
			}
			return r, why, key
		})
}

func scanQSDLPSettings(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals) (storeWriteFailed bool) {
	id := acct.ID
	// ListDlpSettings models InvalidRequestException as "You don't have this
	// feature activated for your account".
	return storeTokenListTags(ctx, st, acct, region, qsListDlpSettingsOp,
		qsPages([]string{qsFeatureOff}, func(ctx context.Context, token *string) ([]qstypes.DlpSettingSummary, *string, error) {
			out, err := client.ListDlpSettings(ctx, &quicksight.ListDlpSettingsInput{AwsAccountId: &id, MaxResults: sdkaws.Int32(qsPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.DlpSettingSummaries, out.NextToken, nil
		}),
		func(d qstypes.DlpSettingSummary) (*store.Resource, string, string) {
			r, why, key := qsRow(acct, region, scanID, TypeQuickSightDLPSetting, "DLP settings", d.Arn, cmp.Or(sv(d.Name), sv(d.DlpSettingId)), d)
			if r != nil {
				r.Status, r.CreatedAt = nonEmptyPtr(string(d.Status)), tp(d.CreatedAt)
			}
			return r, why, key
		},
		qsTagReader(client), p)
}

func scanQSLimitsProfiles(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	id := acct.ID
	return storeTokenList(ctx, st, acct, region, qsListLimitsProfilesOp,
		qsPages(nil, func(ctx context.Context, token *string) ([]qstypes.LimitsProfile, *string, error) {
			out, err := client.ListLimitsProfiles(ctx, &quicksight.ListLimitsProfilesInput{AccountId: &id, MaxResults: sdkaws.Int32(qsPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return qsInRegion(out.Profiles, region, func(p qstypes.LimitsProfile) *string { return p.Arn }), out.NextToken, nil
		}),
		func(lp qstypes.LimitsProfile) (*store.Resource, string, string) {
			r, why, key := qsRow(acct, region, scanID, TypeQuickSightLimitsProfile, "limits profiles", lp.Arn, cmp.Or(sv(lp.ProfileName), sv(lp.ProfileId)), lp)
			if r != nil {
				r.CreatedAt = tp(lp.CreatedAt)
			}
			return r, why, key
		})
}

// qsMemberNativeID keys a folder or group member row by the member's ARN under
// its parent: {parentARN}/member/{memberARN}. The member ARN goes last because
// it contains "/".
func qsMemberNativeID(parentARN, memberARN string) string {
	return parentARN + "/member/" + memberARN
}

// warnQSParentsUnlisted warns once that count stored parents (what, e.g.
// "folder(s)") lacked the key op needs, so their children were not listed.
func warnQSParentsUnlisted(st *store.Store, acct *account, region, op string, count int, what, key, first string) {
	if count == 0 {
		return
	}
	st.ReportWarning(store.ScanWarning{
		Provider: "aws", Service: op, Scope: acct.ID + "/" + region,
		Message: fmt.Sprintf("%d %s stored without a %s, so their members were not listed, first %s", count, what, key, first),
	})
}

// scanQSFolderMembers stores one row per asset in each folder the folder phase
// stored (storeChildrenEachParent). A member carries only an id and an ARN, and each
// asset kind's Create op takes its own id, so ids may repeat across kinds and
// the row is keyed by the member ARN. A folder still failing transiently
// after the SDK's retries is skipped under one warning; any other error, the
// op's edition and parameter codes and an NXDOMAIN (dropUnlessNXDOMAIN)
// included, is returned: the folder phase
// already succeeded, so the account has folders.
func scanQSFolderMembers(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string, folders []childParent) (int, int, error) {
	id := acct.ID
	var transient transientTally
	return storeChildrenEachParent(ctx, st, acct, region, qsListFolderMembersOp, qsChildNotFound, folders, transient.dropUnlessNXDOMAIN,
		func() { transient.warnSkipped(st, qsListFolderMembersOp, acct, region) },
		func(ctx context.Context, p childParent, token *string) ([]qstypes.MemberIdArnPair, *string, error) {
			out, err := client.ListFolderMembers(ctx, &quicksight.ListFolderMembersInput{AwsAccountId: &id, FolderId: &p.id, MaxResults: sdkaws.Int32(qsPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.FolderMemberList, out.NextToken, nil
		},
		func(p childParent, m qstypes.MemberIdArnPair) (*store.Resource, string, string) {
			arn := sv(m.MemberArn)
			if arn == "" {
				return nil, "folder members have no MemberArn", cmp.Or(sv(m.MemberId), "a member of folder "+p.id)
			}
			label := cmp.Or(sv(m.MemberId), arn)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeQuickSightFolderMember, NativeID: qsMemberNativeID(p.arn, arn),
				Name: &label, Region: &region, AttributesJSON: mustJSON(m), DiscoveredBy: scanID,
			}, "", ""
		})
}

// scanQSGroupMemberships stores one row per member user of each group the group
// phase stored, keyed {groupARN}/member/{userARN} (storeChildrenEachParent). A group
// still failing transiently after the SDK's retries is skipped under one
// warning. PreconditionNotMetException and ResourceUnavailableException are
// modeled for the op, but what they mean on this call is unverified: each
// skips that group with one warning per code, so that one group QuickSight
// will not list cannot fail every scan. Any other error, an NXDOMAIN
// included (dropUnlessNXDOMAIN), is returned.
func scanQSGroupMemberships(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string, groups []qsGroupRef) (int, int, error) {
	const why = "QuickSight may refuse the members of a group it cannot list yet or does not manage (unverified)"
	id := acct.ID
	// Keyed by ARN: errors and warnings then name the namespace, which tells
	// apart same-named groups in two namespaces.
	parents := make([]childParent, len(groups))
	refOf := make(map[string]qsGroupRef, len(groups))
	for i, g := range groups {
		parents[i] = childParent{id: g.arn, arn: g.arn}
		refOf[g.arn] = g
	}
	var (
		precondition, unavailable skipTally
		transient                 transientTally
	)
	skip := func(parent string, err error) bool {
		return precondition.dropCode("PreconditionNotMetException", parent, err) ||
			unavailable.dropCode("ResourceUnavailableException", parent, err) ||
			transient.dropUnlessNXDOMAIN(parent, err)
	}
	warn := func() {
		precondition.warnCode(st, qsListGroupMembershipsOp, acct, region, "PreconditionNotMetException", "group(s)", why)
		unavailable.warnCode(st, qsListGroupMembershipsOp, acct, region, "ResourceUnavailableException", "group(s)", why)
		transient.warnSkipped(st, qsListGroupMembershipsOp, acct, region)
	}
	return storeChildrenEachParent(ctx, st, acct, region, qsListGroupMembershipsOp, qsChildNotFound, parents, skip, warn,
		func(ctx context.Context, p childParent, token *string) ([]qstypes.GroupMember, *string, error) {
			g := refOf[p.arn]
			out, err := client.ListGroupMemberships(ctx, &quicksight.ListGroupMembershipsInput{
				AwsAccountId: &id, Namespace: &g.namespace, GroupName: &g.name, MaxResults: sdkaws.Int32(qsPageLimit), NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.GroupMemberList, out.NextToken, nil
		},
		func(p childParent, m qstypes.GroupMember) (*store.Resource, string, string) {
			arn := sv(m.Arn)
			if arn == "" {
				return nil, "group members have no Arn", cmp.Or(sv(m.MemberName), "a member of group "+p.arn)
			}
			label := cmp.Or(sv(m.MemberName), arn)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeQuickSightGroupMembership, NativeID: qsMemberNativeID(p.arn, arn),
				Name: &label, Region: &region, AttributesJSON: mustJSON(m), DiscoveredBy: scanID,
			}, "", ""
		})
}
