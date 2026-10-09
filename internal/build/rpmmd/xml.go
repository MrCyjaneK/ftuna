package rpmmd

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"path"
	"strings"

	"github.com/sassoftware/go-rpmutils"
)

const (
	nsCommon    = "http://linux.duke.edu/metadata/common"
	nsRPM       = "http://linux.duke.edu/metadata/rpm"
	nsFilelists = "http://linux.duke.edu/metadata/filelists"
	nsOther     = "http://linux.duke.edu/metadata/other"
	nsRepo      = "http://linux.duke.edu/metadata/repo"
)

func fragments(hdr *rpmutils.RpmHeader, href, digest string, fileSize, fileTime int64, requires, provides []dep) ([3]string, error) {
	name, err := hdr.GetString(rpmutils.NAME)
	if err != nil {
		return [3]string{}, err
	}
	version, err := optString(hdr, rpmutils.VERSION)
	if err != nil {
		return [3]string{}, err
	}
	release, err := optString(hdr, rpmutils.RELEASE)
	if err != nil {
		return [3]string{}, err
	}
	arch, err := optString(hdr, rpmutils.ARCH)
	if err != nil {
		return [3]string{}, err
	}
	epoch := epochOf(hdr)
	files, err := hdr.GetFiles()
	if err != nil {
		return [3]string{}, err
	}
	var primaryFiles, allFiles []string
	for _, info := range files {
		entry, primary := fileEntry(info)
		allFiles = append(allFiles, entry)
		if primary {
			primaryFiles = append(primaryFiles, entry)
		}
	}
	conflicts, err := readDeps(hdr, rpmutils.CONFLICTNAME, rpmutils.CONFLICTVERSION, rpmutils.CONFLICTFLAGS)
	if err != nil {
		return [3]string{}, err
	}
	obsoletes, err := readDeps(hdr, rpmutils.OBSOLETENAME, rpmutils.OBSOLETEVERSION, rpmutils.OBSOLETEFLAGS)
	if err != nil {
		return [3]string{}, err
	}
	recommends, err := readDeps(hdr, tagRecommendName, tagRecommendVersion, tagRecommendFlags)
	if err != nil {
		return [3]string{}, err
	}
	suggests, err := readDeps(hdr, tagSuggestName, tagSuggestVersion, tagSuggestFlags)
	if err != nil {
		return [3]string{}, err
	}
	supplements, err := readDeps(hdr, tagSupplementName, tagSupplementVer, tagSupplementFlags)
	if err != nil {
		return [3]string{}, err
	}
	enhances, err := readDeps(hdr, tagEnhanceName, tagEnhanceVersion, tagEnhanceFlags)
	if err != nil {
		return [3]string{}, err
	}
	summary, err := optString(hdr, rpmutils.SUMMARY)
	if err != nil {
		return [3]string{}, err
	}
	description, err := optString(hdr, rpmutils.DESCRIPTION)
	if err != nil {
		return [3]string{}, err
	}
	packager, err := optString(hdr, rpmutils.PACKAGER)
	if err != nil {
		return [3]string{}, err
	}
	url, err := optString(hdr, rpmutils.URL)
	if err != nil {
		return [3]string{}, err
	}
	license, err := optString(hdr, rpmutils.LICENSE)
	if err != nil {
		return [3]string{}, err
	}
	vendor, err := optString(hdr, rpmutils.VENDOR)
	if err != nil {
		return [3]string{}, err
	}
	group, err := optString(hdr, rpmutils.GROUP)
	if err != nil {
		return [3]string{}, err
	}
	buildHost, err := optString(hdr, rpmutils.BUILDHOST)
	if err != nil {
		return [3]string{}, err
	}
	source, err := optString(hdr, rpmutils.SOURCERPM)
	if err != nil {
		return [3]string{}, err
	}
	var buildTime int64
	if times, err := optUint32s(hdr, rpmutils.BUILDTIME); err != nil {
		return [3]string{}, err
	} else if len(times) > 0 {
		buildTime = int64(times[0])
	}
	installed, err := hdr.InstalledSize()
	if err != nil {
		installed = 0
	}
	archive, err := hdr.PayloadSize()
	if err != nil {
		archive = 0
	}
	if fileTime < 0 {
		fileTime = 0
	}
	span := hdr.GetRange()
	var primary strings.Builder
	fmt.Fprintf(&primary, `<package type="rpm"><name>%s</name><arch>%s</arch>`, xmlText(name), xmlText(arch))
	writeVersion(&primary, epoch, version, release)
	fmt.Fprintf(&primary, `<checksum type="sha256" pkgid="YES">%s</checksum>`, digest)
	fmt.Fprintf(&primary, `<summary>%s</summary><description>%s</description>`, xmlText(summary), xmlText(description))
	if packager != "" {
		fmt.Fprintf(&primary, `<packager>%s</packager>`, xmlText(packager))
	}
	if url != "" {
		fmt.Fprintf(&primary, `<url>%s</url>`, xmlText(url))
	}
	fmt.Fprintf(&primary, `<time file="%d" build="%d"/>`, fileTime, buildTime)
	fmt.Fprintf(&primary, `<size package="%d" installed="%d" archive="%d"/>`, fileSize, installed, archive)
	fmt.Fprintf(&primary, `<location href="%s"/>`, xmlAttr(href))
	primary.WriteString(`<format>`)
	writeOpt(&primary, "license", license)
	writeOpt(&primary, "vendor", vendor)
	writeOpt(&primary, "group", group)
	writeOpt(&primary, "buildhost", buildHost)
	writeOpt(&primary, "sourcerpm", source)
	fmt.Fprintf(&primary, `<rpm:header-range start="%d" end="%d"/>`, span.Start, span.End)
	writeDeps(&primary, "provides", provides, false)
	writeDeps(&primary, "requires", requires, true)
	writeDeps(&primary, "conflicts", conflicts, false)
	writeDeps(&primary, "obsoletes", obsoletes, false)
	writeDeps(&primary, "recommends", recommends, false)
	writeDeps(&primary, "suggests", suggests, false)
	writeDeps(&primary, "supplements", supplements, false)
	writeDeps(&primary, "enhances", enhances, false)
	for _, entry := range primaryFiles {
		primary.WriteString(entry)
	}
	primary.WriteString(`</format></package>`)

	var lists strings.Builder
	fmt.Fprintf(&lists, `<package pkgid="%s" name="%s" arch="%s">`, digest, xmlAttr(name), xmlAttr(arch))
	writeVersion(&lists, epoch, version, release)
	for _, entry := range allFiles {
		lists.WriteString(entry)
	}
	lists.WriteString(`</package>`)

	changelogs, err := changelogEntries(hdr)
	if err != nil {
		return [3]string{}, err
	}
	var other strings.Builder
	fmt.Fprintf(&other, `<package pkgid="%s" name="%s" arch="%s">`, digest, xmlAttr(name), xmlAttr(arch))
	writeVersion(&other, epoch, version, release)
	other.WriteString(changelogs)
	other.WriteString(`</package>`)
	return [3]string{primary.String(), lists.String(), other.String()}, nil
}

