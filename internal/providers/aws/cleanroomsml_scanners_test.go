package aws

import (
	"context"
	"errors"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cleanrooms"
	crtypes "github.com/aws/aws-sdk-go-v2/service/cleanrooms/types"
	"github.com/aws/aws-sdk-go-v2/service/cleanroomsml"
	mltypes "github.com/aws/aws-sdk-go-v2/service/cleanroomsml/types"
	"github.com/icearp/disco-cli/store"
)

// The fixture: every account-wide listing serves its "t" item on the first
// page and its "b" item on the second; the second page of audience models also
// carries am-x-t, which has neither a name nor a status. The memberships m-t
// and m-b each hold one item of every per-membership kind.
// ListTagsForResource answers with tags for every ARN ending in "-t".

var crmlCreated = time.Date(2026, 9, 2, 9, 0, 0, 0, time.UTC)

func crmlARN(res string) string {
	return arnFor("aws", "cleanrooms-ml", testRegion, testAccountID, res)
}

// crmlChild is the ARN of membership m's item of kind, "<kind>-<m's suffix>".
func crmlChild(m, kind string) string {
	return crmlARN("membership/" + m + "/" + kind + "/" + kind + strings.TrimPrefix(m, "m"))
}

func crmlAlgorithm(n string) mltypes.ConfiguredModelAlgorithmSummary {
	return mltypes.ConfiguredModelAlgorithmSummary{
		ConfiguredModelAlgorithmArn: sdkaws.String(crmlARN("configured-model-algorithm/cma-" + n)),
		Name:                        sdkaws.String("algorithm " + n), CreateTime: &crmlCreated,
	}
}

func crmlDataset(n string) mltypes.TrainingDatasetSummary {
	return mltypes.TrainingDatasetSummary{
		TrainingDatasetArn: sdkaws.String(crmlARN("training-dataset/td-" + n)),
		Name:               sdkaws.String("dataset " + n), Status: mltypes.TrainingDatasetStatusActive, CreateTime: &crmlCreated,
	}
}

func crmlAudienceModel(n string) mltypes.AudienceModelSummary {
	return mltypes.AudienceModelSummary{
		AudienceModelArn: sdkaws.String(crmlARN("audience-model/am-" + n)),
		Name:             sdkaws.String("audience model " + n), Status: mltypes.AudienceModelStatusActive, CreateTime: &crmlCreated,
	}
}

// crmlBareAudience has neither a name nor a status.
func crmlBareAudience() mltypes.AudienceModelSummary {
	return mltypes.AudienceModelSummary{AudienceModelArn: sdkaws.String(crmlARN("audience-model/am-x-t")), CreateTime: &crmlCreated}
}

func crmlConfiguredAudience(n string) mltypes.ConfiguredAudienceModelSummary {
	return mltypes.ConfiguredAudienceModelSummary{
		ConfiguredAudienceModelArn: sdkaws.String(crmlARN("configured-audience-model/cam-" + n)),
		Name:                       sdkaws.String("configured audience " + n), Status: mltypes.ConfiguredAudienceModelStatusActive, CreateTime: &crmlCreated,
	}
}

func crmlAssociation(m string) mltypes.ConfiguredModelAlgorithmAssociationSummary {
	return mltypes.ConfiguredModelAlgorithmAssociationSummary{
		ConfiguredModelAlgorithmAssociationArn: sdkaws.String(crmlChild(m, "configured-model-algorithm-association")),
		MembershipIdentifier:                   sdkaws.String(m), Name: sdkaws.String("association of " + m), CreateTime: &crmlCreated,
	}
}

func crmlChannel(m string) mltypes.MLInputChannelSummary {
	return mltypes.MLInputChannelSummary{
		MlInputChannelArn:    sdkaws.String(crmlChild(m, "ml-input-channel")),
		MembershipIdentifier: sdkaws.String(m), Name: sdkaws.String("channel of " + m), Status: mltypes.MLInputChannelStatusActive, CreateTime: &crmlCreated,
	}
}

func crmlTrainedModel(m string) mltypes.TrainedModelSummary {
	return mltypes.TrainedModelSummary{
		TrainedModelArn:      sdkaws.String(crmlChild(m, "trained-model")),
		MembershipIdentifier: sdkaws.String(m), Name: sdkaws.String("model of " + m), Status: mltypes.TrainedModelStatusActive, CreateTime: &crmlCreated,
	}
}

// crmlWant is a row the fixture stores: its Name, its Status (nil for none)
// and the element its attributes are.
type crmlWant struct {
	typ, arn, name string
	status         *string
	element        any
}

