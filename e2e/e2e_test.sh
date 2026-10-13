#!/usr/bin/env bash
# e2e_test.sh — End-to-end tests for brunnr-code installer
#
# Test tiers (controlled by environment variables):
#   (default)            Tier 1: binary existence + dry-run tests (fast, no side-effects)
#   RUN_FULL_E2E=1       Tier 2: full install tests (writes to filesystem)
#   RUN_BACKUP_TESTS=1   Tier 3: backup/restore tests
#
# Usage inside Docker:
#   ./e2e_test.sh                         # Tier 1 only
#   RUN_FULL_E2E=1 ./e2e_test.sh          # Tier 1 + 2
#   RUN_BACKUP_TESTS=1 ./e2e_test.sh      # Tier 1 + 3
#   RUN_FULL_E2E=1 RUN_BACKUP_TESTS=1 ./e2e_test.sh  # All tiers
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "$SCRIPT_DIR/lib.sh"

# ---------------------------------------------------------------------------
# Resolve binary
# ---------------------------------------------------------------------------
BINARY="$(resolve_binary)"
if [ -z "$BINARY" ]; then
    echo "ERROR: brunnr-code binary not found. Build it first."
    exit 1
fi
log_info "Using binary: $BINARY"

# Side-effect E2E exercises install/injection behavior. Keep it deterministic by
# satisfying the installer's "engram/codegraph already exists on PATH" branches
# unless a maintainer explicitly opts into the live download/npm install paths.
if [ "${RUN_FULL_E2E:-0}" = "1" ] || [ "${RUN_BACKUP_TESTS:-0}" = "1" ]; then
    setup_fake_engram_binary
    setup_fake_codegraph_binary
fi

# ===========================================================================
# TIER 1 — Basic binary & dry-run tests (always run)
# ===========================================================================

# --- Category 1a: Binary basics ---

test_binary_exists() {
    log_test "Binary exists and is executable"

    if [ -x "$(command -v "$BINARY")" ] || [ -x "$BINARY" ]; then
        log_pass "Binary is executable"
    else
        log_fail "Binary not found or not executable"
    fi
}

test_binary_runs() {
    log_test "Binary runs without panic"

    if output=$($BINARY install --dry-run 2>&1); then
        log_pass "Binary exited cleanly with --dry-run"
    else
        if echo "$output" | grep -qi "panic"; then
            log_fail "Binary panicked: $output"
        else
            log_pass "Binary exited with non-zero (no panic)"
        fi
    fi
}

test_version_command() {
    log_test "Version command works"

    output=$($BINARY version 2>&1) || true

    if echo "$output" | grep -q "brunnr-code"; then
        log_pass "Version command returns binary name"
    else
        log_fail "Version command failed: $output"
    fi
}

# --- Category 1b: Dry-run output format ---

test_dry_run_output_format() {
    log_test "Dry-run output contains expected sections"

    output=$($BINARY install --dry-run 2>&1) || true

    assert_output_contains "$output" "dry-run" "Output contains 'dry-run' marker"
    assert_output_contains "$output" "Agents:" "Output contains 'Agents:' header"
    assert_output_contains "$output" "Preset:" "Output contains 'Preset:' header"
    assert_output_contains "$output" "Components order:" "Output contains 'Components order:' header"
    assert_output_contains "$output" "Platform decision:" "Output contains 'Platform decision:' header"
}

test_dry_run_platform_detection() {
    log_test "Dry-run shows platform decision"

    output=$($BINARY install --dry-run 2>&1) || true

    assert_output_contains "$output" "Platform decision" "Platform decision present in dry-run"
}

test_dry_run_detects_linux() {
    log_test "Dry-run detects Linux OS"

    # This test is only meaningful inside the Docker container (Linux).
    # Skip gracefully on macOS/other to avoid killing the test run.
    if [[ "$(uname -s)" != "Linux" ]]; then
        log_skip "Not running on Linux — platform detection test skipped"
        return 0
    fi

    output=$($BINARY install --dry-run 2>&1) || true

    assert_output_contains "$output" "os=linux" "Platform detected as Linux"
}

# --- Category 1c: Agent flag ---

