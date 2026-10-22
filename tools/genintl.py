#!/usr/bin/env python3
"""Generate internal/vm/intl_data.go from CLDR (unicode-org/cldr-json).

Downloads CLDR number/date/list/relative-time data for every locale that has
a directory under cldr-numbers-full/main, shrinks it to what the Intl
implementation reads, and emits one gzip+base64 blob per locale so the runtime
only inflates the locales it actually uses.

Usage:  python3 tools/genintl.py
Env:    GENINTL_OUT   output file (default internal/vm/intl_data.go)
        GENINTL_LOCALES  comma separated locale list override
"""
import gzip
import json
import os
import re
import struct
import sys
import urllib.request
from concurrent.futures import ThreadPoolExecutor

BASE = "https://raw.githubusercontent.com/unicode-org/cldr-json/main/cldr-json/"
CACHE = os.path.join(os.environ.get("GENINTL_CACHE", "/tmp/cldr-cache"))
HERE = os.path.dirname(os.path.abspath(__file__))
OUT = os.environ.get("GENINTL_OUT", os.path.join(HERE, "..", "internal", "vm", "intl_data.go"))

# ICU's dateStyle+timeStyle glue ("{1} at {0}"), captured from Node because
# cldr-json does not export dateTimeFormats-atTime.
_DTS_PATH = os.path.join(HERE, "intl_dts.json")
if os.path.exists(_DTS_PATH):
    with open(_DTS_PATH, encoding="utf-8") as _fh:
        DTS_JSON = json.load(_fh)
else:
    DTS_JSON = {}

# Localized names of the UTC time zone, captured from ICU (Node).
_TZ_PATH = os.path.join(HERE, "intl_tz.json")
if os.path.exists(_TZ_PATH):
    with open(_TZ_PATH, encoding="utf-8") as _fh:
        TZ_JSON = json.load(_fh)
else:
    TZ_JSON = {}

# Calendar ICU resolves as default for each locale (captured from Node).
# Only the Buddhist calendar is actually rendered; every other non-Gregorian
# default falls back to the Gregorian data below (see _CAL_OFFSETS).
_CAL_PATH = os.path.join(HERE, "intl_cal.json")
if os.path.exists(_CAL_PATH):
    with open(_CAL_PATH, encoding="utf-8") as _fh:
        CAL_JSON = json.load(_fh)
else:
    CAL_JSON = {}

# ICU ships currency data only for the locales in its curr directory.  A locale
# without one falls back to its parent (ar-EG -> ar) and ultimately to the
# default locale (en); a locale with data but no entry for a currency serves the
# ISO code from root instead.  List captured from the ICU curr directory.
_CURR_LOCALES_PATH = os.path.join(HERE, "intl_currlocales.json")
if os.path.exists(_CURR_LOCALES_PATH):
    with open(_CURR_LOCALES_PATH, encoding="utf-8") as _fh:
        CURR_LOCALES = set(json.load(_fh))
else:
    CURR_LOCALES = set()

# The locales ICU ships data for.  A requested locale outside this set falls
# back to the default locale, exactly like Node (ve/lld/ts/... -> en-US).
_ICU_LOCALES_PATH = os.path.join(HERE, "intl_iculocales.json")
if os.path.exists(_ICU_LOCALES_PATH):
    with open(_ICU_LOCALES_PATH, encoding="utf-8") as _fh:
        ICU_LOCALES = set(json.load(_fh))
else:
    ICU_LOCALES = set()


def effective_currency_locale(loc):
    """The locale whose ICU currency data ICU actually serves for |loc|."""
    if loc in CURR_LOCALES:
        return loc
    parts = loc.split("-")
    for i in range(len(parts) - 1, 0, -1):
        cand = "-".join(parts[:i])
        if cand in CURR_LOCALES:
            return cand
    return "en"

# Calendar type -> (cldr package suffix, year offset).  ICU's Buddhist
# calendar adds 543 to the Gregorian year and only ever shows era "BE".
_CAL_OFFSETS = {
    "buddhist": ("buddhist", 543),
}

# The day period rules (hour -> flexible day period) are ICU data, not CLDR
# JSON: ICU ships them in icu4c/source/data/misc/dayPeriods.txt.
_ICU_DAY_PERIODS = ("https://raw.githubusercontent.com/unicode-org/icu/"
                    "release-78.3/icu4c/source/data/misc/dayPeriods.txt")

# The day period names in ICU's order (DayPeriodRules::DayPeriod).  The last
# two, "am"/"pm", are the plain AM/PM names.
_DAY_PERIODS = ("midnight", "noon", "morning1", "afternoon1", "evening1", "night1",
                "morning2", "afternoon2", "evening2", "night2", "am", "pm")