// crmlRows is every row the fixture stores.
func crmlRows() []crmlWant {
	active := sdkaws.String("ACTIVE")
	bare := crmlBareAudience()
	rows := []crmlWant{{TypeCleanRoomsMLAudienceModel, sv(bare.AudienceModelArn), "", sdkaws.String(""), bare}}
	for _, n := range []string{"t", "b"} {
		m := "m-" + n
		a, d, am, cam := crmlAlgorithm(n), crmlDataset(n), crmlAudienceModel(n), crmlConfiguredAudience(n)
		as, ch, tm := crmlAssociation(m), crmlChannel(m), crmlTrainedModel(m)
		rows = append(rows,
			crmlWant{TypeCleanRoomsMLConfiguredModelAlgorithm, sv(a.ConfiguredModelAlgorithmArn), sv(a.Name), nil, a},
			crmlWant{TypeCleanRoomsMLTrainingDataset, sv(d.TrainingDatasetArn), sv(d.Name), active, d},
			crmlWant{TypeCleanRoomsMLAudienceModel, sv(am.AudienceModelArn), sv(am.Name), active, am},
			crmlWant{TypeCleanRoomsMLConfiguredAudienceModel, sv(cam.ConfiguredAudienceModelArn), sv(cam.Name), active, cam},
			crmlWant{TypeCleanRoomsMLConfiguredModelAlgorithmAssociation, sv(as.ConfiguredModelAlgorithmAssociationArn), sv(as.Name), nil, as},
			crmlWant{TypeCleanRoomsMLMLInputChannel, sv(ch.MlInputChannelArn), sv(ch.Name), active, ch},
			crmlWant{TypeCleanRoomsMLTrainedModel, sv(tm.TrainedModelArn), sv(tm.Name), active, tm},
		)
	}
	return rows
}

// crmlCalls records the stubbed calls and answers ListTagsForResource from
// tags and errs, keyed by ARN.
type crmlCalls struct{ *crtCalls }

func newCRMLCalls() *crmlCalls {
	c := &crmlCalls{&crtCalls{newBACCalls()}}
	for _, r := range crmlRows() {
		if strings.HasSuffix(r.arn, "-t") {
			c.tags[r.arn] = map[string]string{"type": r.typ}
		}
	}
	return c
}

func (c *crmlCalls) listTags(in any) (any, error) {
	arn := sv(in.(*cleanroomsml.ListTagsForResourceInput).ResourceArn)
	c.mu.Lock()
	c.tagARNs = append(c.tagARNs, arn)
	err, tags := c.errs[arn], c.tags[arn]
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &cleanroomsml.ListTagsForResourceOutput{Tags: tags}, nil
}

// crmlMembershipOf is the membership a per-membership listing was sent.
func crmlMembershipOf(in any) string { return sv(bacField[*string](in, "MembershipIdentifier")) }

// crmlHandlers answers every op the scan calls with the fixture (crmlRows).
func crmlHandlers(c *crmlCalls) map[string]func(any) (any, error) {
	return map[string]func(any) (any, error){
		"ListTagsForResource": c.listTags,
		"ListConfiguredModelAlgorithms": c.twoPages("ListConfiguredModelAlgorithms", func(n string, next *string) any {
			return &cleanroomsml.ListConfiguredModelAlgorithmsOutput{ConfiguredModelAlgorithms: []mltypes.ConfiguredModelAlgorithmSummary{crmlAlgorithm(n)}, NextToken: next}
		}),
		"ListTrainingDatasets": c.twoPages("ListTrainingDatasets", func(n string, next *string) any {
			return &cleanroomsml.ListTrainingDatasetsOutput{TrainingDatasets: []mltypes.TrainingDatasetSummary{crmlDataset(n)}, NextToken: next}
		}),
		"ListAudienceModels": c.twoPages("ListAudienceModels", func(n string, next *string) any {
			items := []mltypes.AudienceModelSummary{crmlAudienceModel(n)}
			if n == "b" {
				items = append(items, crmlBareAudience())
			}
			return &cleanroomsml.ListAudienceModelsOutput{AudienceModels: items, NextToken: next}
		}),
		"ListConfiguredAudienceModels": c.twoPages("ListConfiguredAudienceModels", func(n string, next *string) any {
			return &cleanroomsml.ListConfiguredAudienceModelsOutput{ConfiguredAudienceModels: []mltypes.ConfiguredAudienceModelSummary{crmlConfiguredAudience(n)}, NextToken: next}
		}),
		"ListMemberships": c.twoPages("ListMemberships", func(n string, next *string) any {
			return &cleanrooms.ListMembershipsOutput{MembershipSummaries: []crtypes.MembershipSummary{
				{Arn: sdkaws.String(crtARN("membership/m-" + n)), Id: sdkaws.String("m-" + n)},
			}, NextToken: next}
		}),
		"ListConfiguredModelAlgorithmAssociations": c.answer("ListConfiguredModelAlgorithmAssociations", func(in any) any {
			return &cleanroomsml.ListConfiguredModelAlgorithmAssociationsOutput{ConfiguredModelAlgorithmAssociations: []mltypes.ConfiguredModelAlgorithmAssociationSummary{crmlAssociation(crmlMembershipOf(in))}}
		}),
		"ListMLInputChannels": c.answer("ListMLInputChannels", func(in any) any {
			return &cleanroomsml.ListMLInputChannelsOutput{MlInputChannelsList: []mltypes.MLInputChannelSummary{crmlChannel(crmlMembershipOf(in))}}
		}),
		"ListTrainedModels": c.answer("ListTrainedModels", func(in any) any {
			return &cleanroomsml.ListTrainedModelsOutput{TrainedModels: []mltypes.TrainedModelSummary{crmlTrainedModel(crmlMembershipOf(in))}}
		}),
	}
}

