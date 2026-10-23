package aws

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/qconnect"
	qconnecttypes "github.com/aws/aws-sdk-go-v2/service/qconnect/types"
	"github.com/icearp/disco-cli/store"
)

const wisdomTestRegion = "us-east-1"

var wisdomTestTags = map[string]string{"env": "prod"}

// wisdomParentInputs are the request fields that name the parent of a
// per-parent listing. wisdomTwoPages checks that every other request field,
// except NextToken and MaxResults, is unset.
var wisdomParentInputs = map[string]bool{
	"AssistantId": true, "KnowledgeBaseId": true, "AiAgentId": true, "AiGuardrailId": true,
	"AiPromptId": true, "MessageTemplateId": true, "ContentId": true,
}

// wisdomTwoPages makes serve the first of two pages: a call without NextToken
// gets serve's output with NextToken "t1", a call with "t1" gets an empty last
// page. A token not forwarded therefore repeats page one and ends the listing
// with errRepeatedNextToken. Every call must ask for the documented page cap
// of 100 and leave every filter unset (Origin on the AI agent and prompt
// listings narrows them to SYSTEM or CUSTOMER).
func wisdomTwoPages(t *testing.T, op string, serve func(in any) (any, error)) func(in any) (any, error) {
	return func(in any) (any, error) {
		req := reflect.ValueOf(in).Elem()
		for i := range req.NumField() {
			name := req.Type().Field(i).Name
			f := req.Field(i)
			switch {
			case name == "NextToken" || wisdomParentInputs[name]:
			case name == "MaxResults":
				if mr := f.Interface().(*int32); mr == nil || *mr != 100 {
					t.Errorf("%s MaxResults = %v, want 100", op, mr)
				}
			case !f.IsZero():
				t.Errorf("%s %s = %v, want unset", op, name, f.Interface())
			}
		}
		switch token := req.FieldByName("NextToken").Interface().(*string); sv(token) {
		case "":
			out, err := serve(in)
			if err == nil {
				reflect.ValueOf(out).Elem().FieldByName("NextToken").Set(reflect.ValueOf(ptrStr("t1")))
			}
			return out, err
		case "t1":
			out, err := serve(in)
			if err != nil {
				return nil, err
			}
			return reflect.New(reflect.TypeOf(out).Elem()).Interface(), nil
		default:
			t.Errorf("%s NextToken = %q, want t1", op, sv(token))
			return nil, errors.New("bad token")
		}
	}
}

// wisdomAllHandlers serves every Wisdom listing as two pages (wisdomTwoPages).
// Assistants a-1 and a-2 and knowledge bases kb-1 and kb-2 are listed; each
// per-parent listing gives each parent one child, named and tagged under a-1
// or kb-1 and unnamed and untagged under the other. Every version listing
// gives version 1, and an AI version's summary carries tags. A call for any
// other parent fails the test.
func wisdomAllHandlers(t *testing.T) map[string]func(in any) (any, error) {
	h := wisdomOnePageHandlers(t)
	for op, f := range h {
		h[op] = wisdomTwoPages(t, op, f)
	}
	return h
}

