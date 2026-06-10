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
import { runBenchmark } from './runner.js';
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
    'Judge provider: anthropic|openai|gateway|exact|auto',
    'auto'
  )
  .option(
    '--judge-model <model>',
    'Judge model name (e.g. claude-sonnet-4-5, gpt-4o)',
    null
  )
  .option(
    '--answer-model <model>',
    'Answer generation model (same provider as judge)',
    null
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
      const validJudgeProviders = ['anthropic', 'openai', 'gateway', 'exact', 'auto'];
      if (!validJudgeProviders.includes(options.judgeProvider)) {
        console.error(
          `Error: Invalid judge provider: ${options.judgeProvider}. ` +
          `Must be one of: ${validJudgeProviders.join(', ')}`
        );
        process.exit(1);
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
        answerModel: options.answerModel,
        dataDir: path.join(__dirname, '..', 'data'),
      });

      console.log(`\n✓ Benchmark complete!`);
      console.log(`Report: data/runs/${runId}/report.json`);
      console.log(`Accuracy: ${(report.accuracy * 100).toFixed(1)}%`);
      
      if (report.judge_mode.includes('NOT COMPARABLE')) {
        console.warn('\n⚠️  WARNING: Results are NOT comparable with commercial systems.');
        console.warn('⚠️  To get comparable results, set ANTHROPIC_API_KEY or OPENAI_API_KEY');
      }

      process.exit(0);
    } catch (err) {
      console.error(`Error: ${err.message}`);
      if (process.env.DEBUG) {
        console.error(err.stack);
      }
      process.exit(1);
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
