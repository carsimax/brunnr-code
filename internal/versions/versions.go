// Package versions centralizes pinned external package versions so Renovate can
// auto-PR bumps. The marker comments are machine-readable directives consumed
// by the customManager defined in renovate.json — keep them in the exact form
// `// renovate: datasource=<ds> depName=<name>` immediately above each const.
//
// Agent runtime pins live nowhere here: brunnr-code no longer installs agent
// runtimes on the user's behalf (see agentInstallStep in internal/cli/run.go),
// and the display-only refusal commands advise "latest" instead, since a
// human runs them and a frozen version goes stale as soon as a newer release
// ships.
package versions

// renovate: datasource=npm depName=@upstash/context7-mcp
const Context7MCP = "2.2.5"