func wisdomOnePageHandlers(t *testing.T) map[string]func(in any) (any, error) {
	parent := func(op string, id *string, want ...string) string {
		p := sv(id)
		for _, w := range want {
			if p == w {
				return p
			}
		}
		t.Errorf("%s parent = %q, want one of %v", op, p, want)
		return p
	}
	// child checks that a child listing names a parent and child of the same
	// fixture: AiAgentId "agent-a-1" under AssistantId "a-1".
	child := func(op string, pid, cid *string, prefix string, want ...string) string {
		p := parent(op, pid, want...)
		if c := sv(cid); c != prefix+p {
			t.Errorf("%s child = %q, want %q", op, c, prefix+p)
		}
		return p
	}
	assistants := []string{"a-1", "a-2"}
	kbs := []string{"kb-1", "kb-2"}
	arn := func(kind, n string) *string { return ptrStr("arn:" + kind + "/" + n) }
	first := func(p string) bool { return p == "a-1" || p == "kb-1" }
	nameIf := func(p, n string) *string {
		if first(p) {
			return ptrStr(n)
		}
		return nil
	}
	tagsIf := func(p string) map[string]string {
		if first(p) {
			return wisdomTestTags
		}
		return nil
	}
	one := sdkInt64(1)
	return map[string]func(in any) (any, error){
		"ListAssistants": func(any) (any, error) {
			return &qconnect.ListAssistantsOutput{AssistantSummaries: []qconnecttypes.AssistantSummary{
				{AssistantArn: arn("assistant", "a-1"), AssistantId: ptrStr("a-1"), Name: ptrStr("name-a-1"), Tags: wisdomTestTags},
				{AssistantArn: arn("assistant", "a-2"), AssistantId: ptrStr("a-2")},
			}}, nil
		},
		"ListKnowledgeBases": func(any) (any, error) {
			return &qconnect.ListKnowledgeBasesOutput{KnowledgeBaseSummaries: []qconnecttypes.KnowledgeBaseSummary{
				{KnowledgeBaseArn: arn("kb", "kb-1"), KnowledgeBaseId: ptrStr("kb-1"), Name: ptrStr("name-kb-1"), Tags: wisdomTestTags},
				{KnowledgeBaseArn: arn("kb", "kb-2"), KnowledgeBaseId: ptrStr("kb-2")},
			}}, nil
		},
		"ListAssistantAssociations": func(in any) (any, error) {
			p := parent("ListAssistantAssociations", in.(*qconnect.ListAssistantAssociationsInput).AssistantId, assistants...)
			return &qconnect.ListAssistantAssociationsOutput{AssistantAssociationSummaries: []qconnecttypes.AssistantAssociationSummary{
				{AssistantAssociationArn: arn("assoc", p), AssistantAssociationId: ptrStr("assoc-" + p), Tags: tagsIf(p)},
			}}, nil
		},
		"ListAIAgents": func(in any) (any, error) {
			p := parent("ListAIAgents", in.(*qconnect.ListAIAgentsInput).AssistantId, assistants...)
			return &qconnect.ListAIAgentsOutput{AiAgentSummaries: []qconnecttypes.AIAgentSummary{
				{AiAgentArn: arn("agent", p), AiAgentId: ptrStr("agent-" + p), Name: nameIf(p, "name-agent-"+p), Tags: tagsIf(p)},
			}}, nil
		},
		"ListAIGuardrails": func(in any) (any, error) {
			p := parent("ListAIGuardrails", in.(*qconnect.ListAIGuardrailsInput).AssistantId, assistants...)
			return &qconnect.ListAIGuardrailsOutput{AiGuardrailSummaries: []qconnecttypes.AIGuardrailSummary{
				{AiGuardrailArn: arn("guardrail", p), AiGuardrailId: ptrStr("guardrail-" + p), Name: nameIf(p, "name-guardrail-"+p), Tags: tagsIf(p)},
			}}, nil
		},
		"ListAIPrompts": func(in any) (any, error) {
			p := parent("ListAIPrompts", in.(*qconnect.ListAIPromptsInput).AssistantId, assistants...)
			return &qconnect.ListAIPromptsOutput{AiPromptSummaries: []qconnecttypes.AIPromptSummary{
				{AiPromptArn: arn("prompt", p), AiPromptId: ptrStr("prompt-" + p), Name: nameIf(p, "name-prompt-"+p), Tags: tagsIf(p)},
			}}, nil
		},
		"ListMessageTemplates": func(in any) (any, error) {
			p := parent("ListMessageTemplates", in.(*qconnect.ListMessageTemplatesInput).KnowledgeBaseId, kbs...)
			return &qconnect.ListMessageTemplatesOutput{MessageTemplateSummaries: []qconnecttypes.MessageTemplateSummary{
				{MessageTemplateArn: arn("mt", p), MessageTemplateId: ptrStr("mt-" + p), Name: nameIf(p, "name-mt-"+p), Tags: tagsIf(p)},
			}}, nil
		},
		"ListContents": func(in any) (any, error) {
			p := parent("ListContents", in.(*qconnect.ListContentsInput).KnowledgeBaseId, kbs...)
			return &qconnect.ListContentsOutput{ContentSummaries: []qconnecttypes.ContentSummary{
				{ContentArn: arn("content", p), ContentId: ptrStr("content-" + p), Name: nameIf(p, "name-content-"+p), Tags: tagsIf(p)},
			}}, nil
		},
		"ListQuickResponses": func(in any) (any, error) {
			p := parent("ListQuickResponses", in.(*qconnect.ListQuickResponsesInput).KnowledgeBaseId, kbs...)
			return &qconnect.ListQuickResponsesOutput{QuickResponseSummaries: []qconnecttypes.QuickResponseSummary{
				{QuickResponseArn: arn("qr", p), QuickResponseId: ptrStr("qr-" + p), Name: nameIf(p, "name-qr-"+p), Tags: tagsIf(p)},
			}}, nil
		},
		"ListAIAgentVersions": func(in any) (any, error) {
			req := in.(*qconnect.ListAIAgentVersionsInput)
			p := child("ListAIAgentVersions", req.AssistantId, req.AiAgentId, "agent-", assistants...)
			return &qconnect.ListAIAgentVersionsOutput{AiAgentVersionSummaries: []qconnecttypes.AIAgentVersionSummary{
				{AiAgentSummary: &qconnecttypes.AIAgentSummary{AiAgentArn: arn("agent", p), Tags: wisdomTestTags}, VersionNumber: one},
			}}, nil
		},
		"ListAIGuardrailVersions": func(in any) (any, error) {
			req := in.(*qconnect.ListAIGuardrailVersionsInput)
			p := child("ListAIGuardrailVersions", req.AssistantId, req.AiGuardrailId, "guardrail-", assistants...)
			return &qconnect.ListAIGuardrailVersionsOutput{AiGuardrailVersionSummaries: []qconnecttypes.AIGuardrailVersionSummary{
				{AiGuardrailSummary: &qconnecttypes.AIGuardrailSummary{AiGuardrailArn: arn("guardrail", p), Tags: wisdomTestTags}, VersionNumber: one},
			}}, nil
		},
		"ListAIPromptVersions": func(in any) (any, error) {
			req := in.(*qconnect.ListAIPromptVersionsInput)
			p := child("ListAIPromptVersions", req.AssistantId, req.AiPromptId, "prompt-", assistants...)
			return &qconnect.ListAIPromptVersionsOutput{AiPromptVersionSummaries: []qconnecttypes.AIPromptVersionSummary{
				{AiPromptSummary: &qconnecttypes.AIPromptSummary{AiPromptArn: arn("prompt", p), Tags: wisdomTestTags}, VersionNumber: one},
			}}, nil
		},
		"ListMessageTemplateVersions": func(in any) (any, error) {
			req := in.(*qconnect.ListMessageTemplateVersionsInput)
			p := child("ListMessageTemplateVersions", req.KnowledgeBaseId, req.MessageTemplateId, "mt-", kbs...)
			return &qconnect.ListMessageTemplateVersionsOutput{MessageTemplateVersionSummaries: []qconnecttypes.MessageTemplateVersionSummary{
				{MessageTemplateArn: arn("mt", p), VersionNumber: one, Name: nameIf(p, "name-mtv-"+p)},
			}}, nil
		},
		"ListContentAssociations": func(in any) (any, error) {
			req := in.(*qconnect.ListContentAssociationsInput)
			p := child("ListContentAssociations", req.KnowledgeBaseId, req.ContentId, "content-", kbs...)
			return &qconnect.ListContentAssociationsOutput{ContentAssociationSummaries: []qconnecttypes.ContentAssociationSummary{
				{ContentAssociationArn: arn("ca", p), ContentAssociationId: ptrStr("ca-" + p), Tags: tagsIf(p)},
			}}, nil
		},
	}
}