# Supplementary data fetched once.
SINGLETONS = {
    "plurals": "cldr-core/supplemental/plurals.json",
    "ordinals": "cldr-core/supplemental/ordinals.json",
    "numberingSystems": "cldr-core/supplemental/numberingSystems.json",
    "timeData": "cldr-core/supplemental/timeData.json",
    "currencyFractions": "cldr-core/supplemental/currencyData.json",
    "weekData": "cldr-core/supplemental/weekData.json",
    "calendarPreference": "cldr-core/supplemental/calendarPreferenceData.json",
}

LOCALE_FILES = [
    ("num", "cldr-numbers-full/main/{loc}/numbers.json"),
    ("dates", "cldr-dates-full/main/{loc}/ca-gregorian.json"),
    ("fields", "cldr-dates-full/main/{loc}/dateFields.json"),
    ("lists", "cldr-misc-full/main/{loc}/listPatterns.json"),
    ("cur", "cldr-numbers-full/main/{loc}/currencies.json"),
    ("units", "cldr-units-full/main/{loc}/units.json"),
]

# The sanctioned single unit identifiers, mapped onto their CLDR key.
UNIT_KEYS = {
    "acre": "area-acre", "bit": "digital-bit", "byte": "digital-byte",
    "celsius": "temperature-celsius", "centimeter": "length-centimeter",
    "day": "duration-day", "degree": "angle-degree",
    "fahrenheit": "temperature-fahrenheit", "fluid-ounce": "volume-fluid-ounce",
    "foot": "length-foot", "gallon": "volume-gallon", "gigabit": "digital-gigabit",
    "gigabyte": "digital-gigabyte", "gram": "mass-gram", "hectare": "area-hectare",
    "hour": "duration-hour", "inch": "length-inch", "kilobit": "digital-kilobit",
    "kilobyte": "digital-kilobyte", "kilogram": "mass-kilogram",
    "kilometer": "length-kilometer", "liter": "volume-liter",
    "megabit": "digital-megabit", "megabyte": "digital-megabyte",
    "meter": "length-meter", "microsecond": "duration-microsecond",
    "mile": "length-mile", "mile-scandinavian": "length-mile-scandinavian",
    "milliliter": "volume-milliliter", "millimeter": "length-millimeter",
    "millisecond": "duration-millisecond", "minute": "duration-minute",
    "month": "duration-month", "nanosecond": "duration-nanosecond",
    "ounce": "mass-ounce", "percent": "concentr-percent",
    "petabyte": "digital-petabyte", "pound": "mass-pound",
    "second": "duration-second", "stone": "mass-stone",
    "terabit": "digital-terabit", "terabyte": "digital-terabyte",
    "week": "duration-week", "yard": "length-yard", "year": "duration-year",
}

# The category prefixes CLDR puts in front of the unit names.
UNIT_CATS = ("acceleration", "angle", "area", "concentr", "consumption", "digital",
             "duration", "electric", "energy", "force", "frequency", "graphics",
             "length", "light", "magnetic", "mass", "power", "pressure", "speed",
             "temperature", "torque", "volume")

# The plural category ICU keeps for the compact patterns; "-count-other" is
# the fallback for the rest.
COMPACT_OTHER = "other"


def fetch(path):
    """GET a CLDR file, memoised on disk.  Returns None on 404."""
    key = os.path.join(CACHE, path)
    if os.path.exists(key):
        if os.path.getsize(key) == 0:
            return None
        with open(key, "rb") as fh:
            return json.load(fh)
    os.makedirs(os.path.dirname(key), exist_ok=True)
    try:
        with urllib.request.urlopen(BASE + path, timeout=30) as resp:
            raw = resp.read()
    except Exception:
        raw = None
    if raw is None or raw[:1] != b"{":
        open(key, "wb").close()
        return None
    with open(key, "wb") as fh:
        fh.write(raw)
    return json.loads(raw)


def fetch_text(url, name):
    """GET a plain text file, memoised on disk."""
    key = os.path.join(CACHE, name)
    if os.path.exists(key):
        with open(key, encoding="utf-8") as fh:
            return fh.read()
    os.makedirs(os.path.dirname(key), exist_ok=True)
    with urllib.request.urlopen(url, timeout=30) as resp:
        raw = resp.read()
    with open(key, "wb") as fh:
        fh.write(raw)
    return raw.decode("utf-8")


def _icu_block(text, start):
    """Content of the brace block whose '{' sits at `start`."""
    depth = 0
    for i in range(start, len(text)):
        if text[i] == "{":
            depth += 1
        elif text[i] == "}":
            depth -= 1
            if depth == 0:
                return text[start + 1:i]
    raise ValueError("unbalanced braces in ICU data")


def _icu_entries(body):
    """Yield (name, block) for each `name{...}` entry of an ICU table."""
    i = 0
    while True:
        m = re.search(r"([A-Za-z0-9_]+)\{", body[i:])
        if not m:
            return
        block = _icu_block(body, i + m.end() - 1)
        yield m.group(1), block
        i += m.end() + len(block) + 1


