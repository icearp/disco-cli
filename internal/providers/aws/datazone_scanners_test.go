package aws

import (
	"context"
	"errors"
	"io"
	"reflect"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/datazone"
	dztypes "github.com/aws/aws-sdk-go-v2/service/datazone/types"
	"github.com/icearp/disco-cli/store"
)

func dzDomainARN(id string) string {
	return "arn:aws:datazone:" + testRegion + ":" + testAccountID + ":domain/" + id
}

func dzDomainItem(id string) dztypes.DomainSummary {
	return dztypes.DomainSummary{Arn: ptrStr(dzDomainARN(id)), Id: ptrStr(id), Name: ptrStr(id)}
}

// dzDomainsClient serves ListDomains from pages (page i+1 is requested with
// NextToken "i+1") and GetDomain from get.
func dzDomainsClient(t *testing.T, pages []func() (any, error), get func(id string) (any, error)) *datazone.Client {
	t.Helper()
	handlers := map[string]func(in any) (any, error){
		"ListDomains": func(in any) (any, error) {
			i := 0
			if tok := sv(in.(*datazone.ListDomainsInput).NextToken); tok != "" {
				i = int(tok[0] - '0')
			}
			return pages[i]()
		},
		"GetDomain": func(in any) (any, error) {
			return get(sv(in.(*datazone.GetDomainInput).Identifier))
		},
	}
	return datazone.NewFromConfig(opStubConfig(handlers, errors.New("unexpected op")))
}

func isNilErr(err error) bool { return err == nil }

// scanDZDomains runs the domain phase and returns the errors it filed joined
// with any write error, as scanDataZone reports them.
func scanDZDomains(client dataZoneAPI, st *store.Store) ([]*dzDomain, int, int, error) {
	acct := newTestAccount(testAccountID)
	var errs phaseErrs
	domains, total, inserted, werr := scanDataZoneDomains(context.Background(), client, acct, testRegion, st, testScanID, &errs)
	return domains, total, inserted, errors.Join(errs.join(st, acct, testRegion), werr)
}

func dzOnePage(items ...dztypes.DomainSummary) []func() (any, error) {
	return []func() (any, error){func() (any, error) { return &datazone.ListDomainsOutput{Items: items}, nil }}
}

// TestScanDataZoneDomains_ReadsTags pins that a domain row carries the Tags
// GetDomain returned (the ListDomains element has none), and that an untagged
// domain stores none.
func TestScanDataZoneDomains_ReadsTags(t *testing.T) {
	client := dzDomainsClient(t, dzOnePage(dzDomainItem("dom-1"), dzDomainItem("dom-2")), func(id string) (any, error) {
		out := &datazone.GetDomainOutput{Id: &id, RootDomainUnitId: ptrStr("root-" + id)}
		if id == "dom-1" {
			out.Tags = map[string]string{"team": "data"}
		}
		return out, nil
	})
	st := newTestStore(t)
	domains, total, inserted, err := scanDZDomains(client, st)
	if err != nil {
		t.Fatalf("scanDataZoneDomains: %v", err)
	}
	if total != 2 || inserted != 2 || len(domains) != 2 {
		t.Errorf("total=%d inserted=%d domains=%d, want 2/2/2", total, inserted, len(domains))
	}
	rows := rowsByNativeID(t, st, TypeDataZoneDomain)
	for _, id := range []string{"dom-1", "dom-2"} {
		if _, ok := rows[dzDomainARN(id)]; !ok {
			t.Fatalf("domain %s not stored", id)
		}
	}
	assertTagsMap(t, rows[dzDomainARN("dom-1")].TagsJSON, map[string]string{"team": "data"})
	if got := rows[dzDomainARN("dom-2")].TagsJSON; got != nil {
		t.Errorf("untagged domain TagsJSON = %q, want nil", *got)
	}
}