func sdkInt64(v int64) *int64 { return &v }

func wisdomScanAll(t *testing.T, st *store.Store, h map[string]func(in any) (any, error)) (int, error) {
	t.Helper()
	client := qconnect.NewFromConfig(opStubConfig(h, errors.New("unexpected op")))
	total, _, err := scanWisdomAll(context.Background(), client, newTestAccount(testAccountID), wisdomTestRegion, st, testScanID)
	return total, err
}

// wisdomRowCase is one type's expected rows: tagged is the row whose element
// carries wisdomTestTags (zero when the type stores no tags), untagged the
// rows that store none.
type wisdomRowCase struct {
	typ      string
	tagged   wisdomWantRow
	untagged []wisdomWantRow
}

type wisdomWantRow struct{ nativeID, name string }

// wisdomAllRows is what wisdomAllHandlers stores. The AI version rows store no
// tags although their summaries carry some.
var wisdomAllRows = []wisdomRowCase{
	{TypeWisdomAssistant, wisdomWantRow{"arn:assistant/a-1", "name-a-1"}, []wisdomWantRow{{"arn:assistant/a-2", "a-2"}}},
	{TypeWisdomKnowledgeBase, wisdomWantRow{"arn:kb/kb-1", "name-kb-1"}, []wisdomWantRow{{"arn:kb/kb-2", "kb-2"}}},
	{TypeWisdomAssistantAssociation, wisdomWantRow{"arn:assoc/a-1", "assoc-a-1"}, []wisdomWantRow{{"arn:assoc/a-2", "assoc-a-2"}}},
	{TypeWisdomAIAgent, wisdomWantRow{"arn:agent/a-1", "name-agent-a-1"}, []wisdomWantRow{{"arn:agent/a-2", "agent-a-2"}}},
	{TypeWisdomAIGuardrail, wisdomWantRow{"arn:guardrail/a-1", "name-guardrail-a-1"}, []wisdomWantRow{{"arn:guardrail/a-2", "guardrail-a-2"}}},
	{TypeWisdomAIPrompt, wisdomWantRow{"arn:prompt/a-1", "name-prompt-a-1"}, []wisdomWantRow{{"arn:prompt/a-2", "prompt-a-2"}}},
	{TypeWisdomMessageTemplate, wisdomWantRow{"arn:mt/kb-1", "name-mt-kb-1"}, []wisdomWantRow{{"arn:mt/kb-2", "mt-kb-2"}}},
	{TypeWisdomContent, wisdomWantRow{"arn:content/kb-1", "name-content-kb-1"}, []wisdomWantRow{{"arn:content/kb-2", "content-kb-2"}}},
	{TypeWisdomQuickResponse, wisdomWantRow{"arn:qr/kb-1", "name-qr-kb-1"}, []wisdomWantRow{{"arn:qr/kb-2", "arn:qr/kb-2"}}},
	{TypeWisdomContentAssociation, wisdomWantRow{"arn:ca/kb-1", "ca-kb-1"}, []wisdomWantRow{{"arn:ca/kb-2", "ca-kb-2"}}},
	{TypeWisdomAIAgentVersion, wisdomWantRow{}, []wisdomWantRow{{"arn:agent/a-1:1", "v1"}, {"arn:agent/a-2:1", "v1"}}},
	{TypeWisdomAIGuardrailVersion, wisdomWantRow{}, []wisdomWantRow{{"arn:guardrail/a-1:1", "v1"}, {"arn:guardrail/a-2:1", "v1"}}},
	{TypeWisdomAIPromptVersion, wisdomWantRow{}, []wisdomWantRow{{"arn:prompt/a-1:1", "v1"}, {"arn:prompt/a-2:1", "v1"}}},
	{TypeWisdomMessageTemplateVersion, wisdomWantRow{}, []wisdomWantRow{{"arn:mt/kb-1:1", "name-mtv-kb-1"}, {"arn:mt/kb-2:1", "v1"}}},
}

