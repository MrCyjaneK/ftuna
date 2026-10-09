// Package rpmsample builds an in-memory RPM for analysis tests.
package rpmsample

import (
	"bytes"

	"github.com/google/rpmpack"
)

const (
	tagTriggerScripts = 1065
	tagTriggerName    = 1066
	tagTriggerFlags   = 1068
	tagTriggerIndex   = 1069
	tagTriggerProg    = 1092

	tagFileTriggerScripts = 5066
	tagFileTriggerProg    = 5067
	tagFileTriggerName    = 5069
	tagFileTriggerIndex   = 5070
	tagFileTriggerFlags   = 5072

	senseIn = 1 << 16
)

// RPM returns a package with setuid and setgid binaries, a Sailjail desktop
// file, a systemd service, a ghost file, and scriptlets.
func RPM() ([]byte, error) {
	meta := rpmpack.RPMMetaData{
		Name: "demo", Version: "1.2.3", Release: "4", Arch: "aarch64",
	}
	rpm, err := rpmpack.NewRPM(meta)
	if err != nil {
		return nil, err
	}
	rpm.AddFile(rpmpack.RPMFile{
		Name: "/usr/bin/demo", Body: []byte("bin"), Mode: 04755, Owner: "root", Group: "root",
	})
	rpm.AddFile(rpmpack.RPMFile{
		Name: "/usr/bin/both", Body: []byte("both"), Mode: 06755, Owner: "root", Group: "wheel",
	})
	rpm.AddFile(rpmpack.RPMFile{
		Name: "/usr/bin/helper", Body: []byte("helper"), Mode: 02755, Owner: "root", Group: "root",
	})
	rpm.AddFile(rpmpack.RPMFile{
		Name: "/usr/bin/link", Body: []byte("/usr/bin/demo"), Mode: 0120777, Owner: "root", Group: "root",
	})
	rpm.AddFile(rpmpack.RPMFile{
		Name: "/usr/share/demo", Mode: 040755, Owner: "root", Group: "root",
	})
	rpm.AddFile(rpmpack.RPMFile{
		Name: "/usr/share/applications/demo.desktop",
		Body: []byte("[X-Sailjail]\nPermissions=Internet\n"),
		Mode: 0644, Owner: "root", Group: "root",
	})
	rpm.AddFile(rpmpack.RPMFile{
		Name: "/usr/share/sailjail/permissions/Internet.permission",
		Body: []byte("Internet\n"), Mode: 0644, Owner: "root", Group: "root",
	})
	rpm.AddFile(rpmpack.RPMFile{
		Name: "/usr/lib/systemd/system/demo.service",
		Body: []byte("[Service]\nExecStart=/usr/bin/demo\n"),
		Mode: 0644, Owner: "root", Group: "root",
	})
	rpm.AddFile(rpmpack.RPMFile{
		Name: "/usr/lib/systemd/system/multi-user.target.wants/demo.service",
		Body: []byte("/usr/lib/systemd/system/demo.service"),
		Mode: 0120777, Owner: "root", Group: "root",
	})
	rpm.AddFile(rpmpack.RPMFile{
		Name: "/var/log/demo.log", Body: []byte("log"), Mode: 0644, Owner: "root", Group: "root",
		Type: rpmpack.GhostFile,
	})
	rpm.AddPostin("echo post\n")
	rpm.AddPretrans("echo pretrans\n")
	rpm.AddVerifyScript("echo verify\n")
	rpm.AddCustomTag(tagTriggerScripts, rpmpack.EntryStringSlice([]string{"echo trigger\n"}))
	rpm.AddCustomTag(tagTriggerProg, rpmpack.EntryStringSlice([]string{"/bin/sh"}))
	rpm.AddCustomTag(tagTriggerName, rpmpack.EntryStringSlice([]string{"glibc"}))
	rpm.AddCustomTag(tagTriggerFlags, rpmpack.EntryInt32([]int32{senseIn}))
	rpm.AddCustomTag(tagTriggerIndex, rpmpack.EntryInt32([]int32{0}))
	rpm.AddCustomTag(tagFileTriggerScripts, rpmpack.EntryStringSlice([]string{"echo filetrigger\n"}))
	rpm.AddCustomTag(tagFileTriggerProg, rpmpack.EntryStringSlice([]string{"/bin/sh"}))
	rpm.AddCustomTag(tagFileTriggerName, rpmpack.EntryStringSlice([]string{"/usr/share/applications"}))
	rpm.AddCustomTag(tagFileTriggerFlags, rpmpack.EntryInt32([]int32{senseIn}))
	rpm.AddCustomTag(tagFileTriggerIndex, rpmpack.EntryInt32([]int32{0}))
	var buf bytes.Buffer
	if err := rpm.Write(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
