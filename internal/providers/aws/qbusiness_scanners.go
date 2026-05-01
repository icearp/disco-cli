package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/qbusiness"
)

func init() {
	registerService(serviceEntry{
		name: "aws:qbusiness",
		fn:   scanQBusiness,
		emits: []coverage.TypeDecl{
			{Service: "qbusiness", DiscoType: TypeQBusinessApplication},
			{Service: "qbusiness", DiscoType: TypeQBusinessDataAccessor},
			{Service: "qbusiness", DiscoType: TypeQBusinessDataSource},
			{Service: "qbusiness", DiscoType: TypeQBusinessIndex},
			{Service: "qbusiness", DiscoType: TypeQBusinessPlugin},
			{Service: "qbusiness", DiscoType: TypeQBusinessRetriever},
			{Service: "qbusiness", DiscoType: TypeQBusinessWebExperience},
		},
	})
}

type qbusinessAPI interface {
	ListApplications(context.Context, *qbusiness.ListApplicationsInput, ...func(*qbusiness.Options)) (*qbusiness.ListApplicationsOutput, error)
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
	// Replace first '/' with ':' to match AWS qbusiness ARN format `arn:aws:qbusiness:region:account:application/{id}`.
	for i := len(fmt.Sprintf("arn:aws:qbusiness:%s:%s", region, acct)); i < len(s); i++ {
		if s[i] == '/' {
			return s[:i] + ":" + s[i+1:]
		}
	}
	return s
}

func scanQBusiness(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := qbusiness.NewFromConfig(acct.cfg, func(o *qbusiness.Options) { o.Region = region })

	type appRef struct {
		id, arn string
		indices []string
	}

	// Phase 1: Applications.
	pager := qbusiness.NewListApplicationsPaginator(client, &qbusiness.ListApplicationsInput{})
	var batch []*store.Resource
	var apps []appRef
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
			apps = append(apps, appRef{id: id, arn: arn})
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

	// Phase 2: per-app children.
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
