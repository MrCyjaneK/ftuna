// Package filecaps scores Linux file capabilities from the RPM header.
// A capability that can change credentials, bypass file permissions, or
// load kernel code scores 10. A narrower capability scores 35. A name
// this module does not recognize is treated as dangerous and scores 10:
// an unknown capability is hard to account for. The finding records the
// capability text and, when the header has them, the mode and owner.
// The module score is the worst file. A package with no file capabilities
// abstains.
package filecaps

import (
	"fmt"
	"strings"

	"github.com/mrcyjanek/ftuna/internal/analysis/check"
)

const (
	scoreRoot   = 10
	scoreMedium = 35
)

func init() { check.Register(module{}) }

type module struct{}

func (module) Name() string { return "filecaps" }

func (module) Analyze(files []check.File) check.Report {
	score := check.Neutral
	var issues []check.Issue
	for _, f := range files {
		if f.Kind == check.KindScriptlet || strings.TrimSpace(f.Caps) == "" {
			continue
		}
		points := capsScore(f.Caps)
		issues = append(issues, check.Issue{Path: f.Path, Code: "filecaps", Detail: capsDetail(f)})
		score = check.Worse(score, points)
	}
	return check.Report{Score: score, Issues: issues}
}

func capsDetail(f check.File) string {
	detail := strings.TrimSpace(f.Caps)
	if f.Mode == 0 && f.User == "" && f.Group == "" {
		return detail
	}
	return fmt.Sprintf("%s mode %#o user %s group %s", detail, f.Mode, f.User, f.Group)
}

func capsScore(raw string) int {
	names := capNames(raw)
	if len(names) == 0 {
		return scoreRoot
	}
	score := check.Neutral
	for _, name := range names {
		score = check.Worse(score, capWeight(name))
	}
	return score
}

func capNames(raw string) []string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "=")
	var out []string
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' '
	}) {
		part = strings.TrimSpace(part)
		if part == "" || part == "=" {
			continue
		}
		name, _, _ := strings.Cut(part, "=")
		name, _, _ = strings.Cut(name, "+")
		name = strings.ToLower(strings.TrimSpace(name))
		switch name {
		case "", "e", "i", "p", "ep", "ei", "ip", "eip", "pie", "pei":
			continue
		}
		if !strings.HasPrefix(name, "cap_") {
			name = "cap_" + name
		}
		out = append(out, name)
	}
	return out
}

func capWeight(name string) int {
	switch name {
	case "cap_sys_admin", "cap_sys_module", "cap_sys_rawio", "cap_sys_ptrace",
		"cap_sys_boot", "cap_dac_override", "cap_dac_read_search", "cap_setuid",
		"cap_setgid", "cap_setfcap", "cap_mac_admin", "cap_mac_override",
		"cap_syslog", "cap_audit_control", "cap_bpf", "cap_perfmon":
		return scoreRoot
	case "cap_net_admin", "cap_net_raw", "cap_net_bind_service", "cap_kill",
		"cap_chown", "cap_fowner", "cap_sys_nice", "cap_sys_resource",
		"cap_sys_time", "cap_ipc_lock", "cap_audit_write", "cap_wake_alarm",
		"cap_block_suspend":
		return scoreMedium
	default:
		return scoreRoot
	}
}