def day_period_rules():
    """ICU's day period rules.

    Returns (locale -> rule set, set -> [24 hour -> day period, flags]).
    """
    text = fetch_text(_ICU_DAY_PERIODS, "icu-dayPeriods.txt")
    locs = {}
    for name, block in _icu_entries(_icu_block(text, text.index("locales{") + 7)):
        locs[name.replace("_", "-")] = int(block.strip('"')[3:])
    sets = {}
    for name, body in _icu_entries(_icu_block(text, text.index("rules{") + 5)):
        num = int(name[3:])
        hours = [10] * 24
        flags = 0
        for pname, pblock in _icu_entries(body):
            period = _DAY_PERIODS.index(pname)
            cutoffs = {}
            for cname, cblock in _icu_entries(pblock):
                cutoffs[cname] = int(cblock.strip('"').split(":")[0])
            if "at" in cutoffs:
                if cutoffs["at"] == 0 and period == 0:
                    flags |= 1
                elif cutoffs["at"] == 12 and period == 1:
                    flags |= 2
            if "from" in cutoffs and "before" in cutoffs:
                # ICU's DayPeriodRules::add wraps around midnight.
                i = cutoffs["from"]
                limit = cutoffs["before"]
                guard = 0
                while i != limit and guard < 48:
                    if i == 24:
                        i = 0
                    hours[i] = period
                    i += 1
                    guard += 1
        sets[num] = [hours, flags]
    return locs, sets


def day_period_set(locale, locs):
    """ICU resolves the rule set through the locale fallback chain."""
    tag = locale
    while True:
        if tag in locs:
            return locs[tag]
        if "-" not in tag:
            break
        tag = tag.rsplit("-", 1)[0]
    return locs.get("root")


def list_locales():
    if os.environ.get("GENINTL_LOCALES"):
        return sorted(os.environ["GENINTL_LOCALES"].split(","))
    key = os.path.join(CACHE, "..", "locales.json")
    key = os.path.abspath(key)
    if not os.path.exists(key):
        req = urllib.request.Request(
            "https://api.github.com/repos/unicode-org/cldr-json/contents/"
            "cldr-json/cldr-numbers-full/main",
            headers={"User-Agent": "genintl"},
        )
        with urllib.request.urlopen(req, timeout=60) as resp:
            data = json.load(resp)
        os.makedirs(os.path.dirname(key), exist_ok=True)
        with open(key, "w") as fh:
            json.dump(data, fh)
    with open(key) as fh:
        data = json.load(fh)
    if isinstance(data, dict):
        raise SystemExit("locale listing failed: " + json.dumps(data)[:200])
    return sorted(x["name"] for x in data if x["type"] == "dir")


def pick(d, *keys):
    for k in keys:
        if k in d:
            return d[k]
    return None


def _available_format(key, pattern):
    """One entry of the CLDR available formats.

    ICU spells the day period of an available format whose skeleton has no
    day period field as the plain AM/PM ("a"): the CLDR names the flexible
    day period ("B") in a few locales (zh-Hant).  Only the date and time
    styles keep the flexible day period.
    """
    if any(c in key for c in "aAbB"):
        return pattern
    out = []
    quoted = False
    i = 0
    while i < len(pattern):
        ch = pattern[i]
        if ch == "'":
            if i + 1 < len(pattern) and pattern[i + 1] == "'":
                out.append("''")
                i += 2
                continue
            quoted = not quoted
            out.append(ch)
            i += 1
            continue
        if not quoted and ch in "Bb":
            ch = "a"
        out.append(ch)
        i += 1
    return "".join(out)


def _plural_patterns(node, prefix):
    """The {plural: pattern} map of a CLDR pattern table."""
    if not isinstance(node, dict):
        return None
    out = {}
    for k, v in node.items():
        if k.startswith(prefix) and isinstance(v, str):
            out[k[len(prefix):]] = v
    return out or None


def _compact_rows(df):
    """[[magnitude, pattern]] rows of a decimalFormats short/long table.

    The pattern is a string when the plural variants agree, otherwise a
    {plural: pattern} map.  The magnitude is the exponent of the divisor key
    ("1000" -> 3), which is also the multiplier table index ICU uses.
    """
    by_mag = {}
    for key, pattern in df.items():
        cut = key.find("-count-")
        if cut <= 0 or not key[:cut].isdigit() or not isinstance(pattern, str):
            continue
        entry = by_mag.setdefault(len(key[:cut]) - 1, {})
        entry[key[cut + len("-count-"):]] = pattern
    rows = []
    for mag in sorted(by_mag):
        forms = by_mag[mag]
        if set(forms) == {"one", "other"} and forms["one"] == forms["other"]:
            rows.append([mag, forms["one"]])
        else:
            rows.append([mag, forms])
    return rows


