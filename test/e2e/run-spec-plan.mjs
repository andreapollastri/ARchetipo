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

const DEFAULT_MANIFEST = path.join(repoRoot, "test", "matrix.yaml");
const DEFAULT_SCENARIO = "spec-plan-file";
const DEFAULT_TIMEOUT_MS = 20 * 60 * 1000;
const DEFAULT_CONNECTOR = "file";
const DEFAULT_GITHUB_REPO_PREFIX = "archetipo-e2e";
const LONG_RUNNING_STEP_HEARTBEAT_MS = 30 * 1000;
const PROCESS_TERMINATION_GRACE_MS = 5 * 1000;

const TOOL_SKILL_ROOT = {
  claude: ".claude/skills",
  codex: ".agents/skills",
  gemini: ".gemini/skills",
  opencode: ".opencode/skills",
  copilot: ".github/skills",
  pi: ".pi/skills",
};

const adapters = {
  "codex-cli": {
    async prepare({ backend }) {
      return ensureCommand(backend.command);
    },
    buildSpecInvocation(ctx) {
      const prompt = buildSpecPrompt();
      return {
        command: ctx.backend.command,
        args: [
          "exec",
          "--skip-git-repo-check",
          "--dangerously-bypass-approvals-and-sandbox",
          "--cd",
          ctx.sandboxDir,
          "--model",
          ctx.backend.model,
          "--output-last-message",
          ctx.messagePath("spec"),
          prompt,
        ],
      };
    },
    buildPlanInvocation(ctx, storyCode) {
      const prompt = buildPlanPrompt(storyCode);
      return {
        command: ctx.backend.command,
        args: [
          "exec",
          "--skip-git-repo-check",
          "--dangerously-bypass-approvals-and-sandbox",
          "--cd",
          ctx.sandboxDir,
          "--model",
          ctx.backend.model,
          "--output-last-message",
          ctx.messagePath("plan"),
          prompt,
        ],
      };
    },
  },
  "claude-cli": {
    async prepare({ backend }) {
      return ensureCommand(backend.command);
    },
    buildSpecInvocation(ctx) {
      const prompt = buildSpecPrompt();
      return {
        command: ctx.backend.command,
        args: [
          "-p",
          "--dangerously-skip-permissions",
          "--output-format",
          "text",
          "--model",
          ctx.backend.model,
          prompt,
        ],
      };
    },
    buildPlanInvocation(ctx, storyCode) {
      const prompt = buildPlanPrompt(storyCode);
      return {
        command: ctx.backend.command,
        args: [
          "-p",
          "--dangerously-skip-permissions",
          "--output-format",
          "text",
          "--model",
          ctx.backend.model,
          prompt,
        ],
      };
    },
  },
  "pi-cli": {
    async prepare({ backend }) {
      return ensureCommand(backend.command);
    },
    buildSpecInvocation(ctx) {
      const prompt = buildSpecPrompt();
      const args = [
        "--print",
        "--no-session",
        "--no-lens",
        "--no-lsp",
        "--model",
        ctx.backend.model,
      ];
      if (ctx.backend.thinking) {
        args.push("--thinking", ctx.backend.thinking);
      }
      args.push(prompt);
      return { command: ctx.backend.command, args };
    },
    buildPlanInvocation(ctx, storyCode) {
      const prompt = buildPlanPrompt(storyCode);
      const args = [
        "--print",
        "--no-session",
        "--no-lens",
        "--no-lsp",
        "--model",
        ctx.backend.model,
      ];
      if (ctx.backend.thinking) {
        args.push("--thinking", ctx.backend.thinking);
      }
      args.push(prompt);
      return { command: ctx.backend.command, args };
    },
  },
};

