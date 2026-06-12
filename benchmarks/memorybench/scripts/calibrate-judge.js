#!/usr/bin/env node

/**
 * Calibration Script: OpenCode Judge vs Anthropic API Judge
 * 
 * Validates that the opencode judge (with bench-raw agent) produces verdicts
 * that agree with the original API judge on the same 50 predictions.
 * 
 * Gate: >= 92% agreement (46/50)
 */

import fs from 'fs';
import path from 'path';
import { fileURLToPath } from 'url';
import { llmJudge } from '../src/judge.js';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const REPORT_PATH = path.join(__dirname, '../data/runs/ab50-realdates-2b/report.json');
const AGREEMENT_GATE = 0.92;
const CONCURRENCY = 2;

async function sleep(ms) {
  return new Promise(resolve => setTimeout(resolve, ms));
}

async function runCalibration() {
  console.log('=== OpenCode Judge Calibration ===\n');

  // Load baseline report
  if (!fs.existsSync(REPORT_PATH)) {
    console.error(`Error: Report not found at ${REPORT_PATH}`);
    process.exit(1);
  }

  const report = JSON.parse(fs.readFileSync(REPORT_PATH, 'utf8'));
  const results = report.results;

  console.log(`Loaded ${results.length} predictions from ab50-realdates-2b`);
  console.log(`Original judge: ${report.judge_provider} (${report.judge_mode})`);
  console.log(`OpenCode judge: anthropic/claude-sonnet-4-5 (via bench-raw agent)\n`);
  console.log(`Starting calibration (concurrency: ${CONCURRENCY})...\n`);

  const startTime = Date.now();
  let agreement = 0;
  let apiCorrectOpencodeIncorrect = [];
  let apiIncorrectOpencodeCorrect = [];
  let errors = [];

  // Process in batches with concurrency control
  for (let i = 0; i < results.length; i += CONCURRENCY) {
    const batch = results.slice(i, Math.min(i + CONCURRENCY, results.length));
    
    const promises = batch.map(async (result, batchIdx) => {
      const idx = i + batchIdx;
      const { question_id, question, answer, predicted, correct: apiVerdict } = result;

      try {
        // Call opencode judge using the real judge code path
        const opencodeVerdict = await llmJudge(predicted, answer, question, {
          provider: 'opencode',
          model: 'anthropic/claude-sonnet-4-5',
        });

        const agrees = apiVerdict === opencodeVerdict.correct;

        // Log per-call status
        const status = agrees ? '✓' : '✗';
        console.log(
          `${status} Q${idx}: API=${apiVerdict ? 'CORRECT' : 'INCORRECT'} / OpenCode=${opencodeVerdict.correct ? 'CORRECT' : 'INCORRECT'}`
        );

        // Track disagreements
        if (!agrees) {
          if (apiVerdict && !opencodeVerdict.correct) {
            apiCorrectOpencodeIncorrect.push({
              idx,
              question_id,
              question: question.substring(0, 60),
            });
          } else if (!apiVerdict && opencodeVerdict.correct) {
            apiIncorrectOpencodeCorrect.push({
              idx,
              question_id,
              question: question.substring(0, 60),
            });
          }
        }

        return { idx, agrees };
      } catch (err) {
        console.error(`✗ Q${idx}: ERROR - ${err.message}`);
        errors.push({
          idx,
          question_id,
          error: err.message,
        });
        return { idx, agrees: false };
      }
    });

    const batchResults = await Promise.all(promises);
    agreement += batchResults.filter(r => r.agrees).length;

    // Small delay between batches
    if (i + CONCURRENCY < results.length) {
      await sleep(100);
    }
  }

  const runtime = ((Date.now() - startTime) / 1000).toFixed(1);
  const agreementPercent = (agreement / results.length * 100).toFixed(1);
  const gateStatus = agreement / results.length >= AGREEMENT_GATE ? 'PASS' : 'FAIL';

  console.log('\n=== Calibration Results ===\n');
  console.log(`Agreement: ${agreement}/${results.length} (${agreementPercent}%)`);
  console.log(`Gate (${(AGREEMENT_GATE * 100).toFixed(0)}%): ${gateStatus}\n`);

  console.log('Consensus:');
  const apiCorrectOpencodeCorrect = results.filter(
    (r, idx) => r.correct === true
  ).length - apiCorrectOpencodeIncorrect.length;
  const apiIncorrectOpencodeIncorrect = results.filter(
    (r, idx) => r.correct === false
  ).length - apiIncorrectOpencodeCorrect.length;
  console.log(
    `  API Correct & OpenCode Correct:     ${apiCorrectOpencodeCorrect}`
  );
  console.log(
    `  API Incorrect & OpenCode Incorrect: ${apiIncorrectOpencodeIncorrect}`
  );

  console.log('\nDisagreements:');
  console.log(
    `  API Correct → OpenCode Incorrect: ${apiCorrectOpencodeIncorrect.length}`
  );
  if (apiCorrectOpencodeIncorrect.length > 0) {
    apiCorrectOpencodeIncorrect.forEach(d => {
      console.log(`    Q${d.idx} (${d.question_id}): "${d.question}..."`);
    });
  }

  console.log(
    `  API Incorrect → OpenCode Correct: ${apiIncorrectOpencodeCorrect.length}`
  );
  if (apiIncorrectOpencodeCorrect.length > 0) {
    apiIncorrectOpencodeCorrect.forEach(d => {
      console.log(`    Q${d.idx} (${d.question_id}): "${d.question}..."`);
    });
  }

  if (errors.length > 0) {
    console.log(`\nErrors/Timeouts: ${errors.length}`);
    errors.forEach(e => {
      console.log(`  Q${e.idx} (${e.question_id}): ${e.error}`);
    });
  }

  console.log(`\nRuntime: ${runtime}s`);
  console.log(`\n=== ${gateStatus} ===`);

  process.exit(gateStatus === 'PASS' ? 0 : 1);
}

runCalibration().catch(err => {
  console.error('Fatal error:', err.message);
  process.exit(1);
});
