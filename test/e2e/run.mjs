#!/usr/bin/env node

import fs from "node:fs/promises";
import path from "node:path";
import process from "node:process";
import { spawn } from "node:child_process";
import { fileURLToPath } from "node:url";
import YAML from "yaml";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const repoRoot = path.resolve(__dirname, "..", "..");

const DEFAULT_CONFIG = path.join(repoRoot, "test", "e2e", "run.yaml");
const DEFAULT_TIMEOUT_MS = 20 * 60 * 1000;
const LONG_RUNNING_STEP_HEARTBEAT_MS = 30 * 1000;
const PROCESS_TERMINATION_GRACE_MS = 5 * 1000;
const DEFAULT_SCENARIO_ID = "default";

const TOOL_SKILL_ROOT = {
  claude: ".claude/skills",
  codex: ".agents/skills",
  gemini: ".gemini/skills",
  opencode: ".opencode/skills",
  copilot: ".github/skills",
  pi: ".pi/skills",
};

async function main() {
  const options = parseArgs(process.argv.slice(2));
  const configPath = path.resolve(repoRoot, options.config);
  const manifest = YAML.parse(await fs.readFile(configPath, "utf8"));
  const scenarios = normalizeConfig(manifest, configPath, options.scenario);

  const cliSourceBinaryPath = await buildArchetipoBinary();

  const results = [];
  for (const scenario of scenarios) {
    const agentLabel = `${scenario.agent.id}`;
    console.log(`\n==> Running scenario "${scenario.id}" (agent: ${agentLabel}, model: ${scenario.agent.model ?? "no model"})`);
    const result = await runConfiguredScenario({
      scenario,
      configPath,
      timeoutMs: options.timeoutMs ?? DEFAULT_TIMEOUT_MS,
      cliSourceBinaryPath,
    });
    results.push(result);
    console.log(formatResultLine(result));
  }

  const hasFailure = results.some((r) => r.status === "fail");
  const hasSkip = results.some((r) => r.status === "skip");
  const hasPass = results.some((r) => r.status === "pass");

  console.log(`\n${results.length > 1 ? "====== E2E Summary ======" : "Summary:"}`);
  for (const result of results) {
    console.log(`- ${formatResultLine(result)}`);
  }

  if (results.length === 0) {
    console.error("No scenarios matched.");
    process.exit(1);
  }

  process.exit(hasFailure || (!hasPass && !hasSkip) ? 1 : 0);
}

function parseArgs(argv) {
  const options = {
    config: DEFAULT_CONFIG,
  };

  for (let index = 0; index < argv.length; index += 1) {
    const arg = argv[index];
    switch (arg) {
      case "--config":
        options.config = argv[++index];
        break;
      case "--scenario":
      case "--scenarios":
        options.scenario = argv[++index];
        break;
      case "--timeout-ms":
        options.timeoutMs = Number(argv[++index]);
        break;
      case "--help":
      case "-h":
        printHelp();
        process.exit(0);
        break;
      default:
        throw new Error(`Unknown argument: ${arg}`);
    }
  }

  return options;
}

function printHelp() {
  console.log(`ARchetipo E2E runner

Each scenario is driven entirely by its fixture (the copied .archetipo/config.yaml
decides connector, worktree, etc.).

Usage:
  node ./test/e2e/run.mjs [--config test/e2e/run.yaml] [--scenario scenario-name]
  npm run test:e2e
  npm run test:e2e -- --scenario worktree-implement
`);
}

function normalizeConfig(manifest, configPath, filterScenarios) {
  // Agents + Scenarios format
  const agents = manifest?.agents;
  const rawScenarios = manifest?.scenarios;

  if (!agents || typeof agents !== "object" || Object.keys(agents).length === 0) {
    throw new Error(`Missing or empty 'agents' object in ${configPath}`);
  }
  if (!rawScenarios || typeof rawScenarios !== "object" || Object.keys(rawScenarios).length === 0) {
    throw new Error(`Missing or empty 'scenarios' object in ${configPath}`);
  }

  // Validate agents
  for (const [agentId, agent] of Object.entries(agents)) {
    if (!agent || typeof agent !== "object") {
      throw new Error(`agents.${agentId} must be an object in ${configPath}`);
    }
    for (const key of ["tool", "command"]) {
      if (!agent[key] || typeof agent[key] !== "string") {
        throw new Error(`agents.${agentId}.${key} must be a non-empty string in ${configPath}`);
      }
    }
    if (!Array.isArray(agent.args) || agent.args.length === 0 || !agent.args.every((arg) => typeof arg === "string")) {
      throw new Error(`agents.${agentId}.args must be a non-empty list of strings in ${configPath}`);
    }
  }

  // Build resolved scenarios
  const scenarios = [];
  for (const [scenarioId, rawScenario] of Object.entries(rawScenarios)) {
    if (!rawScenario || typeof rawScenario !== "object") {
      throw new Error(`scenarios.${scenarioId} must be an object in ${configPath}`);
    }
    const agentId = rawScenario.agent;
    if (!agentId || typeof agentId !== "string") {
      throw new Error(`scenarios.${scenarioId}.agent must be a non-empty string referencing an agent in ${configPath}`);
    }
    const agent = agents[agentId];
    if (!agent) {
      throw new Error(`scenarios.${scenarioId} references unknown agent '${agentId}' in ${configPath}`);
    }
    if (!Array.isArray(rawScenario.prompts) || rawScenario.prompts.length === 0 || !rawScenario.prompts.every((prompt) => typeof prompt === "string")) {
      throw new Error(`scenarios.${scenarioId}.prompts must be a non-empty list of strings in ${configPath}`);
    }
    if (rawScenario.fixture !== undefined && (typeof rawScenario.fixture !== "string" || rawScenario.fixture.trim() === "")) {
      throw new Error(`scenarios.${scenarioId}.fixture must be a non-empty string when specified in ${configPath}`);
    }
    for (const key of ["archetipo_pre_commands", "archetipo_post_commands"]) {
      if (rawScenario[key] !== undefined && (!Array.isArray(rawScenario[key]) || !rawScenario[key].every((cmd) => typeof cmd === "string" && cmd.trim() !== ""))) {
        throw new Error(`scenarios.${scenarioId}.${key} must be a list of non-empty strings when specified in ${configPath}`);
      }
    }
    if (rawScenario.verify_integrate !== undefined && (!Array.isArray(rawScenario.verify_integrate) || !rawScenario.verify_integrate.every((code) => typeof code === "string" && code.trim() !== ""))) {
      throw new Error(`scenarios.${scenarioId}.verify_integrate must be a list of non-empty strings when specified in ${configPath}`);
    }
    scenarios.push({
      id: scenarioId,
      agentId,
      agent: { id: agentId, ...agent },
      prompts: rawScenario.prompts,
      env_required: rawScenario.env_required ?? agent.env_required,
      fixture: rawScenario.fixture,
      archetipo_pre_commands: rawScenario.archetipo_pre_commands ?? [],
      archetipo_post_commands: rawScenario.archetipo_post_commands ?? [],
      verify_integrate: rawScenario.verify_integrate ?? [],
    });
  }

  return filterScenarioList(scenarios, filterScenarios, configPath);
}

