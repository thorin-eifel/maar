// Package web enthält die eingebettete Oberfläche im CTW-Design.
package web

import "embed"

//go:embed index.html css js img vendor guest
var FS embed.FS
