package aws

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/qbusiness"
	qbtypes "github.com/aws/aws-sdk-go-v2/service/qbusiness/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeQBusinessApplication, Service: "qbusiness"})
	registerType(restype.Descriptor{Type: TypeQBusinessChatControlsConfiguration, Service: "qbusiness"})
	registerType(restype.Descriptor{Type: TypeQBusinessChatResponseConfiguration, Service: "qbusiness"})
	registerType(restype.Descriptor{Type: TypeQBusinessDataAccessor, Service: "qbusiness"})
	registerType(restype.Descriptor{Type: TypeQBusinessDataSource, Service: "qbusiness"})
	registerType(restype.Descriptor{Type: TypeQBusinessIndex, Service: "qbusiness"})
	registerType(restype.Descriptor{Type: TypeQBusinessPlugin, Service: "qbusiness"})
	registerType(restype.Descriptor{Type: TypeQBusinessRetriever, Service: "qbusiness"})
	registerType(restype.Descriptor{Type: TypeQBusinessSubscription, Service: "qbusiness", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeQBusinessWebExperience, Service: "qbusiness"})
	registerService(serviceEntry{
		name: "aws:qbusiness",
		fn:   scanQBusiness,
	})
}

type qbusinessAPI interface {
	GetChatControlsConfiguration(context.Context, *qbusiness.GetChatControlsConfigurationInput, ...func(*qbusiness.Options)) (*qbusiness.GetChatControlsConfigurationOutput, error)
	ListApplications(context.Context, *qbusiness.ListApplicationsInput, ...func(*qbusiness.Options)) (*qbusiness.ListApplicationsOutput, error)
	ListChatResponseConfigurations(context.Context, *qbusiness.ListChatResponseConfigurationsInput, ...func(*qbusiness.Options)) (*qbusiness.ListChatResponseConfigurationsOutput, error)
	ListSubscriptions(context.Context, *qbusiness.ListSubscriptionsInput, ...func(*qbusiness.Options)) (*qbusiness.ListSubscriptionsOutput, error)
	ListDataAccessors(context.Context, *qbusiness.ListDataAccessorsInput, ...func(*qbusiness.Options)) (*qbusiness.ListDataAccessorsOutput, error)
	ListDataSources(context.Context, *qbusiness.ListDataSourcesInput, ...func(*qbusiness.Options)) (*qbusiness.ListDataSourcesOutput, error)
	ListIndices(context.Context, *qbusiness.ListIndicesInput, ...func(*qbusiness.Options)) (*qbusiness.ListIndicesOutput, error)
	ListPlugins(context.Context, *qbusiness.ListPluginsInput, ...func(*qbusiness.Options)) (*qbusiness.ListPluginsOutput, error)
	ListRetrievers(context.Context, *qbusiness.ListRetrieversInput, ...func(*qbusiness.Options)) (*qbusiness.ListRetrieversOutput, error)
	ListWebExperiences(context.Context, *qbusiness.ListWebExperiencesInput, ...func(*qbusiness.Options)) (*qbusiness.ListWebExperiencesOutput, error)
}

func qbARN(region, acct string, segments ...string) string {
	s := fmt.Sprintf("arn:aws:qbusiness:%s:%s", region, acct)
	for _, seg := range segments {
		s += "/" + seg
	}
	// Replace first '/' with ':' to match AWS's arn:aws:qbusiness:region:account:application/{id} format.
	for i := len(fmt.Sprintf("arn:aws:qbusiness:%s:%s", region, acct)); i < len(s); i++ {
		if s[i] == '/' {
			return s[:i] + ":" + s[i+1:]
		}
	}
	return s
}