// TestScanWisdomAll_StoresTags pins, for every type whose listed element
// carries Tags, that the stored row's TagsJSON is that element's tags and that
// an untagged element stores none; the version types store none. It also pins
// each type's NativeID and Name.
func TestScanWisdomAll_StoresTags(t *testing.T) {
	st := newTestStore(t)
	total, err := wisdomScanAll(t, st, wisdomAllHandlers(t))
	if err != nil {
		t.Fatalf("scanWisdomAll: %v", err)
	}
	if total != 28 {
		t.Errorf("total = %d, want 28", total)
	}
	for _, c := range wisdomAllRows {
		t.Run(c.typ, func(t *testing.T) {
			rows := rowsByNativeID(t, st, c.typ)
			want := len(c.untagged)
			if c.tagged.nativeID != "" {
				want++
				r, ok := rows[c.tagged.nativeID]
				if !ok {
					t.Fatalf("%s not stored; got %d rows", c.tagged.nativeID, len(rows))
				}
				if got := sv(r.Name); got != c.tagged.name {
					t.Errorf("%s Name = %q, want %q", c.tagged.nativeID, got, c.tagged.name)
				}
				assertTagsMap(t, r.TagsJSON, wisdomTestTags)
			}
			if len(rows) != want {
				t.Errorf("stored %d rows, want %d", len(rows), want)
			}
			for _, u := range c.untagged {
				r, ok := rows[u.nativeID]
				if !ok {
					t.Fatalf("%s not stored; got %d rows", u.nativeID, len(rows))
				}
				if got := sv(r.Name); got != u.name {
					t.Errorf("%s Name = %q, want %q", u.nativeID, got, u.name)
				}
				if r.TagsJSON != nil {
					t.Errorf("%s TagsJSON = %s, want nil", u.nativeID, *r.TagsJSON)
				}
			}
		})
	}
}

// wisdomPerParentOps maps each per-parent listing to the type it stores and
// the NativeID of the row it stores for the second parent (a-2 or kb-2), whose
// call never fails in the tests below. A call naming a-1 or kb-1 is the first
// parent's.
var wisdomPerParentOps = map[string]struct{ typ, sibling string }{
	"ListAssistantAssociations":   {TypeWisdomAssistantAssociation, "arn:assoc/a-2"},
	"ListAIAgents":                {TypeWisdomAIAgent, "arn:agent/a-2"},
	"ListAIGuardrails":            {TypeWisdomAIGuardrail, "arn:guardrail/a-2"},
	"ListAIPrompts":               {TypeWisdomAIPrompt, "arn:prompt/a-2"},
	"ListMessageTemplates":        {TypeWisdomMessageTemplate, "arn:mt/kb-2"},
	"ListContents":                {TypeWisdomContent, "arn:content/kb-2"},
	"ListQuickResponses":          {TypeWisdomQuickResponse, "arn:qr/kb-2"},
	"ListAIAgentVersions":         {TypeWisdomAIAgentVersion, "arn:agent/a-2:1"},
	"ListAIGuardrailVersions":     {TypeWisdomAIGuardrailVersion, "arn:guardrail/a-2:1"},
	"ListAIPromptVersions":        {TypeWisdomAIPromptVersion, "arn:prompt/a-2:1"},
	"ListMessageTemplateVersions": {TypeWisdomMessageTemplateVersion, "arn:mt/kb-2:1"},
	"ListContentAssociations":     {TypeWisdomContentAssociation, "arn:ca/kb-2"},
}

// wisdomFirstParent reports whether a per-parent call names a-1 or kb-1.
func wisdomFirstParent(in any) bool {
	req := reflect.ValueOf(in).Elem()
	for _, f := range []string{"AssistantId", "KnowledgeBaseId"} {
		if v := req.FieldByName(f); v.IsValid() {
			p := sv(v.Interface().(*string))
			return p == "a-1" || p == "kb-1"
		}
	}
	return false
}

// TestScanWisdomAll_PerParentOutcomes pins, for every per-parent listing, what
// a failure on the first parent does: ResourceNotFoundException, which each
// op models for a parent deleted since it was listed, skips it silently; a
// denial warns once with the account/region scope; any other error is
// returned. The second parent's row is stored in every case.
func TestScanWisdomAll_PerParentOutcomes(t *testing.T) {
	hard := apiErr("ValidationException", "bad")
	for op, want := range wisdomPerParentOps {
		for _, c := range []struct {
			name      string
			err       error
			wantErr   bool
			wantWarns int
		}{
			{"not found is silent", apiErr("ResourceNotFoundException", "gone"), false, 0},
			{"denied warns", apiErr("AccessDeniedException", "not authorized to perform: wisdom:"+op), false, 1},
			{"other error returned", hard, true, 0},
		} {
			t.Run(op+"/"+c.name, func(t *testing.T) {
				h := wisdomOnePageHandlers(t)
				serve := h[op]
				h[op] = func(in any) (any, error) {
					if wisdomFirstParent(in) {
						return nil, c.err
					}
					return serve(in)
				}
				for o, f := range h {
					h[o] = wisdomTwoPages(t, o, f)
				}
				st := newTestStore(t)
				var scopes []string
				st.OnWarn = func(w store.ScanWarning) {
					if w.Service == "qconnect:"+op {
						scopes = append(scopes, w.Scope)
					}
				}
				_, err := wisdomScanAll(t, st, h)
				if c.wantErr && !errors.Is(err, hard) {
					t.Fatalf("err = %v, want the ValidationException", err)
				}
				if !c.wantErr && err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				rows := rowsByNativeID(t, st, want.typ)
				if _, ok := rows[want.sibling]; !ok {
					t.Errorf("sibling %s not stored", want.sibling)
				}
				if len(rows) != 1 {
					t.Errorf("stored %d %s rows, want 1 (the sibling's)", len(rows), want.typ)
				}
				if len(scopes) != c.wantWarns {
					t.Errorf("warnings = %d, want %d", len(scopes), c.wantWarns)
				}
				for _, s := range scopes {
					if w := testAccountID + "/" + wisdomTestRegion; s != w {
						t.Errorf("warning scope = %q, want %q", s, w)
					}
				}
			})
		}
	}
}

