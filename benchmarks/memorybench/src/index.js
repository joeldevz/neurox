#!/usr/bin/env node

/**
 * Neurox Memorybench CLI
 * Benchmark harness for Neurox against LongMemEval-S dataset
 * 
 * LLM-powered answer generation and judging for comparability with Mem0, Zep, Supermemory
 */

import { Command } from 'commander';
import fs from 'fs';
import path from 'path';
import { fileURLToPath } from 'url';
import { NeuroxProvider } from './providers/neurox.js';
import { LongMemEvalBenchmark } from './benchmarks/longmemeval.js';
import { runBenchmark, IngestVerificationError } from './runner.js';
import { generateRunId, loadJSON } from './utils.js';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const program = new Command();

program
  .name('neurox-memorybench')
  .description('Benchmark Neurox against LongMemEval-S with LLM-powered judging')
  .version('1.0.0');

/**
 * run command — Execute full benchmark pipeline
 */
program
  .command('run')
  .description('Run benchmark pipeline with LLM answer generation and evaluation')
  .option('-p, --provider <name>', 'Provider: neurox', 'neurox')
  .option('-b, --benchmark <name>', 'Benchmark: longmemeval', 'longmemeval')
  .option('-l, --limit <number>', 'Max questions to run', null)
  .option('-r, --run-id <id>', 'Run ID (auto-generated if omitted)', null)
  .option('--stratified', 'Sample proportionally across question types (default: false)', false)
  .option('--no-ingest', 'Skip ingest phase')
  .option(
    '--context-format <format>',
    'Context format: raw (default) or llm (LLM-ready with metadata)',
    'raw'
  )
  .option(
    '--judge-provider <provider>',
    'Judge provider: anthropic|openai|gateway|opencode|exact|auto',
    'auto'
  )
  .option(
    '--judge-model <model>',
    'Judge model name (e.g. claude-sonnet-4-5, gpt-4o)',
    null
  )
  .option(
    '--answer-provider <provider>',
    'Answer generation provider: anthropic|openai|gateway|opencode (default: anthropic)',
    'anthropic'
  )
  .option(
    '--answer-model <model>',
    'Answer generation model (optional; uses provider default if omitted)',
    null
  )
  .option(
    '--ingest-delay-ms <ms>',
    'Delay in milliseconds between ingest POSTs (default: 50, 0 = no delay)',
    '50'
  )
   .option(
     '--no-temporal-branch',
     'Disable temporal reasoning: gates chronological sort, include_stale, and TEMPORAL REASONING INSTRUCTIONS block. Current date anchor always injected when question_date exists.',
     false
   )
  .option(
    '--skip-ingest-verification',
    'Skip post-ingest persistence verification gate (for debugging)',
    false
  )
   .action(async (options) => {
     try {
       // Validate provider
       if (options.provider !== 'neurox') {
         console.error(`Error: Unknown provider: ${options.provider}`);
         process.exit(1);
       }

       // Validate benchmark
       if (options.benchmark !== 'longmemeval') {
         console.error(`Error: Unknown benchmark: ${options.benchmark}`);
         process.exit(1);
       }

        // Validate judge provider
        const validJudgeProviders = ['anthropic', 'openai', 'gateway', 'opencode', 'exact', 'auto'];
        if (!validJudgeProviders.includes(options.judgeProvider)) {
          console.error(
            `Error: Invalid judge provider: ${options.judgeProvider}. ` +
            `Must be one of: ${validJudgeProviders.join(', ')}`
          );
          process.exit(1);
        }

        // Fail-fast on missing API keys / CLI tools (before any work)
        if (options.judgeProvider === 'anthropic' && !process.env.ANTHROPIC_API_KEY) {
          console.error('Error: Judge provider is "anthropic" but ANTHROPIC_API_KEY is not set');
          process.exit(1);
        }
        if (options.judgeProvider === 'openai' && !process.env.OPENAI_API_KEY) {
          console.error('Error: Judge provider is "openai" but OPENAI_API_KEY is not set');
          process.exit(1);
        }
        if (options.judgeProvider === 'gateway' && !process.env.GATEWAY_API_KEY) {
          console.error('Error: Judge provider is "gateway" but GATEWAY_API_KEY is not set');
          process.exit(1);
        }
        if (options.judgeProvider === 'opencode') {
          const { execSync } = await import('child_process');
          try {
            execSync('opencode --version', { stdio: 'pipe' });
          } catch (e) {
            console.error('Error: Judge provider is "opencode" but opencode CLI is not available');
            console.error('Make sure opencode is installed and in PATH: npm install -g opencode');
            process.exit(1);
          }
        }
         // Validate answer provider
         const validAnswerProviders = ['anthropic', 'openai', 'gateway', 'opencode'];
         if (!validAnswerProviders.includes(options.answerProvider)) {
           console.error(
             `Error: Invalid answer provider: ${options.answerProvider}. ` +
             `Must be one of: ${validAnswerProviders.join(', ')}`
           );
           process.exit(1);
         }

         // Fail-fast on missing API keys for answer provider
         if (options.answerProvider === 'anthropic' && !process.env.ANTHROPIC_API_KEY) {
           console.error('Error: Answer provider is "anthropic" but ANTHROPIC_API_KEY is not set');
           process.exit(1);
         }
         if (options.answerProvider === 'openai' && !process.env.OPENAI_API_KEY) {
           console.error('Error: Answer provider is "openai" but OPENAI_API_KEY is not set');
           process.exit(1);
         }
         if (options.answerProvider === 'gateway' && !process.env.GATEWAY_API_KEY) {
           console.error('Error: Answer provider is "gateway" but GATEWAY_API_KEY is not set');
           process.exit(1);
         }
         if (options.answerProvider === 'opencode') {
           const { execSync } = await import('child_process');
           try {
             execSync('opencode --version', { stdio: 'pipe' });
           } catch (e) {
             console.error('Error: Answer provider is "opencode" but opencode CLI is not available');
             process.exit(1);
           }
         }

         // For judge 'auto': check if any key is available; if not, show prominent warning
         if (options.judgeProvider === 'auto') {
           const hasAnyKey = process.env.ANTHROPIC_API_KEY || process.env.OPENAI_API_KEY || process.env.GATEWAY_API_KEY;
           if (!hasAnyKey) {
             console.warn('\n⚠️  WARNING: Judge provider is "auto" and NO LLM API keys are set.');
             console.warn('⚠️  Will degrade to exact-match (fuzzy string matching).');
             console.warn('⚠️  Results with exact-match are NOT comparable with Mem0, Zep, Supermemory.');
             console.warn('⚠️  To get comparable results, set ANTHROPIC_API_KEY, OPENAI_API_KEY, or GATEWAY_API_KEY\n');
           }
         }

      // Initialize provider
      const provider = new NeuroxProvider(process.env.NEUROX_BASE_URL);
      const health = await provider.health();
      if (!health) {
        console.error(`Error: Neurox not available at ${provider.baseUrl}`);
        console.error('Make sure Neurox is running: neurox serve --http 7438');
        process.exit(1);
      }
      console.log(`✓ Neurox available at ${provider.baseUrl}`);

      // Load benchmark
      const benchmark = new LongMemEvalBenchmark();
      await benchmark.load();

      // Run benchmark
      const runId = options.runId || generateRunId();
      const limit = options.limit ? parseInt(options.limit, 10) : null;
      const stratified = options.stratified === true;
      const noIngest = options.noIngest === true;

      // Validate context format
      const validFormats = ['raw', 'llm'];
      if (!validFormats.includes(options.contextFormat)) {
        console.error(
          `Error: Invalid context format: ${options.contextFormat}. ` +
          `Must be one of: ${validFormats.join(', ')}`
        );
        process.exit(1);
      }

      console.log(`\nStarting benchmark: ${runId}`);
      console.log(`Limit: ${limit || 'all'}`);
      console.log(`Sampling: ${stratified ? 'stratified (proportional)' : 'first-N (sequential)'}`);
      console.log(`Context format: ${options.contextFormat}`);
      console.log(`Judge provider: ${options.judgeProvider}`);
      if (options.judgeModel) console.log(`Judge model: ${options.judgeModel}`);
      if (options.answerModel) console.log(`Answer model: ${options.answerModel}`);

       const ingestDelayMs = parseInt(options.ingestDelayMs, 10);
       const noTemporalBranch = options.noTemporalBranch === true;
       const skipIngestVerification = options.skipIngestVerification === true;
       const report = await runBenchmark({
         provider,
         benchmark,
         runId,
         limit,
         stratified,
         noIngest,
         contextFormat: options.contextFormat,
         judgeProvider: options.judgeProvider,
         judgeModel: options.judgeModel,
         answerProvider: options.answerProvider,
         answerModel: options.answerModel,
         ingestDelayMs,
         noTemporalBranch,
         skipIngestVerification,
         dataDir: path.join(__dirname, '..', 'data'),
       });

       console.log(`\n✓ Benchmark complete!`);
       console.log(`Report: data/runs/${runId}/report.json`);
       console.log(`Accuracy: ${(report.accuracy * 100).toFixed(1)}%`);
       
       if (report.judge_mode.includes('NOT COMPARABLE')) {
         console.warn('\n⚠️  WARNING: Results are NOT comparable with commercial systems.');
         console.warn('⚠️  To get comparable results, set ANTHROPIC_API_KEY or OPENAI_API_KEY');
       }

       process.exitCode = 0;
       return;
     } catch (err) {
       // IngestVerificationError is a controlled abort — no stack trace needed
       if (err instanceof IngestVerificationError) {
         process.exitCode = 1;
         return;
       }
       
       console.error(`Error: ${err.message}`);
       if (process.env.DEBUG) {
         console.error(err.stack);
       }
       process.exitCode = 1;
       return;
     }
  });