func scanQBusiness(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := qbusiness.NewFromConfig(acct.cfg, func(o *qbusiness.Options) { o.Region = region })

	// Phase 1: Applications.
	pager := qbusiness.NewListApplicationsPaginator(client, &qbusiness.ListApplicationsInput{})
	var batch []*store.Resource
	var apps []qbAppRef
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "qbusiness:ListApplications", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("qbusiness:ListApplications: %w", perr)
		}
		for _, a := range out.Applications {
			id := sv(a.ApplicationId)
			if id == "" {
				continue
			}
			arn := qbARN(region, acct.ID, "application", id)
			label := sv(a.DisplayName)
			if label == "" {
				label = id
			}
			apps = append(apps, qbAppRef{id: id, arn: arn, status: a.Status})
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeQBusinessApplication, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "qbusiness applications")
	if err != nil {
		return 0, 0, err
	}
	total += t
	inserted += i

	t, i, childErr := scanQBAppChildren(ctx, client, acct, region, st, scanID, apps)
	total += t
	inserted += i
	t, i, ccErr := scanQBChatControlsConfigurations(ctx, client, acct, region, st, scanID, apps)
	total += t
	inserted += i
	// The child lists span several ops and each error already names its op,
	// so a transient child error is reported under the service name. ccErr is
	// never transient: the chat-controls fan-out warns and skips those per
	// application.
	return total, inserted, errors.Join(demoteTransient(st, "aws:qbusiness", acct, region, childErr), ccErr)
}

type qbAppRef struct {
	id, arn string
	status  qbtypes.ApplicationStatus
	indices []string
}

// scanQBAppChildren lists each application's children. It stops at the first
// failed list and returns the counts stored before it.
func scanQBAppChildren(ctx context.Context, client qbusinessAPI, acct *account, region string, st *store.Store, scanID string, apps []qbAppRef) (total, inserted int, err error) {
	for ai := range apps {
		app := &apps[ai]
		for _, phase := range []func() (int, int, error){
			func() (int, int, error) { return scanQBDataAccessors(ctx, client, acct, region, st, scanID, app.id) },
			func() (int, int, error) {
				ids, t, i, e := scanQBIndices(ctx, client, acct, region, st, scanID, app.id, app.arn)
				app.indices = ids
				return t, i, e
			},
			func() (int, int, error) {
				return scanQBPlugins(ctx, client, acct, region, st, scanID, app.id, app.arn)
			},
			func() (int, int, error) {
				return scanQBRetrievers(ctx, client, acct, region, st, scanID, app.id, app.arn)
			},
			func() (int, int, error) {
				return scanQBWebExperiences(ctx, client, acct, region, st, scanID, app.id, app.arn)
			},
			func() (int, int, error) {
				return scanQBChatResponseConfigurations(ctx, client, acct, region, st, scanID, app.id, app.arn)
			},
			func() (int, int, error) {
				return scanQBSubscriptions(ctx, client, acct, region, st, scanID, app.id, app.arn)
			},
		} {
			t, i, perr := phase()
			if perr != nil {
				return total, inserted, perr
			}
			total += t
			inserted += i
		}
		// Per-(app, index): DataSources.
		for _, idxID := range app.indices {
			t, i, perr := scanQBDataSources(ctx, client, acct, region, st, scanID, app.id, app.arn, idxID)
			if perr != nil {
				return total, inserted, perr
			}
			total += t
			inserted += i
		}
	}
	return total, inserted, nil
}

func scanQBDataAccessors(ctx context.Context, client qbusinessAPI, acct *account, region string, st *store.Store, scanID, appID string) (int, int, error) {
	id := appID
	pager := qbusiness.NewListDataAccessorsPaginator(client, &qbusiness.ListDataAccessorsInput{ApplicationId: &id})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "qbusiness:ListDataAccessors", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("qbusiness:ListDataAccessors: %w", perr)
		}
		for _, d := range out.DataAccessors {
			arn := sv(d.DataAccessorArn)
			if arn == "" {
				continue
			}
			label := sv(d.DisplayName)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeQBusinessDataAccessor, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "qbusiness data-accessors")
}

func scanQBIndices(ctx context.Context, client qbusinessAPI, acct *account, region string, st *store.Store, scanID, appID, appARN string) ([]string, int, int, error) {
	id := appID
	pager := qbusiness.NewListIndicesPaginator(client, &qbusiness.ListIndicesInput{ApplicationId: &id})
	var batch []*store.Resource
	var ids []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "qbusiness:ListIndices", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("qbusiness:ListIndices: %w", perr)
		}
		for _, idx := range out.Indices {
			iid := sv(idx.IndexId)
			if iid == "" {
				continue
			}
			arn := fmt.Sprintf("%s/index/%s", appARN, iid)
			label := sv(idx.DisplayName)
			if label == "" {
				label = iid
			}
			ids = append(ids, iid)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeQBusinessIndex, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(idx), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "qbusiness indices")
	return ids, t, i, err
}