function filterScenarioList(scenarios, filter, configPath) {
  if (!filter) {
    return scenarios;
  }
  const requested = filter.split(",").map((s) => s.trim()).filter(Boolean);
  const filtered = scenarios.filter((s) => requested.includes(s.id));
  const found = new Set(filtered.map((s) => s.id));
  const missing = requested.filter((id) => !found.has(id));
  if (missing.length > 0) {
    const available = scenarios.map((s) => s.id).join(", ");
    throw new Error(`Scenario(s) not found: ${missing.join(", ")}. Available scenarios: ${available}`);
  }
  return filtered;
}

async function buildArchetipoBinary() {
  const goCheck = await ensureCommand("go");
  if (goCheck.skip) {
    throw new Error(
      "ARchetipo e2e requires Go to compile the CLI from source. Install Go and re-run.",
    );
  }

  const binDir = path.join(repoRoot, "test", "e2e", ".bin");
  await fs.mkdir(binDir, { recursive: true });
  const binName = process.platform === "win32" ? "archetipo.exe" : "archetipo";
  const binPath = path.join(binDir, binName);

  console.log(` -> [build] compiling archetipo CLI -> ${binPath}`);
  const build = await runProbe("go", ["build", "-o", binPath, "./cmd/archetipo"], {
    cwd: path.join(repoRoot, "cli"),
  });
  if (!build.ok) {
    throw new Error(`go build failed (exit ${build.code}): ${build.stderr || build.stdout}`);
  }
  console.log(` <- [build] archetipo CLI ready`);
  return binPath;
}

