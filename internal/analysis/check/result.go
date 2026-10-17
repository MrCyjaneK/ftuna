package check

import (
	"encoding/json"
	"fmt"
)

// Result is the JSON document stored at <aa>/<bb>/<cc>/<sha256>.json
// under the local ratings directory.
// Version is this analysis suite. RPMVersion is the package version.
type Result struct {
	Version    int     `json:"version"`
	SHA256     string  `json:"sha256"`
	Key        string  `json:"key"`
	Name       string  `json:"name"`
	RPMVersion string  `json:"rpm_version"`
	Release    string  `json:"release"`
	Arch       string  `json:"arch"`
	Issues     []Issue `json:"issues"`
}

// ObjectKey is the relative path of one RPM's result.
// sha256 must be at least 6 characters. The file is plain JSON.
func ObjectKey(sha256 string) string {
	return fmt.Sprintf("%s/%s/%s/%s.json", sha256[:2], sha256[2:4], sha256[4:6], sha256)
}

// Encode writes the result as indented JSON.
func Encode(doc Result) ([]byte, error) {
	if doc.Issues == nil {
		doc.Issues = []Issue{}
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}
