// Package check is the shared interface for static RPM analysis modules.
package check

// Version is the analysis suite version. Increase it when a module
// heuristic changes. A stored file whose version differs is rewritten.
const Version = 7

const (
	ColorRed    = "red"
	ColorYellow = "yellow"
	ColorGreen  = "green"
	ColorGray   = "gray"
)

const (
	KindFile      = "file"
	KindDir       = "dir"
	KindSymlink   = "symlink"
	KindGhost     = "ghost"
	KindScriptlet = "scriptlet"
)

// File is one installed payload entry, or one scriptlet.
// Body is the file bytes for a regular file and the script text for a
// scriptlet. It is empty for directories, symlinks, and ghost entries.
type File struct {
	Path    string
	Mode    uint32
	User    string
	Group   string
	Body    []byte
	Kind    string
	Program string
	// Caps is the RPM file-capability text for this path.
	// It is empty when the header lists none.
	Caps string
}

// Issue is one finding from a module.
// Color is red, yellow, green, or gray.
type Issue struct {
	Module string `json:"module"`
	Path   string `json:"path"`
	Code   string `json:"code"`
	Detail string `json:"detail"`
	Color  string `json:"color,omitempty"`
}

// Report is one module's findings. An empty report means the module
// had nothing to say about this package.
type Report struct {
	Issues []Issue
}

// Module inspects a package and returns its findings.
type Module interface {
	Name() string
	Analyze([]File) Report
}

var modules []Module

// Register adds a module. Modules are called in registration order.
func Register(m Module) {
	modules = append(modules, m)
}

// Modules returns the registered modules.
func Modules() []Module {
	out := make([]Module, len(modules))
	copy(out, modules)
	return out
}

// Apply runs every module on the package.
// Issues from one module stay together.
func Apply(files []File) []Issue {
	var out []Issue
	for _, mod := range modules {
		for _, issue := range mod.Analyze(files).Issues {
			issue.Module = mod.Name()
			out = append(out, issue)
		}
	}
	if out == nil {
		out = []Issue{}
	}
	return out
}

// WorseColor keeps the more concerning color.
// An empty color yields to any real one. Gray yields to green, yellow, and red.
func WorseColor(a, b string) string {
	if rank(b) > rank(a) {
		return b
	}
	return a
}

func rank(color string) int {
	switch color {
	case ColorRed:
		return 4
	case ColorYellow:
		return 3
	case ColorGreen:
		return 2
	case ColorGray:
		return 1
	default:
		return 0
	}
}
