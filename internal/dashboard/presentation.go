package dashboard

import "io/fs"

// EditorAsset shares the standalone canvas and typography without exposing any
// dashboard handler, personal workspace data, or application startup script.
func EditorAsset(name string) ([]byte, error) {
	switch name {
	case "journey-model.js", "journey-editor.js", "journey.css",
		"IBMPlexSans-Regular.woff2", "IBMPlexSans-Medium.woff2", "IBMPlexSans-SemiBold.woff2", "IBMPlexSans-Bold.woff2", "IBMPlexMono-Regular.woff2":
		return assets.ReadFile("assets/" + name)
	default:
		return nil, fs.ErrNotExist
	}
}
