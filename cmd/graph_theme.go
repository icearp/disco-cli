package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/icearp/disco-cli/store"
)

// dotTheme drives all DOT styling. One *dotTheme per --dot-theme value;
// rendering code reads attrs from here, never inlines color literals — so
// adding a new palette is one new themePalette literal + one buildTheme
// call, not edits across renderGraphDot. A theme with empty
// NodePresets / EdgePresets / Graph renders with no per-node fills —
// renderGraphDot's normal path already emits nothing for empty maps.
type dotTheme struct {
	// Graph holds digraph-level attributes (bgcolor, splines, nodesep, ...).
	Graph map[string]string
	// NodeDefaults / EdgeDefaults are the `node [...]` / `edge [...]`
	// header blocks; per-node and per-edge presets layer overrides on top.
	NodeDefaults map[string]string
	EdgeDefaults map[string]string
	// NodePresets keyed by nodePreset; renderer looks up by presetForResource.
	NodePresets map[nodePreset]map[string]string
	// EdgePresets keyed by store.Rel* kind. Edge kinds without an entry
	// fall through to EdgeDefaults only.
	EdgePresets map[string]map[string]string
	// ClusterPalette rotates by cluster index. Cycles silently for graphs
	// with more clusters than entries — five distinct hues is plenty and
	// beats picking a sixth that clashes.
	ClusterPalette []clusterStyle
}

type nodePreset string

const (
	presetPrimary   nodePreset = "primary"   // compute / runtime — box rounded filled
	presetSecondary nodePreset = "secondary" // network / glue — component shape
	presetStorage   nodePreset = "storage"   // S3, RDS, disks — cylinder
	presetIdentity  nodePreset = "identity"  // IAM/AAD/SA — note shape
	presetMuted     nodePreset = "muted"     // managed-by-provider, foreign — dashed grey
	presetError     nodePreset = "error"     // findings overlay (future) — red
)

type clusterStyle struct {
	BGColor string
	Border  string
}

// serviceToPreset is the data half of presetForResource — the type's
// second colon-delimited segment maps to a preset. Adding a new service
// is one entry here, not a switch-case edit.
//
// Note: Azure's "service segment" is the full ARM RP namespace
// (e.g. `microsoft.authorization`), not a short name like AWS's `s3`.
// Both styles intentionally coexist below.
var serviceToPreset = map[string]nodePreset{
	// storage / data
	"s3": presetStorage, "rds": presetStorage, "ebs": presetStorage,
	"efs": presetStorage, "dynamodb": presetStorage, "fsx": presetStorage,
	"elasticache": presetStorage, "storage": presetStorage, "sql": presetStorage,
	"cosmosdb": presetStorage, "redis": presetStorage,
	"bigquery": presetStorage, "spanner": presetStorage,
	"firestore": presetStorage, "filestore": presetStorage,

	// identity / IAM
	"iam": presetIdentity, "sso": presetIdentity, "aad": presetIdentity,
	"iam-policy": presetIdentity, "iam-key": presetIdentity, "rbac": presetIdentity,
	"microsoft.authorization":   presetIdentity,
	"microsoft.managedidentity": presetIdentity,

	// compute / runtime
	"ec2": presetPrimary, "lambda": presetPrimary, "ecs": presetPrimary,
	"eks": presetPrimary, "batch": presetPrimary, "compute": presetPrimary,
	"appservice": presetPrimary, "containerservice": presetPrimary,
	"run": presetPrimary, "functions": presetPrimary,
	"cloudfunctions": presetPrimary, "container": presetPrimary,
}

// presetForResource maps a resource to a preset. ManagedByProvider always
// wins (returns Muted) regardless of type so foreign / cloud-owned nodes
// read as terminal across every theme. Unmapped services fall back to
// Secondary (the catch-all "structural / glue" look).
func presetForResource(r *store.Resource) nodePreset {
	if r.ManagedByProvider {
		return presetMuted
	}
	parts := strings.SplitN(r.Type, ":", 3)
	if len(parts) < 2 {
		return presetSecondary
	}
	if p, ok := serviceToPreset[parts[1]]; ok {
		return p
	}
	return presetSecondary
}