/**
 * status command — Check run status
 */
program
  .command('status')
  .description('Check run status')
  .option('-r, --run-id <id>', 'Run ID', null)
  .action((options) => {
    try {
      if (!options.runId) {
        console.error('Error: --run-id is required');
        process.exit(1);
      }

      const checkpointPath = path.join(__dirname, '..', 'data', 'runs', options.runId, 'checkpoint.json');
      if (!fs.existsSync(checkpointPath)) {
        console.error(`Error: Run not found: ${options.runId}`);
        process.exit(1);
      }

      const checkpoint = loadJSON(checkpointPath);
      console.log(`\nRun ID: ${options.runId}`);
      console.log(`Phase: ${checkpoint.phase}`);
      console.log(`Ingested: ${checkpoint.ingested || 0}`);
      console.log(`Searched: ${checkpoint.searched || 0}`);
      console.log(`Answered: ${checkpoint.answered || 0}`);
      console.log(`Evaluated: ${checkpoint.evaluated || 0}`);

      if (checkpoint.phase === 'complete' && checkpoint.results) {
        const correct = checkpoint.results.filter((r) => r.correct).length;
        const accuracy = (correct / checkpoint.results.length * 100).toFixed(1);
        console.log(`Accuracy: ${correct}/${checkpoint.results.length} (${accuracy}%)`);
      }

      if (checkpoint.errors.length > 0) {
        console.log(`\nErrors:`);
        for (const err of checkpoint.errors) {
          console.log(`  [${err.phase}] ${err.error}`);
        }
      }

      process.exit(0);
    } catch (err) {
      console.error(`Error: ${err.message}`);
      process.exit(1);
    }
  });

/**
 * clear command — Delete all observations in a namespace
 */
program
  .command('clear')
  .description('Clear namespace')
  .option('-n, --namespace <name>', 'Namespace to clear', null)
  .action(async (options) => {
    try {
      if (!options.namespace) {
        console.error('Error: --namespace is required');
        process.exit(1);
      }

      const provider = new NeuroxProvider(process.env.NEUROX_BASE_URL);
      const health = await provider.health();
      if (!health) {
        console.error(`Error: Neurox not available`);
        process.exit(1);
      }

      console.log(`Clearing namespace: ${options.namespace}`);
      await provider.clear(options.namespace);
      console.log('✓ Cleared');

      process.exit(0);
    } catch (err) {
      console.error(`Error: ${err.message}`);
      process.exit(1);
    }
  });

program.parse(process.argv);

// Show help if no command
if (!process.argv.slice(2).length) {
  program.outputHelp();
}
