package communitytool

import (
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"

	"github.com/carsimax/brunnr-code/internal/agents"
	"github.com/carsimax/brunnr-code/internal/catalog"
	"github.com/carsimax/brunnr-code/internal/model"
	"github.com/carsimax/brunnr-code/internal/system"
)

type Availability string

const (
	AvailabilityAvailable Availability = "available"
	AvailabilityMissing   Availability = "missing"
)

type AgentStatusKind string

const (
	AgentStatusUnavailable AgentStatusKind = "unavailable"
	AgentStatusConfigured  AgentStatusKind = "configured"
	AgentStatusMissing     AgentStatusKind = "missing"
)

type Definition struct {
	ID          model.CommunityToolID
	Name        string
	PackageName string
	CommandName string
	RepoURL     string
	Description string
}

type Result struct {
	Tool          model.CommunityToolID
	CommandsRun   []string
	ManualActions []string
	StatusBefore  *Status
	StatusAfter   *Status
}

type Status struct {
	Tool      model.CommunityToolID
	CLI       Availability
	CLIPath   string
	Agents    []AgentStatus
	FollowUps []string
}

type AgentStatus struct {
	Agent      model.AgentID
	Name       string
	Status     AgentStatusKind
	Detected   bool
	Configured bool
	Path       string
	Reason     string
}

type Detector interface {
	LookPath(name string) (string, error)
}

type DetectorFunc func(name string) (string, error)

func (fn DetectorFunc) LookPath(name string) (string, error) { return fn(name) }

type Runner interface {
	Run(name string, args ...string) error
}

type RunnerFunc func(name string, args ...string) error

func (fn RunnerFunc) Run(name string, args ...string) error { return fn(name, args...) }

var (
	codeGraphPackageLookPath = exec.LookPath
	codeGraphPnpmGlobalBin   = defaultPnpmGlobalBin
)

var definitions = []Definition{
	{
		ID:          model.CommunityToolCodeGraph,
		Name:        "CodeGraph",
		PackageName: "@colbymchenry/codegraph@latest",
		CommandName: "codegraph",
		RepoURL:     "https://github.com/colbymchenry/codegraph",
		Description: "Code graph indexing and MCP wiring for supported coding agents",
	},
}

func Definitions() []Definition {
	out := make([]Definition, len(definitions))
	copy(out, definitions)
	return out
}

func DefinitionFor(id model.CommunityToolID) (Definition, bool) {
	for _, def := range definitions {
		if def.ID == id {
			return def, true
		}
	}
	return Definition{}, false
}

func Install(id model.CommunityToolID, workspaceDir string, runner Runner) (Result, error) {
	return InstallWithHome(id, workspaceDir, defaultHomeDir(), runner, DetectorFunc(exec.LookPath))
}