def _compact_data(nums, sys_name):
    """The short and long compact patterns, ICU's multiplier table layout."""
    f = pick(nums, "decimalFormats-numberSystem-" + sys_name,
             "decimalFormats-numberSystem-latn")
    if not isinstance(f, dict):
        return None
    out = {}
    for style, tag in (("short", "s"), ("long", "l")):
        node = f.get(style)
        if not isinstance(node, dict):
            continue
        rows = _compact_rows(node.get("decimalFormat") or {})
        if rows:
            out[tag] = rows
    return out or None


def extract_numbers(loc, data):
    if data is None:
        return None
    nums = data.get("main", {}).get(loc, {}).get("numbers")
    if not isinstance(nums, dict):
        return None
    sys_name = nums.get("defaultNumberingSystem", "latn")
    out = {}
    sym = pick(nums, "symbols-numberSystem-" + sys_name, "symbols-numberSystem-latn")
    if sym:
        keep = {
            "dec": "decimal", "grp": "group", "pct": "percentSign", "min": "minusSign",
            "pls": "plusSign", "pm": "perMille", "inf": "infinity", "nan": "nan",
            "exp": "exponential", "sup": "superscriptingExponent", "tsep": "timeSeparator",
        }
        for short, long in keep.items():
            if long in sym and sym[long]:
                out[short] = sym[long]
    for short, key in (("dsp", "decimalFormats"), ("psp", "percentFormats"),
                       ("csp", "currencyFormats")):
        f = pick(nums, key + "-numberSystem-" + sys_name, key + "-numberSystem-latn")
        if f and isinstance(f, dict) and f.get("standard"):
            out[short] = f["standard"]
            if isinstance(f.get("accounting"), str):
                out[short + "Acc"] = f["accounting"]
            spacing = f.get("currencySpacing")
            if isinstance(spacing, dict):
                for where in ("beforeCurrency", "afterCurrency"):
                    seg = spacing.get(where)
                    if isinstance(seg, dict) and seg.get("insertBetween") is not None:
                        out[short + ("B" if where[0] == "b" else "A")] = seg["insertBetween"]
    comp = _compact_data(nums, sys_name)
    if comp:
        out["cp"] = comp
    if sys_name:
        out["sys"] = sys_name
    if nums.get("minimumGroupingDigits"):
        out["mg"] = int(nums["minimumGroupingDigits"])
    return out or None


def extract_dates(loc, data, cal="gregorian"):
    if data is None:
        return None
    cal = (((data.get("main", {}).get(loc, {}).get("dates", {})).get("calendars")) or {}).get(cal)
    if not cal:
        return None
    out = {}
    months = cal.get("months", {}).get("format", {})
    for short, name in (("ml", "wide"), ("ms", "abbreviated"), ("mn", "narrow")):
        v = months.get(name)
        if isinstance(v, dict) and "1" in v:
            out[short] = [v["%d" % i] for i in range(1, 13)]
    # The stand-alone month names are used when a pattern names a month
    # without a day ("yanvar 2020" in Russian, "January 2020" in English).
    months_sa = cal.get("months", {}).get("stand-alone", {})
    for short, name in (("mxl", "wide"), ("mxs", "abbreviated"), ("mxn", "narrow")):
        v = months_sa.get(name)
        if isinstance(v, dict) and "1" in v:
            out[short] = [v["%d" % i] for i in range(1, 13)]
    quarters = cal.get("quarters", {}).get("format", {})
    for short, name in (("ql", "wide"), ("qs", "abbreviated"), ("qn", "narrow")):
        v = quarters.get(name)
        if isinstance(v, dict) and "1" in v:
            out[short] = [v["%d" % i] for i in range(1, 5)]
    days = cal.get("days", {}).get("format", {})
    for short, name in (("wl", "wide"), ("ws", "abbreviated"), ("wn", "narrow")):
        v = days.get(name)
        if isinstance(v, dict) and "sun" in v:
            out[short] = [v[k] for k in ("sun", "mon", "tue", "wed", "thu", "fri", "sat")]
    days_sa = cal.get("days", {}).get("stand-alone", {})
    for short, name in (("wxl", "wide"), ("wxs", "abbreviated"), ("wxn", "narrow")):
        v = days_sa.get(name)
        if isinstance(v, dict) and "sun" in v:
            out[short] = [v[k] for k in ("sun", "mon", "tue", "wed", "thu", "fri", "sat")]
    zone = TZ_JSON.get(loc)
    if isinstance(zone, dict) and zone:
        out["tz"] = zone
    eras = cal.get("eras", {})
    for short, name in (("ea", "eraAbbr"), ("en", "eraNames"), ("enw", "eraNarrow")):
        v = eras.get(name)
        if isinstance(v, dict) and "0" in v:
            out[short] = [v["0"], v.get("1", "")]
    dp = cal.get("dayPeriods", {}).get("format", {})
    for short, name in (("dap", "abbreviated"), ("daw", "wide"), ("dan", "narrow")):
        v = dp.get(name)
        if isinstance(v, dict) and "am" in v:
            out[short] = [v["am"], v["pm"]]
    # The flexible day periods ("B"): midnight, noon, morning1, ... in ICU's
    # order (DayPeriodRules::DayPeriod).
    for short, name in (("bpa", "abbreviated"), ("bpw", "wide"), ("bpn", "narrow")):
        v = dp.get(name)
        if isinstance(v, dict):
            names = [v.get(k) for k in _DAY_PERIODS[:10]]
            if any(n for n in names):
                out[short] = names
    for short, name in (("df", "dateFormats"), ("tf", "timeFormats")):
        v = cal.get(name)
        if isinstance(v, dict):
            out[short] = {k: v[k] for k in ("full", "long", "medium", "short") if k in v}
    dtf = cal.get("dateTimeFormats", {})
    if isinstance(dtf, dict):
        # "dts" is the date+time glue: ICU's dateTimeFormats-atTime, keyed by the
        # date style.  "dtf" is the plain dateTimeFormats entry, whose medium
        # variant is what ICU concatenates with a single-date interval pattern.
        std = {k: dtf[k] for k in ("full", "long", "medium", "short") if isinstance(dtf.get(k), str)}
        if std:
            out["dtf"] = std
        at = cal.get("dateTimeFormats-atTime")
        atstd = at.get("standard") if isinstance(at, dict) else None
        glue = None
        if isinstance(atstd, dict):
            glue = {k: atstd[k] for k in ("full", "long", "medium", "short") if isinstance(atstd.get(k), str)}
        if not glue:
            glue = DTS_JSON.get(loc)
        if glue:
            out["dts"] = glue
        if isinstance(dtf.get("intervalFormatFallback"), str):
            out["fb"] = dtf["intervalFormatFallback"]
        af = dtf.get("availableFormats")
        if isinstance(af, dict):
            out["af"] = {k: _available_format(k, v) for k, v in af.items() if isinstance(v, str)}
        iv = dtf.get("intervalFormats")
        if isinstance(iv, dict):
            packed = {}
            for k, v in iv.items():
                if k == "intervalFormatFallback":
                    if isinstance(v, str):
                        out["fb"] = v
                elif isinstance(v, dict):
                    packed[k] = {f: p for f, p in v.items() if isinstance(p, str)}
                elif isinstance(v, str):
                    packed[k] = v
            if packed:
                out["iv"] = packed
    return out or None


