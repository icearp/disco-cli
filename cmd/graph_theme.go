package cmd

import (
	"fmt"
	"sort"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
)

// dotTheme drives all DOT styling. One *dotTheme per --dot-theme value;
// rendering code reads attrs from here, never inlines color literals — so
// adding a new palette is one new entry in themes, not edits across
// renderGraphDot.
type dotTheme struct {
	Name           string
	Graph          map[string]string // bgcolor, splines, nodesep, ranksep, fontname, pad
	NodeDefaults   map[string]string
	EdgeDefaults   map[string]string
	NodePresets    map[nodePreset]map[string]string
	EdgePresets    map[string]map[string]string // keyed by store.Rel* kind
	ClusterPalette []clusterStyle               // round-robin per cluster index
	// Mono signals "current pre-theme output" — renderGraphDot takes a
	// fast path and emits no per-node attribute blocks. Byte-for-byte
	// stable for diff-piping into automation.
	Mono bool
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

// presetForResource maps a resource to a preset by type-segment heuristic.
// Type strings are "<provider>:<service>:<kind>" — service segment is the
// pivot. ManagedByProvider always wins (returns Muted) regardless of type
// so foreign / cloud-owned nodes read as terminal across every theme.
func presetForResource(r *store.Resource) nodePreset {
	if r.ManagedByProvider {
		return presetMuted
	}
	parts := strings.Split(r.Type, ":")
	if len(parts) < 2 {
		return presetSecondary
	}
	switch parts[1] {
	case "s3", "rds", "ebs", "efs", "dynamodb", "fsx", "elasticache",
		"storage", "sql", "cosmosdb", "redis",
		"bigquery", "spanner", "firestore", "filestore":
		return presetStorage
	case "iam", "sso", "aad", "iam-policy", "iam-key", "rbac",
		"microsoft.authorization", "microsoft.managedidentity":
		return presetIdentity
	case "ec2", "lambda", "ecs", "eks", "batch",
		"compute", "appservice", "containerservice",
		"run", "functions", "cloudfunctions", "container":
		return presetPrimary
	default:
		return presetSecondary
	}
}

// renderAttrs builds a `k=v, k=v` block (no surrounding brackets), keys
// sorted for stable diffs. Values are quoted unless they look like a bare
// identifier — DOT accepts both, quoting matches Graphviz docs and dodges
// "rounded,filled" comma parsing.
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
// mono so a typo never crashes graph rendering. Caller validates against
// known names at flag-parse time for a friendly error.
func themeByName(name string) *dotTheme {
	if t, ok := themes[name]; ok {
		return t
	}
	return themes["mono"]
}

var themes = map[string]*dotTheme{
	"light": lightTheme(),
	"dark":  darkTheme(),
	"mono":  monoTheme(),
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

// lightTheme is the default — pastel fills with darker accent borders, ortho
// edges, white background. Reads cleanly on white-paper PDFs / GitHub light.
func lightTheme() *dotTheme {
	return &dotTheme{
		Name: "light",
		Graph: map[string]string{
			"bgcolor":  "white",
			"splines":  "ortho",
			"nodesep":  "0.4",
			"ranksep":  "0.6",
			"fontname": "Helvetica",
			"pad":      "0.3",
		},
		NodeDefaults: map[string]string{
			"shape":    "box",
			"style":    "rounded,filled",
			"fontname": "Helvetica",
			"fontsize": "10",
			"penwidth": "1.2",
		},
		EdgeDefaults: map[string]string{
			"fontname":  "Helvetica",
			"fontsize":  "9",
			"color":     "#616161",
			"fontcolor": "#616161",
		},
		NodePresets: map[nodePreset]map[string]string{
			presetPrimary: {
				"shape":     "box",
				"fillcolor": "#E3F2FD",
				"color":     "#1976D2",
				"fontcolor": "#0D47A1",
			},
			presetSecondary: {
				"shape":     "component",
				"fillcolor": "#F5F5F5",
				"color":     "#616161",
				"fontcolor": "#212121",
			},
			presetStorage: {
				"shape":     "cylinder",
				"fillcolor": "#FFF3E0",
				"color":     "#E65100",
				"fontcolor": "#BF360C",
			},
			presetIdentity: {
				"shape":     "note",
				"fillcolor": "#F3E5F5",
				"color":     "#6A1B9A",
				"fontcolor": "#4A148C",
			},
			presetMuted: {
				"shape":     "box",
				"style":     "rounded,dashed",
				"fillcolor": "#FAFAFA",
				"color":     "#BDBDBD",
				"fontcolor": "#9E9E9E",
			},
			presetError: {
				"shape":     "box",
				"fillcolor": "#FFEBEE",
				"color":     "#C62828",
				"fontcolor": "#B71C1C",
				"penwidth":  "2",
			},
		},
		EdgePresets: map[string]map[string]string{
			store.RelContains:          {"penwidth": "1.6", "color": "#212121"},
			store.RelAttachedTo:        {"color": "#424242"},
			store.RelUses:              {"style": "dashed", "color": "#1976D2"},
			store.RelAssumes:           {"color": "#6A1B9A"},
			store.RelRoutesTo:          {"color": "#388E3C"},
			store.RelPeer:              {"style": "dotted", "dir": "both", "color": "#00796B"},
			store.RelBoundedBy:         {"style": "dashed", "color": "#E65100"},
			store.RelCrossAccountTrust: {"style": "dotted", "color": "#C62828"},
			store.RelCrossSubRBAC:      {"style": "dotted", "color": "#C62828"},
			store.RelCrossProjectIAM:   {"style": "dotted", "color": "#C62828"},
		},
		ClusterPalette: []clusterStyle{
			{BGColor: "#FAFAFA", Border: "#9E9E9E"},
			{BGColor: "#E8F5E9", Border: "#66BB6A"},
			{BGColor: "#E1F5FE", Border: "#29B6F6"},
			{BGColor: "#FFF3E0", Border: "#FFA726"},
			{BGColor: "#F3E5F5", Border: "#AB47BC"},
		},
	}
}

// darkTheme inverts lightTheme for dark-mode renderers (GitHub dark, VS Code
// embedded preview, terminal-rendered SVG viewers). Same shape/color-family
// mapping, brighter foreground, near-black background.
func darkTheme() *dotTheme {
	return &dotTheme{
		Name: "dark",
		Graph: map[string]string{
			"bgcolor":  "#1E1E1E",
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
			"fontcolor": "#ECEFF1",
			"penwidth":  "1.2",
		},
		EdgeDefaults: map[string]string{
			"fontname":  "Helvetica",
			"fontsize":  "9",
			"color":     "#9E9E9E",
			"fontcolor": "#BDBDBD",
		},
		NodePresets: map[nodePreset]map[string]string{
			presetPrimary: {
				"shape":     "box",
				"fillcolor": "#0D47A1",
				"color":     "#64B5F6",
				"fontcolor": "#E3F2FD",
			},
			presetSecondary: {
				"shape":     "component",
				"fillcolor": "#37474F",
				"color":     "#90A4AE",
				"fontcolor": "#ECEFF1",
			},
			presetStorage: {
				"shape":     "cylinder",
				"fillcolor": "#BF360C",
				"color":     "#FFAB91",
				"fontcolor": "#FFF3E0",
			},
			presetIdentity: {
				"shape":     "note",
				"fillcolor": "#4A148C",
				"color":     "#CE93D8",
				"fontcolor": "#F3E5F5",
			},
			presetMuted: {
				"shape":     "box",
				"style":     "rounded,dashed",
				"fillcolor": "#2E2E2E",
				"color":     "#616161",
				"fontcolor": "#9E9E9E",
			},
			presetError: {
				"shape":     "box",
				"fillcolor": "#B71C1C",
				"color":     "#EF9A9A",
				"fontcolor": "#FFEBEE",
				"penwidth":  "2",
			},
		},
		EdgePresets: map[string]map[string]string{
			store.RelContains:          {"penwidth": "1.6", "color": "#ECEFF1"},
			store.RelAttachedTo:        {"color": "#B0BEC5"},
			store.RelUses:              {"style": "dashed", "color": "#64B5F6"},
			store.RelAssumes:           {"color": "#CE93D8"},
			store.RelRoutesTo:          {"color": "#81C784"},
			store.RelPeer:              {"style": "dotted", "dir": "both", "color": "#4DB6AC"},
			store.RelBoundedBy:         {"style": "dashed", "color": "#FFAB91"},
			store.RelCrossAccountTrust: {"style": "dotted", "color": "#EF9A9A"},
			store.RelCrossSubRBAC:      {"style": "dotted", "color": "#EF9A9A"},
			store.RelCrossProjectIAM:   {"style": "dotted", "color": "#EF9A9A"},
		},
		ClusterPalette: []clusterStyle{
			{BGColor: "#263238", Border: "#546E7A"},
			{BGColor: "#1B5E20", Border: "#388E3C"},
			{BGColor: "#0D47A1", Border: "#1976D2"},
			{BGColor: "#E65100", Border: "#F57C00"},
			{BGColor: "#4A148C", Border: "#7B1FA2"},
		},
	}
}

// monoTheme reproduces pre-theme output: only the original `node` global
// block, no per-node fills, no edge colors. Selected for diff-stable piping
// into Git-tracked artifacts or downstream tooling that re-themes itself.
func monoTheme() *dotTheme {
	return &dotTheme{
		Name: "mono",
		Mono: true,
		// NodeDefaults intentionally matches the legacy single-line emit
		// in renderGraphDot — keeps byte-for-byte output stability.
		NodeDefaults: map[string]string{
			"shape":    "box",
			"fontname": "Helvetica",
		},
	}
}
