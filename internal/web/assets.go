package web

import (
	"embed"
	"fmt"
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
		"mul": func(a, b int) int { return a * b },
		"div": func(a, b int) int {
			if b == 0 {
				return 0
			}
			return a / b
		},
		// humanCN renders large counts in zh-CN units:
		//   12345    → "1.2万"
		//   3960000  → "396万"
		//   120000000→ "1.2亿"
		// Below 10000 returns the plain integer.
		"humanCN": func(n int) string {
			switch {
			case n >= 100000000:
				return fmt.Sprintf("%.1f亿", float64(n)/100000000)
			case n >= 10000:
				if n%10000 == 0 {
					return fmt.Sprintf("%d万", n/10000)
				}
				return fmt.Sprintf("%.1f万", float64(n)/10000)
			default:
				return fmt.Sprintf("%d", n)
			}
		},
	}
	return template.New("").Funcs(funcs).ParseFS(templatesFS, "templates/*.html")
}