// crmlEmptyHandlers is crmlHandlers with every listing empty, but for
// ListMemberships, which returns memberships on one page.
func crmlEmptyHandlers(c *crmlCalls, memberships ...string) map[string]func(any) (any, error) {
	h := crmlHandlers(c)
	empty := map[string]any{
		"ListConfiguredModelAlgorithms":            &cleanroomsml.ListConfiguredModelAlgorithmsOutput{},
		"ListTrainingDatasets":                     &cleanroomsml.ListTrainingDatasetsOutput{},
		"ListAudienceModels":                       &cleanroomsml.ListAudienceModelsOutput{},
		"ListConfiguredAudienceModels":             &cleanroomsml.ListConfiguredAudienceModelsOutput{},
		"ListConfiguredModelAlgorithmAssociations": &cleanroomsml.ListConfiguredModelAlgorithmAssociationsOutput{},
		"ListMLInputChannels":                      &cleanroomsml.ListMLInputChannelsOutput{},
		"ListTrainedModels":                        &cleanroomsml.ListTrainedModelsOutput{},
	}
	for op, out := range empty {
		h[op] = c.answer(op, func(any) any { return out })
	}
	var items []crtypes.MembershipSummary
	for _, m := range memberships {
		items = append(items, crtypes.MembershipSummary{Arn: sdkaws.String(crtARN("membership/" + m)), Id: sdkaws.String(m)})
	}
	h["ListMemberships"] = c.answer("ListMemberships", func(any) any { return &cleanrooms.ListMembershipsOutput{MembershipSummaries: items} })
	return h
}

// crmlListOps is every listing the scan makes, in call order.
var crmlListOps = []string{
	"ListConfiguredModelAlgorithms", "ListTrainingDatasets", "ListAudienceModels", "ListConfiguredAudienceModels",
	"ListMemberships", "ListConfiguredModelAlgorithmAssociations", "ListMLInputChannels", "ListTrainedModels",
}

// crmlOpLabel is the label op's warnings and errors go under.
func crmlOpLabel(op string) string {
	if op == "ListMemberships" {
		return crmlMembershipsOp
	}
	return "cleanrooms-ml:" + op
}

// crmlScan runs the scan's real entry point against handlers; every op they
// do not answer is denied.
func crmlScan(ctx context.Context, t *testing.T, st *store.Store, handlers map[string]func(any) (any, error)) (int, int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, crDenied)
	return scanCleanRoomsML(ctx, acct, testRegion, st, testScanID)
}

func crmlRowOf(t *testing.T, st *store.Store, typ, arn string) (store.Resource, bool) {
	t.Helper()
	r, ok := rowsByNativeID(t, st, typ)[arn]
	return r, ok
}

// Every type stores the tags ListTagsForResource returns for its ARN, nil for
// a resource without tags, and every stored row's ARN is sent to the tag op.
// Both pages of every account-wide listing and both memberships are read,
// every listing asks for the model's page cap of 100, and the scan reports no
// error or warning. Each row keeps the base scan's Name, Status and verbatim
// element, and its CreatedAt is the element's CreateTime.
func TestScanCleanRoomsML_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newCRMLCalls()

	total, inserted, err := crmlScan(context.Background(), t, st, crmlHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	rows := crmlRows()
	if total != 15 || inserted != 15 || len(rows) != 15 {
		t.Errorf("total, inserted = %d, %d; want 15 (fixture rows %d)", total, inserted, len(rows))
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	var wantTagARNs []string
	wantCreated := sv(tp(&crmlCreated))
	for _, want := range rows {
		r, ok := crmlRowOf(t, st, want.typ, want.arn)
		if !ok {
			t.Errorf("%s %s not stored", want.typ, want.arn)
			continue
		}
		wantTagARNs = append(wantTagARNs, want.arn)
		if strings.HasSuffix(want.arn, "-t") {
			assertTagsMap(t, r.TagsJSON, map[string]string{"type": want.typ})
		} else if r.TagsJSON != nil {
			t.Errorf("%s %s TagsJSON = %s; want nil", want.typ, want.arn, *r.TagsJSON)
		}
		if sv(r.Name) != want.name || (r.Name == nil) != (want.name == "") {
			t.Errorf("%s %s Name = %v; want %q", want.typ, want.arn, r.Name, want.name)
		}
		if (r.Status == nil) != (want.status == nil) || sv(r.Status) != sv(want.status) {
			t.Errorf("%s %s Status = %v; want %v", want.typ, want.arn, r.Status, want.status)
		}
		if sv(r.CreatedAt) != wantCreated {
			t.Errorf("%s %s CreatedAt = %v; want %s", want.typ, want.arn, r.CreatedAt, wantCreated)
		}
		if r.Region == nil || *r.Region != testRegion || r.AttributesJSON != mustJSON(want.element) {
			t.Errorf("%s %s Region, attributes = %v, %s; want %s, %s", want.typ, want.arn, r.Region, r.AttributesJSON, testRegion, mustJSON(want.element))
		}
	}
	got := slices.Clone(calls.tagARNs)
	slices.Sort(got)
	slices.Sort(wantTagARNs)
	if !slices.Equal(got, wantTagARNs) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", got, wantTagARNs)
	}
	for _, op := range crmlListOps {
		caps := calls.maxResults[op]
		if len(caps) != 2 {
			t.Errorf("%s calls with MaxResults = %d; want 2", op, len(caps))
		}
		for _, n := range caps {
			if n != 100 {
				t.Errorf("%s MaxResults = %d; want 100", op, n)
			}
		}
	}
}

