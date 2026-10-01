package awsinventory

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"sort"
	"strings"

	"github.com/icearp/disco-cli/internal/sdkinv"
)

// opFacts is everything the model and the catalog state about one operation.
type opFacts struct {
	id, name string
	inputs   []string // every input member, required or not
	idList   bool     // the output lists primitives under an id name (Names, FleetArns)
	noun     string   // the operation name after its first word, version suffix dropped
	bind     binding
	node     *resNode
	required []string
	paged    bool
	read     bool // readonly trait, a GET or HEAD, or a resource read/list binding
	traited  bool // any read, paging or REST trait at all
	uri      string
	iam      string
	elems    []string // the output's collection element shape ids
	elem     string   // the listed element: the paginated items, the sole or the written one
	items    bool     // elem came from the paginated items path
	wrapped  bool     // the collection sits inside a single payload structure
	// listShaped: every non-primitive member of the output is a collection. A
	// read that also answers structures (GetFunctionConfiguration's VpcConfig
	// beside its Layers) describes one thing, whatever lists it carries.
	listShaped bool
	hasIDs     bool
	elemIDs    []string // id-like member names across the elements
	elemARN    bool
	elemTime   bool
	subject    string // a non-list read's sole structure member, the thing it describes
	act        srAction
	hasAct     bool
	signals    map[string]bool
	// Decided once the whole model is read.
	isList   bool
	evidence string
	lin      place
	ident    string
	disp     string
}

func (f *opFacts) coll() bool    { return len(f.elems) > 0 }
func (f *opFacts) readish() bool { return f.read || (f.hasAct && !f.act.isWrite) }

// writeish is an operation known to mutate: the catalog says so, or the model
// traits it and does not mark it a read. An operation with neither is unknown
// and writes nothing as evidence.
func (f *opFacts) writeish() bool {
	if f.hasAct {
		return f.act.isWrite
	}
	return f.traited && !f.read
}
func (f *opFacts) bound() bool { return f.node != nil && f.bind.role != roleInstance }

// universe accumulates entries across every model file.
type universe struct {
	entries map[string]*entry
	// catalogued is every catalog resource identity per service: a required
	// id naming neither an entry nor one of these is a handle, not a parent.
	catalogued map[string]map[string]bool
}

// Extract derives the universe model-first: Smithy resource bindings,
// paginated items, URI labels and the Service Reference decide; where the SDK
// states nothing, a structural rule decides and says so in the candidate's
// signals.
func (extractor) Extract(_ context.Context, dir string) (*sdkinv.Universe, error) {
	u := &sdkinv.Universe{Provider: "aws", Pins: map[string]string{"aws-sdk-go-v2": SDKRef}}
	sr, srVersion, err := loadServiceReference(filepath.Join(dir, "service-reference"))
	if err != nil {
		return nil, err
	}
	u.Pins["service-reference"] = srVersion
	if srVersion != ServiceReferenceDigest {
		u.Diagnostics = append(u.Diagnostics, sdkinv.Diagnostic{
			Severity: "warn", Source: "service-reference/index.json",
			Message: fmt.Sprintf("catalog digest %s does not match the pin %s in awsinventory/pins.go; the numbers are from the cached catalog", srVersion, ServiceReferenceDigest),
		})
	}
	modelDir := filepath.Join(dir, "repo", smithyModelsDir)
	files, err := filepath.Glob(filepath.Join(modelDir, "*.json"))
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no Smithy models under %s", modelDir)
	}
	x := &universe{entries: map[string]*entry{}, catalogued: map[string]map[string]bool{}}
	aliases := map[string]map[string]bool{}
	for _, f := range files {
		raw, rerr := os.ReadFile(f)
		if rerr != nil {
			return nil, rerr
		}
		var m smithyModel
		if jerr := json.Unmarshal(raw, &m); jerr != nil {
			u.Diagnostics = append(u.Diagnostics, sdkinv.Diagnostic{Severity: "warn", Source: filepath.Base(f), Message: jerr.Error()})
			continue
		}
		base := filepath.Base(f)
		sm := newServiceModel(&m)
		module := modelModule(base)
		svc, _ := serviceKey(&m)
		reached := map[string]bool{}
		for id := range sm.ops {
			reached[shapeName(id)] = true
		}
		for name := range modelOps(&m) {
			u.SourceOps = append(u.SourceOps, sdkinv.OpRef{Module: module, Name: name})
			switch {
			case svc == "": // indexModel keys nothing without a service name
				u.Dropped = append(u.Dropped, sdkinv.Drop{Op: sdkinv.Operation{Name: name, Label: ":" + name, Module: module}, Reason: "no-service-name"})
			case !reached[name]:
				u.Dropped = append(u.Dropped, sdkinv.Drop{Op: sdkinv.Operation{Service: svc, Name: name, Label: svc + ":" + name, Module: module}, Reason: "unreachable-from-service"})
			}
		}
		collectServiceAliases(aliases, &m)
		diags, other := x.indexModel(sm, sr, base, reached)
		for _, d := range diags {
			u.Diagnostics = append(u.Diagnostics, sdkinv.Diagnostic{Severity: "warn", Source: base, Message: d})
		}
		u.Other = append(u.Other, other...)
	}
	u.Diagnostics = append(u.Diagnostics, absentModels(files, sdkinv.LinkedModules())...)
	mergeDetailReads(x.entries)
	resolveTree(x.entries, x.catalogued)
	u.Candidates = assemble(x.entries)
	u.ServiceAliases = serviceAliases(aliases, u.Candidates)
	sdkinv.SortCandidates(u.Candidates)
	sdkinv.SortOps(u.Other)
	sdkinv.SortOpRefs(u.SourceOps)
	sdkinv.SortDrops(u.Dropped)
	return u, nil
}

