package openrepos

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

func TestFlexString(t *testing.T) {
	var item appListItem
	if err := json.Unmarshal([]byte(`{"appid":12558,"updated":"10","published":"1"}`), &item); err != nil {
		t.Fatal(err)
	}
	if item.AppID != "12558" || item.Updated != "10" {
		t.Fatalf("%#v", item)
	}
	var detail appDetail
	if err := json.Unmarshal([]byte(`{"appid":"10000","user":{"uid":"8522"}}`), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.AppID != "10000" || detail.User.UID != "8522" {
		t.Fatalf("%#v", detail)
	}
	var missing appDetail
	raw := `{"appid":"12539","title":"QQ Bridge","body":false,"user":false,"package":{"name":false,"version":"1.0.11-1"},"icon":false,"screenshots":false}`
	if err := json.Unmarshal([]byte(raw), &missing); err != nil {
		t.Fatal(err)
	}
	if missing.Title != "QQ Bridge" || missing.Body != "" || missing.Package.Name != "" || missing.Icon.URL != "" || missing.Screenshots != nil {
		t.Fatalf("%#v", missing)
	}
}

func TestListAppsContinuesAfterShortPage(t *testing.T) {
	pages := []string{
		appPage(1, 100),
		strings.TrimSuffix(appPage(101, 99), "]") + `,{"appid":"999","updated":"1","published":"0"}]`,
		`[{"appid":"10627","updated":"1634130938","published":"1"}]`,
		`[]`,
	}
	items, err := listAppPages(context.Background(), func(page int) ([]byte, error) {
		if page >= len(pages) {
			t.Fatalf("fetched page %d", page)
			return nil, nil
		}
		return []byte(pages[page]), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 200 {
		t.Fatalf("got %d apps", len(items))
	}
	if items[len(items)-1].AppID != "10627" {
		t.Fatalf("last %#v", items[len(items)-1])
	}
}

func appPage(start, n int) string {
	var b strings.Builder
	b.WriteByte('[')
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"appid":"`)
		b.WriteString(strconv.Itoa(start + i))
		b.WriteString(`","updated":"1","published":"1"}`)
	}
	b.WriteByte(']')
	return b.String()
}

func TestAssetPath(t *testing.T) {
	origin, err := assetPath("https://openrepos.net/sites/default/files/packages/1/icon.png")
	if err != nil {
		t.Fatal(err)
	}
	if origin != "/sites/default/files/packages/1/icon.png" {
		t.Fatalf("path %s", origin)
	}
	if assetKey(origin) != "cdn-mirror/openrepos.net/sites/default/files/packages/1/icon.png" {
		t.Fatalf("key %s", assetKey(origin))
	}
	want := "https://cdn-mirror.ftuna.eu/openrepos.net/sites/default/files/packages/1/icon.png"
	if assetPublic(origin) != want {
		t.Fatalf("public %s", assetPublic(origin))
	}
	for _, raw := range []string{
		"https://raw.githubusercontent.com/a/b/icon.png",
		"https://openrepos.net/sites/default/files/packages/1/icon.png?itok=abc",
		"https://openrepos.net/other/icon.png",
		"http://openrepos.net/sites/default/files/packages/1/icon.png",
	} {
		if _, err := assetPath(raw); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestPlainDescription(t *testing.T) {
	raw := "<p>Hello&nbsp;world</p><p>A <a href=\"x\">link</a> and<br>more</p><ul><li>one</li><li>two</li></ul>"
	got := plainDescription(raw)
	want := "Hello world\nA link and\nmore\none\ntwo"
	if got != want {
		t.Fatalf("%q", got)
	}
	if plainDescription("  ") != "" {
		t.Fatal("blank")
	}
}

func TestIndexAppsPrefersUserIDAndNewerRecord(t *testing.T) {
	icon := "https://cdn-mirror.ftuna.eu/openrepos.net/sites/default/files/a.png"
	idx := indexApps([]appBackup{
		{AppID: "1", Updated: "20", UID: "9", User: "Other", Package: "Harbour-Demo", Icon: icon},
		{AppID: "2", Updated: "10", UID: "9", User: "Other", Package: "harbour-demo"},
		{AppID: "3", Updated: "30", UID: "", User: "lone", Package: "quiet"},
		{AppID: "4", Updated: "1", UID: "1", User: "", Package: ""},
	}, map[string]string{"9": "piggz"})
	got := idx[appIndexKey("piggz", "harbour-demo")]
	if got.AppID != "1" || got.Icon != icon {
		t.Fatalf("%#v", got)
	}
	if idx[appIndexKey("lone", "quiet")].AppID != "3" {
		t.Fatalf("%#v", idx)
	}
	if _, ok := idx[appIndexKey("", "")]; ok {
		t.Fatal("empty package was indexed")
	}
}

func TestAppIDFromKey(t *testing.T) {
	if appIDFromKey("cdn-mirror/openrepos.net/apps/10000.json") != "10000" {
		t.Fatal("id")
	}
	if appIDFromKey("cdn-mirror/openrepos.net/apps/index.json") != "" {
		t.Fatal("index")
	}
	if appIDFromKey("cdn-mirror/openrepos.net/apps/10000/extra.json") != "" {
		t.Fatal("nested")
	}
}