// An aws-us-gov account scanned in us-gov-west-1 sends the gov ARNs its
// listings return to the tag op unchanged and stores their tags.
func TestScanCleanRoomsML_GovCloudARNsReachTagOp(t *testing.T) {
	const govRegion = "us-gov-west-1"
	dataset := arnFor("aws-us-gov", "cleanrooms-ml", govRegion, testAccountID, "training-dataset/td-gov")
	model := arnFor("aws-us-gov", "cleanrooms-ml", govRegion, testAccountID, "membership/m-gov/trained-model/tm-gov")
	st := newTestStore(t)
	calls := newCRMLCalls()
	calls.tags[dataset] = map[string]string{"env": "gov"}
	calls.tags[model] = map[string]string{"env": "gov-model"}
	h := crmlEmptyHandlers(calls)
	h["ListTrainingDatasets"] = calls.answer("ListTrainingDatasets", func(any) any {
		return &cleanroomsml.ListTrainingDatasetsOutput{TrainingDatasets: []mltypes.TrainingDatasetSummary{{TrainingDatasetArn: &dataset, Name: sdkaws.String("gov dataset")}}}
	})
	h["ListMemberships"] = calls.answer("ListMemberships", func(any) any {
		return &cleanrooms.ListMembershipsOutput{MembershipSummaries: []crtypes.MembershipSummary{
			{Arn: sdkaws.String(arnFor("aws-us-gov", "cleanrooms", govRegion, testAccountID, "membership/m-gov")), Id: sdkaws.String("m-gov")},
		}}
	})
	h["ListTrainedModels"] = calls.answer("ListTrainedModels", func(any) any {
		return &cleanroomsml.ListTrainedModelsOutput{TrainedModels: []mltypes.TrainedModelSummary{{TrainedModelArn: &model, Name: sdkaws.String("gov model")}}}
	})
	acct := partitionAccount("aws-us-gov")
	acct.cfg = opStubConfig(h, crDenied)

	total, _, err := scanCleanRoomsML(context.Background(), acct, govRegion, st, testScanID)
	if err != nil || total != 2 {
		t.Fatalf("scan = %d, %v; want 2, nil", total, err)
	}
	got := slices.Clone(calls.tagARNs)
	slices.Sort(got)
	if want := []string{model, dataset}; !slices.Equal(got, want) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", got, want)
	}
	for _, c := range []struct {
		typ, arn, env string
	}{{TypeCleanRoomsMLTrainingDataset, dataset, "gov"}, {TypeCleanRoomsMLTrainedModel, model, "gov-model"}} {
		r, ok := crmlRowOf(t, st, c.typ, c.arn)
		if !ok {
			t.Fatalf("%s %s not stored", c.typ, c.arn)
		}
		assertTagsMap(t, r.TagsJSON, map[string]string{"env": c.env})
		if sv(r.Region) != govRegion {
			t.Errorf("%s Region = %v; want %s", c.arn, r.Region, govRegion)
		}
	}
}

// Every listing returning nothing stores nothing, reads no tags and reports
// nothing.
func TestScanCleanRoomsML_EmptyListings(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newCRMLCalls()

	total, inserted, err := crmlScan(context.Background(), t, st, crmlEmptyHandlers(calls))
	if err != nil || total != 0 || inserted != 0 {
		t.Fatalf("scan = %d, %d, %v; want 0, 0, nil", total, inserted, err)
	}
	if *warns != 0 || len(calls.tagARNs) != 0 || calls.count("ListTrainedModels") != 0 {
		t.Errorf("warnings, tag reads, ListTrainedModels calls = %d, %d, %d; want 0, 0, 0", *warns, len(calls.tagARNs), calls.count("ListTrainedModels"))
	}
}