// joinSR finds the catalog document for a model: by signing name, else by the
// SDK client name the catalog publishes its operations under, else by the
// smallest document authorising every operation.
func joinSR(srAll map[string]*srService, svc, sdkID string, ops map[string]bool) (*srService, string) {
	if sr := srAll[svc]; sr != nil {
		return sr, ""
	}
	client := strings.ToLower(strings.ReplaceAll(sdkID, " ", "-"))
	for _, name := range slices.Sorted(maps.Keys(srAll)) {
		if srAll[name].sdkNames[client] {
			return srAll[name], fmt.Sprintf("service %q absent from the Service Reference; joined to %q by SDK client name", svc, name)
		}
	}
	if sr := matchSRByOps(srAll, ops); sr != nil {
		return sr, fmt.Sprintf("service %q absent from the Service Reference; joined to %q by operation names", svc, sr.name)
	}
	return nil, fmt.Sprintf("service %q absent from the Service Reference; classification falls back to SDK shape", svc)
}

// bindAction is the catalog action an operation is authorised by: the model's
// own iamAction, else the union of every own-service action authorising it —
// a listing if any lists, a write if any writes.
func (s *srService) bindAction(op, iam string) (srAction, bool, string) {
	if a, ok := s.actions[iam]; iam != "" && ok {
		return a, true, "sr:iam-action"
	}
	names := s.opActions[op]
	if len(names) == 0 {
		if a, ok := s.actions[op]; ok {
			return a, true, "sr:action-name"
		}
		return srAction{}, false, "sr:unbound"
	}
	u, found := srAction{taggingOnly: true}, false
	for _, n := range names {
		a, ok := s.actions[n]
		if !ok {
			continue
		}
		found = true
		u.isList = u.isList || a.isList
		u.isWrite = u.isWrite || a.isWrite // an operation any write authorises mutates
		u.taggingOnly = u.taggingOnly && a.taggingOnly
		for _, r := range a.resources {
			if !slices.Contains(u.resources, r) {
				u.resources = append(u.resources, r)
			}
		}
	}
	if !found {
		return srAction{}, false, "sr:unbound"
	}
	sort.Strings(u.resources)
	return u, true, ""
}

// opNoun is an operation name after its first word, version suffix dropped:
// "ListObjectsV2" → "Objects". The noun names the candidate only when nothing
// in the model does.
func opNoun(op string) string {
	i := 1
	for i < len(op) && op[i] >= 'a' && op[i] <= 'z' {
		i++
	}
	return versionRe.ReplaceAllString(op[i:], "")
}

// cutQualifier drops a trailing qualifier that names one of the operation's
// input members: the subject is everything before the leftmost short joining
// word whose remainder the member's name ends with, or ends ("TagsForResource"
// given ResourceArn → "Tags", "ResourcesForTagOption" given TagOptionId →
// "Resources", "SourcesForS3TableIntegration" given IntegrationArn →
// "Sources"). The joining word is recognised by shape alone, a capitalised
// word of at most three lowercase-tailed letters, so no preposition list is
// needed. A name word that fits the shape (Vpc, Key, Set) is not cut when the
// matching input names the subject itself.
func cutQualifier(noun string, inputs []string) string {
	words := camelWords(noun)
	for c := 1; c < len(words)-1; c++ {
		if !isJoiner(words[c]) {
			continue
		}
		rest := sdkinv.Ident(strings.Join(words[c+1:], ""))
		subject := sdkinv.Ident(strings.Join(words[:c], ""))
		for _, in := range inputs {
			stem := memberStem(in)
			// An input that opens with the subject is the subject's own id
			// (ClientVpnEndpointIds for ClientVpnEndpoints): "Vpn" there is a
			// word of the name, not a joiner.
			if stem == "" || strings.HasPrefix(stem, subject) {
				continue
			}
			if strings.HasSuffix(rest, stem) || strings.HasSuffix(stem, rest) {
				return strings.Join(words[:c], "")
			}
		}
	}
	return noun
}

func isJoiner(w string) bool {
	if len(w) > 3 || !isUpper(w[0]) {
		return false
	}
	for i := 1; i < len(w); i++ {
		if w[i] < 'a' || w[i] > 'z' {
			return false
		}
	}
	return true
}

// camelWords splits a PascalCase name, keeping an acronym whole ("HostedZonesByVPC"
// → Hosted Zones By VPC, "DBClusterId" → DB Cluster Id).
func camelWords(s string) []string {
	var words []string
	start := 0
	for i := 1; i < len(s); i++ {
		up, prevUp := isUpper(s[i]), isUpper(s[i-1])
		nextLow := i+1 < len(s) && s[i+1] >= 'a' && s[i+1] <= 'z'
		// A plural acronym keeps its "s": HITsFor → HITs For, not HI Ts For.
		if nextLow && prevUp && s[i+1] == 's' && (i+2 == len(s) || isUpper(s[i+2])) {
			nextLow = false
		}
		if up && (!prevUp || nextLow) {
			words = append(words, s[start:i])
			start = i
		}
	}
	if start < len(s) {
		words = append(words, s[start:])
	}
	return words
}