func writeVersion(b *strings.Builder, epoch, ver, rel string) {
	fmt.Fprintf(b, `<version epoch="%s" ver="%s" rel="%s"/>`, xmlAttr(epoch), xmlAttr(ver), xmlAttr(rel))
}

func writeOpt(b *strings.Builder, tag, value string) {
	if value == "" {
		return
	}
	fmt.Fprintf(b, `<rpm:%s>%s</rpm:%s>`, tag, xmlText(value), tag)
}

func writeDeps(b *strings.Builder, tag string, deps []dep, withPre bool) {
	if len(deps) == 0 {
		return
	}
	fmt.Fprintf(b, `<rpm:%s>`, tag)
	for _, dep := range deps {
		fmt.Fprintf(b, `<rpm:entry name="%s"`, xmlAttr(dep.Name))
		flags := flagAttr(dep.Flags)
		if flags != "" && dep.EVR != "" {
			epoch, ver, rel := splitEVR(dep.EVR)
			fmt.Fprintf(b, ` flags="%s" epoch="%s" ver="%s"`, flags, xmlAttr(epoch), xmlAttr(ver))
			if rel != "" {
				fmt.Fprintf(b, ` rel="%s"`, xmlAttr(rel))
			}
		}
		if withPre && dep.Flags&sensePre != 0 {
			b.WriteString(` pre="1"`)
		}
		b.WriteString(`/>`)
	}
	fmt.Fprintf(b, `</rpm:%s>`, tag)
}

func fileEntry(info rpmutils.FileInfo) (string, bool) {
	name := info.Name()
	flags := info.Flags()
	mode := info.Mode()
	primary := inPrimary(name, flags)
	var typ string
	switch {
	case flags&rpmutils.RPMFILE_GHOST != 0:
		typ = "ghost"
	case uint32(mode)&modeType == modeDir:
		typ = "dir"
	}
	if typ == "" {
		return "<file>" + xmlText(name) + "</file>", primary
	}
	return `<file type="` + typ + `">` + xmlText(name) + "</file>", primary
}