test_dry_run_agent_claude_code() {
    log_test "Dry-run with --agent claude-code"

    output=$($BINARY install --agent claude-code --dry-run 2>&1) || true

    assert_output_contains "$output" "claude-code" "Dry-run output shows claude-code agent"
}

test_dry_run_agent_both() {
    log_test "Dry-run with both agents"

    output=$($BINARY install --agent claude-code --agent codex --dry-run 2>&1) || true

    assert_output_contains "$output" "claude-code" "Both agents: shows claude-code"
    assert_output_contains "$output" "codex" "Both agents: shows codex"
}

test_dry_run_agent_csv() {
    log_test "Dry-run with --agent as CSV"

    output=$($BINARY install --agent claude-code,codex --dry-run 2>&1) || true

    assert_output_contains "$output" "claude-code" "CSV agents: shows claude-code"
    assert_output_contains "$output" "codex" "CSV agents: shows codex"
}

# --- Category 1d: Single setup ---

test_dry_run_setup_is_full() {
    log_test "Dry-run always resolves the full setup"

    output=$($BINARY install --dry-run 2>&1) || true

    assert_output_contains "$output" "Preset: full" "Shows the full setup"
}

test_setup_components() {
    log_test "Setup includes every component"

    output=$($BINARY install --agent claude-code --dry-run 2>&1) || true

    local components_line
    components_line=$(echo "$output" | grep "Components order:")

    assert_output_contains "$components_line" "engram" "Setup includes engram"
    assert_output_contains "$components_line" "skills" "Setup includes skills"
    assert_output_contains "$components_line" "persona" "Setup includes persona"
    assert_output_contains "$components_line" "permissions" "Setup includes permissions"
}

test_dry_run_setup_persona_before_engram() {
    log_test "Dry-run: persona appears before engram in component order"

    output=$($BINARY install --agent claude-code --dry-run 2>&1) || true

    local components_line
    components_line=$(echo "$output" | grep "Components order:")

    # Verify all are present
    assert_output_contains "$components_line" "persona" "Setup has persona"
    assert_output_contains "$components_line" "engram" "Setup has engram"

    # Verify ordering: persona before engram
    # Extract the order string and check persona comes first
    local order_str
    order_str=$(echo "$components_line" | sed 's/.*Components order: *//')

    local persona_idx engram_idx
    persona_idx=$(echo "$order_str" | tr ',' '\n' | grep -n '^persona$' | cut -d: -f1)
    engram_idx=$(echo "$order_str" | tr ',' '\n' | grep -n '^engram$' | cut -d: -f1)

    if [ -n "$persona_idx" ] && [ -n "$engram_idx" ] && [ "$persona_idx" -lt "$engram_idx" ]; then
        log_pass "Persona ($persona_idx) before engram ($engram_idx)"
    else
        log_fail "Persona must appear before engram in component order: $order_str"
    fi
}

# --- Category 1e: Invalid input rejection ---

# The setup is fixed, so the flags that used to select presets, components,
# skills or the permissions overlay must fail as unknown flags.
test_removed_setup_flags_rejected() {
    log_test "Removed setup flags are rejected as unknown flags"

    local args output
    for args in "install --preset full" "install --component engram" "install --components engram" \
        "install --skill go-testing" "install --skills go-testing" \
        "sync --skill go-testing" "sync --skills go-testing" "sync --include-permissions"; do
        # shellcheck disable=SC2086 # word splitting of $args is intended
        if output=$($BINARY $args --dry-run 2>&1); then
            log_fail "'$args' should have been rejected"
        else
            assert_output_contains "$output" "flag provided but not defined" "'$args' rejected as unknown flag"
        fi
    done
}

test_unknown_command_rejected() {
    log_test "Unknown command is rejected"

    if $BINARY foobar 2>&1; then
        log_fail "Unknown command should have been rejected"
    else
        log_pass "Unknown command correctly rejected"
    fi
}

# ===========================================================================
# TIER 2 — Full install tests (require RUN_FULL_E2E=1)
# ===========================================================================

# --- Category 2: Claude Code component injection ---

