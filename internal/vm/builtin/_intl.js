'use strict';
// _intl — Intl backed by CLDR locale tables.
//
// The tables live in internal/vm/intl_data.bin (one gzip compressed JSON record
// per CLDR locale plus a "$supp" entry) and are reached through the native
// "__intl" module, which also evaluates CLDR plural rules and collation.
//
// ponytail: the 'hebrew', 'chinese', 'dangi' and 'iso8601' calendars fall back to
// Gregorian field values (not listed here because they are accepted as
// calendars but not yet implemented); the currencyDisplay 'name' renders the ISO
// code, Intl.DisplayNames / Intl.Segmenter are absent.  Upgrade path: add the
// missing calendars to tools/genintl.py and delete the fallbacks marked below.

const intl = require('__intl');

// ---------------------------------------------------------------------------
// locale data
// ---------------------------------------------------------------------------

let SUPP = null;
function supp() {
  if (SUPP === null) SUPP = repairTree(JSON.parse(intl.sync('supplemental', [])));
  return SUPP;
}

// CLDR JSON can arrive double encoded (UTF-8 bytes read as Latin-1); restore
// the intended characters before any of them are used as text.
function repairCldr(s) {
  if (typeof s !== 'string' || s.length === 0) return s;
  let needs = false;
  for (let i = 0; i < s.length; i++) {
    if (s.charCodeAt(i) > 0x7f) { needs = true; break; }
  }
  if (!needs) return s;
  let out = '';
  for (let i = 0; i < s.length;) {
    const b = s.charCodeAt(i);
    if (b < 0x80) { out += String.fromCharCode(b); i++; continue; }
    if (b < 0xc0 || b > 0xf4) return s;
    const len = b < 0xe0 ? 2 : b < 0xf0 ? 3 : 4;
    if (i + len > s.length) return s;
    let cp = b & (0xff >> len);
    for (let j = 1; j < len; j++) {
      const c = s.charCodeAt(i + j);
      if (c < 0x80 || c > 0xbf) return s;
      cp = (cp << 6) | (c & 0x3f);
    }
    if (cp > 0x10ffff) return s;
    out += String.fromCodePoint(cp);
    i += len;
  }
  return out;
}

function repairTree(v) {
  if (typeof v === 'string') return repairCldr(v);
  if (Array.isArray(v)) {
    for (let i = 0; i < v.length; i++) v[i] = repairTree(v[i]);
    return v;
  }
  if (v && typeof v === 'object') {
    for (const k in v) v[k] = repairTree(v[k]);
    return v;
  }
  return v;
}

const RECORDS = new Map();
function record(tag) {
  let rec = RECORDS.get(tag);
  if (rec === undefined) {
    const raw = intl.sync('locale', [tag]);
    rec = raw ? repairTree(JSON.parse(raw)) : null;
    RECORDS.set(tag, rec);
  }
  return rec;
}

let ROOT = null;
function rootNum() {
  if (ROOT === null) {
    const rec = record('und');
    ROOT = (rec && rec.n) || {};
  }
  return ROOT;
}

const NUM_DEFAULT = {
  dec: '.', grp: ',', pct: '%', min: '-', pls: '+', pm: '\u2030',
  inf: '\u221e', nan: 'NaN', exp: 'E', sup: '', tsep: ':', mg: 0,
  sys: 'latn', dsp: '#,##0.###', psp: '#,##0%', csp: '\u00a4#,##0.00',
};

function nsym(b, key) {
  const v = b.n[key];
  if (v !== undefined && v !== null && v !== '') return v;
  const r = rootNum()[key];
  return (r === undefined || r === null || r === '') ? NUM_DEFAULT[key] : r;
}

function digitsFor(sys) {
  const table = supp()._digits;
  const text = table && table[sys];
  return text ? text : '0123456789';
}

function regionOf(tag) {
  const parts = tag.split('-');
  let i = 1;
  if (i < parts.length && parts[i].length === 4) i++;
  if (i >= parts.length) return '';
  const p = parts[i];
  return p.length === 2 ? p.toUpperCase() : '';
}

const BUNDLES = new Map();
function lookupChain(tag) {
  const out = [];
  let t = tag;
  while (t) {
    out.push(t);
    const i = t.lastIndexOf('-');
    if (i <= 0) break;
    t = t.slice(0, i);
  }
  return out;
}

function bundle(tag) {
  let b = BUNDLES.get(tag);
  if (b) return b;
  let rec = null;
  const chain = icuChain(tag);
  for (let i = 0; i < chain.length; i++) {
    const found = record(chain[i]);
    if (found) { rec = found; break; }
  }
  if (!rec) rec = record('en') || {};
  b = {
    tag,
    lang: tag.split('-')[0].toLowerCase(),
    region: regionOf(tag),
    n: rec.n || {},
    d: rec.d || {},
    l: rec.l || {},
    r: rec.r || {},
    cs: rec.cs || {},
    csL: rec.csL || '',
    cn: rec.cn || {},
    u: rec.u || {},
  };
  BUNDLES.set(tag, b);
  return b;
}

// The CLDR plural operands of the rendered digits.
function operandsOf(intText, fracText) {
  const trimmed = fracText.replace(/0+$/, '');
  return {
    n: Number(intText + (fracText ? '.' + fracText : '')),
    i: parseInt(intText, 10) || 0,
    v: fracText.length,
    w: trimmed.length,
    f: fracText ? (parseInt(fracText, 10) || 0) : 0,
    t: trimmed ? (parseInt(trimmed, 10) || 0) : 0,
    e: 0,
  };
}

// The cardinal plural category of |value| in |b|'s language.
function pluralOf(b, value) {
  return pluralOps(b.lang, operands(value));
}

function pluralOfLang(lang, value) {
  const o = operands(value);
  return intl.sync('plural', [lang, o.n, o.i, o.v, o.w, o.f, o.t, o.e, 'cardinal']);
}

function pluralOps(lang, ops) {
  return intl.sync('plural', [lang, ops.n, ops.i, ops.v, ops.w,
    ops.f, ops.t, ops.e, 'cardinal']);
}

// --- tags ------------------------------------------------------------------
// --- tags ------------------------------------------------------------------

function canonical(raw) {
  const text = String(raw);
  // Node's IsStructurallyValidLanguageTag rejects '_' separators.
  if (text.indexOf('_') >= 0 || text === '') return null;
  return intl.sync('canon', [text]) || null;
}

function canonicalizeList(locales) {
  const list = locales === undefined || locales === null ? []
    : (Array.isArray(locales) ? locales : [locales]);
  const out = [];
  for (let i = 0; i < list.length; i++) {
    const raw = list[i];
    if (raw === undefined || raw === null) continue;
    const tag = canonical(raw);
    if (tag === null) throw new RangeError('Invalid language tag: ' + raw);
    out.push(tag);
  }
  return out;
}

function supportedOf(locales) {
  let list;
  try { list = canonicalizeList(locales); } catch (e) { return []; }
  return list.filter((tag) => icuChain(tag).length > 0);
}

// The locales ICU ships data for.  A requested locale outside this set falls
// back to the default locale, exactly like Node (ve/lld/ts/... -> en-US).
const ICU_LOCALES = new Set(supp()._icuLocales || []);
const DEFAULT_LOCALE = 'en-US';

// The ICU lookup chain of |tag|: the tag and its parents, keeping only the
// ones ICU actually has data for.
function icuChain(tag) {
  const out = [];
  let t = tag;
  while (t) {
    if (ICU_LOCALES.has(t)) out.push(t);
    const i = t.lastIndexOf('-');
    if (i <= 0) break;
    t = t.slice(0, i);
  }
  return out;
}

// The locale ICU resolves |tag| to.
function icuLocale(tag) {
  if (!tag) return DEFAULT_LOCALE;
  let t = tag;
  while (t) {
    if (ICU_LOCALES.has(t)) return t;
    const i = t.lastIndexOf('-');
    if (i <= 0) break;
    t = t.slice(0, i);
  }
  return DEFAULT_LOCALE;
}

// ---------------------------------------------------------------------------
// small helpers
// ---------------------------------------------------------------------------

function pad2(value) {
  return value < 10 ? '0' + value : String(value);
}

function pad(value, width) {
  let text = String(value);
  while (text.length < width) text = '0' + text;
  return text;
}

function toDate(value) {
  if (value === undefined) return new Date();
  if (value instanceof Date) return value;
  return new Date(value);
}

function isDigit(ch) { return ch >= '0' && ch <= '9'; }

