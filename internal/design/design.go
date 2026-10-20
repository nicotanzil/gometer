// Package design contains presentation assets shared by the dashboard and portable reports.
package design

import (
	"embed"
	"encoding/base64"
	"strings"
)

// LogoSVG is the trusted, bundled Open Scope mark for portable reports.
//
//go:embed assets/gometer-mark.svg
var LogoSVG string

// Files contains bundled fonts, licenses, brand assets, and presentation styles.
//
//go:embed assets/*
var Files embed.FS

// ReportCSS is built once from bundled assets. Reports need no font server or CDN.
var ReportCSS = func() string {
	faces, _ := Files.ReadFile("assets/fonts.css")
	css := string(faces)
	for _, name := range []string{"BricolageGrotesque-Latin.woff2", "BricolageGrotesque-LatinExt.woff2", "HankenGrotesk-Latin.woff2", "HankenGrotesk-LatinExt.woff2", "IBMPlexMono-Regular.woff2"} {
		font, _ := Files.ReadFile("assets/" + name)
		css = strings.ReplaceAll(css, "/"+name, "data:font/woff2;base64,"+base64.StdEncoding.EncodeToString(font))
	}
	style, _ := Files.ReadFile("assets/report.css")
	return css + string(style)
}()
