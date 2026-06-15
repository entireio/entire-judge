package judge

import "embed"

// templatesFS embeds the lens system prompts shipped with the plugin. New
// templates matching templates/*.md are auto-included at build time.
//
//go:embed templates/*.md
var templatesFS embed.FS
