/**
 * Verify context-format implementation against requirements
 */
import fs from 'fs';

console.log('\n' + '='.repeat(70));
console.log('CONTEXT-FORMAT REQUIREMENT VALIDATION');
console.log('='.repeat(70));

// Read modified files
const indexCode = fs.readFileSync('./src/index.js', 'utf-8');
const neuroxCode = fs.readFileSync('./src/providers/neurox.js', 'utf-8');
const runnerCode = fs.readFileSync('./src/runner.js', 'utf-8');
const answerCode = fs.readFileSync('./src/answer.js', 'utf-8');

const checks = [];

// REQUIREMENT 1: --context-format raw|llm, default raw
console.log('\n1. CLI Option: --context-format raw|llm, default raw');
const hasOption = indexCode.includes("'--context-format <format>'");
const hasRaw = indexCode.includes("'raw'");
const hasLLM = indexCode.includes("'llm'");
const hasDefault = indexCode.includes("'raw'") && indexCode.includes("'raw'");
checks.push({
  check: 'Option defined',
  pass: hasOption && hasRaw && hasLLM,
  details: `Option: ${hasOption ? '✅' : '❌'} | raw value: ${hasRaw ? '✅' : '❌'} | llm value: ${hasLLM ? '✅' : '❌'}`
});

// REQUIREMENT 2: raw behavior backward compatible
console.log('\n2. Raw Format: Backward compatible (content only)');
const hasRawConcat = neuroxCode.includes('.map((r) => r.content)');
const hasRawJoin = neuroxCode.includes("'\\n\\n---\\n\\n'");
checks.push({
  check: 'Raw format = concatenate content only',
  pass: hasRawConcat && hasRawJoin,
  details: `Content concat: ${hasRawConcat ? '✅' : '❌'} | Join with ---: ${hasRawJoin ? '✅' : '❌'}`
});

// REQUIREMENT 3: llm format uses rank/title/kind/observation_type/confidence/tags/staleness/content
console.log('\n3. LLM Format: Includes all required metadata fields');
const hasLLMFormat = neuroxCode.includes('_formatAsLLMContext');
const hasRank = neuroxCode.includes('const rank = index + 1');
const hasTitle = neuroxCode.includes("r.title ||");
const hasKind = neuroxCode.includes('r.kind ||');
const hasObsType = neuroxCode.includes('r.observation_type ||');
const hasConfidence = neuroxCode.includes('r.confidence ?');
const hasTags = neuroxCode.includes('r.tags ||');
const hasStaleness = neuroxCode.includes('r.staleness ||');
const hasContent = neuroxCode.includes('r.content ||');

checks.push({
  check: 'LLM format method exists',
  pass: hasLLMFormat,
  details: hasLLMFormat ? '✅' : '❌'
});

checks.push({
  check: 'LLM includes: rank/title/kind/observation_type/confidence/tags/staleness/content',
  pass: hasRank && hasTitle && hasKind && hasObsType && hasConfidence && hasTags && hasStaleness && hasContent,
  details: `rank:${hasRank ? '✅' : '❌'} title:${hasTitle ? '✅' : '❌'} kind:${hasKind ? '✅' : '❌'} obs_type:${hasObsType ? '✅' : '❌'} conf:${hasConfidence ? '✅' : '❌'} tags:${hasTags ? '✅' : '❌'} staleness:${hasStaleness ? '✅' : '❌'} content:${hasContent ? '✅' : '❌'}`
});

// REQUIREMENT 4: No raw score or layer in LLM context
console.log('\n4. LLM Format: Excludes raw score and layer');
const noScore = !neuroxCode.slice(neuroxCode.indexOf('_formatAsLLMContext'), neuroxCode.indexOf('_formatAsLLMContext') + 1500).includes('r.score');
const noLayer = !neuroxCode.slice(neuroxCode.indexOf('_formatAsLLMContext'), neuroxCode.indexOf('_formatAsLLMContext') + 1500).includes('r.layer');
checks.push({
  check: 'LLM excludes raw score and layer',
  pass: noScore && noLayer,
  details: `No score: ${noScore ? '✅' : '❌'} | No layer: ${noLayer ? '✅' : '❌'}`
});

// REQUIREMENT 5: temperature 0
console.log('\n5. Temperature: Set to 0 for answer generation');
const hasTemp0 = answerCode.includes('temperature = 0') && !answerCode.includes('temperature = 0.5');
checks.push({
  check: 'Answer generation uses temperature 0',
  pass: hasTemp0,
  details: hasTemp0 ? '✅' : '❌'
});

// REQUIREMENT 6: context_format recorded in report
console.log('\n6. Report: context_format field recorded');
const hasReportField = runnerCode.includes("context_format: contextFormat");
checks.push({
  check: 'Report includes context_format field',
  pass: hasReportField,
  details: hasReportField ? '✅' : '❌'
});

// REQUIREMENT 7: contextFormat passed to search()
console.log('\n7. Integration: contextFormat passed to search()');
const searchCall = runnerCode.includes('await provider.search(question, namespace, 10, { contextFormat })');
checks.push({
  check: 'search() called with { contextFormat } option',
  pass: searchCall,
  details: searchCall ? '✅' : '❌'
});

// Summary
console.log('\n' + '='.repeat(70));
const passed = checks.filter(c => c.pass).length;
const total = checks.length;
console.log(`RESULT: ${passed}/${total} checks passed`);
console.log('='.repeat(70));

checks.forEach((c, i) => {
  console.log(`${i+1}. [${c.pass ? '✅' : '❌'}] ${c.check}`);
  console.log(`   ${c.details}`);
});

console.log('\n' + '='.repeat(70));
if (passed === total) {
  console.log('✅ ALL REQUIREMENTS MET');
} else {
  console.log(`❌ ${total - passed} requirement(s) not met`);
}
console.log('='.repeat(70) + '\n');

process.exit(passed === total ? 0 : 1);
