/**
 * Test LLM context formatting output
 */
import { NeuroxProvider } from './src/providers/neurox.js';

console.log('\nTesting LLM context formatting:');
console.log('='.repeat(70));

const provider = new NeuroxProvider();

// Mock search results
const mockResults = [
  {
    id: 'obs-1',
    title: 'Test Memory 1',
    content: 'This is the content of the first observation',
    rank: 1,
    kind: 'episodic',
    observation_type: 'discovery',
    confidence: 0.95,
    tags: ['important', 'verified'],
    staleness: 'fresh'
  },
  {
    id: 'obs-2',
    title: 'Test Memory 2',
    content: 'Second observation with different details',
    rank: 2,
    kind: 'semantic',
    observation_type: 'pattern',
    confidence: 0.87,
    tags: ['pattern', 'reusable'],
    staleness: 'aging'
  }
];

// Test LLM formatting
const llmOutput = provider._formatAsLLMContext(mockResults);

console.log('\nLLM Format Output:');
console.log('-'.repeat(70));
console.log(llmOutput);
console.log('-'.repeat(70));

// Verify required fields are present
const checks = [
  { name: 'Rank markers', check: llmOutput.includes('Rank #') },
  { name: 'Kind field', check: llmOutput.includes('Kind:') },
  { name: 'Confidence field', check: llmOutput.includes('Confidence:') },
  { name: 'Staleness field', check: llmOutput.includes('Staleness:') },
  { name: 'Title field', check: llmOutput.includes('**Title**:') },
  { name: 'Tags field', check: llmOutput.includes('**Tags**:') },
  { name: 'Observation Type', check: llmOutput.includes('**Observation Type**:') },
  { name: 'Content field', check: llmOutput.includes('**Content**:') },
  { name: 'NO raw score', check: !llmOutput.includes('r.score') && !llmOutput.includes('score:') },
  { name: 'NO layer field', check: !llmOutput.includes('layer:') && !llmOutput.includes('r.layer') },
  { name: 'Proper formatting', check: llmOutput.includes('Retrieved Memories') }
];

console.log('\nField Verification:');
const passed = checks.filter(c => c.check).length;
checks.forEach(c => {
  console.log(`  ${c.check ? '✅' : '❌'} ${c.name}`);
});

console.log('\n' + '='.repeat(70));
console.log(`Result: ${passed}/${checks.length} format checks passed`);
if (passed === checks.length) {
  console.log('✅ LLM context formatting is correct');
} else {
  console.log('❌ LLM context formatting has issues');
  process.exit(1);
}
console.log('='.repeat(70) + '\n');
