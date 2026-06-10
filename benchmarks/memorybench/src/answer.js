/**
 * Answer Generation Module
 * Generates answers from retrieved context using LLM
 */

import { callLLM } from './judge.js';

/**
 * Generate an answer from context using LLM
 * @param {string} question - The original question
 * @param {string} context - Retrieved context from search
 * @param {object} config - Configuration object with provider/model settings
 * @returns {Promise<string>} Generated answer
 */
export async function generateAnswer(question, context, config = {}) {
  const { provider = 'anthropic', model = null, temperature = 0 } = config;

  // If no context, return early
  if (!context || context.trim().length === 0) {
    return "I don't know.";
  }

  const prompt = `You are answering a question based ONLY on the provided memory context. 
If the context does not contain the answer, say "I don't know".
Be concise and specific.

Memory context:
${context}

Question: ${question}

Answer:`;

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