// TestScanWisdomAll_AccountListingDeniedWarns pins that a denied assistant or
// knowledge-base listing warns once with the account/region scope instead of
// failing, and the other phases are stored.
func TestScanWisdomAll_AccountListingDeniedWarns(t *testing.T) {
	for _, c := range []struct{ op, other string }{
		{"ListAssistants", TypeWisdomKnowledgeBase},
		{"ListKnowledgeBases", TypeWisdomAssistant},
	} {
		t.Run(c.op, func(t *testing.T) {
			h := wisdomAllHandlers(t)
			h[c.op] = func(any) (any, error) {
				return nil, apiErr("AccessDeniedException", "not authorized to perform: wisdom:"+c.op)
			}
			st := newTestStore(t)
			var scopes []string
			st.OnWarn = func(w store.ScanWarning) {
				if w.Service == "qconnect:"+c.op {
					scopes = append(scopes, w.Scope)
				}
			}
			if _, err := wisdomScanAll(t, st, h); err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if len(scopes) != 1 || scopes[0] != testAccountID+"/"+wisdomTestRegion {
				t.Errorf("warning scopes = %v, want one %s/%s", scopes, testAccountID, wisdomTestRegion)
			}
			if n := len(rowsByNativeID(t, st, c.other)); n != 2 {
				t.Errorf("stored %d %s, want 2", n, c.other)
			}
		})
	}
}

// TestScanWisdomAll_LaterPageFailureKeepsRows pins, for every listing, that a
// failure on the page after the first returns the error and keeps the rows
// the first page read.
func TestScanWisdomAll_LaterPageFailureKeepsRows(t *testing.T) {
	hard := apiErr("ValidationException", "bad")
	byOp := map[string]string{"ListAssistants": TypeWisdomAssistant, "ListKnowledgeBases": TypeWisdomKnowledgeBase}
	for op, w := range wisdomPerParentOps {
		byOp[op] = w.typ
	}
	for op, typ := range byOp {
		t.Run(op, func(t *testing.T) {
			h := wisdomAllHandlers(t)
			serve := h[op]
			h[op] = func(in any) (any, error) {
				if sv(reflect.ValueOf(in).Elem().FieldByName("NextToken").Interface().(*string)) != "" {
					return nil, hard
				}
				return serve(in)
			}
			st := newTestStore(t)
			if _, err := wisdomScanAll(t, st, h); !errors.Is(err, hard) {
				t.Fatalf("err = %v, want the ValidationException", err)
			}
			if n := len(rowsByNativeID(t, st, typ)); n != 2 {
				t.Errorf("stored %d %s, want the 2 the first pages read", n, typ)
			}
		})
	}
}

// wisdomListParent is the parent a listing call names (AssistantId or
// KnowledgeBaseId), or "" for an account-wide listing.
func wisdomListParent(in any) string {
	req := reflect.ValueOf(in).Elem()
	for _, f := range []string{"AssistantId", "KnowledgeBaseId"} {
		if v := req.FieldByName(f); v.IsValid() {
			return sv(v.Interface().(*string))
		}
	}
	return ""
}