test_cc_engram_injection() {
    log_test "Claude Code: engram injection (MCP + CLAUDE.md)"
    cleanup_test_env

    if $BINARY install --agent claude-code 2>&1; then
        # User-scope MCP registry
        local registry="$HOME/.claude.json"
        assert_file_exists "$registry" "Claude user MCP registry"
        assert_file_contains "$registry" '"mcpServers"' "Registry has mcpServers"
        assert_file_contains "$registry" '"engram"' "Registry has Engram server"
        assert_valid_json "$registry" "Claude user MCP registry is valid JSON"
        assert_file_not_exists "$HOME/.claude/mcp/engram.json" "legacy Engram MCP file is not written"

        # CLAUDE.md section
        assert_file_exists "$HOME/.claude/CLAUDE.md" "CLAUDE.md exists"
        assert_file_contains "$HOME/.claude/CLAUDE.md" "brunnr-code:engram-protocol" "CLAUDE.md has engram-protocol section marker"
        assert_file_contains "$HOME/.claude/CLAUDE.md" "mem_save" "CLAUDE.md has real Engram content (mem_save)"
        assert_file_size_min "$HOME/.claude/CLAUDE.md" 500 "CLAUDE.md has substantial content"
    else
        log_fail "engram install command failed"
    fi
}


test_cc_persona() {
    log_test "Claude Code: persona injection (Principal Engineer output style)"
    cleanup_test_env

    if $BINARY install --agent claude-code 2>&1; then
        assert_file_exists "$HOME/.claude/CLAUDE.md" "CLAUDE.md exists"
        assert_file_contains "$HOME/.claude/CLAUDE.md" "brunnr-code:persona" "CLAUDE.md has persona section marker"
        # Claude has an active output-style channel — the CLAUDE.md persona
        # section is now a residual; the mentor identity lives in the output
        # style (design.md Decision 1).
        assert_file_contains "$HOME/.claude/CLAUDE.md" "Persona Voice" "CLAUDE.md persona residual points to the output style"
        assert_file_not_contains "$HOME/.claude/CLAUDE.md" "Senior Architect" "CLAUDE.md persona residual carries no tone content"
        assert_file_exists "$HOME/.claude/output-styles/principal-engineer.md" "Principal Engineer output-style file exists"
        assert_file_contains "$HOME/.claude/output-styles/principal-engineer.md" "Weakening the verifier is the one unforgivable move" "Principal Engineer output-style carries the verification-first content"
        assert_file_not_contains "$HOME/.claude/output-styles/principal-engineer.md" "Rioplatense\|voseo\|loco\|ponete las pilas" "Principal Engineer output-style excludes regional language"
        assert_file_not_exists "$HOME/.claude/output-styles/neutral.md" "Retired Neutral output-style file is absent"
    else
        log_fail "persona install command failed"
    fi
}


test_cc_skills_full() {
    log_test "Claude Code: skills injection (8 fixed setup skills)"
    cleanup_test_env

    if $BINARY install --agent claude-code 2>&1; then
        local skills_dir="$HOME/.claude/skills"
        assert_dir_exists "$skills_dir" "Claude skills directory"

        assert_file_count "$skills_dir" "SKILL.md" 8 "Setup installs exactly 8 skill files"

        # Every fixed setup skill is present
        local skill
        for skill in judgment-day go-testing skill-creator skill-improver skill-registry \
            chained-pr cognitive-doc-design work-unit-commits; do
            assert_file_exists "$skills_dir/$skill/SKILL.md" "$skill SKILL.md"
        done
        assert_file_not_exists "$skills_dir/branch-pr/SKILL.md" "branch-pr NOT installed"
        assert_file_not_exists "$skills_dir/issue-creation/SKILL.md" "issue-creation NOT installed"

        # Real content check
        assert_file_size_min "$skills_dir/go-testing/SKILL.md" 200 "go-testing skill has real content"
        assert_file_size_min "$skills_dir/skill-creator/SKILL.md" 200 "skill-creator skill has real content"
        assert_file_size_min "$skills_dir/chained-pr/SKILL.md" 200 "chained-pr skill has real content"
        assert_file_size_min "$skills_dir/work-unit-commits/SKILL.md" 200 "work-unit-commits skill has real content"
        assert_file_size_min "$skills_dir/skill-registry/SKILL.md" 200 "skill-registry skill has real content"
    else
        log_fail "skills (full) install command failed"
    fi
}

