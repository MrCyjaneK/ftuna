package rpmfile

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mrcyjanek/ftuna/internal/analysis/check"
	"github.com/sassoftware/go-rpmutils"
)

const (
	tagPretrans      = 1151
	tagPosttrans     = 1152
	tagPretransProg  = 1153
	tagPosttransProg = 1154

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

	tagTransFileTriggerScripts = 5076
	tagTransFileTriggerProg    = 5077
	tagTransFileTriggerName    = 5079
	tagTransFileTriggerIndex   = 5080
	tagTransFileTriggerFlags   = 5082

	senseIn     = 1 << 16
	senseUn     = 1 << 17
	sensePostun = 1 << 18
)

type triggerSpec struct {
	prefix  string
	scripts int
	progs   int
	names   int
	index   int
	flags   int
}

func scriptlets(hdr *rpmutils.RpmHeader) ([]check.File, error) {
	var files []check.File
	steps := []struct {
		tag, prog int
		path      string
	}{
		{rpmutils.PREIN, rpmutils.PREINPROG, "%pre"},
		{rpmutils.POSTIN, rpmutils.POSTINPROG, "%post"},
		{rpmutils.PREUN, rpmutils.PREUNPROG, "%preun"},
		{rpmutils.POSTUN, rpmutils.POSTUNPROG, "%postun"},
		{tagPretrans, tagPretransProg, "%pretrans"},
		{tagPosttrans, tagPosttransProg, "%posttrans"},
		{rpmutils.VERIFYSCRIPT, rpmutils.VERIFYSCRIPTPROG, "%verifyscript"},
	}
	for _, step := range steps {
		if err := addScript(&files, hdr, step.tag, step.prog, step.path); err != nil {
			return nil, err
		}
	}
	groups := []triggerSpec{
		{"trigger", tagTriggerScripts, tagTriggerProg, tagTriggerName, tagTriggerIndex, tagTriggerFlags},
		{"filetrigger", tagFileTriggerScripts, tagFileTriggerProg, tagFileTriggerName, tagFileTriggerIndex, tagFileTriggerFlags},
		{"transfiletrigger", tagTransFileTriggerScripts, tagTransFileTriggerProg, tagTransFileTriggerName, tagTransFileTriggerIndex, tagTransFileTriggerFlags},
	}
	for _, spec := range groups {
		if err := addTriggers(&files, hdr, spec); err != nil {
			return nil, err
		}
	}
	return files, nil
}

func addScript(files *[]check.File, hdr *rpmutils.RpmHeader, tag, progTag int, path string) error {
	vals, err := optionalStrings(hdr, tag)
	if err != nil || len(vals) == 0 || vals[0] == "" {
		return err
	}
	prog, err := firstString(hdr, progTag)
	if err != nil {
		return err
	}
	*files = append(*files, check.File{
		Path:    path,
		Kind:    check.KindScriptlet,
		Body:    []byte(vals[0]),
		Program: prog,
	})
	return nil
}

func addTriggers(files *[]check.File, hdr *rpmutils.RpmHeader, spec triggerSpec) error {
	scripts, err := optionalStrings(hdr, spec.scripts)
	if err != nil || len(scripts) == 0 {
		return err
	}
	progs, err := optionalStrings(hdr, spec.progs)
	if err != nil {
		return err
	}
	names, err := optionalStrings(hdr, spec.names)
	if err != nil {
		return err
	}
	indexes, err := optionalUint32(hdr, spec.index)
	if err != nil {
		return err
	}
	flags, err := optionalUint32(hdr, spec.flags)
	if err != nil {
		return err
	}
	if len(indexes) != len(flags) || (len(names) > 0 && len(names) != len(indexes)) {
		return fmt.Errorf("%s tags disagree", spec.prefix)
	}
	for i, script := range scripts {
		if script == "" {
			continue
		}
		var labels []string
		for j, idx := range indexes {
			if int(idx) != i {
				continue
			}
			label := "%" + senseName(spec.prefix, flags[j])
			if j < len(names) && names[j] != "" {
				label += " " + names[j]
			}
			labels = append(labels, label)
		}
		path := strings.Join(labels, ", ")
		if path == "" {
			path = "%" + spec.prefix
		}
		prog := ""
		if i < len(progs) {
			prog = progs[i]
		}
		*files = append(*files, check.File{
			Path:    path,
			Kind:    check.KindScriptlet,
			Body:    []byte(script),
			Program: prog,
		})
	}
	return nil
}

func senseName(prefix string, flags uint32) string {
	switch {
	case flags&senseIn != 0:
		return prefix + "in"
	case flags&senseUn != 0:
		return prefix + "un"
	case flags&sensePostun != 0:
		return prefix + "postun"
	default:
		return prefix
	}
}

func firstString(hdr *rpmutils.RpmHeader, tag int) (string, error) {
	vals, err := optionalStrings(hdr, tag)
	if err != nil || len(vals) == 0 {
		return "", err
	}
	return vals[0], nil
}

func optionalStrings(hdr *rpmutils.RpmHeader, tag int) ([]string, error) {
	vals, err := hdr.GetStrings(tag)
	if err != nil {
		if missing(err) {
			return nil, nil
		}
		return nil, err
	}
	return vals, nil
}

func optionalUint32(hdr *rpmutils.RpmHeader, tag int) ([]uint32, error) {
	vals, err := hdr.GetUint32s(tag)
	if err != nil {
		if missing(err) {
			return nil, nil
		}
		return nil, err
	}
	return vals, nil
}

func missing(err error) bool {
	return errors.As(err, &rpmutils.NoSuchTagError{})
}
