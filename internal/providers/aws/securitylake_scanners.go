package aws

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/securitylake"
	sltypes "github.com/aws/aws-sdk-go-v2/service/securitylake/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

// isSecurityLakeNotEnabled disambiguates the not-enabled shapes from a real IAM
// denial. Real IAM denials always name the action ("User: arn:... is not
// authorized to perform: <action>"), so they still warn.
//
// AWS reports the SAME not-enabled account state in three phrasings across two
// error classes, and which one you get depends on the regional endpoint. Two
// arrive as an access-denied code: "must be a delegated Security Lake
// administrator account" (delegation prerequisite missing) and the canned
// action-less "account is not authorized to perform this operation" (never
// onboarded). The third arrives as a ResourceNotFoundException carrying HTTP
// 404 — "Security Lake isn't enabled for your account in any Regions" — which
// is why the access-denied check cannot be a precondition for all of them.
//
// Measured on one account with Security Lake off everywhere: us-east-1,
// us-east-2, eu-west-1 and ap-northeast-1 answered ListSubscribers with the
// delegated-administrator phrasing, while eu-west-2 alone answered
// ResourceNotFoundException. Identical account state, different regional
// answer — so a single unmatched region turned an account-level fact into a
// per-scan hard error that marked every scan partial. Compare
// isStorageLensHomeRegionGap, where AWS likewise phrases one fact two ways.
// Every branch reads the typed message via isAPIErrorWithMessage rather than
// err.Error(), so a change in how the SDK renders its wrappers cannot silently
// stop a branch matching.
func isSecurityLakeNotEnabled(err error) bool {
	return isAPIErrorWithMessage(err, "ResourceNotFoundException", "Security Lake isn't enabled for your account") ||
		isAccessDeniedWithMessage(err, "delegated Security Lake administrator") ||
		isAccessDeniedWithMessage(err, "account is not authorized to perform this operation")
}

func init() {
	registerType(restype.Descriptor{Type: TypeSecurityLakeDataLake, Service: "security-lake"})
	registerType(restype.Descriptor{Type: TypeSecurityLakeSubscriber, Service: "security-lake"})
	registerType(restype.Descriptor{Type: TypeSecurityLakeDataLakeSource, Service: "security-lake"})
	registerService(serviceEntry{
		name: "aws:security-lake",
		fn:   scanSecurityLake,
	})
}

type securityLakeAPI interface {
	ListDataLakes(context.Context, *securitylake.ListDataLakesInput, ...func(*securitylake.Options)) (*securitylake.ListDataLakesOutput, error)
	ListSubscribers(context.Context, *securitylake.ListSubscribersInput, ...func(*securitylake.Options)) (*securitylake.ListSubscribersOutput, error)
	GetDataLakeSources(context.Context, *securitylake.GetDataLakeSourcesInput, ...func(*securitylake.Options)) (*securitylake.GetDataLakeSourcesOutput, error)
	ListLogSources(context.Context, *securitylake.ListLogSourcesInput, ...func(*securitylake.Options)) (*securitylake.ListLogSourcesOutput, error)
}

const (
	slDataLakesOp       = "securitylake:ListDataLakes"
	slSubscribersOp     = "securitylake:ListSubscribers"
	slDataLakeSourcesOp = "securitylake:GetDataLakeSources"
	slLogSourcesOp      = "securitylake:ListLogSources"
	// slSourcesPageLimit is the model's MaxResults cap for GetDataLakeSources
	// and ListLogSources, which share the MaxResults shape.
	slSourcesPageLimit = 100
)

// scanSecurityLake discovers Security Lake data lakes (per-region), subscribers
// and the data lake sources.
//
// AWS::SecurityLake::SubscriberNotification is skipped: the SDK exposes only
// Create/Delete/Update, no list endpoint.
func scanSecurityLake(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := securitylake.NewFromConfig(acct.cfg, func(o *securitylake.Options) { o.Region = region })
	return scanSecurityLakeWith(ctx, client, acct, region, st, scanID)
}

