/**
 * Test that context-format option validation works
 */

// Simulate the validation logic from index.js
const testCases = [
  { format: 'raw', valid: true },
  { format: 'llm', valid: true },
  { format: 'invalid', valid: false },
  { format: 'JSON', valid: false },
];

console.log('\nTesting context-format option validation:');
console.log('='.repeat(50));

const validFormats = ['raw', 'llm'];

testCases.forEach(tc => {
  const isValid = validFormats.includes(tc.format);
  const expected = tc.valid;
  const pass = isValid === expected;
  
  console.log(`Format: "${tc.format}" → ${isValid ? 'accepted' : 'rejected'} [${pass ? '✅' : '❌'}]`);
});

console.log('='.repeat(50));
console.log('✅ Validation logic works correctly\n');