async function runConfiguredScenario({ scenario, configPath, timeoutMs, cliSourceBinaryPath }) {
  const agent = scenario.agent;
  const toolSkillRoot = TOOL_SKILL_ROOT[agent.tool];
  if (!toolSkillRoot) {
    return {
      scenario: scenario.id,
      agent: agent.id,
      model: agent.model,
      status: "skip",
      reason: `Unsupported installer tool '${agent.tool}'`,
    };
  }

  const workspaceRoot = path.join(repoRoot, "test", "workspaces", scenario.id);
  const runRoot = await createRunRoot(workspaceRoot);
  const sandboxDir = path.join(runRoot, "sandbox");
  const reportPath = path.join(runRoot, "report.html");
  const summaryPath = path.join(runRoot, "summary.json");

  logRunStepStart(scenario.id, "workspace", `Creating sandbox at ${sandboxDir}`);
  await fs.mkdir(path.join(sandboxDir, "docs"), { recursive: true });
  const cliBinaryPath = await copyCliBinaryToSandbox({ scenario, cliSourceBinaryPath, sandboxDir });
  logRunStepDone(scenario.id, "workspace", `Sandbox ready in ${sandboxDir}`);

  const report = createRunReport({
    scenario,
    configPath,
    runRoot,
    sandboxDir,
    fixtureSourcePath: null,
    reportPath,
  });
  const context = {
    scenario,
    agent,
    configPath,
    runRoot,
    sandboxDir,
    report,
    reportPath,
    summaryPath,
    timeoutMs,
    toolSkillRoot,
    cliBinaryPath,
    cliEnv: { ARCHETIPO_DATA_DIR: repoRoot },
    skillRoot(skillName) {
      return path.join(sandboxDir, toolSkillRoot, skillName);
    },
  };

  async function finish(result) {
    const finalResult = finalizeResult(context, result);
    report.result = finalResult;
    await writeHtmlReport(context);
    await writeSummary(finalResult, summaryPath);
    return finalResult;
  }

  try {
    logRunStepStart(scenario.id, "prepare", `Checking local command '${agent.command}'`);
    const prep = await ensureCommand(agent.command);
    if (prep.skip) {
      return finish({ status: "skip", reason: prep.reason });
    }
    logRunStepDone(scenario.id, "prepare", `Command '${agent.command}' is available`);

    logRunStepStart(scenario.id, "env", "Validating required environment");
    await verifyRequiredEnv(agent);
    logRunStepDone(scenario.id, "env", "Environment looks good");

    logRunStepStart(scenario.id, "install", `Installing ARchetipo assets for tool '${agent.tool}'`);
    await installWorkspace(context);
    logRunStepDone(scenario.id, "install", "Installation completed");

    logRunStepStart(scenario.id, "verify-install", "Checking installed files");
    await verifyInstallation(context);
    logRunStepDone(scenario.id, "verify-install", "Installed files verified");

    if (scenario.fixture) {
      logRunStepStart(scenario.id, "fixture", "Overlaying fixture onto the sandbox");
      report.fixtureSourcePath = await copyFixture(context);
      logRunStepDone(scenario.id, "fixture", "Fixture overlay ready");
    }

    logRunStepStart(scenario.id, "git-init", "Initializing sandbox git repository");
    await initSandboxGitRepo(context);
    logRunStepDone(scenario.id, "git-init", "Sandbox git repository ready");

    assertSandboxBinary(context);
    for (let index = 0; index < scenario.archetipo_pre_commands.length; index += 1) {
      const line = scenario.archetipo_pre_commands[index];
      const step = `pre-${index + 1}`;
      logRunStepStart(scenario.id, step, `Running archetipo ${line}`);
      const preRun = await runReportedCommand({
        ...context,
        step,
        command: context.cliBinaryPath,
        args: line.split(/\s+/).filter(Boolean),
      });
      if (!preRun.ok) {
        return finish(classifyRunFailure(context, step, preRun));
      }
      logRunStepDone(scenario.id, step, "Pre-command completed");
    }

    for (let index = 0; index < scenario.prompts.length; index += 1) {
      const prompt = scenario.prompts[index];
      const step = `prompt-${index + 1}`;
      const invocation = buildPromptInvocation(context, prompt);
      logRunStepStart(scenario.id, step, `Running ${invocation.skill}`);
      const promptRun = await runReportedCommand({
        ...context,
        step,
        ...invocation,
      });
      if (!promptRun.ok) {
        return finish(classifyRunFailure(context, step, promptRun));
      }
      logRunStepDone(scenario.id, step, "Prompt completed");
    }

    const integrationStates = new Map();
    for (const code of scenario.verify_integrate) {
      const step = `capture-integrate-state-${code}`;
      logRunStepStart(scenario.id, step, `Capturing ${code}'s branch tip before integration`);
      integrationStates.set(code, await captureIntegrationState(context, code, step));
      logRunStepDone(scenario.id, step, "Integration state captured");
    }

    for (let index = 0; index < scenario.archetipo_post_commands.length; index += 1) {
      const line = scenario.archetipo_post_commands[index];
      const step = `post-${index + 1}`;
      logRunStepStart(scenario.id, step, `Running archetipo ${line}`);
      const postRun = await runReportedCommand({
        ...context,
        step,
        command: context.cliBinaryPath,
        args: line.split(/\s+/).filter(Boolean),
      });
      if (!postRun.ok) {
        return finish(classifyRunFailure(context, step, postRun));
      }
      logRunStepDone(scenario.id, step, "Post-command completed");
    }

    for (const code of scenario.verify_integrate) {
      const step = `verify-integrate-${code}`;
      logRunStepStart(scenario.id, step, `Verifying ${code} reached DONE and its worktree was cleaned up`);
      await verifyIntegration(context, code, integrationStates.get(code), step);
      logRunStepDone(scenario.id, step, "Integration verified");
    }

    return finish({
      status: "pass",
      sandboxDir,
    });
  } catch (error) {
    if (error instanceof SkipError) {
      return finish(error);
    }

    return finish({
      status: "fail",
      reason: error.message,
      sandboxDir,
    });
  }
}

// copyFixture overlays a fixture directory onto the sandbox root. The fixture
// can carry anything the scenario needs as starting state: `docs/PRD.md`, an
// `.archetipo/` tree (config + backlog + specs + plans), etc. The `.archetipo/
// config.yaml` it brings overwrites the one `init` produced, so it is the
// fixture — not a CLI flag — that decides connector, worktree, and the rest. The
// fixture path is resolved relative to the config file.
async function copyFixture({ scenario, configPath, sandboxDir }) {
  const sourcePath = path.resolve(path.dirname(configPath), scenario.fixture.trim());

  try {
    await fs.access(sourcePath);
  } catch {
    throw new Error(`Configured fixture not found: ${sourcePath}`);
  }
  await fs.cp(sourcePath, sandboxDir, { recursive: true, force: true });
  logRunStepDetail(scenario.id, "fixture", `Overlaid fixture ${sourcePath} -> ${sandboxDir}`);
  return sourcePath;
}

// assertSandboxBinary guards that archetipo_post_commands run the CLI compiled
// for and copied into the sandbox, never a binary that happens to be on PATH.
function assertSandboxBinary({ cliBinaryPath, sandboxDir }) {
  const resolvedBinary = path.resolve(cliBinaryPath);
  const resolvedSandbox = path.resolve(sandboxDir);
  if (resolvedBinary !== resolvedSandbox && !resolvedBinary.startsWith(resolvedSandbox + path.sep)) {
    throw new Error(`Sandbox CLI binary ${resolvedBinary} is not inside the sandbox ${resolvedSandbox}`);
  }
}