test_cc_permissions_injection() {
    log_test "Claude Code: permissions injection"
    cleanup_test_env

    if $BINARY install --agent claude-code 2>&1; then
        local settings="$HOME/.claude/settings.json"
        assert_file_exists "$settings" "Claude settings.json"
        assert_file_contains "$settings" '"permissions"' "Has permissions key"
        assert_file_contains "$settings" '"deny"' "Has deny list"
        assert_valid_json "$settings" "settings.json is valid JSON"
    else
        log_fail "permissions install command failed"
    fi
}

# --- Category 4: Full setup integration ---

test_full_setup_claude_code() {
    log_test "Full setup: Claude Code (all components coexist)"
    cleanup_test_env

    # The fake Engram binary on PATH keeps this free of package downloads.
    if $BINARY install --agent claude-code 2>&1; then
        local claude_md="$HOME/.claude/CLAUDE.md"
        local settings="$HOME/.claude/settings.json"

        # ODD routing, memory and persona coexist without duplicate sections.
        assert_file_exists "$claude_md" "CLAUDE.md exists"
        assert_file_contains "$claude_md" "brunnr-code:agent-routing" "Has ODD routing section"
        assert_file_contains "$claude_md" "brunnr-code:engram-protocol" "Has memory section"
        assert_file_contains "$claude_md" "brunnr-code:persona" "Has persona section"
        assert_no_duplicate_section "$claude_md" "agent-routing" "No duplicate ODD routing section"
        assert_no_duplicate_section "$claude_md" "persona" "No duplicate persona section"

        # settings.json should have permissions
        assert_file_exists "$settings" "settings.json exists"
        assert_file_contains "$settings" '"permissions"' "Has permissions"
        assert_valid_json "$settings" "settings.json is valid JSON"

        # MCP registration lives in the ~/.claude.json user registry, the only
        # user-scope file Claude Code reads MCP servers from (issue #1868).
        local registry="$HOME/.claude.json"
        assert_file_exists "$registry" "Claude user MCP registry (~/.claude.json)"
        assert_file_contains "$registry" '"mcpServers"' "Has MCP servers"
        assert_file_contains "$registry" '"engram"' "Has Engram MCP"
        assert_file_contains "$registry" '"codegraph"' "Has CodeGraph MCP"
        assert_valid_json "$registry" "user registry is valid JSON"

        # Skills
        assert_file_count_min "$HOME/.claude/skills" "SKILL.md" 8 "At least 8 foundation skill files"

        log_pass "Full setup: all Claude Code components coexist"
    else
        log_fail "Full setup (Claude Code) install command failed"
    fi
}

test_full_setup_both_agents() {
    log_test "Full setup: both agents"
    cleanup_test_env

    if $BINARY install --agent claude-code --agent codex 2>&1; then
        # Claude Code
        assert_file_exists "$HOME/.claude/CLAUDE.md" "Claude CLAUDE.md"
        assert_file_contains "$HOME/.claude/CLAUDE.md" "brunnr-code:agent-routing" "Claude has ODD routing"
        assert_file_contains "$HOME/.claude.json" '"engram"' "Claude Engram MCP"
        assert_file_count_min "$HOME/.claude/skills" "SKILL.md" 8 "Claude foundation skills"

        # Codex
        assert_file_contains "$HOME/.codex/AGENTS.md" "brunnr-code:agent-routing" "Codex has ODD routing"
        assert_file_count_min "$HOME/.codex/skills" "SKILL.md" 8 "Codex foundation skills"
        assert_file_contains "$HOME/.codex/config.toml" "[mcp_servers.engram]" "Codex Engram MCP"
        assert_file_contains "$HOME/.codex/config.toml" '\[mcp_servers\.codegraph\]' "Codex CodeGraph MCP"

        log_pass "Full setup: both agents have matching components"
    else
        log_fail "Full setup (both agents) install command failed"
    fi
}

# --- Category 5: Content validation ---