// scanSecurityLakeWith runs the data lake phase, then the subscriber phase
// unless the data lake listing failed hard (a throttle does not skip
// subscribers), then the data lake source phase whatever they
// returned, unless the data lake listing showed Security Lake is not enabled. The
// errors are joined by phaseErrs under their own ops: a transient one alone, or
// beside transient ones only, marks the scan partial; beside a hard one it is
// demoted to a warning under the op that failed.
func scanSecurityLakeWith(ctx context.Context, client securityLakeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	total, inserted, storedLakes, err := scanSLDataLakes(ctx, client, acct, region, st, scanID)
	if errors.Is(err, errServiceDisabled) {
		return total, inserted, err
	}
	var subsErr error
	if err == nil || isDemotableTransient(err) {
		var t, i int
		t, i, subsErr = scanSLSubscribers(ctx, client, acct, region, st, scanID)
		total, inserted = total+t, inserted+i
	}
	t, i, derr, lerr := scanSLDataLakeSources(ctx, client, acct, region, st, scanID, storedLakes)
	total, inserted = total+t, inserted+i
	// The dispatcher reads the not-enabled sentinel ahead of a hard error and
	// discards every count with it, so it stands only when no other phase
	// stored a row or failed; otherwise the subscribers' answer is a warning.
	if errors.Is(subsErr, errServiceDisabled) && (total > 0 || err != nil || derr != nil || lerr != nil) {
		st.ReportWarning(store.ScanWarning{
			Provider: "aws", Service: slSubscribersOp, Scope: acct.ID + "/" + region,
			Message: "subscribers not listed: " + subsErr.Error(),
		})
		subsErr = nil
	}
	var errs phaseErrs
	errs.add(slDataLakesOp, err)
	errs.add(slSubscribersOp, subsErr)
	errs.add(slDataLakeSourcesOp, derr)
	errs.add(slLogSourcesOp, lerr)
	return total, inserted, errs.join(st, acct, region)
}

// scanSLDataLakes stores the region's data lakes and returns the ARNs it
// stored, so the source phase links only to data lake rows that exist. The
// set is nil when the listing or the upsert failed, which tells a data lake
// ListDataLakes did not list apart from one it could not be asked about.
func scanSLDataLakes(ctx context.Context, client securityLakeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, map[string]bool, error) {
	out, err := client.ListDataLakes(ctx, &securitylake.ListDataLakesInput{Regions: []string{region}})
	if err != nil {
		if isSecurityLakeNotEnabled(err) {
			return 0, 0, nil, markServiceDisabled(err)
		}
		if isAccessDenied(err) {
			return 0, 0, nil, skipIfAccessDenied(st, slDataLakesOp, acct.ID, region, err)
		}
		return 0, 0, nil, fmt.Errorf("%s: %w", slDataLakesOp, err)
	}
	stored := map[string]bool{}
	var batch []*store.Resource
	for _, d := range out.DataLakes {
		arn := sv(d.DataLakeArn)
		if arn == "" {
			continue
		}
		status := string(d.CreateStatus)
		label := arn
		batch = append(batch, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeSecurityLakeDataLake, NativeID: arn,
			Name: &label, Region: &region, Status: &status,
			AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
		})
		stored[arn] = true
	}
	total, inserted, err := upsertBatch(st, batch, "securitylake data-lakes")
	if err != nil {
		return total, inserted, nil, err
	}
	return total, inserted, stored, nil
}

