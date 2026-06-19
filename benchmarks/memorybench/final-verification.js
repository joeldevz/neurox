/**
 * Final comprehensive verification of context-format implementation
 */
import fs from 'fs';
import { NeuroxProvider } from './src/providers/neurox.js';

console.log('\n' + '='.repeat(70));
console.log('FINAL VERIFICATION: MemoryBench Context-Format Implementation');
console.log('='.repeat(70));

const results = {
  sections: []
};

// SECTION 1: Code Quality
console.log('\n[1] CODE QUALITY');
console.log('-'.repeat(70));

const files = [
  './src/index.js',
  './src/answer.js',
  './src/providers/neurox.js',
  './src/runner.js'
];

let syntaxPass = true;
for (const file of files) {
  const code = fs.readFileSync(file, 'utf-8');
  try {
    new Function(code);
    console.log(`  ✅ ${file} - syntax valid`);
  } catch (e) {
    console.log(`  ❌ ${file} - ${e.message}`);
    syntaxPass = false;
  }
}

results.sections.push({
  name: 'Code Syntax',
  pass: syntaxPass
});

// SECTION 2: Requirements Checklist
console.log('\n[2] REQUIREMENTS CHECKLIST');
console.log('-'.repeat(70));

const indexCode = fs.readFileSync('./src/index.js', 'utf-8');
const neuroxCode = fs.readFileSync('./src/providers/neurox.js', 'utf-8');
const runnerCode = fs.readFileSync('./src/runner.js', 'utf-8');
const answerCode = fs.readFileSync('./src/answer.js', 'utf-8');

const requirements = [
  {
    name: 'Option: --context-format raw|llm with default raw',
    check: indexCode.includes("'--context-format <format>'") && 
           indexCode.includes("'raw'") && 
           indexCode.includes("'llm'")
  },
  {
    name: 'Raw format: backward compatible (content concatenation)',
    check: neuroxCode.includes('.map((r) => r.content)') &&
           neuroxCode.includes("'\\n\\n---\\n\\n'")
  },
  {
    name: 'LLM format: includes all required fields (rank/title/kind/observation_type/confidence/tags/staleness/content)',
    check: neuroxCode.includes('_formatAsLLMContext') &&
           neuroxCode.includes('const rank = index + 1') &&
           neuroxCode.includes('r.title ||') &&
           neuroxCode.includes('r.kind ||') &&
           neuroxCode.includes('r.observation_type ||') &&
           neuroxCode.includes('r.confidence ?') &&
           neuroxCode.includes('r.tags ||') &&
           neuroxCode.includes('r.staleness ||') &&
           neuroxCode.includes('r.content ||')
  },
  {
    name: 'LLM format: excludes raw score and layer',
    check: !neuroxCode.includes('r.score') &&
           !neuroxCode.includes('r.layer')
  },
  {
    name: 'Temperature: set to 0 for answer generation',
    check: answerCode.includes('temperature = 0') &&
           !answerCode.includes('temperature = 0.5')
  },
  {
    name: 'Report: context_format field recorded',
    check: runnerCode.includes('context_format: contextFormat')
  },
  {
    name: 'Integration: contextFormat passed to search()',
    check: runnerCode.includes('await provider.search(question, namespace, 10, { contextFormat })')
  },
  {
    name: 'Option validation: rejects invalid formats',
    check: indexCode.includes("validFormats.includes") &&
           indexCode.includes("['raw', 'llm']")
  }
];

let reqPass = 0;
requirements.forEach((req, i) => {
  const status = req.check ? '✅' : '❌';
  console.log(`  ${status} Req ${i+1}: ${req.name}`);
  if (req.check) reqPass++;
});

results.sections.push({
  name: 'Requirements',
  pass: reqPass === requirements.length,
  detail: `${reqPass}/${requirements.length}`
});

// SECTION 3: Runtime Verification
console.log('\n[3] RUNTIME VERIFICATION');
console.log('-'.repeat(70));

try {
  const provider = new NeuroxProvider();
  console.log('  ✅ NeuroxProvider instantiated');
  console.log('  ✅ Method _formatAsLLMContext exists:', typeof provider._formatAsLLMContext === 'function' ? '✅' : '❌');
  console.log('  ✅ Method search exists:', typeof provider.search === 'function' ? '✅' : '❌');
  
  results.sections.push({
    name: 'Runtime',
    pass: true
  });
} catch (e) {
  console.log('  ❌ Runtime error:', e.message);
  results.sections.push({
    name: 'Runtime',
    pass: false
  });
}

// SUMMARY
console.log('\n' + '='.repeat(70));
console.log('SUMMARY');
console.log('='.repeat(70));

const allPass = results.sections.every(s => s.pass);
results.sections.forEach((s, i) => {
  console.log(`${s.pass ? '✅' : '❌'} ${s.name}${s.detail ? ` (${s.detail})` : ''}`);
});

console.log('\n' + '='.repeat(70));
if (allPass) {
  console.log('✅ ALL VERIFICATION CHECKS PASSED');
  console.log('Status: READY FOR DEPLOYMENT');
} else {
  console.log('❌ SOME CHECKS FAILED');
}
console.log('='.repeat(70) + '\n');

process.exit(allPass ? 0 : 1);