// TestScanDataZoneDomains_GetDomainFails pins what a failed GetDomain does to
// the two listed domains it fails for, beside a third read normally: a denial
// keeps each as its untagged ListDomains element with no root unit and one
// warning; any other error, a transient one included, keeps them and is
// returned; ResourceNotFoundException skips them silently.
func TestScanDataZoneDomains_GetDomainFails(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		wantErr   func(error) bool
		wantKept  bool
		wantWarns int
	}{
		{"denied", apiErr("AccessDeniedException", "not authorized to perform: datazone:GetDomain"), isNilErr, true, 1},
		{"transient", io.EOF, func(err error) bool { return errors.Is(err, io.EOF) }, true, 0},
		{"other error", apiErr("ValidationException", "bad domain"), func(err error) bool { return isAPIErrorCode(err, "ValidationException") }, true, 0},
		{"deleted since listed", apiErr("ResourceNotFoundException", "domain not found"), isNilErr, false, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			client := dzDomainsClient(t, dzOnePage(dzDomainItem("ok"), dzDomainItem("bad1"), dzDomainItem("bad2")), func(id string) (any, error) {
				if id != "ok" {
					return nil, c.err
				}
				return &datazone.GetDomainOutput{Id: &id, RootDomainUnitId: ptrStr("root-ok"), Tags: map[string]string{"team": "data"}}, nil
			})
			st := newTestStore(t)
			warns := countOpWarnings(st, dzGetDomainOp)
			domains, total, _, err := scanDZDomains(client, st)
			if !c.wantErr(err) {
				t.Fatalf("err = %v, unexpected for %s", err, c.name)
			}
			if *warns != c.wantWarns {
				t.Errorf("%s warnings = %d, want %d", dzGetDomainOp, *warns, c.wantWarns)
			}
			wantRows := 1
			if c.wantKept {
				wantRows = 3
			}
			if total != wantRows || len(domains) != wantRows {
				t.Errorf("total=%d domains=%d, want %d each", total, len(domains), wantRows)
			}
			for _, d := range domains {
				want := ""
				if d.id == "ok" {
					want = "root-ok"
				}
				if d.rootUnitID != want {
					t.Errorf("domain %s rootUnitID = %q, want %q", d.id, d.rootUnitID, want)
				}
			}

			rows := rowsByNativeID(t, st, TypeDataZoneDomain)
			assertTagsMap(t, rows[dzDomainARN("ok")].TagsJSON, map[string]string{"team": "data"})
			for _, id := range []string{"bad1", "bad2"} {
				row, kept := rows[dzDomainARN(id)]
				if kept != c.wantKept {
					t.Fatalf("%s stored = %v, want %v", id, kept, c.wantKept)
				}
				if kept && row.TagsJSON != nil {
					t.Errorf("%s TagsJSON = %q, want nil (ListDomains element has no tags)", id, *row.TagsJSON)
				}
			}
		})
	}
}

// A denied GetDomain warns that the domain was stored as its list element
// only once it was: a failed write returns the store error and no warning.
func TestScanDataZoneDomains_KeptDomainWarnsOnlyOnceStored(t *testing.T) {
	st, closeStore := openClosingStore(t)
	warns := countOpWarnings(st, dzGetDomainOp)
	client := dzDomainsClient(t, dzOnePage(dzDomainItem("dom-1")), func(string) (any, error) {
		closeStore()
		return nil, apiErr("AccessDeniedException", "not authorized to perform: datazone:GetDomain")
	})
	_, _, _, err := scanDZDomains(client, st)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Errorf("err = %v, want the store write failure", err)
	}
	if *warns != 0 {
		t.Errorf("%s warnings = %d, want 0: the domain was never stored", dzGetDomainOp, *warns)
	}
}