// initSandboxGitRepo turns the sandbox into a git repository with a `main`
// branch carrying a single empty commit. The empty base commit avoids tracking
// the copied CLI binary while still giving `spec start` a base branch to fork
// the per-spec worktree from. Identity is set on the local repo config so the
// linked worktrees the agent commits in inherit it.
async function initSandboxGitRepo(context) {
  const steps = [
    ["init", "-b", "main"],
    ["config", "user.email", "archetipo-e2e@example.com"],
    ["config", "user.name", "ARchetipo E2E"],
    ["commit", "--allow-empty", "-m", "chore: e2e sandbox base"],
  ];
  for (let index = 0; index < steps.length; index += 1) {
    const args = steps[index];
    const run = await runReportedCommand({
      ...context,
      step: `git-init-${index + 1}`,
      command: "git",
      args,
    });
    if (!run.ok) {
      throw new Error(`Sandbox git ${args[0]} failed: ${run.stderr || run.stdout || `exit ${run.code}`}`);
    }
  }
}

async function captureIntegrationState(context, code, step) {
  const show = await runReportedCommand({
    ...context,
    step,
    command: context.cliBinaryPath,
    args: ["spec", "show", code],
  });
  if (!show.ok) {
    throw new Error(`spec show ${code} failed: ${show.stderr || show.stdout || `exit ${show.code}`}`);
  }
  let spec;
  try {
    spec = JSON.parse(show.stdout)?.data?.spec;
  } catch (err) {
    throw new Error(`could not parse spec show ${code} output: ${err.message}`);
  }
  const branch = spec?.branch || `archetipo/${code}`;
  const worktree = spec?.worktree || "";
  if (!worktree) {
    throw new Error(`spec show ${code} did not return data.spec.worktree before integrate`);
  }

  const tipProbe = await runProbe("git", ["rev-parse", "--verify", "--quiet", `${branch}^{commit}`], {
    cwd: context.sandboxDir,
  });
  if (!tipProbe.ok) {
    throw new Error(`expected branch ${branch} to exist before integrate, but could not resolve its tip`);
  }

  return {
    branch,
    branchTip: tipProbe.stdout.trim(),
    worktree,
    worktreeAbs: resolveSandboxPath(context.sandboxDir, worktree),
  };
}

// verifyIntegration confirms the round-trip closed: the spec reached DONE, the
// branch tip is reachable from main, and the per-spec branch/worktree are gone.
async function verifyIntegration(context, code, beforeIntegrate, step) {
  if (!beforeIntegrate?.branchTip) {
    throw new Error(`missing pre-integrate branch tip for ${code}`);
  }

  const show = await runReportedCommand({
    ...context,
    step,
    command: context.cliBinaryPath,
    args: ["spec", "show", code],
  });
  if (!show.ok) {
    throw new Error(`spec show ${code} failed: ${show.stderr || show.stdout || `exit ${show.code}`}`);
  }
  let spec;
  try {
    spec = JSON.parse(show.stdout)?.data?.spec;
  } catch (err) {
    throw new Error(`could not parse spec show ${code} output: ${err.message}`);
  }
  const status = spec?.status ?? "";
  if (status !== "DONE") {
    throw new Error(`expected ${code} to be DONE after integrate, got ${status || "(empty)"}`);
  }

  const ancestorProbe = await runProbe("git", ["merge-base", "--is-ancestor", beforeIntegrate.branchTip, "main"], {
    cwd: context.sandboxDir,
  });
  if (!ancestorProbe.ok) {
    throw new Error(`expected pre-integrate tip ${beforeIntegrate.branchTip} for ${code} to be reachable from main`);
  }

  const branch = spec?.branch || beforeIntegrate.branch || `archetipo/${code}`;
  const branchProbe = await runProbe("git", ["rev-parse", "--verify", "--quiet", `${branch}^{commit}`], {
    cwd: context.sandboxDir,
  });
  if (branchProbe.ok) {
    throw new Error(`expected branch ${branch} to be deleted after integrate, but it still exists`);
  }

  const worktreeAbs = resolveSandboxPath(context.sandboxDir, spec?.worktree || beforeIntegrate.worktree);
  try {
    await fs.access(worktreeAbs);
    throw new Error(`expected worktree directory ${worktreeAbs} to be removed after integrate`);
  } catch (err) {
    if (err?.code !== "ENOENT") {
      throw err;
    }
  }

  const worktreeList = await runProbe("git", ["worktree", "list", "--porcelain"], {
    cwd: context.sandboxDir,
  });
  if (!worktreeList.ok) {
    throw new Error(`git worktree list --porcelain failed: ${worktreeList.stderr || worktreeList.stdout || `exit ${worktreeList.code}`}`);
  }
  const listedWorktrees = worktreeList.stdout
    .split(/\r?\n/)
    .filter((line) => line.startsWith("worktree "))
    .map((line) => path.resolve(line.slice("worktree ".length)));
  if (listedWorktrees.includes(path.resolve(worktreeAbs))) {
    throw new Error(`expected git worktree list --porcelain to omit ${worktreeAbs}`);
  }
}

function resolveSandboxPath(sandboxDir, value) {
  return path.resolve(path.isAbsolute(value) ? value : path.join(sandboxDir, value));
}

