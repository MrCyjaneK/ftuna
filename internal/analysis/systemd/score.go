package systemd

import (
	"bufio"
	"bytes"
	"path"
	"strings"
)

// A system service with no User= runs as root and starts at 30.
// An explicit unprivileged User= starts at 75. DynamicUser= starts at 80.
// A user-manager unit already runs as the session user and starts at 85.
// Sandbox settings can add up to 70, so a thorough sandbox reaches 100.
// Ambient capabilities subtract 25. The result is clamped to 0..100.
const (
	scoreSystemRoot   = 30
	scoreDynamicUser  = 80
	scoreSystemUser   = 75
	scoreSession      = 85
	scoreUserTimer    = 85
	scoreSystemTimer  = 30
	scoreGroupPenalty = 10
	scoreAmbient      = 25
	sandboxCap        = 70
)

func judgeService(id identity, unitPath string, body []byte, known bool) found {
	svc := parseUnit(body).fields
	user, group := account(id.scope, svc)
	score := baseScore(id.scope, user, svc.dynamic)
	if !rootish(user) && privilegedGroup(group) {
		score -= scoreGroupPenalty
	}
	features, bonus := sandbox(svc)
	if bonus > sandboxCap {
		bonus = sandboxCap
	}
	if known {
		score += bonus
	}
	exec := joinVals(svc.service["ExecStart"])
	if exec == "" {
		exec = "unknown"
	}
	detail := "scope=" + id.scope + " user=" + user + " group=" + group + " exec=" + exec
	if svc.ambient {
		score -= scoreAmbient
		ambient := joinVals(svc.service["AmbientCapabilities"])
		if ambient == "" {
			ambient = "set"
		}
		detail += " ambient=" + ambient
	}
	detail += " sandbox="
	if !known {
		detail += "unknown"
	} else if len(features) == 0 {
		detail += "none"
	} else {
		detail += strings.Join(features, ",")
	}
	return found{
		path:   unitPath,
		code:   "systemd.service",
		detail: detail,
		score:  clamp(score),
		scored: true,
	}
}

func activationCode(name string) (string, bool) {
	switch {
	case strings.HasSuffix(name, ".timer"):
		return "systemd.timer", true
	case strings.HasSuffix(name, ".socket"):
		return "systemd.socket", true
	case strings.HasSuffix(name, ".path"):
		return "systemd.path", true
	default:
		return "", false
	}
}

func activationTarget(name string, body unitBody) string {
	var target, suffix string
	switch {
	case strings.HasSuffix(name, ".socket"):
		target = last(body.socket["Service"])
		suffix = ".socket"
	case strings.HasSuffix(name, ".path"):
		target = last(body.path["Unit"])
		suffix = ".path"
	default:
		target = last(body.timer["Unit"])
		suffix = ".timer"
	}
	if target == "" {
		return strings.TrimSuffix(name, suffix) + ".service"
	}
	return path.Base(target)
}

func judgeActivation(id identity, unitPath, code, target string, serviceSeen bool) found {
	score := scoreSystemTimer
	if id.scope == "user" {
		score = scoreUserTimer
	}
	return found{
		path:   unitPath,
		code:   code,
		detail: "scope=" + id.scope + " activates=" + target,
		score:  score,
		scored: !serviceSeen,
	}
}

func baseScore(scope, user string, dynamic bool) int {
	switch {
	case scope == "user" && !rootish(user):
		return scoreSession
	case dynamic && !rootish(user):
		return scoreDynamicUser
	case !rootish(user):
		return scoreSystemUser
	default:
		return scoreSystemRoot
	}
}

func account(scope string, svc unitFields) (user, group string) {
	user = "root"
	group = "root"
	if scope == "user" {
		user = "session"
		group = "session"
	}
	if svc.dynamic && !svc.userSet {
		user = "dynamic"
	}
	if svc.dynamic && !svc.groupSet {
		group = "dynamic"
	}
	if svc.userSet {
		user = last(svc.service["User"])
	}
	if svc.groupSet {
		group = last(svc.service["Group"])
	}
	return user, group
}

func rootish(user string) bool {
	switch strings.ToLower(strings.TrimSpace(user)) {
	case "root", "0":
		return true
	default:
		return false
	}
}

func privilegedGroup(group string) bool {
	switch strings.ToLower(strings.TrimSpace(group)) {
	case "root", "0", "wheel":
		return true
	default:
		return false
	}
}

type unitFields struct {
	service  map[string][]string
	dynamic  bool
	userSet  bool
	groupSet bool
	ambient  bool
}

