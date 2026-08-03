# {{ .Site.Title }}
{{ with .Site.Params.description }}
> {{ . }}
{{ end }}
[Docs index](/llms.txt)

{{ .RawContent }}
{{/* Landing page copy lives in data/landing.yaml; mirror the prose (hero
     subtitle, feature grid) so the markdown output has content parity with
     the rendered homepage. Buttons, badges, and other landing chrome are
     excluded from the parity comparison in agent-docs.config.yml instead. */}}
{{ with .Site.Data.landing }}{{ with .hero }}{{ with .subtitle }}{{ . }}
{{ end }}{{ end }}{{ with .featureGrid }}{{ if .enable }}
## {{ .title }}
{{ with .subtitle }}
{{ . }}
{{ end }}
{{ range .items }}### {{ .title }}

{{ .description }}
{{ end }}{{ end }}{{ end }}{{ end }}
## Sections
{{ range .Site.Sections }}
- [{{ .Title }}]({{ .Permalink }}index.md){{ with .Description }}: {{ . }}{{ end }}
{{ end }}
