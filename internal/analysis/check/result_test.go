package check

import (
	"encoding/json"
	"testing"
)

func TestEncodeRoundTrip(t *testing.T) {
	raw, err := Encode(Result{
		Version: Version, SHA256: "abc", Key: "repo/ftuna-oss/a.rpm",
		Name: "demo", RPMVersion: "1.0", Release: "1", Arch: "aarch64",
		Issues: []Issue{{
			Module: "setuid", Path: "/usr/bin/demo", Code: "setuid", Detail: "mode", Color: ColorRed,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if _, ok := doc["score"]; ok {
		t.Fatalf("score stored in %s", raw)
	}
	if _, ok := doc["verdict"]; ok {
		t.Fatalf("verdict stored in %s", raw)
	}
	if _, ok := doc["scores"]; ok {
		t.Fatalf("scores stored in %s", raw)
	}
	if doc["version"].(float64) != float64(Version) || doc["rpm_version"] != "1.0" || doc["sha256"] != "abc" {
		t.Fatalf("%s", raw)
	}
	issues, _ := doc["issues"].([]any)
	if len(issues) != 1 {
		t.Fatalf("issues %s", raw)
	}
	row := issues[0].(map[string]any)
	if row["color"] != ColorRed {
		t.Fatalf("color %s", raw)
	}
	const sha = "aabbccddeeff00112233445566778899"
	if ObjectKey(sha) != "aa/bb/cc/"+sha+".json" {
		t.Fatal(ObjectKey(sha))
	}
}

func TestEncodeEmptyIssues(t *testing.T) {
	raw, err := Encode(Result{Version: Version})
	if err != nil {
		t.Fatal(err)
	}
	var doc Result
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Issues == nil || len(doc.Issues) != 0 {
		t.Fatalf("%s", raw)
	}
}

func TestWorseColor(t *testing.T) {
	if got := WorseColor("", ColorYellow); got != ColorYellow {
		t.Fatalf("empty %s", got)
	}
	if got := WorseColor(ColorGreen, ColorRed); got != ColorRed {
		t.Fatalf("red %s", got)
	}
	if got := WorseColor(ColorRed, ColorYellow); got != ColorRed {
		t.Fatalf("keep %s", got)
	}
	if got := WorseColor(ColorGray, ColorGreen); got != ColorGreen {
		t.Fatalf("green %s", got)
	}
}