// TestScanWisdomAll_ParentWithoutIDStoredAndWarned pins that a parent listed
// with an ARN but no id is stored with a warning, and that no child listing is
// called for it; one listed with no ARN is warned about and not stored.
func TestScanWisdomAll_ParentWithoutIDStoredAndWarned(t *testing.T) {
	for _, c := range []struct {
		op       string
		typ      string
		children []string
		serve    func(p string) any
	}{
		{"ListAssistants", TypeWisdomAssistant, []string{"ListAssistantAssociations", "ListAIAgents", "ListAIGuardrails", "ListAIPrompts"}, func(p string) any {
			return &qconnect.ListAssistantsOutput{AssistantSummaries: []qconnecttypes.AssistantSummary{{AssistantArn: ptrStr("arn:assistant/x")}, {AssistantId: ptrStr("a-1")}}}
		}},
		{"ListKnowledgeBases", TypeWisdomKnowledgeBase, []string{"ListMessageTemplates", "ListContents", "ListQuickResponses"}, func(p string) any {
			return &qconnect.ListKnowledgeBasesOutput{KnowledgeBaseSummaries: []qconnecttypes.KnowledgeBaseSummary{{KnowledgeBaseArn: ptrStr("arn:kb/x")}, {KnowledgeBaseId: ptrStr("kb-1")}}}
		}},
		{"ListAIAgents", TypeWisdomAIAgent, []string{"ListAIAgentVersions"}, func(p string) any {
			return &qconnect.ListAIAgentsOutput{AiAgentSummaries: []qconnecttypes.AIAgentSummary{{AiAgentArn: ptrStr("arn:agent/" + p)}, {AiAgentId: ptrStr("agent-" + p)}}}
		}},
		{"ListAIGuardrails", TypeWisdomAIGuardrail, []string{"ListAIGuardrailVersions"}, func(p string) any {
			return &qconnect.ListAIGuardrailsOutput{AiGuardrailSummaries: []qconnecttypes.AIGuardrailSummary{{AiGuardrailArn: ptrStr("arn:guardrail/" + p)}, {AiGuardrailId: ptrStr("guardrail-" + p)}}}
		}},
		{"ListAIPrompts", TypeWisdomAIPrompt, []string{"ListAIPromptVersions"}, func(p string) any {
			return &qconnect.ListAIPromptsOutput{AiPromptSummaries: []qconnecttypes.AIPromptSummary{{AiPromptArn: ptrStr("arn:prompt/" + p)}, {AiPromptId: ptrStr("prompt-" + p)}}}
		}},
		{"ListMessageTemplates", TypeWisdomMessageTemplate, []string{"ListMessageTemplateVersions"}, func(p string) any {
			return &qconnect.ListMessageTemplatesOutput{MessageTemplateSummaries: []qconnecttypes.MessageTemplateSummary{{MessageTemplateArn: ptrStr("arn:mt/" + p)}, {MessageTemplateId: ptrStr("mt-" + p)}}}
		}},
		{"ListContents", TypeWisdomContent, []string{"ListContentAssociations"}, func(p string) any {
			return &qconnect.ListContentsOutput{ContentSummaries: []qconnecttypes.ContentSummary{{ContentArn: ptrStr("arn:content/" + p)}, {ContentId: ptrStr("content-" + p)}}}
		}},
	} {
		t.Run(c.op, func(t *testing.T) {
			h := wisdomAllHandlers(t)
			h[c.op] = wisdomTwoPages(t, c.op, func(in any) (any, error) { return c.serve(wisdomListParent(in)), nil })
			for _, op := range c.children {
				h[op] = func(any) (any, error) {
					t.Errorf("%s called; want no child listing for a parent without an id", op)
					return nil, errors.New("unexpected")
				}
			}
			st := newTestStore(t)
			warns := countOpWarnings(st, "qconnect:"+c.op)
			if _, err := wisdomScanAll(t, st, h); err != nil {
				t.Fatalf("scanWisdomAll: %v", err)
			}
			wantRows := 2 // one per assistant or knowledge base
			if c.op == "ListAssistants" || c.op == "ListKnowledgeBases" {
				wantRows = 1
			}
			if n := len(rowsByNativeID(t, st, c.typ)); n != wantRows {
				t.Errorf("stored %d %s rows, want %d (those with an ARN)", n, c.typ, wantRows)
			}
			if *warns != 2 {
				t.Errorf("warnings = %d, want 2 (no id, no ARN)", *warns)
			}
		})
	}
}

// TestScanWisdomAll_LeafWithoutARNWarned pins that an assistant association,
// quick response or content association listed without an ARN is warned
// about once and not stored, while the ones with an ARN are.
func TestScanWisdomAll_LeafWithoutARNWarned(t *testing.T) {
	for _, c := range []struct {
		op    string
		typ   string
		serve func(in any) (any, error)
	}{
		{"ListAssistantAssociations", TypeWisdomAssistantAssociation, func(in any) (any, error) {
			p := wisdomListParent(in)
			return &qconnect.ListAssistantAssociationsOutput{AssistantAssociationSummaries: []qconnecttypes.AssistantAssociationSummary{
				{AssistantAssociationArn: ptrStr("arn:assoc/" + p)}, {AssistantAssociationId: ptrStr("assoc-x")},
			}}, nil
		}},
		{"ListQuickResponses", TypeWisdomQuickResponse, func(in any) (any, error) {
			p := wisdomListParent(in)
			return &qconnect.ListQuickResponsesOutput{QuickResponseSummaries: []qconnecttypes.QuickResponseSummary{
				{QuickResponseArn: ptrStr("arn:qr/" + p)}, {QuickResponseId: ptrStr("qr-x")},
			}}, nil
		}},
		{"ListContentAssociations", TypeWisdomContentAssociation, func(in any) (any, error) {
			p := wisdomListParent(in)
			return &qconnect.ListContentAssociationsOutput{ContentAssociationSummaries: []qconnecttypes.ContentAssociationSummary{
				{ContentAssociationArn: ptrStr("arn:ca/" + p)}, {ContentAssociationId: ptrStr("ca-x")},
			}}, nil
		}},
	} {
		t.Run(c.op, func(t *testing.T) {
			h := wisdomAllHandlers(t)
			h[c.op] = wisdomTwoPages(t, c.op, c.serve)
			st := newTestStore(t)
			warns := countOpWarnings(st, "qconnect:"+c.op)
			if _, err := wisdomScanAll(t, st, h); err != nil {
				t.Fatalf("scanWisdomAll: %v", err)
			}
			if n := len(rowsByNativeID(t, st, c.typ)); n != 2 {
				t.Errorf("stored %d %s rows, want 2 (one per parent, with an ARN)", n, c.typ)
			}
			if *warns != 1 {
				t.Errorf("warnings = %d, want 1 (no ARN)", *warns)
			}
		})
	}
}

