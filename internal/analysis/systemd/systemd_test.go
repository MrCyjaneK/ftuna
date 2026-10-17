package systemd

import (
	"strings"
	"testing"

	"github.com/mrcyjanek/ftuna/internal/analysis/check"
)

func TestServiceAccounts(t *testing.T) {
	m := module{}
	cases := []struct {
		path, body, detail, color string
	}{
		{
			"/usr/lib/systemd/system/demo.service",
			"[Service]\nExecStart=/usr/bin/demo\n",
			"scope=system user=root group=root exec=/usr/bin/demo sandbox=none",
			check.ColorRed,
		},
		{
			"/usr/lib/systemd/user/demo.service",
			"[Service]\nExecStart=/usr/bin/demo\n",
			"scope=user user=session group=session exec=/usr/bin/demo sandbox=none",
			check.ColorGreen,
		},
		{
			"/etc/systemd/system/demo.service",
			"[Service]\nUser=nemo\nGroup=nemo\nExecStart=/usr/bin/demo\n",
			"scope=system user=nemo group=nemo exec=/usr/bin/demo sandbox=none",
			check.ColorYellow,
		},
		{
			"/usr/lib/systemd/system/demo.service",
			"[Service]\nDynamicUser=yes\nExecStart=/usr/bin/demo\n",
			"scope=system user=dynamic group=dynamic exec=/usr/bin/demo sandbox=none",
			check.ColorGreen,
		},
		{
			"/usr/lib/systemd/system/demo.service",
			"[Service]\nUser=nemo\nGroup=root\nExecStart=/usr/bin/demo\n",
			"scope=system user=nemo group=root exec=/usr/bin/demo sandbox=none",
			check.ColorYellow,
		},
		{
			"/usr/lib/systemd/user/demo.service",
			"[Service]\nUser=root\nExecStart=/usr/bin/demo\n",
			"scope=user user=root group=session exec=/usr/bin/demo sandbox=none",
			check.ColorRed,
		},
	}
	for _, tc := range cases {
		rep := m.Analyze([]check.File{{Path: tc.path, Kind: check.KindFile, Body: []byte(tc.body)}})
		if len(rep.Issues) != 1 || rep.Issues[0].Code != "systemd.service" || rep.Issues[0].Color != tc.color || rep.Issues[0].Detail != tc.detail {
			t.Fatalf("%s %+v", tc.path, rep.Issues)
		}
	}
}