function isAlnum(ch) {
  return isDigit(ch) || (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z');
}

// ---------------------------------------------------------------------------
// option validation
//
// V8 and ICU share their error strings and the order in which the options are
// read, so the error thrown below depends on this sequence and not on the
// order of the properties in the options object.
// ---------------------------------------------------------------------------

const DTF_CTOR = 'Intl.DateTimeFormat';
const NF_CTOR = 'Intl.NumberFormat';

function throwRange(message) { throw new RangeError(message); }

// V8's error messages stringify option values its own way: an object renders as
// "#<Object>", an array as "[object Array]"; scalars render through String().
function optionValueText(value) {
  if (Array.isArray(value)) return '[object Array]';
  if (value !== null && typeof value === 'object') return '#<Object>';
  return String(value);
}

// GetOption with an enumerated allowed list.
function enumOption(value, name, allowed, ctor) {
  if (value === undefined) return undefined;
  const text = optionValueText(value);
  if (allowed.indexOf(text) < 0) {
    throwRange('Value ' + text + ' out of range for ' + ctor + ' options property ' + name);
  }
  return text;
}

// GetNumberOption: ToNumber, range check against the untruncated value, truncate.
function numberOption(value, name, min, max) {
  if (value === undefined) return undefined;
  const n = Number(value);
  if (!Number.isFinite(n) || n < min || n > max) {
    throwRange(name + ' value is out of range.');
  }
  return Math.trunc(n);
}

// A "3-8 alphanumeric" list, the shape V8's Is38AlphaNumList accepts.
const TAG_LIST_RE = /^[0-9A-Za-z]{3,8}(-[0-9A-Za-z]{3,8})*$/;

// The numbering system option: the syntax is checked here, the lookup of a
// known system happens in resolveNumberingSystem.
function numberingSystemOption(value) {
  if (value === undefined) return undefined;
  const text = String(value);
  if (!TAG_LIST_RE.test(text)) {
    throwRange('Invalid numberingSystem : ' + text);
  }
  return text;
}

// An unknown but well formed numbering system falls back to the locale's.
function resolveNumberingSystem(b, value) {
  const fallback = nsym(b, 'sys') || 'latn';
  const text = numberingSystemOption(value);
  if (text === undefined) return fallback;
  const known = supp()._numericSystems || [];
  if (known.indexOf(text) < 0) return fallback;
  return text;
}

// ICU's calendar keyword table (lowercased, with its own canonical spelling).
const CALENDAR_ALIASES = {
  buddhist: 'buddhist', chinese: 'chinese', coptic: 'coptic', dangi: 'dangi',
  ethiopic: 'ethiopic', ethioaa: 'ethioaa', 'ethiopic-amete-alem': 'ethioaa',
  gregory: 'gregory', hebrew: 'hebrew', indian: 'indian', islamic: 'islamic',
  'islamic-civil': 'islamic-civil', 'islamic-rgsa': 'islamic-rgsa',
  'islamic-tbla': 'islamic-tbla', 'islamic-umalqura': 'islamic-umalqura',
  iso8601: 'iso8601', japanese: 'japanese', persian: 'persian', roc: 'roc',
};

function resolveCalendar(b, value) {
  const fallback = b.d.cal || 'gregory';
  if (value === undefined) return fallback;
  const text = String(value);
  if (!TAG_LIST_RE.test(text)) throwRange('Invalid calendar : ' + text);
  const found = CALENDAR_ALIASES[text.toLowerCase()];
  return found === undefined ? fallback : found;
}

// Time zones: the IANA names, the UTC aliases and fixed offsets come from the
// Go side, which also supplies the offsets used to render them.
function resolveTimeZone(value) {
  const raw = value === undefined ? intl.sync('localZone', []) : String(value);
  // ICU canonicalizes the legacy spellings (US/Pacific -> America/Los_Angeles).
  const aliases = supp()._zoneAliases || {};
  const mapped = aliases[raw] === undefined ? raw : aliases[raw];
  const id = intl.sync('timeZone', [mapped]);
  if (id === null || id === '') throwRange('Invalid time zone specified: ' + raw);
  return id;
}

// Minutes the zone is behind UTC at |ms|, matching Date#getTimezoneOffset.
function zoneOffsetMinutes(id, ms) {
  if (id === 'UTC') return 0;
  return -Math.round(intl.sync('zoneOffset', [id, ms]) / 60);
}

// The sanctioned single unit identifiers; compounds are A-per-B of these.
const SINGLE_UNITS = ['acre', 'bit', 'byte', 'celsius', 'centimeter', 'day',
  'degree', 'fahrenheit', 'fluid-ounce', 'foot', 'gallon', 'gigabit', 'gigabyte',
  'gram', 'hectare', 'hour', 'inch', 'kilobit', 'kilobyte', 'kilogram',
  'kilometer', 'liter', 'meter', 'microsecond', 'mile', 'mile-scandinavian',
  'milliliter', 'millimeter', 'millisecond', 'minute', 'month', 'nanosecond',
  'ounce', 'percent', 'petabyte', 'pound', 'second', 'stone', 'terabit',
  'terabyte', 'week', 'yard', 'year'];
const SINGLE_UNIT_SET = new Set(SINGLE_UNITS);

function checkUnit(text) {
  const parts = String(text).split('-per-');
  const valid = parts.length === 1
    ? SINGLE_UNIT_SET.has(parts[0])
    : parts.length === 2 && SINGLE_UNIT_SET.has(parts[0]) && SINGLE_UNIT_SET.has(parts[1]);
  if (!valid) throwRange("Invalid unit argument for Intl.NumberFormat() '" + text + "'");
}

const ROUNDING_INCREMENTS = [1, 2, 5, 10, 20, 25, 50, 100, 200, 250, 500, 1000,
  2000, 2500, 5000];

// GetStringOrBooleanOption: a boolean maps to "always"/false, the listed
// strings map to themselves and anything else falsy becomes false.
function useGroupingOption(value, compact) {
  if (value === undefined) return compact ? 'min2' : 'auto';
  if (typeof value === 'boolean') return value ? 'always' : false;
  const text = optionValueText(value);
  if (text === 'min2' || text === 'auto' || text === 'always') return text;
  if (!value) return false;
  throwRange('Value ' + text + ' out of range for ' + NF_CTOR
    + ' options property useGrouping');
}

function roundingIncrementOption(value) {
  if (value === undefined) return 1;
  const n = numberOption(value, 'roundingIncrement', 1, 5000);
  if (ROUNDING_INCREMENTS.indexOf(n) < 0) {
    throwRange('roundingIncrement value is out of range.');
  }
  return n;
}

// expand "1.23e+21" into plain digits; the fixed-point formatter switches to
// exponential past 1e21 and the group/decimal split needs a plain string.
function expandExp(text) {
  const m = /^(-?)(\d)(?:\.(\d+))?e([+-]\d+)$/.exec(text);
  if (!m) return text;
  const sign = m[1];
  const all = m[2] + (m[3] || '');
  const exp = parseInt(m[4], 10);
  if (exp >= all.length) {
    let out = all;
    for (let i = all.length; i <= exp; i++) out += '0';
    return sign + out;
  }
  if (exp >= 0) return sign + all.slice(0, exp + 1) + '.' + all.slice(exp + 1);
  let out = '0.';
  for (let i = 0; i < -exp - 1; i++) out += '0';
  return sign + out + all;
}



// ---------------------------------------------------------------------------
// number formatting
// ---------------------------------------------------------------------------

function affixTokens(text) {
  const out = [];
  let literal = '';
  function flush() {
    if (literal) { out.push({ t: 'literal', v: literal }); literal = ''; }
  }
  for (let i = 0; i < text.length; i++) {
    const ch = text.charAt(i);
    if (ch === "'") {
      if (text.charAt(i + 1) === "'") { literal += "'"; i++; continue; }
      let j = i + 1;
      while (j < text.length && text.charAt(j) !== "'") j++;
      literal += text.slice(i + 1, j);
      i = j;
      continue;
    }
    if (ch === '%' || ch === '\u2030' || ch === '\u00a4' || ch === '+' || ch === '-') {
      flush();
      out.push({
        t: ch === '%' ? 'percentSign' : ch === '\u2030' ? 'permilleSign'
          : ch === '\u00a4' ? 'currency' : ch === '+' ? 'plusSign' : 'minusSign',
        v: ch,
      });
      continue;
    }
    literal += ch;
  }
  flush();
  return out;
}

function parseSection(text) {
  let i = 0;
  while (i < text.length && text.charAt(i) !== '#' && text.charAt(i) !== '0') i++;
  let j = i;
  while (j < text.length && '#0,.'.indexOf(text.charAt(j)) >= 0) j++;
  const num = text.slice(i, j);
  const dot = num.indexOf('.');
  const intPart = dot < 0 ? num : num.slice(0, dot);
  const fracPart = dot < 0 ? '' : num.slice(dot + 1);
  const groups = intPart.split(',');
  let sizes = null;
  if (groups.length > 1) {
    const primary = groups[groups.length - 1].length;
    const secondary = groups.length > 2 ? groups[groups.length - 2].length : primary;
    sizes = [primary, secondary];
  }
  let minInt = 0;
  for (let k = 0; k < intPart.length; k++) if (intPart.charAt(k) === '0') minInt++;
  return {
    prefix: affixTokens(text.slice(0, i)),
    suffix: affixTokens(text.slice(j)),
    sizes,
    minInt,
    minFrac: fracPart.replace(/[^0]/g, '').length,
    maxFrac: fracPart.length,
  };
}

function parseNumPattern(text) {
  const semi = text.indexOf(';');
  return {
    pos: parseSection(semi < 0 ? text : text.slice(0, semi)),
    neg: semi < 0 ? null : parseSection(text.slice(semi + 1)),
  };
}

// Group the integer digits with the pattern's group sizes.  Grouping is
// suppressed when fewer than `mg` digits sit in front of the first separator
// (ICU's minimumGroupingDigits), or when `min2` asks for two.
function groupDigitsWith(text, sizes, mode, mg, sep) {
  if (!sizes || mode === 'off') return text;
  const n = text.length;
  const threshold = mode === 'min2' ? 2 : mode === 'always' ? 0 : mg;
  if (n - sizes[0] < threshold) return text;
  const chunks = [];
  let end = n;
  let size = sizes[0];
  while (end > 0 && size > 0) {
    const start = Math.max(0, end - size);
    chunks.unshift(text.slice(start, end));
    end = start;
    size = sizes[1];
  }
  return chunks.join(sep);
}

function numberSymbols(b) {
  return {
    dec: nsym(b, 'dec'),
    grp: nsym(b, 'grp'),
    pct: nsym(b, 'pct'),
    min: nsym(b, 'min'),
    pls: nsym(b, 'pls'),
    pm: nsym(b, 'pm'),
    inf: nsym(b, 'inf'),
    nan: nsym(b, 'nan'),
    exp: nsym(b, 'exp'),
    sup: nsym(b, 'sup'),
    mg: parseInt(nsym(b, 'mg'), 10) || 0,
    sys: nsym(b, 'sys') || 'latn',
    dsp: nsym(b, 'dsp'),
    psp: nsym(b, 'psp'),
    csp: nsym(b, 'csp'),
    cspAcc: nsym(b, 'cspAcc'),
    cp: nsym(b, 'cp'),
    cspB: nsym(b, 'cspB'),
    cspA: nsym(b, 'cspA'),
  };
}

// Currency symbols are stored per locale as [standard, narrow]; entries the
// locale does not override live under $supp._currencySymbols.
function currencyEntry(b, code) {
  const local = (b.cs || {})[code];
  const root = supp()._currencySymbols || {};
  const base = root[code];
  if (!Array.isArray(base)) return Array.isArray(local) ? local : null;
  if (!Array.isArray(local)) return base;
  // The locale may name the currency without overriding its symbol; fall back
  // to the root symbol (and any missing name) slot by slot.
  const merged = base.slice();
  for (let i = 0; i < local.length && i < merged.length; i++) {
    if (local[i] !== null && local[i] !== undefined && local[i] !== '') merged[i] = local[i];
  }
  return merged;
}

function currencySymbol(b, code, display) {
  if (!code) return '';
  // ponytail: 'name' renders the ISO code instead of the localized name.
  if (display === 'code' || display === 'name') return code;
  const entry = currencyEntry(b, code);
  if (!entry) return code;
  if (display === 'narrowSymbol') return entry[1] || entry[0] || code;
  // ICU's wide symbol is the CLDR `symbol`; a locale without one serves the ISO
  // code (never the narrow symbol).
  return entry[0] || code;
}

// A per-currency CLDR pattern override, if any (legacy currencies like TRY in
// Turkish or EUR in en-150 keep their own layout).
function currencyPattern(b, code) {
  const entry = currencyEntry(b, code);
  return entry && entry[5] ? entry[5] : null;
}

// The localized name of |code|, pluralized by |cat|.  ICU's chain is
// CurrencyPlurals/<cat> -> CurrencyPlurals/other -> long name -> ISO code.
// A locale that carries currency data but not this code falls straight to the
// ISO code; a locale with no currency data at all falls back to root (English).
function currencyName(b, code, cat) {
  const entry = currencyEntry(b, code);
  if (!entry || entry.length < 3) return code;
  const extra = entry[4];
  if (cat !== 'one' && cat !== 'other' && extra && extra[cat]) return extra[cat];
  if (cat === 'one' && entry[3]) return entry[3];
  return entry[2] || code;
}

// The CLDR currency unit pattern: "{0}" is the number, "{1}" the name and
// "{S}" the symbol.  Only a few locales deviate from "{0} {1}".
function currencyUnitPattern(tag, cat) {
  const table = supp()._currencyPatterns || {};
  const chain = lookupChain(tag);
  for (let i = 0; i < chain.length; i++) {
    const entry = table[chain[i]];
    if (!entry) continue;
    return entry[cat] || entry.other || '{0} {1}';
  }
  return '{0} {1}';
}

// Splits a pattern into its literal runs and the {0} placeholder.
function applyTemplate(parts, pattern) {
  const out = [];
  let lit = '';
  let i = 0;
  while (i < pattern.length) {
    if (pattern.charAt(i) === '{' && pattern.charAt(i + 1) === '0') {
      if (lit) { out.push({ type: 'literal', value: lit }); lit = ''; }
      out.push.apply(out, parts);
      i += 3;
      continue;
    }
    lit += pattern.charAt(i);
    i++;
  }
  if (lit) out.push({ type: 'literal', value: lit });
  return out;
}

// The unit pattern of |f.unit| for |value|, pluralized.
function unitPattern(f, value, cat) {
  const pats = f.unitPatterns;
  if (!pats) return null;
  const style = f.unitDisplay === 'long' ? 'l'
    : f.unitDisplay === 'narrow' ? 'n' : 's';
  let pat = pats[style] || pats.s || pats.l || pats.n;
  if (!pat) return null;
  if (typeof pat !== 'string') {
    pat = pat[cat] || pat.other;
  }
  return pat || null;
}

// The currency long name: the number and the localized currency name, laid out
// by the CLDR currency unit pattern ("{0} {1}", "{1} {0}", "{S} {0} {1}", ...).
function applyCurrencyName(parts, f, cat) {
  const pattern = currencyUnitPattern(f.tag, cat);
  const name = currencyName(f.b, f.currencyCode, cat);
  const symbol = currencySymbol(f.b, f.currencyCode, 'symbol');
  const out = [];
  let lit = '';
  let i = 0;
  while (i < pattern.length) {
    const ch = pattern.charAt(i);
    if (ch === '{') {
      const end = pattern.indexOf('}', i);
      const tag = end < 0 ? '' : pattern.slice(i + 1, end);
      if (tag === '0' || tag === '1' || tag === 'S') {
        if (lit) { out.push({ type: 'literal', value: lit }); lit = ''; }
        if (tag === '0') out.push.apply(out, parts);
        else if (tag === '1') out.push({ type: 'currency', value: name });
        else out.push({ type: 'currency', value: symbol });
        i = end + 1;
        continue;
      }
    }
    lit += ch;
    i++;
  }
  if (lit) out.push({ type: 'literal', value: lit });
  return out;
}

// The base-10 exponent of the leading digit of |value|.
function leadingExponent(value) {
  if (!value) return 0;
  let text = String(Math.abs(value));
  if (text.indexOf('e') >= 0 || text.indexOf('E') >= 0) text = expandExp(text);
  const dot = text.indexOf('.');
  const intPart = dot < 0 ? text : text.slice(0, dot);
  return intPart.length - 1;
}

// The base-10 exponent of the leading digit of a rendered number.
function leadingExponentOf(intText, fracText) {
  if (intText !== '0') return intText.length - 1;
  if (!fracText) return 0;
  const zeros = fracText.length - fracText.replace(/^0+/, '').length;
  return -zeros - 1;
}

// ICU's compact multiplier for the magnitude |m|: the divisor is 10^m and the
// pattern carries the zeros of the compacted integer.  ICU counts only the
// leading zeros, and clamps the magnitude to the largest entry.
function compactMultiplier(cp, m) {
  if (m < 0) return 0;
  let max = -1;
  for (let i = 0; i < cp.length; i++) if (cp[i][0] > max) max = cp[i][0];
  if (m > max) m = max;
  for (let i = 0; i < cp.length; i++) {
    if (cp[i][0] !== m) continue;
    const entry = cp[i][1];
    const text = typeof entry === 'string' ? entry : (entry.other || '');
    if (!text || text === '0') return 0;
    let zeros = 0;
    while (zeros < text.length && text.charAt(zeros) === '0') zeros++;
    return zeros > 0 ? zeros - m - 1 : 0;
  }
  return 0;
}

// The compact pattern of the magnitude |m| for the plural |cat|.  A magnitude
// past the largest entry is clamped (ICU does the same).
function compactPattern(cp, m, cat) {
  let max = -1;
  for (let i = 0; i < cp.length; i++) if (cp[i][0] > max) max = cp[i][0];
  if (m > max) m = max;
  let entry = null;
  for (let i = 0; i < cp.length; i++) if (cp[i][0] === m) { entry = cp[i][1]; break; }
  if (entry === null) return null;
  if (typeof entry === 'string') return entry === '0' ? null : entry;
  let pat = entry[cat];
  if (pat === undefined) pat = entry.other;
  if (pat === undefined || pat === '0') return null;
  return pat;
}

// The literal of a compact pattern ("0K" gives "K"), with the quotes removed,
// and the number of integer digits the pattern asks for.
function compactAffix(pattern) {
  let out = '';
  let i = 0;
  while (i < pattern.length && pattern.charAt(i) === '0') i++;
  const minInt = i;
  while (i < pattern.length) {
    const ch = pattern.charAt(i);
    if (ch === "'") {
      if (pattern.charAt(i + 1) === "'") { out += "'"; i += 2; continue; }
      let j = i + 1;
      while (j < pattern.length && pattern.charAt(j) !== "'") j++;
      out += pattern.slice(i + 1, j);
      i = j + 1;
      continue;
    }
    if (ch === '0') { i++; continue; }
    out += ch;
    i++;
  }
  return { literal: out, minInt };
}

// The compact digits of |value|: ICU rounds with the coarser of the fraction
// and the significant precision ("morePrecision") when no digit option was
// given, and with the requested precision otherwise.
function compactDigits(value, f, negative) {
  if (f.roundingType === 'morePrecision' || f.roundingType === 'lessPrecision') {
    return precisionCombined(value, f, negative);
  }
  if (f.roundingType === 'significantDigits') {
    return precisionParts(value, f.sig[0], f.sig[1], f.roundingMode, negative);
  }
  return fixedDigits(value, f, negative);
}

// The integer and fraction digits of |value| rounded to the fraction
// precision, trimmed down to the minimum fraction digits.
function fixedDigits(value, f, negative) {
  const fixed = fixedParts(value, f.maxFrac, f.roundingMode, negative);
  let fracText = fixed[1];
  while (fracText.length > f.minFrac && fracText.charAt(fracText.length - 1) === '0') {
    fracText = fracText.slice(0, fracText.length - 1);
  }
  while (fracText.length < f.minFrac) fracText += '0';
  return [fixed[0], fracText];
}

// ICU's combined fraction/significant rounding ("roundingPriority"):
// the rounding magnitude is the min (morePrecision) or max (lessPrecision)
// of the two precisions, and the displayed fraction digits follow the
// matching side.
function precisionCombined(value, f, negative) {
  const m = leadingExponent(value);
  const roundMag1 = -f.maxFrac;
  const roundMag2 = m - f.sig[1] + 1;
  const roundMag = f.roundingType === 'lessPrecision'
    ? Math.max(roundMag1, roundMag2) : Math.min(roundMag1, roundMag2);
  const keep = Math.max(1, m - roundMag + 1);
  const parts = precisionParts(value, 1, keep, f.roundingMode, negative);
  const after = leadingExponentOf(parts[0], parts[1]);
  const displayMag1 = f.minFrac === 0 ? 2147483647 : -f.minFrac;
  const displayMag2 = after - f.sig[0] + 1;
  let displayMag;
  if (f.roundingType === 'lessPrecision') {
    displayMag = roundMag2 <= roundMag1 ? displayMag1 : displayMag2;
  } else {
    displayMag = roundMag2 <= roundMag1 ? displayMag2 : displayMag1;
  }
  const minFrac = Math.max(0, -displayMag);
  let fracText = parts[1];
  while (fracText.length > minFrac && fracText.charAt(fracText.length - 1) === '0') {
    fracText = fracText.slice(0, fracText.length - 1);
  }
  while (fracText.length < minFrac) fracText += '0';
  return [parts[0], fracText];
}

// The compact rendering ("1.2K") of |scaled|, following ICU's algorithm: the
// divisor comes from the magnitude, and a rounding that pushes the value into
// the next magnitude retries with the next divisor.
function compactFormat(scaled, f, negative) {
  const cp = f.compact;
  if (!cp) return null;
  const mag = leadingExponent(scaled);
  let mult = compactMultiplier(cp, mag);
  let value = scaled * Math.pow(10, mult);
  let digits = compactDigits(value, f, negative);
  if (mult !== 0
      && leadingExponentOf(digits[0], digits[1]) !== mag + mult) {
    const mult2 = compactMultiplier(cp, mag + 1);
    if (mult2 !== mult) {
      mult = mult2;
      value = scaled * Math.pow(10, mult);
      digits = compactDigits(value, f, negative);
    }
  }
  const mag2 = leadingExponentOf(digits[0], digits[1]);
  const cat = pluralOf(f.b, Number(digits[0] + (digits[1] ? '.' + digits[1] : '')));
  const pattern = compactPattern(cp, mag2 - mult, cat);
  if (pattern === null) {
    // No compact pattern: the plain number is kept, rounded with the compact
    // precision (ICU reuses the default modifier in that case).
    return { intText: digits[0], fracText: digits[1], literal: '', noCompact: true };
  }
  const affix = compactAffix(pattern);
  return {
    intText: digits[0],
    fracText: digits[1],
    literal: affix.literal,
    minInt: affix.minInt,
  };
}

// Renders |value| as parts.  `f` is a resolved number format:
// { pattern, scale, sym, digits, grouping, minInt, minFrac, maxFrac, sign,
//   currency, currencyDisplay, spacingB, spacingA }.
// True when the rendered digits are all zero (after rounding), so signDisplay
// 'exceptZero' suppresses the sign.
function displayedZero(parts) {
  let numeric = false;
  for (let i = 0; i < parts.length; i++) {
    const t = parts[i].type;
    if (t === 'integer' || t === 'fraction') {
      numeric = true;
      if (parts[i].value !== '0') return false;
    } else if (t === 'nan' || t === 'infinity') {
      return false;
    }
  }
  return numeric;
}

function numberParts(value, f) {
  const num = Number(value);
  const pos = f.pattern.pos;
  if (Number.isNaN(num)) {
    return affixParts(pos.prefix, f).concat([{ type: 'nan', value: f.sym.nan }],
      affixParts(pos.suffix, f));
  }
  const negative = num < 0 || (num === 0 && 1 / num < 0);
  const infinite = !Number.isFinite(num);
  if (f.sign === 'never') {
    // keep the magnitude
  }
  const sign = negative && f.sign !== 'never';
  const section = (negative && f.pattern.neg) ? f.pattern.neg : pos;
  const body = [];
  let intText = '0';
  let fracText = '';
  if (!infinite) {
    let scaled = Math.abs(num) * f.scale;
    if (f.roundingIncrement !== 1) {
      scaled = roundIncrement(scaled, f.roundingIncrement, f.roundingMode,
        negative);
    }
    let exponent = null;
    let minInt = f.minInt;
    const compact = f.notation === 'compact'
      ? compactFormat(scaled, f, negative) : null;
    if (compact) {
      intText = compact.intText;
      fracText = compact.fracText;
      if (compact.minInt > minInt) minInt = compact.minInt;
    } else if (f.notation === 'scientific' || f.notation === 'engineering') {
      if (scaled === 0) {
        intText = '0';
        fracText = '';
      } else {
        let exp = Math.floor(Math.log10(scaled));
        if (f.notation === 'engineering') exp -= ((exp % 3) + 3) % 3;
        const fixed = fixedParts(scaled / Math.pow(10, exp), f.maxFrac,
          f.roundingMode, negative);
        intText = fixed[0];
        fracText = fixed[1];
        if (intText.length > 1) {
          // Rounding pushed the mantissa to 10; carry into the exponent.
          exp += intText.length - 1;
          intText = intText.charAt(0);
        }
        exponent = exp;
      }
    } else if (f.sig && (f.roundingType === 'morePrecision'
        || f.roundingType === 'lessPrecision')) {
      const parts = precisionCombined(scaled, f, negative);
      intText = parts[0];
      fracText = parts[1];
    } else if (f.sig) {
      const parts = precisionParts(scaled, f.sig[0], f.sig[1], f.roundingMode,
        negative);
      intText = parts[0];
      fracText = parts[1];
    } else {
      const parts = fixedDigits(scaled, f, negative);
      intText = parts[0];
      fracText = parts[1];
    }
    while (intText.length < minInt) intText = '0' + intText;
    const digits = f.digits;
    if (exponent === null) {
      const grouped = groupDigitsWith(intText, f.sizes, f.grouping, f.sym.mg, f.sym.grp);
      if (f.sizes && f.grouping !== 'off' && grouped !== intText) {
        // re-emit so that group separators are their own part
        const sep = f.sym.grp;
        const pieces = grouped.split(sep);
        for (let i = 0; i < pieces.length; i++) {
          if (i) body.push({ type: 'group', value: sep });
          body.push({ type: 'integer', value: toLocalDigits(pieces[i], digits) });
        }
      } else {
        body.push({ type: 'integer', value: toLocalDigits(grouped, digits) });
      }
    } else {
      body.push({ type: 'integer', value: toLocalDigits(intText, digits) });
    }
    if (fracText) {
      if (f.sym.dec) body.push({ type: 'decimal', value: f.sym.dec });
      body.push({ type: 'fraction', value: toLocalDigits(fracText, digits) });
    }
    if (compact && compact.literal) {
      body.push({ type: 'compact', value: compact.literal });
    }
    if (exponent !== null) {
      body.push({ type: 'exponentSeparator', value: f.sym.exp });
      if (exponent < 0) body.push({ type: 'exponentMinusSign', value: f.sym.min });
      body.push({ type: 'exponentInteger', value: toLocalDigits(String(Math.abs(exponent)), digits) });
    }
  } else {
    body.push({ type: 'infinity', value: f.sym.inf });
  }
  const out = [];
  // An explicit negative section is the whole negative rendering (ICU's
  // accounting patterns are "¤#,##0.00;(¤#,##0.00)").
  const folded = section !== pos;
  if (sign && !folded) out.push({ type: 'minusSign', value: f.sym.min });
  out.push.apply(out, affixParts(section.prefix, f));
  out.push.apply(out, body);
  out.push.apply(out, affixParts(section.suffix, f));
  const parts = applyCurrencySpacing(out, f);
  parts.digits = infinite ? null : [intText, fracText];
  return parts;
}

function toLocalDigits(text, digits) {
  let out = '';
  for (let i = 0; i < text.length; i++) {
    const ch = text.charAt(i);
    out += isDigit(ch) ? digits.charAt(ch.charCodeAt(0) - 48) : ch;
  }
  return out;
}

// Rounds the digit string |digits| so that only |cut| digits are kept, using
// the given rounding mode.  The magnitude is rounded, |neg| tells whether the
// value is negative (the signed modes are not symmetric).
function roundDigits(digits, cut, mode, neg) {
  const kept = digits.slice(0, cut);
  const rest = digits.slice(cut);
  if (!rest) return kept;
  const first = rest.charAt(0);
  const tail = rest.slice(1).replace(/0+$/, '');
  const more = first !== '0' || tail.length > 0;
  const aboveHalf = first > '5' || (first === '5' && tail.length > 0);
  const half = first === '5' && tail.length === 0;
  let up = false;
  switch (mode) {
    case 'ceil': up = neg ? false : more; break;
    case 'floor': up = neg ? more : false; break;
    case 'expand': up = more; break;
    case 'trunc': up = false; break;
    case 'halfCeil': up = aboveHalf || (half && !neg); break;
    case 'halfFloor': up = aboveHalf || (half && neg); break;
    case 'halfExpand': up = aboveHalf || half; break;
    case 'halfTrunc': up = aboveHalf; break;
    case 'halfEven': {
      if (aboveHalf) up = true;
      else if (half) {
        const last = kept.length ? kept.charAt(kept.length - 1) : '0';
        up = Number(last) % 2 === 1;
      }
      break;
    }
    default: up = aboveHalf || half; break;
  }
  if (!up) return kept;
  const chars = kept.split('');
  let i = chars.length - 1;
  while (i >= 0 && chars[i] === '9') { chars[i] = '0'; i--; }
  if (i < 0) chars.unshift('1');
  else chars[i] = String(Number(chars[i]) + 1);
  return chars.join('');
}

// The integer and fraction digits of |v| rounded to |maxFrac| fraction digits.
function fixedParts(v, maxFrac, mode, neg) {
  let text = String(v);
  if (text.indexOf('e') >= 0 || text.indexOf('E') >= 0) text = expandExp(text);
  const dot = text.indexOf('.');
  let intText = dot < 0 ? text : text.slice(0, dot);
  let fracText = dot < 0 ? '' : text.slice(dot + 1);
  if (fracText.length > maxFrac) {
    const head = roundDigits(intText + fracText, intText.length + maxFrac,
      mode, neg);
    intText = maxFrac ? head.slice(0, head.length - maxFrac) : head;
    fracText = maxFrac ? head.slice(head.length - maxFrac) : '';
  }
  return [intText, fracText];
}

// Rounds |scaled| to the nearest multiple of |inc|, using the rounding mode.
function roundIncrement(scaled, inc, mode, neg) {
  const units = scaled / inc;
  const text = String(units);
  const dot = text.indexOf('.');
  const intText = dot < 0 ? text : text.slice(0, dot);
  const fracText = dot < 0 ? '' : text.slice(dot + 1);
  const head = roundDigits(intText + fracText, intText.length, mode, neg);
  return Number(head) * inc;
}

// The integer and fraction digits of |v| rounded to at most |maxSig|
// significant digits and padded to at least |minSig| of them.
function precisionParts(v, minSig, maxSig, mode, neg) {
  if (v === 0) {
    let zeros = '';
    for (let i = 1; i < minSig; i++) zeros += '0';
    return ['0', zeros];
  }
  let text = String(v);
  if (text.indexOf('e') >= 0 || text.indexOf('E') >= 0) text = expandExp(text);
  const dot = text.indexOf('.');
  const intPart = dot < 0 ? text : text.slice(0, dot);
  const fracPart = dot < 0 ? '' : text.slice(dot + 1);
  let digits;
  let exp;
  if (intPart === '0') {
    const zeros = fracPart.length - fracPart.replace(/^0+/, '').length;
    digits = fracPart.slice(zeros);
    exp = -zeros - 1;
  } else {
    digits = intPart + fracPart;
    exp = intPart.length - 1;
  }
  const keep = Math.min(maxSig, digits.length);
  let head = digits;
  if (digits.length > keep) {
    head = roundDigits(digits, keep, mode, neg);
    // A carry (9999 -> 1000) moves the leading digit one magnitude up.
    if (head.length > keep) exp += 1;
  }
  let intText;
  let fracText;
  if (exp >= 0) {
    intText = head.slice(0, exp + 1);
    while (intText.length < exp + 1) intText += '0';
    fracText = head.slice(exp + 1);
  } else {
    intText = '0';
    fracText = '0'.repeat(-exp - 1) + head;
  }
  let sig = intText.replace(/^0+/, '').length + fracText.length;
  while (sig < minSig) { fracText += '0'; sig++; }
  return [intText, fracText];
}

// Turns an affix token list into parts, inserting the currency symbol and the
// currency spacing CLDR asks for between a letter-like symbol and a digit.
function affixParts(tokens, f) {
  const out = [];
  for (let i = 0; i < tokens.length; i++) {
    const tok = tokens[i];
    switch (tok.t) {
      case 'percentSign':
        out.push({ type: 'percentSign', value: f.sym.pct });
        break;
      case 'permilleSign':
        out.push({ type: 'permilleSign', value: f.sym.pm });
        break;
      case 'plusSign':
        out.push({ type: 'plusSign', value: f.sym.pls });
        break;
      case 'minusSign':
        out.push({ type: 'minusSign', value: f.sym.min });
        break;
      case 'currency': {
        const symbol = f.currency || '';
        out.push({ type: 'currency', value: symbol });
        break;
      }
      default:
        out.push({ type: 'literal', value: tok.v });
    }
  }
  return out;
}

// Currency patterns place the symbol against the digits; CLDR inserts a
// spacer when both sides are letter/digit shaped ("CHF 1'234.50").
// ICU's currency-spacing matches test the code points flanking the currency
// field, not the part types: the currency char must be [[:^S:]&[:^Z:]] and the
// number char must be [:digit:].
const RE_ND = /\p{Nd}/u;
const RE_SZ = /[\p{S}\p{Z}]/u;

function firstCP(s) { const m = /^[\s\S]/u.exec(s); return m ? m[0] : ''; }
function lastCP(s) { const m = /[\s\S]$/u.exec(s); return m ? m[0] : ''; }

function applyCurrencySpacing(parts, f) {
  if (!f.currency) return parts;
  const sym = f.currency;
  const out = parts.slice();
  for (let i = 0; i < out.length; i++) {
    if (out[i].type !== 'currency') continue;
    // Currency before the number: CLDR afterCurrency.
    const next = out[i + 1];
    if (next && next.value && RE_ND.test(firstCP(next.value))
      && !RE_SZ.test(lastCP(sym)) && f.spacingA) {
      out.splice(i + 1, 0, { type: 'literal', value: f.spacingA });
      i++;
      continue;
    }
    // Currency after the number: CLDR beforeCurrency.
    const prev = out[i - 1];
    if (prev && prev.value && RE_ND.test(lastCP(prev.value))
      && !RE_SZ.test(firstCP(sym)) && f.spacingB) {
      out.splice(i, 0, { type: 'literal', value: f.spacingB });
      i++;
    }
  }
  return out;
}

class NumberFormat {
  constructor(locales, options) {
    const o = options || {};
    this.locales = canonicalizeList(locales);
    this.locale = icuLocale(this.locales[0]);
    this.b = bundle(this.locale);
    this.sym = numberSymbols(this.b);
    enumOption(o.localeMatcher, 'localeMatcher', ['lookup', 'best fit'], NF_CTOR);
    this.numberingSystem = resolveNumberingSystem(this.b, o.numberingSystem);
    this.digits = digitsFor(this.numberingSystem);
    this.style = enumOption(o.style, 'style',
      ['decimal', 'percent', 'currency', 'unit'], NF_CTOR) || 'decimal';
    this.currency = undefined;
    if (o.currency !== undefined) {
      this.currency = String(o.currency);
      if (!/^[A-Za-z]{3}$/.test(this.currency)) {
        throwRange('Invalid currency code : ' + this.currency);
      }
    }
    if (this.style === 'currency') {
      if (o.currency === undefined) {
        throw new TypeError('Currency code is required with currency style.');
      }
      this.currency = this.currency.toUpperCase();
    }
    this.currencyDisplay = enumOption(o.currencyDisplay, 'currencyDisplay',
      ['code', 'symbol', 'name', 'narrowSymbol'], NF_CTOR) || 'symbol';
    this.currencySign = enumOption(o.currencySign, 'currencySign',
      ['standard', 'accounting'], NF_CTOR) || 'standard';
    this.unit = undefined;
    if (o.unit !== undefined) {
      this.unit = String(o.unit);
      checkUnit(this.unit);
    }
    if (this.style === 'unit' && o.unit === undefined) {
      throw new TypeError("Invalid unit argument for Intl.NumberFormat() ''");
    }
    this.unitDisplay = enumOption(o.unitDisplay, 'unitDisplay',
      ['short', 'narrow', 'long'], NF_CTOR) || 'short';
    this.notation = enumOption(o.notation, 'notation',
      ['standard', 'scientific', 'engineering', 'compact'], NF_CTOR) || 'standard';

    const currencyDigits = supp()._currencyDigits || {};
    let mnfdDefault = 0;
    let mxfdDefault = 3;
    if (this.style === 'currency' && this.notation === 'standard') {
      const cd = currencyDigits[this.currency];
      mnfdDefault = cd === undefined ? 2 : cd;
      mxfdDefault = mnfdDefault;
    } else if (this.style === 'percent') {
      mxfdDefault = 0;
    }
    this.minimumIntegerDigits = numberOption(o.minimumIntegerDigits,
      'minimumIntegerDigits', 1, 21) || 1;
    this.roundingIncrement = roundingIncrementOption(o.roundingIncrement);
    this.roundingMode = enumOption(o.roundingMode, 'roundingMode',
      ['ceil', 'floor', 'expand', 'trunc', 'halfCeil', 'halfFloor', 'halfExpand',
       'halfTrunc', 'halfEven'], NF_CTOR) || 'halfExpand';
    this.roundingPriority = enumOption(o.roundingPriority, 'roundingPriority',
      ['auto', 'morePrecision', 'lessPrecision'], NF_CTOR) || 'auto';
    this.trailingZeroDisplay = enumOption(o.trailingZeroDisplay,
      'trailingZeroDisplay', ['auto', 'stripIfInteger'], NF_CTOR) || 'auto';

    // V8's needSd/needFd dance from SetNumberFormatDigitOptions.
    const hasSd = o.minimumSignificantDigits !== undefined
      || o.maximumSignificantDigits !== undefined;
    const hasFd = o.minimumFractionDigits !== undefined
      || o.maximumFractionDigits !== undefined;
    let needSd = true;
    let needFd = true;
    if (this.roundingPriority === 'auto') {
      needSd = hasSd;
      if (needSd || (!hasFd && this.notation === 'compact')) needFd = false;
    }
    this.sig = null;
    if (needSd) {
      const lo = numberOption(o.minimumSignificantDigits,
        'minimumSignificantDigits', 1, 21) || 1;
      const hi = numberOption(o.maximumSignificantDigits,
        'maximumSignificantDigits', lo, 21) || 21;
      this.sig = [lo, hi];
      this.minimumSignificantDigits = lo;
      this.maximumSignificantDigits = hi;
    } else {
      this.minimumSignificantDigits = undefined;
      this.maximumSignificantDigits = undefined;
    }
    if (needFd) {
      let mn = numberOption(o.minimumFractionDigits, 'minimumFractionDigits', 0, 100);
      let mx = numberOption(o.maximumFractionDigits, 'maximumFractionDigits', 0, 100);
      if (mn === undefined && mx === undefined) {
        mn = mnfdDefault;
        mx = mxfdDefault;
      } else if (mn === undefined) {
        mn = Math.min(mnfdDefault, mx);
      } else if (mx === undefined) {
        mx = Math.max(mxfdDefault, mn);
      } else if (mn > mx) {
        throwRange('maximumFractionDigits value is out of range.');
      }
      this.minimumFractionDigits = mn;
      this.maximumFractionDigits = mx;
    } else {
      this.minimumFractionDigits = 0;
      this.maximumFractionDigits = 0;
    }
    if (!needSd && !needFd) {
      this.sig = [1, 2];
      this.minimumSignificantDigits = 1;
      this.maximumSignificantDigits = 2;
      this.roundingType = 'morePrecision';
    } else if (this.roundingPriority === 'morePrecision') {
      this.roundingType = 'morePrecision';
    } else if (this.roundingPriority === 'lessPrecision') {
      this.roundingType = 'lessPrecision';
    } else if (hasSd) {
      this.roundingType = 'significantDigits';
    } else {
      this.roundingType = 'fractionDigits';
    }
    if (this.roundingIncrement !== 1) {
      if (this.roundingType !== 'fractionDigits') {
        throw new TypeError('RoundingType is not fractionDigits');
      }
      if (this.maximumFractionDigits !== this.minimumFractionDigits) {
        throwRange('maximumFractionDigits value is out of range.');
      }
    }
    this.compactDisplay = enumOption(o.compactDisplay, 'compactDisplay',
      ['short', 'long'], NF_CTOR) || 'short';
    this.useGrouping = useGroupingOption(o.useGrouping,
      this.notation === 'compact');
    this.signDisplay = enumOption(o.signDisplay, 'signDisplay',
      ['auto', 'never', 'always', 'exceptZero', 'negative'], NF_CTOR) || 'auto';
    this._pattern = null;
    this._currency = null;
  }

  _grouping() {
    const g = this.useGrouping;
    if (g === false) return 'off';
    if (g === 'min2') return 'min2';
    if (g === 'always') return 'always';
    return 'auto';
  }

  _format() {
    if (this._pattern) return this._pattern;
    const b = this.b;
    let patternText = this.sym.dsp;
    let scale = 1;
    let currency = '';
    let displayIsSymbol = false;
    let currencyName = false;
    let unitPatterns = null;
    if (this.style === 'percent') {
      patternText = this.sym.psp;
      scale = 100;
    } else if (this.style === 'currency') {
      const override = currencyPattern(b, this.currency);
      const cn = b.cn || {};
      patternText = override || cn.csp || this.sym.csp;
      if (this.currencySign === 'accounting' && (cn.cspAcc || this.sym.cspAcc)) {
        patternText = cn.cspAcc || this.sym.cspAcc;
      }
      if (this.currencyDisplay === 'name') {
        currencyName = true;
        if (override) {
          currency = currencySymbol(b, this.currency, 'symbol');
        } else {
          // No override: the long name takes the place of the symbol, so ICU
          // formats the number with the decimal pattern.
          patternText = this.sym.dsp;
        }
      } else {
        currency = currencySymbol(b, this.currency, this.currencyDisplay);
      }
      displayIsSymbol = this.currencyDisplay === 'symbol' || this.currencyDisplay === undefined
        || this.currencyDisplay === 'narrowSymbol';
    } else if (this.style === 'unit') {
      patternText = this.sym.dsp;
      unitPatterns = b.u[this.unit] || null;
    }
    // The CLDR currency pattern carries the ISO currency placeholder (U+00A4);
    // render it as the resolved currency symbol. When the placeholder is absent
    // (inherited patterns), prefix the symbol as ICU does.
    if (this.style === 'currency' && !currencyName) {
      const symWide = currencySymbol(b, this.currency, this.currencyDisplay);
      if (patternText.indexOf('\u00a4') >= 0) {
        patternText = patternText.split('\u00a4').join(symWide);
      } else if (patternText.indexOf(symWide) < 0) {
        patternText = symWide + patternText;
      }
    }
    const pattern = parseNumPattern(patternText);
    const section = pattern.pos;    const f = {
      pattern,
      scale,
      sym: this.sym,
      b,
      tag: this.locale,
      digits: this.digits,
      grouping: this._grouping(),
      sizes: section.sizes,
      minInt: Math.max(this.minimumIntegerDigits, section.minInt),
      minFrac: this.minimumFractionDigits,
      maxFrac: this.maximumFractionDigits,
      sign: this.signDisplay,
      sig: this.sig,
      roundingType: this.roundingType,
      notation: this.notation,
      roundingMode: this.roundingMode,
      roundingIncrement: this.roundingIncrement,
      currency,
      currencyCode: this.currency,
      currencyName,
      currencyDisplayIsSymbol: displayIsSymbol,
      unit: this.unit,
      unitDisplay: this.unitDisplay,
      unitPatterns,
      compact: this.notation === 'compact' && this.sym.cp
        ? (this.sym.cp[this.compactDisplay === 'long' ? 'l' : 's'] || null)
        : null,
      spacingB: (b.cn || {}).cspB || this.sym.cspB,
      spacingA: (b.cn || {}).cspA || this.sym.cspA,
    };
    this._pattern = f;
    return f;
  }

  format(value) {
    const parts = this.formatToParts(value);
    let out = '';
    for (let i = 0; i < parts.length; i++) out += parts[i].value;
    return out;
  }

  formatToParts(value) {
    const f = this._format();
    let parts = numberParts(value, f);
    const cat = parts.digits ? pluralOps(f.b.lang, operandsOf(parts.digits[0], parts.digits[1]))
      : pluralOf(f.b, value);
    if (this.style === 'unit' && this.unit) {
      const pat = unitPattern(f, value, cat);
      if (pat) {
        parts = applyTemplate(parts, pat);
      } else {
        parts.push({ type: 'literal', value: ' ' + this.unit });
      }
    }
    if (f.currencyName) {
      // The currency long name is pluralized with the effective currency
      // locale's rules, not the formatting locale's (vo -> en).
      const nameCat = parts.digits
        ? pluralOps(f.b.csL || f.b.lang, operandsOf(parts.digits[0], parts.digits[1]))
        : pluralOfLang(f.b.csL || f.b.lang, value);
      parts = applyCurrencyName(parts, f, nameCat);
    }
    if (this.signDisplay === 'always'
      || (this.signDisplay === 'exceptZero' && !displayedZero(parts))) {
      let signed = false;
      let numeric = false;
      for (let i = 0; i < parts.length; i++) {
        const t = parts[i].type;
        if (t === 'minusSign' || t === 'plusSign') signed = true;
        if (t === 'integer' || t === 'fraction' || t === 'nan' || t === 'infinity') {
          numeric = true;
        }
      }
      if (numeric && !signed) parts.unshift({ type: 'plusSign', value: f.sym.pls });
    }
    return parts;
  }

  resolvedOptions() {
    const currency = this.style === 'currency';
    const unit = this.style === 'unit';
    const out = {
      locale: this.locale,
      numberingSystem: this.numberingSystem,
      style: this.style,
      currency: currency ? this.currency : undefined,
      currencyDisplay: currency ? this.currencyDisplay : undefined,
      currencySign: currency ? this.currencySign : undefined,
      unit: unit ? this.unit : undefined,
      unitDisplay: unit ? this.unitDisplay : undefined,
      minimumIntegerDigits: this.minimumIntegerDigits,
      // V8 derives the digit options from the ICU skeleton: a format that
      // rounds with significant digits has no fraction digits at all.
      minimumFractionDigits: this.roundingType === 'significantDigits'
        ? undefined : this.minimumFractionDigits,
      maximumFractionDigits: this.roundingType === 'significantDigits'
        ? undefined : this.maximumFractionDigits,
      minimumSignificantDigits: this.minimumSignificantDigits,
      maximumSignificantDigits: this.maximumSignificantDigits,
      useGrouping: this.useGrouping,
      notation: this.notation,
      compactDisplay: this.notation === 'compact' ? this.compactDisplay : undefined,
      signDisplay: this.signDisplay,
      roundingIncrement: this.roundingIncrement,
      roundingMode: this.roundingMode,
      roundingPriority: this.roundingType === 'morePrecision' ? 'morePrecision'
        : this.roundingType === 'lessPrecision' ? 'lessPrecision' : 'auto',
      trailingZeroDisplay: this.trailingZeroDisplay,
    };
    const clean = {};
    for (const key in out) if (out[key] !== undefined) clean[key] = out[key];
    return clean;
  }

  static supportedLocalesOf(locales) {
    return supportedOf(locales);
  }
}

// ---------------------------------------------------------------------------
// date formatting
// ---------------------------------------------------------------------------

// ICU pattern letters in the order CLDR writes skeleton keys ("yMEd").
const FIELD_ORDER = ['G', 'y', 'Y', 'u', 'Q', 'q', 'M', 'L', 'w', 'W', 'E', 'e',
  'c', 'd', 'D', 'F', 'a', 'b', 'B', 'h', 'K', 'H', 'k', 'm', 's', 'S', 'A',
  'z', 'Z', 'O', 'v', 'V', 'X', 'x'];

// Field identity used when comparing skeletons and interval patterns.
function fieldKey(f) {
  switch (f) {
    case 'Y': case 'u': return 'y';
    case 'q': return 'Q';
    case 'L': return 'M';
    case 'w': case 'W': return 'w';
    case 'e': case 'c': return 'E';
    case 'D': case 'F': return 'd';
    case 'b': case 'B': return 'a';
    case 'K': case 'H': case 'k': return 'H';
    case 'Z': case 'O': case 'v': case 'V': case 'X': case 'x': return 'z';
    default: return f;
  }
}

// ICU renders the narrow no-break space CLDR puts before day periods and era
// markers as a plain space, so normalise it away here.
function normalizeNBSP(text) {
  let out = '';
  for (let i = 0; i < text.length; i++) {
    const c = text.charCodeAt(i);
    if (c === 0x202f) { out += ' '; continue; }
    if (c === 0xe2 && text.charCodeAt(i + 1) === 0x80 && text.charCodeAt(i + 2) === 0xaf) {
      out += ' ';
      i += 2;
      continue;
    }
    out += text.charAt(i);
  }
  return out;
}

function parseDatePattern(text) {
  text = normalizeNBSP(text);
  const tokens = [];
  let i = 0;
  while (i < text.length) {
    const ch = text.charAt(i);
    if (ch === "'") {
      if (text.charAt(i + 1) === "'") { tokens.push({ l: "'" }); i += 2; continue; }
      let j = i + 1;
      while (j < text.length && text.charAt(j) !== "'") j++;
      tokens.push({ l: text.slice(i + 1, j) });
      i = j + 1;
      continue;
    }
    if ((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')) {
      let j = i;
      while (j < text.length && text.charAt(j) === ch) j++;
      tokens.push({ f: ch, n: j - i });
      i = j;
      continue;
    }
    let j = i;
    while (j < text.length && text.charAt(j) !== "'"
      && !((text.charAt(j) >= 'a' && text.charAt(j) <= 'z')
        || (text.charAt(j) >= 'A' && text.charAt(j) <= 'Z'))) j++;
    tokens.push({ l: text.slice(i, j) });
    i = j;
  }
  return tokens;
}

function tokensText(tokens) {
  let out = '';
  for (let i = 0; i < tokens.length; i++) {
    const t = tokens[i];
    if (t.f) out += repeatField(t.f, t.n);
    else out += quoteDateLiteral(t.l);
  }
  return out;
}

function widthTable(tokens) {
  const table = {};
  for (let i = 0; i < tokens.length; i++) {
    const t = tokens[i];
    if (!t.f) continue;
    const key = fieldKey(t.f);
    if (table[key] === undefined) table[key] = t.n;
  }
  return table;
}

// The field widths of ICU's normalized skeleton: the year, month and weekday
// keep their run count (the month counts from 3 on, the week day from 4 on),
// every other field is a single letter.  ICU widens an interval pattern's runs
// to these widths, so the day never gains a digit.
function normalizedWidths(tokens) {
  const full = widthTable(tokens);
  const table = {};
  if (full.y !== undefined) table.y = full.y;
  if (full.M !== undefined) table.M = full.M < 3 ? 1 : full.M;
  if (full.E !== undefined) table.E = full.E <= 3 ? 1 : full.E;
  return table;
}

// The set of fields a pattern renders.  The week day keeps the letter the
// pattern used: the interval data is keyed by "E", a stand-alone week day
// ("c") has no entry.
function fieldSet(tokens) {
  const set = [];
  for (let i = 0; i < tokens.length; i++) {
    const t = tokens[i];
    if (!t.f) continue;
    const key = (t.f === 'c' || t.f === 'e') ? t.f : fieldKey(t.f);
    if (set.indexOf(key) < 0) set.push(key);
  }
  return set.sort().join(',');
}

// The CLDR skeleton of a pattern: the field letters with their canonical
// widths, in the canonical field order.  Interval data is keyed by it.
const SKELETON_ORDER = ['G', 'y', 'Q', 'M', 'w', 'E', 'd', 'B', 'H', 'h', 'm', 's'];

function patternSkeleton(tokens, withZone) {
  const widths = {};
  const chars = {};
  let zone = false;
  for (let i = 0; i < tokens.length; i++) {
    const t = tokens[i];
    if (!t.f) continue;
    // ICU strips the plain day period from the skeleton it looks up but keeps
    // the flexible one, which the interval data is keyed by.
    if (t.f === 'a' || t.f === 'b') continue;
    const key = t.f === 'B' ? 'B' : fieldKey(t.f);
    if (key === 'z') { zone = true; continue; }
    if (widths[key] === undefined) { widths[key] = t.n; chars[key] = t.f; }
  }
  let out = '';
  for (let i = 0; i < SKELETON_ORDER.length; i++) {
    const key = SKELETON_ORDER[i];
    const n = widths[key];
    if (n === undefined) continue;
    if (key === 'M') out += n >= 4 ? 'MMMM' : (n === 3 ? 'MMM' : 'M');
    // The week day keeps the letter the pattern used: the interval data is
    // keyed by "E", a stand-alone week day ("c") has no entry.
    else if (key === 'E') out += chars.E;
    else out += key;
  }
  if (withZone && zone) out += 'v';
  return out;
}

// The entry of an interval table for the greatest differing field.  ICU keeps
// the flexible day period in the AM/PM slot, so the "B" entry wins over "a".
function ivFieldPattern(table, field) {
  if (field === 'a') {
    if (typeof table.B === 'string') return table.B;
    return typeof table.a === 'string' ? table.a : null;
  }
  return typeof table[field] === 'string' ? table[field] : null;
}

function fieldValues(date, utc) {
  const get = (name) => (utc ? date['getUTC' + name]() : date['get' + name]());
  const year = get('FullYear');
  const hour = get('Hours');
  return {
    year: year <= 0 ? 1 - year : year,
    era: year <= 0 ? 0 : 1,
    month: get('Month'),
    day: get('Date'),
    weekday: get('Day'),
    hour,
    minute: get('Minutes'),
    second: get('Seconds'),
    ms: get('Milliseconds'),
    hour12: hour % 12 === 0 ? 12 : hour % 12,
    hour11: hour % 12,
    hour24: hour === 0 ? 24 : hour,
  };
}

function eraText(b, width, era, cal) {
  // The Buddhist calendar has a single era in CLDR (index 0) and ICU shows the
  // locale's own Buddhist name for it.  A locale whose default calendar is not
  // Buddhist has no such data, so the CLDR Gregorian name must not be used.
  if (cal === 'buddhist' && b && b.d && b.d.cal === 'buddhist') {
    const list = (width <= 3 ? b.d.ea : width === 4 ? b.d.en : b.d.enw) || b.d.ea;
    return (list && (list[era] || list[0])) || '';
  }
  if (cal && cal !== 'gregory') {
    const named = calendarEraText(cal, width, era);
    if (named !== null) return named;
  }
  const list = (width <= 3 ? b.d.ea : width === 4 ? b.d.en : b.d.enw) || b.d.ea;
  return (list && list[era]) || '';
}

// Julian Day Number of a proleptic Gregorian date (|m0| is 0-based).
function toJD(y, m0, d) {
  const mm = m0 + 1;
  const a = Math.floor((14 - mm) / 12);
  const yy = y + 4800 - a;
  const m2 = mm + 12 * a - 3;
  return d + Math.floor((153 * m2 + 2) / 5) + 365 * yy
    + Math.floor(yy / 4) - Math.floor(yy / 100) + Math.floor(yy / 400) - 32045;
}

function isGregLeap(y) {
  return y % 4 === 0 && (y % 100 !== 0 || y % 400 === 0);
}

// The calendar year containing |jd|, by binary search over |toJDcal|.
function calendarYearFromJD(toJDcal, jd) {
  let lo = -1000;
  let hi = 4000;
  while (lo < hi) {
    const mid = Math.floor((lo + hi + 1) / 2);
    if (toJDcal(mid, 0, 1) <= jd) lo = mid; else hi = mid - 1;
  }
  return lo;
}

// ICU's islamic calendar is the astronomical (true lunar) one: it picks the day
// the new moon is visible, so its month lengths never settle into a cycle.
// Captured month lengths, one character per month ('1' = 30 days), 12 per year,
// for years 1..2000 (622 CE - 2560 CE).  Outside that range the civil calendar.
const ISLAMIC_START1 = 1948439;
const ISLAMIC_LO = -1000;
const ISLAMIC_MONTHS = '0110101010101010110110010010111011001001010111100100101011101010010101101101001010101110100101010110101010101010110101010101010110101010010110110101001010111010100101011011010010011011101001010101101100101010101101010101010101101010100101101101010010101110101001010110110100100110111010010010111011001001011011010101001101101010101001101011010100101011101010010101101101001001101110100100101111010010010111011001001011011010101001011011010101001011011010100101011011010010011011101001001011110100100101110110010010110110101001010110110100101010110110100101010110110100101010111010010010111011001001011101100100101011101010010101110101001010110110100101010110101100101010110110100101010110110010010111011001001010111100100101011101010010101101101001010101101100101010101101010101010101101010100101110110010010101110101001010111010100101011011010010101011101001010101101010101010101011010101001101101010100101101101010010101110101001001110110100100110111010010011011010101010101011010101010101011010101'
  + '0010110110101001010111010100100111011010010011011101001001011101100100101101101010100110101101010100110101101010010101110101001010110110100100110111010010010111011001001011011010100101101101010010101101101010010101101101010010101101101001001101110100100101110110010010111011001001011011010100101011011010010101011011010010101011011010010101011101001001011101100100101110110010010101110101001010111010100101011010110010101011010101010101011010110010011011011001001011011100100101011101010010101110101001010110110100101010110101010101010110101010101010110101010010110110101001010111010100101011101010010101101101001010101101100101010110101010101010101101010100101101101010010101110101001010111010100100111011010010011011101001001101101010101001101101010101001101101010100101101101010010101110101001001110110100100110111010010010111011001001011011010100101101011010100101011011010100101011101010010101101101001001101110100100101110110010010110110101001011011010100101011010110100101011011010100101011011'
  + '0100100110111010010010111011001001011101100100101011101010010101101101001010101101011001010101101101001001101110100100101110110010010111011001001010111010100101011011010010101101010101010101011010101010101011010101001011101100100101101110010010101110101001010111010100101011011010010101011010101010101011010101010011011010101001011011010100101011101010010101110101001001110110100100110110101010101010110101010101010110101010010110110101001010111010100101011101010010011101101001001101110100100110110101010010110110101010010110110101010010110110101001010111010100100111011010010011011101001001011101010100101101101010010101101101010010101101101010010101110101001001110110100100110111010010010111011001001011011010100101011101010010101101011010010101011011001010101101101001001101110100100101110110010010110110101001010111010100101011011010010101011010101010101011011001010011011101001001011101100100101101110010010101110101001010110110100101010110101010101010110101010101010110101010010111010101001011'
  + '0110101001010111010100101011101010010101101100101010101101010101010101101010101001101101010100101101101010010101110101001010111010100100111011010010011011010101001101011010101010101011010101001011011010100101011101010010101110101000101110110100100110111001010010111010101001011011010101001011010110100101011011010100101011101010001011101101000101101110100010101110101010010110110101001010110110101001010110110100101010110110100100111011010001011011101000101011101010010101101101010010101101101001010110101100101010101101100101010110110100100110111010001010111011000101011011010010101011101010010101101100101010101101010101010101101010101001101101010100101110110010010110110100101010111010010101011011001010101011010101010101011010101010101011010101001011101010100101101101010001101110101000110111010010100111010101010101011010101010101011010101001011011010100101011011010100101011101010001011110101000101110110010100110110101010011010110101010010110110101001010110110100101010111010100010111101010001'
  + '0111011010001011011010100101011101010100101101011010100101011011010010101101101001010101110101000101110110100010110111001001010111010100101011011010100101011011010010101011011010010101011011001001011101101000101011110100010101110101001010110110100101010110110010101010110101010101010110110010100111011001001011011100100101011101010010101101101001010101110100101010110101010101010110101010101010110101010010110110101001010111010100101011011010010011011101001010101101100101010101101010101010101101010101001101101010010101110101001010110110100100110111010010011011011001001011011010101001101101010101010101011010100101011101010010101101101001001101110100100101111010010010111011001001011011010101001011011010101001011011010100101011011010010101011101001001011110100100101110110010010110110101001010110110101001011010110100101010110110100101010111010010011011011001001011101100100101101101010010101110101001010110110100101010110101100101010110110100101010110110010010111011001001010111100100101011101010'
  + '0101011011010010101011011001010101011010101010101011010101001011101100100101101110010010101110101001010110110100101010111010010101011010101010101011010101010101011010101001011011010100101011101010010101101101001001101110100100110110110010101010110101010101010110101010010110110101001010111010100101011011010010011011101001001011101100100101101101010100110101101010100110101101010010101110101001010110110100100110111010010010111101001001011101010100101101101010010101101101010010101101101010010101101101001001101110100100101111010010010111011001001011011010100101011011010010101011011010010101011011010010101011101001001011101100100101110110010010101110101001010111010100101011011010010101011010101010101011010110010011011011001001011101100100101011101010010101110101001010110110100101010110101010101010110101010101010110101010010110111001001010111010100101011101010010101101101001010101110100101010110101010101010101101010100110110101010010110110101001010111010100100111011010010011011101001001101101'
  + '0101010101011010101010101011010101001011011010100101011101010010011101101001001101110100100101110110010010110110101010011010110101001010110110101001010111010100101011011010010011011101001001011101100100101101101010010110110101001010110110101001010110110101001010110110100100110111010010010111101001001011101100100101101101010010101101101001010101101101001010101101101001010101110100100101110110010010111011001001010111010100101011101010010101101011001010101101010101010101101010101001101101100100101101110010010101110101001010111010100101011011010010101011010101010101011010101010101011010101001011011010100101011101010010101110101001001110110100100110110110010101011010101010101010110101010010111010101001011011010100101011101010010011101101001001101110100100110110101010100110110101010010110110101010010110110101001010111010100100111011010010011011101001001011101100100101101101010010110101101010010101101101010010101110101001001110110100100110111010010010111011001001011011010100101101101010010101'
  + '1010110100101011010110010101011011010010011011101001001011101100100101110101010010110110101001010110110100101010110101010101010110110010100110111010010010111011001001011011100100101011101010010101101101001010101101010101010110101010101010101101010100101110110010010110111001001010111010100101011101010010101101101001010101101010101010101101010101001101101010100101101101010010101110101001010111010100100111011010010011011010101010101011010101010101011010101001011011010100101011101010010101110101000101110110100100110111010010011011010101001011011010101001011011010100101011011010100101011101010010011101101000101101110100100101110101010010110110101001010110110101001010110110100101010111010100100111011010001011011101000101011101100100101101101010010110110101001010110101100101010101101100101010110110100100110111010010010111011001001011011010100101011101010010101101101001010101101010101010101101100101001101110100100101110110010010110110101001010111010100101011011010010101011010101010101011010101'
  + '0101010110101010010111010101001011011010100101011101010010101110101001010110110010101010110101010101010110101010100110110101010010110110101001010111010100011011101010001011101100101001101101010100110101101010100110101101010010101101101010010101110101000101111010100010111011010001011011100101001011101010100101101011010100101101011010010101101101001010101110101000101110110100010110111010001010111010100101011011010100101011011010010101011011010010101011011001001011101101000101101110100010101110101001010110110100101010110110100101011010110010101010110110010101011011001001011011101000101011101010010101101101001010101110100101010110101010101010110101010101010110101010010110110101001010111010100101011011010010101011101001010101101100101010101101010101010101101010101001101101010010101110101001010110110100100110111010010011011101001010011101010101010101101010101010101011010100101101101010010101101101001010101110100100101111010010010111011001001011011010101001011011010101001011011010100101011011'
  + '0100101010111010010010111101001001011101100100101101101010010101110101010010110101101001010101101101001010110110100100110111010010010111011001001011011100100101011101010010101101101001010101101011001010101101101001010101101100100101110110010010110111001001010111010100101011011010010101011011001010101011010101010101011011001001011101100100101101110010010101110101001010110110100101010111010010101011010101010101011010101010101011010101001011011010100101011101010010101101101001001101110100100110110110010101010110101010101010110101010010110110101001010111010100101011011010010011011101001001011101100100101101101010100110110101010100110101101010010101110101001010110110100100110111010010010111101001001011101100100101101101010100101101101010100101101101010010101101101001010101110100100101111010010010111011001001011011010100101011011010010101101011010010101011011010010101011101001001011110100100101110110010010110110101001010111010100101011011010010101011010101010101011011010010101011011001001011'
  + '1011001001011011100100101011101010010101101101001010101101100101010101101010101010101101010100101110110010010110110101001010111010100101011011010010101011101001010101101010101010101101010101001101101010100101101101010010101110101001001110110100100110111010010011011010101010101011010101010101011010101001011011010100101011101010010011101101001001101110100100101110110010010110110101010011010110101010011010110101001010111010100101011011010010011011101001001011110100100101110101010010110110101001010110110101001010110110101001010110110100100110111010010010111101001001011101100100101101101010010101101101001010101101101001010101101101001010101110100100101110110010010111011001001011011010100101011101010010101101101001010101101010101010101101010101001101101100100101110110010010101110101001010111010100101011011010010101011010101010101011010101010101011010101001011101010100101011101010010101110101001001110110100101010111010010101011010101010101010110101010010111010101001011011010100101011101010010'
  + '0111011010010011011101001001101101010101001101101010101010101101010100101101101010010101110101001001110110100100110111010010010111011001001011011010100101101011010100101011011010100101011101010010101101101001001101110100100101110110010010111010101001011011010100101011010110100101011011010100101011011010010011011101001001011110100100101110110010010110110101001010110110100101010110101100101010110110010100110111010010010111011001001011101100100101011101010010101110101001010110101010101010110101010101010101101010100110110110010010111011001001010111010100101011101010010101101101001010101101010101010101101010101001101101010100101110101010010101110101001010111010100100111011010010011011010101010101011010101010101011010101001011101010100101101101010010101110101001001110110100100110111010010011011010101001011011010101001011011010101001011011010100101011101010010011101101001001101110100100101110101010010110110101001010110110101001010110110100101010111010100100111011010010011011101001001011101100'
  + '100101101101'
  + '0100101101101010010101101011010010101101011001010101101101001001101110100100101110110010010111010101001011011010100101011011010010101011010101010101011011001010011011101001001011101100100101110110010010101110101001010111010100101011010101010101010110101010101010110101010010111010101001011101010100101011101010010101110101001010110110010101010110101010101010110101010100110110101010010110110101001010111010100011011101010001011101101001001101101010100110101101010101010101101010010101101101010010101110101001010111010100010111011010001011011100101001011101010100101101101010100101101101010010101101101010010101110101000101110110100010110111010001010111010101001011011010100101011011010100101011011010010101011011010010011101101000101101110100010101110101001010110110101001010111010100101011010110010101010110110010101011011010001011011101000101011101100010101101101001010101110100101010110110010101010110101010101010110101010100110110101001010111010100101011101010010101011101001010101101100101010101'
  + '1010101010101011010101010101011010100101011101010010101101101001010101110100100110111010010101011011001010101011010101010101011010101001011011010100101011011010010101011101001001011110101000101110110010010110110101010011010110101010011010110101001010110110100101010111010010010111101010001011101100100101101101010010101110101010010110101101010010110101101001010110110100101010111010010010111011010001011011100100101011101010010101101101010010101101101001010101101101001010101101100100101110110010010110111001001010111010100101011011010010101011011001010101101010101010101011011001010011101100100101101110010010101110101001010110110100101010111010010101011010101010101011010101010101011010101001011011010100101011101010010101101101001001101110100101010110110010101010110101010101010110101010100110110101001010111010100101011011010010011011101001001101110100100101110101010101010110101010100110101101010010110110101001010110110100100110111010010010111101001001011101100100101101101010100101101101010100'
  + '1011011010100101011011010010101011101001001011110100100101110110010010110110101001010111010100101011010110100101010110110100101010111010010011011101001001011101100100101101110010010101110101001010110110100101010110101100101010110110100101010110110010010111011001001011011100100101011101010010101101101001010101101100101010101101010101010101101010100101110110010010110111001001010111010100101011011010010101011101001010101101010101010101101010101010101101010100101101101010010101110101001010110110100100110111010010011011011001010101011010101010101011010101001011011010100101011101010010101101101001001101110100100110111010010010111010101010011011010101010011010110101001010111010100101011011010010011011101001001011110100100101110110010010110110101001010110110101001010110110101001010110110100100110111010010010111101001001011101100100101101101010010101101101001010110101101001010101101101001010101110100100101111010010010111011001001011011010100101011101010010101101101001010101101010101010101101011'
  + '0010011011011001001011101100100101101101010010101110101001010110110100101010110101010101010110101010101010110101010010111011001001010111010100101011101010010101101101001010101110100101010110101010101010101101010100110110101010010110110101001010111010100100111011010010011011101001001101101010101010101101010101010101101010100101101101010010101110101001001110110100100110111010010010111011001001011011010100101101011010100101101011010100101011101010010101101101001001101110100100101111010010010111010101001011011010100101011011010100101011011010100101011011010010011011101001001011110100100101110110010010110110101001010110110100101010110110100101010110110100101010111010010010111011001001011101100100101101101010010101110101001010110110100101010110101010101010110101010100110110110010010111011001001010111010100101011101010010101101101001010101101010101010101101010101010101101010100101110101010010101110101001010111010100100111011010010011011011001010101101010101010101011010101001011101010100101101'
  + '1010100101011101010010011101101001001101110100100110110101010010110110101010010110110101010010110110101001010111010100100111011010010011011101001001011101010100101101101010010110101101010010101101101010010101110101001001110110100100110111010010010111011001001011101010100101101101010010101101011010010101101011001010101101101001001101110100100101110110010010111010101001011011010100101011011010010101011010101010101011011001010011011101001001011101100100101110110010010101110101001010111010100101011010101010101011010101010101010110101010010111010101001011101010100101011101010010101110101001010110110100101010110101010101010110101010100110110101010010111010101001010111010100101011101010010011101101001001101101010100110101101010101010101101010100101101101010010101110101001010111010100010111011010001011011100101001101101010100101101101010100101101101010010101101101010010101110101001001110110100010110111010001010111010101001011011010100101011011010100101011011010010101011101010010011101101000101'
  + '1011101000101011101100010101101101010010110110101001010110101100101010110101100101010110110100100110111010001010111011000101011101010010101011101010010101101101001010101101010101010101101010101001101110010100101110110010010111010101001010111010010101011101001010101101010101010101011010101010101011010101001011101010010101110101010010101110101000110111010010101011011001010101011010101010101011010101010011011010100101011011010100101011101010001101110101000101110110010100110110101010011010110101010011010110101001010110110100101010111010100011011101010001011101101000101101110010010101110101010010110110101010010110110101001010110110100101010111010100010111011010001011011100100101011101010010101101101010010101101101001010101101101001010101101100100101110110100010110111010001010111010100101011011010010101011101001010101101011001010101011011001010101101100100101101110100010101110101001010111010100101010111010010101011010101010101011010101010101011010101001011011010100101011101010010101110101001'
  + '0101011101001010101101100101010101101010101010101101010101001101101010010101110101001010110110100100110111010010011011101001001011101010101010101101010101010101101010100101101101010010101101101001001101110100100101111010010010111011001001011011010101001101011010101001011011010100101011011010010101011101001001101110100100101110110010010110110101001010111010101001011010110100101011010110100101011011010010011011101001001011101100100101101110010010101110101001010110110100101010110101100101010110110100101010110110010010111011001001011011100100101011101010010101101101001010101101100101010101101010101010101101100100101110110010010110111001001010111010100101011011010010101011101001010101101010101010101101010101010101101010100101101101010010101110101001010110110100100110111010010011011011001010101011010101010101011010101001011011010100101011101010010101101101001001101110100100110111010010010111010101010011011010101010011010110101001011011010100101011011010010011011101001001011110100100101110110'
  + '0100101101101010100101101101010010101101101010010101101101001010101110100100101111010010010111011001001011011010100101011101010010101101011010010101011011010010101011101001001101110100100101110110010010110111001001010111010100101011011010010101011010101010101011010110010101011011001001011101100100101101110010010101110101001010110110100101010110110010101010110101010101010110101010010111011001001011011010100101011101010010101101101001010101110100101010110101010101010110101010100110110101010010110110101001010111010100100111011010010011011101001001101101010101010101101010101010101101010100101101101010010101110101001010110110100100110111010010011011011001001011011010101001101101010100101101011010100101011101010010101101101001001101110100100101111010010010111010101001011011010100101011011010100101011011010100101011011010010011011101001001011110100100101110110010010110110101001010110110100101011010110100101010110110100101010111010010010111101001001011101100100101101101010010101110101001010110'
  + '1101001010101101010101010101101010101001101101100100101110110010010110110101001010111010100101011011010010101011010101010101011010101010101011010101001011101010100101101101010010101110101001001110110100100110111010010101011010101010101010110101010011011010101001011011010100101011101010010011101101001001101110100100110110101010101010110101010101010110101010010110110101001010111010100100111011010010011011101001001011101100100101101101010010110101101010010110101101010010101110101001010110110100100110111010010010111101001001011101010100101101101010010101101101010010101101101001010101101101001001101110100100101111010010010111011001001011011010100101011011010010101011010101010101011011001010101011101001001011101100100101110110010010110110101001010111010100101011010110010101011010101010101011010101010011011011001001011101100100101011101010010101110101001010110110100101010110101010101010110101010101010110101010010111010101001011011010100101011101010010011101101001001101101010101010110101010101'
  + '0101011010101001011101010100101101101010010101110101001001110110100100110111010010011011010101001011011010101001011011010101001011011010100101011101010010011101101000101101110100100101110101010010110110101001011010110101001010110110100101010111010100100111011010010011011101000101011101100100101110101010010110110101001010110101100101010110101100101010110110100100110111010010010111011001001011101010100101101101010010101101101001010101101010101010101101100101001101110100100101110110010010111010101001011011010100101011101010010101101010101010101011010101010101011010101001101101010100101110101010010101110101001010111010100101011011001010101011010101010101011010101010011011010101001011011010100101011101010001101110101000101110110100100110110101010011010110101010101010110101001010110110101001011011010100101011101010001011101101000101101110010100101110101010010110110101010010110110101001010110110101001010111010100010111011010001011011101000101011101010010101101101010010101101101001010101101101'
  + '0010101011101010010011101101000101101110100010101110101001010110110100101011011010100101011010110010101010110110010101011011001001011011101000101011101010010101110101001010110110100101010110110010101010110101010101010110101010100110110101001010111010100101011101010010101101101001010101101100101010101101010101010101101010101010101101010010101110101001010111010100101010111010010011011101001001101101100101010101101010101010101101010100101101101010010101101101001010101110100100110111010010010111011001001011011010101001101011010101001101011010100101011011010010101101101001001101110100100101110110010010110110101001010111010101001011011010101001011010110100101011011010010011011101001001011101100100101101110010010101110101001010110110100101010110110100101010110110100101010111010010010111011001001011011100100101011101010010101101101001010101101100101010110101010101010101101100101001110110010010110111001001010111010100101011101010010101101101001010101101010101010101101010101010101101010100101101'
  + '1010100101011101010010101110101001001101110100101010110110010101010110101010101010110101010010110110101001010111010100101011101010010011011101001001101110100100101110101010100110110101010100110110101010010110110101001010110110100100110111010010011011101001001011101100100101101101010100110101101010100101101101010010101101101001010101110100100101111010010010111011001001011011010100101011101010010101101011010010101101011010010101101101001001101110100100101110110010010110111001001010111010100101011011010010101011010110010101011011010010101011011001001011101100100101101110010010101110101001010110110100101010110110010101010110101010101010110101010010111011001001011011100100101011101010010101101101001010101110100101010110101010101010110101010101010110101010010110110101001010111010100101011101010010011011101001001101101100101010101101010101010101101010100101101101010010101110101001010111010100100110111010010011011101001001011101010101001101101010101001101011010100101101101010010101101101001001'
  + '1011101001001011110100100101110110010010110110101001010110110101001010110110101001010110110100100110111010010010111101001001011101100100101101101010010101110101001010110101101001010101101101001010110110100100110111010010010111011001001011011010100101011101010010101101101001010101101010101010101101010101001101101100100101110110010010110111001001010111010100101011011010010101011010101010101011010101010101011010101001011101100100101101101010010101110101001010110110100101010111010010101011010101010101011010101010011011010101001011011010100101011101010010011110101001001101110100100110110101010101010110101010101010110101010010110110101001010111010100101011011010010011011101001001101101100100101101101010010110110101010010110101101010010101110101001010110110100100110111010010010111101001001011101010100101101101010010101101101010010101101101010010101101101001001101110100100101111010010010111011001001011011010100101011011010010101011011010010101011011001010101011101001001011101100100101110110010'
  + '0101101101010010101110101001010110110100101010110101010101010110101010100110110110010010111011001001011011010100101011101010010101101101001010101101010101010101101010101010101101010100101110101010010110110101001010111010100100111011010010011011011001010101101010101010101011010101001101101010100101101101010010101110101001001110110100100110111010010011011010101001011011010101001011011010101001011011010100101011101010010011101101001001101110100100101110101010010110110101001011010110101001010110110100101010111010100101011011010010011011101001001011110100100101110101010010110110101001010110110101001010110110100101010110110100100110111010010010111101001001011101010100101101101010010101101101001010101101010101010101101100101001101110100100101110110010010111011001001011011010100101011101010010101101010101010101101010101010101101010101001101101010100101110101010010110110101001010111010100101011011001010101011010101010101011010101010011011010101001011101010100101101101010010101110101000101110110'
  + '1001001101101010100110110101010101010101101010100101110101010010110110101001010111010100010111011010001011011100101001101101010100101101101010100101101101010010101101101010010101110101000101110110100010110111010001010111010101001011011010100101101011010100101011011010010101011101010010011101101000101101110100010101110110001010111010101001011011010100101011010110010101011010110010101011011010001011011101000101011101100010101110101001010110110100101010110110010101010110101010101010110101010100110111001001010111011000101011101010010101101101001010101110100101010110101010101010101101010101010101101010100110110101001010111010100101010111010010011011101001010101101100101010101101010101010101101010101001101101010010101110101001010101110100100110111010100010111011001001011011010101001101011010101001101011010100101011101010010101101101001001101110101000101110110010010110111001001011011010101001011011010101001011011010100101011011010010101011101010001011101101000101101110010010101110101001010110'
  + '1101010010101101101001010101101101001010101110100100101110110100010110111001001010111010100101011011010010101101101001010101101010101010101011011001010101101100100101101110010010101110101001010111010100101011011010010101011011001010101011010101010101011010101001011011010100101011101010010101110101001010110110100101010110110010101010110101010101010110101010101010110101001010111010100101011101010010011011101001001101110100100101110101010101010110101010101010110101010010110110101001010111010100100110111010010011011101001001011101100100101101101010100110101101010100101101101010010101101101001010101110100100110111010010010111011001001011011010100101011101010100101101101010010101101011010010101101101001001101110100100101110110010010110111001001010111010100101011011010010101011011010010101011011010010101011101001001011101100100101101110010010101110101001010110110100101010110110010101010110101010101010110110010010111011001001011011100100101011101010010101110101001010101110100101010110101010101'
  + '0101101010101010101101010100101101101010010101110101001010111010100100110111010010011011011001010101011010101010101011010101001011011010100101011101010010101110101001001101110100100110111010010010111010101010011011010101010011011010101001011011010100101011011010010011011101001001011110100100101110110010010110110101001010110110101001010110110101001010110110100101010111010010010111101001001011101100100101101101010010101110101001010110101101001010101101101001010110110100100110111010010010111011001001011011100100101011101010010101101101001010101101010101010101101101001001101101100100101110110010010110111001001010111010100101011011010010101011011001010101011010101010101011010101001011101100100101101110010010101110101001010110110100101010111010010101011010101010101011010101010011011010101001011011010100101011101010010011110101001001101110100100110110110010101010110101010101010110101010010110110101001010111010100101011011010010011011101001001101101100100101110101010010110110101010010110101101'
  + '0100101011101010010101101101001001101110100100101111010010010111010101001011011010100101011011010100101011011010100101011011010010011011101001001011110100100101110110010010110110101001010110110100101011010110100101010110110010101011011010010011011101001001011101100100101101101010010101110101001010110110100101010110101010101010110101010100110110110010010111011001001011011100100101011101010010101101101001010101101010101010101101010101010101101010100101110101010010110110101001010111010100101011011010010011011101001010101101010101010101101010101001101101010100101101101010010101110101001001110110100100110111010010011011010101001101011010101010101011010101001011011010100101011101010010011101101001001101110100100101110110010010110110101001011010110101001011010110101001010111010100101011011010010011011101001001011110100100101110101010010110110101001010110110101001010110110100101010110110100100110111010010010111101001001011101100100101101101010010101101101001010101101010101010101101100101010101'
  + '1101001001011101100100101110110010010110110101001010111010100101011010110010101011010101010101011010101010011011011001001011101100100101101101010010101110101001010110110100101010110101010101010110101010100110110101010010111010101001011011010100101011101010010011101101001001101101010101010110101010101010101101010100110110101010010110110101001010111010100010111011010001011011101001001101101010100101101101010100101101101010010101101101010010101110101001001110110100010110111010001010111010101001011011010100101101011010100101011011010010101011101010010101101101000101101110100010101110110001010111010101001011011010100101011010110010101011010110010101011011010010011011101000101011110100010101110101001010110110101001010110110100101010110101010101010110110010100110111010001010111011000101011101010100101101101010010101110100101010110101010101010110101010101010110101010100110110101001010111010101001011011010100011011101010010101101100101010101101010101010101101010101001101101010100101110101001010'
  + '1101101010001101110101000101110110010100110110101010011011010101010011010110101001010111010100101011011010100101011101010001011101101000101101110010010110110101010010110110101010010110110101001010110110100101010111010100010111011010001011011100100101011101010010101101101010010101101101001010101101101001010101110100100101110110100010110111010001010111010100101011101010010101101101001010101101011001010101101011001010101101100100101101110100010101110101001010111010100101011011010010101011011001010101011010101010101011010101010011011010100101011101010010101110101001010110110100101010110110010101011010101010101010110101010101010110101001010111010100101011101010010101011101001001101110100100110110110010101010110101010101010110101010010110110101001010111010100101011011010010011011101001001011101100100101101101010100110101101010100110101101010010101110101001010110110100100110111010010010111011001001011011010100101101101010100101101101010010101101011010010101101101001010101110100100101110110010'
  + '0101101110010010101110101001010110110100101010110110100101010110110100101010111010010010111011001001011011100100101011101010010101110101001010110101100101010110101010101010101101100101010110110010010110111001001010111010100101011101010010101101101001010101101010101010101101010101010101101010100101101101010010101110101001010111010100100111011010010101011011001010101011010101010101011010101001101011010100101011101010010101110101001001110110100100110111010010010111010101010011011010101010101011010101001011011010100101011101010010011011101001001101110100100101110110010010110110101010011010110101001010110110101001010110110100101010111010010011011101001001011101100100101101101010010101110101001010110101101001010110101101001010110110100100110111010010010111011001001011011100100101011101010010101101101001010101101011001010101101101001010101101100100101110110010010110111001001010111010100101011011010010101011011001010101011010101010101011010101001011101100100101101110010010101110101001010111010'
  + '1001010101110100101010110101010101010110101010101010110101010010110110101001010111010100101011101010010011101101001001101101100101010101101010101010101101010100101101101010010101110101001010111010100100110111010010011011101001001011101010101001101101010100101101011010100101101101010010101101101001001101110100100101111010010010111011001001011011010100101011011010100101011011010100101011011010010011011101001001011110100100101110110010010110110101001010111010100101011010110100101010110110100101011011010010011011101001001011101100100101101101010010101110101001010110110100101010110101010101010110110010100110110110010010111011001001011011100100101011101010010101101101001010101101010101010101101010101010101101010100101110101010010110110101001010111010100101011101010010101011101001010101101010101010101101010101001101101010100101101101010010101110101001001111010100100110111010010011011010101010101011010101010101011010101001011011010100101011101010010101101101001001101110100100110110110010010111'
  + '0101010010110110101010010110101101010010101110101001010110110100100110111010010010111101001001011101010100101101101010010101101101010010101101101001010101101101001001101110100100101111010010010111011001001011011010100101011011010010101011011001010101011011001010101011101001001011110100100101110110010010110110101001010111010100101011011010010101011010101010101011010101010011011011001001011101100100101101101010010101110101001010110110100101010110101010101010110101010101010110101010010111010101001011011010100101011101010010101101101001001101110010101010110101010101010110101010100110110101010010110110101001010111010100100111011010010011011101001001101101010100101101101010100101101101010100101101101010010101110101001001110110100010110111010010011011010101001011101010100101101011010100101011011010010101011101010010101101101001001101110100010101111010010010111010101001011011010100101011011010010101011011010010101011011010010011011101001001011110100100101110101010010110110101001010110110100101'
  + '0101101010101010101101100101001101110100100101111010010010111011001001011011010100101011101010010101101010101010101101010101010101101010101001101101010100101110101010010110110101001010111010100101011011001010101011010101010101011010101010011011010101001011101010100101101101010010101110101000101110110100100110110101010011011010101010101010110101010010111010101001011011010100101011101010001011101101000101101110010100110110101010010110110101010010110110101001010110110101001010111010100010111011010001011011101000101011101010010101101101010010110101101010010101101101001010101110101001001110110100010110111010001010111010100101011101010010101101101010010101101011001010101101011001010101101100100101101110100010101111010001010111010100101011011010010101011011001010101011010101010101011010101010011011100100101011101100010101110101001010110110100101010111010010101011010101010101011010101010101011010101001011011010100101011101010010101101101001001101110100101010110110010101010110101010101010110101';

function islamicMonthLength(year, month) {
  const i = (year - ISLAMIC_LO) * 12 + month;
  if (i < 0 || i >= ISLAMIC_MONTHS.length) {
    let len = 29 + (month + 1) % 2;
    if (month === 11 && (14 + 11 * year) % 30 < 11) len++;
    return len;
  }
  return ISLAMIC_MONTHS.charCodeAt(i) === 49 ? 30 : 29;
}

function islamicYearLength(year) {
  let n = 0;
  for (let m = 0; m < 12; m++) n += islamicMonthLength(year, m);
  return n;
}

// Lazily built year starts; ISLAMIC_START1 is the JDN of 1 Muharram 1 AH.
const ISLAMIC_CUM = [0];
function islamicCum(i) {
  while (ISLAMIC_CUM.length <= i) {
    const n = ISLAMIC_CUM.length;
    ISLAMIC_CUM.push(ISLAMIC_CUM[n - 1] + islamicYearLength(n));
  }
  return ISLAMIC_CUM[i];
}
const ISLAMIC_NEG = [0];
function islamicNeg(i) {
  while (ISLAMIC_NEG.length <= i) {
    const n = ISLAMIC_NEG.length;
    ISLAMIC_NEG.push(ISLAMIC_NEG[n - 1] + islamicYearLength(1 - n));
  }
  return ISLAMIC_NEG[i];
}
function islamicYearStart(year) {
  return year >= 1 ? ISLAMIC_START1 + islamicCum(year - 1)
    : ISLAMIC_START1 - islamicNeg(1 - year);
}
// islamic-umalqura: ICU keeps its own month length table for the years
// 1300-1600 and falls back to the civil computation outside that window.
// One character per month ('1' = 30 days) for years 1..2000.
const ISLAMIC_UMALQURA_MONTHS = '1010101010101010101010111010101010101010101010101010101010111010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010111010101010101010101010101010101010111010101010101010101010101010'
  + '1010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010'
  + '1010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010101010101011'
  + '1010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010'
  + '1010101010101010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101110101010'
  + '1010101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010'
  + '1010101010111010101010101010101010101010101010111010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010'
  + '1010101010101010101010101010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010'
  + '1011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010101010101011101010101010'
  + '1010101010101010101010111010101010101010101010101010101010111010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010111010101010101010101010101010101010111010101010101010101010101010'
  + '1010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010'
  + '1010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010101010101011'
  + '1010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010'
  + '1010101010101010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101110101010'
  + '1010101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010'
  + '1010101010111010101010101010101010101010101010111010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010111010101010101010101010101010101010101101010101001110110010010110110101000110111010100011011011001010101011010101010101010110101010010111100100101011101010010101110101001010110110100101010111001101001011010110100101010111010010101011010101001011011010100101101011010100101011101010010011110101000101110110100010110110101001011010110101010010110101101001010110110100100111011010010011011101001001101101100101010101101011001001101101100010'
  + '1011101010100101101101010010101110101001010101101100101010101110100100101111010010010111011000101011011010010101011010101100101010111010010010111101001001011101100100101101110010010110110101010100110110101010010110110101001010111010010101011011010010011011011001010101011100101001011101010100101101101010001101110101001010110110010101010110101010101010101101010010101111001001010111010100101011011010010101011100101010101101011010010101011101001010101110010100101110101010010110110101001010110110101001010111010100100111011010001011011101000101101101010101010101011010100101011011010010011101101001001101110100100110111010010011011010101010101011010101010011011011001001011101010100101101101010010101101101001010101110100101010110110100100110110110010010110111000101011011010010101011010110100101010111010010010111101001001011101100100101101101010010101110100110010110101101001010101110101001001111010100100111011010010011011011001010101011100101001011101010100101101101010010101110101001010110110010'
  + '1010101101010101010101011100010010111101001000111101100100011101101010010101101101001010101101011010010101101101001010110110100100111011010010011011011001010101011010101001011101010100101101101010010101101100101010101101010101010101101100101001101110010010101110101001010111010100101011011010010101011010101010101011010110010101011101001001011101100100101110101010010110110101001010110110101001010110111001001101101100100101101101010010101101101010010110101101001010101110100100101111010010010111011001001011011010100101011010101100101011010110010101011101010010011101101001001101110100010110110110010101010110101010010110110101001011011010100101011011010010101101010110010101011011001010011011100100101011101010010011110101001010110110100101010110101010101010101101010100101111010010010111011001001011101010100101101101010010101101101010010101101101001010101110100101010110110010100110110101010011010110101010010111010101000111011010010011011101001001101101010101010101101010101001101011010100101011'
  + '1010100010111101010001101101101000110101110010101010110101100100110110110010011010111001010010111010101001011011010100101011011010010101011101010001011101101000101101110010010110110101001010110101011001010101101101001001110110100100111011010001011011011000101101101010101001101101010100101101101010010101110101001010110110101001010110110100101010110110010100110111001010010111011000101011101010010101101100101010101101010101010101011011001001011101100100101110110010010110110100101010111010010101011010110100101010111010010101011101001010011101010101001101101010101001101101010100101110101010001110110100100110111010010011011010101010101010110101010010110110101001010111010100010111101010001011101100100110101101010101010110101100100110101110010100101110101010010111010101001011011010100101011011010100101011101010001011101101000101101110010010110110101001010110101011010010101101101001001110110100010110111010001011011010100101011011010101001101010110100101011101010010101101101010001101110101000110'
  + '1101101001010101101100101001110101100010101110110001010110110100101010111001010101011010101010101010111010010010111011001000111101010010011101101001010101101010101010101101011001010101110100101001110110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010'
  + '1010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010101010101011'
  + '1010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010101010111010101010101010101010101010101010111010101010101010101010101010101010111010101010101010'
  + '1010101010101010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101110101010101010101010101010101010101110101010101010101010101010101010101110101010101010101010101110101010'
  + '1010101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101010101010101011101010101010101010101011101010101010101010101010';
const ISLAMIC_UMQURA_STARTS = [1948440];
function islamicUmQuraMonthLength(year, month) {
  return ISLAMIC_UMALQURA_MONTHS.charCodeAt((year - 1) * 12 + month) === 49 ? 30 : 29;
}
function islamicUmQuraYearLength(year) {
  let n = 0;
  for (let m = 0; m < 12; m++) n += islamicUmQuraMonthLength(year, m);
  return n;
}
function islamicUmQuraYearStart(year) {
  while (ISLAMIC_UMQURA_STARTS.length < year) {
    const n = ISLAMIC_UMQURA_STARTS.length;
    ISLAMIC_UMQURA_STARTS.push(ISLAMIC_UMQURA_STARTS[n - 1] + islamicUmQuraYearLength(n));
  }
  return ISLAMIC_UMQURA_STARTS[year - 1];
}
// The civil (tabular) variants share one arithmetic; the epoch is the only
// difference (islamic-civil uses 1948440, islamic-tbla the astronomical one).
function islamicCivilParts(jd, epoch) {
  const days = jd - epoch;
  const year = Math.floor((30 * days + 10646) / 10631);
  let rem = days - (354 * (year - 1) + Math.floor((3 + 11 * year) / 30));
  let month = 0;
  while (month < 12) {
    let len = 29 + (month + 1) % 2;
    if (month === 11 && (14 + 11 * year) % 30 < 11) len++;
    if (rem < len) break;
    rem -= len;
    month++;
  }
  return { year: year, month: month, day: rem + 1, era: 1 };
}

function islamicUmQuraParts(jd, epoch) {
  let year = Math.floor((30 * (jd - epoch) + 10646) / 10631);
  if (year < 1 || year > 2000) return islamicCivilParts(jd, epoch);
  while (islamicUmQuraYearStart(year) > jd) year--;
  while (islamicUmQuraYearStart(year + 1) <= jd) year++;
  let rem = jd - islamicUmQuraYearStart(year);
  let month = 0;
  while (month < 12) {
    const len = islamicUmQuraMonthLength(year, month);
    if (rem < len) break;
    rem -= len;
    month++;
  }
  return { year: year, month: month, day: rem + 1, era: 1 };
}

// mode: 'astronomical' (islamic, islamic-rgsa), 'civil', 'tbla', 'umalqura'
function islamicParts(jd, mode) {
  if (mode === 'civil') return islamicCivilParts(jd, 1948440);
  if (mode === 'tbla') return islamicCivilParts(jd, 1948439);
  if (mode === 'umalqura') return islamicUmQuraParts(jd, 1948440);
  let year = Math.floor((jd - ISLAMIC_START1) / 354) + 1;
  if (year >= ISLAMIC_LO && year <= 2000) {
    while (year > ISLAMIC_LO && islamicYearStart(year) > jd) year--;
    while (year < 2000 && islamicYearStart(year + 1) <= jd) year++;
    if (islamicYearStart(year) <= jd && islamicYearStart(year + 1) > jd) {
      let rem = jd - islamicYearStart(year);
      let month = 0;
      while (month < 12) {
        const len = islamicMonthLength(year, month);
        if (rem < len) break;
        rem -= len;
        month++;
      }
      return { year: year, month: month, day: rem + 1, era: 1 };
    }
  }
  return islamicCivilParts(jd, 1948439);
}

// ICU's Persian calendar counts era years from 1 Farvardin 1 AP (JDN 1947955,
// 621-03-21 Gregorian proleptic); year 0 precedes it and earlier years go
// negative.  ICU computes Nowruz astronomically, so the leap pattern is not a
// fixed cycle; it is captured as one character per year ('1' = 366 days)
// starting at PERSIAN_LO.
const PERSIAN_LO = -8000;
const PERSIAN_START0 = 1947955;
const PERSIAN_LEN = '0001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100'
  + '0100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001'
  + '0001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100'
  + '0100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000'
  + '1000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010'
  + '0010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000'
  + '1000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001'
  + '0001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100'
  + '0100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001'
  + '0001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000010001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010'
  + '0010001000100010001000100010000100010001000100010001000100001000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000100001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000'
  + '1000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010'
  + '0010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000'
  + '1000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001000100010001000100010001000010001'
  + '0001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100010001000100010000100010001000100'
  + '0100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001000100001000100010001000100010001';
const PERSIAN_MONTHS = [31, 31, 31, 31, 31, 31, 30, 30, 30, 30, 30, 29];
function persianYearLength(year) {
  return PERSIAN_LEN.charCodeAt(year - PERSIAN_LO) === 49 ? 366 : 365;
}
const PERSIAN_CUM = [0];
function persianCum(i) {
  while (PERSIAN_CUM.length <= i) {
    const n = PERSIAN_CUM.length;
    PERSIAN_CUM.push(PERSIAN_CUM[n - 1] + persianYearLength(n - 1));
  }
  return PERSIAN_CUM[i];
}
const PERSIAN_NEG = [0];
function persianNeg(i) {
  while (PERSIAN_NEG.length <= i) {
    const n = PERSIAN_NEG.length;
    PERSIAN_NEG.push(PERSIAN_NEG[n - 1] + persianYearLength(-n));
  }
  return PERSIAN_NEG[i];
}
function persianYearStart(year) {
  return year >= 0 ? PERSIAN_START0 + persianCum(year)
    : PERSIAN_START0 - persianNeg(-year);
}
function persianParts(jd) {
  let year = Math.round((jd - PERSIAN_START0) / 365.2422);
  while (persianYearStart(year) > jd) year--;
  while (persianYearStart(year + 1) <= jd) year++;
  let rem = jd - persianYearStart(year);
  let month = 0;
  while (month < 12) {
    let len = PERSIAN_MONTHS[month];
    if (month === 11 && persianYearLength(year) === 366) len++;
    if (rem < len) break;
    rem -= len;
    month++;
  }
  return { year: year, month: month, day: rem + 1, era: 0 };
}

// Indian national (Shaka) calendar: 1 Chaitra falls on 21 or 22 March of the
// Gregorian year that carries the Saka year plus 78.
const INDIAN_MONTHS = [30, 31, 31, 31, 31, 31, 30, 30, 30, 30, 30, 30];
function indianParts(gy, m0, d, jd) {
  const leap = isGregLeap(gy);
  const chaitra1 = toJD(gy, 2, leap ? 21 : 22);
  const saka = jd >= chaitra1 ? gy - 78 : gy - 79;
  const start = toJD(saka + 78, 2, isGregLeap(saka + 78) ? 21 : 22);
  let rem = Math.round(jd - start);
  let month = 0;
  while (month < 12) {
    const len = INDIAN_MONTHS[month] + (month === 0 && isGregLeap(saka + 78) ? 1 : 0);
    if (rem < len) break;
    rem -= len;
    month++;
  }
  return { year: saka, month: month, day: rem + 1, era: 0 };
}

// Coptic and Ethiopic calendars share the 12 x 30 + 5 layout; the Ethiopic
// year is the Coptic one plus 276.
const COPTIC_EPOCH = 1825030;
function fixedEpochParts(epoch, jd) {
  let year = Math.floor((jd - epoch) / 365) + 1;
  while (toFixedJD(epoch, year, 1, 1) > jd) year--;
  while (toFixedJD(epoch, year + 1, 1, 1) <= jd) year++;
  let rem = jd - toFixedJD(epoch, year, 1, 1);
  let month = Math.floor(rem / 30);
  if (month > 12) month = 12;
  return { year: year, month: month, day: rem - month * 30 + 1, era: year >= 1 ? 1 : 0 };
}
function toFixedJD(epoch, year, month, day) {
  return epoch + 365 * (year - 1) + Math.floor(year / 4) + 30 * (month - 1) + day - 1;
}
function copticParts(jd) {
  const p = fixedEpochParts(COPTIC_EPOCH, jd);
  // ICU numbers the years before the epoch positively (1 - eraYear) and shows
  // no era for them.
  if (p.era === 0) p.year = 1 - p.year;
  return p;
}
function ethiopicParts(jd) {
  const p = fixedEpochParts(COPTIC_EPOCH, jd);
  p.year += p.era === 0 ? 5776 : 276;
  return p;
}

// The Republic of China calendar counts from 1912.
// The ROC calendar is the Julian calendar before the Gregorian reform (1582-10-15)
// and the Gregorian one after; the year is counted from 1912.
function julianFromJDN(jdn) {
  const c = jdn + 32082;
  const d = Math.floor((4 * c + 3) / 1461);
  const e = c - Math.floor(1461 * d / 4);
  const m = Math.floor((5 * e + 2) / 153);
  const day = e - Math.floor((153 * m + 2) / 5) + 1;
  const month = m + 3 - 12 * Math.floor(m / 10);
  return { year: d - 4800 + Math.floor(m / 10), month: month - 1, day: day };
}
function rocParts(jd, gy, m0, d) {
  if (jd < 2299161) {
    const j = julianFromJDN(jd);
    return { year: 1912 - j.year, month: j.month, day: j.day, era: 0 };
  }
  const minguo = gy - 1911;
  if (minguo >= 1) return { year: minguo, month: m0, day: d, era: 1 };
  return { year: 1912 - gy, month: m0, day: d, era: 0 };
}

// ICU's Japanese era table: [start y, m0, d, abbreviated, narrow].  The
// pre-Meiji eras carry their year range in every width.
const JAPANESE_ERAS = [
  [1596, 9, 27, 'Keichō (1596–1615)', 'Keichō (1596–1615)'],
  [1688, 8, 30, 'Genroku (1688–1704)', 'Genroku (1688–1704)'],
  [1704, 2, 13, 'Hōei (1704–1711)', 'Hōei (1704–1711)'],
  [1711, 3, 25, 'Shōtoku (1711–1716)', 'Shōtoku (1711–1716)'],
  [1716, 5, 22, 'Kyōhō (1716–1736)', 'Kyōhō (1716–1736)'],
  [1736, 3, 28, 'Genbun (1736–1741)', 'Genbun (1736–1741)'],
  [1741, 1, 27, 'Kanpō (1741–1744)', 'Kanpō (1741–1744)'],
  [1744, 1, 21, 'Enkyō (1744–1748)', 'Enkyō (1744–1748)'],
  [1748, 6, 12, 'Kan’en (1748–1751)', 'Kan’en (1748–1751)'],
  [1751, 9, 27, 'Hōreki (1751–1764)', 'Hōreki (1751–1764)'],
  [1764, 5, 2, 'Meiwa (1764–1772)', 'Meiwa (1764–1772)'],
  [1772, 10, 16, 'An’ei (1772–1781)', 'An’ei (1772–1781)'],
  [1781, 3, 2, 'Tenmei (1781–1789)', 'Tenmei (1781–1789)'],
  [1789, 0, 25, 'Kansei (1789–1801)', 'Kansei (1789–1801)'],
  [1801, 1, 5, 'Kyōwa (1801–1804)', 'Kyōwa (1801–1804)'],
  [1818, 3, 22, 'Bunsei (1818–1830)', 'Bunsei (1818–1830)'],
  [1830, 11, 10, 'Tenpō (1830–1844)', 'Tenpō (1830–1844)'],
  [1844, 11, 2, 'Kōka (1844–1848)', 'Kōka (1844–1848)'],
  [1848, 1, 28, 'Kaei (1848–1854)', 'Kaei (1848–1854)'],
  [1854, 10, 27, 'Ansei (1854–1860)', 'Ansei (1854–1860)'],
  [1860, 2, 18, 'Man’en (1860–1861)', 'Man’en (1860–1861)'],
  [1861, 2, 19, 'Bunkyū (1861–1864)', 'Bunkyū (1861–1864)'],
  [1864, 1, 20, 'Genji (1864–1865)', 'Genji (1864–1865)'],
  [1865, 3, 7, 'Keiō (1865–1868)', 'Keiō (1865–1868)'],
  [1868, 9, 23, 'Meiji', 'M'],
  [1912, 6, 30, 'Taishō', 'T'],
  [1926, 11, 25, 'Shōwa', 'S'],
  [1989, 0, 8, 'Heisei', 'H'],
  [2019, 4, 1, 'Reiwa', 'R'],
];
const JAPANESE_EARLIEST = [645, 5, 24, 'Taika (645–650)', 'Taika (645–650)'];
const JAPANESE_START = JAPANESE_ERAS.map((e) => toJD(e[0], e[1], e[2]));
function japaneseEraIndex(jd) {
  let idx = 0;
  for (let i = 0; i < JAPANESE_START.length; i++) {
    if (jd >= JAPANESE_START[i]) idx = i + 1;
  }
  return idx;
}
function japaneseEraText(idx, width) {
  const entry = idx === 0 ? JAPANESE_EARLIEST : JAPANESE_ERAS[idx - 1];
  return width >= 5 ? entry[4] : entry[3];
}
function japaneseEraYear(idx, gy) {
  const start = idx === 0 ? JAPANESE_EARLIEST : JAPANESE_ERAS[idx - 1];
  return gy - start[0] + 1;
}

// The calendar fields of a non-Gregorian calendar, replacing the Gregorian
// ones: [year, month (0-based), day, era index].
function calendarParts(cal, g) {
  const gy = g.era === 0 ? 1 - g.year : g.year;
  const jd = toJD(gy, g.month, g.day);
  switch (cal) {
    case 'islamic': case 'islamic-rgsa': return islamicParts(jd, 'astronomical');
    case 'islamic-civil': return islamicParts(jd, 'civil');
    case 'islamic-tbla': return islamicParts(jd, 'tbla');
    case 'islamic-umalqura': return islamicParts(jd, 'umalqura');
    case 'persian': return persianParts(jd);
    case 'indian': return indianParts(gy, g.month, g.day, jd);
    case 'coptic': return copticParts(jd);
    case 'ethiopic': case 'ethioaa': return ethiopicParts(jd);
    case 'roc': return rocParts(jd, gy, g.month, g.day);
    case 'japanese': {
      const idx = japaneseEraIndex(jd);
      return { year: japaneseEraYear(idx, gy), month: g.month, day: g.day, era: idx };
    }
    case 'buddhist': return { year: gy + 543, month: g.month, day: g.day, era: 0 };
    default: return { year: gy, month: g.month, day: g.day, era: g.era };
  }
}

// Month names of the calendars whose months differ from the Gregorian ones.
const CAL_MONTHS = {
  islamic: {
    a: ['Muh.', 'Saf.', 'Rab. I', 'Rab. II', 'Jum. I', 'Jum. II', 'Raj.',
      'Sha.', 'Ram.', 'Shaw.', 'Dhuʻl-Q.', 'Dhuʻl-H.'],
    w: ['Muharram', 'Safar', 'Rabiʻ I', 'Rabiʻ II', 'Jumada I', 'Jumada II',
      'Rajab', 'Shaʻban', 'Ramadan', 'Shawwal', 'Dhuʻl-Qiʻdah', 'Dhuʻl-Hijjah'],
    n: ['1', '2', '3', '4', '5', '6', '7', '8', '9', '10', '11', '12'],
  },
  persian: {
    a: ['Farvardin', 'Ordibehesht', 'Khordad', 'Tir', 'Mordad', 'Shahrivar',
      'Mehr', 'Aban', 'Azar', 'Dey', 'Bahman', 'Esfand'],
    w: ['Farvardin', 'Ordibehesht', 'Khordad', 'Tir', 'Mordad', 'Shahrivar',
      'Mehr', 'Aban', 'Azar', 'Dey', 'Bahman', 'Esfand'],
    n: ['1', '2', '3', '4', '5', '6', '7', '8', '9', '10', '11', '12'],
  },
  indian: {
    a: ['Chaitra', 'Vaisakha', 'Jyaistha', 'Asadha', 'Sravana', 'Bhadra',
      'Asvina', 'Kartika', 'Agrahayana', 'Pausa', 'Magha', 'Phalguna'],
    w: ['Chaitra', 'Vaisakha', 'Jyaistha', 'Asadha', 'Sravana', 'Bhadra',
      'Asvina', 'Kartika', 'Agrahayana', 'Pausa', 'Magha', 'Phalguna'],
    n: ['1', '2', '3', '4', '5', '6', '7', '8', '9', '10', '11', '12'],
  },
  coptic: {
    a: ['Tout', 'Baba', 'Hator', 'Kiahk', 'Toba', 'Amshir', 'Baramhat',
      'Baramouda', 'Bashans', 'Paona', 'Epep', 'Mesra', 'Nasie'],
    w: ['Tout', 'Baba', 'Hator', 'Kiahk', 'Toba', 'Amshir', 'Baramhat',
      'Baramouda', 'Bashans', 'Paona', 'Epep', 'Mesra', 'Nasie'],
    n: ['1', '2', '3', '4', '5', '6', '7', '8', '9', '10', '11', '12', '13'],
  },
  ethiopic: {
    a: ['Meskerem', 'Tekemt', 'Hedar', 'Tahsas', 'Ter', 'Yekatit', 'Megabit',
      'Miazia', 'Genbot', 'Sene', 'Hamle', 'Nehasse', 'Pagumen'],
    w: ['Meskerem', 'Tekemt', 'Hedar', 'Tahsas', 'Ter', 'Yekatit', 'Megabit',
      'Miazia', 'Genbot', 'Sene', 'Hamle', 'Nehasse', 'Pagumen'],
    n: ['1', '2', '3', '4', '5', '6', '7', '8', '9', '10', '11', '12', '13'],
  },
};

// Era names of the non-Gregorian calendars: [abbreviated, wide, narrow] lists
// indexed by era.  ICU's Japanese era carries its own name.
const CAL_ERAS = {
  islamic: {
    a: ['BH', 'AH'], w: ['Before Hijrah', 'Anno Hegirae'], n: ['BH', 'AH'],
  },
  persian: { a: ['AP'], w: ['AP'], n: ['AP'] },
  roc: { a: ['B.R.O.C.', 'Minguo'], w: ['B.R.O.C.', 'Minguo'], n: ['B.R.O.C.', 'Minguo'] },
  indian: { a: ['Śaka'], w: ['Śaka'], n: ['Śaka'] },
  coptic: { a: ['', 'AM'], w: ['', 'Anno Martyrum'], n: ['', 'AM'] },
  ethiopic: { a: ['AA', 'AM'], w: ['AA', 'AM'], n: ['AA', 'AM'] },
  buddhist: { a: ['BE'], w: ['BE'], n: ['BE'] },
};

function calendarEraText(cal, width, era) {
  if (cal === 'japanese') return japaneseEraText(era, width);
  const e = CAL_ERAS[cal];
  if (!e) return null;
  const list = width <= 3 ? e.a : width === 4 ? e.w : e.n;
  const v = list[era];
  return v === undefined ? null : v;
}

function calendarMonthText(cal, width, month) {
  const e = CAL_MONTHS[cal];
  if (!e) return null;
  const list = width === 3 ? e.a : width === 4 ? e.w : e.n;
  return list[month] !== undefined ? list[month] : null;
}

// ICU renders the year of a non-Gregorian calendar together with its era, even
// when the caller asked for no era at all.  The Japanese calendar keeps the era
// in the skeleton (so resolvedOptions reports it) and uses the narrow form.
function withCalendarEra(tokens, cal, want, sameAsDefault) {
  let hasYear = false;
  let hasEra = false;
  for (let i = 0; i < tokens.length; i++) {
    const t = tokens[i];
    if (!t.f) continue;
    if (t.f === 'y' || t.f === 'Y' || t.f === 'u' || t.f === 'U') hasYear = true;
    if (t.f === 'G') hasEra = true;
  }
  if (hasEra) return tokens;
  let width;
  let calEra = false;
  if (cal && cal !== 'gregory') {
    // ICU serves the locale's own calendar data when that calendar is the
    // default for the locale, so the pattern already carries (or omits) the era.
    if (sameAsDefault) return tokens;
    if (!hasYear) return tokens;
    width = cal === 'japanese' ? 5 : 1;
    calEra = true;
  } else {
    width = -1;
    if (want) for (let i = 0; i < want.length; i++) if (want[i].f === 'G') width = want[i].n;
    if (width < 0) return tokens;
  }
  const out = [];
  for (let i = 0; i < tokens.length; i++) {
    const t = tokens[i];
    out.push(t);
    if (calEra && t.f && (t.f === 'y' || t.f === 'Y' || t.f === 'u' || t.f === 'U')) {
      out.push({ l: ' ' }, { f: 'G', n: width, calEra: cal !== 'japanese' });
    }
  }
  if (!calEra) {
    if (out.length) out.push({ l: ' ' });
    out.push({ f: 'G', n: width });
  }
  return out;
}

// ICU's Buddhist calendar counts years from 543 CE and only ever shows the
// "BE" era.
function calendarYear(b, g, cal) {
  return g.year;
}


function monthText(b, width, month, standalone) {
  if (width <= 2) return null;
  let list = null;
  if (standalone) {
    list = width === 3 ? b.d.mxs : width === 4 ? b.d.mxl : b.d.mxn;
  }
  if (!list) list = width === 3 ? b.d.ms : width === 4 ? b.d.ml : b.d.mn;
  if (!list) list = b.d.ms;
  return (list && list[month]) || null;
}

function weekdayText(b, width, weekday, standalone) {
  if (width <= 2) width = 3;
  let list = null;
  if (standalone) {
    list = width === 3 ? b.d.wxs : width === 4 ? b.d.wxl : b.d.wxn;
  }
  if (!list) list = width === 3 ? b.d.ws : width === 4 ? b.d.wl : b.d.wn;
  if (!list) list = b.d.ws;
  return (list && list[weekday]) || null;
}

function dayPeriodText(b, width, hour) {
  // ICU picks the wide names for four letters, the narrow ones for five and
  // the abbreviated ones for everything else.
  const list = (width === 4 ? b.d.daw : width === 5 ? b.d.dan : b.d.dap)
    || b.d.dap || ['AM', 'PM'];
  const text = hour < 12 ? list[0] : list[1];
  // ICU spells the narrow no-break space CLDR puts in day periods as a space.
  if (typeof text === 'string') return normalizeNBSP(text);
  return text;
}

// The flexible day period names ("B"), indexed like ICU's DayPeriod.
function flexibleDayPeriodName(b, width, period) {
  const list = width === 4 ? b.d.bpw : width === 5 ? b.d.bpn : b.d.bpa;
  if (!list) return null;
  return list[period] || null;
}

// ICU's day period rules (dayperiodrules.cpp): the flexible day period of an
// hour, or -1 when the locale has no rule for it.
function dayPeriodRule(b, hour) {
  const set = b.d.bpr;
  if (set === undefined) return -1;
  const rules = supp()._dayPeriodRules;
  if (!rules) return -1;
  const entry = rules[set];
  if (!entry) return -1;
  const period = entry[0][hour];
  return period === 10 || period === 11 ? -1 : period;
}

// Bit 1: the rules know a midnight, bit 2: they know a noon.
function dayPeriodRuleFlags(b) {
  const set = b.d.bpr;
  if (set === undefined) return 0;
  const rules = supp()._dayPeriodRules;
  if (!rules) return 0;
  const entry = rules[set];
  return entry ? entry[1] : 0;
}

function quarterText(b, width, month) {
  const q = Math.floor(month / 3);
  if (width <= 2) return String(q + 1);
  const list = (width === 3 ? b.d.qs : width === 4 ? b.d.ql : b.d.qn) || b.d.qs;
  return (list && list[q]) || String(q + 1);
}

function timeZoneText(b, g, kind, n) {
  const offset = -g.tzOffset;
  const sign = offset < 0 ? '-' : '+';
  const abs = Math.abs(offset);
  const hours = Math.floor(abs / 60);
  const minutes = abs % 60;
  if (kind === 'v' || kind === 'O' || kind === 'X' || kind === 'x') {
    // Generic and offset names are always spelled GMT+5 or GMT+05:00.
    if (n >= 4) return 'GMT' + sign + pad(hours, 2) + ':' + pad(minutes, 2);
    return 'GMT' + sign + hours + (minutes ? ':' + minutes : '');
  }
  if (offset === 0) {
    // ICU spells the full UTC name out in the locale's language; the other
    // widths fall back to the abbreviation (ponytail: exemplar city names are
    // not available).
    const zone = b.d.tz || {};
    if (n >= 4) return zone.long || 'Coordinated Universal Time';
    if (n === 2 || n === 3) return 'GMT';
    return zone.short || 'UTC';
  }
  // ponytail: only the offset form is available, exemplar names are not.
  return 'GMT' + sign + pad(hours, 2) + ':' + pad(minutes, 2);
}

// Renders one field run.  Returns null when the caller should fall back to the
// numeric form (month/weekday names missing for a locale).
function fieldText(b, token, g, ctx) {
  const f = token.f;
  const n = token.n;
  const standalone = ctx || { month: false, weekday: false, minute: false, second: false };
  switch (f) {
    case 'G': {
      const v = eraText(b, n, g.era, ctx && ctx.cal);
      // ICU drops the era part entirely when the calendar names it with an
      // empty string (the Coptic years before the epoch).
      return v === '' ? null : v;
    }
    case 'y': case 'Y': case 'u': {
      const y = calendarYear(b, g, ctx && ctx.cal);
      const two = ((y % 100) + 100) % 100;
      return n === 2 ? pad(two, 2) : String(y);
    }
    case 'Q': case 'q': return quarterText(b, n, g.month);
    case 'M': case 'L': {
      // "L" is the stand-alone month, "M" the format context one.
      const cal = ctx && ctx.cal;
      const named = cal ? calendarMonthText(cal, n, g.month) : null;
      if (named !== null) return named;
      const name = monthText(b, n, g.month, f === 'L' || standalone.month);
      if (name !== null) return name;
      return n === 2 ? pad(g.month + 1, 2) : String(g.month + 1);
    }
    case 'd': return n >= 2 ? pad(g.day, 2) : String(g.day);
    case 'D': {
      const start = Date.UTC(g.year, g.month, g.day);
      const base = Date.UTC(g.year, 0, 1);
      const ordinal = Math.round((start - base) / 86400000) + 1;
      return n >= 3 ? pad(ordinal, 3) : n === 2 ? pad(ordinal, 2) : String(ordinal);
    }
    case 'F': return String(Math.ceil(g.day / 7));
    case 'w': {
      const jan1 = new Date(Date.UTC(g.year, 0, 1));
      const first = jan1.getUTCDay();
      const doy = Math.round((Date.UTC(g.year, g.month, g.day) - jan1.getTime()) / 86400000);
      return String(Math.floor((doy + first) / 7) + 1);
    }
    case 'W': return String(Math.ceil((g.day + 6) / 7));
    case 'E': case 'e': case 'c': {
      const name = weekdayText(b, n, g.weekday, standalone.weekday);
      if (name !== null) return name;
      return String(g.weekday + 1);
    }
    case 'a': return dayPeriodText(b, n, g.hour);
    case 'b': {
      // "b" is noon at exactly 12:00 (the minute and second only matter when
      // the pattern shows them), AM/PM otherwise.
      const minute = standalone.minute ? g.minute : 0;
      const second = standalone.second ? g.second : 0;
      if (g.hour === 12 && minute === 0 && second === 0) {
        const name = flexibleDayPeriodName(b, n, 1);
        if (name !== null) return name;
      }
      return dayPeriodText(b, n, g.hour);
    }
    case 'B': {
      // The flexible day period.  ICU suppresses its midnight text, so 00:00
      // falls back to the hour's own period; noon is kept when the locale has
      // a name for it.  Without a name the field falls back to AM/PM.
      const minute = standalone.minute ? g.minute : 0;
      const second = standalone.second ? g.second : 0;
      const flags = dayPeriodRuleFlags(b);
      let period = (g.hour === 12 && minute === 0 && second === 0 && (flags & 2))
        ? 1 : dayPeriodRule(b, g.hour);
      let name = period >= 0 ? flexibleDayPeriodName(b, n, period) : null;
      if (name === null && period === 1) {
        const hour = dayPeriodRule(b, g.hour);
        name = hour >= 0 ? flexibleDayPeriodName(b, n, hour) : null;
      }
      if (name === null) return dayPeriodText(b, n, g.hour);
      return name;
    }
    case 'h': return n >= 2 ? pad(g.hour12, 2) : String(g.hour12);
    case 'K': return n >= 2 ? pad(g.hour11, 2) : String(g.hour11);
    case 'H': return n >= 2 ? pad(g.hour, 2) : String(g.hour);
    case 'k': return n >= 2 ? pad(g.hour24, 2) : String(g.hour24);
    case 'm': return n >= 2 ? pad(g.minute, 2) : String(g.minute);
    case 's': return n >= 2 ? pad(g.second, 2) : String(g.second);
    case 'S': {
      let text = pad(g.ms, 3);
      while (text.length < n) text += '0';
      return text.slice(0, n);
    }
    case 'A': return String(g.hour * 3600000 + g.minute * 60000 + g.second * 1000 + g.ms);
    default:
      if (f === 'z' || f === 'v' || f === 'O' || f === 'X') return timeZoneText(b, g, f, n);
      return null;
  }
}

const PART_TYPE = {
  G: 'era', y: 'year', Y: 'year', u: 'year', Q: 'quarter', q: 'quarter',
  M: 'month', L: 'month', w: 'weekOfYear', W: 'weekOfMonth', E: 'weekday',
  e: 'weekday', c: 'weekday', d: 'day', D: 'dayOfYear', F: 'dayOfWeekInMonth',
  a: 'dayPeriod', b: 'dayPeriod', B: 'dayPeriod', h: 'hour', K: 'hour',
  H: 'hour', k: 'hour', m: 'minute', s: 'second', S: 'fractionalSecond',
  A: 'dayPeriod', z: 'timeZoneName', Z: 'timeZoneName', O: 'timeZoneName',
  v: 'timeZoneName', V: 'timeZoneName', X: 'timeZoneName', x: 'timeZoneName',
};

// Rewrites the Latin digits of a numeric field to the digits of the numbering
// system in use.
function localDigits(text, digits) {
  if (!digits || digits === '0123456789') return text;
  let out = '';
  for (let i = 0; i < text.length; i++) {
    const c = text.charCodeAt(i);
    out += (c >= 48 && c <= 57) ? digits.charAt(c - 48) : text.charAt(i);
  }
  return out;
}

function renderTokens(tokens, b, g, ctx) {
  const parts = [];
  for (let i = 0; i < tokens.length; i++) {
    const t = tokens[i];
    if (t.f) {
      let value = fieldText(b, t, g, ctx);
      if (value === null) continue;
      // A numeric field renders digits in the numbering system of the locale.
      if (dtBase(t.f, t.n) > 0) value = localDigits(value, ctx && ctx.digits);
      parts.push({ type: PART_TYPE[t.f] || 'literal', value: value });
    } else if (t.l) {
      // ICU keeps every run of literal text between fields as one part.
      const last = parts[parts.length - 1];
      if (last && last.type === 'literal' && last._lit) last.value += t.l;
      else parts.push({ type: 'literal', value: t.l, _lit: true });
    }
  }
  for (let i = 0; i < parts.length; i++) delete parts[i]._lit;
  return parts;
}

function partsText(parts) {
  let out = '';
  for (let i = 0; i < parts.length; i++) out += parts[i].value;
  return out;
}

// Rewrites a 12/24 hour pattern to match the requested hour cycle.
// The hour field letter ICU uses to render each cycle.
const HOUR_LETTER = { h11: 'K', h12: 'h', h23: 'H', h24: 'k' };
// The letter ICU's generator matches a cycle against: h11 and h24 have no
// patterns of their own, so they fall back to the locale's plain h/H.
const HOUR_PATTERN_LETTER = { h11: 'h', h12: 'h', h23: 'H', h24: 'H' };

// ICU's generator has no fractional-second patterns; it appends the fraction
// right after the seconds field, separated by the locale's decimal separator.
function withFractionalSeconds(tokens, digits, dec) {
  if (digits === undefined) return tokens;
  let hasSecond = false;
  for (let i = 0; i < tokens.length; i++) {
    if (tokens[i].f === 'S') return tokens;
    if (tokens[i].f === 's') hasSecond = true;
  }
  if (!hasSecond) return tokens;
  const out = [];
  for (let i = 0; i < tokens.length; i++) {
    out.push(tokens[i]);
    if (tokens[i].f === 's') out.push({ l: dec || '.' }, { f: 'S', n: digits });
  }
  return out;
}

// Rewrites the pattern's hour field to the letter of the wanted cycle and
// drops the day period when the clock runs on 24 hours.
function applyHourCycle(tokens, cycle) {
  const hour12 = cycle === 'h11' || cycle === 'h12';
  const letter = HOUR_LETTER[cycle] || 'H';
  const out = [];
  let hourIndex = -1;
  let hasPeriod = false;
  for (let i = 0; i < tokens.length; i++) {
    const t = tokens[i];
    if (!t.f) { out.push(t); continue; }
    if (t.f === 'a' || t.f === 'b' || t.f === 'B') {
      // The pattern's own day period letter is kept: "B" (flexible day period)
      // is not the same as "a" (AM/PM).  It only goes away with a 24 hour
      // clock.
      if (hour12) { out.push({ f: t.f, n: t.n }); hasPeriod = true; }
      continue;
    }
    if (t.f === 'h' || t.f === 'K' || t.f === 'k' || t.f === 'H') {
      out.push({ f: letter, n: t.n });
      hourIndex = out.length - 1;
      continue;
    }
    out.push(t);
  }
  if (hour12 && !hasPeriod && hourIndex >= 0) {
    out.splice(hourIndex + 1, 0, { l: ' ' }, { f: 'a', n: 1 });
  }
  return out;
}

function hasHour(tokens) {
  for (let i = 0; i < tokens.length; i++) {
    const f = tokens[i].f;
    if (f === 'h' || f === 'H' || f === 'k' || f === 'K') return true;
  }
  return false;
}

function hasDateField(tokens) {
  for (let i = 0; i < tokens.length; i++) {
    const t = tokens[i];
    if (!t.f) continue;
    const k = fieldKey(t.f);
    if (k === 'G' || k === 'y' || k === 'Q' || k === 'M' || k === 'd' || k === 'E' || k === 'w') {
      return true;
    }
  }
  return false;
}

function hasTimeField(tokens) {
  for (let i = 0; i < tokens.length; i++) {
    if (tokens[i].f && isTimeField(tokens[i].f)) return true;
  }
  return false;
}

function hasWeekday(tokens) {
  for (let i = 0; i < tokens.length; i++) {
    if (tokens[i].f && fieldKey(tokens[i].f) === 'E') return true;
  }
  return false;
}

// The month width of a pattern, normalised the way the interval tables key it.
function monthWidth(tokens) {
  for (let i = 0; i < tokens.length; i++) {
    const t = tokens[i];
    if (!t.f || fieldKey(t.f) !== 'M') continue;
    return t.n >= 3 ? 3 : 1;
  }
  return 0;
}

// The text CLDR puts between the two dates of an interval fallback.
function intervalSeparator(fallback) {
  if (typeof fallback === 'string') {
    const m = /^\{0\}([\s\S]*)\{1\}$/.exec(fallback);
    if (m && m[1]) return m[1];
  }
  return ' \u2013 ';
}

function isTimeField(f) {
  const k = fieldKey(f);
  return k === 'H' || k === 'h' || k === 'm' || k === 's' || k === 'S'
    || k === 'a' || k === 'z';
}

// CLDR dateTimeFormats styles, used to glue a date pattern to a time pattern.
function dateStyleOf(pattern) {
  const tokens = parseDatePattern(pattern);
  let wide = false;
  let abbreviated = false;
  for (let i = 0; i < tokens.length; i++) {
    const t = tokens[i];
    if (!t.f || fieldKey(t.f) !== 'M') continue;
    if (t.n >= 4) wide = true;
    else if (t.n === 3) abbreviated = true;
  }
  if (wide) return 'long';
  return abbreviated ? 'medium' : 'short';
}

function timeStyleOf(pattern) {
  const tokens = parseDatePattern(pattern);
  let seconds = false;
  let zone = 0;
  for (let i = 0; i < tokens.length; i++) {
    const t = tokens[i];
    if (!t.f) continue;
    const k = fieldKey(t.f);
    if (k === 's' || t.f === 'S') seconds = true;
    if (k === 'z') zone = Math.max(zone, t.n);
  }
  if (zone >= 4) return 'full';
  if (zone > 0) return 'long';
  return seconds ? 'medium' : 'short';
}

function patternUsesHour12(tokens) {
  for (let i = 0; i < tokens.length; i++) {
    const f = tokens[i].f;
    if (f === 'h' || f === 'K') return true;
    if (f === 'H' || f === 'k') return false;
  }
  return true;
}

function repeatField(letter, n) {
  let out = '';
  for (let i = 0; i < n; i++) out += letter;
  return out;
}

// ---- ICU date pattern generation ----------------------------------------
// Mirrors ICU's DateTimePatternGenerator: the requested fields form a
// skeleton, the availableFormats entry with the smallest field distance wins,
// and the widths of the found pattern are rewritten for the requested fields.

const DT_NUMERIC = 0x100, DT_SHORT = -0x103, DT_LONG = -0x104;
const DT_NARROW = -0x101, DT_SHORTER = -0x102, DT_DELTA = 0x10;

// The ICU field a pattern letter belongs to.
function icuField(f) {
  switch (f) {
    case 'G': return 'era';
    case 'y': case 'Y': case 'u': case 'U': case 'r': return 'year';
    case 'Q': case 'q': return 'quarter';
    case 'M': case 'L': case 'l': return 'month';
    case 'W': return 'weekOfMonth';
    case 'F': return 'dayOfWeekInMonth';
    case 'd': return 'day';
    case 'D': return 'dayOfYear';
    case 'E': case 'c': case 'e': return 'weekday';
    case 'a': case 'b': case 'B': return 'dayperiod';
    case 'h': case 'H': case 'k': case 'K': case 'j': case 'J': case 'C': return 'hour';
    case 'm': return 'minute';
    case 's': case 'A': return 'second';
    case 'S': return 'fractional';
    case 'w': return 'week';
    default: return 'zone';
  }
}

// The ICU base type of a pattern field; positive values are numeric fields.
function dtBase(f, n) {
  switch (f) {
    case 'G': return n < 4 ? DT_SHORT : (n === 4 ? DT_LONG : DT_NARROW);
    case 'y': return DT_NUMERIC;
    case 'Y': return DT_NUMERIC + DT_DELTA;
    case 'u': return DT_NUMERIC + 2 * DT_DELTA;
    case 'U': return n < 4 ? DT_SHORT : (n === 4 ? DT_LONG : DT_NARROW);
    case 'r': return DT_NUMERIC + 3 * DT_DELTA;
    case 'Q': return n < 3 ? DT_NUMERIC : (n === 3 ? DT_SHORT : (n === 4 ? DT_LONG : DT_NARROW));
    case 'q': return n < 3 ? DT_NUMERIC + DT_DELTA
      : (n === 3 ? DT_SHORT - DT_DELTA : (n === 4 ? DT_LONG - DT_DELTA : DT_NARROW - DT_DELTA));
    case 'M': return n < 3 ? DT_NUMERIC : (n === 3 ? DT_SHORT : (n === 4 ? DT_LONG : DT_NARROW));
    case 'L': return n < 3 ? DT_NUMERIC + DT_DELTA
      : (n === 3 ? DT_SHORT - DT_DELTA : (n === 4 ? DT_LONG - DT_DELTA : DT_NARROW - DT_DELTA));
    case 'l': return DT_NUMERIC + DT_DELTA;
    case 'E': return n < 4 ? DT_SHORT : (n === 4 ? DT_LONG : (n === 5 ? DT_NARROW : DT_SHORTER));
    case 'c': return n < 3 ? DT_NUMERIC + 2 * DT_DELTA
      : (n === 3 ? DT_SHORT - 2 * DT_DELTA : (n === 4 ? DT_LONG - 2 * DT_DELTA
        : (n === 5 ? DT_NARROW - 2 * DT_DELTA : DT_SHORTER - 2 * DT_DELTA)));
    case 'e': return n < 3 ? DT_NUMERIC + DT_DELTA
      : (n === 3 ? DT_SHORT - DT_DELTA : (n === 4 ? DT_LONG - DT_DELTA
        : (n === 5 ? DT_NARROW - DT_DELTA : DT_SHORTER - DT_DELTA)));
    case 'd': return n < 3 ? DT_NUMERIC : DT_SHORT;
    case 'a': return n < 4 ? DT_SHORT : (n === 4 ? DT_LONG : DT_NARROW);
    case 'b': return n < 4 ? DT_SHORT - DT_DELTA : (n === 4 ? DT_LONG - DT_DELTA : DT_NARROW - DT_DELTA);
    case 'B': return n < 4 ? DT_SHORT - 3 * DT_DELTA
      : (n === 4 ? DT_LONG - 3 * DT_DELTA : DT_NARROW - 3 * DT_DELTA);
    case 'h': return DT_NUMERIC;
    case 'H': return DT_NUMERIC + 10 * DT_DELTA;
    case 'k': return DT_NUMERIC + 11 * DT_DELTA;
    case 'K': return DT_NUMERIC + DT_DELTA;
    case 'v': return n < 4 ? DT_SHORT - 2 * DT_DELTA : DT_LONG - 2 * DT_DELTA;
    case 'z': return n < 4 ? DT_SHORT : DT_LONG;
    case 'Z': return n < 4 ? DT_NARROW - DT_DELTA : (n === 4 ? DT_LONG - DT_DELTA : DT_SHORT - DT_DELTA);
    case 'O': return n < 4 ? DT_SHORT - DT_DELTA : DT_LONG - DT_DELTA;
    case 'V': return n < 4 ? DT_SHORT - DT_DELTA : (n === 4 ? DT_LONG - 2 * DT_DELTA : DT_LONG - DT_DELTA);
    case 'X': case 'x': return n < 4 ? DT_NARROW - DT_DELTA : (n === 4 ? DT_LONG - DT_DELTA : DT_SHORT - DT_DELTA);
    default: return DT_NUMERIC;
  }
}

// ICU's dtTypes minimum length of a field: the base skeleton repeats the
// field character this many times.
function dtMinLen(f, n) {
  switch (f) {
    case 'G': case 'U': case 'a': case 'b': case 'B':
      return n < 4 ? 1 : (n === 4 ? 4 : 5);
    case 'Q': case 'q': case 'M': case 'L':
      return n < 3 ? 1 : (n === 3 ? 3 : (n === 4 ? 4 : 5));
    case 'E':
      return n < 4 ? 1 : (n === 4 ? 4 : (n === 5 ? 5 : 6));
    case 'c': case 'e':
      return n < 3 ? 1 : (n === 3 ? 3 : (n === 4 ? 4 : (n === 5 ? 5 : 6)));
    case 'd':
      return n < 3 ? 1 : 3;
    case 'v': case 'z':
      return n < 4 ? 1 : 4;
    case 'Z':
      return n < 4 ? 1 : (n === 4 ? 4 : 5);
    case 'O':
      return n < 4 ? 1 : 4;
    case 'V':
      return n < 3 ? n : 4;
    case 'X': case 'x':
      return n < 3 ? 1 : 4;
    default:
      return 1;
  }
}

function fieldEntry(f, n) {
  const base = dtBase(f, n);
  return { letter: f, n: n, base: base, type: base > 0 ? base + n : base };
}

// The fields of a skeleton or pattern, keyed by ICU field.
function skeletonFields(text) {
  const tokens = parseDatePattern(text);
  const out = {};
  for (let i = 0; i < tokens.length; i++) {
    const t = tokens[i];
    if (!t.f) continue;
    const field = icuField(t.f);
    if (out[field]) continue;
    out[field] = fieldEntry(t.f, t.n);
  }
  return out;
}

// ICU's DateTimeMatcher::set injects the default day period into a 12 hour
// skeleton and strips it from a 24 hour one.
function applyDayPeriodRule(fields) {
  const hour = fields.hour;
  if (!hour) return fields;
  if (hour.letter === 'h' || hour.letter === 'K') {
    if (!fields.dayperiod) fields.dayperiod = fieldEntry('a', 1);
  } else {
    delete fields.dayperiod;
  }
  return fields;
}

function requestSkeleton(want, hourLetter) {
  const out = {};
  for (let i = 0; i < want.length; i++) {
    const t = want[i];
    let f = t.f;
    if (f === 'h' || f === 'H' || f === 'k' || f === 'K') f = hourLetter;
    const field = icuField(f);
    if (out[field]) continue;
    out[field] = fieldEntry(f, t.n);
  }
  return applyDayPeriodRule(out);
}

// The canonical skeleton of a field set: the ICU field order with each
// field's canonical character.  The first character decides the map bucket,
// which is the order ICU walks the candidates in.
// The ICU field order; it decides the order of the base skeleton.
const FIELD_CANON = [
  'era', 'year', 'quarter', 'month', 'week', 'weekOfMonth', 'dayOfWeekInMonth',
  'day', 'dayOfYear', 'weekday', 'dayperiod', 'hour', 'minute', 'second',
  'fractional', 'zone',
];

const FIELD_INDEX = {
  era: 0, year: 1, quarter: 2, month: 3, week: 4, weekOfMonth: 5,
  dayOfWeekInMonth: 6, day: 7, dayOfYear: 8, weekday: 9, dayperiod: 10,
  hour: 11, minute: 12, second: 13, fractional: 14, zone: 15,
};

// ICU's canonical items: one single field pattern per UDATPG field.
const CANONICAL_ITEMS = [
  'G', 'y', 'Q', 'M', 'w', 'W', 'E', 'D', 'F', 'd', 'a', 'H', 'm', 's', 'S', 'v',
];

function canonicalSkeleton(fields) {
  let out = '';
  for (let i = 0; i < FIELD_CANON.length; i++) {
    const f = fields[FIELD_CANON[i]];
    if (!f) continue;
    out += f.letter + repeatChar(f.letter, dtMinLen(f.letter, f.n));
  }
  return out;
}

function repeatChar(letter, n) {
  let out = '';
  for (let i = 0; i < n; i++) out += letter;
  return out;
}

// The map key of an entry: the base skeleton plus the field types.
function skeletonKey(fields) {
  let out = '';
  for (let i = 0; i < FIELD_CANON.length; i++) {
    const f = fields[FIELD_CANON[i]];
    out += (f ? f.type : 0) + ',';
  }
  return out;
}

// ICU buckets the map by the first character of the base skeleton, upper
// case letters first.
function bucketIndex(base) {
  const c = base.charCodeAt(0);
  if (c >= 65 && c <= 90) return c - 65;
  if (c >= 97 && c <= 122) return 26 + c - 97;
  return 52;
}

// ICU's pattern map holds the locale's availableFormats entries (whose
// skeleton is the map key) and its standard date and time format patterns
// (whose skeleton is derived from the pattern).  Both share one map, and an
// entry is replaced when a later one has the same base skeleton and the same
// field types.
function dtpgCandidates(b) {
  if (b._dtpg) return b._dtpg;
  const entries = [];
  const index = {};
  const stdBases = {};
  const addEntry = (text, fields, spec) => {
    text = normalizeNBSP(text);
    const base = canonicalSkeleton(fields);
    const key = base + '|' + skeletonKey(fields);
    const hit = index[key];
    if (hit) { hit.text = text; hit.spec = spec; return; }
    const e = { text: text, fields: fields, spec: spec, base: base };
    index[key] = e;
    entries.push(e);
  };
  // ICU adds the canonical items first: one single field pattern per field,
  // in the UDATPG field order.
  for (let i = 0; i < CANONICAL_ITEMS.length; i++) {
    const letter = CANONICAL_ITEMS[i];
    const fields = {};
    fields[icuField(letter)] = fieldEntry(letter, 1);
    addEntry(letter, fields, null);
  }
  // ICU adds the standard date and time patterns next and never lets a
  // later style replace an earlier one with the same base skeleton.
  const styles = ['full', 'long', 'medium', 'short'];
  const tables = [b.d.df, b.d.tf];
  for (let i = 0; i < tables.length; i++) {
    const table = tables[i];
    if (!table) continue;
    for (let j = 0; j < styles.length; j++) {
      const text = table[styles[j]];
      if (typeof text !== 'string' || !text) continue;
      const fields = applyDayPeriodRule(skeletonFields(text));
      const base = canonicalSkeleton(fields);
      if (stdBases[base]) continue;
      stdBases[base] = true;
      addEntry(text, fields, null);
    }
  }
  const af = b.d.af || {};
  const keys = Object.keys(af);
  for (let i = 0; i < keys.length; i++) {
    const key = keys[i];
    if (key.indexOf('-count-') >= 0) continue;
    const text = af[key];
    if (typeof text !== 'string' || !text) continue;
    const fields = applyDayPeriodRule(skeletonFields(key));
    addEntry(text, fields, fields);
  }
  const buckets = [];
  for (let i = 0; i < 53; i++) buckets.push([]);
  for (let i = 0; i < entries.length; i++) {
    buckets[bucketIndex(entries[i].base)].push(entries[i]);
  }
  b._dtpg = [].concat.apply([], buckets);
  return b._dtpg;
}

const MISSING_FIELD = 0x1000, EXTRA_FIELD = 0x10000;

function dtpgDistance(req, cand, includeMask) {
  let dist = 0;
  let missing = 0;
  let extra = 0;
  for (const f in req) {
    const bit = 1 << FIELD_INDEX[f];
    const mine = (includeMask & bit) === 0 ? 0 : req[f].type;
    const theirs = cand[f] ? cand[f].type : 0;
    if (mine === theirs) continue;
    if (mine === 0) { dist += EXTRA_FIELD; extra |= bit; }
    else if (theirs === 0) { dist += MISSING_FIELD; missing |= bit; }
    else dist += Math.abs(mine - theirs);
  }
  for (const f in cand) {
    if (req[f]) continue;
    dist += EXTRA_FIELD;
    extra |= 1 << FIELD_INDEX[f];
  }
  return { dist: dist, missing: missing, extra: extra };
}

// ICU's yearLengthIsCompatible: a two digit year only matches patterns whose
// own year is two digits as well.
function yearCompatible(req, cand) {
  const ry = req.year;
  const cy = cand.year;
  if (!ry || !cy || ry.type <= 0 || cy.type <= 0) return true;
  return cy.n !== 2 || ry.n === 2;
}

// ICU's getBestRaw: the smallest field distance wins, ties go to the smaller
// missing field mask, and the walk stops at distance 0.
function dtpgLookup(b, want, hourLetter) {
  const req = requestSkeleton(want, hourLetter);
  const cands = dtpgCandidates(b);
  let best = null;
  let bestDist = Infinity;
  let bestMissing = -1;
  for (let i = 0; i < cands.length; i++) {
    const c = cands[i];
    if (!yearCompatible(req, c.fields)) continue;
    const d = dtpgDistance(req, c.fields, -1);
    if (d.dist < bestDist || (d.dist === bestDist && bestMissing < d.missing)) {
      bestDist = d.dist;
      bestMissing = d.missing;
      best = { text: c.text, fields: c.fields, spec: c.spec, info: d };
      if (d.dist === 0) break;
    }
  }
  return best;
}

// ICU's adjustFieldTypes: rewrite the widths of a found pattern so that the
// requested widths win, except where the locale data or the field type has
// the last word.
function adjustFieldTypes(tokens, req, spec, plain) {
  const out = [];
  for (let i = 0; i < tokens.length; i++) {
    const t = tokens[i];
    if (!t.f) { out.push(t); continue; }
    const field = icuField(t.f);
    const r = req[field];
    if (!r) { out.push(t); continue; }
    let reqLen = r.n;
    if (r.letter === 'E' && reqLen < 3) reqLen = 3;
    let adjLen = reqLen;
    if ((plain && field === 'hour') || field === 'minute' || field === 'second') {
      // The Intl API only sets UDATPG_MATCH_HOUR_FIELD_LENGTH, so these keep
      // the length of the locale data.  The interval fallback passes no
      // options at all, so the hour keeps its own length too.
      adjLen = t.n;
    } else if (spec && r.letter !== 'c' && r.letter !== 'e') {
      const skelLen = spec[field] ? spec[field].n : 0;
      const patNumeric = dtBase(t.f, t.n) > 0;
      const reqNumeric = r.base > 0;
      if (skelLen === reqLen || (patNumeric && !reqNumeric) || (reqNumeric && !patNumeric)) {
        adjLen = t.n;
      }
    }
    let c = t.f;
    if (field !== 'hour' && field !== 'month' && field !== 'weekday'
      && (field !== 'year' || r.letter === 'Y')) {
      c = r.letter;
    }
    if (c === 'E' && adjLen < 3) c = 'e';
    out.push({ f: c, n: adjLen });
  }
  return out;
}

// Builds a pattern when the locale has no usable entry at all.
function synthesize(want) {
  let out = '';
  for (let i = 0; i < want.length; i++) {
    const t = want[i];
    if (i) out += t.f === 'd' || t.f === 'y' || t.f === 'M' ? ' ' : ' ';
    out += repeatField(t.f, t.n);
  }
  return out;
}

// CLDR glues a date pattern and a time pattern with literal text; the text
// has to be quoted so that letters in it are not read as pattern fields.
function quoteDateLiteral(text) {
  if (!/[A-Za-z']/.test(text)) return text;
  return "'" + text.replace(/'/g, "''") + "'";
}

// The date+time glue is a SimpleFormatter pattern: {0} is the time, {1} the
// date.  CLDR quotes the literal text, so the quotes come off before the
// literal goes back into the pattern (a literal may hold pattern letters).
function mergeDateTime(glue, dateText, timeText) {
  const g = typeof glue === 'string' && glue ? glue : '{1}, {0}';
  let out = '';
  let i = 0;
  while (i < g.length) {
    if (g.charAt(i) === '{') {
      const end = g.indexOf('}', i);
      if (end > i) {
        const idx = g.charAt(i + 1);
        if (idx === '0') out += timeText;
        else if (idx === '1') out += dateText;
        else out += g.slice(i, end + 1);
        i = end + 1;
        continue;
      }
    }
    let j = i;
    while (j < g.length && g.charAt(j) !== '{') j++;
    out += quoteDateLiteral(unquotePatternText(g.slice(i, j)));
    i = j;
  }
  return out;
}

function choosePattern(b, want, hourLetter) {
  const found = dtpgLookup(b, want, hourLetter);
  if (found) return found;
  const made = synthesize(want);
  return { text: made, fields: skeletonFields(made), spec: null };
}

// ---- ICU DateIntervalFormat -------------------------------------------------
// ICU splits a pattern's skeleton into a date part and a time part, looks the
// interval table up for the closest matching skeleton, and renders the entry of
// the greatest differing field.  Date-only and time-only patterns both reduce
// to that table; a date+time pattern concatenates the date with the time entry.

// ICU's SimpleDateFormat::isFieldUnitIgnored compares field levels: the larger
// the level, the smaller the field unit, so a field counts as rendered when the
// pattern holds anything as small as it.
const LEVEL = {
  G: 0, y: 10, Y: 10, u: 10, U: 10, Q: 20, q: 20, M: 20, L: 20, W: 30, w: 20,
  D: 30, d: 30, F: 30, E: 20, e: 30, c: 30, a: 40, h: 50, H: 50, K: 50, k: 50,
  m: 60, s: 70, S: 80, A: 40,
};
const CAL_LEVEL = { G: 0, y: 10, M: 20, d: 30, a: 40, h: 50, H: 50, m: 60, s: 70 };

function fieldUnitIgnored(tokens, field) {
  const want = CAL_LEVEL[field];
  for (let i = 0; i < tokens.length; i++) {
    const f = tokens[i].f;
    if (!f) continue;
    const level = LEVEL[f];
    if (level !== undefined && want <= level) return false;
  }
  return true;
}

// ICU's DateIntervalFormat::splitPatternInto2Part: the first repeated field
// letter ends the first half of the pattern.
function icuSplitPattern(pattern) {
  const repeated = {};
  let inQuote = false;
  let prevCh = '';
  let count = 0;
  let foundRepetition = false;
  let i;
  for (i = 0; i < pattern.length; i++) {
    const ch = pattern.charAt(i);
    if (ch !== prevCh && count > 0) {
      if (repeated[prevCh] === undefined) repeated[prevCh] = true;
      else { foundRepetition = true; break; }
      count = 0;
    }
    if (ch === "'") {
      if (i + 1 < pattern.length && pattern.charAt(i + 1) === "'") i++;
      else inQuote = !inQuote;
    } else if (!inQuote && ((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z'))) {
      prevCh = ch;
      count++;
    }
  }
  if (count > 0 && !foundRepetition) {
    if (repeated[prevCh] === undefined) count = 0;
  }
  return i - count;
}

function unquotePatternText(text) {
  let out = '';
  let i = 0;
  while (i < text.length) {
    const ch = text.charAt(i);
    if (ch === "'") {
      if (text.charAt(i + 1) === "'") { out += "'"; i += 2; continue; }
      let j = i + 1;
      while (j < text.length && text.charAt(j) !== "'") j++;
      out += text.slice(i + 1, j);
      i = j + 1;
      continue;
    }
    out += ch;
    i++;
  }
  return out;
}

// ICU's FormattedValueFieldPositionIteratorImpl::addOverlapSpans merges the
// first two occurrences of every duplicated field into one span per side; V8
// then marks a part startRange/endRange only when it lies fully inside a span.
function combineSpans(left, right, gap) {
  const fields = [];
  let pos = 0;
  for (let i = 0; i < left.length; i++) {
    const p = left[i];
    if (p.type !== 'literal') {
      fields.push({ key: p.type, start: pos, limit: pos + p.value.length });
    }
    pos += p.value.length;
  }
  pos += gap;
  for (let i = 0; i < right.length; i++) {
    const p = right[i];
    if (p.type !== 'literal') {
      fields.push({ key: p.type, start: pos, limit: pos + p.value.length });
    }
    pos += p.value.length;
  }
  let s1a = Infinity, s1b = -1, s2a = Infinity, s2b = -1;
  for (let i = 0; i < fields.length; i++) {
    for (let j = i + 1; j < fields.length; j++) {
      if (fields[i].key !== fields[j].key) continue;
      s1a = Math.min(s1a, fields[i].start);
      s1b = Math.max(s1b, fields[i].limit);
      s2a = Math.min(s2a, fields[j].start);
      s2b = Math.max(s2b, fields[j].limit);
      break;
    }
  }
  const has = s1a !== Infinity;
  const contains = (span, start, limit) =>
    has && span[0] <= start && start <= span[1] && span[0] <= limit && limit <= span[1];
  return { has, s1: [s1a, s1b], s2: [s2a, s2b], contains };
}

function sharedParts(parts) {
  const out = [];
  for (let i = 0; i < parts.length; i++) {
    out.push({ type: parts[i].type, value: parts[i].value, source: 'shared' });
  }
  return out;
}

function rangeParts(left, right, middle, after) {
  const span = combineSpans(left, right, middle.length);
  const parts = left.slice();
  if (middle) parts.push({ type: 'literal', value: middle });
  for (let i = 0; i < right.length; i++) parts.push(right[i]);
  if (after) parts.push({ type: 'literal', value: after });
  const out = [];
  let pos = 0;
  for (let i = 0; i < parts.length; i++) {
    const p = parts[i];
    let source = 'shared';
    if (span.has) {
      if (span.contains(span.s1, pos, pos + p.value.length)) source = 'startRange';
      else if (span.contains(span.s2, pos, pos + p.value.length)) source = 'endRange';
    }
    out.push({ type: p.type, value: p.value, source });
    pos += p.value.length;
  }
  return out;
}

class DateTimeFormat {
  constructor(locales, options) {
    const o = options || {};
    this.locales = canonicalizeList(locales);
    this.locale = icuLocale(this.locales[0]);
    this.b = bundle(this.locale);
    this.options = o;
    enumOption(o.localeMatcher, 'localeMatcher', ['lookup', 'best fit'], DTF_CTOR);
    this.cal = resolveCalendar(this.b, o.calendar);
    this.numberingSystem = resolveNumberingSystem(this.b, o.numberingSystem);
    this.digits = digitsFor(this.numberingSystem);
    enumOption(o.hourCycle, 'hourCycle', ['h11', 'h12', 'h23', 'h24'], DTF_CTOR);
    this.timeZone = resolveTimeZone(o.timeZone);
    enumOption(o.weekday, 'weekday', ['narrow', 'short', 'long'], DTF_CTOR);
    enumOption(o.era, 'era', ['narrow', 'short', 'long'], DTF_CTOR);
    enumOption(o.year, 'year', ['numeric', '2-digit'], DTF_CTOR);
    enumOption(o.month, 'month', ['numeric', '2-digit', 'short', 'long', 'narrow'], DTF_CTOR);
    enumOption(o.day, 'day', ['numeric', '2-digit'], DTF_CTOR);
    enumOption(o.dayPeriod, 'dayPeriod', ['short', 'narrow', 'long'], DTF_CTOR);
    enumOption(o.hour, 'hour', ['numeric', '2-digit'], DTF_CTOR);
    enumOption(o.minute, 'minute', ['numeric', '2-digit'], DTF_CTOR);
    enumOption(o.second, 'second', ['numeric', '2-digit'], DTF_CTOR);
    this.fractionalSecondDigits = numberOption(o.fractionalSecondDigits,
      'fractionalSecondDigits', 1, 3);
    enumOption(o.timeZoneName, 'timeZoneName',
      ['short', 'long', 'shortOffset', 'longOffset', 'shortGeneric', 'longGeneric'],
      DTF_CTOR);
    enumOption(o.formatMatcher, 'formatMatcher', ['basic', 'best fit'], DTF_CTOR);
    this.dateStyle = enumOption(o.dateStyle, 'dateStyle',
      ['full', 'long', 'medium', 'short'], DTF_CTOR);
    this.timeStyle = enumOption(o.timeStyle, 'timeStyle',
      ['full', 'long', 'medium', 'short'], DTF_CTOR);
    if (this.dateStyle) {
      for (const key of ['year', 'month', 'day', 'weekday', 'era', 'dayPeriod']) {
        if (o[key] !== undefined) throw new TypeError('Invalid option : option');
      }
    }
    if (this.timeStyle) {
      for (const key of ['hour', 'minute', 'second', 'dayPeriod', 'fractionalSecondDigits',
        'timeZoneName']) {
        if (o[key] !== undefined) throw new TypeError('Invalid option : option');
      }
    }
    this._build();
  }

  _hour12Default() {
    // The locale's own time patterns already encode its preferred hour cycle.
    const tf = this.b.d.tf || {};
    const text = tf.medium || tf.long || tf.short || tf.full || 'H';
    return patternUsesHour12(parseDatePattern(text));
  }

  // The effective hour cycle: "h11", "h12", "h23" or "h24".  The hour12
  // option wins over the hourCycle option.
  _hourCycle() {
    const o = this.options;
    if (o.hour12 !== undefined) return o.hour12 ? 'h12' : 'h23';
    if (o.hourCycle !== undefined) return o.hourCycle;
    return this._hour12Default() ? 'h12' : 'h23';
  }

  _hour12() {
    const c = this._hourCycle();
    return c === 'h11' || c === 'h12';
  }

  _want() {
    const o = this.options;
    const want = [];
    function push(f, value, widths) {
      if (value === undefined) return;
      let n;
      if (typeof value === 'string') {
        n = widths[value] === undefined ? widths.numeric : widths[value];
      } else {
        n = value;
      }
      want.push({ f, n });
    }
    const hour12 = this._hour12();
    for (let i = 0; i < FIELD_ORDER.length; i++) {
      const f = FIELD_ORDER[i];
      switch (f) {
        case 'G':
          push('G', o.era, { short: 1, narrow: 5, long: 4, numeric: 1 });
          break;
        case 'y':
          push('y', o.year, { numeric: 1, '2-digit': 2 });
          break;
        case 'Q':
          break;
        case 'M':
          push('M', o.month, { numeric: 1, '2-digit': 2, short: 3, narrow: 5, long: 4 });
          break;
        case 'w':
          break;
        case 'W':
          break;
        case 'E':
          push('E', o.weekday, { short: 1, narrow: 5, long: 4, numeric: 1 });
          break;
        case 'd':
          push('d', o.day, { numeric: 1, '2-digit': 2 });
          break;
        case 'D':
          break;
        case 'a':
          // V8 maps the dayPeriod option to the flexible day period field.
          push('B', o.dayPeriod, { short: 1, long: 4, narrow: 5 });
          break;
        case 'h':
        case 'H': {
          if (f === 'H' && hour12) break;
          if (f === 'h' && !hour12) break;
          push(HOUR_PATTERN_LETTER[this._hourCycle()], o.hour, { numeric: 1, '2-digit': 2 });
          break;
        }
        case 'm':
          push('m', o.minute, { numeric: 1, '2-digit': 2 });
          break;
        case 's':
          push('s', o.second, { numeric: 1, '2-digit': 2 });
          break;
        case 'S':
          if (this.fractionalSecondDigits !== undefined) {
            want.push({ f: 'S', n: this.fractionalSecondDigits });
          }
          break;
        case 'z':
          if (o.timeZoneName !== undefined) {
            // ICU's skeleton uses these characters for the zone spelling.
            const map = {
              short: ['z', 1], long: ['z', 4],
              shortOffset: ['O', 1], longOffset: ['O', 4],
              shortGeneric: ['v', 1], longGeneric: ['v', 4],
            };
            const entry = map[o.timeZoneName] || ['z', 1];
            want.push({ f: entry[0], n: entry[1] });
          }
          break;
        default:
          break;
      }
    }
    return want;
  }

  _styleTokens(dateStyle, timeStyle) {
    const b = this.b;
    const fallback = record('en') || {};
    const dateTable = b.d.df || fallback.d && fallback.d.df || {};
    const timeTable = b.d.tf || fallback.d && fallback.d.tf || {};
    const glueTable = b.d.dts || fallback.d && fallback.d.dts || {};
    const dpat = dateTable[dateStyle] || 'y';
    const tpat = timeTable[timeStyle] || 'H';
    const glue = glueTable[dateStyle] || glueTable[dateStyle + '|' + timeStyle] || '{1}, {0}';
    return parseDatePattern(mergeDateTime(glue, dpat, tpat));
  }

  _build() {
    const b = this.b;
    const fallback = record('en') || {};
    const dateTable = b.d.df || fallback.d && fallback.d.df || {};
    const timeTable = b.d.tf || fallback.d && fallback.d.tf || {};
    let text = '';
    if (this.dateStyle && this.timeStyle) {
      const dpat = dateTable[this.dateStyle] || 'y';
      const tpat = timeTable[this.timeStyle] || 'H';
      const glueTable = b.d.dts || fallback.d && fallback.d.dts || {};
      const glue = glueTable[this.dateStyle]
        || glueTable[this.dateStyle + '|' + this.timeStyle] || '{1}, {0}';
      text = mergeDateTime(glue, dpat, tpat);
    } else if (this.dateStyle) {
      text = dateTable[this.dateStyle] || 'y';
    } else if (this.timeStyle) {
      text = timeTable[this.timeStyle] || 'H';
    } else {
      let want = this._want();
      // V8 adds the default date fields when none of the date or time
      // components was requested; the era alone does not count.
      if (!want.some((w) => w.f !== 'G')) {
        want.push({ f: 'y', n: 1 }, { f: 'M', n: 1 }, { f: 'd', n: 1 });
      }
      const hourLetter = HOUR_PATTERN_LETTER[this._hourCycle()];
      // ICU tries the whole skeleton first and only splits the date from the
      // time when the best match cannot supply every requested field.
      const whole = dtpgLookup(b, want, hourLetter);
      if (whole && whole.info.missing === 0 && whole.info.extra === 0) {
        text = tokensText(adjustFieldTypes(
          parseDatePattern(whole.text), requestSkeleton(want, hourLetter), whole.spec));
      } else {
        const dateWant = [];
        const timeWant = [];
        for (let i = 0; i < want.length; i++) {
          if (isTimeField(want[i].f)) timeWant.push(want[i]);
          else dateWant.push(want[i]);
        }
        if (dateWant.length && timeWant.length) {
          // ICU builds the date and the time separately and glues them with the
          // dateTimeFormats entry of the matching styles.
          const d = choosePattern(b, dateWant, hourLetter);
          const t = choosePattern(b, timeWant, hourLetter);
          const dpat = tokensText(adjustFieldTypes(
            parseDatePattern(d.text), requestSkeleton(dateWant, hourLetter), d.spec));
          const tpat = tokensText(adjustFieldTypes(
            parseDatePattern(t.text), requestSkeleton(timeWant, hourLetter), t.spec));
          const glueTable = b.d.dts || {};
          const glue = glueTable[dateStyleOf(d.text)]
            || glueTable[dateStyleOf(d.text) + '|' + timeStyleOf(t.text)] || '{1}, {0}';
          text = mergeDateTime(glue, dpat, tpat);
        } else {
          const c = whole || choosePattern(b, want, hourLetter);
          text = tokensText(adjustFieldTypes(
            parseDatePattern(c.text), requestSkeleton(want, hourLetter), c.spec));
        }
      }
      let tokens = parseDatePattern(text);
      if (hasHour(tokens)) tokens = applyHourCycle(tokens, this._hourCycle());
      tokens = withFractionalSeconds(tokens, this.fractionalSecondDigits,
        numberSymbols(b).dec);
      tokens = withCalendarEra(tokens, this.cal, this._want(), !!(this.b && this.b.d && this.b.d.cal === this.cal));
      this.tokens = tokens;
      this._setContext();
      this.hour12Resolved = hasHour(tokens) ? this._hour12() : undefined;
      this.hourCycleResolved = hasHour(tokens) ? this._hourCycle() : undefined;
      return;
    }
    let tokens = parseDatePattern(text);
    if (hasHour(tokens)) {
      tokens = applyHourCycle(tokens, this._hourCycle());
    }
    tokens = withFractionalSeconds(tokens, this.fractionalSecondDigits,
      numberSymbols(b).dec);
    tokens = withCalendarEra(tokens, this.cal, this._want(), !!(this.b && this.b.d && this.b.d.cal === this.cal));
    this.tokens = tokens;
    this._setContext();
    this.hour12Resolved = hasHour(tokens) ? this._hour12() : undefined;
    this.hourCycleResolved = hasHour(tokens) ? this._hourCycle() : undefined;
  }

  // A stand-alone week day field ("cccc") makes the week day use the
  // stand-alone names.  The day period fields ("b", "B") only look at the
  // minute and second when the pattern shows them.
  _setContext() {
    let standaloneWeekday = false;
    let minute = false;
    let second = false;
    for (let i = 0; i < this.tokens.length; i++) {
      const t = this.tokens[i];
      if (!t.f) continue;
      if (t.f === 'c' || t.f === 'e') standaloneWeekday = true;
      if (t.f === 'm') minute = true;
      if (t.f === 's') second = true;
    }
    this.ctx = {
      month: false, weekday: standaloneWeekday, minute, second,
      digits: this.digits, cal: this.cal,
    };
  }

  _fields(value) {
    const date = toDate(value);
    const offset = zoneOffsetMinutes(this.timeZone, date.getTime());
    const g = fieldValues(new Date(date.getTime() - offset * 60000), true);
    g.tzOffset = offset;
    if (this.cal !== 'gregory') {
      const c = calendarParts(this.cal, g);
      g.year = c.year;
      g.month = c.month;
      g.day = c.day;
      g.era = c.era;
    }
    return g;
  }

  format(value) {
    return partsText(this.formatToParts(value));
  }

  formatToParts(value) {
    const date = toDate(value);
    if (Number.isNaN(date.getTime())) throw new RangeError('Invalid time value');
    return renderTokens(this.tokens, this.b, this._fields(date), this.ctx);
  }

  // Greatest field that differs between two dates.  ICU reads the calendar,
  // so a date field counts even when the pattern renders none of it; the time
  // fields only count when the pattern renders them.
  _differing(g1, g2) {
    if (g1.era !== g2.era) return 'G';
    if (g1.year !== g2.year) return 'y';
    if (g1.month !== g2.month) return 'M';
    if (g1.day !== g2.day) return 'd';
    const seen = {};
    for (let i = 0; i < this.tokens.length; i++) {
      const t = this.tokens[i];
      if (t.f) seen[fieldKey(t.f)] = true;
    }
    if (seen.a && (g1.hour < 12) !== (g2.hour < 12)) return 'a';
    if (seen.h && g1.hour !== g2.hour) return 'h';
    if (seen.H && g1.hour !== g2.hour) return 'H';
    if (seen.m && g1.minute !== g2.minute) return 'm';
    if (seen.s && g1.second !== g2.second) return 's';
    return null;
  }

  _intervalTokens(g1, g2, field) {
    const b = this.b;
    const iv = b.d.iv || {};
    const wantWidths = normalizedWidths(this.tokens);
    // Interval tables are keyed by the CLDR skeleton of the pattern; the
    // greatest differing field picks the entry inside it.
    const skeleton = patternSkeleton(this.tokens);
    let combined = null;
    let bestSkeleton = null;
    const exact = iv[skeleton];
    if (exact && typeof exact === 'object') {
      bestSkeleton = skeleton;
      combined = ivFieldPattern(exact, field);
    }
    if (combined === null) {
      // No entry for the exact skeleton: pick the one with the same fields
      // whose month width comes closest to the requested pattern.
      const wantSet = fieldSet(this.tokens);
      const wantMonth = monthWidth(this.tokens);
      let best = null;
      for (const key in iv) {
        const table = iv[key];
        if (!table || typeof table !== 'object') continue;
        if (!ivFieldPattern(table, field)) continue;
        const keyTokens = parseDatePattern(key);
        if (fieldSet(keyTokens) !== wantSet) continue;
        const score = Math.abs(wantMonth - monthWidth(keyTokens));
        if (!best || score < best.score) best = { table, score, key };
      }
      if (best) {
        combined = ivFieldPattern(best.table, field);
        bestSkeleton = best.key;
      }
    }
    if (combined === null && bestSkeleton) {
      // ICU looks the pattern up in the skeleton with the field letter added
      // in front: "MMMd" takes the year from "yMMMd".
      const ext = iv[field + bestSkeleton];
      if (ext && typeof ext === 'object') combined = ivFieldPattern(ext, field);
    }
    if (combined === null) return null;
    // ICU splits at the first repeated field letter, so the separator stays at
    // the end of the first half; that covers separators the fallback list does
    // not know about (zh "至", ko "~").
    const split = icuSplitPattern(combined);
    if (split <= 0 || split >= combined.length) return null;
    let start = parseDatePattern(combined.slice(0, split));
    let end = parseDatePattern(combined.slice(split));
    start = this._widen(start, wantWidths);
    end = this._widen(end, wantWidths);
    return [start, end, ''];
  }

  // Interval patterns carry the skeleton's field widths; the requested pattern
  // supplies the widths ICU actually renders.  A numeric month stays numeric
  // and a stand-alone weekday keeps its own width.
  _widen(tokens, wantWidths) {
    const out = [];
    for (let i = 0; i < tokens.length; i++) {
      const t = tokens[i];
      if (!t.f) { out.push(t); continue; }
      const key = fieldKey(t.f);
      const w = wantWidths[key];
      if (w === undefined) { out.push(t); continue; }
      if (key === 'M' && t.n <= 2) { out.push(t); continue; }
      if (key === 'E' && (t.f === 'c' || t.f === 'e')) { out.push(t); continue; }
      out.push({ f: t.f, n: Math.max(t.n, w) });
    }
    return out;
  }

  _fallbackTokens(g1, g2, tokens) {
    // ICU renders the whole pattern for both dates, the interval separator
    // between them; nothing is shared with the start side.
    const p1 = renderTokens(tokens, this.b, g1, this.ctx);
    const p2 = renderTokens(tokens, this.b, g2, this.ctx);
    return [p1, p2, []];
  }

  _rangeParts(value1, value2) {
    const start = toDate(value1);
    const end = toDate(value2);
    if (Number.isNaN(start.getTime()) || Number.isNaN(end.getTime())) {
      throw new RangeError('Invalid time value');
    }
    if (start.getTime() > end.getTime()) throw new RangeError('Invalid time value');
    const g1 = this._fields(start);
    const g2 = this._fields(end);
    const saved = this.tokens;
    try {
      return this._rangePartsFor(g1, g2);
    } finally {
      this.tokens = saved;
    }
  }

  // Pattern ICU falls back to for an interval field the skeleton lacks and the
  // interval table has no entry for: the date half, the locale's date-time
  // glue, the time half, each looked up in the available formats with the
  // widths the pattern itself used.
  _addedTokens(field) {
    const b = this.b;
    if (!hasTimeField(this.tokens)) return null;
    if (field !== 'd' && field !== 'M' && field !== 'y' && field !== 'G') return null;
    const dateWant = [];
    const timeWant = [];
    let hourLetter = 'H';
    for (let i = 0; i < this.tokens.length; i++) {
      const t = this.tokens[i];
      if (!t.f) continue;
      if (isTimeField(t.f)) {
        timeWant.push({ f: t.f, n: t.n });
        if (t.f === 'h' || t.f === 'H' || t.f === 'k' || t.f === 'K') hourLetter = t.f;
      } else {
        dateWant.push({ f: t.f, n: t.n });
      }
    }
    const has = (f) => {
      for (let i = 0; i < dateWant.length; i++) {
        if (fieldKey(dateWant[i].f) === f) return true;
      }
      return false;
    };
    if (has(field)) return null;
    // ICU prefixes the date fields the skeleton lacks, era first, up to the
    // field that differs; a time-only skeleton gets the whole short date.
    if (dateWant.length === 0) {
      dateWant.push({ f: 'y', n: 1 }, { f: 'M', n: 1 }, { f: 'd', n: 1 });
    } else {
      const prefix = [];
      if (field === 'y' || field === 'G') prefix.push('y');
      if (field !== 'd') prefix.push('M');
      prefix.push('d');
      if (field === 'G') prefix.push('G');
      for (let i = 0; i < prefix.length; i++) {
        if (!has(prefix[i])) dateWant.push({ f: prefix[i], n: 1 });
      }
    }
    const d = choosePattern(b, dateWant, hourLetter);
    const t = choosePattern(b, timeWant, hourLetter);
    if (!d.text || !t.text) return null;
    // The interval fallback calls getBestPattern with no match options, so
    // the hour keeps the length the locale data chose.
    const dpat = tokensText(adjustFieldTypes(
      parseDatePattern(d.text), requestSkeleton(dateWant, hourLetter), d.spec, true));
    const tpat = tokensText(adjustFieldTypes(
      parseDatePattern(t.text), requestSkeleton(timeWant, hourLetter), t.spec, true));
    // ICU picks the glue style from the month width of the requested skeleton.
    const mw = monthWidth(this.tokens);
    const style = mw === 4 ? (hasWeekday(this.tokens) ? 'long' : 'full')
      : (mw === 3 ? 'medium' : 'short');
    const glue = (b.d.dts || {})[style] || (b.d.dts || {}).short;
    let tokens = parseDatePattern(mergeDateTime(glue, dpat, tpat));
    if (hasHour(tokens)) {
      tokens = applyHourCycle(tokens, this._hourCycle());
    }
    return withCalendarEra(tokens, this.cal, this._want(), !!(this.b && this.b.d && this.b.d.cal === this.cal));
  }

  _rangePartsFor(g1, g2) {
    const field = this._differing(g1, g2);
    if (field === null) return sharedParts(renderTokens(this.tokens, this.b, g1, this.ctx));
    const pair = this._intervalTokens(g1, g2, field);
    if (pair) {
      return rangeParts(
        renderTokens(pair[0], this.b, g1, this.ctx),
        renderTokens(pair[1], this.b, g2, this.ctx), pair[2] || '');
    }
    // A field the pattern does not render at all has no interval entry either:
    // ICU then falls back to the single start date.
    const added = this._addedTokens(field);
    const tokens = added === null ? this.tokens : added;
    if (added === null && fieldUnitIgnored(this.tokens, field)) {
      return sharedParts(renderTokens(this.tokens, this.b, g1, this.ctx));
    }
    const glue = (this.b.d.fb || '{0} \u2013 {1}').split('{1}');
    const middle = glue.length > 1 ? glue[0].replace('{0}', '') : '';
    const after = glue.length > 1 ? glue[1] : '';
    const split = this._fallbackTokens(g1, g2, tokens);
    return rangeParts(split[0], split[1], middle, after);
  }

  formatRange(start, end) {
    return partsText(this._rangeParts(start, end));
  }

  formatRangeToParts(start, end) {
    return this._rangeParts(start, end);
  }

  resolvedOptions() {
    const o = this.options;
    const out = {
      locale: this.locale,
      calendar: this.cal,
      numberingSystem: this.numberingSystem,
      timeZone: this.timeZone,
    };
    if (hasHour(this.tokens)) {
      out.hourCycle = this.hourCycleResolved;
      out.hour12 = this.hour12Resolved;
    }
    if (o.dateStyle !== undefined || o.timeStyle !== undefined) {
      if (o.dateStyle !== undefined) out.dateStyle = o.dateStyle;
      if (o.timeStyle !== undefined) out.timeStyle = o.timeStyle;
      return out;
    }
    // ECMA-402 resolves the component fields from the pattern, not the request.
    const widths = {};
    for (let i = 0; i < this.tokens.length; i++) {
      const t = this.tokens[i];
      if (!t.f || t.calEra) continue;
      const key = fieldKey(t.f);
      if (widths[key] === undefined) widths[key] = t.n;
    }
    const numeric = (n) => (n === 2 ? '2-digit' : 'numeric');
    const text = (n, short, long, narrow) => (n >= 5 ? narrow : (n >= 4 ? long : short));
    if (widths.E !== undefined) out.weekday = text(widths.E, 'short', 'long', 'narrow');
    if (widths.G !== undefined) out.era = text(widths.G, 'short', 'long', 'narrow');
    if (widths.y !== undefined) out.year = numeric(widths.y);
    if (widths.M !== undefined) {
      const n = widths.M;
      out.month = n >= 5 ? 'narrow' : (n === 4 ? 'long' : (n === 3 ? 'short' : numeric(n)));
    }
    if (widths.d !== undefined) out.day = numeric(widths.d);
    if (o.dayPeriod !== undefined && widths.a !== undefined) {
      out.dayPeriod = o.dayPeriod;
    }
    const hour = widths.h !== undefined ? widths.h : widths.H;
    if (hour !== undefined) out.hour = numeric(hour);
    if (widths.m !== undefined) out.minute = numeric(widths.m);
    if (widths.s !== undefined) out.second = numeric(widths.s);
    if (this.fractionalSecondDigits !== undefined) {
      out.fractionalSecondDigits = this.fractionalSecondDigits;
    }
    if (o.timeZoneName !== undefined) out.timeZoneName = o.timeZoneName;
    return out;
  }

  static supportedLocalesOf(locales) {
    return supportedOf(locales);
  }
}

// ---------------------------------------------------------------------------
// plural rules
// ---------------------------------------------------------------------------

function operands(value) {
  const num = Math.abs(Number(value));
  let text = String(num);
  if (text.indexOf('e') >= 0 || text.indexOf('E') >= 0) text = expandExp(text);
  const dot = text.indexOf('.');
  const intText = dot < 0 ? text : text.slice(0, dot);
  const fracText = dot < 0 ? '' : text.slice(dot + 1);
  const trimmed = fracText.replace(/0+$/, '');
  const parsed = parseInt(intText, 10);
  return {
    n: num,
    i: Number.isNaN(parsed) ? 0 : parsed,
    v: fracText.length,
    w: trimmed.length,
    f: fracText ? (parseInt(fracText, 10) || 0) : 0,
    t: trimmed ? (parseInt(trimmed, 10) || 0) : 0,
    e: 0,
  };
}

class PluralRules {
  constructor(locales, options) {
    const o = options || {};
    this.locales = canonicalizeList(locales);
    this.locale = icuLocale(this.locales[0]);
    this.b = bundle(this.locale);
    this.type = o.type === 'ordinal' ? 'ordinal' : 'cardinal';
  }

  select(value) {
    const ops = operands(value);
    return intl.sync('plural', [this.b.lang, ops.n, ops.i, ops.v, ops.w,
      ops.f, ops.t, ops.e, this.type]);
  }

  selectRange(start, end) {
    return this.select(start);
  }

  resolvedOptions() {
    return {
      locale: this.b.lang,
      type: this.type,
      minimumIntegerDigits: 1,
      minimumFractionDigits: 0,
      maximumFractionDigits: 3,
      minimumSignificantDigits: 1,
      maximumSignificantDigits: 21,
      roundingMode: 'halfExpand',
      pluralCategories: intl.sync('categories', [this.b.lang, this.type]),
    };
  }

  static supportedLocalesOf(locales) {
    return supportedOf(locales);
  }
}

// ---------------------------------------------------------------------------
// collation, relative time, lists
// ---------------------------------------------------------------------------

class Collator {
  constructor(locales, options) {
    const o = options || {};
    this.locales = canonicalizeList(locales);
    this.locale = icuLocale(this.locales[0]);
    this.b = bundle(this.locale);
    this.sensitivity = o.sensitivity || 'variant';
    this.numeric = !!o.numeric;
    this.caseFirst = o.caseFirst || 'false';
    this.usage = o.usage || 'sort';
  }

  compare(a, b) {
    const r = intl.sync('compare', [this.b.lang, String(a), String(b),
      this.sensitivity, this.numeric]);
    return r < 0 ? -1 : r > 0 ? 1 : 0;
  }

  resolvedOptions() {
    return {
      locale: this.locale,
      usage: this.usage,
      sensitivity: this.sensitivity,
      ignorePunctuation: false,
      collation: 'default',
      numeric: this.numeric,
      caseFirst: this.caseFirst,
    };
  }

  static supportedLocalesOf(locales) {
    return supportedOf(locales);
  }
}

const RELATIVE_UNITS = {
  year: 'year', quarter: 'quarter', month: 'month', week: 'week', day: 'day',
  hour: 'hour', minute: 'minute', second: 'second',
};

class RelativeTimeFormat {
  constructor(locales, options) {
    const o = options || {};
    this.locales = canonicalizeList(locales);
    this.locale = icuLocale(this.locales[0]);
    this.b = bundle(this.locale);
    this.style = o.style || 'long';
    this.numeric = o.numeric === undefined ? 'always' : o.numeric;
  }

  _entry(unit, style) {
    const key = RELATIVE_UNITS[unit];
    if (!key) throw new RangeError('Invalid unit: ' + unit);
    const suffix = style === 'long' ? '' : '-' + style;
    return this.b.r[key + suffix] || this.b.r[key] || null;
  }

  format(value, unit) {
    return partsText(this.formatToParts(value, unit));
  }

  formatToParts(value, unit) {
    const n = Number(value);
    const entry = this._entry(unit, this.style);
    if (!entry) return [{ type: 'literal', value: String(value) }];
    const negativeZero = n === 0 && 1 / n < 0;
    const rounded = n < 0 || negativeZero ? Math.ceil(n) : Math.floor(n);
    if (this.numeric === 'auto' && entry.rel) {
      const word = entry.rel[String(rounded)];
      if (word !== undefined) return [{ type: 'literal', value: word }];
    }
    const key = n < 0 || negativeZero ? 'past' : 'future';
    const table = entry[key] || {};
    const category = this.b.lang ? this.select(n) : 'other';
    const pattern = table[category] || table.other;
    if (pattern === undefined) return [{ type: 'literal', value: String(value) }];
    return templateParts(pattern, Math.abs(rounded));
  }

  select(value) {
    const ops = operands(value);
    return intl.sync('plural', [this.b.lang, ops.n, ops.i, ops.v, ops.w,
      ops.f, ops.t, ops.e, 'cardinal']);
  }

  resolvedOptions() {
    return {
      locale: this.locale,
      style: this.style,
      numeric: this.numeric,
    };
  }

  static supportedLocalesOf(locales) {
    return supportedOf(locales);
  }
}

function templateParts(pattern, n) {
  const parts = [];
  const text = String(pattern);
  let i = 0;
  while (i < text.length) {
    const at = text.indexOf('{0}', i);
    if (at < 0) {
      parts.push({ type: 'literal', value: text.slice(i) });
      break;
    }
    if (at > i) parts.push({ type: 'literal', value: text.slice(i, at) });
    parts.push({ type: 'integer', value: String(n) });
    i = at + 3;
  }
  return parts;
}

function isHebrew(ch) {
  const c = ch.charCodeAt(0);
  return (c >= 0x0590 && c <= 0x05FF) || (c >= 0xFB1D && c <= 0xFB4F);
}

function spanishE(text) {
  if (text.length === 0) return false;
  if ((text.charAt(0) === 'h' || text.charAt(0) === 'H') && text.length > 1
      && (text.charAt(1) === 'i' || text.charAt(1) === 'I')
      && (text.length === 2 || !/[ae]/.test(text.charAt(2).toLowerCase()))) {
    return true;
  }
  return text.charAt(0) === 'i' || text.charAt(0) === 'I';
}

function spanishU(text) {
  if (text.length === 0) return false;
  if (text.charAt(0) === 'o' || text.charAt(0) === 'O' || text.charAt(0) === '8') return true;
  if ((text.charAt(0) === 'h' || text.charAt(0) === 'H')
      && text.length > 1 && (text.charAt(1) === 'o' || text.charAt(1) === 'O')) return true;
  return text.length >= 2 && text.charAt(0) === '1' && text.charAt(1) === '1'
    && (text.length === 2 || text.charAt(2) === ' ');
}

class ListFormat {
  constructor(locales, options) {
    const o = options || {};
    this.locales = canonicalizeList(locales);
    this.locale = icuLocale(this.locales[0]);
    this.b = bundle(this.locale);
    this.style = o.style || 'long';
    this.type = o.type || 'conjunction';
  }

  _patterns() {
    const table = this.b.l;
    const type = this.type === 'disjunction' ? 'or'
      : this.type === 'unit' ? 'unit' : 'standard';
    const style = this.style === 'long' ? '' : '-' + this.style;
    return table[type + style] || table[type] || table.standard
      || { 2: '{0}, {1}', start: '{0}, {1}', middle: '{0}, {1}', end: '{0}, {1}' };
  }

  _format(items) {
    const pats = this._patterns();
    if (items.length === 1) return String(items[0]);
    if (items.length === 2) {
      const pat = this._conjunction(pats['2'], String(items[1]));
      return pat.replace('{0}', String(items[0])).replace('{1}', String(items[1]));
    }
    let out = pats.start.replace('{0}', String(items[0]));
    out = out.replace('{1}', String(items[1]));
    for (let i = 2; i < items.length - 1; i++) {
      out = pats.middle.replace('{0}', out).replace('{1}', String(items[i]));
    }
    const end = this._conjunction(pats.end, String(items[items.length - 1]));
    return end.replace('{0}', out).replace('{1}', String(items[items.length - 1]));
  }

  // ICU swaps the conjunction for Spanish ("y"->"e", "o"->"u") and inserts a
  // dash after the Hebrew vav when the next item does not start with a Hebrew
  // letter (listformatter.cpp).
  _conjunction(pattern, next) {
    const lang = (this.locale.split('-')[0] || '').toLowerCase();
    if (lang === 'he' || lang === 'iw') {
      if (pattern === '{0} ו{1}' && next.length > 0 && !isHebrew(next.charAt(0))) {
        return '{0} ו-{1}';
      }
    } else if (lang === 'es') {
      if (pattern === '{0} y {1}' && spanishE(next)) return '{0} e {1}';
      if (pattern === '{0} o {1}' && spanishU(next)) return '{0} u {1}';
    }
    return pattern;
  }

  format(items) {
    const list = Array.isArray(items) ? items : [items];
    return this._format(list);
  }

  formatToParts(items) {
    const list = Array.isArray(items) ? items : [items];
    return [{ type: 'literal', value: this._format(list) }];
  }

  resolvedOptions() {
    return { locale: this.locale, type: this.type, style: this.style };
  }

  static supportedLocalesOf(locales) {
    return supportedOf(locales);
  }
}

// ---------------------------------------------------------------------------
// Intl.Locale
// ---------------------------------------------------------------------------

function tagInfo(tag) {
  const raw = intl.sync('tagInfo', [tag]);
  return raw ? JSON.parse(raw) : { language: tag, script: '', region: '', variants: '' };
}

class Locale {
  constructor(tag) {
    const text = canonical(tag);
    if (text === null) throw new RangeError('Invalid language tag: ' + tag);
    this._tag = text;
    const info = tagInfo(text);
    this._info = info;
    // Parse the -u- extension: keyword/value pairs after the 'u' singleton.
    const parts = text.split('-');
    let key = null;
    this._numberingSystem = null;
    this._calendar = null;
    this._script = '';
    this._region = '';
    this._variant = '';
    for (let i = 0; i < parts.length; i++) {
      if (parts[i].length === 1) {
        if (parts[i] === 'u' && i + 1 < parts.length) {
          for (let j = i + 1; j < parts.length; j++) {
            const sub = parts[j];
            if (sub.length === 1) break;
            if (key === null) key = sub;
            else {
              if (key === 'nu') this._numberingSystem = sub;
              if (key === 'ca') this._calendar = sub;
              key = null;
            }
          }
        }
        break;
      }
      if (i === 0) continue;
      const sub = parts[i];
      if (sub.length === 4) this._script = sub.charAt(0).toUpperCase() + sub.slice(1).toLowerCase();
      else if (sub.length === 2 || /^[0-9]{3}$/.test(sub)) this._region = sub.toUpperCase();
      else this._variant = sub.toLowerCase();
    }
  }

  toString() { return this._tag; }

  get baseName() {
    return this._tag.split('-u-')[0].split('-t-')[0].split('-x-')[0];
  }

  get language() { return this._info.language; }
  get script() { return this._script || undefined; }
  get region() { return this._region || undefined; }
  get variant() { return this._variant || undefined; }
  get calendar() { return this._calendar || undefined; }
  get numberingSystem() { return this._numberingSystem || undefined; }

  maximize() {
    const chain = lookupChain(this._tag);
    for (let i = 0; i < chain.length; i++) if (record(chain[i])) return new Locale(chain[i]);
    return new Locale('en');
  }

  minimize() {
    return new Locale(this._info.language || this._tag);
  }
}

// ---------------------------------------------------------------------------
// Intl object
// ---------------------------------------------------------------------------

const SUPPORTED_KEYS = ['calendar', 'collation', 'currency', 'numberingSystem',
  'timeZone', 'unit'];

const Intl = {
  NumberFormat,
  DateTimeFormat,
  PluralRules,
  Collator,
  RelativeTimeFormat,
  ListFormat,
  Locale,
  getCanonicalLocales: canonicalizeList,
  supportedValuesOf(key) {
    const name = String(key);
    if (SUPPORTED_KEYS.indexOf(name) < 0) {
      throw new RangeError('Invalid key : ' + name);
    }
    const list = supp()[name];
    return Array.isArray(list) ? list.slice() : [];
  },
};

module.exports = Intl;

// Route the locale aware builtins through this implementation so that they
// behave like Node's instead of falling back to plain toString().
Number.prototype.toLocaleString = function (locales, options) {
  return new NumberFormat(locales, options).format(this.valueOf());
};
// toLocale* mirrors Node's ToDateTimeOptions: an explicit field wins over the
// defaults, and the defaults are only filled for the parts the method needs.
const DATE_FIELDS = ['year', 'month', 'day', 'weekday', 'era', 'dayPeriod',
  'dateStyle', 'quarter', 'weekOfYear', 'weekOfMonth', 'dayOfYear'];
const TIME_FIELDS = ['hour', 'minute', 'second', 'timeStyle', 'fractionalSecondDigits',
  'timeZoneName'];

function localeDate(date, locales, options, fillDate, fillTime) {
  const o = {};
  const given = options || {};
  for (const key in given) o[key] = given[key];
  const hasDate = DATE_FIELDS.some((k) => o[k] !== undefined);
  const hasTime = TIME_FIELDS.some((k) => o[k] !== undefined);
  if (fillDate && !hasDate) {
    o.year = 'numeric';
    o.month = 'numeric';
    o.day = 'numeric';
  }
  if (fillTime && !hasTime) {
    o.hour = 'numeric';
    o.minute = 'numeric';
    o.second = 'numeric';
  }
  return new DateTimeFormat(locales, o).format(date.valueOf());
}

Date.prototype.toLocaleString = function (locales, options) {
  const o = {};
  const given = options || {};
  for (const key in given) o[key] = given[key];
  const hasAny = DATE_FIELDS.concat(TIME_FIELDS).some((k) => o[k] !== undefined);
  if (hasAny) return localeDate(this, locales, o, false, false);
  return localeDate(this, locales, o, true, true);
};
Date.prototype.toLocaleDateString = function (locales, options) {
  return localeDate(this, locales, options, true, false);
};
Date.prototype.toLocaleTimeString = function (locales, options) {
  return localeDate(this, locales, options, false, true);
};
