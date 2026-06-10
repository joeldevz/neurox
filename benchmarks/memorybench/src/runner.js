/**
 * Benchmark Pipeline Runner
 * Orchestrates 6 phases: ingest → wait → search → answer → evaluate → report
 * Per-question namespacing for LongMemEval protocol
 */

import fs from 'fs';
import path from 'path';
import fetch from 'node-fetch';
import { createJudge } from './judge.js';
import { generateAnswer } from './answer.js';

/**
 * Wait for observations to be indexed in namespace
 * Polls /search endpoint with a generic query until count >= expected or timeout
 */
async function waitForIndexing(provider, namespace, expectedCount, timeoutMs = 60000) {
  const pollInterval = 1000; // 1 second polling
  const startTime = Date.now();
  let lastCount = 0;
  
  // Initial delay to allow first batch consolidation
  await new Promise((resolve) => setTimeout(resolve, 2000));
  
  while (Date.now() - startTime < timeoutMs) {
    try {
      // Use a generic query term to avoid wildcard issues
      const params = new URLSearchParams({
        q: 'session',
        namespace,
        limit: '1000',
      });
      const res = await fetch(`${provider.baseUrl}/api/v1/observations/search?${params}`);
      
      if (res.ok) {
        const data = await res.json();
        const count = data.results?.length || 0;
        
        if (count > lastCount) {
          lastCount = count;
          process.stdout.write(`[${count}/${expectedCount}]`);
        }
        
        if (count >= expectedCount) {
          console.log(` ✓`);
          return true;
        }
      }
    } catch (err) {
      // Silently retry
    }
    
    await new Promise((resolve) => setTimeout(resolve, pollInterval));
  }
  
  console.log(` ⚠ (got ${lastCount}/${expectedCount})`);
  return false;
}

/**
 * Wait for embeddings to be generated in namespace
 * Polls /search endpoint with debug=true and checks if any result has semantic_score > 0
 * Falls back to a fixed delay if debug mode isn't available or takes too long
 * 
 * Strategy: Polling with debug=true for semantic_score detection (1.5s interval)
 * Reason: Allows dynamic detection of when embeddings are ready without hardcoding delays
 * Fallback: Math.min(60000, numObs * 400) ms for safety if polling fails
 * 
 * @param {Object} provider - Provider with baseUrl
 * @param {string} namespace - Namespace to check
 * @param {string} sampleQuery - Query to use for debug polling (e.g., the question)
 * @param {number} numObs - Number of observations in namespace
 * @param {number} timeoutMs - Maximum wait time
 * @returns {Promise<boolean>} true if embeddings detected, false on timeout
 */
async function waitForEmbeddings(provider, namespace, sampleQuery, numObs, timeoutMs = 60000) {
  const pollInterval = 1500; // 1.5 second polling
  const startTime = Date.now();
  let pollAttempts = 0;
  
  // Try polling with debug=true for semantic_score detection
  while (Date.now() - startTime < timeoutMs) {
    try {
      const params = new URLSearchParams({
        q: sampleQuery,
        namespace,
        limit: '5',
        debug: 'true',
      });
      const res = await fetch(`${provider.baseUrl}/api/v1/observations/search?${params}`);
      
      if (res.ok) {
        const data = await res.json();
        const results = data.results || [];
        
        // Check if any result has semantic_score > 0
        const hasSemanticScore = results.some(r => 
          r.score_breakdown?.semantic_score > 0
        );
        
        if (hasSemanticScore) {
          process.stdout.write(`✓ embeddings detected`);
          return true;
        }
        
        pollAttempts++;
        if (pollAttempts % 4 === 0) {
          process.stdout.write('.');
        }
      }
    } catch (err) {
      // Silently continue polling
    }
    
    await new Promise((resolve) => setTimeout(resolve, pollInterval));
  }
  
  // Polling timeout — fall back to fixed delay
  console.log(`\n    ⚠ embedding detection timed out, using fixed delay fallback`);
  const fallbackDelayMs = Math.min(60000, Math.max(5000, numObs * 400));
  console.log(`    Waiting ${fallbackDelayMs}ms for async embeddings...`);
  await new Promise((resolve) => setTimeout(resolve, fallbackDelayMs));
  
  return false;
}

const PHASES = ['ingest', 'wait', 'search', 'answer', 'evaluate', 'report'];

/**
 * Load or create checkpoint
 */
function loadCheckpoint(checkpointPath) {
  if (fs.existsSync(checkpointPath)) {
    const content = fs.readFileSync(checkpointPath, 'utf-8');
    return JSON.parse(content);
  }
  return {
    phase: 'ingest',
    phase_index: 0,
    ingested: 0,
    searched: 0,
    answered: 0,
    evaluated: 0,
    results: [],
    errors: [],
    timestamps: {},
  };
}

