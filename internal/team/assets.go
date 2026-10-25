package team

import (
	"embed"
	"html/template"
	"net/http"
	"strings"

	"gometer/internal/dashboard"
)

//go:embed assets/*
var files embed.FS
var workspaceTemplate = template.Must(template.New("workspace.html").ParseFS(files, "assets/workspace.html"))

func serveAsset(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/assets/")
	var data []byte
	var err error
	switch name {
	case "workspace.js", "workspace.css":
		data, err = files.ReadFile("assets/" + name)
	default:
		data, err = dashboard.EditorAsset(name)
	}
	if err != nil {
		http.NotFound(w, r)
		return
	}
	kind := "text/javascript; charset=utf-8"
	if strings.HasSuffix(name, ".css") {
		kind = "text/css; charset=utf-8"
	}
	if strings.HasSuffix(name, ".woff2") {
		kind = "font/woff2"
	}
	w.Header().Set("Content-Type", kind)
	w.Write(data)
}