test_content_claude_md_sections_substantial() {
    log_test "Content validation: CLAUDE.md sections are substantial"
    cleanup_test_env

    $BINARY install --agent claude-code 2>&1 || true

    local claude_md="$HOME/.claude/CLAUDE.md"
    if [ -f "$claude_md" ]; then
        assert_file_contains "$claude_md" "brunnr-code:agent-routing" "CLAUDE.md has ODD routing"
        assert_file_size_min "$claude_md" 1000 "CLAUDE.md with routing, memory and persona >= 1000 bytes"
    else
        log_fail "CLAUDE.md not created"
    fi
}

test_content_skills_are_real() {
    log_test "Content validation: skill files contain real instructions"
    cleanup_test_env

    $BINARY install --agent claude-code 2>&1 || true

    local skills_dir="$HOME/.claude/skills"
    if [ -d "$skills_dir" ]; then
        # Check every SKILL.md is at least 200 bytes (real content, not stubs)
        local all_ok=true
        while IFS= read -r skill_file; do
            local size
            size=$(wc -c < "$skill_file" | tr -d ' ')
            if [ "$size" -lt 200 ]; then
                log_fail "Skill file too small ($size bytes): $skill_file"
                all_ok=false
            fi
        done < <(find "$skills_dir" -name "SKILL.md" -type f)

        if $all_ok; then
            log_pass "All skill files have >= 200 bytes of real content"
        fi
    else
        log_fail "Skills directory not created"
    fi
}

test_content_mcp_json_valid() {
    log_test "Content validation: MCP JSON files are parseable"
    cleanup_test_env

    $BINARY install --agent claude-code 2>&1 || true

    # Claude Code reads user-scoped MCP servers from ~/.claude.json. The legacy
    # ~/.claude/mcp directory is intentionally no longer created.
    local registry="$HOME/.claude.json"
    assert_file_exists "$registry" "Claude user MCP registry"
    assert_valid_json "$registry" "Claude user MCP registry is valid JSON"
    assert_file_contains "$registry" '"engram"' "Registry has Engram server"
    assert_file_not_exists "$HOME/.claude/mcp/engram.json" "legacy Engram MCP file is not written"
}

# --- Category 6: Idempotency ---

test_idempotent_persona_claude() {
    log_test "Idempotency: persona on Claude Code (no duplicate sections)"
    cleanup_test_env

    $BINARY install --agent claude-code 2>&1 || true
    $BINARY install --agent claude-code 2>&1 || true

    local claude_md="$HOME/.claude/CLAUDE.md"
    if [ -f "$claude_md" ]; then
        assert_no_duplicate_section "$claude_md" "persona" "No duplicate persona section after 2 runs"
    else
        log_fail "CLAUDE.md not found"
    fi
}

test_idempotent_engram_claude() {
    log_test "Idempotency: engram on Claude Code (no duplicate sections)"
    cleanup_test_env

    $BINARY install --agent claude-code 2>&1 || true
    $BINARY install --agent claude-code 2>&1 || true

    local claude_md="$HOME/.claude/CLAUDE.md"
    if [ -f "$claude_md" ]; then
        assert_no_duplicate_section "$claude_md" "engram-protocol" "No duplicate engram section after 2 runs"

        # Also check the user registry remains valid.
        local mcp_file="$HOME/.claude.json"
        if [ -f "$mcp_file" ]; then
            assert_valid_json "$mcp_file" "Claude user MCP registry still valid after 2 runs"
        fi
    else
        log_fail "CLAUDE.md not found"
    fi
}

# ─── Codex parity tests ───────────────────────────────────────────────────────

test_codex_engram_injection() {
    log_test "Codex: engram injection writes config.toml + instruction files"
    cleanup_test_env

    if $BINARY install --agent codex 2>&1; then
        local config_toml="$HOME/.codex/config.toml"
        local instructions="$HOME/.codex/engram-instructions.md"
        local compact="$HOME/.codex/engram-compact-prompt.md"

        assert_file_exists "$config_toml" "Codex config.toml"
        assert_file_contains "$config_toml" '[mcp_servers.engram]' "config.toml has [mcp_servers.engram]"
        assert_file_contains "$config_toml" 'command = ".*engram"' "config.toml has correct command"
        assert_file_contains "$config_toml" '"--tools=agent"' "config.toml has --tools=agent"
        assert_file_contains "$config_toml" 'model_instructions_file' "config.toml references instruction file"
        assert_file_contains "$config_toml" 'experimental_compact_prompt_file' "config.toml references compact prompt"

        assert_file_exists "$instructions" "engram-instructions.md"
        assert_file_contains "$instructions" 'mem_save' "Instructions have memory protocol content"

        assert_file_exists "$compact" "engram-compact-prompt.md"
        assert_file_contains "$compact" 'FIRST ACTION REQUIRED' "Compact prompt has required sentinel"
    else
        log_fail "Codex engram install command failed"
    fi
}