async function copyCliBinaryToSandbox({ scenario, cliSourceBinaryPath, sandboxDir }) {
  const binName = path.basename(cliSourceBinaryPath);
  const targetDir = path.join(sandboxDir, "bin");
  const targetPath = path.join(targetDir, binName);

  await fs.mkdir(targetDir, { recursive: true });
  await fs.copyFile(cliSourceBinaryPath, targetPath);
  if (process.platform !== "win32") {
    await fs.chmod(targetPath, 0o755);
  }

  logRunStepDetail(scenario.id, "workspace", `Copied CLI ${cliSourceBinaryPath} -> ${targetPath}`);
  return targetPath;
}

async function createRunRoot(workspaceRoot) {
  const runsRoot = path.join(workspaceRoot, "runs");
  await fs.mkdir(runsRoot, { recursive: true });
  const baseName = formatRunTimestamp(new Date());
  for (let attempt = 0; attempt < 1000; attempt += 1) {
    const suffix = attempt === 0 ? "" : `-${attempt + 1}`;
    const runRoot = path.join(runsRoot, `${baseName}${suffix}`);
    try {
      await fs.mkdir(runRoot);
      return runRoot;
    } catch (error) {
      if (error?.code !== "EEXIST") {
        throw error;
      }
    }
  }
  throw new Error(`Unable to create a unique run directory under ${runsRoot}`);
}

function formatRunTimestamp(date) {
  const pad = (value) => String(value).padStart(2, "0");
  return [
    date.getFullYear(),
    "-",
    pad(date.getMonth() + 1),
    "-",
    pad(date.getDate()),
    "T",
    pad(date.getHours()),
    pad(date.getMinutes()),
    pad(date.getSeconds()),
  ].join("");
}

async function verifyRequiredEnv(agent) {
  const required = agent.env_required ?? [];
  const missing = required.filter((name) => !process.env[name]);
  if (missing.length > 0) {
    throw new SkipError(`Missing required environment variables: ${missing.join(", ")}`);
  }
}

async function installWorkspace(context) {
  // `init` needs a connector non-interactively (`--yes` doesn't cover the
  // connector prompt), so we pass a `file` default. It's only a baseline: a
  // fixture carrying its own `.archetipo/config.yaml` (e.g. the worktree
  // scenario) overwrites it, so the fixture stays authoritative.
  const install = await runReportedCommand({
    ...context,
    step: "install",
    command: context.cliBinaryPath,
    args: [
      "init",
      "--tool",
      context.agent.tool,
      "--connector",
      "file",
      "--yes",
    ],
  });
  if (!install.ok) {
    throw new Error(`archetipo init failed: ${install.stderr || install.stdout || `exit ${install.code}`}`);
  }
}

async function verifyInstallation(context) {
  const requiredPaths = [
    path.join(context.sandboxDir, ".archetipo", "config.yaml"),
    path.join(context.sandboxDir, ".archetipo", "shared-runtime.md"),
    ...deriveSkillNames(context.scenario.prompts).map((skillName) => context.skillRoot(skillName)),
  ];
  for (const requiredPath of requiredPaths) {
    try {
      await fs.access(requiredPath);
    } catch {
      throw new Error(`Expected installation artifact missing: ${requiredPath}`);
    }
  }
}

function deriveSkillNames(prompts) {
  return [...new Set(prompts.map(deriveSkillName).filter(Boolean).map((skill) => skill.replace(/^\/+/, "")))];
}

function deriveSkillName(prompt) {
  return String(prompt).trim().split(/\s+/)[0] ?? "";
}

function buildPromptInvocation(context, prompt) {
  return {
    kind: "prompt",
    skill: deriveSkillName(prompt),
    prompt,
    command: context.agent.command,
    args: context.agent.args.map((arg) => interpolateArg(arg, context, prompt)),
  };
}

function interpolateArg(arg, context, prompt) {
  return arg
    .replaceAll("{model}", context.agent.model ?? "")
    .replaceAll("{prompt}", prompt)
    .replaceAll("{sandboxDir}", context.sandboxDir);
}

async function runReportedCommand({
  sandboxDir,
  cliBinaryPath,
  cliEnv,
  report,
  step,
  command,
  args,
  timeoutMs,
  acceptedExitCodes = [0],
  kind,
  skill,
  prompt,
}) {
  const startedAt = Date.now();
  const stdoutChunks = [];
  const stderrChunks = [];
  const heartbeatLabel = `${step} (${path.basename(command)})`;
  const event = {
    step,
    kind: kind ?? inferCommandKind({ cliBinaryPath, command }),
    skill,
    prompt,
    command,
    args,
    cwd: sandboxDir,
    startedAt,
  };
  report.events.push(event);

  const binDir = cliBinaryPath ? path.dirname(cliBinaryPath) : null;
  const env = {
    ...process.env,
    ...(cliEnv ?? {}),
  };
  if (binDir) {
    env.PATH = `${binDir}${path.delimiter}${env.PATH || process.env.PATH}`;
  }
  const child = spawn(command, args, {
    cwd: sandboxDir,
    env,
    stdio: ["ignore", "pipe", "pipe"],
    shell: process.platform === "win32",
  });

  child.stdout.on("data", (chunk) => stdoutChunks.push(chunk));
  child.stderr.on("data", (chunk) => stderrChunks.push(chunk));

  let timedOut = false;
  const timeout = setTimeout(() => {
    timedOut = true;
    void terminateChildProcess(child);
  }, timeoutMs);
  const heartbeat = setInterval(() => {
    const elapsedSeconds = formatDurationMs(Date.now() - startedAt);
    console.log(`   ... ${heartbeatLabel} still running (${elapsedSeconds})`);
  }, LONG_RUNNING_STEP_HEARTBEAT_MS);

  const { code, spawnError } = await new Promise((resolve) => {
    child.on("error", (error) => resolve({ code: 1, spawnError: error }));
    child.on("close", (code) => resolve({ code, spawnError: null }));
  }).finally(() => {
    clearTimeout(timeout);
    clearInterval(heartbeat);
  });

  const stdout = Buffer.concat(stdoutChunks).toString("utf8");
  const stderr = [Buffer.concat(stderrChunks).toString("utf8"), spawnError?.message ?? ""]
    .filter(Boolean)
    .join("\n");
  const endedAt = Date.now();
  const accepted = acceptedExitCodes.includes(code);
  const duration = formatDurationMs(endedAt - startedAt);
  Object.assign(event, {
    endedAt,
    durationMs: endedAt - startedAt,
    exitCode: code,
    timedOut,
    accepted,
    ok: accepted && !timedOut,
    stdout,
    stderr,
  });
  if (accepted && !timedOut) {
    console.log(`   done ${step} in ${duration}`);
  } else if (timedOut) {
    console.log(`   timeout ${step} after ${duration}`);
  } else {
    console.log(`   failed ${step} after ${duration} (exit ${code})`);
  }

  return {
    ok: accepted && !timedOut,
    code,
    stdout,
    stderr,
    timedOut,
  };
}