func isUpper(c byte) bool { return c >= 'A' && c <= 'Z' }

func (x *universe) facts(sm *serviceModel, id string, sr *srService) *opFacts {
	sh := sm.m.Shapes[id]
	f := &opFacts{id: id, name: shapeName(id), bind: sm.ops[id], signals: map[string]bool{}}
	if sh == nil { // a binding to a shape the file lacks: nothing to read
		f.noun = opNoun(f.name)
		return f
	}
	f.noun = opNoun(f.name)
	f.node = sm.res[f.bind.res]
	pag, paged := sm.pagination(sh)
	f.paged = paged
	var h httpTrait
	if decodeTrait(sh.Traits, "smithy.api#http", &h) {
		f.uri = h.URI
	}
	_, ro := sh.Traits["smithy.api#readonly"]
	f.read = ro || h.Method == "GET" || h.Method == "HEAD" || f.bind.role == "read" || f.bind.role == "list"
	f.traited = ro || h.Method != "" || paged
	var iam struct {
		Name string `json:"name"`
	}
	decodeTrait(sh.Traits, "aws.iam#iamAction", &iam)
	f.iam = iam.Name
	f.readInputs(sm, sh, pag)
	if cut := cutQualifier(f.noun, f.inputs); cut != f.noun {
		f.noun = cut
		f.signals["noun:qualifier-cut"] = true
	}
	f.rebind()
	if sr != nil {
		var sig string
		f.act, f.hasAct, sig = sr.bindAction(f.name, f.iam)
		if sig != "" {
			f.signals[sig] = true
		}
	}
	if sh.Output != nil {
		if out := sm.m.Shapes[sh.Output.Target]; out != nil {
			f.collection(sm, out, pag)
		}
	}
	return f
}

// readInputs records every input member and the required ones that identify
// something: paging and scope members never do.
func (f *opFacts) readInputs(sm *serviceModel, op *shape, pag paginated) {
	if op.Input == nil || sm.m.Shapes[op.Input.Target] == nil {
		return
	}
	for name, mem := range sm.m.Shapes[op.Input.Target].Members {
		f.inputs = append(f.inputs, name)
		_, req := mem.Traits["smithy.api#required"]
		if req && name != pag.InputToken && name != pag.PageSize && !scopeParams[strings.ToLower(name)] {
			f.required = append(f.required, name)
		}
	}
	sort.Strings(f.inputs)
	sort.Strings(f.required)
}

// rebind corrects bindings the models file loosely.
func (f *opFacts) rebind() {
	if f.node == nil {
		return
	}
	// An instance read named after its resource is that resource's own read
	// (lambda binds GetFunction as an instance operation of Function).
	if f.bind.role == roleInstance && f.read && sdkinv.Ident(f.noun) == sdkinv.Ident(f.node.name) {
		f.bind.role = "read"
	}
	// A collection operation that takes the resource's own identifiers acts on
	// one instance (lambda files GetFunctionConfiguration that way).
	if f.bind.role == roleCollection && len(f.node.ids) > 0 {
		all := true
		for _, id := range f.node.ids {
			all = all && slices.Contains(f.required, id)
		}
		if all {
			f.bind.role = roleInstance
		}
	}
}

// collection finds what the output enumerates: the paginated items, else the
// structural guess, descending into a sole payload structure only for an
// operation already known to list (cloudfront's KeyGroupList.Items).
func (f *opFacts) collection(sm *serviceModel, out *shape, pag paginated) {
	if pag.Items != "" {
		if el := sm.itemsElement(out, pag.Items); el != "" {
			f.items, f.elem, f.elems = true, el, []string{el}
			f.scanElement(sm.m, el)
			return
		}
	}
	f.guessCollection(sm.m, out)
	if !f.coll() && f.subject != "" && (f.bind.role == "list" || f.hasAct && f.act.isList) {
		if inner := sm.m.Shapes[f.subject]; inner != nil {
			f.guessCollection(sm.m, inner)
			f.wrapped = f.coll()
		}
	}
}

// scanElement records what one element says about the subject. A primitive
// element is the identifier itself.
func (f *opFacts) scanElement(m *smithyModel, el string) {
	t := m.Shapes[el]
	if t == nil || (t.Type != "structure" && t.Type != "union") {
		f.hasIDs = true
		return
	}
	for name, fm := range t.Members {
		if idLike(name) {
			f.hasIDs = true
			f.elemIDs = append(f.elemIDs, name)
		}
		f.elemARN = f.elemARN || (arnMemberRe.MatchString(name) && !slices.ContainsFunc(f.required, func(r string) bool { return strings.EqualFold(r, name) }))
		if ft := m.Shapes[fm.Target]; ft != nil && ft.Type == "timestamp" && createdRe.MatchString(name) {
			f.elemTime = true
		}
	}
}

