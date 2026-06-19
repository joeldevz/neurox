/**
 * Evaluation Judge & LLM Interface
 * Multi-provider LLM abstraction (Anthropic, OpenAI, Gateway, OpenCode)
 * Deterministic question-answering evaluation
 */

import fetch from 'node-fetch';
import { callOpencodeModel } from './llm-opencode.js';

/**
 * Normalize text for fuzzy matching
 */
function normalize(text) {
  if (!text) return '';
  const str = String(text ?? '');
  return str
    .toLowerCase()
    .trim()
    .replace(/[^\w\s]/g, '') // Remove punctuation
    .replace(/\b(the|a|an|is|are)\b/g, '') // Remove articles
    .replace(/\s+/g, ' ')
    .trim();
}

/**
 * Check if predicted contains answer (fuzzy)
 */
function fuzzyMatch(predicted, answer) {
  const normPred = normalize(predicted);
  const normAns = normalize(answer);

  // Exact match
  if (normPred === normAns) return true;

  // Contains check (either direction)
  if (normPred.includes(normAns) || normAns.includes(normPred)) {
    return true;
  }

  // Word overlap check
  const predWords = normPred.split(' ');
  const ansWords = normAns.split(' ');
  const overlap = ansWords.filter((w) => predWords.includes(w)).length;

  return overlap >= Math.max(1, Math.floor(ansWords.length * 0.7));
}

/**
 * Judge with exact-match (no LLM) — fallback mode
 * WARNING: Results are NOT comparable to competition
 */
export function exactMatch(predicted, answer) {
  return {
    correct: fuzzyMatch(predicted, answer),
    confidence: 1.0,
    reason: 'exact-match (fuzzy normalization) ⚠️  NOT COMPARABLE',
  };
}

/**
 * Multi-provider LLM call abstraction
 * Supports: anthropic (Claude), openai (GPT), gateway (OpenAI-compatible), opencode (local CLI)
 *
 * @param {Array<{role, content}>} messages - Chat messages
 * @param {object} config - Configuration
 *   - provider: 'anthropic'|'openai'|'gateway'|'opencode'
 *   - model: model name (defaults based on provider)
 *   - temperature: 0-1 (default 0 for judging)
 *   - max_tokens: number
 * @returns {Promise<string>} LLM response text
 */
export async function callLLM(messages, config = {}) {
  const {
    provider = process.env.JUDGE_PROVIDER || 'anthropic',
    model = null,
    temperature = 0,
    max_tokens = 100,
  } = config;

  if (provider === 'anthropic') {
    return callAnthropicClaude(messages, { model, temperature, max_tokens });
  } else if (provider === 'openai') {
    return callOpenAI(messages, { model, temperature, max_tokens });
  } else if (provider === 'gateway') {
    return callGateway(messages, { model, temperature, max_tokens });
  } else if (provider === 'opencode') {
    return callOpencodeModel(messages, { model, temperature, max_tokens });
  } else {
    throw new Error(`Unknown LLM provider: ${provider}`);
  }
}

/**
 * Call Anthropic Claude API
 */
async function callAnthropicClaude(messages, config) {
  const apiKey = process.env.ANTHROPIC_API_KEY;
  if (!apiKey) {
    throw new Error('ANTHROPIC_API_KEY not set');
  }

  const model = config.model || process.env.JUDGE_MODEL || 'claude-sonnet-4-5';
  const url = 'https://api.anthropic.com/v1/messages';

  const response = await fetch(url, {
    method: 'POST',
    headers: {
      'x-api-key': apiKey,
      'anthropic-version': '2023-06-01',
      'content-type': 'application/json',
    },
    body: JSON.stringify({
      model,
      max_tokens: config.max_tokens,
      temperature: config.temperature,
      messages,
    }),
  });

  if (!response.ok) {
    const error = await response.text();
    throw new Error(`Anthropic API error: ${response.status} ${error}`);
  }

  const data = await response.json();
  if (!data.content || !data.content[0]) {
    throw new Error('No content in Anthropic response');
  }

  return data.content[0].text;
}

/**
 * Call OpenAI GPT API
 */