def extract_fields(loc, data):
    """Trim dateFields down to the relative time data RelativeTimeFormat reads."""
    if data is None:
        return None
    fields = (((data.get("main", {}).get(loc, {}).get("dates", {})).get("fields")) or {})
    out = {}
    for unit in ("year", "quarter", "month", "week", "day", "weekday", "hour",
                 "minute", "second", "weekdayOfMonth", "sun", "mon", "tue", "wed",
                 "thu", "fri", "sat", "zone", "dayperiod"):
        for style in ("", "-short", "-narrow"):
            key = unit + style
            f = fields.get(key)
            if not isinstance(f, dict):
                continue
            entry = {}
            for k, v in f.items():
                if k.startswith("relative-type-"):
                    n = k[len("relative-type-"):]
                    if n in ("-2", "-1", "0", "1", "2"):
                        entry.setdefault("rel", {})[n] = v
                elif k.startswith("relativeTime-type-"):
                    t = k[len("relativeTime-type-"):]
                    pats = {kk[len("relativeTimePattern-count-"):]: vv
                            for kk, vv in v.items()
                            if kk.startswith("relativeTimePattern-count-")}
                    entry[t] = pats
                elif k == "displayName" and not style:
                    entry["dn"] = v
            if entry:
                out[key] = entry
    return out or None


def _currency_forms(v):
    """[symbol, narrow, other, one, extra] for one CLDR currency entry.

    `other` folds in the plain display name because ICU's fallback chain is
    CurrencyPlurals/<cat> -> CurrencyPlurals/other -> long name.  `one` and
    `extra` (zero/two/few/many) stay raw so the runtime falls back the same way.
    """
    def s(key):
        val = v.get(key)
        return val if isinstance(val, str) else None

    other = s("displayName-count-other") or s("displayName")
    one = s("displayName-count-one")
    extra = {}
    for cat in ("zero", "two", "few", "many"):
        val = s("displayName-count-" + cat)
        if val is not None:
            extra[cat] = val
    # A per-currency pattern override (CLDR `pattern`) wins over the locale's
    # general currency pattern for both symbol and long-name displays.
    return [s("symbol") or "", s("symbol-alt-narrow") or "", other, one,
            extra or None, s("pattern")]