// guessCollection is the structural fallback for an output the paginated
// trait does not describe: its list and map members of structures, and its
// lists of primitives named as ids or after the noun.
func (f *opFacts) guessCollection(m *smithyModel, out *shape) {
	f.subject = ""
	var subjects []string
	for _, name := range slices.Sorted(maps.Keys(out.Members)) {
		t := m.Shapes[out.Members[name].Target]
		if t == nil {
			continue
		}
		el := ""
		switch {
		case t.Type == "list" && t.Member != nil:
			el = t.Member.Target
		case t.Type == "map" && t.Value != nil:
			el = t.Value.Target
		case t.Type == "structure":
			subjects = append(subjects, out.Members[name].Target)
			continue
		default:
			continue
		}
		if isStructure(m, el) {
			f.elems = append(f.elems, el)
			f.scanElement(m, el)
		} else if t.Type == "list" && (idLike(name) || (f.noun != "" && strings.HasPrefix(sdkinv.Ident(name), sdkinv.Ident(sdkinv.CanonSingular(f.noun))))) {
			// Primitives listed under an id name, or under the operation's
			// noun (sqs:ListQueues answers QueueUrls), each name one element.
			// A map of primitives is key/value pairs (tags), not elements.
			f.elems = append(f.elems, el)
			f.hasIDs = true
			f.idList = f.idList || idLike(name)
		}
	}
	if len(f.elems) == 1 {
		f.elem = f.elems[0]
	}
	if len(subjects) == 1 {
		f.subject = subjects[0]
	}
	f.listShaped = f.coll() && len(subjects) == 0
}

// written is what the service's write operations create: the structures they
// take or return (lists count only of structures), the stems of ids named
// after the write's own noun, and each write's field names. A write's name is
// otherwise not evidence: PurchaseReservedInstancesOffering and PutLogEvents
// name an offering and an event, and telling them from CreateVolume takes a
// verb list. A write that merely requires an id references the thing, and an
// element carries its parent's ids too (a StackEvent's StackId), so neither
// is evidence. Tagging-only writes contribute only their tag shapes.
type written struct {
	shapes map[string]bool
	stems  map[string]bool
	fields []map[string]string // per write: the member names it takes or answers
	// tags are the element field sets tagging-only writes take (Tag{Key,Value}).
	tags []map[string]string
	sm   *serviceModel
}

// owns reports whether a listed element is something the account creates: a
// write takes or returns it, or answers the element's own id (AllocateHosts
// returns HostIds; snowball's CreateCluster answers the ClusterId a
// ClusterListEntry carries). An element's own id is stemmed with the start of
// its shape name — a SpotPrice's AvailabilityZone is someone else's — and an
// id the lister itself requires is the parent's (a StackEvent's StackId).
func (w written) owns(f *opFacts) bool {
	for _, e := range f.elems {
		if w.isTag(e) {
			continue
		}
		if w.shapes[e] || w.stems[memberStem(shapeName(e))] {
			return true
		}
	}
	// A write that sets or answers three of the element's own fields makes
	// the element. Fields the lister filters by and ids of other resources
	// do not count: they match every write that merely mentions them (glue's CreateClassifier
	// takes the GrokClassifier/XMLClassifier a Classifier carries; logs'
	// PutMetricFilter the filterName/filterPattern of a MetricFilter).
	for _, e := range f.elems {
		if w.isTag(e) {
			continue
		}
		elem := map[string]string{}
		fieldNames(w.sm, e, elem)
		for _, in := range f.inputs {
			delete(elem, strings.ToLower(in))
		}
		own := sdkinv.Ident(shapeName(e))
		for k, name := range elem {
			if stem := memberStem(name); idLike(name) && (stem == "" || !strings.HasPrefix(own, stem)) {
				delete(elem, k)
			}
		}
		for _, fields := range w.fields {
			n := 0
			for k := range elem {
				if _, ok := fields[k]; ok {
					n++
				}
			}
			if n >= 3 {
				return true
			}
		}
	}
	for _, id := range f.elemIDs {
		stem := memberStem(id)
		if stem == "" || !w.stems[stem] || slices.ContainsFunc(f.required, func(r string) bool { return memberStem(r) == stem }) {
			continue
		}
		if slices.ContainsFunc(f.elems, func(e string) bool { return strings.HasPrefix(sdkinv.Ident(shapeName(e)), stem) }) {
			return true
		}
	}
	return false
}

// fieldNames adds the lower-cased member names of a structure and of the
// structures it holds directly: a summary element often wraps the resource
// one level down (cloudfront's KeyGroupSummary{KeyGroup}).
func fieldNames(sm *serviceModel, id string, into map[string]string) {
	sh := sm.m.Shapes[id]
	if sh == nil {
		return
	}
	for name, mem := range sh.Members {
		if tokenNameRe.MatchString(name) {
			continue
		}
		into[strings.ToLower(name)] = name
		if t := sm.m.Shapes[mem.Target]; t != nil && t.Type == "structure" {
			for inner := range t.Members {
				if !tokenNameRe.MatchString(inner) {
					into[strings.ToLower(inner)] = inner
				}
			}
		}
	}
}

// tagShapes are the element structures a tagging-only write takes.
func tagShapes(sm *serviceModel, op *shape) []map[string]string {
	var out []map[string]string
	if op.Input == nil || sm.m.Shapes[op.Input.Target] == nil {
		return nil
	}
	for _, mem := range sm.m.Shapes[op.Input.Target].Members {
		t := sm.m.Shapes[mem.Target]
		if t == nil || t.Type != "list" || t.Member == nil || !isStructure(sm.m, t.Member.Target) {
			continue
		}
		fields := map[string]string{}
		for name := range sm.m.Shapes[t.Member.Target].Members {
			fields[strings.ToLower(name)] = name
		}
		if len(fields) >= 2 { // TagKeyOnly{Key} would claim anything with a Key
			out = append(out, fields)
		}
	}
	return out
}

