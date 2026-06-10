/**
 * LongMemEval-S Dataset Loader
 * Loads questions and sessions from the benchmark dataset
 */

import fs from 'fs';
import path from 'path';
import { fileURLToPath } from 'url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));

export class LongMemEvalBenchmark {
  constructor(dataPath = null) {
    // Default path relative to benchmarks dir
    if (!dataPath) {
      const benchmarksDir = path.join(__dirname, '../../..');
      dataPath = path.join(benchmarksDir, 'longmemeval', 'data', 'longmemeval_s.json');
    }
    this.dataPath = dataPath;
    this.dataset = null;
  }

  /**
   * Load the dataset from disk
   */
  async load() {
    try {
      const content = fs.readFileSync(this.dataPath, 'utf-8');
      this.dataset = JSON.parse(content);
      console.log(`Loaded ${this.dataset.length} questions from ${this.dataPath}`);
      return this.dataset;
    } catch (err) {
      throw new Error(`Failed to load dataset: ${err.message}`);
    }
  }

  /**
   * Get questions with their haystack sessions
   * Returns array of { question_id, question, answer, question_type, haystack_sessions }
   */
  getQuestions(limit = null, stratified = false) {
    if (!this.dataset) {
      throw new Error('Dataset not loaded. Call load() first.');
    }

    let questions = this.dataset.map((item) => ({
      question_id: item.question_id,
      question: item.question,
      answer: item.answer,
      question_type: item.question_type,
      question_date: item.question_date,
      haystack_sessions: item.haystack_sessions || [],
      haystack_dates: item.haystack_dates || [],
      answer_session_ids: item.answer_session_ids || [],
    }));

    if (limit && limit > 0) {
      if (stratified) {
        questions = this._stratifiedSample(questions, limit);
      } else {
        questions = questions.slice(0, limit);
      }
    }

    return questions;
  }

  /**
   * Stratified sampling: sample limit questions proportionally across question_type categories
   * Returns exactly 'limit' questions sampled from each category
   * Uses deterministic selection (first N from each category)
   */
  _stratifiedSample(questions, limit) {
    // Group by question_type
    const grouped = {};
    for (const q of questions) {
      const type = q.question_type;
      if (!grouped[type]) {
        grouped[type] = [];
      }
      grouped[type].push(q);
    }

    // Total count
    const totalCount = questions.length;

    // Compute allocation per type
    const allocation = {};
    let totalAllocated = 0;

    // Calculate proportional allocation for each type
    const types = Object.keys(grouped).sort();
    for (const type of types) {
      const count = grouped[type].length;
      const proportion = count / totalCount;
      const allocated = Math.round(limit * proportion);
      allocation[type] = allocated;
      totalAllocated += allocated;
    }

    // Adjust rounding: add/subtract from largest category to reach exactly 'limit'
    if (totalAllocated !== limit) {
      const difference = limit - totalAllocated;
      // Find category with largest count
      let largestType = types[0];
      let largestCount = grouped[types[0]].length;
      for (const type of types) {
        if (grouped[type].length > largestCount) {
          largestCount = grouped[type].length;
          largestType = type;
        }
      }
      allocation[largestType] += difference;
    }

    // Sample from each category
    const sampled = [];
    for (const type of types) {
      const count = Math.max(0, allocation[type]); // ensure non-negative
      sampled.push(...grouped[type].slice(0, count));
    }

    return sampled;
  }

  /**
   * Get session counts by type
   */
  getTypeDistribution(limit = null, stratified = false) {
    const questions = this.getQuestions(limit, stratified);
    const distribution = {};
    
    for (const q of questions) {
      distribution[q.question_type] = (distribution[q.question_type] || 0) + 1;
    }

    return distribution;
  }
}