// TestScanWisdomAll_VersionWithoutIdentityWarned pins that a version listed
// without the ARN or the VersionNumber its NativeID is built from is warned
// about and not stored, while its siblings are.
func TestScanWisdomAll_VersionWithoutIdentityWarned(t *testing.T) {
	agentARN := func(in any) *string {
		return ptrStr("arn:agent/" + sv(in.(*qconnect.ListAIAgentVersionsInput).AssistantId))
	}
	for _, c := range []struct {
		op    string
		typ   string
		serve func(in any) (any, error)
	}{
		{"ListAIAgentVersions", TypeWisdomAIAgentVersion, func(in any) (any, error) {
			return &qconnect.ListAIAgentVersionsOutput{AiAgentVersionSummaries: []qconnecttypes.AIAgentVersionSummary{
				{AiAgentSummary: &qconnecttypes.AIAgentSummary{AiAgentArn: agentARN(in)}, VersionNumber: sdkInt64(1)},
				{AiAgentSummary: &qconnecttypes.AIAgentSummary{AiAgentArn: agentARN(in)}},
				{VersionNumber: sdkInt64(2)},
			}}, nil
		}},
		{"ListAIGuardrailVersions", TypeWisdomAIGuardrailVersion, func(in any) (any, error) {
			a := ptrStr("arn:guardrail/" + sv(in.(*qconnect.ListAIGuardrailVersionsInput).AssistantId))
			return &qconnect.ListAIGuardrailVersionsOutput{AiGuardrailVersionSummaries: []qconnecttypes.AIGuardrailVersionSummary{
				{AiGuardrailSummary: &qconnecttypes.AIGuardrailSummary{AiGuardrailArn: a}, VersionNumber: sdkInt64(1)},
				{AiGuardrailSummary: &qconnecttypes.AIGuardrailSummary{AiGuardrailArn: a}},
				{VersionNumber: sdkInt64(2)},
			}}, nil
		}},
		{"ListAIPromptVersions", TypeWisdomAIPromptVersion, func(in any) (any, error) {
			a := ptrStr("arn:prompt/" + sv(in.(*qconnect.ListAIPromptVersionsInput).AssistantId))
			return &qconnect.ListAIPromptVersionsOutput{AiPromptVersionSummaries: []qconnecttypes.AIPromptVersionSummary{
				{AiPromptSummary: &qconnecttypes.AIPromptSummary{AiPromptArn: a}, VersionNumber: sdkInt64(1)},
				{AiPromptSummary: &qconnecttypes.AIPromptSummary{AiPromptArn: a}},
				{VersionNumber: sdkInt64(2)},
			}}, nil
		}},
		{"ListMessageTemplateVersions", TypeWisdomMessageTemplateVersion, func(in any) (any, error) {
			a := ptrStr("arn:mt/" + sv(in.(*qconnect.ListMessageTemplateVersionsInput).KnowledgeBaseId))
			return &qconnect.ListMessageTemplateVersionsOutput{MessageTemplateVersionSummaries: []qconnecttypes.MessageTemplateVersionSummary{
				{MessageTemplateArn: a, VersionNumber: sdkInt64(1)},
				{MessageTemplateArn: a},
				{VersionNumber: sdkInt64(2)},
			}}, nil
		}},
	} {
		t.Run(c.op, func(t *testing.T) {
			h := wisdomAllHandlers(t)
			h[c.op] = wisdomTwoPages(t, c.op, c.serve)
			st := newTestStore(t)
			warns := countOpWarnings(st, "qconnect:"+c.op)
			if _, err := wisdomScanAll(t, st, h); err != nil {
				t.Fatalf("scanWisdomAll: %v", err)
			}
			if n := len(rowsByNativeID(t, st, c.typ)); n != 2 {
				t.Errorf("stored %d %s rows, want 2 (version 1 of each parent)", n, c.typ)
			}
			if *warns != 2 {
				t.Errorf("warnings = %d, want 2 (no ARN, no VersionNumber)", *warns)
			}
		})
	}
}

// TestScanWisdomAll_NoChildIDWarnsOnlyAfterWrite pins that a parent stored
// without an id is not warned about when the write failed, since the warning
// would claim it was stored.
func TestScanWisdomAll_NoChildIDWarnsOnlyAfterWrite(t *testing.T) {
	for _, c := range []struct {
		op    string
		serve func(in any) (any, error)
	}{
		{"ListAssistants", func(any) (any, error) {
			return &qconnect.ListAssistantsOutput{AssistantSummaries: []qconnecttypes.AssistantSummary{{AssistantArn: ptrStr("arn:assistant/x")}}}, nil
		}},
		{"ListKnowledgeBases", func(any) (any, error) {
			return &qconnect.ListKnowledgeBasesOutput{KnowledgeBaseSummaries: []qconnecttypes.KnowledgeBaseSummary{{KnowledgeBaseArn: ptrStr("arn:kb/x")}}}, nil
		}},
		{"ListAIAgents", func(in any) (any, error) {
			return &qconnect.ListAIAgentsOutput{AiAgentSummaries: []qconnecttypes.AIAgentSummary{{AiAgentArn: ptrStr("arn:agent/" + wisdomListParent(in))}}}, nil
		}},
		{"ListAIGuardrails", func(in any) (any, error) {
			return &qconnect.ListAIGuardrailsOutput{AiGuardrailSummaries: []qconnecttypes.AIGuardrailSummary{{AiGuardrailArn: ptrStr("arn:guardrail/" + wisdomListParent(in))}}}, nil
		}},
		{"ListAIPrompts", func(in any) (any, error) {
			return &qconnect.ListAIPromptsOutput{AiPromptSummaries: []qconnecttypes.AIPromptSummary{{AiPromptArn: ptrStr("arn:prompt/" + wisdomListParent(in))}}}, nil
		}},
		{"ListMessageTemplates", func(in any) (any, error) {
			return &qconnect.ListMessageTemplatesOutput{MessageTemplateSummaries: []qconnecttypes.MessageTemplateSummary{{MessageTemplateArn: ptrStr("arn:mt/" + wisdomListParent(in))}}}, nil
		}},
		{"ListContents", func(in any) (any, error) {
			return &qconnect.ListContentsOutput{ContentSummaries: []qconnecttypes.ContentSummary{{ContentArn: ptrStr("arn:content/" + wisdomListParent(in))}}}, nil
		}},
	} {
		t.Run(c.op, func(t *testing.T) {
			st, closeStore := openClosingStore(t)
			warns := countOpWarnings(st, "qconnect:"+c.op)
			h := wisdomAllHandlers(t)
			h[c.op] = func(in any) (any, error) {
				closeStore()
				return c.serve(in)
			}
			if _, err := wisdomScanAll(t, st, h); !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v, want ErrStoreWrite", err)
			}
			if *warns != 0 {
				t.Errorf("warnings = %d, want 0", *warns)
			}
		})
	}
}

