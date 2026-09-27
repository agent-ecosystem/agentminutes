{{ with .Title }}# {{ . }}{{ end }}
{{ with .Description }}
> {{ . }}
{{ end }}
[Docs index]({{ "llms.txt" | absURL }})

{{ partial "absolute-links.html" .RawContent }}