// TestScanDataZoneDomains_ListPageFails pins that a failed first ListDomains
// page stores nothing (denied: warned, nil; otherwise returned), and that a
// failed later page keeps the domains read before it: a denial warns once and
// returns nil, any other error is returned. A page repeating the NextToken it
// was sent keeps its domains and returns errRepeatedNextToken.
func TestScanDataZoneDomains_ListPageFails(t *testing.T) {
	denied := func() (any, error) {
		return nil, apiErr("AccessDeniedException", "not authorized to perform: datazone:ListDomains")
	}
	hard := func() (any, error) { return nil, apiErr("ValidationException", "bad token") }
	page1 := func() (any, error) {
		return &datazone.ListDomainsOutput{NextToken: ptrStr("1"), Items: []dztypes.DomainSummary{dzDomainItem("dom-1")}}, nil
	}
	repeated := func() (any, error) {
		return &datazone.ListDomainsOutput{NextToken: ptrStr("1"), Items: []dztypes.DomainSummary{dzDomainItem("dom-2")}}, nil
	}
	isNil := func(err error) bool { return err == nil }
	isValidation := func(err error) bool { return isAPIErrorCode(err, "ValidationException") }
	isRepeated := func(err error) bool { return errors.Is(err, errRepeatedNextToken) }
	cases := []struct {
		name      string
		pages     []func() (any, error)
		wantErr   func(error) bool
		wantRows  int
		wantWarns int
	}{
		{"first page denied", []func() (any, error){denied}, isNil, 0, 1},
		{"first page hard error", []func() (any, error){hard}, isValidation, 0, 0},
		{"later page denied", []func() (any, error){page1, denied}, isNil, 1, 1},
		{"later page hard error", []func() (any, error){page1, hard}, isValidation, 1, 0},
		{"repeated token", []func() (any, error){page1, repeated}, isRepeated, 2, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			client := dzDomainsClient(t, c.pages, func(id string) (any, error) {
				return &datazone.GetDomainOutput{Id: &id, Tags: map[string]string{"team": "data"}}, nil
			})
			st := newTestStore(t)
			warns := countOpWarnings(st, dzListDomainsOp)
			domains, total, _, err := scanDZDomains(client, st)
			if !c.wantErr(err) {
				t.Fatalf("err = %v, unexpected for %s", err, c.name)
			}
			if total != c.wantRows || len(domains) != c.wantRows {
				t.Errorf("total=%d domains=%d, want %d each", total, len(domains), c.wantRows)
			}
			if *warns != c.wantWarns {
				t.Errorf("%s warnings = %d, want %d", dzListDomainsOp, *warns, c.wantWarns)
			}
			rows := rowsByNativeID(t, st, TypeDataZoneDomain)
			if len(rows) != c.wantRows {
				t.Fatalf("stored %d domains, want %d", len(rows), c.wantRows)
			}
			if c.wantRows > 0 {
				assertTagsMap(t, rows[dzDomainARN("dom-1")].TagsJSON, map[string]string{"team": "data"})
			}
		})
	}
}

// TestScanDataZoneDomains_EmptyKeyWarns pins that a listed domain with no ARN
// or no id is not stored and is reported once, beside a keyed domain stored
// normally.
func TestScanDataZoneDomains_EmptyKeyWarns(t *testing.T) {
	noID := dzDomainItem("no-id")
	noID.Id = nil
	noARN := dzDomainItem("no-arn")
	noARN.Arn = nil
	client := dzDomainsClient(t, dzOnePage(dzDomainItem("dom-1"), noID, noARN), func(id string) (any, error) {
		return &datazone.GetDomainOutput{Id: &id}, nil
	})
	st := newTestStore(t)
	warns := countOpWarnings(st, dzListDomainsOp)
	domains, total, _, err := scanDZDomains(client, st)
	if err != nil {
		t.Fatalf("scanDataZoneDomains: %v", err)
	}
	if total != 1 || len(domains) != 1 {
		t.Errorf("total=%d domains=%d, want 1 each", total, len(domains))
	}
	if *warns != 1 {
		t.Errorf("%s warnings = %d, want 1", dzListDomainsOp, *warns)
	}
	if _, ok := rowsByNativeID(t, st, TypeDataZoneDomain)[dzDomainARN("dom-1")]; !ok {
		t.Error("keyed domain not stored")
	}
}

// dzTopLevelHandlers serves every op dzTopLevelResponses stubs from its first
// response, for scanDataZone tests that override single ops.
func dzTopLevelHandlers() map[string]func(in any) (any, error) {
	handlers := map[string]func(in any) (any, error){}
	for op, calls := range dzTopLevelResponses() {
		out := calls[0].Output
		handlers[op] = func(any) (any, error) { return out, nil }
	}
	handlers["ListEnvironmentBlueprints"] = func(any) (any, error) { return &datazone.ListEnvironmentBlueprintsOutput{}, nil }
	return handlers
}