async function main() {
  const options = parseArgs(process.argv.slice(2));
  const manifestPath = path.resolve(repoRoot, options.manifest ?? DEFAULT_MANIFEST);
  const manifest = YAML.parse(await fs.readFile(manifestPath, "utf8"));
  const scenario = manifest?.scenarios?.[options.scenario];
  if (!scenario) {
    throw new Error(`Scenario '${options.scenario}' not found in ${manifestPath}`);
  }

  const backends = selectBackends(manifest.backends ?? [], options.backend, options.scenario);
  if (backends.length === 0) {
    throw new Error(`No backends selected for scenario '${options.scenario}'.`);
  }

  const results = [];
  for (const backend of backends) {
    console.log(`\n==> Running ${backend.id} (${backend.model}) [connector=${options.connector}]`);
    const result = await runBackendScenario({
      backend,
      connector: options.connector,
      manifestPath,
      scenarioName: options.scenario,
      scenario,
      timeoutMs: options.timeoutMs ?? DEFAULT_TIMEOUT_MS,
    });
    results.push(result);
    console.log(formatResultLine(result));
  }

  await writeSummary(results, options.scenario);
  printSummary(results);

  const hasFailure = results.some((result) => result.status === "fail");
  const hasPass = results.some((result) => result.status === "pass");
  process.exit(hasFailure || !hasPass ? 1 : 0);
}

