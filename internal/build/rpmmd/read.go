package rpmmd

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mrcyjanek/ftuna/internal/build/deps"
	"github.com/sassoftware/go-rpmutils"
)

const (
	senseLess    = 1 << 1
	senseGreater = 1 << 2
	senseEqual   = 1 << 3
	senseMask    = senseLess | senseGreater | senseEqual

	sensePostTrans    = 1 << 5
	sensePrereq       = 1 << 6
	sensePreTrans     = 1 << 7
	senseInterp       = 1 << 8
	senseScriptPre    = 1 << 9
	senseScriptPost   = 1 << 10
	senseScriptPreun  = 1 << 11
	senseScriptPostun = 1 << 12
	senseScriptVerify = 1 << 13
	sensePre          = sensePostTrans | sensePrereq | sensePreTrans | senseInterp | senseScriptPre | senseScriptPost | senseScriptPreun | senseScriptPostun | senseScriptVerify

	tagRecommendName    = 5046
	tagRecommendVersion = 5047
	tagRecommendFlags   = 5048
	tagSuggestName      = 5049
	tagSuggestVersion   = 5050
	tagSuggestFlags     = 5051
	tagSupplementName   = 5052
	tagSupplementVer    = 5053
	tagSupplementFlags  = 5054
	tagEnhanceName      = 5055
	tagEnhanceVersion   = 5056
	tagEnhanceFlags     = 5057

	modeType = 0170000
	modeDir  = 0040000
)

// Package is one RPM's contribution to primary, filelists, and other.
type Package struct {
	Href      string
	SHA256    string
	Primary   string
	Filelists string
	Other     string
}

// Read returns the name, version, release, summary, requires, and provides of an RPM.
// Requires use the same text as rpm -qp --requires. Provides are capability names.
func Read(path string) (deps.RPM, error) {
	rec, err := inspect(path, filepath.Base(path))
	if err != nil {
		return deps.RPM{}, err
	}
	rec.RPM.File = filepath.Base(path)
	return rec.RPM, nil
}

// Inspect reads path and returns the rpm-md fragments for a package stored at href.
func Inspect(path, href string) (Package, error) {
	href, err := cleanHref(href)
	if err != nil {
		return Package{}, err
	}
	rec, err := inspect(path, href)
	if err != nil {
		return Package{}, err
	}
	return rec.Package, nil
}

type inspected struct {
	RPM     deps.RPM
	Package Package
}

func inspect(path, href string) (inspected, error) {
	f, err := os.Open(path)
	if err != nil {
		return inspected{}, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return inspected{}, err
	}
	sum := sha256.New()
	pkg, err := rpmutils.ReadRpm(io.TeeReader(f, sum))
	if err != nil {
		return inspected{}, fmt.Errorf("%s: %w", path, err)
	}
	if _, err := io.Copy(sum, f); err != nil {
		return inspected{}, err
	}
	hdr := pkg.Header
	name, err := hdr.GetString(rpmutils.NAME)
	if err != nil || name == "" {
		return inspected{}, fmt.Errorf("%s: rpm has no name", path)
	}
	version, err := optString(hdr, rpmutils.VERSION)
	if err != nil {
		return inspected{}, err
	}
	release, err := optString(hdr, rpmutils.RELEASE)
	if err != nil {
		return inspected{}, err
	}
	summary, err := optString(hdr, rpmutils.SUMMARY)
	if err != nil {
		return inspected{}, err
	}
	requires, err := readDeps(hdr, rpmutils.REQUIRENAME, rpmutils.REQUIREVERSION, rpmutils.REQUIREFLAGS)
	if err != nil {
		return inspected{}, fmt.Errorf("%s: %w", path, err)
	}
	provides, err := readDeps(hdr, rpmutils.PROVIDENAME, rpmutils.PROVIDEVERSION, rpmutils.PROVIDEFLAGS)
	if err != nil {
		return inspected{}, fmt.Errorf("%s: %w", path, err)
	}
	reqText := make([]string, 0, len(requires))
	for _, dep := range requires {
		reqText = append(reqText, dep.text())
	}
	provNames := make([]string, 0, len(provides))
	for _, dep := range provides {
		if cap := deps.Capability(dep.Name); cap != "" {
			provNames = append(provNames, cap)
		}
	}
	digest := hex.EncodeToString(sum.Sum(nil))
	fragments, err := fragments(hdr, href, digest, st.Size(), st.ModTime().Unix(), requires, provides)
	if err != nil {
		return inspected{}, fmt.Errorf("%s: %w", path, err)
	}
	return inspected{
		RPM: deps.RPM{
			File:     href,
			Name:     name,
			Version:  version,
			Release:  release,
			Summary:  summary,
			Provides: provNames,
			Requires: reqText,
			SHA256:   digest,
		},
		Package: Package{
			Href:      href,
			SHA256:    digest,
			Primary:   fragments[0],
			Filelists: fragments[1],
			Other:     fragments[2],
		},
	}, nil
}

