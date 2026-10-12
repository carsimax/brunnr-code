package mcp

import (
	"fmt"

	"github.com/carsimax/brunnr-code/internal/versions"
)

var defaultContext7ServerJSON = []byte(fmt.Sprintf("{\n  \"command\": \"npx\",\n  \"args\": [\n    \"-y\",\n    \"--package=@upstash/context7-mcp@%s\",\n    \"--\",\n    \"context7-mcp\"\n  ]\n}\n", versions.Context7MCP))

var defaultContext7OverlayJSON = []byte(fmt.Sprintf("{\n  \"mcpServers\": {\n    \"context7\": {\n      \"command\": \"npx\",\n      \"args\": [\n        \"-y\",\n        \"--package=@upstash/context7-mcp@%s\",\n        \"--\",\n        \"context7-mcp\"\n      ]\n    }\n  }\n}\n", versions.Context7MCP))

func DefaultContext7ServerJSON() []byte {
	content := make([]byte, len(defaultContext7ServerJSON))
	copy(content, defaultContext7ServerJSON)
	return content
}

func DefaultContext7OverlayJSON() []byte {
	content := make([]byte, len(defaultContext7OverlayJSON))
	copy(content, defaultContext7OverlayJSON)
	return content
}