func InstallWithHome(id model.CommunityToolID, workspaceDir string, homeDir string, runner Runner, detector Detector) (Result, error) {
	if runner == nil {
		return Result{}, fmt.Errorf("community tool runner is not configured")
	}
	def, ok := DefinitionFor(id)
	if !ok {
		return Result{}, fmt.Errorf("unknown community tool %q", id)
	}
	if def.ID != model.CommunityToolCodeGraph {
		return Result{}, fmt.Errorf("community tool %q is not supported", id)
	}

	result := Result{Tool: id}
	before := DetectStatus(id, homeDir, detector)
	result.StatusBefore = &before
	snapshots, err := snapshotCodeGraphPaths(CodeGraphManagedPaths(homeDir))
	if err != nil {
		return result, err
	}
	rollback := func(cause error) (Result, error) {
		if restoreErr := restoreCodeGraphPaths(snapshots); restoreErr != nil {
			return result, fmt.Errorf("%w; rollback CodeGraph configuration: %v", cause, restoreErr)
		}
		return result, cause
	}
	upgraded := false
	if before.CLI == AvailabilityAvailable {
		upgraded, err = upgradeCodeGraphToFloor(id, homeDir, before.CLIPath, runner, detector, &result)
		if err != nil {
			return rollback(err)
		}
	}
	if !upgraded && (before.CodeGraphReconcileSatisfied() || codeGraphCanRepairWithoutFullInstall(homeDir, before)) {
		guidanceResult, err := InjectCodeGraphGuidance(homeDir)
		if err != nil {
			return rollback(err)
		}
		after := DetectStatus(id, homeDir, detector)
		result.StatusAfter = &after
		if err := validateCodeGraphInstallStatus(after); err != nil {
			return rollback(err)
		}
		if guidanceResult.Changed {
			result.ManualActions = append(result.ManualActions, "CodeGraph is already available and MCP-configured. Agent guidance was updated so enabled agents lazily initialize project indexes when needed.")
		} else {
			result.ManualActions = append(result.ManualActions, "CodeGraph is already available and configured for all detected supported agents. No changes were needed.")
		}
		return result, nil
	}

	targets := detectedCodeGraphTargets(homeDir)
	commands := make([][]string, 0, 2)
	if len(targets) > 0 {
		commands = append(commands, []string{"codegraph", "install", "--target", strings.Join(targets, ","), "--location", "global", "--yes"})
	}
	if before.CLI != AvailabilityAvailable {
		var err error
		commands, err = CodeGraphCommandsForDetectorAndTargets(DetectorFunc(codeGraphPackageLookPath), targets)
		if err != nil {
			return result, err
		}
		if len(targets) == 0 {
			commands = commands[:1]
		}
	}
	for index, command := range commands {
		if len(command) == 0 {
			continue
		}
		result.CommandsRun = append(result.CommandsRun, strings.Join(command, " "))
		if err := runner.Run(command[0], command[1:]...); err != nil {
			return rollback(fmt.Errorf("run %q: %w", strings.Join(command, " "), err))
		}
		if before.CLI != AvailabilityAvailable && index == 0 {
			afterPackageInstall := DetectStatus(id, homeDir, detector)
			result.StatusAfter = &afterPackageInstall
			if afterPackageInstall.CLI != AvailabilityAvailable {
				return rollback(fmt.Errorf("CodeGraph package installation did not leave a runnable codegraph CLI available"))
			}
		}
	}
	if _, err := InjectCodeGraphGuidance(homeDir); err != nil {
		return rollback(err)
	}
	after := DetectStatus(id, homeDir, detector)
	result.StatusAfter = &after
	if err := validateCodeGraphInstallStatus(after); err != nil {
		return rollback(err)
	}
	result.ManualActions = append(result.ManualActions, "CodeGraph CLI was installed and supported agents were connected. Project indexes will be created automatically when an enabled agent opens inside a project.")
	return result, nil
}

// codeGraphManualUpgradeHint names every supported way to bring the CLI up to
// the floor by hand when Brunnr-Code cannot do it.
const codeGraphManualUpgradeHint = "Run `codegraph upgrade`, or reinstall with `npm install -g @colbymchenry/codegraph@latest` (or `pnpm add -g @colbymchenry/codegraph@latest`), then rerun Brunnr-Code."

// upgradeCodeGraphToFloor runs `codegraph upgrade` when the installed CLI is
// below codeGraphUpstreamVersion, then re-probes the CLI. An undeterminable
// version is treated as below the floor: the targeted install below passes
// agent ids that an older CLI rejects as a whole, so only a proven version is
// trusted. Upgrading an already-current CLI is a no-op upstream ("Already up
// to date", exit 0), so this choice costs one extra command at worst. The
// upgrade never prompts: upstream only offers its beta signup when both stdin
// and stdout are TTYs, and the runner gives it no stdin. It reports whether an
// upgrade ran, and fails without falling back to an untargeted install when
// the CLI still does not meet the floor.
func upgradeCodeGraphToFloor(id model.CommunityToolID, homeDir, cliPath string, runner Runner, detector Detector, result *Result) (bool, error) {
	installed, ok := codeGraphInstalledVersion(cliPath)
	if ok && !codeGraphVersionLess(installed, codeGraphUpstreamVersion) {
		return false, nil
	}
	current := codeGraphVersionLabel(installed, ok)
	command := []string{"codegraph", "upgrade"}
	result.CommandsRun = append(result.CommandsRun, strings.Join(command, " "))
	if err := runner.Run(command[0], command[1:]...); err != nil {
		return true, fmt.Errorf("CodeGraph %s is older than the required %s and `codegraph upgrade` failed: %w. %s", current, codeGraphUpstreamVersion, err, codeGraphManualUpgradeHint)
	}
	after := DetectStatus(id, homeDir, detector)
	if after.CLI != AvailabilityAvailable {
		return true, fmt.Errorf("`codegraph upgrade` did not leave a runnable codegraph CLI available. %s", codeGraphManualUpgradeHint)
	}
	upgradedVersion, ok := codeGraphInstalledVersion(after.CLIPath)
	if !ok || codeGraphVersionLess(upgradedVersion, codeGraphUpstreamVersion) {
		return true, fmt.Errorf("CodeGraph is still %s after `codegraph upgrade`, below the required %s. %s", codeGraphVersionLabel(upgradedVersion, ok), codeGraphUpstreamVersion, codeGraphManualUpgradeHint)
	}
	result.ManualActions = append(result.ManualActions, fmt.Sprintf("CodeGraph was upgraded from %s to %s.", current, upgradedVersion))
	return true, nil
}

