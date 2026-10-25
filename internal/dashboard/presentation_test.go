package dashboard

import "testing"

func TestEditorAssetsCannotExposeApplicationOrPersonalHandlers(t *testing.T) {
	for _, name := range []string{"journey-model.js", "journey-editor.js", "journey.css", "IBMPlexSans-Regular.woff2", "IBMPlexMono-Regular.woff2"} {
		if data, err := EditorAsset(name); err != nil || len(data) == 0 {
			t.Fatalf("missing editor asset %s", name)
		}
	}
	for _, name := range []string{"app.js", "index.html", "../server.go", "/api/scenarios", "assets/journey-editor.js"} {
		if _, err := EditorAsset(name); err == nil {
			t.Fatalf("non-editor asset exposed: %s", name)
		}
	}
}