/**
 * Save checkpoint
 */
function saveCheckpoint(checkpointPath, state) {
  const dir = path.dirname(checkpointPath);
  if (!fs.existsSync(dir)) {
    fs.mkdirSync(dir, { recursive: true });
  }
  fs.writeFileSync(checkpointPath, JSON.stringify(state, null, 2));
}

/**
 * Latency statistics
 */
function computeStats(latencies) {
  if (latencies.length === 0) return { avg: 0, p50: 0, p95: 0, p99: 0 };
  const sorted = latencies.slice().sort((a, b) => a - b);
  const avg = latencies.reduce((a, b) => a + b, 0) / latencies.length;
  const p50 = sorted[Math.floor(sorted.length * 0.5)];
  const p95 = sorted[Math.floor(sorted.length * 0.95)];
  const p99 = sorted[Math.floor(sorted.length * 0.99)];
  return { avg, p50, p95, p99 };
}

/**
 * Main benchmark runner
 */
export async function runBenchmark(options = {}) {
  const {
    provider,
    benchmark,
    runId = `run-${Date.now()}`,
    limit = null,
    stratified = false,
    noIngest = false,
    contextFormat = 'raw',
    judgeProvider = 'auto',
    judgeModel = null,
    answerModel = null,
    dataDir = './data',
  } = options;

  const runDir = path.join(dataDir, 'runs', runId);
  const checkpointPath = path.join(runDir, 'checkpoint.json');
  const reportPath = path.join(runDir, 'report.json');

  // Load checkpoint
  let checkpoint = loadCheckpoint(checkpointPath);
  console.log(`Starting benchmark. Phase: ${checkpoint.phase}`);

  try {
    // Phase 0: Load benchmark
    if (checkpoint.phase_index === 0) {
      console.log('\n[ingest] Loading benchmark...');
      const questions = benchmark.getQuestions(limit, stratified);
      checkpoint.total_questions = questions.length;
      checkpoint.questions = questions;
      const distribution = benchmark.getTypeDistribution(limit, stratified);
      console.log(
        `Loaded ${questions.length} questions, distribution: ${JSON.stringify(distribution)}`
      );
      if (stratified) {
        console.log('Sampling: stratified (proportional across categories)');
      }
      saveCheckpoint(checkpointPath, checkpoint);
    }

    // Phase 1: Per-Question Ingest & Wait
    // LongMemEval protocol: each question gets its own namespace with only its haystack
    if (checkpoint.phase_index <= 0 && !noIngest) {
      console.log('\n[ingest] Ingesting per-question haystacks into Neurox...');
      checkpoint.phase = 'ingest';
      checkpoint.timestamps.ingest_start = Date.now();

      let totalIngested = 0;
      
      for (let i = 0; i < checkpoint.questions.length; i++) {
        const q = checkpoint.questions[i];
        const sessions = q.haystack_sessions || [];
        
        // Per-question namespace
        const questionNamespace = `${runId}-q${i}`;
        
        // Initialize session for this question's namespace
        await provider.initialize(questionNamespace);

        // Ingest ONLY this question's haystack sessions
        const obsIds = await provider.ingest(sessions, questionNamespace);
        
        totalIngested += obsIds.length;
        
        // Store mapping for later search
        checkpoint.results.push({
          question_id: q.question_id,
          question: q.question,
          answer: q.answer,
          question_type: q.question_type,
          namespace: questionNamespace,
          haystack_count: sessions.length,
          observation_ids: obsIds,
          context: null,
          predicted: null,
          correct: null,
          latency_ms: 0,
          error: null,
        });

        if ((i + 1) % 10 === 0) {
          console.log(`  Ingested ${i + 1}/${checkpoint.questions.length} questions`);
        }
      }

      checkpoint.ingested = totalIngested;
      checkpoint.phase_index = 1;
      checkpoint.timestamps.ingest_end = Date.now();
      console.log(`Ingested ${totalIngested} session observations across ${checkpoint.questions.length} question namespaces`);
      saveCheckpoint(checkpointPath, checkpoint);
    }

    // Phase 2: Wait for indexing per question
    if (checkpoint.phase_index <= 1) {
      console.log('\n[wait] Waiting for FTS5 indexing per question...');
      checkpoint.phase = 'wait';
      checkpoint.timestamps.wait_start = Date.now();
      
      for (let i = 0; i < checkpoint.results.length; i++) {
        const result = checkpoint.results[i];
        const expectedCount = result.haystack_count;
        
        process.stdout.write(`  Q${i}: `);
        await waitForIndexing(provider, result.namespace, expectedCount, 60000);
      }
      
      console.log('\n[wait] Waiting for async embeddings per question...');
      for (let i = 0; i < checkpoint.results.length; i++) {
        const result = checkpoint.results[i];
        process.stdout.write(`  Q${i}: `);
        await waitForEmbeddings(
          provider,
          result.namespace,
          result.question,  // Use the question itself as sample query
          result.haystack_count,
          60000
        );
        console.log('');  // Newline after embeddings wait
      }
      
      checkpoint.phase_index = 2;
      checkpoint.timestamps.wait_end = Date.now();
      console.log('[wait] FTS5 + embeddings ready');
      saveCheckpoint(checkpointPath, checkpoint);
    }

    // Phase 3: Search in per-question namespaces
    if (checkpoint.phase_index <= 2) {
      console.log('\n[search] Searching for context in per-question namespaces...');
      checkpoint.phase = 'search';
      checkpoint.timestamps.search_start = Date.now();

      const latencies = [];

      for (let i = 0; i < checkpoint.results.length; i++) {
         const result = checkpoint.results[i];
         const question = result.question;
         const namespace = result.namespace; // Use question-specific namespace

         const t0 = Date.now();
         const context = await provider.search(question, namespace, 10, { contextFormat }); // top-10 from this question's haystack
         const latency = Date.now() - t0;

         result.context = context;
         result.latency_ms = latency;
         latencies.push(latency);

        if ((i + 1) % 10 === 0) {
          console.log(`  Searched ${i + 1}/${checkpoint.results.length} questions`);
        }
      }

      checkpoint.searched = checkpoint.results.length;
      checkpoint.latency_stats = computeStats(latencies);
      checkpoint.phase_index = 3;
      checkpoint.timestamps.search_end = Date.now();
      console.log(
        `Search latency: avg ${checkpoint.latency_stats.avg.toFixed(0)}ms, ` +
        `p50 ${checkpoint.latency_stats.p50.toFixed(0)}ms, ` +
        `p95 ${checkpoint.latency_stats.p95.toFixed(0)}ms`
      );
      saveCheckpoint(checkpointPath, checkpoint);
    }

    // Phase 4: Answer Generation (LLM-based)
    if (checkpoint.phase_index <= 3) {
      console.log('\n[answer] Generating answers with LLM...');
      checkpoint.phase = 'answer';
      checkpoint.timestamps.answer_start = Date.now();

      let answeredCount = 0;
      
      // Determine the answer provider (for LLM, not judge)
      // If judge is "exact", we still need anthropic for answer generation
      let answerProvider = judgeProvider === 'auto' || judgeProvider === 'exact' ? 'anthropic' : judgeProvider;

      for (let i = 0; i < checkpoint.results.length; i++) {
        const result = checkpoint.results[i];
        const context = result.context || '';

        try {
           // Generate answer using LLM (or fallback to context extraction)
           const answer = await generateAnswer(result.question, context, {
             provider: answerProvider,
             model: answerModel,
             temperature: 0,
           });

          result.predicted = answer;
          answeredCount++;
        } catch (err) {
          console.warn(`  Answer generation failed for Q${i}: ${err.message}`);
          // Fallback: extract first non-empty line from context
          if (context.length > 0) {
            const lines = context.split('\n').filter(l => l.trim().length > 10);
            result.predicted = lines[0]?.substring(0, 200) || 'No context available';
          } else {
            result.predicted = 'No context available';
          }
        }

        if ((i + 1) % 10 === 0) {
          console.log(`  Generated ${i + 1}/${checkpoint.results.length} answers`);
        }
      }

      checkpoint.answered = answeredCount;
      checkpoint.phase_index = 4;
      checkpoint.timestamps.answer_end = Date.now();
      console.log(`Generated answers for ${answeredCount}/${checkpoint.results.length} questions`);
      saveCheckpoint(checkpointPath, checkpoint);
    }

    // Phase 5: Evaluate
    if (checkpoint.phase_index <= 4) {
      console.log('\n[evaluate] Evaluating answers...');
      checkpoint.phase = 'evaluate';
      checkpoint.timestamps.eval_start = Date.now();

      const judge = createJudge(judgeProvider, { model: judgeModel });
      let evalCount = 0;

      for (const result of checkpoint.results) {
        const evaluation = await judge(result.predicted, result.answer, result.question);
        result.correct = evaluation.correct;
        result.judge_reason = evaluation.reason;

        evalCount++;
        if (evalCount % 10 === 0) {
          console.log(`  Evaluated ${evalCount}/${checkpoint.results.length}`);
        }
      }

      checkpoint.evaluated = evalCount;
      checkpoint.phase_index = 5;
      checkpoint.timestamps.eval_end = Date.now();
      saveCheckpoint(checkpointPath, checkpoint);
    }

    // Phase 6: Report
    if (checkpoint.phase_index <= 5) {
      console.log('\n[report] Generating report...');
      checkpoint.phase = 'report';
      checkpoint.timestamps.report_start = Date.now();

      // Compute metrics
      const accuracy = checkpoint.results.filter((r) => r.correct).length / checkpoint.results.length;
      const accuracyByType = {};
      const countByType = {};

      for (const result of checkpoint.results) {
        const type = result.question_type;
        countByType[type] = (countByType[type] || 0) + 1;
        accuracyByType[type] = (accuracyByType[type] || 0) + (result.correct ? 1 : 0);
      }

      const report = {
        run_id: runId,
        benchmark: 'LongMemEval-S',
        provider: 'Neurox',
        provider_url: provider.baseUrl,
        context_format: contextFormat,
        judge_provider: judgeProvider,
        judge_mode: judgeProvider === 'exact' ? 'exact-match ⚠️  NOT COMPARABLE' : `llm (${judgeProvider})`,
        total_questions: checkpoint.results.length,
        correct: checkpoint.results.filter((r) => r.correct).length,
        accuracy: accuracy,
        accuracy_by_type: {},
        latency_stats: checkpoint.latency_stats,
        total_tokens: checkpoint.results.reduce((sum, r) => sum + (r.context?.length || 0), 0) / 4, // approx tokens
        duration_ms: checkpoint.timestamps.report_start - checkpoint.timestamps.ingest_start,
        results: checkpoint.results,
        ingested: checkpoint.ingested,
        searched: checkpoint.searched,
        answered: checkpoint.answered,
        evaluated: checkpoint.evaluated,
      };

      // Compute accuracy by type
      for (const type in countByType) {
        const correct = accuracyByType[type];
        const total = countByType[type];
        report.accuracy_by_type[type] = {
          correct,
          total,
          accuracy: total > 0 ? correct / total : 0,
        };
      }

      checkpoint.timestamps.report_end = Date.now();

      // Write report
      if (!fs.existsSync(runDir)) {
        fs.mkdirSync(runDir, { recursive: true });
      }
      fs.writeFileSync(reportPath, JSON.stringify(report, null, 2));

      // Print summary
      printReport(report);

      checkpoint.phase = 'complete';
      saveCheckpoint(checkpointPath, checkpoint);

      console.log(`\nReport saved to: ${reportPath}`);
      return report;
    }
  } catch (err) {
    checkpoint.errors.push({
      phase: checkpoint.phase,
      timestamp: Date.now(),
      error: err.message,
    });
    saveCheckpoint(checkpointPath, checkpoint);
    throw err;
  }
}