def root_currency_symbols():
    """The CLDR root ("und") currency symbols every locale inherits.

    Root carries symbols but no display names, so a locale that does not name a
    currency falls through to the ISO code (never the English name).  English's
    own names live in the "en" record like any other locale's.
    """
    data = fetch("cldr-numbers-full/main/und/currencies.json")
    out = {}
    if data:
        cur = data.get("main", {}).get("und", {}).get("numbers", {}).get("currencies")
        for code, v in (cur or {}).items():
            if isinstance(v, dict):
                out[code] = _currency_forms(v)
    return out


def extract_currencies(loc, data, root_syms):
    """Keep only the currency data this locale does not inherit from root.

    Entry: [symbol, narrow, other, one, extra] (extra = zero/two/few/many).
    """
    if data is None:
        return None
    cur = data.get("main", {}).get(loc, {}).get("numbers", {}).get("currencies")
    if not isinstance(cur, dict):
        return None
    out = {}
    for code, v in cur.items():
        if not isinstance(v, dict):
            continue
        entry = _currency_forms(v)
        base = root_syms.get(code) or ["", "", None, None, None, None]
        # Symbols fall back to root; names never do (ICU then serves the ISO
        # code, not the English name).
        if entry[0] == "" and base[0]:
            entry[0] = base[0]
        if entry[1] == "" and base[1]:
            entry[1] = base[1]
        if entry == base:
            continue
        out[code] = entry
    return out or None


def extract_units(loc, data):
    """The sanctioned unit patterns: {unit: {style: pattern | {plural: pat}}}."""
    if data is None:
        return None
    units = ((data.get("main", {}).get(loc, {}).get("units")) or {})
    styles = (("short", "s"), ("long", "l"), ("narrow", "n"))
    out = {}

    def grab(node):
        pats = _plural_patterns(node, "unitPattern-count-")
        if not pats:
            return None
        if pats.get("one") == pats.get("other"):
            return pats["one"]
        return pats

    for name, key in UNIT_KEYS.items():
        entry = {}
        for style, tag in styles:
            pat = grab((units.get(style) or {}).get(key))
            if pat:
                entry[tag] = pat
        if entry:
            out[name] = entry
    # Compound units: the CLDR keys look like "consumption-kilometer-per-hour";
    # the category prefix is stripped so the spec's "A-per-B" form matches.
    for style, tag in styles:
        table = units.get(style)
        if not isinstance(table, dict):
            continue
        for key, node in table.items():
            cut = key.find("-per-")
            if cut <= 0 or not isinstance(node, dict):
                continue
            left, right = key[:cut], key[cut + len("-per-"):]
            for cat in UNIT_CATS:
                pre = cat + "-"
                if left.startswith(pre):
                    name = left[len(pre):] + "-per-" + right
                    pat = grab(node)
                    if pat:
                        out.setdefault(name, {})[tag] = pat
                    break
    return out or None


def extract_lists(loc, data):
    if data is None:
        return None
    lp = (((data.get("main", {}).get(loc, {}).get("listPatterns")) or {}))
    out = {}
    # Keys look like "listPattern-type-standard-short"; the value maps directly
    # onto {2, start, middle, end}.  Flatten to "<type>-<style>".
    for k, v in lp.items():
        if k.startswith("listPattern-type-") and isinstance(v, dict):
            slim = {kk: vv for kk, vv in v.items()
                    if kk in ("2", "start", "middle", "end")}
            if slim:
                out[k[len("listPattern-type-"):]] = slim
    return out or None


_DAY_PERIOD_CACHE = None


def day_period_data():
    """The ICU day period rules, loaded once."""
    global _DAY_PERIOD_CACHE
    if _DAY_PERIOD_CACHE is None:
        _DAY_PERIOD_CACHE = day_period_rules()
    return _DAY_PERIOD_CACHE


def build_locale(loc, root_syms):
    blobs = {}
    for name, path in LOCALE_FILES:
        blobs[name] = fetch(path.format(loc=loc))
    cal_name = CAL_JSON.get(loc, "gregory")
    cal = "gregorian"
    if cal_name in _CAL_OFFSETS:
        pkg, _ = _CAL_OFFSETS[cal_name]
        blobs["dates"] = fetch("cldr-cal-%s-full/main/%s/ca-%s.json" % (pkg, loc, pkg))
        cal = cal_name
    rec = {}
    n = extract_numbers(loc, blobs["num"])
    d = extract_dates(loc, blobs["dates"], cal)
    if d:
        # The calendar ICU picks by default for this locale.
        d["cal"] = cal_name
    if d:
        dp_locs, _ = day_period_data()
        dp_set = day_period_set(loc, dp_locs)
        if dp_set:
            d["bpr"] = dp_set
    r = extract_fields(loc, blobs["fields"])
    l = extract_lists(loc, blobs["lists"])
    eff = effective_currency_locale(loc)
    cur_data = blobs["cur"]
    cn = None
    if eff != loc:
        cur_data = fetch("cldr-numbers-full/main/%s/currencies.json" % eff)
        # ICU serves the whole currency pattern from the fallback locale too
        # (vo/cu -> en's "¤#,##0.00", not their own "¤ #,##0.00").
        en = extract_numbers(eff, fetch("cldr-numbers-full/main/%s/numbers.json" % eff))
        if en:
            cn = {k: en[k] for k in ("csp", "cspAcc", "cspA", "cspB") if k in en}
    c = extract_currencies(eff, cur_data, root_syms)
    u = extract_units(loc, blobs["units"])
    if n:
        rec["n"] = n
    if d:
        rec["d"] = d
    if r:
        rec["r"] = r
    if l:
        rec["l"] = l
    if c is not None or eff == loc:
        rec["cs"] = c if c is not None else {}
        rec["csL"] = eff.split("-")[0].lower()
    if cn:
        rec["cn"] = cn
    if u:
        rec["u"] = u
    return loc, (rec if (n and d) else None)