function parseArgs(argv) {
  const options = {
    connector: DEFAULT_CONNECTOR,
    manifest: DEFAULT_MANIFEST,
    scenario: DEFAULT_SCENARIO,
  };

  for (let index = 0; index < argv.length; index += 1) {
    const arg = argv[index];
    switch (arg) {
      case "--manifest":
        options.manifest = argv[++index];
        break;
      case "--backend":
        options.backend = argv[++index];
        break;
      case "--scenario":
        options.scenario = argv[++index];
        break;
      case "--connector":
        options.connector = argv[++index];
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
  npm run test:e2e:file -- [--manifest test/matrix.yaml] [--backend codex-cli] [--scenario spec-plan-file]
  npm run test:e2e:github -- [--manifest test/matrix.yaml] [--backend codex-cli] [--scenario spec-plan-file]
  node ./test/e2e/run-spec-plan.mjs --connector github [--backend codex-cli]
`);
}

function selectBackends(backends, requestedBackend, scenarioName) {
  const requested = requestedBackend
    ? new Set(requestedBackend.split(",").map((value) => value.trim()).filter(Boolean))
    : null;

  return backends.filter((backend) => {
    const supportsScenario = Array.isArray(backend.scenarios)
      ? backend.scenarios.includes(scenarioName)
      : true;
    if (!supportsScenario) {
      return false;
    }
    if (!requested) {
      return true;
    }
    return requested.has(backend.id) || requested.has(backend.type);
  });
}

async function runBackendScenario({ backend, connector, scenarioName, scenario, timeoutMs }) {
  const adapter = adapters[backend.type];
  if (!adapter) {
    return {
      backend: backend.id,
      connector,
      model: backend.model,
      status: "skip",
      reason: `Unsupported backend type '${backend.type}'`,
    };
  }

  const toolKey = backend.tool;
  const toolSkillRoot = TOOL_SKILL_ROOT[toolKey];
  if (!toolSkillRoot) {
    return {
      backend: backend.id,
      connector,
      model: backend.model,
      status: "skip",
      reason: `Unsupported installer tool '${toolKey}'`,
    };
  }

  const workspaceRoot = path.join(repoRoot, "test", "workspaces", scenarioName, connector, backend.id);
  logBackendStepStart(backend.id, "workspace", `Resetting sandbox at ${workspaceRoot}`);
  const workspaceReset = await resetWorkspace(workspaceRoot);
  const sandboxDir = path.join(workspaceRoot, workspaceReset.sandboxName);
  const artifactsDir = path.join(workspaceRoot, "artifacts");

  await fs.mkdir(path.join(sandboxDir, "docs"), { recursive: true });
  await fs.mkdir(artifactsDir, { recursive: true });

  const prdSource = path.resolve(repoRoot, scenario.prd_source);
  await fs.copyFile(prdSource, path.join(sandboxDir, "docs", "PRD.md"));
  logBackendStepDone(backend.id, "workspace", `Sandbox ready in ${sandboxDir}`);

  const context = {
    backend,
    connector,
    scenarioName,
    sandboxDir,
    artifactsDir,
    timeoutMs,
    toolSkillRoot,
    messagePath(step) {
      return path.join(artifactsDir, `${step}-last-message.txt`);
    },
    skillRoot(skillName) {
      return path.join(sandboxDir, toolSkillRoot, skillName);
    },
  };

  try {
    logBackendStepStart(backend.id, "prepare", `Checking local command '${backend.command}'`);
    const prep = await adapter.prepare(context);
    if (prep?.skip) {
      return finalizeResult(context, {
        status: "skip",
        reason: prep.reason,
      });
    }
    logBackendStepDone(backend.id, "prepare", `Command '${backend.command}' is available`);

    logBackendStepStart(backend.id, "env", "Validating required environment");
    await verifyRequiredEnv(backend);
    logBackendStepDone(backend.id, "env", "Environment looks good");

    logBackendStepStart(backend.id, "bootstrap", `Preparing ${connector} workspace`);
    await prepareWorkspace(context);
    logBackendStepDone(backend.id, "bootstrap", `${connector} workspace ready`);

    logBackendStepStart(backend.id, "install", `Installing ARchetipo assets for tool '${backend.tool}'`);
    await installWorkspace(context);
    logBackendStepDone(backend.id, "install", "Installation completed");

    logBackendStepStart(backend.id, "verify-install", "Checking installed files and connector config");
    await verifyInstallation(context);
    logBackendStepDone(backend.id, "verify-install", "Installed files verified");

    logBackendStepStart(backend.id, "init", "Reading project metadata from local CLI");
    const init = await readCliEnvelope(context, "init", ["init"]);
    logBackendStepDone(backend.id, "init", "CLI init completed");

    logBackendStepStart(backend.id, "preflight", "Checking backlog is empty before spec");
    const before = await readOptionalBacklog(context);
    if ((before?.data?.summary?.codes ?? []).length !== 0) {
      throw new Error("Expected an empty backlog before running archetipo-spec.");
    }
    logBackendStepDone(backend.id, "preflight", "Backlog is empty");

    const specInvocation = adapter.buildSpecInvocation(context);
    logBackendStepStart(backend.id, "spec", "Running archetipo-spec against docs/PRD.md");
    const specRun = await runLoggedCommand({
      ...context,
      step: "spec",
      ...specInvocation,
    });
    if (!specRun.ok) {
      return finalizeResult(context, classifyBackendFailure(context, "spec", specRun));
    }
    logBackendStepDone(backend.id, "spec", "Spec generation completed");

    logBackendStepStart(backend.id, "verify-spec", "Reading generated backlog");
    const afterSpec = await readCliEnvelope(context, "backlog-after-spec", ["backlog", "show"]);
    const backlogItems = afterSpec.data?.items ?? [];
    if (backlogItems.length === 0) {
      throw new Error("archetipo-spec completed without generating backlog items.");
    }
    await verifySpecSideEffects(context);
    logBackendStepDone(backend.id, "verify-spec", `Backlog generated with ${backlogItems.length} stories`);

    logBackendStepStart(backend.id, "select-story", "Selecting the first TODO story to plan");
    const firstTodoEnvelope = await readCliEnvelope(context, "first-todo", ["story", "show", "--status", "TODO"]);
    const firstTodo = firstTodoEnvelope.data?.story;
    if (!firstTodo?.code) {
      throw new Error("No TODO story found after backlog generation.");
    }
    logBackendStepDone(backend.id, "select-story", `Selected ${firstTodo.code}`);

    const planInvocation = adapter.buildPlanInvocation(context, firstTodo.code);
    logBackendStepStart(backend.id, "plan", `Running archetipo-plan for ${firstTodo.code}`);
    const planRun = await runLoggedCommand({
      ...context,
      step: "plan",
      ...planInvocation,
    });
    if (!planRun.ok) {
      return finalizeResult(context, classifyBackendFailure(context, "plan", planRun));
    }
    logBackendStepDone(backend.id, "plan", `Plan saved for ${firstTodo.code}`);

    logBackendStepStart(backend.id, "verify-plan", "Validating backlog and plan outputs");
    const afterPlan = await readCliEnvelope(context, "backlog-after-plan", ["backlog", "show"]);
    const storyAfterPlan = await readCliEnvelope(context, "planned-story", ["story", "show", firstTodo.code]);

    const verification = await verifyFinalState({
      connector: context.connector,
      sandboxDir,
      init: init.data,
      backlog: afterPlan.data,
      selectedStoryCode: firstTodo.code,
      plannedStory: storyAfterPlan.data?.story,
      tasks: storyAfterPlan.data?.tasks ?? [],
    });
    logBackendStepDone(
      backend.id,
      "verify-plan",
      `Final state verified (${verification.taskCount} tasks, story ${verification.selectedStoryCode})`,
    );

    await fs.writeFile(
      path.join(artifactsDir, "result.json"),
      JSON.stringify(
        {
          backend: backend.id,
          connector: context.connector,
          githubRepo: context.githubRepo,
          model: backend.model,
          status: "pass",
          story: firstTodo.code,
          sandboxDir,
          verification,
        },
        null,
        2,
      ),
    );

    return finalizeResult(context, {
      status: "pass",
      story: firstTodo.code,
      sandboxDir,
    });
  } catch (error) {
    if (error instanceof SkipError) {
      await fs.writeFile(
        path.join(artifactsDir, "result.json"),
        JSON.stringify(
          {
            backend: backend.id,
            connector: context.connector,
            githubRepo: context.githubRepo,
            model: backend.model,
            status: "skip",
            reason: error.message,
            sandboxDir,
          },
          null,
          2,
        ),
      );
      return finalizeResult(context, error);
    }

    await fs.writeFile(
      path.join(artifactsDir, "result.json"),
      JSON.stringify(
        {
          backend: backend.id,
          connector: context.connector,
          githubRepo: context.githubRepo,
          model: backend.model,
          status: "fail",
          reason: error.message,
          sandboxDir,
        },
        null,
        2,
      ),
    );

    return finalizeResult(context, {
      status: "fail",
      reason: error.message,
      sandboxDir,
    });
  }
}

async function resetWorkspace(workspaceRoot) {
  const defaultSandboxName = "sandbox";
  try {
    await fs.rm(workspaceRoot, {
      recursive: true,
      force: true,
      maxRetries: process.platform === "win32" ? 10 : 3,
      retryDelay: 250,
    });
    return { sandboxName: defaultSandboxName };
  } catch (error) {
    if (process.platform !== "win32" || !["EBUSY", "ENOTEMPTY", "EPERM"].includes(error?.code)) {
      throw error;
    }
  }

  const staleRoot = `${workspaceRoot}.stale-${Date.now()}`;
  try {
    await fs.rename(workspaceRoot, staleRoot);
    await fs.rm(staleRoot, {
      recursive: true,
      force: true,
      maxRetries: 10,
      retryDelay: 250,
    }).catch(() => {});
    return { sandboxName: defaultSandboxName };
  } catch (error) {
    if (!["EBUSY", "ENOTEMPTY", "EPERM"].includes(error?.code)) {
      throw error;
    }
  }

  await fs.rm(path.join(workspaceRoot, "artifacts"), {
    recursive: true,
    force: true,
    maxRetries: 5,
    retryDelay: 250,
  }).catch(() => {});
  return { sandboxName: `sandbox-${Date.now()}` };
}

async function verifyRequiredEnv(backend) {
  const required = backend.env_required ?? [];
  const missing = required.filter((name) => !process.env[name]);
  if (missing.length > 0) {
    throw new SkipError(`Missing required environment variables: ${missing.join(", ")}`);
  }
}

async function prepareWorkspace(context) {
  if (context.connector !== "github") {
    return;
  }

  logBackendStepDetail(context.backend.id, "bootstrap", "Checking GitHub prerequisites");
  await verifyGitHubPrerequisites(context);
  logBackendStepDetail(context.backend.id, "bootstrap", "Provisioning temporary GitHub repository");
  await provisionGitHubRepository(context);
  logBackendStepDetail(context.backend.id, "bootstrap", "Initializing git sandbox");
  await bootstrapSandboxGit(context);
}

async function installWorkspace(context) {
  const invocation = getInstallerInvocation(context);
  const install = await runLoggedCommand({
    ...context,
    step: "install",
    command: invocation.command,
    args: invocation.args,
  });
  if (!install.ok) {
    throw new Error(`Installer failed: ${install.stderr || install.stdout || `exit ${install.code}`}`);
  }
}

async function verifyInstallation(context) {
  const requiredPaths = [
    context.skillRoot("archetipo-spec"),
    context.skillRoot("archetipo-plan"),
    getCliBinaryPath(context.sandboxDir),
    path.join(context.sandboxDir, ".archetipo", "config.yaml"),
    path.join(context.sandboxDir, ".archetipo", "shared-runtime.md"),
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

async function readCliEnvelope(context, step, cliArgs) {
  const archetipoPath = getCliBinaryPath(context.sandboxDir);
  const result = await runLoggedCommand({
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

async function readOptionalBacklog(context) {
  const archetipoPath = getCliBinaryPath(context.sandboxDir);
  const result = await runLoggedCommand({
    ...context,
    step: "backlog-before",
    command: archetipoPath,
    args: ["backlog", "show"],
    acceptedExitCodes: [0, 4],
  });
  if (result.code === 0 && result.ok) {
    return JSON.parse(result.stdout);
  }

  let envelope;
  try {
    envelope = JSON.parse(result.stderr);
  } catch {
    throw new Error(`CLI command failed (backlog show): ${result.stderr || result.stdout}`);
  }

  if (envelope?.error?.code === "E_PRECONDITION") {
    await fs.appendFile(
      path.join(context.artifactsDir, "backlog-before.log"),
      "\nexpected_precondition=true\nnote=Missing backlog before archetipo-spec is expected in this preflight check.\n",
    );
    return null;
  }

  throw new Error(`CLI command failed (backlog show): ${result.stderr || result.stdout}`);
}

async function verifyFinalState({ connector, sandboxDir, init, backlog, selectedStoryCode, plannedStory, tasks }) {
  const items = backlog?.items ?? [];
  if (items.length === 0) {
    throw new Error("Backlog is empty after planning.");
  }

  const selected = items.find((story) => story.code === selectedStoryCode);
  if (!selected) {
    throw new Error(`Selected story ${selectedStoryCode} not found in final backlog.`);
  }
  if (selected.status !== "PLANNED") {
    throw new Error(`Selected story ${selectedStoryCode} expected PLANNED, found ${selected.status}.`);
  }

  const invalidStatuses = items.filter((story) => {
    if (story.code === selectedStoryCode) {
      return story.status !== "PLANNED";
    }
    return story.status !== "TODO";
  });
  if (invalidStatuses.length > 0) {
    throw new Error(
      `Expected only ${selectedStoryCode}=PLANNED and the rest TODO. Invalid stories: ${invalidStatuses
        .map((story) => `${story.code}:${story.status}`)
        .join(", ")}`,
    );
  }

  if (!plannedStory || plannedStory.code !== selectedStoryCode) {
    throw new Error(`Unable to read the planned story ${selectedStoryCode} after planning.`);
  }
  if (plannedStory.status !== "PLANNED") {
    throw new Error(`Story show returned status ${plannedStory.status} for ${selectedStoryCode}.`);
  }

  if (!Array.isArray(tasks) || tasks.length === 0) {
    throw new Error(`Plan for ${selectedStoryCode} was saved without tasks.`);
  }
  const nonTodoTasks = tasks.filter((task) => task.status !== "TODO");
  if (nonTodoTasks.length > 0) {
    throw new Error(
      `Expected all tasks to start in TODO. Invalid tasks: ${nonTodoTasks
        .map((task) => `${task.id}:${task.status}`)
        .join(", ")}`,
    );
  }
  const verification = {
    connector,
    selectedStoryCode,
    taskCount: tasks.length,
  };
  if (connector === "file") {
    const planningPath = resolveProjectPath(sandboxDir, init?.paths?.planning ?? ".archetipo/plans");
    const planFile = path.join(planningPath, `${selectedStoryCode}-plan.yaml`);
    await fs.access(planFile);
    verification.planFile = planFile;
  }
  return verification;
}

function resolveProjectPath(projectRoot, maybeRelative) {
  if (path.isAbsolute(maybeRelative)) {
    return maybeRelative;
  }
  return path.join(projectRoot, maybeRelative);
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
        context.backend.tool,
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
      context.backend.tool,
      "--connector",
      context.connector,
      "--yes",
    ],
  };
}

function getCliBinaryPath(sandboxDir) {
  const executable = process.platform === "win32" ? "archetipo.exe" : "archetipo";
  return path.join(sandboxDir, ".archetipo", "bin", executable);
}

async function runLoggedCommand({ sandboxDir, artifactsDir, step, command, args, timeoutMs, acceptedExitCodes = [0] }) {
  const startedAt = Date.now();
  const stdoutChunks = [];
  const stderrChunks = [];
  const prettyCommand = [command, ...args].join(" ");
  const heartbeatLabel = `${step} (${path.basename(command)})`;

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

  const code = await new Promise((resolve, reject) => {
    child.on("error", reject);
    child.on("close", resolve);
  }).finally(() => {
    clearTimeout(timeout);
    clearInterval(heartbeat);
  });

  const stdout = Buffer.concat(stdoutChunks).toString("utf8");
  const stderr = Buffer.concat(stderrChunks).toString("utf8");
  await fs.writeFile(
    path.join(artifactsDir, `${step}.log`),
    [
      `$ ${[command, ...args].join(" ")}`,
      "",
      "=== STDOUT ===",
      stdout,
      "",
      "=== STDERR ===",
      stderr,
      "",
      `exit_code=${code}`,
      `timed_out=${timedOut}`,
      `duration_ms=${Date.now() - startedAt}`,
    ].join("\n"),
  );

  const accepted = acceptedExitCodes.includes(code);
  const duration = formatDurationMs(Date.now() - startedAt);
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
    `ARchetipo E2E sandbox for ${context.scenarioName}/${context.backend.id}`,
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

  const gitInit = await runLoggedCommand({
    ...context,
    step: "git-init",
    command: "git",
    args: ["init", "-b", "main"],
  });
  if (!gitInit.ok) {
    throw new Error(`Sandbox git init failed: ${gitInit.stderr || gitInit.stdout || `exit ${gitInit.code}`}`);
  }

  const gitRemote = await runLoggedCommand({
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
    context.scenarioName,
    context.backend.id,
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

function buildSpecPrompt() {
  return "/archetipo-spec";
}

async function verifySpecSideEffects(context) {
  const archetipoRoot = path.join(context.sandboxDir, ".archetipo");
  const planningDir = path.join(archetipoRoot, "plans");

  const [planFiles, autopilotFiles] = await Promise.all([
    listFilesIfPresent(planningDir),
    listFilesIfPresent(archetipoRoot, /^autopilot-state-.*\.ya?ml$/),
  ]);

  if (planFiles.length > 0 || autopilotFiles.length > 0) {
    const details = [];
    if (planFiles.length > 0) {
      details.push(`unexpected plan files: ${planFiles.join(", ")}`);
    }
    if (autopilotFiles.length > 0) {
      details.push(`unexpected autopilot state: ${autopilotFiles.join(", ")}`);
    }
    throw new Error(`archetipo-spec produced side effects beyond backlog generation (${details.join("; ")}).`);
  }
}

async function listFilesIfPresent(dirPath, namePattern = null) {
  try {
    const entries = await fs.readdir(dirPath, { withFileTypes: true });
    return entries
      .filter((entry) => entry.isFile())
      .map((entry) => entry.name)
      .filter((name) => (namePattern ? namePattern.test(name) : true))
      .sort();
  } catch (error) {
    if (error?.code === "ENOENT") {
      return [];
    }
    throw error;
  }
}

function buildPlanPrompt(storyCode) {
  return `/archetipo-plan ${storyCode}`;
}

function shellEscape(value) {
  return `'${String(value).replace(/'/g, `'\\''`)}'`;
}

