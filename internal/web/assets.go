package web

import (
	"embed"
	"html/template"
	"io/fs"
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed static/*
var staticFS embed.FS

// Static returns the embedded /static/* filesystem rooted at "static".
func Static() fs.FS {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	return sub
}

// LoadTemplates parses every .html under templates/ as one ParseFS set.
func LoadTemplates() (*template.Template, error) {
	funcs := template.FuncMap{
		"add": func(a, b int) int { return a + b },
		"sub": func(a, b int) int { return a - b },
	}
	return template.New("").Funcs(funcs).ParseFS(templatesFS, "templates/*.html")
}