// A membership whose ML listings return an empty page stores nothing for it,
// reads no tags and reports nothing.
func TestScanCleanRoomsML_EmptyMembershipListings(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newCRMLCalls()

	total, _, err := crmlScan(context.Background(), t, st, crmlEmptyHandlers(calls, "m-t"))
	if err != nil || total != 0 {
		t.Fatalf("scan = %d, %v; want 0, nil", total, err)
	}
	for _, op := range []string{"ListConfiguredModelAlgorithmAssociations", "ListMLInputChannels", "ListTrainedModels"} {
		if n := calls.count(op); n != 1 {
			t.Errorf("%s calls = %d; want 1, for m-t", op, n)
		}
	}
	if *warns != 0 || len(calls.tagARNs) != 0 {
		t.Errorf("warnings, tag reads = %d, %d; want 0, 0", *warns, len(calls.tagARNs))
	}
}

// A rescan stores the same rows without inserting any.
func TestScanCleanRoomsML_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	calls := newCRMLCalls()
	if _, _, err := crmlScan(context.Background(), t, st, crmlHandlers(calls)); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := crmlScan(context.Background(), t, st, crmlHandlers(calls))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != 15 || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want 15, 0", total, inserted)
	}
}

// A failed tag read keeps the row without tags unless the resource is gone:
// a denial warns once under cleanrooms-ml:ListTagsForResource, a throttle
// after retries is a transient error, any other code a hard one; a
// not-found drops the row. The same holds for an account-wide and a
// per-membership listing.
func TestScanCleanRoomsML_TagReadFailures(t *testing.T) {
	for _, target := range []struct{ typ, arn string }{
		{TypeCleanRoomsMLTrainingDataset, crmlARN("training-dataset/td-t")},
		{TypeCleanRoomsMLConfiguredModelAlgorithmAssociation, crmlChild("m-t", "configured-model-algorithm-association")},
	} {
		t.Run(target.typ, func(t *testing.T) {
			for _, c := range []struct {
				name      string
				err       error
				kept      bool
				outcome   serviceOutcome
				wantWarns int
			}{
				{"denied", crDenied, true, 0, 1},
				{"not found", crNotFound, false, 0, 0},
				{"other error", crtBadInput, true, outcomeError, 0},
				{"transient", crThrottled, true, outcomeTransient, 0},
			} {
				t.Run(c.name, func(t *testing.T) {
					st := newTestStore(t)
					warnings := bacWarnings(st, crmlTagsOp)
					calls := newCRMLCalls()
					calls.errs[target.arn] = c.err

					total, _, err := crmlScan(context.Background(), t, st, crmlHandlers(calls))
					if c.outcome == 0 && err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
					if c.outcome != 0 {
						assertGlueOutcome(t, err, c.outcome)
					}
					if c.outcome == outcomeError {
						assertAPICode(t, err, "ValidationException")
					}
					r, ok := crmlRowOf(t, st, target.typ, target.arn)
					if ok != c.kept {
						t.Fatalf("row stored = %v; want %v", ok, c.kept)
					}
					if ok && r.TagsJSON != nil {
						t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
					}
					want := 15
					if !c.kept {
						want = 14
					}
					if total != want {
						t.Errorf("total = %d; want %d", total, want)
					}
					if len(*warnings) != c.wantWarns {
						t.Fatalf("%s warnings = %v; want %d", crmlTagsOp, *warnings, c.wantWarns)
					}
					for _, w := range *warnings {
						if w.Scope != testAccountID+"/"+testRegion {
							t.Errorf("warning Scope = %q; want %s/%s", w.Scope, testAccountID, testRegion)
						}
					}
				})
			}
		})
	}
}

// Denied tag reads in several listings are one warning for the scan.
func TestScanCleanRoomsML_TagDenialsWarnOnce(t *testing.T) {
	st := newTestStore(t)
	warnings := bacWarnings(st, crmlTagsOp)
	calls := newCRMLCalls()
	calls.errs[crmlARN("training-dataset/td-t")] = crDenied
	calls.errs[crmlChild("m-b", "trained-model")] = crDenied

	if _, _, err := crmlScan(context.Background(), t, st, crmlHandlers(calls)); err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if len(*warnings) != 1 || (*warnings)[0].Scope != testAccountID+"/"+testRegion {
		t.Errorf("%s warnings = %v; want one, scoped %s/%s", crmlTagsOp, *warnings, testAccountID, testRegion)
	}
	for _, r := range []struct{ typ, arn string }{
		{TypeCleanRoomsMLTrainingDataset, crmlARN("training-dataset/td-t")},
		{TypeCleanRoomsMLTrainedModel, crmlChild("m-b", "trained-model")},
	} {
		if got, ok := crmlRowOf(t, st, r.typ, r.arn); !ok || got.TagsJSON != nil {
			t.Errorf("%s stored %v, TagsJSON %v; want it kept without tags", r.arn, ok, got.TagsJSON)
		}
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanCleanRoomsML_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, crmlTagsOp)
	calls := newCRMLCalls()
	calls.errs[crmlARN("training-dataset/td-t")] = crThrottled
	calls.errs[crmlARN("audience-model/am-b")] = crtBadInput

	_, _, err := crmlScan(context.Background(), t, st, crmlHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", crmlTagsOp, *warns)
	}
}