func scanQBPlugins(ctx context.Context, client qbusinessAPI, acct *account, region string, st *store.Store, scanID, appID, appARN string) (int, int, error) {
	id := appID
	pager := qbusiness.NewListPluginsPaginator(client, &qbusiness.ListPluginsInput{ApplicationId: &id})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "qbusiness:ListPlugins", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("qbusiness:ListPlugins: %w", perr)
		}
		for _, p := range out.Plugins {
			pid := sv(p.PluginId)
			if pid == "" {
				continue
			}
			arn := fmt.Sprintf("%s/plugin/%s", appARN, pid)
			label := sv(p.DisplayName)
			if label == "" {
				label = pid
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeQBusinessPlugin, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "qbusiness plugins")
}

func scanQBRetrievers(ctx context.Context, client qbusinessAPI, acct *account, region string, st *store.Store, scanID, appID, appARN string) (int, int, error) {
	id := appID
	pager := qbusiness.NewListRetrieversPaginator(client, &qbusiness.ListRetrieversInput{ApplicationId: &id})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "qbusiness:ListRetrievers", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("qbusiness:ListRetrievers: %w", perr)
		}
		for _, r := range out.Retrievers {
			rid := sv(r.RetrieverId)
			if rid == "" {
				continue
			}
			arn := fmt.Sprintf("%s/retriever/%s", appARN, rid)
			label := sv(r.DisplayName)
			if label == "" {
				label = rid
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeQBusinessRetriever, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "qbusiness retrievers")
}

func scanQBWebExperiences(ctx context.Context, client qbusinessAPI, acct *account, region string, st *store.Store, scanID, appID, appARN string) (int, int, error) {
	id := appID
	pager := qbusiness.NewListWebExperiencesPaginator(client, &qbusiness.ListWebExperiencesInput{ApplicationId: &id})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "qbusiness:ListWebExperiences", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("qbusiness:ListWebExperiences: %w", perr)
		}
		for _, w := range out.WebExperiences {
			wid := sv(w.WebExperienceId)
			if wid == "" {
				continue
			}
			arn := fmt.Sprintf("%s/web-experience/%s", appARN, wid)
			label := wid
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeQBusinessWebExperience, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(w), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "qbusiness web-experiences")
}