func TestSandboxAndTimer(t *testing.T) {
	m := module{}
	sandboxed := `[Service]
User=nemo
Group=nemo
ExecStart=/usr/bin/demo
NoNewPrivileges=yes
ProtectSystem=strict
PrivateTmp=yes
PrivateDevices=yes
PrivateNetwork=yes
PrivateUsers=yes
MemoryDenyWriteExecute=yes
RestrictSUIDSGID=yes
RestrictNamespaces=yes
ProtectKernelModules=yes
ProtectHome=yes
SystemCallFilter=@system-service
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
`
	rep := m.Analyze([]check.File{{
		Path: "/usr/lib/systemd/system/demo.service", Kind: check.KindFile, Body: []byte(sandboxed),
	}})
	if len(rep.Issues) != 1 || rep.Issues[0].Color != check.ColorGreen {
		t.Fatalf("sandboxed %+v", rep.Issues)
	}
	if rep.Issues[0].Detail != "scope=system user=nemo group=nemo exec=/usr/bin/demo sandbox=ProtectSystem=strict,ProtectHome,PrivateTmp,PrivateDevices,PrivateNetwork,PrivateUsers,NoNewPrivileges,MemoryDenyWriteExecute,RestrictSUIDSGID,RestrictNamespaces,ProtectKernelModules,SystemCallFilter,CapabilityBoundingSet" {
		t.Fatalf("detail %s", rep.Issues[0].Detail)
	}
	rootBox := m.Analyze([]check.File{{
		Path: "/usr/lib/systemd/system/demo.service", Kind: check.KindFile,
		Body: []byte(strings.Replace(sandboxed, "User=nemo\nGroup=nemo\n", "", 1)),
	}})
	if len(rootBox.Issues) != 1 || rootBox.Issues[0].Color != check.ColorGreen || rootBox.Issues[0].Detail != "scope=system user=root group=root exec=/usr/bin/demo sandbox=ProtectSystem=strict,ProtectHome,PrivateTmp,PrivateDevices,PrivateNetwork,PrivateUsers,NoNewPrivileges,MemoryDenyWriteExecute,RestrictSUIDSGID,RestrictNamespaces,ProtectKernelModules,SystemCallFilter,CapabilityBoundingSet" {
		t.Fatalf("sandboxed root %+v", rootBox.Issues)
	}

	ambient := m.Analyze([]check.File{{
		Path: "/usr/lib/systemd/system/demo.service", Kind: check.KindFile,
		Body: []byte("[Service]\nAmbientCapabilities=CAP_NET_BIND_SERVICE\n"),
	}})
	if len(ambient.Issues) != 1 || ambient.Issues[0].Color != check.ColorRed || ambient.Issues[0].Detail != "scope=system user=root group=root exec=unknown ambient=CAP_NET_BIND_SERVICE sandbox=none" {
		t.Fatalf("ambient %+v", ambient.Issues)
	}

	userTimer := m.Analyze([]check.File{{
		Path: "/usr/lib/systemd/user/demo.timer", Kind: check.KindFile,
		Body: []byte("[Timer]\nOnCalendar=daily\n"),
	}})
	if len(userTimer.Issues) != 1 || userTimer.Issues[0].Color != check.ColorGreen || userTimer.Issues[0].Code != "systemd.timer" || userTimer.Issues[0].Detail != "scope=user activates=demo.service" {
		t.Fatalf("user timer %+v", userTimer)
	}
	systemTimer := m.Analyze([]check.File{{
		Path: "/lib/systemd/system/backup.timer", Kind: check.KindFile,
		Body: []byte("[Timer]\nUnit=backup.service\nOnBootSec=5min\n"),
	}})
	if len(systemTimer.Issues) != 1 || systemTimer.Issues[0].Color != check.ColorRed || systemTimer.Issues[0].Detail != "scope=system activates=backup.service" {
		t.Fatalf("system timer %+v", systemTimer)
	}

	socket := m.Analyze([]check.File{{
		Path: "/usr/lib/systemd/system/demo.socket", Kind: check.KindFile,
		Body: []byte("[Socket]\nListenStream=80\nService=worker.service\n"),
	}})
	if len(socket.Issues) != 1 || socket.Issues[0].Color != check.ColorRed || socket.Issues[0].Code != "systemd.socket" || socket.Issues[0].Detail != "scope=system activates=worker.service" {
		t.Fatalf("socket %+v", socket)
	}
	userPath := m.Analyze([]check.File{{
		Path: "/usr/lib/systemd/user/demo.path", Kind: check.KindFile,
		Body: []byte("[Path]\nPathChanged=/var/lib/demo\n"),
	}})
	if len(userPath.Issues) != 1 || userPath.Issues[0].Color != check.ColorGreen || userPath.Issues[0].Code != "systemd.path" || userPath.Issues[0].Detail != "scope=user activates=demo.service" {
		t.Fatalf("path %+v", userPath)
	}
	withService := m.Analyze([]check.File{
		{Path: "/usr/lib/systemd/system/demo.service", Kind: check.KindFile, Body: []byte("[Service]\nExecStart=/usr/bin/demo\n")},
		{Path: "/usr/lib/systemd/system/demo.socket", Kind: check.KindFile, Body: []byte("[Socket]\nListenStream=1\n")},
		{Path: "/usr/lib/systemd/system/demo.path", Kind: check.KindFile, Body: []byte("[Path]\nUnit=demo.service\n")},
	})
	if len(withService.Issues) != 3 || withService.Issues[0].Color != check.ColorRed || withService.Issues[1].Color != check.ColorRed || withService.Issues[2].Color != check.ColorRed {
		t.Fatalf("with service %+v", withService.Issues)
	}
}

