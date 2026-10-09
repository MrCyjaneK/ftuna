// Package rpmfile reads an RPM in memory: header metadata, payload bytes, and scriptlets.
package rpmfile

import (
	"bytes"
	"fmt"
	"io"

	"github.com/mrcyjanek/ftuna/internal/analysis/check"
	"github.com/sassoftware/go-rpmutils"
)

const (
	modeType = 0170000
	modeDir  = 0040000
	modeLink = 0120000
)

// Identity is the package name, version, release, and architecture.
type Identity struct {
	Name    string
	Version string
	Release string
	Arch    string
}

// Read returns the package identity and every payload entry and scriptlet.
// File bodies are taken from the payload. Nothing is written to disk.
func Read(data []byte) (Identity, []check.File, error) {
	pkg, err := rpmutils.ReadRpm(bytes.NewReader(data))
	if err != nil {
		return Identity{}, nil, err
	}
	id, err := identity(pkg.Header)
	if err != nil {
		return Identity{}, nil, err
	}
	infos, err := pkg.Header.GetFiles()
	if err != nil {
		return id, nil, err
	}
	bodies, err := payloadBodies(pkg, infos)
	if err != nil {
		return id, nil, err
	}
	caps, err := fileCaps(pkg.Header, len(infos))
	if err != nil {
		return id, nil, err
	}
	files := make([]check.File, 0, len(infos))
	for i, info := range infos {
		file, err := fileFrom(info, bodies, caps[i])
		if err != nil {
			return id, nil, err
		}
		files = append(files, file)
	}
	scripts, err := scriptlets(pkg.Header)
	if err != nil {
		return id, nil, err
	}
	files = append(files, scripts...)
	return id, files, nil
}

func identity(hdr *rpmutils.RpmHeader) (Identity, error) {
	name, err := oneString(hdr, rpmutils.NAME)
	if err != nil {
		return Identity{}, err
	}
	version, err := oneString(hdr, rpmutils.VERSION)
	if err != nil {
		return Identity{}, err
	}
	release, err := oneString(hdr, rpmutils.RELEASE)
	if err != nil {
		return Identity{}, err
	}
	arch, err := oneString(hdr, rpmutils.ARCH)
	if err != nil {
		return Identity{}, err
	}
	if name == "" {
		return Identity{}, fmt.Errorf("rpm has no name")
	}
	return Identity{Name: name, Version: version, Release: release, Arch: arch}, nil
}

type payload struct {
	byName  map[string][]byte
	byInode map[inode][]byte
}

type inode struct {
	dev int
	ino int
}

func payloadBodies(pkg *rpmutils.Rpm, infos []rpmutils.FileInfo) (*payload, error) {
	out := &payload{
		byName:  map[string][]byte{},
		byInode: map[inode][]byte{},
	}
	if len(infos) == 0 {
		return out, nil
	}
	reader, err := pkg.PayloadReaderExtended()
	if err != nil {
		return nil, err
	}
	for {
		info, err := reader.Next()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(reader)
		if err != nil {
			return nil, err
		}
		out.byName[info.Name()] = body
		if len(body) > 0 && info.Inode() != 0 {
			out.byInode[inode{dev: info.Device(), ino: info.Inode()}] = body
		}
	}
}

func fileCaps(hdr *rpmutils.RpmHeader, n int) ([]string, error) {
	vals, err := optionalStrings(hdr, rpmutils.FILECAPS)
	if err != nil {
		return nil, err
	}
	if len(vals) == 0 {
		return make([]string, n), nil
	}
	if len(vals) != n {
		return nil, fmt.Errorf("file caps %d, files %d", len(vals), n)
	}
	return vals, nil
}

func fileFrom(info rpmutils.FileInfo, bodies *payload, caps string) (check.File, error) {
	kind := kindOf(info)
	file := check.File{
		Path:  info.Name(),
		Mode:  uint32(info.Mode()),
		User:  info.UserName(),
		Group: info.GroupName(),
		Kind:  kind,
		Caps:  caps,
	}
	if kind != check.KindFile {
		return file, nil
	}
	body, ok := bodies.byName[info.Name()]
	if !ok {
		return check.File{}, fmt.Errorf("payload missing %s", info.Name())
	}
	if len(body) == 0 {
		if shared, ok := bodies.byInode[inode{dev: info.Device(), ino: info.Inode()}]; ok && info.Inode() != 0 {
			body = shared
		}
	}
	file.Body = body
	return file, nil
}

func kindOf(info rpmutils.FileInfo) string {
	if info.Flags()&rpmutils.RPMFILE_GHOST != 0 {
		return check.KindGhost
	}
	switch uint32(info.Mode()) & modeType {
	case modeDir:
		return check.KindDir
	case modeLink:
		return check.KindSymlink
	default:
		return check.KindFile
	}
}

func oneString(hdr *rpmutils.RpmHeader, tag int) (string, error) {
	vals, err := hdr.GetStrings(tag)
	if err != nil {
		return "", err
	}
	if len(vals) == 0 {
		return "", nil
	}
	return vals[0], nil
}