async function callOpenAI(messages, config) {
  const apiKey = process.env.OPENAI_API_KEY;
  if (!apiKey) {
    throw new Error('OPENAI_API_KEY not set');
  }

  const model = config.model || process.env.JUDGE_MODEL || 'gpt-4o';
  const url = 'https://api.openai.com/v1/chat/completions';

  const response = await fetch(url, {
    method: 'POST',
    headers: {
      'Authorization': `Bearer ${apiKey}`,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({
      model,
      max_tokens: config.max_tokens,
      temperature: config.temperature,
      messages,
    }),
  });

  if (!response.ok) {
    const error = await response.text();
    throw new Error(`OpenAI API error: ${response.status} ${error}`);
  }

  const data = await response.json();
  if (!data.choices || !data.choices[0]?.message?.content) {
    throw new Error('No content in OpenAI response');
  }

  return data.choices[0].message.content;
}

/**
 * Call OpenAI-compatible gateway (e.g. opencode.ai/zen)
 */
async function callGateway(messages, config) {
  const apiKey = process.env.GATEWAY_API_KEY;
  const url = process.env.GATEWAY_URL || 'https://opencode.ai/zen/go/v1';

  if (!apiKey) {
    throw new Error('GATEWAY_API_KEY not set');
  }

  const model = config.model || process.env.JUDGE_MODEL || 'deepseek-v4-pro';
  const fullUrl = `${url}/chat/completions`;

  const response = await fetch(fullUrl, {
    method: 'POST',
    headers: {
      'Authorization': `Bearer ${apiKey}`,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({
      model,
      max_tokens: config.max_tokens,
      temperature: config.temperature,
      messages,
    }),
  });

  if (!response.ok) {
    const error = await response.text();
    throw new Error(`Gateway API error: ${response.status} ${error}`);
  }

  const data = await response.json();
  if (!data.choices || !data.choices[0]?.message?.content) {
    throw new Error('No content in Gateway response');
  }

  return data.choices[0].message.content;
}

/**
 * Judge with LLM — strict evaluation
 * Uses the configured provider to evaluate if predicted answer is correct
 */
export async function llmJudge(predicted, answer, question, config = {}) {
  const {
    provider = process.env.JUDGE_PROVIDER || 'anthropic',
    model = null,
  } = config;

  const prompt = `You are a strict evaluator of question-answering memory systems.
Given a question, the ground-truth answer, and a predicted answer, decide if the prediction is CORRECT.

Rules:
- The prediction must contain the specific information in the ground truth (names, dates, numbers must match).
- A vague answer that identifies the topic but misses the specific detail is INCORRECT.
- "I don't know" is INCORRECT unless the ground truth is an abstention.
- Ignore differences in phrasing, capitalization, or extra correct detail.

Question: ${question}
Ground Truth: ${answer}
Predicted: ${predicted}

Respond with ONLY a JSON object: {"correct": true|false, "reason": "<one short sentence>"}`;

  try {
    const response = await callLLM([{ role: 'user', content: prompt }], {
      provider,
      model,
      temperature: 0,
      max_tokens: 150,
    });

    // Try to parse JSON response
    const jsonMatch = response.match(/\{[\s\S]*\}/);
    if (jsonMatch) {
      const parsed = JSON.parse(jsonMatch[0]);
      return {
        correct: parsed.correct === true,
        confidence: 0.95,
        reason: `llm-judge (${parsed.reason || response.slice(0, 50)})`,
      };
    }

    // Fallback: search for key words in response
    const lowerResponse = String(response ?? '').toLowerCase();
    const correct = lowerResponse.includes('true') || 
                    lowerResponse.includes('correct') ||
                    (lowerResponse.includes('yes') && !lowerResponse.includes('no'));

    return {
      correct,
      confidence: 0.85,
      reason: `llm-judge (parsed: ${response.slice(0, 50)})`,
    };
  } catch (err) {
    console.warn(`LLM judge exception: ${err.message}, falling back to exact-match`);
    return exactMatch(predicted, answer);
  }
}

/**
 * Create a judge function based on mode
 * 'auto' = use anthropic if ANTHROPIC_API_KEY, else exact-match with warning
 * 'llm' = use LLM (auto-detect provider from JUDGE_PROVIDER or anthropic)
 * 'exact' = use fuzzy exact-match
 * '<provider>' = use specific provider
 */
export function createJudge(mode = 'auto', judgeConfig = {}) {
  if (mode === 'auto') {
    // Auto-detect: use Anthropic if key available, else exact-match
    if (process.env.ANTHROPIC_API_KEY) {
      console.log('ℹ️  Using Anthropic Claude for judging (comparable with competition)');
      mode = 'llm';
    } else if (process.env.OPENAI_API_KEY) {
      console.log('ℹ️  Using OpenAI for judging (comparable with competition)');
      mode = 'llm';
    } else if (process.env.GATEWAY_API_KEY) {
      console.log('ℹ️  Using Gateway for judging (comparable with competition)');
      mode = 'llm';
    } else {
      console.warn('⚠️  No LLM API keys found. Falling back to exact-match.');
      console.warn('⚠️  Results with exact-match are NOT comparable with Mem0, Zep, Supermemory.');
      console.warn('⚠️  Set ANTHROPIC_API_KEY, OPENAI_API_KEY, or GATEWAY_API_KEY for comparable results.');
      mode = 'exact';
    }
  }

  if (mode === 'llm' || ['anthropic', 'openai', 'gateway', 'opencode'].includes(mode)) {
    const provider = mode === 'llm' ? (process.env.JUDGE_PROVIDER || 'anthropic') : mode;
    const config = { ...judgeConfig, provider };
    return (predicted, answer, question) => llmJudge(predicted, answer, question, config);
  }

  if (mode === 'exact') {
    return exactMatch;
  }

  console.warn(`Unknown judge mode: ${mode}, falling back to exact-match`);
  return exactMatch;
}