type dep struct {
	Name  string
	EVR   string
	Flags uint32
}

func (d dep) text() string {
	op := senseOp(d.Flags)
	if op == "" || d.EVR == "" {
		return d.Name
	}
	return d.Name + " " + op + " " + d.EVR
}

func senseOp(flags uint32) string {
	switch flags & senseMask {
	case senseLess:
		return "<"
	case senseGreater:
		return ">"
	case senseEqual:
		return "="
	case senseLess | senseEqual:
		return "<="
	case senseGreater | senseEqual:
		return ">="
	default:
		return ""
	}
}

func flagAttr(flags uint32) string {
	switch flags & senseMask {
	case senseLess:
		return "LT"
	case senseGreater:
		return "GT"
	case senseEqual:
		return "EQ"
	case senseLess | senseEqual:
		return "LE"
	case senseGreater | senseEqual:
		return "GE"
	default:
		return ""
	}
}

func readDeps(hdr *rpmutils.RpmHeader, nameTag, verTag, flagTag int) ([]dep, error) {
	names, err := optStrings(hdr, nameTag)
	if err != nil || len(names) == 0 {
		return nil, err
	}
	vers, err := optStrings(hdr, verTag)
	if err != nil {
		return nil, err
	}
	flags, err := optUint32s(hdr, flagTag)
	if err != nil {
		return nil, err
	}
	if len(vers) != len(names) || len(flags) != len(names) {
		return nil, fmt.Errorf("dependency tag lengths disagree")
	}
	out := make([]dep, len(names))
	for i, name := range names {
		out[i] = dep{Name: name, EVR: vers[i], Flags: flags[i]}
	}
	return out, nil
}

func optString(hdr *rpmutils.RpmHeader, tag int) (string, error) {
	if !hdr.HasTag(tag) {
		return "", nil
	}
	return hdr.GetString(tag)
}

func optStrings(hdr *rpmutils.RpmHeader, tag int) ([]string, error) {
	if !hdr.HasTag(tag) {
		return nil, nil
	}
	return hdr.GetStrings(tag)
}

func optUint32s(hdr *rpmutils.RpmHeader, tag int) ([]uint32, error) {
	if !hdr.HasTag(tag) {
		return nil, nil
	}
	return hdr.GetUint32s(tag)
}

func epochOf(hdr *rpmutils.RpmHeader) string {
	vals, err := optUint32s(hdr, rpmutils.EPOCH)
	if err != nil || len(vals) == 0 {
		return "0"
	}
	return strconv.FormatUint(uint64(vals[0]), 10)
}

func splitEVR(evr string) (epoch, ver, rel string) {
	evr = strings.TrimSpace(evr)
	if evr == "" {
		return "", "", ""
	}
	epoch = "0"
	if i := strings.IndexByte(evr, ':'); i >= 0 {
		epoch = evr[:i]
		evr = evr[i+1:]
	}
	if i := strings.LastIndexByte(evr, '-'); i >= 0 {
		return epoch, evr[:i], evr[i+1:]
	}
	return epoch, evr, ""
}