// TestScanDataZone_ChildPhaseErrorRunsLaterPhases pins that a hard error in a
// child phase is returned without stopping the phases after it, and that a
// phase failing on one domain keeps the rows it read for another.
func TestScanDataZone_ChildPhaseErrorRunsLaterPhases(t *testing.T) {
	handlers := dzTopLevelHandlers()
	handlers["ListDomains"] = func(any) (any, error) {
		return &datazone.ListDomainsOutput{Items: []dztypes.DomainSummary{dzDomainItem("dom-1"), dzDomainItem("dom-2")}}, nil
	}
	handlers["GetDomain"] = func(in any) (any, error) {
		id := sv(in.(*datazone.GetDomainInput).Identifier)
		return &datazone.GetDomainOutput{Id: &id, RootDomainUnitId: ptrStr("root-" + id)}, nil
	}
	profilesErr := apiErr("ValidationException", "profiles")
	projectsErr := apiErr("ConflictException", "projects")
	handlers["ListProjectProfiles"] = func(any) (any, error) { return nil, profilesErr }
	handlers["ListProjects"] = func(in any) (any, error) {
		if sv(in.(*datazone.ListProjectsInput).DomainIdentifier) == "dom-2" {
			return nil, projectsErr
		}
		return &datazone.ListProjectsOutput{Items: []dztypes.ProjectSummary{{Id: ptrStr("prj-1"), Name: ptrStr("p")}}}, nil
	}
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, errors.New("unexpected op"))
	_, _, err := scanDataZone(context.Background(), acct, testRegion, st, testScanID)
	if !errors.Is(err, profilesErr) || !errors.Is(err, projectsErr) {
		t.Fatalf("err = %v, want both phase errors", err)
	}
	if _, ok := dzRows(t, st, TypeDataZoneProject)[dzARN(testRegion, testAccountID, "dom-1", "project", "prj-1")]; !ok {
		t.Error("dom-1 project not stored beside dom-2's failure")
	}
	if _, ok := dzRows(t, st, TypeDataZoneNotebook)[dzARN(testRegion, testAccountID, "dom-1", "notebook", "nb-1")]; !ok {
		t.Error("dom-1 notebook (last phase, under its stored project) not stored")
	}
	for _, dom := range []string{"dom-1", "dom-2"} {
		if _, ok := dzRows(t, st, TypeDataZoneSubscription)[dzARN(testRegion, testAccountID, dom, "subscription", "sub-1")]; !ok {
			t.Errorf("%s subscription (later phase) not stored", dom)
		}
	}
}

// A child phase whose store write fails stops the phases after it.
func TestScanDataZone_StoreWriteStopsPhases(t *testing.T) {
	st, closeStore := openClosingStore(t)
	handlers := dzTopLevelHandlers()
	notebooks := false
	handlers["ListProjectProfiles"] = func(any) (any, error) {
		closeStore()
		return &datazone.ListProjectProfilesOutput{Items: []dztypes.ProjectProfileSummary{{Id: ptrStr("pp-1")}}}, nil
	}
	handlers["ListNotebooks"] = func(any) (any, error) {
		notebooks = true
		return &datazone.ListNotebooksOutput{}, nil
	}
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, errors.New("unexpected op"))
	_, _, err := scanDataZone(context.Background(), acct, testRegion, st, testScanID)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v, want the store write failure", err)
	}
	if notebooks {
		t.Error("ListNotebooks called after a failed store write")
	}
}

// TestScanDataZone_DomainErrorStillListsChildren pins that a GetDomain error
// on one domain is returned without skipping the child phases: both stored
// domains' projects are listed and counted.
func TestScanDataZone_DomainErrorStillListsChildren(t *testing.T) {
	handlers := dzTopLevelHandlers()
	handlers["ListDomains"] = func(any) (any, error) {
		return &datazone.ListDomainsOutput{Items: []dztypes.DomainSummary{dzDomainItem("dom-1"), dzDomainItem("dom-2")}}, nil
	}
	handlers["GetDomain"] = func(in any) (any, error) {
		if id := sv(in.(*datazone.GetDomainInput).Identifier); id == "dom-1" {
			return &datazone.GetDomainOutput{Id: &id, RootDomainUnitId: ptrStr("root-1")}, nil
		}
		return nil, apiErr("ValidationException", "bad domain")
	}
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, errors.New("unexpected op"))
	total, _, err := scanDataZone(context.Background(), acct, testRegion, st, testScanID)
	if !isAPIErrorCode(err, "ValidationException") {
		t.Fatalf("err = %v, want one carrying ValidationException", err)
	}
	projects := dzRows(t, st, TypeDataZoneProject)
	for _, dom := range []string{"dom-1", "dom-2"} {
		if _, ok := projects[dzARN(testRegion, testAccountID, dom, "project", "prj-1")]; !ok {
			t.Errorf("%s project not stored", dom)
		}
	}
	if total < 4 {
		t.Errorf("total = %d, want at least 4 (2 domains, 2 projects)", total)
	}
}