func scanSLSubscribers(ctx context.Context, client securityLakeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := securitylake.NewListSubscribersPaginator(client, &securitylake.ListSubscribersInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isSecurityLakeNotEnabled(err) {
				return 0, 0, markServiceDisabled(err)
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, slSubscribersOp, acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("%s: %w", slSubscribersOp, err)
		}
		for _, s := range out.Subscribers {
			arn := sv(s.SubscriberArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSecurityLakeSubscriber, NativeID: arn,
				Name: s.SubscriberName, Region: &region,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "securitylake subscribers")
}

// slDataLakeSourceNativeID builds a data lake source's id from the parts
// GetDataLakeSources returns; the API issues no ARN per source. The source
// account is part of the id because a delegated administrator's data lake can
// list the sources of the member accounts it collects from. The source name
// goes last: the model gives it no pattern that excludes "/".
func slDataLakeSourceNativeID(dataLakeARN, sourceAccount, sourceName string) string {
	return dataLakeARN + "/source/" + sourceAccount + "/" + sourceName
}

// slDataLakeSourceAttrs is a data lake source row's attributes: the
// GetDataLakeSources element, embedded so its fields stay top-level, plus the
// ListLogSources entries for the same (account, source name). Those carry what
// the element lacks: the source version, and a custom source's provider role,
// location and Glue ARNs. LogSources is a list because nothing in either
// model rules out one source enabled at several versions. It is null when
// ListLogSources failed this scan, and empty when it listed no entry for the
// source.
type slDataLakeSourceAttrs struct {
	sltypes.DataLakeSource
	LogSources []slLogSource
}

// slLogSource is one ListLogSources union entry; exactly one field is set.
type slLogSource struct {
	AwsLogSource    *sltypes.AwsLogSourceResource    `json:",omitempty"`
	CustomLogSource *sltypes.CustomLogSourceResource `json:",omitempty"`
}

type slSourceKey struct{ account, name string }

// slListedSource is a GetDataLakeSources element with its page's data lake ARN.
type slListedSource struct {
	lakeARN string
	src     sltypes.DataLakeSource
}

// scanSLDataLakeSources stores every source GetDataLakeSources reports for the
// region, AWS-native and custom alike: one row per (source account, source
// name), carrying its per-resource collection statuses verbatim and the
// ListLogSources entries for it. A row is linked under its data lake when
// scanSLDataLakes stored that data lake. It returns the GetDataLakeSources
// and ListLogSources errors apart, so the caller can demote each on its own.
//
// An entry missing an id part, or repeating an id already listed, is reported
// instead of stored. The element carries no source version, so nothing in it
// can tell two such entries apart, and a second upsert of one resource in a
// scan is a version split. A ListLogSources entry that matches no stored row
// is reported too: by name after a complete GetDataLakeSources listing, and
// counted under GetDataLakeSources after one cut short.
func scanSLDataLakeSources(ctx context.Context, client securityLakeAPI, acct *account, region string, st *store.Store, scanID string, storedLakes map[string]bool) (total, inserted int, err, logErr error) {
	listed, complete, err := listSLDataLakeSources(ctx, client, acct, region, st)
	logSources, logErr := listSLLogSources(ctx, client, acct, region, st)

	var (
		children      []containedChild
		unstored      []string
		unlinkedLakes []string
	)
	seen := map[string]bool{}
	matched := map[slSourceKey]bool{}
	for _, l := range listed {
		sourceAccount, name := sv(l.src.Account), sv(l.src.SourceName)
		id := slDataLakeSourceNativeID(l.lakeARN, sourceAccount, name)
		if l.lakeARN == "" || sourceAccount == "" || name == "" {
			unstored = append(unstored, fmt.Sprintf("missing id part (data lake %q, account %q, source %q)", l.lakeARN, sourceAccount, name))
			continue
		}
		if seen[id] {
			unstored = append(unstored, "repeated "+id)
			continue
		}
		seen[id] = true
		attrs := slDataLakeSourceAttrs{DataLakeSource: l.src}
		if logSources != nil {
			key := slSourceKey{sourceAccount, name}
			attrs.LogSources = sortedSLLogSources(logSources.bySource[key])
			matched[key] = true
		}
		parentID := ""
		switch {
		case storedLakes[l.lakeARN]:
			parentID = store.ResourceID("aws", acct.ID, l.lakeARN)
		case storedLakes != nil && !slices.Contains(unlinkedLakes, l.lakeARN):
			unlinkedLakes = append(unlinkedLakes, l.lakeARN)
		}
		children = append(children, containedChild{parentID: parentID, row: &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeSecurityLakeDataLakeSource, NativeID: id,
			Name: &name, Region: &region, AttributesJSON: mustJSON(attrs), DiscoveredBy: scanID,
		}})
	}
	if len(unstored) > 0 {
		st.ReportWarning(store.ScanWarning{
			Provider: "aws", Service: slDataLakeSourcesOp, Scope: acct.ID + "/" + region,
			Message: fmt.Sprintf("%d data lake sources not stored because disco cannot identify them: %s; please report this response shape",
				len(unstored), strings.Join(unstored, "; ")),
		})
	}
	if len(unlinkedLakes) > 0 {
		st.ReportWarning(store.ScanWarning{
			Provider: "aws", Service: slDataLakeSourcesOp, Scope: acct.ID + "/" + region,
			Message: fmt.Sprintf("data lake sources stored without a link to their data lake: ListDataLakes did not list %s, which GetDataLakeSources named",
				strings.Join(unlinkedLakes, ", ")),
		})
	}
	if logSources != nil {
		logSources.reportUnmatched(st, acct, region, matched, complete)
	}
	total, inserted, err = upsertRegionChildren(st, acct, region, slDataLakeSourcesOp, children, err)
	return total, inserted, err, logErr
}

// sortedSLLogSources copies a row's entries in a fixed order, AWS-native
// before custom, then by version, then by name, with the whole entry as the
// last tie-break. ListLogSources gives no order, and a reordered answer must
// not version the row.
func sortedSLLogSources(entries []slLogSource) []slLogSource {
	out := append([]slLogSource{}, entries...)
	slices.SortFunc(out, func(a, b slLogSource) int {
		aKind, aVersion, aName := a.sortKey()
		bKind, bVersion, bName := b.sortKey()
		return cmp.Or(cmp.Compare(aKind, bKind), strings.Compare(aVersion, bVersion),
			strings.Compare(aName, bName), strings.Compare(mustJSON(a), mustJSON(b)))
	})
	return out
}

func (e slLogSource) sortKey() (kind int, version, name string) {
	if e.CustomLogSource != nil {
		return 1, sv(e.CustomLogSource.SourceVersion), sv(e.CustomLogSource.SourceName)
	}
	return 0, sv(e.AwsLogSource.SourceVersion), string(e.AwsLogSource.SourceName)
}

// listSLDataLakeSources lists GetDataLakeSources for the region. complete is
// false when an error ended the listing early; err is that error after
// slDataLakeSourcesErr: nil for a no-data-lake answer or a warned conflict,
// otherwise the error itself, an IAM denial included, for the caller to handle.
func listSLDataLakeSources(ctx context.Context, client securityLakeAPI, acct *account, region string, st *store.Store) (listed []slListedSource, complete bool, err error) {
	pager := securitylake.NewGetDataLakeSourcesPaginator(client, &securitylake.GetDataLakeSourcesInput{},
		func(o *securitylake.GetDataLakeSourcesPaginatorOptions) {
			o.Limit = slSourcesPageLimit
			o.StopOnDuplicateToken = true
		})
	for read := false; pager.HasMorePages(); read = true {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			// A no-data-lake answer to the first page means the region has no
			// sources: the listing is complete, so log sources left over are
			// reported as unmatched instead of dropped unseen.
			if !read && isSLNoDataLake(perr) {
				return nil, true, nil
			}
			return listed, false, slDataLakeSourcesErr(st, acct, region, perr)
		}
		for _, s := range out.DataLakeSources {
			listed = append(listed, slListedSource{lakeARN: sv(out.DataLakeArn), src: s})
		}
	}
	return listed, true, nil
}