def supplement():
    """Plural rules, numbering systems and hour cycle preferences."""
    out = {}
    card = fetch(SINGLETONS["plurals"])["supplemental"]["plurals-type-cardinal"]
    ordn = fetch(SINGLETONS["ordinals"])["supplemental"]["plurals-type-ordinal"]
    for lang, rules in card.items():
        out.setdefault(lang, {})["c"] = _strip_rules(rules)
    for lang, rules in ordn.items():
        out.setdefault(lang, {})["o"] = _strip_rules(rules)
    # Category order matters: the evaluator walks rules top down.
    order = ["zero", "one", "two", "few", "many", "other"]
    for lang, kinds in out.items():
        for kind in kinds:
            kinds[kind] = {k: kinds[kind][k] for k in order if k in kinds[kind]}
    out["_icuLocales"] = sorted(ICU_LOCALES)
    ns = fetch(SINGLETONS["numberingSystems"])["supplemental"]["numberingSystems"]
    out["_digits"] = {k: v["_digits"] for k, v in ns.items() if v.get("_digits")}
    # ICU rejects the algorithmic systems (the ones carrying a "_rules" rule),
    # they are not valid "nu" values.
    out["_numericSystems"] = sorted(k for k, v in ns.items()
                                    if v.get("_type") == "numeric" and "_rules" not in v)
    td = fetch(SINGLETONS["timeData"])["supplemental"]["timeData"]
    out["_hourCycle"] = {k: v.get("_preferred", "H") for k, v in td.items() if "_preferred" in v}
    cf = fetch(SINGLETONS["currencyFractions"])["supplemental"]["currencyData"]["fractions"]
    out["_currencyDigits"] = {k: int(v.get("_digits", 2)) for k, v in cf.items()}
    out["_currencySymbols"] = root_currency_symbols()
    wk = fetch(SINGLETONS["weekData"])["supplemental"].get("weekData", {})
    out["_firstDay"] = wk.get("firstDay", {})
    out["_minDays"] = wk.get("minDays", {})
    out["_weekendStart"] = wk.get("weekendStart", {})
    out["_weekendEnd"] = wk.get("weekendEnd", {})
    cp = fetch(SINGLETONS["calendarPreference"])["supplemental"]["calendarPreferenceData"]
    out["_calendars"] = cp
    _, dp_sets = day_period_data()
    out["_dayPeriodRules"] = {str(k): v for k, v in dp_sets.items()}
    # supportedValuesOf catalogs.  ICU's own lists, captured once into
    # tools/intl_catalogs.json (see the README of that file).
    cat_path = os.path.join(HERE, "intl_catalogs.json")
    if os.path.exists(cat_path):
        with open(cat_path) as fh:
            out.update(json.load(fh))
    # ICU's canonical time zone names, captured from Node (see tools/genzones.py).
    zone_path = os.path.join(HERE, "intl_zones.json")
    if os.path.exists(zone_path):
        with open(zone_path, encoding="utf-8") as fh:
            out["_zoneAliases"] = json.load(fh)
    # The CLDR currency unit patterns ("{0} {1}"), captured from Node: the
    # CLDR JSON has no source for them.
    cur_path = os.path.join(HERE, "intl_curpat.json")
    if os.path.exists(cur_path):
        with open(cur_path, encoding="utf-8") as fh:
            out["_currencyPatterns"] = json.load(fh)
    return out


def _strip_rules(rules):
    """Keep only the rule text of the pluralRule-count-* keys."""
    out = {}
    for k, v in rules.items():
        if k.startswith("pluralRule-count-"):
            out[k[len("pluralRule-count-"):]] = _strip_samples(v)
    return out


def _strip_samples(text):
    """Drop the ' @integer ...' samples tail; only the condition is needed."""
    at = text.find(" @")
    return (text[:at] if at >= 0 else text).strip()