func codeGraphVersionLabel(version string, ok bool) string {
	if !ok {
		return "at an undeterminable version"
	}
	return version
}

func codeGraphCanRepairWithoutFullInstall(homeDir string, status Status) bool {
	if status.CLI != AvailabilityAvailable {
		return false
	}
	reg, err := agents.NewDefaultRegistry()
	if err != nil {
		return false
	}
	foundMissing := false
	for _, agent := range status.Agents {
		if !agent.Detected || agent.Configured {
			continue
		}
		foundMissing = true
		adapter, ok := reg.Get(agent.Agent)
		if !ok {
			return false
		}
		if _, wired := hasCodeGraphToolWiring(homeDir, adapter); !wired {
			return false
		}
	}
	return foundMissing
}

func validateCodeGraphInstallStatus(status Status) error {
	if status.Tool != model.CommunityToolCodeGraph {
		return nil
	}
	if status.CLI != AvailabilityAvailable {
		return fmt.Errorf("CodeGraph install did not leave the codegraph CLI available")
	}
	missing := make([]string, 0)
	for _, agent := range status.Agents {
		if agent.Detected && !agent.Configured {
			missing = append(missing, agent.Name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("CodeGraph install did not configure detected supported agents: %s", strings.Join(missing, ", "))
	}
	return nil
}

func DetectStatus(id model.CommunityToolID, homeDir string, detector Detector) Status {
	status := Status{Tool: id, CLI: AvailabilityMissing}
	def, ok := DefinitionFor(id)
	if !ok {
		status.FollowUps = append(status.FollowUps, fmt.Sprintf("status detection for %q is not implemented", id))
		return status
	}
	if id != model.CommunityToolCodeGraph {
		status.FollowUps = append(status.FollowUps, fmt.Sprintf("status detection for %q is not implemented", id))
		return status
	}
	if detector == nil {
		detector = DetectorFunc(exec.LookPath)
	}
	if path, err := detector.LookPath(def.CommandName); err == nil && strings.TrimSpace(path) != "" {
		status.CLI = AvailabilityAvailable
		status.CLIPath = path
	}
	status.Agents = detectCodeGraphAgents(homeDir)
	status.FollowUps = append(status.FollowUps, "CodeGraph markers can vary by upstream version; detection currently checks conservative MCP entries and instruction markers containing codegraph.")
	return status
}

func (s Status) CodeGraphReconcileSatisfied() bool {
	if s.Tool != model.CommunityToolCodeGraph || s.CLI != AvailabilityAvailable {
		return false
	}
	for _, agent := range s.Agents {
		if agent.Detected && !agent.Configured {
			return false
		}
	}
	return true
}

func (s Status) DetectedConfiguredMissingCounts() (detected, configured, missing int) {
	for _, agent := range s.Agents {
		if !agent.Detected {
			continue
		}
		detected++
		if agent.Configured {
			configured++
		} else {
			missing++
		}
	}
	return detected, configured, missing
}

func detectCodeGraphAgents(homeDir string) []AgentStatus {
	reg, err := agents.NewDefaultRegistry()
	if err != nil {
		return nil
	}
	supported := codeGraphSupportedAgents()
	installed := agents.DiscoverSelected(reg, homeDir)
	installedByID := make(map[model.AgentID]string, len(installed))
	for _, agent := range installed {
		installedByID[agent.ID] = agent.ConfigDir
	}

	result := make([]AgentStatus, 0, len(supported))
	for _, id := range supported {
		adapter, ok := reg.Get(id)
		if !ok {
			continue
		}
		name := agentDisplayName(id)
		configDir, detected := installedByID[id]
		state := AgentStatus{
			Agent:    id,
			Name:     name,
			Detected: detected,
			Path:     configDir,
			Status:   AgentStatusUnavailable,
			Reason:   "agent config directory was not detected",
		}
		if detected {
			configured, markerPath, reason := hasCodeGraphWiring(homeDir, adapter)
			if configured {
				state.Status = AgentStatusConfigured
			} else {
				state.Status = AgentStatusMissing
			}
			state.Configured = configured
			state.Path = markerPath
			state.Reason = reason
		}
		result = append(result, state)
	}
	return result
}

func codeGraphSupportedAgents() []model.AgentID {
	reg, err := agents.NewDefaultRegistry()
	if err != nil {
		return nil
	}
	ids := reg.SupportedAgents()
	ids = slices.DeleteFunc(ids, func(id model.AgentID) bool { return !isCodeGraphCompatibleAgent(id) })
	return ids
}

func hasCodeGraphWiring(homeDir string, adapter agents.Adapter) (bool, string, string) {
	guidancePath := codeGraphGuidancePath(homeDir, adapter)
	toolPath, hasToolWiring := hasCodeGraphToolWiring(homeDir, adapter)
	if adapter.SupportsSystemPrompt() {
		if !hasCodeGraphGuidance(guidancePath) {
			return false, guidancePath, "detected agent but no CodeGraph guidance marker was found in the system prompt file"
		}
		if !hasToolWiring {
			return false, guidancePath, "found CodeGraph guidance but no effective MCP/tool wiring"
		}
		return true, toolPath, "found CodeGraph guidance and MCP/tool wiring"
	}
	if hasToolWiring {
		return true, toolPath, "found CodeGraph tool wiring marker"
	}
	return false, adapter.GlobalConfigDir(homeDir), "detected agent but no CodeGraph MCP or instruction marker was found"
}

func hasDetectedCodeGraphToolWiring(homeDir string) bool {
	reg, err := agents.NewDefaultRegistry()
	if err != nil {
		return false
	}
	for _, installedAgent := range agents.DiscoverSelected(reg, homeDir) {
		adapter, ok := reg.Get(installedAgent.ID)
		if !ok || !isCodeGraphCompatibleAgent(installedAgent.ID) {
			continue
		}
		if _, ok := hasCodeGraphToolWiring(homeDir, adapter); ok {
			return true
		}
	}
	return false
}

func agentDisplayName(id model.AgentID) string {
	for _, agent := range catalog.AllAgents() {
		if agent.ID == id {
			return agent.Name
		}
	}
	return string(id)
}

func defaultHomeDir() string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h
	}
	return os.Getenv("HOME")
}