// TestScanWisdomAll_PhaseErrorRunsLaterPhases pins that a failed listing does
// not stop the phases after it: its error is returned and every independent
// phase's rows are stored.
func TestScanWisdomAll_PhaseErrorRunsLaterPhases(t *testing.T) {
	hard := apiErr("ValidationException", "bad")
	for _, c := range []struct {
		failOp   string
		stillHas []string
	}{
		{"ListAssistants", []string{TypeWisdomKnowledgeBase, TypeWisdomMessageTemplateVersion, TypeWisdomContentAssociation, TypeWisdomQuickResponse}},
		{"ListKnowledgeBases", []string{TypeWisdomAIAgent, TypeWisdomAIPromptVersion, TypeWisdomAssistantAssociation}},
		{"ListAIAgents", []string{TypeWisdomAIGuardrail, TypeWisdomAIPrompt, TypeWisdomContent, TypeWisdomAIPromptVersion, TypeWisdomQuickResponse}},
		{"ListContents", []string{TypeWisdomAssistantAssociation, TypeWisdomAIAgentVersion, TypeWisdomQuickResponse}},
		{"ListContentAssociations", []string{TypeWisdomAssistantAssociation, TypeWisdomMessageTemplateVersion, TypeWisdomQuickResponse}},
		{"ListMessageTemplateVersions", []string{TypeWisdomQuickResponse}},
	} {
		t.Run(c.failOp, func(t *testing.T) {
			h := wisdomAllHandlers(t)
			h[c.failOp] = func(any) (any, error) { return nil, hard }
			st := newTestStore(t)
			if _, err := wisdomScanAll(t, st, h); !errors.Is(err, hard) {
				t.Fatalf("err = %v, want the %s error", err, c.failOp)
			}
			for _, typ := range c.stillHas {
				if n := len(rowsByNativeID(t, st, typ)); n != 2 {
					t.Errorf("stored %d %s after %s failed, want 2", n, typ, c.failOp)
				}
			}
		})
	}
}

// TestScanWisdomAll_StoreWriteStops pins that a failed write stops the scan,
// whichever phase it hits: no phase after it calls AWS.
func TestScanWisdomAll_StoreWriteStops(t *testing.T) {
	for _, c := range []struct {
		closeIn  string
		notAfter []string
	}{
		{"ListAssistants", []string{"ListKnowledgeBases", "ListQuickResponses"}},
		{"ListKnowledgeBases", []string{"ListAIAgents", "ListQuickResponses"}},
		{"ListAIAgents", []string{"ListAIGuardrails", "ListQuickResponses"}},
		{"ListAIGuardrails", []string{"ListAIPrompts", "ListQuickResponses"}},
		{"ListAIPrompts", []string{"ListMessageTemplates", "ListQuickResponses"}},
		{"ListMessageTemplates", []string{"ListContents", "ListQuickResponses"}},
		{"ListContents", []string{"ListContentAssociations", "ListQuickResponses"}},
		{"ListContentAssociations", []string{"ListAssistantAssociations", "ListQuickResponses"}},
	} {
		t.Run(c.closeIn, func(t *testing.T) {
			var calls sync.Map
			h := wisdomAllHandlers(t)
			for op, f := range h {
				h[op] = func(in any) (any, error) {
					calls.Store(op, true)
					return f(in)
				}
			}
			st, closeStore := openClosingStore(t)
			serve := h[c.closeIn]
			h[c.closeIn] = func(in any) (any, error) {
				closeStore()
				return serve(in)
			}
			if _, err := wisdomScanAll(t, st, h); !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v, want ErrStoreWrite", err)
			}
			for _, op := range c.notAfter {
				if _, called := calls.Load(op); called {
					t.Errorf("%s called after the failed write, want not", op)
				}
			}
		})
	}
}

// TestScanWisdomAll_TransientBesideHardError pins that a throttled phase
// beside a hard-failing one is demoted to one warning and the scan classified
// as an error, not as transient.
func TestScanWisdomAll_TransientBesideHardError(t *testing.T) {
	h := wisdomAllHandlers(t)
	h["ListAssistants"] = func(any) (any, error) { return nil, apiErr("ThrottlingException", "slow down") }
	h["ListQuickResponses"] = func(any) (any, error) { return nil, apiErr("ValidationException", "bad") }
	st := newTestStore(t)
	warns := countOpWarnings(st, wisdomListAssistantsOp)
	_, err := wisdomScanAll(t, st, h)
	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError(%v) = %v, want outcomeError", err, got)
	}
	if *warns != 1 {
		t.Errorf("transient warnings = %d, want 1", *warns)
	}
}