// isTag reports whether a shape carries all the fields of a structure a
// tagging-only write takes (discovery's ConfigurationTag adds the tagged
// configuration's id to Key/Value, and is still a tag).
func (w written) isTag(id string) bool {
	if !isStructure(w.sm.m, id) {
		return false
	}
	elem := map[string]string{}
	fieldNames(w.sm, id, elem)
	return slices.ContainsFunc(w.tags, func(tag map[string]string) bool {
		for k := range tag {
			if _, ok := elem[k]; !ok {
				return false
			}
		}
		return true
	})
}

// tagged reports whether every element is a tag: the listing reads tags.
func (w written) tagged(f *opFacts) bool {
	return len(f.elems) > 0 && !slices.ContainsFunc(f.elems, func(e string) bool { return !w.isTag(e) })
}

func writtenBy(sm *serviceModel, facts []*opFacts) written {
	w := written{shapes: map[string]bool{}, stems: map[string]bool{}, sm: sm}
	for _, f := range facts {
		if !f.writeish() {
			continue
		}
		sh := sm.m.Shapes[f.id]
		// A tagging write writes tags, not the resource it tags: its Tag
		// shape would otherwise own every tag listing.
		if f.act.taggingOnly {
			w.tags = append(w.tags, tagShapes(sm, sh)...)
			continue
		}
		fields := map[string]string{}
		for _, io := range []*ref{sh.Input, sh.Output} {
			if io != nil {
				fieldNames(sm, io.Target, fields)
			}
		}
		w.fields = append(w.fields, fields)
		for _, io := range []*ref{sh.Input, sh.Output} {
			if io == nil {
				continue
			}
			w.shapes[io.Target] = true
			o := sm.m.Shapes[io.Target]
			if o == nil {
				continue
			}
			for name, mem := range o.Members {
				t := sm.m.Shapes[mem.Target]
				switch {
				case t == nil:
				case t.Type == "structure":
					w.shapes[mem.Target] = true
				case t.Type == "list" && t.Member != nil && isStructure(sm.m, t.Member.Target):
					w.shapes[t.Member.Target] = true
				}
				// Only an id named after the write's own noun is one it
				// issues (AllocateHosts → HostIds); others are references.
				if io == sh.Output && idLike(name) {
					if stem := memberStem(name); stem != "" && stem == sdkinv.Ident(f.noun) {
						w.stems[stem] = true
					}
				}
			}
		}
	}
	return w
}

// lister decides whether an operation enumerates a collection and names the
// evidence: a resource's list binding, the catalog's IsList, paginated items
// on a read, a read over something the account writes, or — only where the
// catalog binds no action — the structural shape of the output.
func (f *opFacts) lister(w written) (bool, string) {
	switch {
	case f.bind.role == "list":
		return true, "smithy-list"
	case f.hasAct && f.act.isList && !f.act.isWrite:
		return true, "sr-list"
	case f.items && f.readish():
		return true, "paginated-items"
	case f.paged && f.readish() && len(f.elems) > 0:
		// Paged without naming its items (servicediscovery's ListInstances).
		return true, "paginated-collection"
	case f.readish() && f.listShaped && w.owns(f):
		return true, "written-collection"
	case f.readish() && f.listShaped && f.idList:
		// A read answering nothing but a list of ids or names enumerates
		// them (appstream's ListAssociatedFleets answers Names).
		return true, "id-list"
	case f.readish() && f.listShaped && len(f.required) == 0:
		// Takes nothing and answers only a collection: an enumeration
		// whatever the catalog says (ses:ListReceiptFilters, cloud9's
		// ListEnvironments, dynamodb-streams' ListStreams are IsList false).
		return true, "unfiltered-collection"
	case !f.hasAct && f.listShaped && (f.read || f.paged):
		return true, "shape-list"
	case !f.hasAct && f.listShaped && !f.traited:
		return true, "untraited"
	}
	return false, ""
}

func (x *universe) indexModel(sm *serviceModel, srAll map[string]*srService, file string, reached map[string]bool) ([]string, []sdkinv.Operation) {
	svc, sdkID := serviceKey(sm.m)
	if svc == "" {
		return []string{"no service shape with a signing name"}, nil
	}
	var diags []string
	sr, diag := joinSR(srAll, svc, sdkID, reached)
	if diag != "" {
		diags = append(diags, diag)
	}
	namespaces := modelNamespaces(sm.m, svc)
	if sr != nil {
		namespaces[sr.name] = true
		if x.catalogued[svc] == nil {
			x.catalogued[svc] = map[string]bool{}
		}
		for id := range sr.resCanon {
			x.catalogued[svc][id] = true
		}
	}
	module := modelModule(file)
	var facts []*opFacts
	for _, id := range slices.Sorted(maps.Keys(sm.ops)) {
		f := x.facts(sm, id, sr)
		if sr != nil && sr != srAll[svc] {
			f.signals["sr:document="+sr.name] = true
		}
		facts = append(facts, f)
	}
	w := writtenBy(sm, facts)
	var cands []*opFacts
	var other []sdkinv.Operation
	for _, f := range facts {
		f.isList, f.evidence = f.lister(w)
		if !f.isList && (f.listShaped || !f.readish()) {
			// Writes, actions, and reads of several things at once.
			other = append(other, sdkinv.Operation{Service: svc, Name: f.name, Label: svc + ":" + f.name, Required: f.required, Module: module})
			continue
		}
		if f.elem == "" {
			for _, e := range f.elems { // the one of several collections the account writes
				if w.shapes[e] && !w.isTag(e) {
					f.elem = e
					break
				}
			}
		}
		if f.evidence != "" {
			f.signals["list:"+f.evidence] = true
		}
		f.lin = lineage(sm, f, sr)
		cands = append(cands, f)
	}
	keyByShape(sm.m, cands, sr, newVocab(facts), w)
	// Element-keyed operations go last so direct namings place first (see add).
	sort.SliceStable(cands, func(i, j int) bool { return !cands[i].signals["key:element"] && cands[j].signals["key:element"] })
	for _, f := range cands {
		op := sdkinv.Operation{Service: svc, Name: f.name, Label: svc + ":" + f.name, IsList: f.isList, Paged: f.paged, Required: f.required, Targets: f.lin.targets, Module: module}
		if f.ident == "" {
			other = append(other, op)
			continue
		}
		class, rule := classify(f, sr, namespaces, w)
		x.add(sm, sr, svc, f, op, class, rule)
	}
	return diags, other
}