// slLogSourceIndex holds the region's ListLogSources entries by (account,
// source name), and a description of each entry it could not file: an
// unknown union member, or an element for another Region.
type slLogSourceIndex struct {
	bySource map[slSourceKey][]slLogSource
	unfiled  []string
}

// add files one ListLogSources element's entries under its account. The model
// leaves the account optional; an entry without one is taken as the scanning
// account's own instead of being dropped. An element for another Region is not filed: its sources are
// not this region's data lake sources.
func (x *slLogSourceIndex) add(ls sltypes.LogSource, scanAccount, region string) {
	account := cmp.Or(sv(ls.Account), scanAccount)
	if r := sv(ls.Region); r != "" && r != region {
		x.unfiled = append(x.unfiled, fmt.Sprintf("account %q: %d entries for Region %q", account, len(ls.Sources), r))
		return
	}
	for _, r := range ls.Sources {
		switch v := r.(type) {
		case *sltypes.LogSourceResourceMemberAwsLogSource:
			key := slSourceKey{account, string(v.Value.SourceName)}
			x.bySource[key] = append(x.bySource[key], slLogSource{AwsLogSource: &v.Value})
		case *sltypes.LogSourceResourceMemberCustomLogSource:
			key := slSourceKey{account, sv(v.Value.SourceName)}
			x.bySource[key] = append(x.bySource[key], slLogSource{CustomLogSource: &v.Value})
		default:
			x.unfiled = append(x.unfiled, fmt.Sprintf("account %q: unknown entry %T", account, r))
		}
	}
}

// listSLLogSources indexes the region's ListLogSources entries and warns about
// the entries it could not file. A nil index means the listing failed:
// entries from pages read before the failure are dropped too, so no row
// carries a partial list. A denial or conflict is warned, a modeled not-found
// or a not-enabled answer is silent (the data lake source rows then carry no
// LogSources), and any other error is returned.
func listSLLogSources(ctx context.Context, client securityLakeAPI, acct *account, region string, st *store.Store) (*slLogSourceIndex, error) {
	pager := securitylake.NewListLogSourcesPaginator(client, &securitylake.ListLogSourcesInput{Regions: []string{region}},
		func(o *securitylake.ListLogSourcesPaginatorOptions) {
			o.Limit = slSourcesPageLimit
			o.StopOnDuplicateToken = true
		})
	idx := &slLogSourceIndex{bySource: map[slSourceKey][]slLogSource{}}
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			return nil, slLogSourcesErr(st, acct, region, err)
		}
		for _, ls := range out.Sources {
			idx.add(ls, acct.ID, region)
		}
	}
	if len(idx.unfiled) > 0 {
		st.ReportWarning(store.ScanWarning{
			Provider: "aws", Service: slLogSourcesOp, Scope: acct.ID + "/" + region,
			Message: fmt.Sprintf("ListLogSources for Region %s returned entries disco cannot file under a data lake source, which are not stored: %s; please report this response shape",
				region, strings.Join(idx.unfiled, "; ")),
		})
	}
	return idx, nil
}