// TestScanDataZoneChildPhases_ErrorKeepsRowsRead pins, for each child phase
// in this file and the profiles file, with failing dom-2 listed before dom-1
// and failing dom-3 after it: a hard error is returned with dom-1's row
// stored; a denial warns once under the phase's op, keeps dom-1's row and
// returns nil; an item with no id beside it is not stored and warns once; and
// when the write fails, ErrStoreWrite is returned and the id-less item does
// not warn.
func TestScanDataZoneChildPhases_ErrorKeepsRowsRead(t *testing.T) {
	var bad error
	// fail reports whether the call is for dom-2 or dom-3, the failing domains.
	fail := func(domain *string) bool { return sv(domain) == "dom-2" || sv(domain) == "dom-3" }
	cases := []struct {
		op      string
		typ     string
		fn      dzScanFn
		handler func(in any) (any, error)
	}{
		{"ListDomainUnitsForParent", TypeDataZoneDomainUnit, scanDataZoneDomainUnits, func(in any) (any, error) {
			i := in.(*datazone.ListDomainUnitsForParentInput)
			if fail(i.DomainIdentifier) {
				return nil, bad
			}
			out := &datazone.ListDomainUnitsForParentOutput{}
			if sv(i.ParentDomainUnitIdentifier) == "root-1" {
				out.Items = []dztypes.DomainUnitSummary{{Id: ptrStr("du-1")}}
			}
			return out, nil
		}},
		{"ListProjectProfiles", TypeDataZoneProjectProfile, scanDataZoneProjectProfiles, func(in any) (any, error) {
			if fail(in.(*datazone.ListProjectProfilesInput).DomainIdentifier) {
				return nil, bad
			}
			return &datazone.ListProjectProfilesOutput{Items: []dztypes.ProjectProfileSummary{{Id: ptrStr("pp-1")}}}, nil
		}},
		{"ListProjects", TypeDataZoneProject, scanDataZoneProjects, func(in any) (any, error) {
			if fail(in.(*datazone.ListProjectsInput).DomainIdentifier) {
				return nil, bad
			}
			return &datazone.ListProjectsOutput{Items: []dztypes.ProjectSummary{{Id: ptrStr("prj-x")}}}, nil
		}},
		{"ListProjectMemberships", TypeDataZoneProjectMembership, scanDataZoneProjectMemberships, func(in any) (any, error) {
			if fail(in.(*datazone.ListProjectMembershipsInput).DomainIdentifier) {
				return nil, bad
			}
			return &datazone.ListProjectMembershipsOutput{Members: []dztypes.ProjectMember{{Designation: dztypes.UserDesignationProjectOwner}}}, nil
		}},
		{"SearchGroupProfiles", TypeDataZoneGroupProfile, scanDataZoneGroupProfiles, func(in any) (any, error) {
			i := in.(*datazone.SearchGroupProfilesInput)
			if fail(i.DomainIdentifier) {
				return nil, bad
			}
			out := &datazone.SearchGroupProfilesOutput{}
			if i.GroupType == dzGroupTypes[0] {
				out.Items = []dztypes.GroupProfileSummary{{Id: ptrStr("gp-1")}}
			}
			return out, nil
		}},
		{"SearchUserProfiles", TypeDataZoneUserProfile, scanDataZoneUserProfiles, func(in any) (any, error) {
			i := in.(*datazone.SearchUserProfilesInput)
			if fail(i.DomainIdentifier) {
				return nil, bad
			}
			out := &datazone.SearchUserProfilesOutput{}
			if i.UserType == dzUserTypes[0] {
				out.Items = []dztypes.UserProfileSummary{{Id: ptrStr("up-1")}}
			}
			return out, nil
		}},
		{"ListEnvironmentProfiles", TypeDataZoneEnvironmentProfile, scanDataZoneEnvironmentProfiles, func(in any) (any, error) {
			if fail(in.(*datazone.ListEnvironmentProfilesInput).DomainIdentifier) {
				return nil, bad
			}
			return &datazone.ListEnvironmentProfilesOutput{Items: []dztypes.EnvironmentProfileSummary{{Id: ptrStr("ep-1")}}}, nil
		}},
		{"ListEnvironmentBlueprintConfigurations", TypeDataZoneEnvironmentBlueprintConfiguration, scanDataZoneEnvironmentBlueprintConfigurations, func(in any) (any, error) {
			if fail(in.(*datazone.ListEnvironmentBlueprintConfigurationsInput).DomainIdentifier) {
				return nil, bad
			}
			return &datazone.ListEnvironmentBlueprintConfigurationsOutput{Items: []dztypes.EnvironmentBlueprintConfigurationItem{{EnvironmentBlueprintId: ptrStr("bp-1")}}}, nil
		}},
		{"ListEnvironments", TypeDataZoneEnvironment, scanDataZoneEnvironments, func(in any) (any, error) {
			if fail(in.(*datazone.ListEnvironmentsInput).DomainIdentifier) {
				return nil, bad
			}
			return &datazone.ListEnvironmentsOutput{Items: []dztypes.EnvironmentSummary{{Id: ptrStr("env-x")}}}, nil
		}},
		{"ListEnvironmentActions", TypeDataZoneEnvironmentActions, scanDataZoneEnvironmentActions, func(in any) (any, error) {
			if fail(in.(*datazone.ListEnvironmentActionsInput).DomainIdentifier) {
				return nil, bad
			}
			return &datazone.ListEnvironmentActionsOutput{Items: []dztypes.EnvironmentActionSummary{{Id: ptrStr("ea-1")}}}, nil
		}},
		{"ListSubscriptionTargets", TypeDataZoneSubscriptionTarget, scanDataZoneSubscriptionTargets, func(in any) (any, error) {
			if fail(in.(*datazone.ListSubscriptionTargetsInput).DomainIdentifier) {
				return nil, bad
			}
			return &datazone.ListSubscriptionTargetsOutput{Items: []dztypes.SubscriptionTargetSummary{{Id: ptrStr("st-1")}}}, nil
		}},
		{"ListDataSources", TypeDataZoneDataSource, scanDataZoneDataSources, func(in any) (any, error) {
			if fail(in.(*datazone.ListDataSourcesInput).DomainIdentifier) {
				return nil, bad
			}
			return &datazone.ListDataSourcesOutput{Items: []dztypes.DataSourceSummary{{DataSourceId: ptrStr("ds-1")}}}, nil
		}},
		{"ListConnections", TypeDataZoneConnection, scanDataZoneConnections, func(in any) (any, error) {
			if fail(in.(*datazone.ListConnectionsInput).DomainIdentifier) {
				return nil, bad
			}
			return &datazone.ListConnectionsOutput{Items: []dztypes.ConnectionSummary{{ConnectionId: ptrStr("cn-1")}}}, nil
		}},
	}
	for _, e := range []struct {
		name       string
		err        error
		wantErr    bool
		wantWarns  int
		emptyItem  bool
		writeFails bool
	}{
		{"hard error", apiErr("ValidationException", "bad input"), true, 0, false, false},
		{"denied", apiErr("AccessDeniedException", "not authorized to perform: datazone:List"), false, 1, false, false},
		{"no id beside denial", apiErr("AccessDeniedException", "not authorized to perform: datazone:List"), false, 2, true, false},
		{"no id, write fails", apiErr("ValidationException", "bad input"), true, 0, true, true},
	} {
		for _, c := range cases {
			// Memberships synthesize their id, so they have no id-less case.
			if e.emptyItem && c.op == "ListProjectMemberships" {
				continue
			}
			t.Run(e.name+"/"+c.op, func(t *testing.T) {
				bad = e.err
				domains := []*dzDomain{
					{id: "dom-2", rootUnitID: "root-2", projectIDs: []string{"prj-2"}, envIDs: []string{"env-2"}},
					{id: "dom-1", rootUnitID: "root-1", projectIDs: []string{"prj-1"}, envIDs: []string{"env-1"}},
					{id: "dom-3", rootUnitID: "root-3", projectIDs: []string{"prj-3"}, envIDs: []string{"env-3"}},
				}
				handler := c.handler
				if e.emptyItem {
					handler = func(in any) (any, error) {
						out, err := c.handler(in)
						if err == nil {
							appendZeroItem(out)
						}
						return out, err
					}
				}
				st := newTestStore(t)
				if e.writeFails {
					var closeStore func()
					st, closeStore = openClosingStore(t)
					serve := handler
					handler = func(in any) (any, error) {
						closeStore()
						return serve(in)
					}
				}
				client := datazone.NewFromConfig(opStubConfig(map[string]func(in any) (any, error){c.op: handler}, errors.New("unexpected op")))
				warns := countOpWarnings(st, "datazone:"+c.op)
				total, _, err := c.fn(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, domains)
				if got := errors.Is(err, bad); got != e.wantErr || !e.wantErr && err != nil {
					t.Fatalf("err = %v, want dom-2 failure returned: %v", err, e.wantErr)
				}
				if *warns != e.wantWarns {
					t.Errorf("warnings = %d, want %d", *warns, e.wantWarns)
				}
				if e.writeFails {
					if !errors.Is(err, store.ErrStoreWrite) {
						t.Errorf("err = %v, want ErrStoreWrite", err)
					}
					return
				}
				if n := len(dzRows(t, st, c.typ)); total != 1 || n != 1 {
					t.Errorf("total=%d stored=%d, want dom-1's row (1 each)", total, n)
				}
			})
		}
	}
}

