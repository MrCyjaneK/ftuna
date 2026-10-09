package openrepos

import (
	"compress/bzip2"
	"compress/gzip"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"hash"
	"io"
	"os"
	"strconv"
	"strings"
)

// looseInt reads an integer attribute from primary.xml. Guix writes None
// when the RPM header has no build time.
type looseInt int64

func (n *looseInt) UnmarshalXMLAttr(attr xml.Attr) error {
	v := strings.TrimSpace(attr.Value)
	if v == "" || strings.EqualFold(v, "none") || strings.EqualFold(v, "null") {
		*n = 0
		return nil
	}
	parsed, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		*n = 0
		return nil
	}
	*n = looseInt(parsed)
	return nil
}

type repoData struct {
	Type         string
	Href         string
	Checksum     string
	ChecksumType string
	OpenChecksum string
	OpenType     string
}

type repoMD struct {
	Data []repoData
}

type pkgRef struct {
	Name        string
	Arch        string
	Version     string
	Release     string
	Href        string
	Checksum    string
	SumType     string
	Size        int64
	Summary     string
	Description string
	Source      string
	Build       int64
	Requires    []string
	App         bool
	SHA256      string
}

func parseRepoMD(b []byte) (repoMD, error) {
	var doc struct {
		Data []struct {
			Type     string `xml:"type,attr"`
			Location struct {
				Href string `xml:"href,attr"`
			} `xml:"location"`
			Checksum struct {
				Type  string `xml:"type,attr"`
				Value string `xml:",chardata"`
			} `xml:"checksum"`
			OpenChecksum struct {
				Type  string `xml:"type,attr"`
				Value string `xml:",chardata"`
			} `xml:"open-checksum"`
		} `xml:"data"`
	}
	if err := xml.Unmarshal(b, &doc); err != nil {
		return repoMD{}, err
	}
	if len(doc.Data) == 0 {
		return repoMD{}, fmt.Errorf("repomd has no data entries")
	}
	out := repoMD{}
	for _, d := range doc.Data {
		href, err := safeRel(strings.TrimSpace(d.Location.Href))
		if err != nil {
			return repoMD{}, fmt.Errorf("repomd %s: %w", d.Type, err)
		}
		out.Data = append(out.Data, repoData{
			Type:         d.Type,
			Href:         href,
			Checksum:     strings.TrimSpace(d.Checksum.Value),
			ChecksumType: strings.TrimSpace(d.Checksum.Type),
			OpenChecksum: strings.TrimSpace(d.OpenChecksum.Value),
			OpenType:     strings.TrimSpace(d.OpenChecksum.Type),
		})
	}
	return out, nil
}

func parsePrimary(path, name string) ([]pkgRef, error) {
	r, err := openMetadata(path, name)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return parsePrimaryXML(r)
}