// add files one candidate operation under its entry: an attribute nests under
// its parent's identity, a resource or catalog row stands at the service.
func (x *universe) add(sm *serviceModel, sr *srService, svc string, f *opFacts, op sdkinv.Operation, class sdkinv.Class, rule string) {
	key := svc + "/" + f.ident
	if class == sdkinv.ClassAttribute && f.lin.parent != "" && f.lin.parent != f.ident && !f.lin.self {
		key = svc + "/" + f.lin.parent + "/" + f.ident
	}
	en := x.entries[key]
	if en == nil {
		en = &entry{service: svc, depth: -1, signals: map[string]bool{}, refs: map[string]bool{}}
		x.entries[key] = en
	}
	en.nouns = append(en.nouns, f.disp)
	if sr != nil {
		if name, ok := sr.resCanon[f.ident]; ok {
			en.srName = sdkinv.Canon(name)
		}
	}
	// Operations folded in by their element are other ways to reach the
	// subject (ListRetirableGrants beside ListGrants): they place it only
	// when nothing named it directly.
	if !f.signals["key:element"] || en.depth < 0 {
		en.place(f.lin)
	}
	en.admit(class, rule)
	for _, r := range refsOfElement(sm.m, f) {
		en.refs[r] = true
	}
	for s := range f.signals {
		en.signals[s] = true
	}
	en.ops = append(en.ops, op)
}

// ownKey names an operation's subject without looking at its siblings: the
// model's resource, else the operation's own noun. The catalog resource an
// operation is authorised against is not its subject: DescribeLogStreams is
// authorised against the log group.
// vocab is what one model's operation names say about its words: the first
// word of every operation, and how many operations spell each noun.
type vocab struct {
	first map[string]bool
	nouns map[string]int
}

func newVocab(facts []*opFacts) vocab {
	v := vocab{first: map[string]bool{}, nouns: map[string]int{}}
	for _, f := range facts {
		v.first[camelWords(f.name)[0]] = true
		v.nouns[sdkinv.Ident(f.noun)]++
	}
	return v
}

func ownKey(f *opFacts, sr *srService, v vocab) (ident, disp, rule string) {
	if f.bound() {
		return sdkinv.Ident(f.node.name), sdkinv.Canon(f.node.name), "key:smithy-resource"
	}
	ident = sdkinv.Ident(f.noun)
	if ident == "" {
		return "", "", ""
	}
	// A child named without its parent misses the catalog (ListVersionsByFunction
	// says "versions" where the catalog says "function version").
	if sr != nil && f.lin.parent != "" && sr.resCanon[ident] == "" {
		if joined := sdkinv.Ident(f.lin.parent + ident); sr.resCanon[joined] != "" {
			return joined, sdkinv.Canon(f.noun), "key:parent-noun"
		}
	}
	// A noun that opens with words the model uses as operation verbs
	// (BatchGetProjects → "GetProjects") names what follows, when that is a
	// catalog resource or another operation's noun. The verbs come from this
	// model's own operation names, not a list.
	if v.nouns[ident] <= 1 && (sr == nil || sr.resCanon[ident] == "") {
		words := camelWords(f.noun)
		for i := 0; i < len(words)-1 && v.first[words[i]]; i++ {
			rest := strings.Join(words[i+1:], "")
			if id := sdkinv.Ident(rest); v.nouns[id] > 0 || (sr != nil && sr.resCanon[id] != "") {
				return id, sdkinv.Canon(rest), "key:verb-prefix"
			}
		}
	}
	return ident, sdkinv.Canon(f.noun), "key:op-name"
}