// appendZeroItem adds a zero element, which has no id, to the Items slice of a
// non-empty list output, so every phase sees one id-less item beside a good one.
func appendZeroItem(out any) {
	items := reflect.ValueOf(out).Elem().FieldByName("Items")
	if items.Len() > 0 {
		items.Set(reflect.Append(items, reflect.Zero(items.Type().Elem())))
	}
}

// TestScanDataZone_ListDomainsFailsReturnsError pins that a failed first
// ListDomains page, which stores no domain, is still returned.
func TestScanDataZone_ListDomainsFailsReturnsError(t *testing.T) {
	bad := apiErr("ValidationException", "bad request")
	handlers := map[string]func(in any) (any, error){
		"ListDomains": func(any) (any, error) { return nil, bad },
	}
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, errors.New("unexpected op"))
	total, _, err := scanDataZone(context.Background(), acct, testRegion, st, testScanID)
	if !errors.Is(err, bad) || total != 0 {
		t.Fatalf("total=%d err=%v, want 0 and the ListDomains error", total, err)
	}
}

// TestScanDataZone_TransientGetDomainFiledUnderGetDomain pins that a
// transient GetDomain beside a hard child-phase error is demoted to a warning
// under the GetDomain op, not under ListDomains.
func TestScanDataZone_TransientGetDomainFiledUnderGetDomain(t *testing.T) {
	handlers := dzTopLevelHandlers()
	handlers["GetDomain"] = func(any) (any, error) { return nil, io.EOF }
	projectsErr := apiErr("ConflictException", "projects")
	handlers["ListProjects"] = func(any) (any, error) { return nil, projectsErr }
	st := newTestStore(t)
	getWarns := countOpWarnings(st, dzGetDomainOp)
	listWarns := countOpWarnings(st, dzListDomainsOp)
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, errors.New("unexpected op"))
	_, _, err := scanDataZone(context.Background(), acct, testRegion, st, testScanID)
	if !errors.Is(err, projectsErr) {
		t.Fatalf("err = %v, want the ListProjects error", err)
	}
	if *getWarns != 1 || *listWarns != 0 {
		t.Errorf("GetDomain warnings=%d ListDomains warnings=%d, want 1/0", *getWarns, *listWarns)
	}
}