test_codex_engram_idempotent() {
    log_test "Codex: engram injection is idempotent (no duplicate blocks)"
    cleanup_test_env

    $BINARY install --agent codex 2>&1 || true
    $BINARY install --agent codex 2>&1 || true

    local config_toml="$HOME/.codex/config.toml"
    if [ -f "$config_toml" ]; then
        local count
        count=$(grep -c '\[mcp_servers\.engram\]' "$config_toml" || true)
        if [ "$count" -ne 1 ]; then
            log_fail "config.toml has $count [mcp_servers.engram] blocks after 2 runs (want exactly 1)"
        else
            log_pass "config.toml has exactly 1 [mcp_servers.engram] block after 2 runs"
        fi
    else
        log_fail "config.toml not found after 2 runs"
    fi
}

test_idempotent_skills_claude() {
    log_test "Idempotency: skills injection produces same files"
    cleanup_test_env

    $BINARY install --agent claude-code 2>&1 || true
    # Capture file hashes
    local first_hashes
    first_hashes=$(find "$HOME/.claude/skills" -name "SKILL.md" -exec md5sum {} \; 2>/dev/null | sort)

    $BINARY install --agent claude-code 2>&1 || true
    local second_hashes
    second_hashes=$(find "$HOME/.claude/skills" -name "SKILL.md" -exec md5sum {} \; 2>/dev/null | sort)

    if [ "$first_hashes" = "$second_hashes" ] && [ -n "$first_hashes" ]; then
        log_pass "Idempotent: same skill files after two runs"
    else
        log_fail "Skill files changed between runs"
    fi
}

test_idempotent_full_claude() {
    log_test "Idempotency: full setup on Claude Code"
    cleanup_test_env

    $BINARY install --agent claude-code 2>&1 || true
    local first_md_hash
    first_md_hash=$(md5sum "$HOME/.claude/CLAUDE.md" 2>/dev/null | cut -d' ' -f1)
    # Snapshot settings.json for semantic comparison (engram setup may reorder
    # top-level keys on re-run — see engram binary's non-deterministic map
    # serialization). Byte-exact hashing would false-fail on harmless reorder.
    cp "$HOME/.claude/settings.json" /tmp/gai_settings_run1.json 2>/dev/null || true

    $BINARY install --agent claude-code 2>&1 || true
    local second_md_hash
    second_md_hash=$(md5sum "$HOME/.claude/CLAUDE.md" 2>/dev/null | cut -d' ' -f1)

    if [ "$first_md_hash" = "$second_md_hash" ] && [ -n "$first_md_hash" ]; then
        log_pass "Idempotent: CLAUDE.md identical after 2 runs"
    else
        log_fail "CLAUDE.md changed between runs"
    fi
    if [ -f /tmp/gai_settings_run1.json ] && [ -f "$HOME/.claude/settings.json" ]; then
        if json_files_equal /tmp/gai_settings_run1.json "$HOME/.claude/settings.json"; then
            log_pass "Idempotent: settings.json identical after 2 runs"
        else
            log_fail "settings.json changed between runs"
        fi
    else
        log_fail "settings.json missing after install"
    fi
    rm -f /tmp/gai_settings_run1.json
}

# --- Category 8: Edge cases ---

test_edge_multiple_agents() {
    log_test "Edge case: multiple agents share the Engram MCP setup"
    cleanup_test_env

    if $BINARY install --agent claude-code --agent codex 2>&1; then
        # Both agents should register the Engram MCP server
        assert_file_contains "$HOME/.claude.json" '"engram"' "Claude Engram MCP"
        assert_file_contains "$HOME/.codex/config.toml" "[mcp_servers.engram]" "Codex Engram MCP"
    else
        log_fail "Multiple agents install command failed"
    fi
}