def main():
    locales = list_locales()
    if os.environ.get("GENINTL_PROGRESS", "1") == "1":
        print("locales:", len(locales), file=sys.stderr)
    results = {}
    root_syms = root_currency_symbols()
    with ThreadPoolExecutor(max_workers=16) as pool:
        for i, (loc, rec) in enumerate(
                pool.map(lambda loc: build_locale(loc, root_syms), locales)):
            if rec:
                results[loc] = rec
            if os.environ.get("GENINTL_PROGRESS", "1") == "1" and i % 100 == 0:
                print("  %d/%d" % (i, len(locales)), file=sys.stderr)
    sup = supplement()
    if not results:
        raise SystemExit("no locale data extracted")
    print("extracted %d locales" % len(results), file=sys.stderr)

    def blob(obj):
        text = json.dumps(obj, ensure_ascii=False, separators=(",", ":"), sort_keys=True)
        return gzip.compress(text.encode("utf-8"), 9)

    # --- binary container -------------------------------------------------
    # [u16 namelen][name][u32 datalen][gzip bytes] repeated; empty name ends
    # the stream, so an index is built once at init.
    parts = []
    names = []
    for loc in sorted(results):
        data = blob(results[loc])
        names.append(loc)
        parts.append(struct.pack("<H", len(loc)) + loc.encode("ascii")
                     + struct.pack("<I", len(data)) + data)
    supp = blob(sup)
    parts.append(struct.pack("<H", len(b"$supp")) + b"$supp" + struct.pack("<I", len(supp)) + supp)
    bin_path = os.path.join(os.path.dirname(OUT), "intl_data.bin")
    with open(bin_path, "wb") as fh:
        fh.write(b"".join(parts))
    print("wrote", bin_path, os.path.getsize(bin_path), "bytes,", len(names),
          "locales", file=sys.stderr)

    src = '''// Code generated by tools/genintl.py. DO NOT EDIT.

package vm

import (
\t"bytes"
\t"compress/gzip"
\t_ "embed"
\t"encoding/binary"
\t"io"
\t"sort"
\t"sync"
)

// intl_data.bin packs one gzip compressed JSON record per CLDR locale plus a
// "$supp" entry holding the cross-locale tables (plural rules, numbering
// systems, hour cycle and week preferences).  Records inflate on first use.
//
//go:embed intl_data.bin
var intlData []byte

var intlLocaleBlobs = map[string][]byte{}

func init() {
\tr := bytes.NewReader(intlData)
\tfor {
\t\tvar nl uint16
\t\tif err := binary.Read(r, binary.LittleEndian, &nl); err != nil || nl == 0 {
\t\t\treturn
\t\t}
\t\tname := make([]byte, nl)
\t\tif _, err := io.ReadFull(r, name); err != nil {
\t\t\treturn
\t\t}
\t\tvar dl uint32
\t\tif err := binary.Read(r, binary.LittleEndian, &dl); err != nil {
\t\t\treturn
\t\t}
\t\tdata := make([]byte, dl)
\t\tif _, err := io.ReadFull(r, data); err != nil {
\t\t\treturn
\t\t}
\t\tintlLocaleBlobs[string(name)] = data
\t}
}

var intlSuppTextOnce sync.Once

func intlSupplementalText() string {
\tintlSuppTextOnce.Do(func() {
\t\tintlSupplementalJSON = intlInflate(intlLocaleBlobs["$supp"])
\t})
\treturn intlSupplementalJSON
}

var intlSupplementalJSON string

// intlInflate decodes one blob, memoising the JSON text.
var intlInflateCache = map[string]string{}

func intlInflate(encoded []byte) string {
\tif text, ok := intlInflateCache[string(encoded)]; ok {
\t\treturn text
\t}
\tzr, err := gzip.NewReader(bytes.NewReader(encoded))
\tif err != nil {
\t\treturn ""
\t}
\tout, err := io.ReadAll(zr)
\tif err != nil {
\t\treturn ""
\t}
\ttext := string(out)
\tif len(intlInflateCache) < 512 {
\t\tintlInflateCache[string(encoded)] = text
\t}
\treturn text
}

// intlLocaleList returns every locale with a record in the data table.  The
// blob table is filled by init(), so anything reading it must be lazy.
var intlLocaleListOnce sync.Once
var intlLocaleNames []string

func intlLocaleList() []string {
\tintlLocaleListOnce.Do(func() {
\t\tlist := make([]string, 0, len(intlLocaleBlobs))
\t\tfor name := range intlLocaleBlobs {
\t\t\tif name != "$supp" {
\t\t\t\tlist = append(list, name)
\t\t\t}
\t\t}
\t\tsort.Strings(list)
\t\tintlLocaleNames = list
\t})
\treturn intlLocaleNames
}

func intlHasLocale(name string) bool {
\tif name == "$supp" {
\t\treturn false
\t}
\t_, ok := intlLocaleBlobs[name]
\treturn ok
}
'''
    src = src.replace("\\t", "\t")
    with open(OUT, "w") as fh:
        fh.write(src)
    print("wrote", OUT, os.path.getsize(OUT), "bytes", file=sys.stderr)


if __name__ == "__main__":
    main()
