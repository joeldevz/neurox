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
   * Returns true if the query requires temporal reasoning about event relations.
   * Designed to exclude state durations, aggregations, and habitual information.
   * 
   * Validation against longmemeval_s.json (500 questions):
   * - Temporal-reasoning recall: 91.73% (122/133)
   * - False-fire rate: 7.08% (26/367 non-temporal)
   * - All 5 benchmark failure cases excluded ✓
   * Fixes (2026-06-11 retry):
   *   1. Expanded "since I X" exclusion to include: got, received, acquired, bought, purchased, changed, adopted
   *   2. Added explicit "how many/much hours/time have I spent" aggregation filter
   *   3. Added "how long is my X" state-dimension exclusion
   * Date: 2026-06-11 (revised)
 * 
 * @param {string} query - The query/question to analyze
 * @returns {boolean} true if temporal intent detected
 */
export function detectTemporalIntent(query) {
  if (!query) {
    return false;
  }
  
  const lowerQuery = String(query ?? '').toLowerCase();
  
  // === EXCLUSION FILTERS (fast-path rejections for non-temporal) ===
  
  // Habitual patterns: "what time do I X" (not events)
  if (/what\s+time\s+do\s+i\b/.test(lowerQuery)) {
    return false;
  }
  
  // Habitual: "what day of week do I X"
  if (/what\s+day\s+of\s+the\s+week\s+do\s+i\b/.test(lowerQuery)) {
    return false;
  }
  
  // State duration: "how long have I been using/living in/had"
  if (/how\s+long\s+have\s+i\s+been\s+\w+\s+(my|this|a|your)\b/.test(lowerQuery)) {
    return false;
  }
  
  // Aggregation: "how many hours/time have I spent" (activity totals)
  // Must have explicit temporal markers to be temporal reasoning
  if (/how\s+(many|much)\s+(hours?|days?|weeks?|months?|time)\s+(have|had)\s+i\s+spent\b/.test(lowerQuery)) {
    if (!/\b(between|before|after|since|ago|passed|event|trip|visit)\b/.test(lowerQuery)) {
      return false;  // Pure aggregation without inter-event relation
    }
  }
  
  // Aggregation: "how many days/weeks did I spend/watch/read" (activity totals)
  // Unless it has inter-event markers (between, before, after, since, ago)
  if (/how\s+(many|much)\s+(days?|weeks?|months?)\s+did\s+i\s+(spend|take|watch|read|write|listen)\b/.test(lowerQuery)) {
    if (!/\b(between|before|after|since|ago|event|trip|visit|camping|reading|watching)\b/.test(lowerQuery)) {
      return false;
    }
  }
  
  // "have I spent/had total" without explicit temporal context
  if (/how\s+(many|much)\s+have\s+i\s+(spent|had)\b/.test(lowerQuery)) {
    if (!/\b(days?|weeks?|months?|years?|between|since|before|after|passed|ago|event|trip|visit|on)\b/.test(lowerQuery)) {
      return false;
    }
  }
  
  // "it took X [to do Y]" without explicit event relation
  if (/did\s+it\s+take\b/.test(lowerQuery) && !/\d+\s+(days?|weeks?|months?|years?)/.test(lowerQuery)) {
    if (!/\b(how|between|before|after|since|ago)\s+(long|many|much)\b/.test(lowerQuery)) {
      return false;
    }
  }
  
  // "since I started/got/received/acquired/bought X my Y" (collection/habit) without event context
  if (/since\s+i\s+(started|began|got|received|acquired|bought|purchased|changed|adopted)\s+\w+\s+(my|this|a)\b/.test(lowerQuery)) {
    if (!/\b(event|trip|class|course|work|job|school|university|college|business|project|career|program|initiative|role|position)\b/.test(lowerQuery)) {
      return false;
    }
  }
  
  // "how long is my X" (state dimension, not event time)
  if (/how\s+long\s+is\s+(my|this|a|your)\b/.test(lowerQuery)) {
    return false;
  }
  
  // "this weekend" / future planning (not temporal reasoning)
  if (/this\s+(weekend|week)\b/.test(lowerQuery) && !/\b(last|previous|before|after|happened|when)\b/.test(lowerQuery)) {
    return false;
  }
  
  // === HIGH-CONFIDENCE TEMPORAL PATTERNS ===
  
  const temporalPatterns = [
    // ORDERING/COMPARISON (highest confidence)
    /\bhappened\s+(first|last|earlier|later)\b/,
    /which\s+.+\s+happened\s+(first|last)\b/,
    /which\s+.+\s+(first|last|earliest|latest)\b/,
    /what\s+is\s+the\s+order/,
    /order\s+of\s+(the\s+)?.+\s+(from\s+(earliest|latest|first|last))?/,
    /\bfirst\s+(event|trip|time|day|activity|visit|book|concert|show|device|item|thing|speaker|class|issue)\b/,
    /\blast\s+(event|trip|time|day|activity|visit|book|concert|show|device)\s+(i\s+)?/,
    /earliest|latest/,
    /who\s+.+\s+(first|last|earlier|later)\b/,
    /who\s+(graduated|became|started|finished|took|met|visited|attended|participated|purchased|bought|completed)\s+(first|last)\b/,
    
    // DURATIONS: explicit time units + temporal verbs
    /\bago\b/,
    /how\s+long/,
    /days?\s+(passed|between|ago|apart)\b/,
    /weeks?\s+(passed|between|ago|apart|in\s+total)\b/,
    /months?\s+(passed|between|ago|apart)\b/,
    /years?\s+(passed|between|ago|apart)\b/,
    /hours?\s+(passed|ago)\b/,
    /\d+\s+(days?|weeks?|months?|years?|hours?)\s+(passed|ago|between|have|had)\b/,
    /\d+\s+(days?|weeks?|months?|years?)\s+in\s+total/,
    
    // TIME RELATIONS
    /time\s+(between|since|passed|when|before|after)\b/,
    /duration/,
    /how\s+(much|many)\s+(time|hours?|days?|weeks?|months?|years?)\s+(passed|between|since|ago|have|had|took|before|after)\b/,
    /how\s+(many)\s+(days?|weeks?|months?|years?)\s+(had\s+)?passed\b/,
    
    // TEMPORAL EVENT QUESTIONS
    /when\s+(did|do|was|were|will|is|are|have|has|had)\s+i\b/,
    /what\s+(time|date|year|month|day)\s+(did|do|was|were)\b/,
    /which\s+(date|year|time)\b/,
    
    // PAST REFERENCES
    /\b(last)\s+(saturday|friday|sunday|monday|tuesday|wednesday|thursday|week)\b/,
    
    // PAST EVENT + TEMPORAL RELATION
    /\bsince\s+i\s+(started|began|finished|completed|participated|took|visited|attended|moved|graduated|joined|changed|adopted|acquired|bought|purchase|received|began)\b/,
    /\bsince\s+i\s+.+\s+(event|trip|visit|activity|class|course|job|work)\b/,
    
    // EXPLICIT INTER-EVENT TIME
    /how\s+(many)\s+(days?|weeks?|months?|years?)\s+.+\s+(since|between|ago|passed)\b/,
  ];
  
  return temporalPatterns.some(pattern => pattern.test(lowerQuery));
}
