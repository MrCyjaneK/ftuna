// Package udev scores udev rules that run a program or open a device node
// to every user. udev runs as root. An external program, including
// PROGRAM and IMPORT{program}, scores 30. A udev builtin is udev's own
// command, so it is not a finding. MODE that grants other-write scores 15.
// A rules file that only sets ownership or a private mode abstains.
// The module score is the worst hit.
package udev

import (
	"path"
	"strings"

	"github.com/mrcyjanek/ftuna/internal/analysis/check"
)

const (
	scoreProgram = 30
	scoreMode    = 15
)

func init() { check.Register(module{}) }

type module struct{}

func (module) Name() string { return "udev" }

func (module) Analyze(files []check.File) check.Report {
	score := check.Neutral
	var issues []check.Issue
	for _, f := range files {
		if f.Kind != check.KindFile || !isRules(f.Path) {
			continue
		}
		for _, run := range runs(f.Body) {
			if run.builtin {
				continue
			}
			points := scoreProgram
			code := "udev.run"
			detail := "user=root program=" + run.command
			if run.mode {
				points = scoreMode
				code = "udev.mode"
				detail = "user=root mode=" + run.command
			}
			issues = append(issues, check.Issue{Path: f.Path, Code: code, Detail: detail})
			score = check.Worse(score, points)
		}
	}
	return check.Report{Score: score, Issues: issues}
}

func isRules(name string) bool {
	if !strings.HasSuffix(name, ".rules") {
		return false
	}
	switch path.Dir(name) {
	case "/lib/udev/rules.d", "/usr/lib/udev/rules.d", "/etc/udev/rules.d":
		return true
	default:
		return false
	}
}

type run struct {
	command string
	builtin bool
	mode    bool
}

func runs(body []byte) []run {
	text := strings.ReplaceAll(string(body), "\\\n", " ")
	var out []run
	for _, line := range strings.Split(text, "\n") {
		line = cutComment(line)
		for _, field := range splitFields(line) {
			key, op, val, ok := assign(field)
			if !ok || op == "-=" || strings.TrimSpace(val) == "" {
				continue
			}
			item, ok := ruleHit(key, op, val)
			if !ok {
				continue
			}
			out = append(out, item)
		}
	}
	return out
}

func ruleHit(key, op, val string) (run, bool) {
	val = strings.TrimSpace(val)
	brace := ""
	base := key
	if i := strings.Index(key, "{"); i >= 0 {
		brace = key[i:]
		base = key[:i]
	}
	switch base {
	case "RUN":
		return run{command: val, builtin: strings.Contains(brace, "builtin")}, true
	case "PROGRAM":
		return run{command: val}, true
	case "IMPORT":
		if strings.Contains(brace, "program") {
			return run{command: val}, true
		}
	case "MODE":
		if (op == "=" || op == "+=") && worldWrite(val) {
			return run{command: val, mode: true}, true
		}
	}
	return run{}, false
}

func worldWrite(mode string) bool {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		return false
	}
	last := mode[len(mode)-1]
	return last == '2' || last == '3' || last == '6' || last == '7'
}

func cutComment(line string) string {
	quote := byte(0)
	for i := 0; i < len(line); i++ {
		c := line[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '\'' {
			quote = c
			continue
		}
		if c == '#' {
			return line[:i]
		}
	}
	return line
}

func splitFields(line string) []string {
	var out []string
	var b strings.Builder
	quote := byte(0)
	for i := 0; i < len(line); i++ {
		c := line[i]
		if quote != 0 {
			b.WriteByte(c)
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '\'' {
			quote = c
			b.WriteByte(c)
			continue
		}
		if c == ',' {
			out = append(out, b.String())
			b.Reset()
			continue
		}
		b.WriteByte(c)
	}
	if strings.TrimSpace(b.String()) != "" {
		out = append(out, b.String())
	}
	return out
}

func assign(field string) (key, op, val string, ok bool) {
	field = strings.TrimSpace(field)
	for _, candidate := range []string{"+=", "-=", "==", "!=", "="} {
		i := strings.Index(field, candidate)
		if i < 0 {
			continue
		}
		key = strings.TrimSpace(field[:i])
		val = trimQuotes(strings.TrimSpace(field[i+len(candidate):]))
		return key, candidate, val, key != ""
	}
	return "", "", "", false
}

func trimQuotes(val string) string {
	if len(val) >= 2 {
		if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
			return val[1 : len(val)-1]
		}
	}
	return val
}