/**
 * Print human-readable report
 */
function printReport(report) {
  console.log('\n' + '='.repeat(60));
  console.log('NEUROX MEMORYBENCH REPORT');
  console.log('='.repeat(60));
  console.log(`Run ID:    ${report.run_id}`);
  console.log(`Benchmark: ${report.benchmark}`);
  console.log(`Provider:  ${report.provider} (${report.provider_url})`);
  console.log(`Judge:     ${report.judge_mode}`);
  console.log(`Questions: ${report.total_questions}`);

  console.log('\nACCURACY BY TYPE:');
  const typeLabels = Object.keys(report.accuracy_by_type).sort();
  for (const type of typeLabels) {
    const { correct, total, accuracy } = report.accuracy_by_type[type];
    const pct = (accuracy * 100).toFixed(1);
    console.log(`  ${type.padEnd(25)} ${correct}/${total}  (${pct}%)`);
  }

  const overallPct = (report.accuracy * 100).toFixed(1);
  console.log(`  ${'─'.repeat(50)}`);
  console.log(`  ${'OVERALL'.padEnd(25)} ${report.correct}/${report.total_questions}  (${overallPct}%)`);

  console.log('\nLATENCY:');
  console.log(
    `  avg search: ${report.latency_stats.avg.toFixed(0)}ms  ` +
    `p50: ${report.latency_stats.p50.toFixed(0)}ms  ` +
    `p95: ${report.latency_stats.p95.toFixed(0)}ms`
  );

  console.log(`\nMemScore: ${overallPct}% / ${report.latency_stats.avg.toFixed(0)}ms / ${Math.round(report.total_tokens)}tok`);
  console.log('='.repeat(60) + '\n');
}