// keyByShape keys every candidate operation. Operations whose listed element
// (or, for a read, whose described structure) is one shape name one subject:
// the model's resource when one of them is bound to it, else the catalogued
// or shortest of their own keys.
func keyByShape(m *smithyModel, cands []*opFacts, sr *srService, v vocab, w written) {
	shapeOf := func(f *opFacts) string {
		if f.isList {
			return f.elem
		}
		return f.subject
	}
	type choice struct{ ident, disp string }
	best := map[string]choice{}
	bound := map[string]bool{}
	for _, f := range cands {
		var rule string
		f.ident, f.disp, rule = ownKey(f, sr, v)
		if rule != "" {
			f.signals[rule] = true
		}
		s := shapeOf(f)
		// Tags of many resources share one Tag shape; it names none of them.
		if f.ident == "" || !isStructure(m, s) || w.isTag(s) {
			continue
		}
		cur, seen := best[s]
		switch {
		case f.bound() && !bound[s]:
			best[s], bound[s] = choice{f.ident, f.disp}, true
		case bound[s]:
		case !seen || betterKey(f.ident, cur.ident, sr):
			best[s] = choice{f.ident, f.disp}
		}
	}
	for _, f := range cands {
		// A catalogued resource keeps its own key though its listing shares a
		// summary shape (sagemaker's four job definitions all answer
		// MonitoringJobDefinitionSummary).
		if c, ok := best[shapeOf(f)]; ok && f.ident != c.ident && !f.bound() && (sr == nil || sr.resCanon[f.ident] == "") {
			f.ident, f.disp = c.ident, c.disp
			f.signals["key:element"] = true
		}
	}
}

// betterKey prefers a catalogued identity, then the shorter, then the
// lexically first, so the pick does not depend on operation order.
func betterKey(a, b string, sr *srService) bool {
	if sr != nil {
		if ca, cb := sr.resCanon[a] != "", sr.resCanon[b] != ""; ca != cb {
			return ca
		}
	}
	return shorter(a, b)
}

// isStructure reports whether a shape id names a structure or union: only
// those identify a subject. Strings and ARNs are shared by everything.
func isStructure(m *smithyModel, id string) bool {
	t := m.Shapes[id]
	return t != nil && (t.Type == "structure" || t.Type == "union")
}

// lineage derives parent and depth: the resource tree, then the URI's labels,
// then the catalog's ARN nesting, then — for protocols with none of these —
// the required id members' names.
func lineage(sm *serviceModel, f *opFacts, sr *srService) place {
	var lin place
	if f.hasAct && len(f.required) > 0 {
		lin.targets = slices.Clone(f.act.resources)
	}
	nounIdent := sdkinv.Ident(f.noun)
	if n := f.node; n != nil {
		f.signals["lineage:resource"] = true
		if f.bind.role == roleInstance {
			lin.parent, lin.parentDisp, lin.depth = sdkinv.Ident(n.name), sdkinv.Canon(n.name), n.depth+1
			return lin
		}
		lin.self, lin.depth, lin.declared = true, n.depth, true
		if p := sm.res[n.parent]; p != nil {
			lin.parent, lin.parentDisp = sdkinv.Ident(p.name), sdkinv.Canon(p.name)
		}
		return lin
	}
	if levels, endsInLabel := uriLevels(f.uri); len(levels) > 0 {
		f.signals["lineage:uri"] = true
		last := levels[len(levels)-1]
		if !f.isList && endsInLabel && memberStem(last.label) == nounIdent {
			lin.self = true
			levels = levels[:len(levels)-1]
		}
		if len(levels) == 0 {
			return lin
		}
		p := levels[len(levels)-1]
		lin.depth = len(levels)
		if p.seg != "" {
			lin.parent, lin.parentDisp = sdkinv.Ident(sdkinv.CanonSingular(p.seg)), sdkinv.CanonSingular(p.seg)
		} else {
			lin.parent, lin.parentDisp = memberStem(p.label), memberDisp(p.label)
		}
		return lin
	}
	if sr != nil && f.hasAct && len(f.required) > 0 && len(f.act.resources) > 0 {
		var parents []string
		for _, r := range f.act.resources {
			lv := sr.levels[r]
			if len(lv) == 0 {
				continue
			}
			if sdkinv.Ident(r) == nounIdent {
				f.signals["lineage:arn"] = true
				lin.self, lin.depth = true, len(lv)-1
				if len(lv) > 1 {
					lin.parent, lin.parentDisp = memberStem(lv[len(lv)-2]), memberDisp(lv[len(lv)-2])
				}
				return lin
			}
			parents = append(parents, r)
		}
		if len(parents) > 0 {
			f.signals["lineage:arn"] = true
			p := pickParent(sr, parents, f.required)
			lin.parent, lin.parentDisp, lin.depth = sdkinv.Ident(p), sdkinv.Canon(p), len(sr.levels[p])
			return lin
		}
	}
	disp := map[string]string{}
	var stems []string
	for _, r := range f.required {
		stem := memberStem(r)
		switch {
		case stem == "" || strings.HasSuffix(nounIdent, stem):
			// The noun ends in what the id names (BatchGetFarms given
			// FarmIds): the operation reads that thing, not its child.
			lin.self = true
		case idLike(r):
			stems = append(stems, stem)
			disp[stem] = memberDisp(r)
			if sr != nil {
				if res := sr.stemResource(stem, nounIdent); res != "" {
					disp[stem] = sdkinv.Canon(res)
				}
			}
		}
	}
	if len(stems) > 0 {
		sort.Strings(stems)
		f.signals["lineage:required-id"] = true
		lin.parent, lin.parentDisp, lin.depth, lin.weak = stems[len(stems)-1], disp[stems[len(stems)-1]], 1, true
	}
	return lin
}

// stemResource names the catalog resource whose own ARN id an id member
// stems to. With several sharing the stem, the operation's own noun decides,
// then the shortest name, so the pick is order-independent.
func (s *srService) stemResource(stem, nounCanon string) string {
	best := ""
	for _, n := range s.levelStem[stem] {
		switch {
		case sdkinv.Ident(n) == nounCanon:
			return n
		case best == "" || shorter(n, best):
			best = n
		}
	}
	return best
}