function createRunReport({ scenario, configPath, runRoot, sandboxDir, fixtureSourcePath, reportPath }) {
  return {
    startedAt: Date.now(),
    scenario: scenario.id,
    agent: scenario.agent.id,
    model: scenario.agent.model,
    configPath,
    fixtureSourcePath,
    runRoot,
    sandboxDir,
    reportPath,
    events: [],
    result: null,
  };
}

function inferCommandKind({ cliBinaryPath, command }) {
  if (!cliBinaryPath) {
    return "command";
  }
  return path.resolve(command) === path.resolve(cliBinaryPath) ? "cli" : "command";
}

async function writeHtmlReport(context) {
  context.report.endedAt = Date.now();
  const html = renderHtmlReport(context.report);
  await fs.writeFile(context.reportPath, html);
  console.log(`   report ${context.reportPath}`);
}

async function writeSummary(result, summaryPath) {
  await fs.writeFile(summaryPath, JSON.stringify(result, null, 2));
}

function renderHtmlReport(report) {
  const result = report.result ?? {};
  const events = report.events ?? [];
  const promptEvents = events.filter((event) => event.kind === "prompt");
  const cliEvents = events.filter((event) => event.kind === "cli");
  const otherEvents = events.filter((event) => event.kind !== "prompt" && event.kind !== "cli");
  const skillNames = [...new Set(promptEvents.map((event) => event.skill).filter(Boolean))];
  const durationMs = (report.endedAt ?? Date.now()) - report.startedAt;

  return `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>ARchetipo E2E Report - ${escapeHtml(report.scenario)}</title>
  <style>
    :root { color-scheme: light; --bg: #f6f7f9; --panel: #ffffff; --ink: #172026; --muted: #61707d; --line: #d8dee6; --ok: #18794e; --fail: #c93a2f; --skip: #8a5a00; --prompt: #2457a7; --cli: #386a20; }
    * { box-sizing: border-box; }
    body { margin: 0; background: var(--bg); color: var(--ink); font: 14px/1.45 -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; }
    main { max-width: 1180px; margin: 0 auto; padding: 28px; }
    header { background: var(--panel); border: 1px solid var(--line); border-radius: 8px; padding: 20px; }
    h1 { margin: 0 0 14px; font-size: 22px; letter-spacing: 0; }
    h2 { margin: 28px 0 12px; font-size: 18px; letter-spacing: 0; }
    .meta { display: grid; grid-template-columns: repeat(auto-fit, minmax(190px, 1fr)); gap: 10px; }
    .meta div { border: 1px solid var(--line); border-radius: 6px; padding: 8px 10px; background: #fbfcfd; min-width: 0; }
    .label { display: block; color: var(--muted); font-size: 12px; }
    .value { overflow-wrap: anywhere; }
    .skills { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 14px; }
    .badge { display: inline-flex; align-items: center; gap: 6px; border-radius: 999px; padding: 3px 9px; font-size: 12px; font-weight: 650; border: 1px solid var(--line); background: #fff; color: var(--muted); }
    .badge.prompt { border-color: #b7c9ee; background: #eef4ff; color: var(--prompt); }
    .badge.cli { border-color: #c4deb9; background: #f0faeb; color: var(--cli); }
    .badge.command { background: #f4f5f6; color: #53606b; }
    .badge.pass { border-color: #b7dec9; background: #eefaf3; color: var(--ok); }
    .badge.fail { border-color: #f0bbb6; background: #fff0ef; color: var(--fail); }
    .badge.skip { border-color: #e8d4a6; background: #fff8e5; color: var(--skip); }
    .badge.timeout { border-color: #f0bbb6; background: #fff0ef; color: var(--fail); }
    .timeline { display: grid; gap: 14px; }
    .event { border: 1px solid var(--line); border-left-width: 5px; border-radius: 8px; background: var(--panel); padding: 16px; box-shadow: 0 1px 2px rgba(20, 31, 43, 0.04); }
    .event.prompt { border-left-color: var(--prompt); }
    .event.cli { border-left-color: var(--cli); }
    .event.command { border-left-color: #8b98a5; }
    .event-head { display: flex; gap: 10px; align-items: flex-start; justify-content: space-between; flex-wrap: wrap; }
    .event-title { display: flex; flex-wrap: wrap; gap: 8px; align-items: center; }
    .step { font-size: 16px; font-weight: 750; }
    .time { color: var(--muted); font-variant-numeric: tabular-nums; }
    .command-line, pre { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, "Liberation Mono", monospace; }
    .command-line { margin-top: 10px; padding: 9px 10px; border-radius: 6px; background: #f2f4f7; overflow-x: auto; white-space: pre-wrap; overflow-wrap: anywhere; }
    .prompt-text { margin-top: 12px; border: 1px solid #b7c9ee; background: #f8fbff; border-radius: 6px; padding: 10px; }
    .prompt-text strong { display: block; margin-bottom: 4px; color: var(--prompt); }
    details { margin-top: 10px; border: 1px solid var(--line); border-radius: 6px; background: #fbfcfd; }
    summary { cursor: pointer; padding: 8px 10px; color: var(--muted); font-weight: 650; }
    pre { margin: 0; padding: 10px; overflow-x: auto; white-space: pre-wrap; overflow-wrap: anywhere; max-height: 520px; }
    .empty { color: var(--muted); padding: 12px; border: 1px dashed var(--line); border-radius: 8px; background: #fff; }
    .index { display: grid; gap: 8px; }
    .index-row { display: grid; grid-template-columns: 130px 1fr auto; gap: 10px; align-items: start; border: 1px solid var(--line); border-radius: 6px; background: var(--panel); padding: 9px 10px; }
    @media (max-width: 720px) { main { padding: 16px; } .index-row { grid-template-columns: 1fr; } }
  </style>
</head>
<body>
  <main>
    <header>
      <h1>ARchetipo E2E Report</h1>
      <div class="meta">
        ${renderMeta("Scenario", report.scenario)}
        ${renderMeta("Agent", report.agent)}
        ${renderMeta("Model", report.model)}
        ${renderMeta("Status", result.status ?? "unknown")}
        ${renderMeta("Duration", formatDurationMs(durationMs))}
        ${renderMeta("Sandbox", report.sandboxDir)}
        ${renderMeta("Config", report.configPath)}
        ${renderMeta("Fixture", report.fixtureSourcePath)}
      </div>
      <div class="skills">
        <span class="badge ${escapeHtml(result.status ?? "skip")}">status ${escapeHtml(result.status ?? "unknown")}</span>
        ${skillNames.map((skill) => `<span class="badge prompt">skill ${escapeHtml(skill)}</span>`).join("")}
      </div>
    </header>

    <h2>Timeline</h2>
    <section class="timeline">
      ${events.length > 0 ? events.map((event, index) => renderEvent(event, index, report.startedAt)).join("") : `<div class="empty">No commands were recorded.</div>`}
    </section>

    <h2>CLI Invocations</h2>
    <section class="index">
      ${cliEvents.length > 0 ? cliEvents.map((event) => renderIndexRow(event, report.startedAt)).join("") : `<div class="empty">No CLI invocations were recorded.</div>`}
    </section>

    <h2>Other Commands</h2>
    <section class="index">
      ${otherEvents.length > 0 ? otherEvents.map((event) => renderIndexRow(event, report.startedAt)).join("") : `<div class="empty">No other commands were recorded.</div>`}
    </section>
  </main>
</body>
</html>`;
}

