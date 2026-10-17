package cron

import (
	"testing"

	"github.com/mrcyjanek/ftuna/internal/analysis/check"
)

func TestJobs(t *testing.T) {
	m := module{}
	rep := m.Analyze([]check.File{
		{Path: "/etc/cron.d/demo", Kind: check.KindFile, Body: []byte("SHELL=/bin/sh\n*/5 * * * * nemo /usr/bin/demo\n")},
		{Path: "/etc/crontab", Kind: check.KindFile, Body: []byte("0 3 * * * root /usr/bin/backup\n")},
		{Path: "/etc/cron.daily/demo", Kind: check.KindFile, Body: []byte("#!/bin/sh\n/usr/bin/demo\n")},
		{Path: "/etc/cron.hourly/empty", Kind: check.KindFile, Body: []byte("# nothing\n")},
		{Path: "/var/spool/cron/crontabs/nemo", Kind: check.KindFile, Body: []byte("@daily /usr/bin/demo\n")},
		{Path: "/etc/anacrontab", Kind: check.KindFile, Body: []byte("1 5 cron.daily nice run-parts /etc/cron.daily\n")},
		{Path: "/etc/cron.allow", Kind: check.KindFile, Body: []byte("nemo\n")},
	})
	if len(rep.Issues) != 5 {
		t.Fatalf("%+v", rep.Issues)
	}
	byPath := map[string]check.Issue{}
	for _, issue := range rep.Issues {
		byPath[issue.Path] = issue
		if issue.Code != "cron" {
			t.Fatalf("%+v", issue)
		}
	}
	if byPath["/etc/cron.d/demo"].Detail != "user=nemo command=/usr/bin/demo" || byPath["/etc/cron.d/demo"].Color != check.ColorYellow || byPath["/etc/crontab"].Detail != "user=root command=/usr/bin/backup" || byPath["/etc/crontab"].Color != check.ColorRed || byPath["/var/spool/cron/crontabs/nemo"].Detail != "user=nemo command=/usr/bin/demo" || byPath["/var/spool/cron/crontabs/nemo"].Color != check.ColorGreen {
		t.Fatalf("%v", byPath)
	}
	if byPath["/etc/cron.daily/demo"].Detail != "user=root interpreter=/bin/sh" || byPath["/etc/cron.daily/demo"].Color != check.ColorRed || byPath["/etc/anacrontab"].Detail != "user=root command=nice run-parts /etc/cron.daily" || byPath["/etc/anacrontab"].Color != check.ColorRed {
		t.Fatalf("%v", byPath)
	}
	userOnly := m.Analyze([]check.File{{
		Path: "/etc/cron.d/demo", Kind: check.KindFile, Body: []byte("*/5 * * * * nemo /usr/bin/demo\n"),
	}})
	if len(userOnly.Issues) != 1 || userOnly.Issues[0].Color != check.ColorYellow {
		t.Fatalf("user %+v", userOnly)
	}
	spool := m.Analyze([]check.File{{
		Path: "/var/spool/cron/nemo", Kind: check.KindFile, Body: []byte("0 * * * * /usr/bin/demo\n"),
	}})
	if len(spool.Issues) != 1 || spool.Issues[0].Color != check.ColorGreen || spool.Issues[0].Detail != "user=nemo command=/usr/bin/demo" {
		t.Fatalf("spool %+v", spool)
	}
	if got := m.Analyze(nil); len(got.Issues) != 0 {
		t.Fatalf("none %+v", got)
	}
}