# --- Category 7: Injection integrity (guards against issue #4 regression) ---

test_integrity_setup_all_skills_nonempty() {
    log_test "Integrity: full setup — every SKILL.md is non-empty"
    cleanup_test_env

    if $BINARY install --agent claude-code 2>&1; then
        local skill_dir="$HOME/.claude/skills"
        assert_file_count "$skill_dir" "SKILL.md" 8 "Setup installs 8 foundation skills"
        local all_ok=true
        local empty_count=0

        while IFS= read -r skill_file; do
            local size
            size=$(wc -c < "$skill_file" | tr -d ' ')
            if [ "$size" -lt 100 ]; then
                log_fail "Skill file empty/corrupt ($size bytes): $skill_file"
                all_ok=false
                empty_count=$((empty_count + 1))
            fi
        done < <(find "$skill_dir" -name "SKILL.md" -type f)

        local total
        total=$(find "$skill_dir" -name "SKILL.md" -type f | wc -l | tr -d ' ')
        if $all_ok && [ "$total" -eq 8 ]; then
            log_pass "All 8 foundation skill files have >= 100 bytes of real content"
        else
            log_fail "Expected 8 non-empty foundation skills (found $total; $empty_count empty or corrupt)"
        fi
    else
        log_fail "Setup install for integrity check failed"
    fi
}

# ===========================================================================
# TIER 3 — Backup / restore tests (require RUN_BACKUP_TESTS=1)
# ===========================================================================

test_backup_created_on_install() {
    log_test "Backup snapshot created during install"
    cleanup_test_env
    setup_fake_configs

    if $BINARY install --agent claude-code 2>&1; then
        local backup_count
        backup_count=$(find "$HOME/.brunnr-code/backups" -mindepth 1 -maxdepth 1 -type d 2>/dev/null | wc -l | tr -d ' ')
        if [ "$backup_count" -gt 0 ]; then
            log_pass "Backup directory created ($backup_count snapshots)"
        else
            log_fail "No backup directory found"
        fi
    else
        log_fail "Install with backup failed"
    fi
}

test_backup_contains_original_files() {
    log_test "Backup snapshot contains original config files"
    cleanup_test_env
    setup_fake_configs

    if $BINARY install --agent claude-code 2>&1; then
        local latest_backup
        latest_backup=$(find "$HOME/.brunnr-code/backups" -mindepth 1 -maxdepth 1 -type d 2>/dev/null | sort | tail -1)
        if [ -n "$latest_backup" ]; then
            local file_count
            file_count=$(find "$latest_backup" -type f 2>/dev/null | wc -l | tr -d ' ')
            if [ "$file_count" -gt 0 ]; then
                log_pass "Backup contains $file_count file(s)"
            else
                log_fail "Backup directory is empty"
            fi
        else
            log_fail "No backup snapshot directory found"
        fi
    else
        log_fail "Install for backup test failed"
    fi
}

test_backup_manifest_exists() {
    log_test "Backup manifest file exists"
    cleanup_test_env
    setup_fake_configs

    if $BINARY install --agent claude-code 2>&1; then
        local latest_backup
        latest_backup=$(find "$HOME/.brunnr-code/backups" -mindepth 1 -maxdepth 1 -type d 2>/dev/null | sort | tail -1)
        if [ -n "$latest_backup" ]; then
            if [ -f "$latest_backup/manifest.json" ]; then
                assert_valid_json "$latest_backup/manifest.json" "Backup manifest is valid JSON"
            else
                log_fail "manifest.json not found in backup: $latest_backup"
            fi
        else
            log_fail "No backup snapshot found"
        fi
    else
        log_fail "Install for manifest test failed"
    fi
}