// A denied listing warns once under its own op and costs no other phase its
// rows; a denied membership listing leaves the per-membership listings no
// membership to read.
func TestScanCleanRoomsML_DeniedListingWarnsUnderItsOp(t *testing.T) {
	// 15 rows, less the denied listing's: an account-wide listing stores 2
	// (audience models 3), the memberships' children 6, a per-membership
	// listing 2.
	lost := map[string]int{"ListAudienceModels": 3, "ListMemberships": 6}
	for _, op := range crmlListOps {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			opWarns := countOpWarnings(st, crmlOpLabel(op))
			calls := newCRMLCalls()
			h := crmlHandlers(calls)
			h[op] = func(any) (any, error) { return nil, crDenied }

			total, _, err := crmlScan(context.Background(), t, st, h)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if *warns != 1 || *opWarns != 1 {
				t.Errorf("warnings = %d (%s: %d); want 1 under the op", *warns, crmlOpLabel(op), *opWarns)
			}
			want := 15 - 2
			if n, ok := lost[op]; ok {
				want = 15 - n
			}
			if stored := countStoredRows(t, st); total != want || stored != want {
				t.Errorf("total = %d, stored rows = %d; want %d", total, stored, want)
			}
			if op == "ListMemberships" && calls.count("ListTrainedModels") != 0 {
				t.Errorf("ListTrainedModels called %d times with no membership read", calls.count("ListTrainedModels"))
			}
		})
	}
}

// A failed write stops the scan before any later listing and sends no
// warning about the rows it lost.
func TestScanCleanRoomsML_FailedWriteStopsWithoutWarning(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	refuseStoreWrites(t, st, TypeCleanRoomsMLTrainingDataset)
	calls := newCRMLCalls()
	calls.errs[crmlARN("training-dataset/td-t")] = crDenied

	_, _, err := crmlScan(context.Background(), t, st, crmlHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	for _, op := range []string{"ListAudienceModels", "ListMemberships", "ListTrainedModels"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times after the failed write", op, n)
		}
	}
}

// A failed write of a per-membership listing sends no warning about the
// membership it skipped.
func TestScanCleanRoomsML_FailedMembershipWriteSendsNoSkipWarning(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	refuseStoreWrites(t, st, TypeCleanRoomsMLTrainedModel)
	h := crmlHandlers(newCRMLCalls())
	models := h["ListTrainedModels"]
	h["ListTrainedModels"] = func(in any) (any, error) {
		if crmlMembershipOf(in) == "m-t" {
			return nil, crtBadInput
		}
		return models(in)
	}

	_, _, err := crmlScan(context.Background(), t, st, h)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
}

// A listing's failed page keeps the pages read before it, and no listing's
// failure stops the phases after it. A membership a listing refuses with
// ValidationException is skipped with one warning, its sibling's items
// stored; any other code, ResourceNotFoundException included (the
// per-membership listings model none), is returned.
func TestScanCleanRoomsML_ListFailureKeepsPagesAndRunsEveryPhase(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	skips := bacWarnings(st, "cleanrooms-ml:ListTrainedModels")
	calls := newCRMLCalls()
	h := crmlHandlers(calls)
	h["ListTrainingDatasets"] = failOnCall(2, apiErr("ConflictException", "page 2"), h["ListTrainingDatasets"])
	h["ListConfiguredModelAlgorithms"] = func(any) (any, error) { return nil, apiErr("InternalServerException", "down") }
	models := h["ListTrainedModels"]
	h["ListTrainedModels"] = func(in any) (any, error) {
		if crmlMembershipOf(in) == "m-t" {
			return nil, crtBadInput
		}
		return models(in)
	}
	channels := h["ListMLInputChannels"]
	h["ListMLInputChannels"] = func(in any) (any, error) {
		if crmlMembershipOf(in) == "m-t" {
			return nil, crNotFound
		}
		return channels(in)
	}

	_, _, err := crmlScan(context.Background(), t, st, h)
	for _, code := range []string{"ConflictException", "ResourceNotFoundException"} {
		if !joinCarriesAPIErrorCode(err, code) {
			t.Errorf("err = %v; want it to carry %s", err, code)
		}
	}
	if joinCarriesAPIErrorCode(err, "ValidationException") {
		t.Errorf("err = %v; want the refused membership skipped", err)
	}
	// The ListTrainedModels skip, and the demoted InternalServerException.
	if *warns != 2 || len(*skips) != 1 {
		t.Errorf("warnings = %d, ListTrainedModels warnings = %v; want 2, one of them the skip", *warns, *skips)
	}
	for _, want := range []struct{ typ, arn string }{
		{TypeCleanRoomsMLTrainingDataset, crmlARN("training-dataset/td-t")},
		{TypeCleanRoomsMLTrainedModel, crmlChild("m-b", "trained-model")},
		{TypeCleanRoomsMLMLInputChannel, crmlChild("m-b", "ml-input-channel")},
		{TypeCleanRoomsMLConfiguredModelAlgorithmAssociation, crmlChild("m-t", "configured-model-algorithm-association")},
	} {
		if _, ok := crmlRowOf(t, st, want.typ, want.arn); !ok {
			t.Errorf("%s %s missing", want.typ, want.arn)
		}
	}
	for _, gone := range []struct{ typ, arn string }{
		{TypeCleanRoomsMLTrainingDataset, crmlARN("training-dataset/td-b")},
		{TypeCleanRoomsMLTrainedModel, crmlChild("m-t", "trained-model")},
		{TypeCleanRoomsMLMLInputChannel, crmlChild("m-t", "ml-input-channel")},
	} {
		if _, ok := crmlRowOf(t, st, gone.typ, gone.arn); ok {
			t.Errorf("%s %s stored; want it not read", gone.typ, gone.arn)
		}
	}
}

