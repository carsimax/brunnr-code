package cli

import (
	"flag"
	"fmt"
	"io"
	"strings"
)

type InstallFlags struct {
	Agents     []string
	Components []string
	Skills     []string
	Preset     string
	Scope      string
	DryRun     bool

	ClaudeOrchestratorModules bool
}

func PrintInstallHelp(w io.Writer) {
	fmt.Fprint(w, `USAGE
  brunnr-code install [flags]

FLAGS
  --agent, --agents <list>           Agents to install
  --component, --components <list>   Components to install
  --skill, --skills <list>           Skills to install (requires skills in the resolved plan)
  --preset <name>                    Preset to apply
  --scope global|workspace           Install scope (env: BRUNNR_CODE_INSTALL_SCOPE)
  --claude-orchestrator-modules      Global Claude only: install the orchestrator as a core plus on-demand modules
                                     under ~/.claude/brunnr-code/orchestrator/ (default off)
  --dry-run                          Preview plan without executing
  --help, -h                         Show this help

ISOLATED INSTALLS (absolute paths; all unset by default)
  BRUNNR_CODE_HOME=<dir>               Brunnr-Code state and backups instead of ~/.brunnr-code
  BRUNNR_CODE_ENGRAM_BIN=<file>        Use this Engram executable: no brew, download, go install or PATH change,
                                     and dependency detection runs no git, curl, node, npm, brew or go command

ORPHAN MODIFIERS
  Rerun brunnr-code install with the required consumer added to your existing
  component list, or remove the modifier. Components are not added automatically.
`)
}

func ParseInstallFlags(args []string) (InstallFlags, error) {
	var opts InstallFlags

	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.SetOutput(ioDiscard{})
	registerListFlag(fs, "agent", &opts.Agents)
	registerListFlag(fs, "agents", &opts.Agents)
	registerListFlag(fs, "component", &opts.Components)
	registerListFlag(fs, "components", &opts.Components)
	registerListFlag(fs, "skill", &opts.Skills)
	registerListFlag(fs, "skills", &opts.Skills)
	fs.StringVar(&opts.Preset, "preset", "", "preset to apply")
	fs.StringVar(&opts.Scope, "scope", "", "install scope: global (default) or workspace — env: BRUNNR_CODE_INSTALL_SCOPE")
	fs.BoolVar(&opts.ClaudeOrchestratorModules, "claude-orchestrator-modules", false, "global Claude only: install the orchestrator as a core plus on-demand modules")
	fs.BoolVar(&opts.DryRun, "dry-run", false, "preview plan without executing")

	if err := fs.Parse(args); err != nil {
		return InstallFlags{}, err
	}

	if fs.NArg() > 0 {
		return InstallFlags{}, fmt.Errorf("unexpected install argument %q", fs.Arg(0))
	}
	return opts, nil
}

type csvListFlag struct {
	values *[]string
}

func (f csvListFlag) String() string {
	if f.values == nil {
		return ""
	}

	return strings.Join(*f.values, ",")
}

func (f csvListFlag) Set(value string) error {
	for _, part := range strings.Split(value, ",") {
		item := strings.TrimSpace(part)
		if item == "" {
			continue
		}
		*f.values = append(*f.values, item)
	}

	return nil
}

func registerListFlag(fs *flag.FlagSet, name string, values *[]string) {
	fs.Var(csvListFlag{values: values}, name, "comma-separated list")
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) {
	return len(p), nil
}