func TestEachUnit(t *testing.T) {
	m := module{}
	rep := m.Analyze([]check.File{
		{Path: "/usr/lib/systemd/user/demo.service", Kind: check.KindFile, Body: []byte("[Service]\nExecStart=/usr/bin/demo\n")},
		{Path: "/usr/lib/systemd/system/demo.service", Kind: check.KindFile, Body: []byte("[Service]\nExecStart=/usr/bin/demo\n")},
		{Path: "/usr/lib/systemd/system/demo.timer", Kind: check.KindFile, Body: []byte("[Timer]\nOnCalendar=daily\n")},
	})
	if len(rep.Issues) != 3 {
		t.Fatalf("%+v", rep.Issues)
	}
	byPath := map[string]check.Issue{}
	for _, issue := range rep.Issues {
		byPath[issue.Path] = issue
	}
	if byPath["/usr/lib/systemd/user/demo.service"].Color != check.ColorGreen || byPath["/usr/lib/systemd/system/demo.service"].Color != check.ColorRed || byPath["/usr/lib/systemd/system/demo.timer"].Color != check.ColorRed {
		t.Fatalf("%+v", rep.Issues)
	}
	if byPath["/usr/lib/systemd/system/demo.timer"].Detail != "scope=system activates=demo.service" {
		t.Fatalf("timer %+v", byPath["/usr/lib/systemd/system/demo.timer"])
	}
}

func TestDropInAndAliases(t *testing.T) {
	m := module{}
	rep := m.Analyze([]check.File{
		{Path: "/usr/lib/systemd/system/demo.service", Kind: check.KindFile, Body: []byte("[Service]\nExecStart=/usr/bin/demo\n")},
		{Path: "/usr/lib/systemd/system/demo.service.d/10-user.conf", Kind: check.KindFile, Body: []byte("[Service]\nUser=nemo\nGroup=nemo\n")},
		{Path: "/usr/lib/systemd/system/demo.service.d/20-sandbox.conf", Kind: check.KindFile, Body: []byte("[Service]\nNoNewPrivileges=yes\nProtectSystem=strict\n")},
		{Path: "/etc/systemd/system/demo.service", Kind: check.KindSymlink},
		{Path: "/usr/lib/systemd/system/multi-user.target.wants/demo.service", Kind: check.KindSymlink},
		{Path: "/usr/lib/systemd/system/demo.socket", Kind: check.KindFile, Body: []byte("[Socket]\nListenStream=1\n")},
		{Path: "/usr/lib/systemd/system/demo.service", Kind: check.KindDir},
		{Path: "%post", Kind: check.KindScriptlet, Body: []byte("systemctl enable demo.service\n")},
	})
	if len(rep.Issues) != 2 || rep.Issues[0].Color != check.ColorGreen || rep.Issues[1].Color != check.ColorRed {
		t.Fatalf("%+v", rep.Issues)
	}
	if rep.Issues[0].Path != "/usr/lib/systemd/system/demo.service" || rep.Issues[0].Detail != "scope=system user=nemo group=nemo exec=/usr/bin/demo sandbox=ProtectSystem=strict,NoNewPrivileges" {
		t.Fatalf("%+v", rep.Issues[0])
	}
	if rep.Issues[1].Code != "systemd.socket" || rep.Issues[1].Detail != "scope=system activates=demo.service" {
		t.Fatalf("%+v", rep.Issues[1])
	}

	link := m.Analyze([]check.File{{
		Path: "/etc/systemd/system/demo.service", Kind: check.KindSymlink,
	}})
	if len(link.Issues) != 1 || link.Issues[0].Color != check.ColorRed || link.Issues[0].Detail != "scope=system user=root group=root exec=unknown sandbox=unknown" {
		t.Fatalf("link %+v", link)
	}
}

func TestUnitDirs(t *testing.T) {
	m := module{}
	paths := []string{
		"/usr/lib/systemd/system/demo.service",
		"/usr/lib/systemd/user/demo.service",
		"/lib/systemd/system/demo.service",
		"/lib/systemd/user/demo.service",
		"/etc/systemd/system/demo.service",
		"/etc/systemd/user/demo.service",
	}
	for _, path := range paths {
		rep := m.Analyze([]check.File{{Path: path, Kind: check.KindFile, Body: []byte("[Service]\nExecStart=/bin/demo\n")}})
		if len(rep.Issues) != 1 || rep.Issues[0].Code != "systemd.service" {
			t.Fatalf("%s %+v", path, rep)
		}
	}
	if rep := m.Analyze(nil); len(rep.Issues) != 0 {
		t.Fatalf("none %+v", rep)
	}
}
