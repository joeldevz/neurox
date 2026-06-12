/**
 * OpenCode LLM Provider
 * Spawns local opencode CLI for deterministic model evaluation
 */

import { spawn } from 'child_process';

/**
 * Parse JSONL opencode output to extract final assistant text
 * @param {string} stdout - Raw JSONL output from opencode
 * @returns {string} Final assistant text content
 */
function parseOpencodeOutput(stdout) {
  if (!stdout || typeof stdout !== 'string') {
    throw new Error('No output from opencode');
  }

  let finalText = '';

  // Parse JSONL lines
  const lines = stdout.trim().split('\n');
  for (const line of lines) {
    if (!line.trim()) continue;

    try {
      const event = JSON.parse(line);

      // Extract text from text events
      if (event.type === 'text' && event.part?.text) {
        finalText += event.part.text;
      }

      // Log step_finish for debugging (contains token info)
      if (event.type === 'step_finish') {
        // Token consumption recorded
      }
    } catch (err) {
      // Skip unparseable lines
      continue;
    }
  }

  if (!finalText) {
    throw new Error('No text extracted from opencode output');
  }

  return finalText.trim();
}

/**
 * Complete a prompt via opencode CLI
 * @param {string} prompt - The user prompt/message
 * @param {object} config - Configuration
 *   - model: opencode model ID (e.g. "anthropic/claude-haiku-4-5")
 *   - timeoutMs: request timeout in milliseconds (default 180000)
 *   - title: session title for tracing (default "memorybench-llm")
 *   - cwd: working directory (default current dir)
 * @returns {Promise<string>} Assistant response text
 */
export async function opencodeComplete(prompt, config = {}) {
  const {
    model = 'anthropic/claude-haiku-4-5',
    timeoutMs = 180000,
    title = 'memorybench-llm',
    cwd = process.cwd(),
  } = config;

  // Validate inputs
  if (!prompt || typeof prompt !== 'string') {
    throw new Error('Prompt must be a non-empty string');
  }
  if (!model || typeof model !== 'string') {
    throw new Error('Model must be specified');
  }

  const args = [
    'run',
    '-m',
    model,
    '--format',
    'json',
    '--title',
    title,
    '--agent',
    'bench-raw',
  ];

  let lastError = null;
  const maxAttempts = 3; // With env vars, without env vars, final retry

  for (let attempt = 1; attempt <= maxAttempts; attempt++) {
    try {
      const result = await spawnAndCapture('opencode', args, prompt, timeoutMs, cwd, attempt);
      return parseOpencodeOutput(result);
    } catch (err) {
      lastError = err;
      
      if (attempt < maxAttempts) {
        // Log and retry
        continue;
      } else {
        // Final attempt failed
        throw new Error(`opencode call failed after all retry strategies: ${err.message}`);
      }
    }
  }

  // Should not reach here, but fallback to last error
  throw lastError || new Error('opencode failed after all attempts');
}

/**
 * Helper: spawn process and capture output with isolated environment
 * @param {string} command - Command to run
 * @param {string[]} args - Command arguments
 * @param {string} stdin - Input to send via stdin
 * @param {number} timeoutMs - Timeout in milliseconds
 * @param {string} cwd - Working directory
 * @param {number} attempt - Attempt number (1 = with env, 2+ = without)
 */
function spawnAndCapture(command, args, stdin, timeoutMs, cwd, attempt = 1) {
  return new Promise((resolve, reject) => {
    // Attempt 1: try with plugin isolation flags
    // Attempt 2+: fall back to no special env vars
    const useEnvFlags = attempt === 1;
    
    const env = useEnvFlags
      ? {
          ...process.env,
          OPENCODE_DISABLE_EXTERNAL_SKILLS: '1',
          OPENCODE_DISABLE_CLAUDE_CODE_SKILLS: '1',
        }
      : process.env;

    const proc = spawn(command, args, { cwd, env });
    let stdout = '';
    let stderr = '';
    let timedOut = false;

    const timer = setTimeout(() => {
      timedOut = true;
      proc.kill();
    }, timeoutMs);

    proc.stdout.on('data', (data) => {
      stdout += data.toString();
    });

    proc.stderr.on('data', (data) => {
      stderr += data.toString();
    });

    proc.on('close', (code) => {
      clearTimeout(timer);

      if (timedOut) {
        reject(new Error(`Timeout after ${timeoutMs}ms`));
        return;
      }

      if (code !== 0) {
        const errMsg = stderr || `Process exited with code ${code}`;
        reject(new Error(`Exit code ${code}: ${errMsg.slice(0, 200)}`));
        return;
      }

      if (!stdout) {
        reject(new Error('No output from opencode'));
        return;
      }

      resolve(stdout);
    });

    proc.on('error', (err) => {
      clearTimeout(timer);
      reject(err);
    });

    // Send prompt via stdin
    proc.stdin.write(stdin);
    proc.stdin.end();
  });
}

/**
 * Create an LLM call abstraction for opencode
 * @param {Array<{role, content}>} messages - Chat messages
 * @param {object} config - Configuration (model, temperature, max_tokens, etc.)
 * @returns {Promise<string>} LLM response text
 */
export async function callOpencodeModel(messages, config = {}) {
  // Default model should match anthropic's default: claude-sonnet-4-5
  // When passed from llmJudge, model will be null or explicit value
  const {
    model = null,
    temperature = 0,
    max_tokens = 100,
    timeoutMs = 180000,
  } = config;

  // Resolve model: if null/undefined, use the equivalent of anthropic's default
  const resolvedModel = model || process.env.JUDGE_MODEL || 'anthropic/claude-sonnet-4-5';

  // Convert messages to a single prompt (user message is the last one)
  if (!messages || messages.length === 0) {
    throw new Error('No messages provided');
  }

  const lastMessage = messages[messages.length - 1];
  if (!lastMessage.content) {
    throw new Error('Last message has no content');
  }

  // Note: temperature is set in the agent frontmatter, but we pass it here for compatibility
  const prompt = lastMessage.content;

  return opencodeComplete(prompt, {
    model: resolvedModel,
    timeoutMs,
    title: 'memorybench-llm',
  });
}
