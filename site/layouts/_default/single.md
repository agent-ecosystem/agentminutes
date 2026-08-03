{{ with .Title }}# {{ . }}{{ end }}
{{ with .Description }}
> {{ . }}
{{ end }}
[Docs index](/llms.txt)

{{ .RawContent }}
