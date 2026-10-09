// Package check is the shared interface for static RPM analysis modules.
package check

// Version is the analysis suite version. Increase it when a module
// heuristic changes. A stored file whose version differs is rewritten.
const Version = 6

// Neutral is a module score that does not apply to this package.
// Verdict ignores every Neutral score.
const Neutral = -1

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
type Issue struct {
	Module string `json:"module"`
	Path   string `json:"path"`
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

// Score is one module's judgment of the package.
// Score is 0..100, where 100 is the least concerning case that module
// knows and 0 is the most. Neutral means the module had nothing to say.
// A root process with no sandbox sits near 30. A user process sits near
// 75. A real sandbox sits near 100. Setuid, file capabilities, and other
// files that are hard to account for sit near 0.
type Score struct {
	Module string `json:"module"`
	Score  int    `json:"score"`
}

// Report is one module's score and the findings behind it.
// Several findings do not add. Score is the worst of them, or Neutral
// when the module does not apply.
type Report struct {
	Score  int
	Issues []Issue
}

// Module inspects a package and returns one score.
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

// Apply runs every module on the package. Scores follow registration order.
// Issues from one module stay together.
func Apply(files []File) ([]Score, []Issue) {
	scores := make([]Score, 0, len(modules))
	var out []Issue
	for _, mod := range modules {
		rep := mod.Analyze(files)
		for i := range rep.Issues {
			rep.Issues[i].Module = mod.Name()
		}
		scores = append(scores, Score{Module: mod.Name(), Score: normalize(rep.Score)})
		out = append(out, rep.Issues...)
	}
	if out == nil {
		out = []Issue{}
	}
	return scores, out
}

// Verdict is the average of every score that is not Neutral, rounded to
// the nearest integer. It is Neutral when every module abstains.
func Verdict(scores []Score) int {
	sum, n := 0, 0
	for _, s := range scores {
		if s.Score < 0 {
			continue
		}
		sum += s.Score
		n++
	}
	if n == 0 {
		return Neutral
	}
	return (sum + n/2) / n
}

// Worse keeps the more concerning of two scores.
// Neutral yields to any score in 0..100.
func Worse(a, b int) int {
	if a < 0 {
		return b
	}
	if b < 0 || a <= b {
		return a
	}
	return b
}

func normalize(score int) int {
	if score < 0 {
		return Neutral
	}
	if score > 100 {
		return 100
	}
	return score
}
