// Package dbus reports bus activators and bus policies.
// A system service with no User= runs as root and is red. Another User=
// is yellow. A session service runs as the session user and is green,
// unless it explicitly sets User=root, which is red. A policy is reported
// when it grants a wildcard or lets a broad context own a name. A system
// policy is red. A session-bus policy is yellow.
package dbus

import (
	"bufio"
	"bytes"
	"encoding/xml"
	"io"
	"path"
	"sort"
	"strings"

	"github.com/mrcyjanek/ftuna/internal/analysis/check"
)

func init() { check.Register(module{}) }

type module struct{}

func (module) Name() string { return "dbus" }

func (module) Analyze(files []check.File) check.Report {
	var issues []check.Issue
	for _, f := range files {
		if f.Kind != check.KindFile {
			continue
		}
		kind := kindOf(f.Path)
		switch kind {
		case "service", "session-service":
			user, exec, systemd, explicit := serviceAccount(f.Body)
			scope := "system"
			color := check.ColorRed
			if kind == "session-service" {
				scope = "session"
				if !explicit {
					user = "session"
				}
				color = check.ColorGreen
			}
			if user != "root" && user != "0" && scope == "system" {
				color = check.ColorYellow
			}
			if (user == "root" || user == "0") && explicit {
				color = check.ColorRed
			}
			if exec == "" {
				exec = "unknown"
			}
			detail := "scope=" + scope + " user=" + user + " exec=" + exec
			if systemd != "" {
				detail += " systemd=" + systemd
			}
			issues = append(issues, check.Issue{
				Path:   f.Path,
				Code:   "dbus.service",
				Detail: detail,
				Color:  color,
			})
		case "policy", "session-policy":
			detail, ok := policyFinding(f.Body)
			if !ok {
				continue
			}
			color := check.ColorRed
			if kind == "session-policy" {
				color = check.ColorYellow
			}
			issues = append(issues, check.Issue{Path: f.Path, Code: "dbus.policy", Detail: detail, Color: color})
		}
	}
	return check.Report{Issues: issues}
}

func kindOf(name string) string {
	dir := path.Dir(name)
	switch dir {
	case "/usr/share/dbus-1/system-services", "/etc/dbus-1/system-services":
		if strings.HasSuffix(name, ".service") {
			return "service"
		}
	case "/usr/share/dbus-1/services", "/etc/dbus-1/services":
		if strings.HasSuffix(name, ".service") {
			return "session-service"
		}
	case "/usr/share/dbus-1/system.d", "/etc/dbus-1/system.d":
		if strings.HasSuffix(name, ".conf") {
			return "policy"
		}
	case "/usr/share/dbus-1/session.d", "/etc/dbus-1/session.d":
		if strings.HasSuffix(name, ".conf") {
			return "session-policy"
		}
	}
	return ""
}

func serviceAccount(body []byte) (user, exec, systemd string, explicit bool) {
	user = "root"
	sc := bufio.NewScanner(bytes.NewReader(body))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "User":
			if v := strings.TrimSpace(val); v != "" {
				user = v
				explicit = true
			}
		case "Exec":
			exec = strings.TrimSpace(val)
		case "SystemdService":
			systemd = strings.TrimSpace(val)
		}
	}
	return user, exec, systemd, explicit
}

func policyFinding(body []byte) (string, bool) {
	dec := xml.NewDecoder(bytes.NewReader(body))
	var user, group, context string
	inPolicy := false
	parsed := false
	grants := map[string]bool{}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			if !parsed {
				return "unparsed", true
			}
			break
		}
		switch el := tok.(type) {
		case xml.StartElement:
			parsed = true
			switch el.Name.Local {
			case "policy":
				inPolicy = true
				user = xmlAttr(el, "user")
				group = xmlAttr(el, "group")
				context = xmlAttr(el, "context")
			case "allow":
				if !inPolicy {
					continue
				}
				noteAllow(grants, user, group, context, el)
			}
		case xml.EndElement:
			if el.Name.Local == "policy" {
				inPolicy = false
			}
		}
	}
	if len(grants) == 0 {
		return "", false
	}
	names := make([]string, 0, len(grants))
	for name := range grants {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, " "), true
}

func noteAllow(grants map[string]bool, user, group, context string, el xml.StartElement) {
	subject := policySubject(user, group, context)
	broad := user == "*" || group == "*" || context == "default" || context == "mandatory"
	for _, attr := range el.Attr {
		switch attr.Name.Local {
		case "own", "send_destination", "receive_sender", "send_interface":
			if attr.Value == "*" {
				grants[subject+" "+attr.Name.Local+"=*"] = true
			} else if attr.Name.Local == "own" && broad && attr.Value != "" {
				grants[subject+" own"] = true
			}
		case "eavesdrop":
			if attr.Value == "true" || attr.Value == "1" {
				grants[subject+" eavesdrop"] = true
			}
		}
	}
}

func policySubject(user, group, context string) string {
	switch {
	case user != "":
		return "user=" + user
	case group != "":
		return "group=" + group
	case context != "":
		return "context=" + context
	default:
		return "context=default"
	}
}

func xmlAttr(el xml.StartElement, name string) string {
	for _, attr := range el.Attr {
		if attr.Name.Local == name {
			return attr.Value
		}
	}
	return ""
}