func inPrimary(name string, flags int) bool {
	if flags&(rpmutils.RPMFILE_GHOST|rpmutils.RPMFILE_CONFIG) != 0 {
		return true
	}
	if name == "/usr/lib/sendmail" || strings.HasPrefix(name, "/usr/lib/sendmail/") {
		return true
	}
	for _, dir := range []string{"/etc", "/bin", "/sbin", "/usr/bin", "/usr/sbin"} {
		if name == dir || strings.HasPrefix(name, dir+"/") {
			return true
		}
	}
	base := path.Base(name)
	return strings.HasSuffix(base, ".so") || strings.Contains(base, ".so.")
}

func changelogEntries(hdr *rpmutils.RpmHeader) (string, error) {
	times, err := optUint32s(hdr, rpmutils.CHANGELOGTIME)
	if err != nil || len(times) == 0 {
		return "", err
	}
	names, err := optStrings(hdr, rpmutils.CHANGELOGNAME)
	if err != nil {
		return "", err
	}
	texts, err := optStrings(hdr, rpmutils.CHANGELOGTEXT)
	if err != nil {
		return "", err
	}
	if len(names) != len(times) || len(texts) != len(times) {
		return "", fmt.Errorf("changelog tag lengths disagree")
	}
	var b strings.Builder
	for i := range times {
		fmt.Fprintf(&b, `<changelog author="%s" date="%d">%s</changelog>`, xmlAttr(names[i]), times[i], xmlText(texts[i]))
	}
	return b.String(), nil
}

func xmlText(s string) string {
	var b strings.Builder
	xml.EscapeText(&b, []byte(s))
	return b.String()
}

func xmlAttr(s string) string {
	return strings.NewReplacer(`"`, `&#34;`, `'`, `&#39;`).Replace(xmlText(s))
}

func wrap(root, xmlns, extra string, pkgs []Package, frag func(Package) string) []byte {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	fmt.Fprintf(&b, `<%s xmlns="%s"`, root, xmlns)
	if extra != "" {
		b.WriteByte(' ')
		b.WriteString(extra)
	}
	fmt.Fprintf(&b, ` packages="%d">`, len(pkgs))
	for _, pkg := range pkgs {
		b.WriteString(strings.TrimSpace(frag(pkg)))
	}
	fmt.Fprintf(&b, "</%s>\n", root)
	return []byte(b.String())
}

type metaFile struct {
	Type         string
	Name         string
	Checksum     string
	OpenChecksum string
	Size         int64
	OpenSize     int64
}

func repomdXML(files []metaFile, revision int64) []byte {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	fmt.Fprintf(&b, `<repomd xmlns="%s" xmlns:rpm="%s">`, nsRepo, nsRPM)
	fmt.Fprintf(&b, `<revision>%d</revision>`, revision)
	for _, file := range files {
		fmt.Fprintf(&b, `<data type="%s">`, file.Type)
		fmt.Fprintf(&b, `<checksum type="sha256">%s</checksum>`, file.Checksum)
		fmt.Fprintf(&b, `<open-checksum type="sha256">%s</open-checksum>`, file.OpenChecksum)
		fmt.Fprintf(&b, `<location href="%s"/>`, xmlAttr("repodata/"+file.Name))
		fmt.Fprintf(&b, `<timestamp>%d</timestamp>`, revision)
		fmt.Fprintf(&b, `<size>%d</size>`, file.Size)
		fmt.Fprintf(&b, `<open-size>%d</open-size>`, file.OpenSize)
		b.WriteString(`</data>`)
	}
	b.WriteString(`</repomd>` + "\n")
	return []byte(b.String())
}

// GzipHrefs returns the gzip primary, filelists, and other locations from repomd.xml.
func GzipHrefs(repomd []byte) (primary, filelists, other string, err error) {
	var doc struct {
		Data []struct {
			Type     string `xml:"type,attr"`
			Location struct {
				Href string `xml:"href,attr"`
			} `xml:"location"`
		} `xml:"data"`
	}
	if err := xml.Unmarshal(repomd, &doc); err != nil {
		return "", "", "", err
	}
	for _, data := range doc.Data {
		href := strings.TrimSpace(data.Location.Href)
		if !strings.HasSuffix(href, ".xml.gz") {
			continue
		}
		switch data.Type {
		case "primary":
			primary = href
		case "filelists":
			filelists = href
		case "other":
			other = href
		}
	}
	if primary == "" || filelists == "" || other == "" {
		return "", "", "", fmt.Errorf("repomd has no gzip primary, filelists, and other")
	}
	return primary, filelists, other, nil
}