function classifyBackendFailure(context, step, commandResult) {
  const combined = `${commandResult.stdout}\n${commandResult.stderr}`;
  const authPattern = /(api key|oauth|unauthori[sz]ed|forbidden|not logged in|login|token|credential|authentication required)/i;
  if (authPattern.test(combined)) {
    return {
      status: "skip",
      reason: `${step} skipped because ${context.backend.id} is not authenticated or lacks credentials.`,
      sandboxDir: context.sandboxDir,
    };
  }

  if (commandResult.timedOut) {
    return {
      status: "fail",
      reason: `${step} timed out after ${context.timeoutMs}ms. See ${path.join(context.artifactsDir, `${step}.log`)}`,
      sandboxDir: context.sandboxDir,
    };
  }

  return {
    status: "fail",
    reason: `${step} failed with exit code ${commandResult.code}. See ${path.join(context.artifactsDir, `${step}.log`)}`,
    sandboxDir: context.sandboxDir,
  };
}

function finalizeResult(context, result) {
  if (result instanceof SkipError) {
    return {
      backend: context.backend.id,
      connector: context.connector,
      githubRepo: context.githubRepo,
      model: context.backend.model,
      status: "skip",
      reason: result.message,
      sandboxDir: context.sandboxDir,
    };
  }

  return {
    backend: context.backend.id,
    connector: context.connector,
    githubRepo: context.githubRepo,
    model: context.backend.model,
    status: result.status,
    reason: result.reason,
    story: result.story,
    sandboxDir: result.sandboxDir ?? context.sandboxDir,
  };
}

async function writeSummary(results, scenarioName) {
  const connector = results[0]?.connector ?? DEFAULT_CONNECTOR;
  const summaryPath = path.join(repoRoot, "test", "workspaces", scenarioName, connector, "summary.json");
  await fs.mkdir(path.dirname(summaryPath), { recursive: true });
  await fs.writeFile(summaryPath, JSON.stringify(results, null, 2));
}

function printSummary(results) {
  console.log("\nSummary:");
  for (const result of results) {
    console.log(`- ${formatResultLine(result)}`);
  }
}

function formatResultLine(result) {
  const base = `${result.backend} [${result.model}] [connector=${result.connector ?? DEFAULT_CONNECTOR}] -> ${result.status.toUpperCase()}`;
  if (result.story) {
    return `${base} (${result.story})`;
  }
  if (result.reason) {
    return `${base} - ${result.reason}`;
  }
  return base;
}

function logBackendStepStart(backendId, step, message) {
  console.log(` -> [${backendId}] ${step}: ${message}`);
}

function logBackendStepDone(backendId, step, message) {
  console.log(` <- [${backendId}] ${step}: ${message}`);
}

function logBackendStepDetail(backendId, step, message) {
  console.log(`    [${backendId}] ${step}: ${message}`);
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
