/**
 * Answer Generation Module
 * Generates answers from retrieved context using LLM
 */

import { callLLM } from './judge.js';
import { detectTemporalIntent } from './utils.js';

/**
 * Normalize question_date to ISO YYYY-MM-DD format
 * Handles both slash and hyphen formats
 * @param {string} dateStr - Date string in format like "2023/05/20" or "2023-05-20"
 * @returns {string|null} Normalized date as YYYY-MM-DD or null if invalid
 */
function normalizeQuestionDate(dateStr) {
  if (!dateStr || typeof dateStr !== 'string') {
    return null;
  }
  
  // Match both slash and hyphen formats: YYYY/MM/DD or YYYY-MM-DD
  const match = dateStr.match(/(\d{4})[\/-](\d{2})[\/-](\d{2})/);
  if (match) {
    return `${match[1]}-${match[2]}-${match[3]}`;
  }
  
  return null;
}

/**
 * Generate an answer from context using LLM
 * @param {string} question - The original question
 * @param {string} context - Retrieved context from search
 * @param {object} config - Configuration object with provider/model/questionDate settings
 * @returns {Promise<string>} Generated answer
 */
export async function generateAnswer(question, context, config = {}) {
  const { provider = 'anthropic', model = null, temperature = 0, questionDate = null, noTemporalBranch = false } = config;

  // If no context, return early
  if (!context || context.trim().length === 0) {
    return "I don't know.";
  }

  // Normalize question date if provided
  let normalizedDate = null;
  if (questionDate && !noTemporalBranch) {
    normalizedDate = normalizeQuestionDate(questionDate);
  }

  // Build prompt with optional current date section (CHANGE 3)
  let promptParts = [
    'You are answering a question based ONLY on the provided memory context.',
    'If the context does not contain the answer, say "I don\'t know".',
    'Be concise and specific.'
  ];

  // Add current date if available (CHANGE 3)
  if (normalizedDate) {
    promptParts.push(`\nCurrent date: ${normalizedDate} (the question is being asked on this date).`);
  }

  // Add temporal reasoning instructions if question has temporal intent (CHANGE 5)
  const hasTemporalIntent = !noTemporalBranch && detectTemporalIntent(question);
  if (hasTemporalIntent && normalizedDate) {
    promptParts.push(`
TEMPORAL REASONING INSTRUCTIONS:
1. Each memory includes a Date (the date of that conversation/event). Build a timeline.
2. Use the current date above as "now" for questions about "ago", "since", "how long".
3. For sequence questions: "first" = earliest Date, "last" = most recent Date.
4. For duration questions: compute the difference between the relevant Dates in days/weeks/months.
5. Cite the dates you used in your answer.`);
  }

  promptParts.push(`
Memory context:
${context}

Question: ${question}

Answer:`);

  const prompt = promptParts.join('\n');

  try {
    const messages = [{ role: 'user', content: prompt }];
    const response = await callLLM(messages, {
      provider,
      model,
      temperature,
      max_tokens: 300,
    });

    return response.trim();
  } catch (err) {
    console.warn(`Answer generation error: ${err.message}, returning "I don't know"`);
    return "I don't know.";
  }
}
