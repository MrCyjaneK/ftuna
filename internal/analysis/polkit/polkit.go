// Package polkit scores rules that grant an action without authentication.
// allow_any and allow_inactive score 10. allow_active scores 25. An action
// that still asks for auth_admin is not a finding. A JavaScript rule that
// returns Result.YES scores 10, and the finding is that line. A pkla
// finding includes the Identity when the section has one. The module
// score is the worst grant in the package.
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

const (
	scoreAny    = 10
	scoreActive = 25
)

func init() { check.Register(module{}) }

type module struct{}

func (module) Name() string { return "polkit" }

func (module) Analyze(files []check.File) check.Report {
	score := check.Neutral
	var issues []check.Issue
	for _, f := range files {
		if f.Kind != check.KindFile {
			continue
		}
		var points int
		var detail, code string
		var ok bool
		switch kindOf(f.Path) {
		case "policy":
			points, detail, ok = policyScore(f.Body)
			code = "polkit.policy"
		case "rules":
			points, detail, ok = rulesScore(f.Body)
			code = "polkit.rules"
		case "pkla":
			points, detail, ok = pklaScore(f.Body)
			code = "polkit.pkla"
		default:
			continue
		}
		if !ok {
			continue
		}
		issues = append(issues, check.Issue{Path: f.Path, Code: code, Detail: detail})
		score = check.Worse(score, points)
	}
	return check.Report{Score: score, Issues: issues}
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

func policyScore(body []byte) (int, string, bool) {
	dec := xml.NewDecoder(bytes.NewReader(body))
	var action, field string
	inDefaults := false
	var text strings.Builder
	score := check.Neutral
	detail := ""
	consider := func() {
		if field == "" {
			return
		}
		label, ok := yesScore(field, text.String())
		text.Reset()
		field = ""
		if !ok {
			return
		}
		points := scoreActive
		if label == "allow_any=yes" || label == "allow_inactive=yes" {
			points = scoreAny
		}
		if score < 0 || points < score {
			detail = strings.TrimSpace(action + " " + label)
		}
		score = check.Worse(score, points)
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
	if score < 0 {
		return 0, "", false
	}
	return score, detail, true
}

func yesScore(field, raw string) (string, bool) {
	if !strings.EqualFold(strings.TrimSpace(raw), "yes") {
		return "", false
	}
	return field + "=yes", true
}

func rulesScore(body []byte) (int, string, bool) {
	sc := bufio.NewScanner(bytes.NewReader(body))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "//") || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "*") {
			continue
		}
		if strings.Contains(line, "Result.YES") {
			return scoreAny, trimDetail(line), true
		}
	}
	return 0, "", false
}

func pklaScore(body []byte) (int, string, bool) {
	score := check.Neutral
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
		points := scoreActive
		switch key {
		case "ResultAny", "ResultInactive":
			points = scoreAny
		case "ResultActive":
		default:
			continue
		}
		if score < 0 || points < score {
			detail = key + "=yes"
			if identity != "" {
				detail = "Identity=" + identity + " " + detail
			}
		}
		score = check.Worse(score, points)
	}
	if score < 0 {
		return 0, "", false
	}
	return score, detail, true
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