// reportUnmatched warns once about the entries no stored data lake source row
// carries. After a complete GetDataLakeSources listing the warning names them
// under ListLogSources: the two ops are expected to list the same sources,
// which is unverified against a live data lake, so an entry here is reported
// instead of assumed impossible. After a cut-short listing the sources it did
// not reach are unknown, so the warning goes under GetDataLakeSources and
// counts the entries left unmatched.
func (x *slLogSourceIndex) reportUnmatched(st *store.Store, acct *account, region string, matched map[slSourceKey]bool, complete bool) {
	var unmatched []string
	entries := 0
	for key, es := range x.bySource {
		if !matched[key] {
			unmatched = append(unmatched, fmt.Sprintf("account %q source %q (%d entries)", key.account, key.name, len(es)))
			entries += len(es)
		}
	}
	if len(unmatched) == 0 {
		return
	}
	slices.Sort(unmatched)
	w := store.ScanWarning{
		Provider: "aws", Service: slLogSourcesOp, Scope: acct.ID + "/" + region,
		Message: fmt.Sprintf("%d log sources listed by ListLogSources match no data lake source from GetDataLakeSources and are not stored: %s; please report this response shape",
			len(unmatched), strings.Join(unmatched, "; ")),
	}
	if !complete {
		w.Service = slDataLakeSourcesOp
		w.Message = fmt.Sprintf("the GetDataLakeSources listing was cut short; %d ListLogSources entries for %d sources match no stored data lake source and are not stored: %s",
			entries, len(unmatched), strings.Join(unmatched, "; "))
	}
	st.ReportWarning(w)
}

// slDataLakeSourcesErr drops the GetDataLakeSources errors that leave the rows
// listed before them valid. ResourceNotFoundException is modeled for the op;
// what it answers here is unobserved, but a missing data lake has nothing to
// list, and a data lake deleted mid-scan is recorded as removed by a later
// scan. The not-enabled shapes say the account has no data lake.
// ConflictException is also modeled but has not been observed; the data lake
// may still exist, so its sources missing from this scan are reported.
func slDataLakeSourcesErr(st *store.Store, acct *account, region string, err error) error {
	return slSourcesListErr(st, acct, region, slDataLakeSourcesOp, "data lake sources from this page on not listed this scan", err)
}

// slLogSourcesErr applies the same policy to ListLogSources, whose errors are
// modeled alike. A denial is warned here, because the caller stores the data
// lake source rows without the entries instead of failing; any other error is
// returned wrapped with the op.
func slLogSourcesErr(st *store.Store, acct *account, region string, err error) error {
	if isAccessDenied(err) && !isSecurityLakeNotEnabled(err) {
		return skipIfAccessDenied(st, slLogSourcesOp, acct.ID, region, err)
	}
	if err = slSourcesListErr(st, acct, region, slLogSourcesOp, "data lake sources stored without their log source entries", err); err != nil {
		return fmt.Errorf("%s: %w", slLogSourcesOp, err)
	}
	return nil
}

// isSLNoDataLake reports the answers that say the region has no data lake to
// list sources of: the modeled ResourceNotFoundException and the not-enabled
// shapes.
func isSLNoDataLake(err error) bool {
	return isSecurityLakeNotEnabled(err) || isAPIErrorCode(err, "ResourceNotFoundException")
}

// slSourcesListErr is the policy both source listings share: a not-found or
// not-enabled answer is dropped silently, a conflict is warned with its
// effect, and any other error is returned unchanged.
func slSourcesListErr(st *store.Store, acct *account, region, op, conflictEffect string, err error) error {
	switch {
	case isSLNoDataLake(err):
		return nil
	case isAPIErrorCode(err, "ConflictException"):
		st.ReportWarning(store.ScanWarning{
			Provider: "aws", Service: op, Scope: acct.ID + "/" + region,
			Message: "conflict: " + conflictEffect + ": " + err.Error(),
		})
		return nil
	}
	return err
}