test_backup_idempotent_install() {
    log_test "Idempotent: running install twice produces same result (with backup)"
    cleanup_test_env

    $BINARY install --agent claude-code 2>&1 || true
    local first_content
    first_content=$(cat "$HOME/.claude/settings.json" 2>/dev/null)

    $BINARY install --agent claude-code 2>&1 || true
    local second_content
    second_content=$(cat "$HOME/.claude/settings.json" 2>/dev/null)

    if [ "$first_content" = "$second_content" ] && [ -n "$first_content" ]; then
        log_pass "Idempotent: same config after two runs (with backup)"
    else
        log_fail "Config changed between runs (with backup)"
    fi
}

test_backup_multiple_snapshots() {
    log_test "Multiple installs create multiple backup snapshots"
    cleanup_test_env
    setup_fake_configs

    $BINARY install --agent claude-code 2>&1 || true
    sleep 0.1
    $BINARY install --agent claude-code 2>&1 || true

    local backup_count
    backup_count=$(find "$HOME/.brunnr-code/backups" -mindepth 1 -maxdepth 1 -type d 2>/dev/null | wc -l | tr -d ' ')
    if [ "$backup_count" -ge 2 ]; then
        log_pass "Multiple backup snapshots created ($backup_count)"
    else
        log_fail "Expected >= 2 backup snapshots, got $backup_count"
    fi
}

test_backup_claude_code_files() {
    log_test "Backup captures Claude Code files"
    cleanup_test_env
    setup_fake_configs

    if $BINARY install --agent claude-code 2>&1; then
        local latest_backup
        latest_backup=$(find "$HOME/.brunnr-code/backups" -mindepth 1 -maxdepth 1 -type d 2>/dev/null | sort | tail -1)
        if [ -n "$latest_backup" ] && [ -f "$latest_backup/manifest.json" ]; then
            log_pass "Claude Code backup snapshot with manifest created"
        else
            log_fail "No proper backup found for Claude Code install"
        fi
    else
        log_fail "Install for Claude backup test failed"
    fi
}

# ===========================================================================
# Test execution
# ===========================================================================

log_info "=== Tier 1: Basic binary & dry-run tests ==="

# Category 1a: Binary basics
test_binary_exists
test_binary_runs
test_version_command

# Category 1b: Dry-run output format
test_dry_run_output_format
test_dry_run_platform_detection
test_dry_run_detects_linux

# Category 1c: Agent flags
test_dry_run_agent_claude_code
test_dry_run_agent_both
test_dry_run_agent_csv

# Category 1d: Single setup
test_dry_run_setup_is_full
test_setup_components
test_dry_run_setup_persona_before_engram

# Category 1e: Invalid inputs
test_removed_setup_flags_rejected
test_unknown_command_rejected

if [ "${RUN_FULL_E2E:-0}" = "1" ]; then
    log_info ""
    log_info "=== Tier 2: Component injection tests ==="

    # Category 2: Claude Code injection
    test_cc_engram_injection
    test_cc_persona
    test_cc_skills_full
    test_cc_permissions_injection

    # Category 4: Full setup integration
    test_full_setup_claude_code
    test_full_setup_both_agents

    # Category 5: Content validation
    test_content_claude_md_sections_substantial
    test_content_skills_are_real
    test_content_mcp_json_valid

    # Category 6: Idempotency
    test_idempotent_persona_claude
    test_idempotent_engram_claude
    test_idempotent_skills_claude
    test_idempotent_full_claude

    # Category 6b: Codex engram parity
    test_codex_engram_injection
    test_codex_engram_idempotent

    # Category 8: Edge cases
    test_edge_multiple_agents

    # Category 7: Injection integrity (issue #4 regression guard)
    test_integrity_setup_all_skills_nonempty
else
    log_skip "Tier 2 tests (set RUN_FULL_E2E=1 to enable)"
fi

if [ "${RUN_BACKUP_TESTS:-0}" = "1" ]; then
    log_info ""
    log_info "=== Tier 3: Backup/restore tests ==="
    test_backup_created_on_install
    test_backup_contains_original_files
    test_backup_manifest_exists
    test_backup_idempotent_install
    test_backup_multiple_snapshots
    test_backup_claude_code_files
else
    log_skip "Tier 3 tests (set RUN_BACKUP_TESTS=1 to enable)"
fi

# ---------------------------------------------------------------------------
# Summary & exit
# ---------------------------------------------------------------------------
print_summary
