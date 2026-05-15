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
const DEFAULT_CONNECTOR = "file";
const DEFAULT_GITHUB_REPO_PREFIX = "archetipo-e2e";
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

  const results = [];
  for (const scenario of scenarios) {
    const agentLabel = `${scenario.agent.id}`;
    console.log(`\n==> Running scenario "${scenario.id}" (agent: ${agentLabel}, model: ${scenario.agent.model ?? "no model"}) [connector=${options.connector}]`);
    const result = await runConfiguredScenario({
      scenario,
      connector: options.connector,
      configPath,
      timeoutMs: options.timeoutMs ?? DEFAULT_TIMEOUT_MS,
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
    connector: DEFAULT_CONNECTOR,
  };

  for (let index = 0; index < argv.length; index += 1) {
    const arg = argv[index];
    switch (arg) {
      case "--config":
        options.config = argv[++index];
        break;
      case "--connector":
        options.connector = argv[++index];
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

  if (!["file", "github"].includes(options.connector)) {
    throw new Error(`Unknown connector: ${options.connector}`);
  }

  return options;
}

function printHelp() {
  console.log(`ARchetipo E2E runner

Usage:
  node ./test/e2e/run.mjs --config test/e2e/run.yaml --connector file
  node ./test/e2e/run.mjs --config test/e2e/run.yaml --connector github --scenario spec-plan
  npm run test:e2e:file -- [--config test/e2e/run.yaml] [--scenario scenario-name]
  npm run test:e2e:github -- [--config test/e2e/run.yaml] [--scenario scenario-name]
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
    if (rawScenario.prd !== undefined && (typeof rawScenario.prd !== "string" || rawScenario.prd.trim() === "")) {
      throw new Error(`scenarios.${scenarioId}.prd must be a non-empty string when specified in ${configPath}`);
    }
    scenarios.push({
      id: scenarioId,
      agentId,
      agent: { id: agentId, ...agent },
      prd: rawScenario.prd,
      prompts: rawScenario.prompts,
      env_required: rawScenario.env_required ?? agent.env_required,
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

async function runConfiguredScenario({ scenario, connector, configPath, timeoutMs }) {
  const agent = scenario.agent;
  const toolSkillRoot = TOOL_SKILL_ROOT[agent.tool];
  if (!toolSkillRoot) {
    return {
      scenario: scenario.id,
      agent: agent.id,
      connector,
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
  const prdSourcePath = await copyConfiguredPrd({ scenario, configPath, sandboxDir });
  logRunStepDone(scenario.id, "workspace", `Sandbox ready in ${sandboxDir}`);

  const report = createRunReport({
    scenario,
    connector,
    configPath,
    runRoot,
    sandboxDir,
    prdSourcePath,
    reportPath,
  });
  const context = {
    scenario,
    agent,
    connector,
    configPath,
    runRoot,
    sandboxDir,
    report,
    reportPath,
    summaryPath,
    timeoutMs,
    toolSkillRoot,
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

    logRunStepStart(scenario.id, "bootstrap", `Preparing ${connector} workspace`);
    await prepareWorkspace(context);
    logRunStepDone(scenario.id, "bootstrap", `${connector} workspace ready`);

    logRunStepStart(scenario.id, "install", `Installing ARchetipo assets for tool '${agent.tool}'`);
    await installWorkspace(context);
    logRunStepDone(scenario.id, "install", "Installation completed");

    logRunStepStart(scenario.id, "verify-install", "Checking installed files and connector config");
    await verifyInstallation(context);
    logRunStepDone(scenario.id, "verify-install", "Installed files verified");

    logRunStepStart(scenario.id, "init", "Reading project metadata from local CLI");
    await readCliEnvelope(context, "init", ["init"]);
    logRunStepDone(scenario.id, "init", "CLI init completed");

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

async function copyConfiguredPrd({ scenario, configPath, sandboxDir }) {
  if (!scenario.prd) {
    return null;
  }

  const sourcePath = path.resolve(path.dirname(configPath), scenario.prd.trim());
  const targetDir = path.join(sandboxDir, "docs");
  const targetPath = path.join(targetDir, "PRD.md");

  await fs.mkdir(targetDir, { recursive: true });
  try {
    await fs.copyFile(sourcePath, targetPath);
  } catch (error) {
    if (error?.code === "ENOENT") {
      throw new Error(`Configured PRD not found: ${sourcePath}`);
    }
    throw error;
  }

  logRunStepDetail(scenario.id, "workspace", `Copied PRD ${sourcePath} -> ${targetPath}`);
  return sourcePath;
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

async function prepareWorkspace(context) {
  if (context.connector !== "github") {
    return;
  }

  logRunStepDetail(context.scenario.id, "bootstrap", "Checking GitHub prerequisites");
  await verifyGitHubPrerequisites(context);
  logRunStepDetail(context.scenario.id, "bootstrap", "Provisioning temporary GitHub repository");
  await provisionGitHubRepository(context);
  logRunStepDetail(context.scenario.id, "bootstrap", "Initializing git sandbox");
  await bootstrapSandboxGit(context);
}

async function installWorkspace(context) {
  const invocation = getInstallerInvocation(context);
  const install = await runReportedCommand({
    ...context,
    step: "install",
    command: invocation.command,
    args: invocation.args,
  });
  if (!install.ok) {
    throw new Error(`Installer failed: ${install.stderr || install.stdout || `exit ${install.code}`}`);
  }
}

function getInstallerInvocation(context) {
  if (process.platform === "win32") {
    const installScript = path.join(repoRoot, "install.ps1");
    return {
      command: "powershell",
      args: [
        "-NoProfile",
        "-ExecutionPolicy",
        "Bypass",
        "-File",
        installScript,
        "-Local",
        "-Tool",
        context.agent.tool,
        "-Connector",
        context.connector,
        "-Yes",
      ],
    };
  }

  const installScript = path.join(repoRoot, "install.sh");
  return {
    command: "bash",
    args: [
      installScript,
      "--local",
      "--tool",
      context.agent.tool,
      "--connector",
      context.connector,
      "--yes",
    ],
  };
}

async function verifyInstallation(context) {
  const requiredPaths = [
    getCliBinaryPath(context.sandboxDir),
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

  const configText = await fs.readFile(path.join(context.sandboxDir, ".archetipo", "config.yaml"), "utf8");
  const connectorPattern = new RegExp(`^connector:\\s*${context.connector}\\b`, "m");
  if (!connectorPattern.test(configText)) {
    throw new Error(`Installed config.yaml does not use connector: ${context.connector}.`);
  }
}

function getCliBinaryPath(sandboxDir) {
  const executable = process.platform === "win32" ? "archetipo.exe" : "archetipo";
  return path.join(sandboxDir, ".archetipo", "bin", executable);
}

function deriveSkillNames(prompts) {
  return [...new Set(prompts.map(deriveSkillName).filter(Boolean).map((skill) => skill.replace(/^\/+/, "")))];
}

function deriveSkillName(prompt) {
  return String(prompt).trim().split(/\s+/)[0] ?? "";
}

async function readCliEnvelope(context, step, cliArgs) {
  const archetipoPath = getCliBinaryPath(context.sandboxDir);
  const result = await runReportedCommand({
    ...context,
    step,
    command: archetipoPath,
    args: cliArgs,
  });
  if (!result.ok) {
    throw new Error(`CLI command failed (${cliArgs.join(" ")}): ${result.stderr || result.stdout}`);
  }

  try {
    return JSON.parse(result.stdout);
  } catch (error) {
    throw new Error(`Invalid JSON from archetipo ${cliArgs.join(" ")}: ${error.message}`);
  }
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
    kind: kind ?? inferCommandKind({ sandboxDir, command }),
    skill,
    prompt,
    command,
    args,
    cwd: sandboxDir,
    startedAt,
  };
  report.events.push(event);

  const child = spawn(command, args, {
    cwd: sandboxDir,
    env: process.env,
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

function createRunReport({ scenario, connector, configPath, runRoot, sandboxDir, prdSourcePath, reportPath }) {
  return {
    startedAt: Date.now(),
    scenario: scenario.id,
    agent: scenario.agent.id,
    connector,
    githubRepo: null,
    model: scenario.agent.model,
    configPath,
    prdSourcePath,
    runRoot,
    sandboxDir,
    reportPath,
    events: [],
    result: null,
  };
}

function inferCommandKind({ sandboxDir, command }) {
  return path.resolve(command) === path.resolve(getCliBinaryPath(sandboxDir)) ? "cli" : "command";
}

async function writeHtmlReport(context) {
  context.report.githubRepo = context.githubRepo ?? null;
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
        ${renderMeta("Connector", report.connector)}
        ${renderMeta("Model", report.model)}
        ${renderMeta("Status", result.status ?? "unknown")}
        ${renderMeta("Duration", formatDurationMs(durationMs))}
        ${renderMeta("Sandbox", report.sandboxDir)}
        ${renderMeta("Config", report.configPath)}
        ${renderMeta("PRD Source", report.prdSourcePath)}
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

async function verifyGitHubPrerequisites(context) {
  const ghCommand = await ensureCommand("gh");
  if (ghCommand.skip) {
    throw new SkipError("GitHub connector requires the 'gh' CLI to be installed.");
  }

  const gitCommand = await ensureCommand("git");
  if (gitCommand.skip) {
    throw new SkipError("GitHub connector requires the 'git' CLI to be installed.");
  }

  const authStatus = await runProbe("gh", ["auth", "status"]);
  if (!authStatus.ok) {
    throw new SkipError("GitHub connector requires an authenticated 'gh' session.");
  }
}

async function provisionGitHubRepository(context) {
  const owner = await getGitHubViewerLogin();
  if (!owner) {
    throw new SkipError("GitHub connector requires a resolvable GitHub owner for the test repository.");
  }

  const repoName = buildDefaultGitHubRepoName(context);
  const repoSlug = `${owner}/${repoName}`;
  const repoUrl = `https://github.com/${repoSlug}.git`;
  const projectTitle = `${repoName} Backlog`;

  const existingRepo = await runProbe("gh", ["repo", "view", repoSlug, "--json", "nameWithOwner"]);
  if (existingRepo.ok) {
    const deleteRepo = await runProbe("gh", ["repo", "delete", repoSlug, "--yes"]);
    if (!deleteRepo.ok) {
      throw new Error(`Failed to delete existing GitHub repo ${repoSlug}: ${deleteRepo.stderr || deleteRepo.stdout || `exit ${deleteRepo.code}`}`);
    }
  }

  await deleteProjectByTitle(owner, projectTitle);

  const createRepo = await runProbe("gh", [
    "repo",
    "create",
    repoSlug,
    "--private",
    "--disable-wiki",
    "--description",
    `ARchetipo E2E sandbox for ${context.scenario.id}`,
  ]);
  if (!createRepo.ok) {
    throw new Error(`Failed to create GitHub repo ${repoSlug}: ${createRepo.stderr || createRepo.stdout || `exit ${createRepo.code}`}`);
  }

  context.githubRepo = {
    owner,
    projectTitle,
    repoName,
    repoSlug,
    repoUrl,
  };
}

async function bootstrapSandboxGit(context) {
  const remoteUrl = context.githubRepo?.repoUrl;
  if (!remoteUrl) {
    throw new Error("GitHub sandbox bootstrap is missing the provisioned remote repository URL.");
  }

  const gitInit = await runReportedCommand({
    ...context,
    step: "git-init",
    command: "git",
    args: ["init", "-b", "main"],
  });
  if (!gitInit.ok) {
    throw new Error(`Sandbox git init failed: ${gitInit.stderr || gitInit.stdout || `exit ${gitInit.code}`}`);
  }

  const gitRemote = await runReportedCommand({
    ...context,
    step: "git-remote",
    command: "git",
    args: ["remote", "add", "origin", remoteUrl],
  });
  if (!gitRemote.ok) {
    throw new Error(`Sandbox git remote setup failed: ${gitRemote.stderr || gitRemote.stdout || `exit ${gitRemote.code}`}`);
  }
}

async function getGitHubViewerLogin() {
  const viewer = await runProbe("gh", ["api", "user"]);
  if (!viewer.ok) {
    return "";
  }
  try {
    const parsed = JSON.parse(viewer.stdout);
    return parsed.login ?? "";
  } catch {
    return "";
  }
}

async function deleteProjectByTitle(owner, projectTitle) {
  const list = await runProbe("gh", [
    "project",
    "list",
    "--owner",
    owner,
    "--format",
    "json",
    "--limit",
    "100",
  ]);
  if (!list.ok) {
    throw new Error(`Failed to list GitHub projects for ${owner}: ${list.stderr || list.stdout || `exit ${list.code}`}`);
  }

  let projects = [];
  try {
    projects = JSON.parse(list.stdout).projects ?? [];
  } catch (error) {
    throw new Error(`Failed to parse GitHub project list JSON: ${error.message}`);
  }

  for (const project of projects) {
    if (project.title !== projectTitle) {
      continue;
    }
    const remove = await runProbe("gh", ["project", "delete", String(project.number), "--owner", owner]);
    if (!remove.ok) {
      throw new Error(`Failed to delete existing GitHub project '${projectTitle}': ${remove.stderr || remove.stdout || `exit ${remove.code}`}`);
    }
  }
}

function buildDefaultGitHubRepoName(context) {
  return sanitizeGitHubName([
    DEFAULT_GITHUB_REPO_PREFIX,
    context.scenario.id,
  ].join("-"));
}

function sanitizeGitHubName(value) {
  return value
    .toLowerCase()
    .replace(/[^a-z0-9-]+/g, "-")
    .replace(/-+/g, "-")
    .replace(/^-|-$/g, "");
}

async function runProbe(command, args) {
  return new Promise((resolve) => {
    const child = spawn(command, args, {
      env: process.env,
      stdio: ["ignore", "pipe", "pipe"],
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
      connector: context.connector,
      githubRepo: context.githubRepo,
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
    connector: context.connector,
    githubRepo: context.githubRepo,
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
  const base = `${scenarioLabel} (agent: ${agentLabel}, model: ${result.model ?? "no model"}) [connector=${result.connector ?? DEFAULT_CONNECTOR}] -> ${result.status.toUpperCase()}`;
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