// Split reads uncompressed primary, filelists, and other documents and returns
// one Package per primary location. A package missing from filelists or other
// gets an empty fragment so the three documents stay aligned.
func Split(primary, filelists, other []byte) (map[string]Package, error) {
	primaries, err := eachPackage(primary)
	if err != nil {
		return nil, fmt.Errorf("primary: %w", err)
	}
	lists, err := packagesByID(filelists)
	if err != nil {
		return nil, fmt.Errorf("filelists: %w", err)
	}
	others, err := packagesByID(other)
	if err != nil {
		return nil, fmt.Errorf("other: %w", err)
	}
	out := make(map[string]Package, len(primaries))
	for _, raw := range primaries {
		id, href, err := primaryIdentity(raw)
		if err != nil {
			return nil, err
		}
		fl := lists[id.pkgid]
		if fl == "" {
			fl = emptySide(id)
		}
		ot := others[id.pkgid]
		if ot == "" {
			ot = emptySide(id)
		}
		if _, ok := out[href]; ok {
			return nil, fmt.Errorf("duplicate location %s", href)
		}
		out[href] = Package{Href: href, SHA256: id.pkgid, Primary: string(raw), Filelists: fl, Other: ot}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("primary lists no packages")
	}
	return out, nil
}

type sideID struct {
	pkgid, name, arch, epoch, ver, rel string
}

func primaryIdentity(raw []byte) (sideID, string, error) {
	var doc struct {
		Name    string `xml:"name"`
		Arch    string `xml:"arch"`
		Version struct {
			Epoch string `xml:"epoch,attr"`
			Ver   string `xml:"ver,attr"`
			Rel   string `xml:"rel,attr"`
		} `xml:"version"`
		Checksum struct {
			Value string `xml:",chardata"`
		} `xml:"checksum"`
		Location struct {
			Href string `xml:"href,attr"`
		} `xml:"location"`
	}
	if err := xml.Unmarshal(raw, &doc); err != nil {
		return sideID{}, "", err
	}
	href, err := cleanHref(doc.Location.Href)
	if err != nil {
		return sideID{}, "", err
	}
	id := sideID{
		pkgid: strings.ToLower(strings.TrimSpace(doc.Checksum.Value)),
		name:  strings.TrimSpace(doc.Name),
		arch:  strings.TrimSpace(doc.Arch),
		epoch: strings.TrimSpace(doc.Version.Epoch),
		ver:   strings.TrimSpace(doc.Version.Ver),
		rel:   strings.TrimSpace(doc.Version.Rel),
	}
	if id.pkgid == "" || id.name == "" {
		return sideID{}, "", fmt.Errorf("package %s has no checksum", href)
	}
	if id.epoch == "" {
		id.epoch = "0"
	}
	return id, href, nil
}

func packagesByID(doc []byte) (map[string]string, error) {
	raws, err := eachPackage(doc)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(raws))
	for _, raw := range raws {
		var doc struct {
			PkgID string `xml:"pkgid,attr"`
		}
		if err := xml.Unmarshal(raw, &doc); err != nil {
			return nil, err
		}
		id := strings.ToLower(strings.TrimSpace(doc.PkgID))
		if id == "" {
			return nil, fmt.Errorf("package has no pkgid")
		}
		out[id] = string(raw)
	}
	return out, nil
}

func eachPackage(doc []byte) ([][]byte, error) {
	var out [][]byte
	rest := doc
	for {
		i := bytes.Index(rest, []byte("<package"))
		if i < 0 {
			return out, nil
		}
		if i+len("<package") < len(rest) {
			switch rest[i+len("<package")] {
			case ' ', '>', '/', '\n', '\t', '\r':
			default:
				rest = rest[i+len("<package"):]
				continue
			}
		}
		chunk := rest[i:]
		end := bytes.Index(chunk, []byte("</package>"))
		if end < 0 {
			return nil, fmt.Errorf("unclosed package element")
		}
		out = append(out, chunk[:end+len("</package>")])
		rest = chunk[end+len("</package>"):]
	}
}

func emptySide(id sideID) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<package pkgid="%s" name="%s" arch="%s">`, id.pkgid, xmlAttr(id.name), xmlAttr(id.arch))
	writeVersion(&b, id.epoch, id.ver, id.rel)
	b.WriteString(`</package>`)
	return b.String()
}

func cleanHref(href string) (string, error) {
	href = strings.TrimSpace(href)
	if href == "" || strings.HasPrefix(href, "/") || strings.Contains(href, "\\") {
		return "", fmt.Errorf("bad location %q", href)
	}
	clean := path.Clean(href)
	if clean == "." || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") {
		return "", fmt.Errorf("bad location %q", href)
	}
	return clean, nil
}

// ListsSQLite reports whether repomd.xml publishes a sqlite database.
func ListsSQLite(repomd []byte) bool {
	return bytes.Contains(repomd, []byte(".sqlite"))
}
