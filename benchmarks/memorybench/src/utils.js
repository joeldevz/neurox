/**
 * Utility functions
 */

import fs from 'fs';
import path from 'path';

/**
 * Generate a unique run ID
 */
export function generateRunId() {
  const timestamp = new Date().toISOString().replace(/[^\d]/g, '').substring(0, 12);
  const random = Math.random().toString(36).substring(2, 8).toUpperCase();
  return `${timestamp}-${random}`;
}

/**
 * Ensure directory exists
 */
export function ensureDir(dirPath) {
  if (!fs.existsSync(dirPath)) {
    fs.mkdirSync(dirPath, { recursive: true });
  }
}

/**
 * Load JSON file with error handling
 */
export function loadJSON(filePath) {
  try {
    const content = fs.readFileSync(filePath, 'utf-8');
    return JSON.parse(content);
  } catch (err) {
    throw new Error(`Failed to load ${filePath}: ${err.message}`);
  }
}

/**
 * Save JSON file with formatting
 */
export function saveJSON(filePath, data) {
  ensureDir(path.dirname(filePath));
  fs.writeFileSync(filePath, JSON.stringify(data, null, 2));
}

/**
 * Format time duration
 */
export function formatDuration(ms) {
  if (ms < 1000) return `${Math.round(ms)}ms`;
  if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`;
  return `${(ms / 60000).toFixed(1)}m`;
}

/**
 * Format percentage
 */
export function formatPercent(value) {
  return `${(value * 100).toFixed(1)}%`;
}

/**
 * Detect temporal intent in a query
 * Returns true if the query contains temporal reasoning keywords
 * @param {string} query - The query/question to analyze
 * @returns {boolean} true if temporal intent detected
 */
export function detectTemporalIntent(query) {
  if (!query || typeof query !== 'string') {
    return false;
  }
  
  const lowerQuery = query.toLowerCase();
  const temporalKeywords = [
    'first', 'last', 'before', 'after', 'when',
    'earliest', 'latest', 'how long', 'how many days',
    'how many weeks', 'how many months', 'since', 'until',
    'ago', 'between'
  ];
  
  return temporalKeywords.some(keyword => lowerQuery.includes(keyword));
}