// renderAttrs builds a `k=v, k=v` block (no surrounding brackets), keys
// sorted for stable diffs. All values quoted via %q — DOT accepts both
// quoted and bare-identifier forms; over-quoting is harmless and dodges
// "rounded,filled" comma-parsing surprises.
func renderAttrs(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%q", k, m[k]))
	}
	return strings.Join(parts, ", ")
}

// themeByName looks up a registered theme; unknown names fall back to
// light so a typo never crashes graph rendering. Caller validates against
// known names at flag-parse time for a friendly error.
func themeByName(name string) *dotTheme {
	if t, ok := themes[name]; ok {
		return t
	}
	return themes["light"]
}

var themes = map[string]*dotTheme{
	"light": lightTheme(),
	"dark":  darkTheme(),
}

// dotThemeNames returns the registered theme names, sorted, for help text
// and validation. Reads themes once at init since the map is package-level.
func dotThemeNames() []string {
	out := make([]string, 0, len(themes))
	for k := range themes {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// presetColors is the three-tone slot a preset reads from a palette:
// fill (background), border (stroke), text (label foreground).
type presetColors struct {
	Fill, Border, Text string
}

// themePalette holds every semantic color a theme needs. light/dark
// supply different hex literals for the same slots; buildTheme stamps
// them into the structurally-identical attribute maps.
type themePalette struct {
	BG          string // graph bgcolor
	NodeFG      string // default node fontcolor (used when preset doesn't override)
	EdgeFG      string // default edge stroke
	EdgeFGLabel string // default edge label fontcolor

	Primary, Secondary, Storage, Identity, Muted, Errored presetColors

	EdgeContains    string // hierarchy edges — bold accent
	EdgeAttachedTo  string // structural membership
	EdgeUses        string // runtime dependency (dashed)
	EdgeAssumes     string // IAM trust
	EdgeRoutesTo    string // routing
	EdgePeer        string // peering (dotted bidirectional)
	EdgeBoundedBy   string // permission boundary (dashed)
	EdgeCrossTenant string // cross-account/sub/project (dotted, single color)

	Cluster []clusterStyle
}

// buildTheme stamps a palette into the standard preset / edge / cluster
// shape. Both themes (light, dark) flow through here so adding a new
// theme is one palette literal + one buildTheme call.
func buildTheme(p themePalette) *dotTheme {
	crossTenant := map[string]string{"style": "dotted", "color": p.EdgeCrossTenant}
	return &dotTheme{
		Graph: map[string]string{
			"bgcolor":  p.BG,
			"splines":  "ortho",
			"nodesep":  "0.4",
			"ranksep":  "0.6",
			"fontname": "Helvetica",
			"pad":      "0.3",
		},
		NodeDefaults: map[string]string{
			"shape":     "box",
			"style":     "rounded,filled",
			"fontname":  "Helvetica",
			"fontsize":  "10",
			"fontcolor": p.NodeFG,
			"penwidth":  "1.2",
		},
		EdgeDefaults: map[string]string{
			"fontname":  "Helvetica",
			"fontsize":  "9",
			"color":     p.EdgeFG,
			"fontcolor": p.EdgeFGLabel,
		},
		NodePresets: map[nodePreset]map[string]string{
			presetPrimary:   filledPreset("box", p.Primary),
			presetSecondary: filledPreset("component", p.Secondary),
			presetStorage:   filledPreset("cylinder", p.Storage),
			presetIdentity:  filledPreset("note", p.Identity),
			presetMuted:     mutedPreset(p.Muted),
			presetError:     errorPreset(p.Errored),
		},
		EdgePresets: map[string]map[string]string{
			store.RelContains:          {"penwidth": "1.6", "color": p.EdgeContains},
			store.RelAttachedTo:        {"dir": "back", "color": p.EdgeAttachedTo},
			store.RelUses:              {"style": "dashed", "color": p.EdgeUses},
			store.RelAssumes:           {"color": p.EdgeAssumes},
			store.RelRoutesTo:          {"color": p.EdgeRoutesTo},
			store.RelPeer:              {"style": "dotted", "dir": "both", "color": p.EdgePeer},
			store.RelBoundedBy:         {"style": "dashed", "color": p.EdgeBoundedBy},
			store.RelCrossAccountTrust: crossTenant,
			store.RelCrossSubRBAC:      crossTenant,
			store.RelCrossProjectIAM:   crossTenant,
			store.RelOrgIAM:            crossTenant,
		},
		ClusterPalette: p.Cluster,
	}
}

func filledPreset(shape string, c presetColors) map[string]string {
	return map[string]string{
		"shape":     shape,
		"fillcolor": c.Fill,
		"color":     c.Border,
		"fontcolor": c.Text,
	}
}

// mutedPreset adds dashed border alongside the rounded/filled default;
// `filled` stays so fillcolor renders.
func mutedPreset(c presetColors) map[string]string {
	return map[string]string{
		"shape":     "box",
		"style":     "rounded,filled,dashed",
		"fillcolor": c.Fill,
		"color":     c.Border,
		"fontcolor": c.Text,
	}
}

// errorPreset is filledPreset with a thicker border to make findings pop.
func errorPreset(c presetColors) map[string]string {
	return map[string]string{
		"shape":     "box",
		"fillcolor": c.Fill,
		"color":     c.Border,
		"fontcolor": c.Text,
		"penwidth":  "2",
	}
}

// lightTheme — pastel fills with darker accent borders, ortho edges,
// white background. Reads cleanly on white-paper PDFs / GitHub light.
func lightTheme() *dotTheme {
	return buildTheme(themePalette{
		BG:          "white",
		NodeFG:      "#212121",
		EdgeFG:      "#616161",
		EdgeFGLabel: "#616161",

		Primary:   presetColors{"#E3F2FD", "#1976D2", "#0D47A1"},
		Secondary: presetColors{"#F5F5F5", "#616161", "#212121"},
		Storage:   presetColors{"#FFF3E0", "#E65100", "#BF360C"},
		Identity:  presetColors{"#F3E5F5", "#6A1B9A", "#4A148C"},
		Muted:     presetColors{"#FAFAFA", "#BDBDBD", "#9E9E9E"},
		Errored:   presetColors{"#FFEBEE", "#C62828", "#B71C1C"},

		EdgeContains:    "#212121",
		EdgeAttachedTo:  "#424242",
		EdgeUses:        "#1976D2",
		EdgeAssumes:     "#6A1B9A",
		EdgeRoutesTo:    "#388E3C",
		EdgePeer:        "#00796B",
		EdgeBoundedBy:   "#E65100",
		EdgeCrossTenant: "#C62828",

		Cluster: []clusterStyle{
			{BGColor: "#FAFAFA", Border: "#9E9E9E"},
			{BGColor: "#E8F5E9", Border: "#66BB6A"},
			{BGColor: "#E1F5FE", Border: "#29B6F6"},
			{BGColor: "#FFF3E0", Border: "#FFA726"},
			{BGColor: "#F3E5F5", Border: "#AB47BC"},
		},
	})
}

// darkTheme inverts lightTheme for dark-mode renderers.
func darkTheme() *dotTheme {
	return buildTheme(themePalette{
		BG:          "#1E1E1E",
		NodeFG:      "#ECEFF1",
		EdgeFG:      "#9E9E9E",
		EdgeFGLabel: "#BDBDBD",

		Primary:   presetColors{"#0D47A1", "#64B5F6", "#E3F2FD"},
		Secondary: presetColors{"#37474F", "#90A4AE", "#ECEFF1"},
		Storage:   presetColors{"#5D4037", "#FFAB91", "#FFF3E0"},
		Identity:  presetColors{"#4A148C", "#CE93D8", "#F3E5F5"},
		Muted:     presetColors{"#2E2E2E", "#616161", "#9E9E9E"},
		Errored:   presetColors{"#B71C1C", "#EF9A9A", "#FFEBEE"},

		EdgeContains:    "#ECEFF1",
		EdgeAttachedTo:  "#B0BEC5",
		EdgeUses:        "#64B5F6",
		EdgeAssumes:     "#CE93D8",
		EdgeRoutesTo:    "#81C784",
		EdgePeer:        "#4DB6AC",
		EdgeBoundedBy:   "#FFAB91",
		EdgeCrossTenant: "#EF9A9A",

		Cluster: []clusterStyle{
			{BGColor: "#263238", Border: "#546E7A"},
			{BGColor: "#1B5E20", Border: "#388E3C"},
			{BGColor: "#0D47A1", Border: "#1976D2"},
			{BGColor: "#E65100", Border: "#F57C00"},
			{BGColor: "#4A148C", Border: "#7B1FA2"},
		},
	})
}