// A membership listing that fails after its first page still has the
// memberships of that page read, and its error is filed under
// crmlMembershipsOp: throttled beside a hard error of another listing, it is
// demoted to one warning under that op.
func TestScanCleanRoomsML_MembershipListFailureKeepsPage(t *testing.T) {
	st := newTestStore(t)
	opWarns := countOpWarnings(st, crmlMembershipsOp)
	h := crmlHandlers(newCRMLCalls())
	h["ListMemberships"] = failOnCall(2, crThrottled, h["ListMemberships"])
	h["ListConfiguredModelAlgorithms"] = func(any) (any, error) { return nil, crtBadInput }

	_, _, err := crmlScan(context.Background(), t, st, h)
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *opWarns != 1 {
		t.Errorf("%s warnings = %d; want 1, the demoted throttle", crmlMembershipsOp, *opWarns)
	}
	if _, ok := crmlRowOf(t, st, TypeCleanRoomsMLTrainedModel, crmlChild("m-t", "trained-model")); !ok {
		t.Error("trained model of m-t, from the page read, missing")
	}
	if _, ok := crmlRowOf(t, st, TypeCleanRoomsMLTrainedModel, crmlChild("m-b", "trained-model")); ok {
		t.Error("trained model of m-b stored; want its membership not read")
	}
}

// Items listed without an ARN, and memberships without an id, are not
// stored or read and are warned about once per listing, under a key that is
// never empty.
func TestScanCleanRoomsML_UnstorableItemsWarn(t *testing.T) {
	for _, c := range []struct {
		op, key string
		out     any
	}{
		{"ListTrainingDatasets", "first (a training dataset with no name)", &cleanroomsml.ListTrainingDatasetsOutput{TrainingDatasets: []mltypes.TrainingDatasetSummary{{}, {}}}},
		{"ListConfiguredModelAlgorithms", "first algorithm x", &cleanroomsml.ListConfiguredModelAlgorithmsOutput{ConfiguredModelAlgorithms: []mltypes.ConfiguredModelAlgorithmSummary{{Name: sdkaws.String("algorithm x")}}}},
		{"ListTrainedModels", "first (a trained model with no name) of membership m-", &cleanroomsml.ListTrainedModelsOutput{TrainedModels: []mltypes.TrainedModelSummary{{}}}},
		{"ListMemberships", "first (a membership with no name)", &cleanrooms.ListMembershipsOutput{MembershipSummaries: []crtypes.MembershipSummary{{}}}},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			warnings := bacWarnings(st, crmlOpLabel(c.op))
			h := crmlHandlers(newCRMLCalls())
			h[c.op] = func(any) (any, error) { return c.out, nil }

			if _, _, err := crmlScan(context.Background(), t, st, h); err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if len(*warnings) != 1 || !strings.Contains((*warnings)[0].Message, c.key) {
				t.Errorf("warnings = %v; want one naming %q", *warnings, c.key)
			}
		})
	}
}

// A repeated token ends the listing as an error, keeping its pages.
func TestScanCleanRoomsML_RepeatedTokenKeepsPages(t *testing.T) {
	for _, op := range []string{"ListAudienceModels", "ListTrainedModels"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			h := crmlHandlers(newCRMLCalls())
			var typ, arn string
			if op == "ListAudienceModels" {
				typ, arn = TypeCleanRoomsMLAudienceModel, crmlARN("audience-model/am-t")
				h[op] = func(any) (any, error) {
					return &cleanroomsml.ListAudienceModelsOutput{AudienceModels: []mltypes.AudienceModelSummary{crmlAudienceModel("t")}, NextToken: sdkaws.String("same")}, nil
				}
			} else {
				typ, arn = TypeCleanRoomsMLTrainedModel, crmlChild("m-t", "trained-model")
				h[op] = func(in any) (any, error) {
					return &cleanroomsml.ListTrainedModelsOutput{TrainedModels: []mltypes.TrainedModelSummary{crmlTrainedModel(crmlMembershipOf(in))}, NextToken: sdkaws.String("same")}, nil
				}
			}

			_, _, err := crmlScan(context.Background(), t, st, h)
			if !errors.Is(err, errRepeatedNextToken) {
				t.Fatalf("err = %v; want errRepeatedNextToken", err)
			}
			if _, ok := crmlRowOf(t, st, typ, arn); !ok {
				t.Errorf("%s from the pages read missing", arn)
			}
		})
	}
}

