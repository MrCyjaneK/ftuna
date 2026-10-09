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
	if rep.Score != scoreRoot || len(rep.Issues) != 5 {
		t.Fatalf("%d %+v", rep.Score, rep.Issues)
	}
	byPath := map[string]string{}
	for _, issue := range rep.Issues {
		byPath[issue.Path] = issue.Detail
		if issue.Code != "cron" {
			t.Fatalf("%+v", issue)
		}
	}
	if byPath["/etc/cron.d/demo"] != "user=nemo command=/usr/bin/demo" || byPath["/etc/crontab"] != "user=root command=/usr/bin/backup" || byPath["/var/spool/cron/crontabs/nemo"] != "user=nemo command=/usr/bin/demo" {
		t.Fatalf("%v", byPath)
	}
	if byPath["/etc/cron.daily/demo"] != "user=root interpreter=/bin/sh" || byPath["/etc/anacrontab"] != "user=root command=nice run-parts /etc/cron.daily" {
		t.Fatalf("%v", byPath)
	}
	userOnly := m.Analyze([]check.File{{
		Path: "/etc/cron.d/demo", Kind: check.KindFile, Body: []byte("*/5 * * * * nemo /usr/bin/demo\n"),
	}})
	if userOnly.Score != scoreUser {
		t.Fatalf("user %d", userOnly.Score)
	}
	spool := m.Analyze([]check.File{{
		Path: "/var/spool/cron/nemo", Kind: check.KindFile, Body: []byte("0 * * * * /usr/bin/demo\n"),
	}})
	if spool.Score != scoreSpool || spool.Issues[0].Detail != "user=nemo command=/usr/bin/demo" {
		t.Fatalf("spool %+v", spool)
	}
	if got := m.Analyze(nil); got.Score != check.Neutral {
		t.Fatalf("none %+v", got)
	}
}
