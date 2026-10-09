package check

import (
	"encoding/json"
	"testing"
)

func TestEncodeRoundTrip(t *testing.T) {
	raw, err := Encode(Result{
		Version: Version, SHA256: "abc", Key: "repo/ftuna-oss/a.rpm",
		Name: "demo", RPMVersion: "1.0", Release: "1", Arch: "aarch64",
		Verdict: 40,
		Scores:  []Score{{Module: "setuid", Score: 0}, {Module: "sailjail", Score: Neutral}},
		Issues:  []Issue{{Module: "setuid", Path: "/usr/bin/demo", Code: "setuid", Detail: "mode"}},
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
	if doc["version"].(float64) != float64(Version) || doc["rpm_version"] != "1.0" || doc["sha256"] != "abc" {
		t.Fatalf("%s", raw)
	}
	if doc["verdict"].(float64) != 40 {
		t.Fatalf("verdict %s", raw)
	}
	scores, _ := doc["scores"].([]any)
	if len(scores) != 2 {
		t.Fatalf("scores %s", raw)
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
	if doc.Issues == nil || len(doc.Issues) != 0 || doc.Scores == nil || len(doc.Scores) != 0 {
		t.Fatalf("%s", raw)
	}
}

func TestVerdict(t *testing.T) {
	if got := Verdict(nil); got != Neutral {
		t.Fatalf("empty %d", got)
	}
	if got := Verdict([]Score{{Module: "a", Score: Neutral}, {Module: "b", Score: Neutral}}); got != Neutral {
		t.Fatalf("abstain %d", got)
	}
	if got := Verdict([]Score{{Module: "a", Score: Neutral}, {Module: "b", Score: 0}, {Module: "c", Score: 100}}); got != 50 {
		t.Fatalf("ends %d", got)
	}
	if got := Verdict([]Score{{Module: "a", Score: 30}, {Module: "b", Score: 80}}); got != 55 {
		t.Fatalf("mid %d", got)
	}
	if got := Verdict([]Score{{Module: "a", Score: 1}, {Module: "b", Score: 2}}); got != 2 {
		t.Fatalf("half %d", got)
	}
	if got := Worse(Neutral, 40); got != 40 {
		t.Fatalf("neutral %d", got)
	}
	if got := Worse(80, 30); got != 30 {
		t.Fatalf("worse %d", got)
	}
}
