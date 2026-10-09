package cdn

import "testing"

func TestOriginPath(t *testing.T) {
	got, err := OriginPath("https://raw.githubusercontent.com/piggz/harbour-advanced-camera/cdcef3fb69267178f3f224b12f5f6b862a205f6e/harbour-advanced-camera.svg")
	if err != nil {
		t.Fatal(err)
	}
	want := "/piggz/harbour-advanced-camera/cdcef3fb69267178f3f224b12f5f6b862a205f6e/harbour-advanced-camera.svg"
	if got != want {
		t.Fatalf("path %s", got)
	}
	if ObjectKey(got) != "cdn-mirror/raw.githubusercontent.com"+want {
		t.Fatalf("key %s", ObjectKey(got))
	}
	if PublicURL(got) != "https://cdn-mirror.ftuna.eu/raw.githubusercontent.com"+want {
		t.Fatalf("url %s", PublicURL(got))
	}
	for _, bad := range []string{
		"http://raw.githubusercontent.com/a/b/c/d.svg",
		"https://github.com/piggz/harbour-advanced-camera/raw/master/harbour-advanced-camera.svg",
		"https://raw.githubusercontent.com/a/b/../c.svg",
		"https://raw.githubusercontent.com/a/b/c.svg?x=1",
	} {
		if _, err := OriginPath(bad); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
}

func TestParseAssetOpenRepos(t *testing.T) {
	raw := "https://openrepos.net/sites/default/files/styles/thumbnail/public/packages/18816/icon-harbour-yast-client.png"
	a, err := ParseAsset(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := "/sites/default/files/styles/thumbnail/public/packages/18816/icon-harbour-yast-client.png"
	if a.Host != OpenReposHost || a.Path != want {
		t.Fatalf("asset %#v", a)
	}
	if a.ObjectKey() != "cdn-mirror/openrepos.net"+want {
		t.Fatalf("key %s", a.ObjectKey())
	}
	wantURL := "https://cdn-mirror.ftuna.eu/openrepos.net/sites/default/files/styles/thumbnail/public/packages/18816/icon-harbour-yast-client.png"
	if a.PublicURL() != wantURL {
		t.Fatalf("url %s", a.PublicURL())
	}
	for _, bad := range []string{
		"https://example.com/icon.png",
		"https://openrepos.net/sites/default/files/packages/1/icon.png?itok=abc",
		"https://openrepos.net/other/icon.png",
	} {
		if _, err := ParseAsset(bad); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
}