// Once ctx ends no further phase starts, and the stop is reported once,
// naming the phases not run.
func TestScanCleanRoomsML_ContextEndStopsOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newCRMLCalls()
	h := crmlHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	memberships := h["ListMemberships"]
	h["ListMemberships"] = func(in any) (any, error) {
		if in.(*cleanrooms.ListMembershipsInput).NextToken != nil {
			cancel()
			return nil, context.Canceled
		}
		return memberships(in)
	}

	_, _, err := crmlScan(ctx, t, st, h)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v; want context.Canceled", err)
	}
	stops := 0
	for _, m := range bacTopMembers(err) {
		if errors.Is(m, context.Canceled) {
			stops++
		}
	}
	if stops != 1 {
		t.Errorf("members carrying the stop = %d; want 1 (err %v)", stops, err)
	}
	if n := calls.count("ListMLInputChannels"); n != 0 {
		t.Errorf("ListMLInputChannels called %d times after ctx ended", n)
	}
	// Memberships are listed in phase 5 of 7, so 2 never started.
	if !strings.Contains(err.Error(), "cleanrooms-ml: 2 phases not run") {
		t.Errorf("err = %v; want it to say 2 phases not run", err)
	}
}

// A region without Clean Rooms ML (NXDOMAIN on the first listing, nothing
// stored) is unavailable, and no other ML listing and no membership listing
// is made. An NXDOMAIN of the Clean Rooms membership listing alone costs
// nothing else and is not an error. An NXDOMAIN after rows were stored is a
// failure of this scan: the counts are kept.
func TestScanCleanRoomsML_Unreachable(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "ml.cleanrooms.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("ML endpoint absent", func(t *testing.T) {
		st := newTestStore(t)
		calls := newCRMLCalls()
		h := crmlHandlers(calls)
		h["ListConfiguredModelAlgorithms"] = func(any) (any, error) { return nil, nx }
		total, _, err := crmlScan(context.Background(), t, st, h)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if total != 0 || calls.count("ListTrainingDatasets") != 0 || calls.count("ListMemberships") != 0 || calls.count("ListTrainedModels") != 0 {
			t.Errorf("total = %d, ListTrainingDatasets, ListMemberships, ListTrainedModels calls = %d, %d, %d; want 0, 0, 0, 0",
				total, calls.count("ListTrainingDatasets"), calls.count("ListMemberships"), calls.count("ListTrainedModels"))
		}
	})
	t.Run("membership endpoint absent", func(t *testing.T) {
		st := newTestStore(t)
		h := crmlHandlers(newCRMLCalls())
		h["ListMemberships"] = func(any) (any, error) { return nil, nx }
		total, _, err := crmlScan(context.Background(), t, st, h)
		if err != nil || total != 9 {
			t.Errorf("total, err = %d, %v; want 9, nil", total, err)
		}
	})
	// Page 1 resolved, so page 2's NXDOMAIN is a failure of this scan, not a
	// region without Clean Rooms: it is returned, flattened by
	// resultLateNXDOMAIN (formatted with %v, so isDNSNotFound no longer sees
	// it and the dispatcher does not report the region unavailable). The op
	// it is filed under survives only in that flattened message.
	t.Run("membership endpoint gone after page 1", func(t *testing.T) {
		st := newTestStore(t)
		h := crmlHandlers(newCRMLCalls())
		h["ListMemberships"] = failOnCall(2, nx, h["ListMemberships"])
		_, _, err := crmlScan(context.Background(), t, st, h)
		assertGlueOutcome(t, err, outcomeError)
		if isDNSNotFound(err) || !strings.Contains(err.Error(), crmlMembershipsOp+": the endpoint stopped resolving") {
			t.Errorf("err = %v; want the NXDOMAIN flattened under %s", err, crmlMembershipsOp)
		}
		if _, ok := crmlRowOf(t, st, TypeCleanRoomsMLTrainedModel, crmlChild("m-t", "trained-model")); !ok {
			t.Error("trained model of m-t, from page 1, missing")
		}
		if _, ok := crmlRowOf(t, st, TypeCleanRoomsMLTrainedModel, crmlChild("m-b", "trained-model")); ok {
			t.Error("trained model of m-b stored; want its membership not read")
		}
	})
	t.Run("tag read after rows were listed", func(t *testing.T) {
		st := newTestStore(t)
		calls := newCRMLCalls()
		calls.errs[crmlARN("training-dataset/td-t")] = nx
		total, _, err := crmlScan(context.Background(), t, st, crmlHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
		if total != 15 {
			t.Errorf("total = %d; want 15", total)
		}
	})
}