function renderMeta(label, value) {
  return `<div><span class="label">${escapeHtml(label)}</span><span class="value">${escapeHtml(value ?? "")}</span></div>`;
}

function renderEvent(event, index, runStartedAt) {
  const status = event.timedOut ? "timeout" : event.ok ? "pass" : event.endedAt ? "fail" : "running";
  return `<article class="event ${escapeHtml(event.kind ?? "command")}" id="event-${index + 1}">
    <div class="event-head">
      <div class="event-title">
        <span class="step">${escapeHtml(event.step)}</span>
        <span class="badge ${escapeHtml(event.kind ?? "command")}">${escapeHtml(event.kind ?? "command")}</span>
        ${event.skill ? `<span class="badge prompt">skill ${escapeHtml(event.skill)}</span>` : ""}
        <span class="badge ${escapeHtml(status)}">${escapeHtml(status)}</span>
      </div>
      <div class="time">+${formatDurationMs(event.startedAt - runStartedAt)} · ${formatDurationMs(event.durationMs ?? 0)}</div>
    </div>
    <div class="command-line">$ ${escapeHtml(formatCommand(event.command, event.args ?? []))}</div>
    ${event.prompt ? `<div class="prompt-text"><strong>Prompt passed to backend</strong><pre>${escapeHtml(event.prompt)}</pre></div>` : ""}
    ${renderOutputDetails("STDOUT", event.stdout)}
    ${renderOutputDetails("STDERR", event.stderr)}
    <details><summary>Execution metadata</summary><pre>${escapeHtml(JSON.stringify({
      exitCode: event.exitCode,
      timedOut: event.timedOut,
      durationMs: event.durationMs,
      cwd: event.cwd,
    }, null, 2))}</pre></details>
  </article>`;
}

function renderIndexRow(event, runStartedAt) {
  const status = event.timedOut ? "timeout" : event.ok ? "pass" : event.endedAt ? "fail" : "running";
  return `<div class="index-row">
    <span class="time">+${formatDurationMs(event.startedAt - runStartedAt)}</span>
    <span class="command-line">$ ${escapeHtml(formatCommand(event.command, event.args ?? []))}</span>
    <span class="badge ${escapeHtml(status)}">${escapeHtml(status)}</span>
  </div>`;
}