func sandbox(svc unitFields) (features []string, bonus int) {
	add := func(token string, points int) {
		features = append(features, token)
		bonus += points
	}
	switch strings.ToLower(last(svc.service["ProtectSystem"])) {
	case "strict":
		add("ProtectSystem=strict", 12)
	case "full":
		add("ProtectSystem=full", 8)
	case "yes", "true", "on", "1":
		add("ProtectSystem", 5)
	}
	switch strings.ToLower(last(svc.service["ProtectHome"])) {
	case "yes", "true", "on", "1", "tmpfs":
		add("ProtectHome", 6)
	case "read-only":
		add("ProtectHome=read-only", 4)
	}
	if enabled(svc.service["PrivateTmp"]) {
		add("PrivateTmp", 4)
	}
	if enabled(svc.service["PrivateDevices"]) {
		add("PrivateDevices", 4)
	}
	if enabled(svc.service["PrivateNetwork"]) {
		add("PrivateNetwork", 4)
	}
	if enabled(svc.service["PrivateUsers"]) {
		add("PrivateUsers", 4)
	}
	if enabled(svc.service["NoNewPrivileges"]) {
		add("NoNewPrivileges", 8)
	}
	if enabled(svc.service["MemoryDenyWriteExecute"]) {
		add("MemoryDenyWriteExecute", 4)
	}
	if enabled(svc.service["RestrictSUIDSGID"]) {
		add("RestrictSUIDSGID", 4)
	}
	if restrictive(last(svc.service["RestrictNamespaces"])) {
		add("RestrictNamespaces", 4)
	}
	var kernel []string
	for _, key := range []string{"ProtectKernelModules", "ProtectKernelTunables", "ProtectKernelLogs", "ProtectControlGroups", "ProtectClock", "ProtectHostname"} {
		if enabled(svc.service[key]) {
			kernel = append(kernel, key)
		}
	}
	if len(kernel) > 0 {
		features = append(features, kernel...)
		bonus += 4
	}
	if anySet(svc.service["SystemCallFilter"]) {
		add("SystemCallFilter", 8)
	}
	if restrictedCaps(svc.service["CapabilityBoundingSet"]) {
		add("CapabilityBoundingSet", 8)
	}
	return features, bonus
}

type unitBody struct {
	fields unitFields
	timer  map[string][]string
	socket map[string][]string
	path   map[string][]string
}

func parseUnit(body []byte) unitBody {
	svc := map[string][]string{}
	timer := map[string][]string{}
	socket := map[string][]string{}
	pathKeys := map[string][]string{}
	if len(body) == 0 {
		return unitBody{fields: unitFields{service: svc}, timer: timer, socket: socket, path: pathKeys}
	}
	section := ""
	pending := ""
	sc := bufio.NewScanner(bytes.NewReader(body))
	for sc.Scan() {
		line := sc.Text()
		if pending != "" {
			line = pending + line
			pending = ""
		}
		if strings.HasSuffix(line, `\`) && !strings.HasSuffix(line, `\\`) {
			pending = strings.TrimSuffix(line, `\`)
			continue
		}
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = line[1 : len(line)-1]
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		switch section {
		case "Service":
			svc[key] = append(svc[key], val)
		case "Timer":
			timer[key] = append(timer[key], val)
		case "Socket":
			socket[key] = append(socket[key], val)
		case "Path":
			pathKeys[key] = append(pathKeys[key], val)
		}
	}
	fields := unitFields{service: svc}
	fields.dynamic = enabled(svc["DynamicUser"])
	_, fields.userSet = svc["User"]
	_, fields.groupSet = svc["Group"]
	fields.ambient = anySet(svc["AmbientCapabilities"])
	return unitBody{fields: fields, timer: timer, socket: socket, path: pathKeys}
}

func enabled(vals []string) bool {
	on, ok := truthy(last(vals))
	return ok && on
}

func truthy(val string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(val)) {
	case "1", "yes", "true", "on":
		return true, true
	case "0", "no", "false", "off":
		return false, true
	default:
		return false, false
	}
}

func restrictive(val string) bool {
	val = strings.TrimSpace(val)
	if val == "" {
		return false
	}
	if on, ok := truthy(val); ok {
		return on
	}
	return true
}

func anySet(vals []string) bool {
	for _, val := range vals {
		if strings.TrimSpace(val) != "" {
			return true
		}
	}
	return false
}

func restrictedCaps(vals []string) bool {
	if len(vals) == 0 {
		return false
	}
	for _, val := range vals {
		val = strings.TrimSpace(val)
		if val == "" || strings.HasPrefix(val, "~") {
			continue
		}
		for _, cap := range strings.Fields(val) {
			if strings.EqualFold(cap, "CAP_SYS_ADMIN") {
				return false
			}
		}
	}
	return true
}

func last(vals []string) string {
	if len(vals) == 0 {
		return ""
	}
	return strings.TrimSpace(vals[len(vals)-1])
}

func joinVals(vals []string) string {
	var out []string
	for _, val := range vals {
		val = strings.TrimSpace(val)
		if val != "" {
			out = append(out, val)
		}
	}
	return strings.Join(out, "; ")
}

func clamp(score int) int {
	if score < 0 {
		return 0
	}
	if score > 100 {
		return 100
	}
	return score
}