// pickParent chooses the nearest ancestor among catalog targets: the deepest
// ARN, then the one a required input names.
func pickParent(sr *srService, targets, required []string) string {
	best, bestScore := "", -1
	for _, t := range targets {
		score := 2 * len(sr.levels[t])
		tc := sdkinv.Ident(t)
		for _, r := range required {
			if strings.Contains(sdkinv.Canon(r), tc) {
				score++
				break
			}
		}
		if score > bestScore {
			best, bestScore = t, score
		}
	}
	return best
}

// arnLevels is the id variables of an ARN format's resource field. The ARN
// grammar is arn:partition:service:region:account:resource, so only the sixth
// field nests; its variables are the levels whatever they are called.
func arnLevels(format string) []string {
	parts := strings.SplitN(format, ":", 6)
	if len(parts) < 6 {
		return nil
	}
	var out []string
	for _, m := range arnVarRe.FindAllStringSubmatch(parts[5], -1) {
		out = append(out, m[1])
	}
	return out
}

// classify decides the class and names the rule that decided it.
func classify(f *opFacts, sr *srService, namespaces map[string]bool, w written) (sdkinv.Class, string) {
	lin, signals := f.lin, f.signals
	if !f.isList {
		signals["detail-read"] = true
		return sdkinv.ClassAttribute, "detail-read"
	}
	if f.bound() {
		signals["smithy-resource"] = true
		return sdkinv.ClassResource, "smithy-resource"
	}
	if sr != nil {
		if name, ok := sr.resCanon[f.ident]; ok {
			if ns := sr.resNS[f.ident]; ns != "" && !ownsNamespace(namespaces, ns) {
				signals["sr:foreign-namespace="+ns] = true
			} else {
				signals["sr:resource="+name] = true
				return sdkinv.ClassResource, "sr-resource"
			}
		}
	}
	if others := foreignTargets(lin, f.ident); others >= crossCuttingTargets {
		signals["cross-cutting"] = true
		return sdkinv.ClassAttribute, "cross-cutting"
	}
	// A structural listing without paging over its own subject reads one
	// thing's state (GetFunctionConfiguration), not a collection.
	if (f.evidence == "shape-list" || f.evidence == "untraited" || f.evidence == "written-collection") && !f.paged && lin.self {
		signals["single-subject-read"] = true
		return sdkinv.ClassAttribute, "single-subject-read"
	}
	if w.tagged(f) {
		signals["tagging"] = true
		return sdkinv.ClassAttribute, "tagging"
	}
	owned := w.owns(f)
	if lin.depth > 0 {
		// An element without an id of its own is a property list (tags,
		// settings) however it is written.
		if !f.hasIDs {
			signals["id-less-collection"] = true
			return sdkinv.ClassAttribute, "id-less-collection"
		}
		signals["child-uncatalogued"] = true
		return sdkinv.ClassResource, "child-uncatalogued"
	}
	if !f.coll() {
		signals["no-collection"] = true
		return sdkinv.ClassNonResource, "no-collection"
	}
	if owned {
		signals["element-written"] = true
		return sdkinv.ClassResource, "element-written"
	}
	// An element carrying its own ARN is addressable in the account: the ARN is
	// AWS's resource-name grammar, and catalog rows (instance types, offerings,
	// zones) carry none.
	if f.elemARN {
		signals["element-arn"] = true
		return sdkinv.ClassResource, "element-arn"
	}
	// A creation timestamp says someone created the element; catalog rows are
	// published, not created.
	if f.elemTime {
		signals["element-created"] = true
		return sdkinv.ClassResource, "element-created"
	}
	signals["read-only"] = true
	return sdkinv.ClassCatalog, "read-only"
}

// refsOfElement is refsOf narrowed to the paginated element when the model
// names it: a side list in the same output is not the listed thing.
func refsOfElement(m *smithyModel, f *opFacts) []string {
	if !f.items {
		return refsOf(m, m.Shapes[f.id], sdkinv.Ident(f.noun))
	}
	el := m.Shapes[f.elem]
	if el == nil || (el.Type != "structure" && el.Type != "union") {
		return nil
	}
	refs := map[string]bool{}
	walkRefs(m, el, "", 0, sdkinv.Ident(f.noun), refs)
	return slices.Sorted(maps.Keys(refs))
}

// absentModels reports each linked aws-sdk-go-v2 service module with no model
// at SDKRef. Existence only: comparing each module's version to its model
// would warn for most modules whenever go.mod lags the pin, which is normal.
// A missing model means the service's operations are outside the universe, so
// its scanners pair as sdk-skew and its types leave the denominator. A
// `service/<pkg>/vN` module is skipped (ImportKey keys no nested path); none
// exist today.
func absentModels(files []string, deps []*debug.Module) []sdkinv.Diagnostic {
	have := map[string]bool{}
	for _, f := range files {
		have[modelPackage(filepath.Base(f))] = true
	}
	var out []sdkinv.Diagnostic
	for _, d := range deps {
		pkg := awsResolver{}.ImportKey(d.Path)
		if pkg == "" || have[pkg] {
			continue
		}
		out = append(out, sdkinv.Diagnostic{
			Severity: "warn", Source: d.Path + "@" + d.Version,
			Message: fmt.Sprintf("linked service %s has no Smithy model at the pinned SDK ref; its operations are outside the universe", pkg),
		})
	}
	return out
}
