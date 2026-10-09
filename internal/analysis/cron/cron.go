// Package cron scores crontab, cron directory, and anacrontab entries.
// A root job is 30. A named non-root job in a system table is 75.
// A user spool runs at 85. The module score is the worst file.
// A file with no jobs abstains. Each finding names the user and the command.
package cron

import (
	"path"
	"strings"
	"unicode"

	"github.com/mrcyjanek/ftuna/internal/analysis/check"
)

const (
	scoreRoot  = 30
	scoreUser  = 75
	scoreSpool = 85
)

func init() { check.Register(module{}) }

type module struct{}

func (module) Name() string { return "cron" }

func (module) Analyze(files []check.File) check.Report {
	score := check.Neutral
	var issues []check.Issue
	for _, f := range files {
		if f.Kind != check.KindFile {
			continue
		}
		points, detail, ok := fileScore(f)
		if !ok {
			continue
		}
		issues = append(issues, check.Issue{Path: f.Path, Code: "cron", Detail: detail})
		score = check.Worse(score, points)
	}
	return check.Report{Score: score, Issues: issues}
}

type job struct {
	user    string
	command string
}

func fileScore(f check.File) (int, string, bool) {
	switch classOf(f.Path) {
	case "table":
		jobs := cronJobs(f.Body, true)
		if len(jobs) == 0 {
			return 0, "", false
		}
		return jobsScore(jobs, false), jobsDetail(jobs), true
	case "periodic":
		if !hasJob(f.Body) {
			return 0, "", false
		}
		detail := "user=root"
		if interp := interpreter(f.Body); interp != "" {
			detail += " interpreter=" + interp
		}
		return scoreRoot, detail, true
	case "anacron":
		jobs := anacronJobs(f.Body)
		if len(jobs) == 0 {
			return 0, "", false
		}
		return scoreRoot, jobsDetail(jobs), true
	case "spool":
		jobs := cronJobs(f.Body, false)
		if len(jobs) == 0 {
			return 0, "", false
		}
		user := path.Base(f.Path)
		for i := range jobs {
			jobs[i].user = user
		}
		return jobsScore(jobs, true), jobsDetail(jobs), true
	default:
		return 0, "", false
	}
}

func jobsScore(jobs []job, spool bool) int {
	points := scoreUser
	if spool {
		points = scoreSpool
	}
	for _, item := range jobs {
		if rootish(item.user) {
			return scoreRoot
		}
	}
	return points
}

func jobsDetail(jobs []job) string {
	parts := make([]string, len(jobs))
	for i, item := range jobs {
		parts[i] = "user=" + item.user
		if item.command != "" {
			parts[i] += " command=" + item.command
		}
	}
	return strings.Join(parts, "; ")
}

func classOf(name string) string {
	switch name {
	case "/etc/crontab":
		return "table"
	case "/etc/anacrontab":
		return "anacron"
	}
	base := path.Base(name)
	if base == "" || strings.HasPrefix(base, ".") || base == "cron.allow" || base == "cron.deny" {
		return ""
	}
	switch path.Dir(name) {
	case "/etc/cron.d":
		return "table"
	case "/etc/cron.hourly", "/etc/cron.daily", "/etc/cron.weekly", "/etc/cron.monthly":
		return "periodic"
	case "/var/spool/cron", "/var/spool/cron/crontabs":
		return "spool"
	default:
		return ""
	}
}

func cronJobs(body []byte, withUser bool) []job {
	var jobs []job
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if strings.HasPrefix(fields[0], "@") {
			if !withUser {
				cmd := ""
				if len(fields) >= 2 {
					cmd = strings.Join(fields[1:], " ")
				}
				jobs = append(jobs, job{user: "root", command: cmd})
				continue
			}
			if len(fields) >= 2 {
				cmd := ""
				if len(fields) >= 3 {
					cmd = strings.Join(fields[2:], " ")
				}
				jobs = append(jobs, job{user: fields[1], command: cmd})
			}
			continue
		}
		need := 6
		if withUser {
			need = 7
		}
		if len(fields) < need || !isTime(fields[0]) {
			continue
		}
		if withUser {
			jobs = append(jobs, job{user: fields[5], command: strings.Join(fields[6:], " ")})
			continue
		}
		jobs = append(jobs, job{user: "root", command: strings.Join(fields[5:], " ")})
	}
	return jobs
}

func anacronJobs(body []byte) []job {
	var jobs []job
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		if strings.HasPrefix(fields[0], "@") || isNumber(fields[0]) {
			jobs = append(jobs, job{user: "root", command: strings.Join(fields[3:], " ")})
		}
	}
	return jobs
}

func hasJob(body []byte) bool {
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			return true
		}
	}
	return false
}

func interpreter(body []byte) string {
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "#!") {
			return ""
		}
		fields := strings.Fields(strings.TrimPrefix(line, "#!"))
		if len(fields) == 0 {
			return ""
		}
		return fields[0]
	}
	return ""
}

func isTime(field string) bool {
	if field == "" {
		return false
	}
	for _, r := range field {
		if unicode.IsLetter(r) {
			return false
		}
	}
	return true
}

func isNumber(field string) bool {
	if field == "" {
		return false
	}
	for _, r := range field {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func rootish(user string) bool {
	switch strings.ToLower(strings.TrimSpace(user)) {
	case "", "root", "0":
		return true
	default:
		return false
	}
}