func parsePrimaryXML(r io.Reader) ([]pkgRef, error) {
	dec := xml.NewDecoder(r)
	var pkgs []pkgRef
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return pkgs, nil
		}
		if err != nil {
			return nil, err
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "package" {
			continue
		}
		var p struct {
			Name        string `xml:"name"`
			Arch        string `xml:"arch"`
			Summary     string `xml:"summary"`
			Description string `xml:"description"`
			Version     struct {
				Ver string `xml:"ver,attr"`
				Rel string `xml:"rel,attr"`
			} `xml:"version"`
			Checksum struct {
				Type  string `xml:"type,attr"`
				Value string `xml:",chardata"`
			} `xml:"checksum"`
			Location struct {
				Href string `xml:"href,attr"`
			} `xml:"location"`
			Size struct {
				Package looseInt `xml:"package,attr"`
			} `xml:"size"`
			Time struct {
				Build looseInt `xml:"build,attr"`
			} `xml:"time"`
			Format struct {
				SourceRPM string `xml:"sourcerpm"`
				Provides  struct {
					Entry []struct {
						Name string `xml:"name,attr"`
					} `xml:"entry"`
				} `xml:"provides"`
				Requires struct {
					Entry []struct {
						Name string `xml:"name,attr"`
					} `xml:"entry"`
				} `xml:"requires"`
				File []string `xml:"file"`
			} `xml:"format"`
		}
		if err := dec.DecodeElement(&p, &se); err != nil {
			return nil, err
		}
		href, err := safeRel(strings.TrimSpace(p.Location.Href))
		if err != nil {
			return nil, fmt.Errorf("package %s: %w", p.Name, err)
		}
		requires := make([]string, 0, len(p.Format.Requires.Entry))
		for _, entry := range p.Format.Requires.Entry {
			name := strings.TrimSpace(entry.Name)
			if name != "" {
				requires = append(requires, name)
			}
		}
		provides := make([]string, 0, len(p.Format.Provides.Entry))
		for _, entry := range p.Format.Provides.Entry {
			if name := strings.TrimSpace(entry.Name); name != "" {
				provides = append(provides, name)
			}
		}
		pkgs = append(pkgs, pkgRef{
			Name:        strings.TrimSpace(p.Name),
			Arch:        strings.TrimSpace(p.Arch),
			Version:     strings.TrimSpace(p.Version.Ver),
			Release:     strings.TrimSpace(p.Version.Rel),
			Href:        href,
			Checksum:    strings.TrimSpace(p.Checksum.Value),
			SumType:     strings.TrimSpace(p.Checksum.Type),
			Size:        int64(p.Size.Package),
			Summary:     strings.TrimSpace(p.Summary),
			Description: strings.TrimSpace(p.Description),
			Source:      strings.TrimSpace(p.Format.SourceRPM),
			Build:       int64(p.Time.Build),
			Requires:    requires,
			App:         isApp(provides, p.Format.File),
		})
	}
}

func isApp(provides, files []string) bool {
	for _, name := range provides {
		if name == "application()" || strings.HasPrefix(name, "application(") {
			return true
		}
	}
	for _, file := range files {
		if strings.HasSuffix(file, ".desktop") {
			return true
		}
	}
	return false
}

func openSum(path, name, sumType string) (string, error) {
	r, err := openMetadata(path, name)
	if err != nil {
		return "", err
	}
	defer r.Close()
	h, err := newHash(sumType)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// openMetadata returns the uncompressed metadata. name is the repomd href;
// the downloaded file itself has no compression suffix. repomd open-checksum
// is the hash of that plaintext. OpenRepos publishes gzip xml and bzip2 sqlite.
func openMetadata(path, name string) (io.ReadCloser, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	switch {
	case strings.HasSuffix(name, ".gz"):
		zr, err := gzip.NewReader(f)
		if err != nil {
			f.Close()
			return nil, err
		}
		return &stackCloser{zr, f}, nil
	case strings.HasSuffix(name, ".bz2"):
		return &readerCloser{Reader: bzip2.NewReader(f), closer: f}, nil
	case strings.HasSuffix(name, ".xml"), strings.HasSuffix(name, ".sqlite"):
		return f, nil
	default:
		f.Close()
		return nil, fmt.Errorf("unsupported metadata compression for %s", name)
	}
}

type stackCloser struct {
	io.ReadCloser
	file *os.File
}

func (s *stackCloser) Close() error {
	err := s.ReadCloser.Close()
	if ferr := s.file.Close(); err == nil {
		err = ferr
	}
	return err
}

type readerCloser struct {
	io.Reader
	closer io.Closer
}

func (r *readerCloser) Close() error {
	return r.closer.Close()
}

func checkSum(sumType, got, want string) error {
	if want == "" {
		return fmt.Errorf("missing checksum")
	}
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("%s checksum %s, metadata says %s", sumType, got, want)
	}
	return nil
}

func newHash(sumType string) (hash.Hash, error) {
	switch strings.ToLower(sumType) {
	case "sha", "sha1":
		return sha1.New(), nil
	case "sha256":
		return sha256.New(), nil
	default:
		return nil, fmt.Errorf("unsupported checksum type %q", sumType)
	}
}

func sumFile(path, sumType string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h, err := newHash(sumType)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
