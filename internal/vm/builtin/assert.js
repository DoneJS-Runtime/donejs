'use strict';
// assert — Node.js assert module.

const util = require('util');

class AssertionError extends Error {
  constructor(options) {
    super(options && options.message);
    this.name = 'AssertionError';
    this.code = 'ERR_ASSERTION';
    if (options) {
      this.actual = options.actual;
      this.expected = options.expected;
      this.operator = options.operator;
      this.generatedMessage = options.generatedMessage;
    }
    if (Error.captureStackTrace) Error.captureStackTrace(this, AssertionError);
  }
}

const assert = module.exports;

function inspectValue(v) { return util.inspect(v, { depth: 3 }); }

assert.AssertionError = AssertionError;
assert.ok = function (value, message) {
  if (!value) {
    throw new AssertionError({
      message: message || 'The expression evaluated to a falsy value:\n\n  ' + inspectValue(value) + '\n',
      actual: value,
      expected: true,
      operator: '==',
      generatedMessage: true,
    });
  }
};
assert.equal = function (actual, expected, message) {
  if (actual != expected) {
    throw new AssertionError({
      message: message || 'Expected values to be loosely equal:\n\n' + inspectValue(actual) + ' != ' + inspectValue(expected) + '\n',
      actual, expected, operator: '==', generatedMessage: true,
    });
  }
};
assert.notEqual = function (actual, expected, message) {
  if (actual == expected) {
    throw new AssertionError({
      message: message || 'Expected values to not be loosely equal:\n\n' + inspectValue(actual) + ' == ' + inspectValue(expected) + '\n',
      actual, expected, operator: '!=', generatedMessage: true,
    });
  }
};
assert.strictEqual = function (actual, expected, message) {
  if (!Object.is(actual, expected)) {
    throw new AssertionError({
      message: message || 'Expected values to be strictly equal:\n\n' + inspectValue(actual) + ' !== ' + inspectValue(expected) + '\n',
      actual, expected, operator: '===', generatedMessage: true,
    });
  }
};
assert.notStrictEqual = function (actual, expected, message) {
  if (Object.is(actual, expected)) {
    throw new AssertionError({
      message: message || 'Expected values to not be strictly equal:\n\n' + inspectValue(actual) + ' === ' + inspectValue(expected) + '\n',
      actual, expected, operator: '!==', generatedMessage: true,
    });
  }
};
assert.deepStrictEqual = function (actual, expected, message) {
  if (!util.isDeepStrictEqual(actual, expected)) {
    throw new AssertionError({
      message: message || 'Expected values to be strictly deep-equal:\n\n' + inspectValue(actual) + ' !== ' + inspectValue(expected) + '\n',
      actual, expected, operator: 'deepStrictEqual', generatedMessage: true,
    });
  }
};
assert.deepEqual = function (actual, expected, message) {
  if (!isLooseDeepEqual(actual, expected)) {
    throw new AssertionError({
      message: message || 'Expected values to be loosely deep-equal:\n\n' + inspectValue(actual) + ' != ' + inspectValue(expected) + '\n',
      actual, expected, operator: 'deepEqual', generatedMessage: true,
    });
  }
};
assert.throws = function (fn, expected, message) {
  if (typeof fn !== 'function') throw new TypeError('assert.throws expects a function');
  let thrown = null;
  try { fn(); } catch (e) { thrown = e; }
  if (!thrown) {
    throw new AssertionError({
      message: message || 'Missing expected exception.',
      actual: undefined, expected, operator: 'throws', generatedMessage: true,
    });
  }
  if (expected && typeof expected === 'function' && !expected(thrown)) {
    throw new AssertionError({
      message: message || 'The validation function is expected to return true',
      actual: thrown, expected, operator: 'throws', generatedMessage: true,
    });
  }
  return thrown;
};
assert.doesNotThrow = function (fn, message) {
  if (typeof fn !== 'function') throw new TypeError('assert.doesNotThrow expects a function');
  try { fn(); } catch (e) {
    throw new AssertionError({
      message: message || 'Got unwanted exception: ' + (e && e.message),
      actual: e, expected: undefined, operator: 'doesNotThrow', generatedMessage: true,
    });
  }
};
assert.rejects = async function (fn, expected, message) {
  let thrown = null;
  try {
    if (typeof fn === 'function') await fn();
    else await fn;
  } catch (e) { thrown = e; }
  if (!thrown) {
    throw new AssertionError({ message: message || 'Missing expected rejection.', operator: 'rejects', generatedMessage: true });
  }
  if (expected && typeof expected === 'function') await expected(thrown);
  else if (expected && expected instanceof RegExp) {
    if (!expected.test(String(thrown && thrown.message))) {
      throw new AssertionError({ message: message || 'The error message does not match', operator: 'rejects', generatedMessage: true });
    }
  }
  return thrown;
};
assert.doesNotReject = async function (fn, message) {
  try {
    if (typeof fn === 'function') await fn();
    else await fn;
  } catch (e) {
    throw new AssertionError({ message: message || 'Got unwanted rejection: ' + (e && e.message), actual: e, operator: 'doesNotReject', generatedMessage: true });
  }
};
assert.match = function (string, regexp, message) {
  if (!regexp.test(string)) {
    throw new AssertionError({ message: message || 'The input did not match the regular expression.', actual: string, expected: regexp, operator: 'match', generatedMessage: true });
  }
};
assert.doesNotMatch = function (string, regexp, message) {
  if (regexp.test(string)) {
    throw new AssertionError({ message: message || 'The input matched the regular expression.', actual: string, expected: regexp, operator: 'doesNotMatch', generatedMessage: true });
  }
};
assert.fail = function (message) {
  throw new AssertionError({ message: message || 'Failed', generatedMessage: false });
};
assert.ifError = function (value) {
  if (value) throw value;
};
assert.strict = assert.strictEqual;
assert.strictEqualStrict = assert.strictEqual;

function isLooseDeepEqual(a, b) {
  if (Object.is(a, b)) return true;
  if (typeof a !== 'object' || typeof b !== 'object' || a === null || b === null) return a == b;
  if (Array.isArray(a) !== Array.isArray(b)) return false;
  const ka = Object.keys(a), kb = Object.keys(b);
  if (ka.length !== kb.length) return false;
  for (const k of ka) {
    if (!Object.prototype.hasOwnProperty.call(b, k)) return false;
    if (!isLooseDeepEqual(a[k], b[k])) return false;
  }
  return true;
}
