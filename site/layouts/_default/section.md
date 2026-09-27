{{ with .Title }}# {{ . }}{{ end }}
{{ with .Description }}
> {{ . }}
{{ end }}
[Docs index]({{ "llms.txt" | absURL }})

{{ partial "absolute-links.html" .RawContent }}
{{ if .Pages }}## Pages
{{ range .Pages }}{{ if not .Draft }}
- [{{ .Title }}]({{ .Permalink }}index.md){{ with .Description }}: {{ . }}{{ end }}
{{ end }}{{ end }}{{ end }}
