// Package polkit reports rules that grant an action without authentication.
// allow_any, allow_inactive, allow_active, a JavaScript Result.YES, and a
// pkla ResultAny, ResultInactive, or ResultActive are red. An action that
// still asks for auth_admin is not a finding. A pkla finding includes the
// Identity when the section has one.
package polkit

import (
	"bufio"
	"bytes"
	"encoding/xml"
	"io"
	"path"
	"strings"

	"github.com/mrcyjanek/ftuna/internal/analysis/check"
)

func init() { check.Register(module{}) }

type module struct{}

func (module) Name() string { return "polkit" }

func (module) Analyze(files []check.File) check.Report {
	var issues []check.Issue
	for _, f := range files {
		if f.Kind != check.KindFile {
			continue
		}
		var detail, code string
		var ok bool
		switch kindOf(f.Path) {
		case "policy":
			detail, ok = policyFinding(f.Body)
			code = "polkit.policy"
		case "rules":
			detail, ok = rulesFinding(f.Body)
			code = "polkit.rules"
		case "pkla":
			detail, ok = pklaFinding(f.Body)
			code = "polkit.pkla"
		default:
			continue
		}
		if !ok {
			continue
		}
		issues = append(issues, check.Issue{Path: f.Path, Code: code, Detail: detail, Color: check.ColorRed})
	}
	return check.Report{Issues: issues}
}

func kindOf(name string) string {
	switch {
	case path.Dir(name) == "/usr/share/polkit-1/actions" && strings.HasSuffix(name, ".policy"):
		return "policy"
	case (path.Dir(name) == "/usr/share/polkit-1/rules.d" || path.Dir(name) == "/etc/polkit-1/rules.d") && strings.HasSuffix(name, ".rules"):
		return "rules"
	case strings.HasSuffix(name, ".pkla") && strings.Contains(name, "/polkit-1/localauthority/"):
		return "pkla"
	default:
		return ""
	}
}

func policyFinding(body []byte) (string, bool) {
	dec := xml.NewDecoder(bytes.NewReader(body))
	var action, field string
	inDefaults := false
	var text strings.Builder
	found := false
	broad := false
	detail := ""
	consider := func() {
		if field == "" {
			return
		}
		label, ok := yesGrant(field, text.String())
		text.Reset()
		field = ""
		if !ok {
			return
		}
		wider := label == "allow_any=yes" || label == "allow_inactive=yes"
		if !found || (wider && !broad) {
			detail = strings.TrimSpace(action + " " + label)
			broad = wider
		}
		found = true
	}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			consider()
			break
		}
		if err != nil {
			break
		}
		switch el := tok.(type) {
		case xml.StartElement:
			switch el.Name.Local {
			case "action":
				action = xmlAttr(el, "id")
			case "defaults":
				inDefaults = true
			case "allow_any", "allow_inactive", "allow_active":
				if inDefaults {
					consider()
					field = el.Name.Local
				}
			}
		case xml.EndElement:
			switch el.Name.Local {
			case "defaults":
				consider()
				inDefaults = false
			case "allow_any", "allow_inactive", "allow_active":
				consider()
			}
		case xml.CharData:
			if field != "" {
				text.Write(el)
			}
		}
	}
	if !found {
		return "", false
	}
	return detail, true
}

func yesGrant(field, raw string) (string, bool) {
	if !strings.EqualFold(strings.TrimSpace(raw), "yes") {
		return "", false
	}
	return field + "=yes", true
}

func rulesFinding(body []byte) (string, bool) {
	sc := bufio.NewScanner(bytes.NewReader(body))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "//") || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "*") {
			continue
		}
		if strings.Contains(line, "Result.YES") {
			return trimDetail(line), true
		}
	}
	return "", false
}

func pklaFinding(body []byte) (string, bool) {
	found := false
	broad := false
	detail := ""
	identity := ""
	sc := bufio.NewScanner(bytes.NewReader(body))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			identity = ""
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if key == "Identity" && val != "" {
			identity = val
			continue
		}
		if !strings.EqualFold(val, "yes") {
			continue
		}
		wider := false
		switch key {
		case "ResultAny", "ResultInactive":
			wider = true
		case "ResultActive":
		default:
			continue
		}
		if !found || (wider && !broad) {
			detail = key + "=yes"
			if identity != "" {
				detail = "Identity=" + identity + " " + detail
			}
			broad = wider
		}
		found = true
	}
	if !found {
		return "", false
	}
	return detail, true
}

func trimDetail(line string) string {
	if len(line) > 180 {
		return line[:180]
	}
	return line
}

func xmlAttr(el xml.StartElement, name string) string {
	for _, attr := range el.Attr {
		if attr.Name.Local == name {
			return attr.Value
		}
	}
	return ""
}