func CodeGraphCommands() [][]string {
	return codeGraphCommands("npm", nil)
}

func CodeGraphCommandsForDetector(detector Detector) ([][]string, error) {
	return CodeGraphCommandsForDetectorAndTargets(detector, nil)
}

func CodeGraphCommandsForDetectorAndTargets(detector Detector, targets []string) ([][]string, error) {
	packageManager, err := detectCodeGraphPackageManager(detector)
	if err != nil {
		return nil, err
	}
	return codeGraphCommands(packageManager, targets), nil
}

func defaultPnpmGlobalBin() (string, error) {
	command := exec.Command("pnpm", "bin", "-g")
	system.EnsureCommandDir(command)
	output, err := command.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return "", fmt.Errorf("pnpm global binary directory is not usable: %s", message)
	}
	return strings.TrimSpace(string(output)), nil
}

func detectCodeGraphPackageManager(detector Detector) (string, error) {
	if detector == nil {
		detector = DetectorFunc(exec.LookPath)
	}
	if _, err := detector.LookPath("npm"); err == nil {
		return "npm", nil
	}
	if _, err := detector.LookPath("pnpm"); err == nil {
		globalBin, binErr := codeGraphPnpmGlobalBin()
		if binErr != nil {
			return "", fmt.Errorf("CodeGraph installation found pnpm, but pnpm global installs are not ready. Run `pnpm setup`, restart your shell, then rerun Brunnr-Code: %w", binErr)
		}
		if globalBin == "" {
			return "", fmt.Errorf("CodeGraph installation found pnpm, but `pnpm bin -g` returned an empty global binary directory. Run `pnpm setup`, restart your shell, then rerun Brunnr-Code")
		}
		return "pnpm", nil
	}
	return "", fmt.Errorf("CodeGraph installation requires `npm` or `pnpm` in PATH, and neither was found. Install Node.js (which ships npm) or pnpm, then rerun Brunnr-Code; it installs the CLI with `npm install -g @colbymchenry/codegraph@latest` or `pnpm add -g @colbymchenry/codegraph@latest`")
}

func codeGraphCommands(packageManager string, targets []string) [][]string {
	installCommand := []string{"npm", "install", "-g", "@colbymchenry/codegraph@latest"}
	if packageManager == "pnpm" {
		installCommand = []string{"pnpm", "add", "-g", "@colbymchenry/codegraph@latest"}
	}
	install := []string{"codegraph", "install", "--yes"}
	if len(targets) > 0 {
		install = []string{"codegraph", "install", "--target", strings.Join(targets, ","), "--location", "global", "--yes"}
	}
	return [][]string{installCommand, install}
}