function renderOutputDetails(label, value) {
  if (!value) {
    return "";
  }
  return `<details><summary>${escapeHtml(label)}</summary><pre>${escapeHtml(value)}</pre></details>`;
}

function formatCommand(command, args) {
  return [command, ...args].map(shellDisplayValue).join(" ");
}

function shellDisplayValue(value) {
  const text = String(value);
  return /[\s"'$`\\]/.test(text) ? JSON.stringify(text) : text;
}

function escapeHtml(value) {
  return String(value)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

async function terminateChildProcess(child) {
  if (!child?.pid) {
    return;
  }

  if (process.platform === "win32") {
    await runProbe("taskkill", ["/PID", String(child.pid), "/T", "/F"]);
    return;
  }

  try {
    child.kill("SIGTERM");
  } catch {}

  await new Promise((resolve) => setTimeout(resolve, PROCESS_TERMINATION_GRACE_MS));

  try {
    child.kill("SIGKILL");
  } catch {}
}

async function ensureCommand(command) {
  if (process.platform === "win32") {
    const result = await runProbe("where", [command]);
    if (!result.ok) {
      return { skip: true, reason: `Command '${command}' is not available.` };
    }
    return { skip: false };
  }

  const result = await runProbe("bash", ["-lc", `command -v ${shellEscape(command)}`]);
  if (!result.ok) {
    return { skip: true, reason: `Command '${command}' is not available.` };
  }
  return { skip: false };
}

async function runProbe(command, args, options = {}) {
  return new Promise((resolve) => {
    const child = spawn(command, args, {
      env: process.env,
      stdio: ["ignore", "pipe", "pipe"],
      cwd: options.cwd,
    });
    const stdout = [];
    const stderr = [];
    child.stdout.on("data", (chunk) => stdout.push(chunk));
    child.stderr.on("data", (chunk) => stderr.push(chunk));
    child.on("close", (code) => {
      resolve({
        ok: code === 0,
        code,
        stdout: Buffer.concat(stdout).toString("utf8"),
        stderr: Buffer.concat(stderr).toString("utf8"),
      });
    });
    child.on("error", () => {
      resolve({ ok: false, code: 1, stdout: "", stderr: "" });
    });
  });
}

function shellEscape(value) {
  return `'${String(value).replace(/'/g, `'\\''`)}'`;
}

function classifyRunFailure(context, step, commandResult) {
  const combined = `${commandResult.stdout}\n${commandResult.stderr}`;
  const authPattern = /(api key|oauth|unauthori[sz]ed|forbidden|not logged in|login|token|credential|authentication required)/i;
  if (authPattern.test(combined)) {
    return {
      status: "skip",
      reason: `${step} skipped because ${context.scenario.id} is not authenticated or lacks credentials.`,
      sandboxDir: context.sandboxDir,
    };
  }

  if (commandResult.timedOut) {
    return {
      status: "fail",
      reason: `${step} timed out after ${context.timeoutMs}ms. See ${context.reportPath}`,
      sandboxDir: context.sandboxDir,
    };
  }

  return {
    status: "fail",
    reason: `${step} failed with exit code ${commandResult.code}. See ${context.reportPath}`,
    sandboxDir: context.sandboxDir,
    reportPath: context.reportPath,
  };
}

function finalizeResult(context, result) {
  if (result instanceof SkipError) {
    return {
      scenario: context.scenario.id,
      agent: context.agent.id,
      model: context.agent.model,
      status: "skip",
      reason: result.message,
      runRoot: context.runRoot,
      sandboxDir: context.sandboxDir,
      reportPath: context.reportPath,
      summaryPath: context.summaryPath,
    };
  }

  return {
    scenario: context.scenario.id,
    agent: context.agent.id,
    model: context.agent.model,
    status: result.status,
    reason: result.reason,
    runRoot: context.runRoot,
    sandboxDir: result.sandboxDir ?? context.sandboxDir,
    reportPath: context.reportPath,
    summaryPath: context.summaryPath,
  };
}

function formatResultLine(result) {
  const scenarioLabel = result.scenario ?? "?";
  const agentLabel = result.agent ?? "?";
  const base = `${scenarioLabel} (agent: ${agentLabel}, model: ${result.model ?? "no model"}) -> ${result.status.toUpperCase()}`;
  if (result.reason) {
    return `${base} - ${result.reason}`;
  }
  return base;
}

function logRunStepStart(runIdOrScenario, step, message) {
  console.log(` -> [${runIdOrScenario}] ${step}: ${message}`);
}

function logRunStepDone(runIdOrScenario, step, message) {
  console.log(` <- [${runIdOrScenario}] ${step}: ${message}`);
}

function logRunStepDetail(runIdOrScenario, step, message) {
  console.log(`    [${runIdOrScenario}] ${step}: ${message}`);
}

function formatDurationMs(durationMs) {
  const totalSeconds = Math.max(1, Math.round(durationMs / 1000));
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds % 60;
  if (minutes === 0) {
    return `${seconds}s`;
  }
  return `${minutes}m ${seconds}s`;
}

class SkipError extends Error {}

main().catch((error) => {
  const status = error instanceof SkipError ? "SKIPPED" : "FAILED";
  console.error(`${status}: ${error.message}`);
  process.exit(1);
});
