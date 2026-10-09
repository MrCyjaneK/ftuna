package catalog

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/mrcyjanek/ftuna/internal/build/deps"
	"github.com/mrcyjanek/ftuna/internal/build/recipe"
	"github.com/ulikunitz/xz"
)

func TestAssembleLibraryAndApplication(t *testing.T) {
	older := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	doc := Assemble([]Item{
		{
			Recipe:  recipe.Recipe{ID: "github.com/sailfishos/sailfish-components-gallery", Name: "Gallery components", AI: "H", Description: "QML components"},
			Updated: older,
			Built: []Built{{
				Arch:    "aarch64",
				RepoID:  "ftuna-oss-5.1.0.11-aarch64",
				RepoURL: "https://repo-oss.ftuna.eu/5.1.0.11/aarch64/",
				RPMs: []deps.RPM{
					{
						File: "github.com/sailfishos/sailfish-components-gallery/sailfish-components-gallery-1.0.0-1.aarch64.rpm",
						Name: "sailfish-components-gallery", Version: "1.0.0", Release: "1",
						Summary:  "Gallery components",
						Requires: []string{"qt5-qtdeclarative >= 5", "rpmlib(CompressedFileNames) <= 3.0.4-1"},
						SHA256:   "abc123",
					},
					{
						File: "github.com/sailfishos/sailfish-components-gallery/sailfish-components-gallery-doc-1.0.0-1.noarch.rpm",
						Name: "sailfish-components-gallery-doc", Version: "1.0.0", Release: "1",
					},
				},
			}},
		},
		{
			Recipe: recipe.Recipe{
				ID:          "github.com/piggz/harbour-advanced-camera",
				Name:        "Advanced Camera",
				Install:     []string{"harbour-advanced-camera"},
				AI:          "A",
				Description: "Advanced camera",
			},
			Updated:     newer,
			Icon:        "https://cdn-mirror.ftuna.eu/raw.githubusercontent.com/piggz/harbour-advanced-camera/cdcef3fb/harbour-advanced-camera.svg",
			Screenshots: []string{"https://cdn-mirror.ftuna.eu/raw.githubusercontent.com/piggz/harbour-advanced-camera/cdcef3fb/screenshots/screenshot1.png"},
			Built: []Built{{
				Arch:    "aarch64",
				RepoID:  "ftuna-oss-5.1.0.11-aarch64",
				RepoURL: "https://repo-oss.ftuna.eu/5.1.0.11/aarch64/",
				RPMs: []deps.RPM{
					{
						File: "github.com/piggz/harbour-advanced-camera/harbour-advanced-camera-2.0.0-1.aarch64.rpm",
						Name: "harbour-advanced-camera", Version: "2.0.0", Release: "1",
						Summary: "Camera",
					},
					{
						File: "github.com/piggz/harbour-advanced-camera/harbour-advanced-camera-debuginfo-2.0.0-1.aarch64.rpm",
						Name: "harbour-advanced-camera-debuginfo", Version: "2.0.0", Release: "1",
						Summary: "debug",
					},
				},
			}},
		},
	})
	if doc.Version != 1 || doc.Updated != "2026-09-29T00:00:00Z" {
		t.Fatalf("header %#v", doc)
	}
	if len(doc.Categories) != 2 || doc.Categories[0] != "application" || doc.Categories[1] != "library" {
		t.Fatalf("categories %#v", doc.Categories)
	}
	if len(doc.Repositories) != 1 || doc.Repositories[0].ID != "ftuna-oss-5.1.0.11-aarch64" {
		t.Fatalf("repos %#v", doc.Repositories)
	}
	if len(doc.Packages) != 2 {
		t.Fatalf("packages %d", len(doc.Packages))
	}
	lib := doc.Packages[1]
	if lib.ID != "github.com/sailfishos/sailfish-components-gallery" || lib.Category != "library" || lib.Icon != "" {
		t.Fatalf("library %#v", lib)
	}
	if len(lib.Screenshots) != 0 || len(lib.Status.Setuid) != 0 || len(lib.Status.RedFlags) != 0 || len(lib.Status.Permissions) != 0 {
		t.Fatalf("empty lists %#v", lib)
	}
	if lib.Status.Sailjail != "unknown" {
		t.Fatalf("sailjail %s", lib.Status.Sailjail)
	}
	if lib.Description != "QML components\n\n<img src=\"https://raw.githubusercontent.com/sailfishos-chum/sailfishos-chum-gui/main/icons/theme/ai-score/svgs/icons/AI-score-H.svg\">" {
		t.Fatalf("library description %s", lib.Description)
	}
	if lib.Summary != "Gallery components" || lib.RPMs[0].Install != true || lib.RPMs[1].Install != false {
		t.Fatalf("library rpms %#v summary %s", lib.RPMs, lib.Summary)
	}
	if len(lib.RPMs[0].Requires) != 1 || lib.RPMs[0].Requires[0] != "qt5-qtdeclarative" {
		t.Fatalf("requires %#v", lib.RPMs[0].Requires)
	}
	if lib.RPMs[0].SHA256 != "abc123" || lib.RPMs[1].SHA256 != "" {
		t.Fatalf("sha256 %#v", lib.RPMs)
	}
	if lib.RPMs[1].Arch != "noarch" {
		t.Fatalf("doc arch %s", lib.RPMs[1].Arch)
	}
	app := doc.Packages[0]
	if !strings.HasPrefix(app.Description, "Advanced camera\n\n") || !strings.HasSuffix(app.Description, "AI-score-A.svg\">") {
		t.Fatalf("app description %s", app.Description)
	}
	if app.Category != "application" || app.Summary != "Advanced Camera" || !app.RPMs[0].Install || app.RPMs[1].Install {
		t.Fatalf("app %#v", app)
	}
	if app.RPMs[0].Href != "github.com/piggz/harbour-advanced-camera/harbour-advanced-camera-2.0.0-1.aarch64.rpm" {
		t.Fatalf("href %#v", app.RPMs[0])
	}
	raw, err := Encode(doc)
	if err != nil {
		t.Fatal(err)
	}
	r, err := xz.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	var again Doc
	if err := json.Unmarshal(body, &again); err != nil {
		t.Fatal(err)
	}
	if again.Packages[0].Icon != app.Icon {
		t.Fatalf("round trip icon %s", again.Packages[0].Icon)
	}
}