func scanQBDataSources(ctx context.Context, client qbusinessAPI, acct *account, region string, st *store.Store, scanID, appID, appARN, idxID string) (int, int, error) {
	aid := appID
	iid := idxID
	pager := qbusiness.NewListDataSourcesPaginator(client, &qbusiness.ListDataSourcesInput{ApplicationId: &aid, IndexId: &iid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "qbusiness:ListDataSources", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("qbusiness:ListDataSources: %w", perr)
		}
		for _, ds := range out.DataSources {
			did := sv(ds.DataSourceId)
			if did == "" {
				continue
			}
			arn := fmt.Sprintf("%s/index/%s/data-source/%s", appARN, idxID, did)
			label := sv(ds.DisplayName)
			if label == "" {
				label = did
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeQBusinessDataSource, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(ds), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "qbusiness data-sources")
}

func scanQBChatResponseConfigurations(ctx context.Context, client qbusinessAPI, acct *account, region string, st *store.Store, scanID, appID, appARN string) (int, int, error) {
	id := appID
	pager := qbusiness.NewListChatResponseConfigurationsPaginator(client, &qbusiness.ListChatResponseConfigurationsInput{ApplicationId: &id})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "qbusiness:ListChatResponseConfigurations", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("qbusiness:ListChatResponseConfigurations: %w", perr)
		}
		for _, c := range out.ChatResponseConfigurations {
			cid := sv(c.ChatResponseConfigurationId)
			if cid == "" {
				continue
			}
			// Synthetic off appARN so resolveQBusinessChildrenToApp can strip
			// back to the parent application ARN.
			arn := fmt.Sprintf("%s/chat-response-configuration/%s", appARN, cid)
			label := sv(c.DisplayName)
			if label == "" {
				label = cid
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeQBusinessChatResponseConfiguration, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "qbusiness chat-response-configurations")
}

func scanQBSubscriptions(ctx context.Context, client qbusinessAPI, acct *account, region string, st *store.Store, scanID, appID, appARN string) (int, int, error) {
	id := appID
	pager := qbusiness.NewListSubscriptionsPaginator(client, &qbusiness.ListSubscriptionsInput{ApplicationId: &id})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "qbusiness:ListSubscriptions", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("qbusiness:ListSubscriptions: %w", perr)
		}
		for _, s := range out.Subscriptions {
			sid := sv(s.SubscriptionId)
			if sid == "" {
				continue
			}
			arn := fmt.Sprintf("%s/subscription/%s", appARN, sid)
			label := sid
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeQBusinessSubscription, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "qbusiness subscriptions")
}

// qbChatControlsNativeID keys an application's chat controls, which have no id
// of their own (one set per application). An application ARN ends in the bare
// application id, and every other child kind adds two or more segments, so
// this single-segment suffix collides with neither.
func qbChatControlsNativeID(appARN string) string {
	return appARN + "/chat-controls-configuration"
}

// scanQBChatControlsConfigurations stores each application's chat controls.
// FAILED applications are not queried. That gate is inferred from the model's
// status doc (an application is ready to use only when ACTIVE, and FAILED is
// the only status that is neither ACTIVE nor a transition); the op documents
// no error for a FAILED application. ResourceNotFound means the application
// was deleted after it was listed. Applications still failing transiently
// (throttling, 5xx, network) after the SDK's retries are skipped and reported
// in one warning, so one such application cannot cost the others' rows or turn
// the whole service's result into a transient error.
func scanQBChatControlsConfigurations(ctx context.Context, client qbusinessAPI, acct *account, region string, st *store.Store, scanID string, apps []qbAppRef) (int, int, error) {
	const op = "qbusiness:GetChatControlsConfiguration"
	appARNs := make(map[string]string, len(apps))
	var ids []string
	for _, a := range apps {
		if a.status == qbtypes.ApplicationStatusFailed {
			continue
		}
		ids = append(ids, a.id)
		appARNs[a.id] = a.arn
	}
	var transient transientTally
	rows, err := describeFanOut(ctx, st, acct, region, op, ids, isQBAppGone,
		func(ctx context.Context, appID string) (*store.Resource, error) {
			cfg, err := getQBChatControls(ctx, client, appID)
			if isTransientNetworkError(err) {
				transient.skipped(appID, err)
				return nil, nil
			}
			if err != nil {
				return nil, err
			}
			label := appID
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeQBusinessChatControlsConfiguration, NativeID: qbChatControlsNativeID(appARNs[appID]),
				Name: &label, Region: &region, AttributesJSON: mustJSON(cfg), DiscoveredBy: scanID,
			}, nil
		})
	transient.warnSkipped(st, op, acct, region)
	if err != nil {
		return 0, 0, err
	}
	return upsertBatch(st, rows, "qbusiness chat-controls-configurations")
}

func isQBAppGone(err error) bool {
	return isAPIErrorCode(err, "ResourceNotFoundException")
}

// getQBChatControls reads every page of an application's chat controls. Only
// TopicConfigurations is paged; the first page carries the other settings, and
// the page token is dropped so a multi-page read stores the same shape as a
// single-page one.
func getQBChatControls(ctx context.Context, client qbusinessAPI, appID string) (*qbusiness.GetChatControlsConfigurationOutput, error) {
	pager := qbusiness.NewGetChatControlsConfigurationPaginator(client,
		&qbusiness.GetChatControlsConfigurationInput{ApplicationId: &appID},
		func(o *qbusiness.GetChatControlsConfigurationPaginatorOptions) {
			o.Limit = 50
			o.StopOnDuplicateToken = true
		})
	var cfg *qbusiness.GetChatControlsConfigurationOutput
	var topics []qbtypes.TopicConfiguration
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		if cfg == nil {
			cfg = out
		}
		topics = append(topics, out.TopicConfigurations...)
	}
	cfg.TopicConfigurations = topics
	cfg.NextToken = nil
	return cfg, nil
}
