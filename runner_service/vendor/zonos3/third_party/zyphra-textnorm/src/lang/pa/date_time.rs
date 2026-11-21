//! Date and clock graph branches.
use super::graph_utils::{cardinal, cardinal_input_weight, digit, lookup};
use crate::text::{Cost, cost};
use std::collections::HashSet;
use std::sync::LazyLock;
const DAYS: &str = include_str!("../../../data/pa/date/days.tsv");
const ORD: &str = include_str!("../../../data/pa/date/day_ordinal.tsv");
const MONTHS: &str = include_str!("../../../data/pa/date/months.tsv");
const NAMES: &str = include_str!("../../../data/pa/date/month_names.tsv");
const ABBR: &str = include_str!("../../../data/pa/date/month_abbr.tsv");
const ERA: &str = include_str!("../../../data/pa/date/year_suffix.tsv");
const HOURS: &str = include_str!("../../../data/pa/time/hours.tsv");
const MINUTES: &str = include_str!("../../../data/pa/time/minutes.tsv");
const SECONDS: &str = include_str!("../../../data/pa/time/seconds.tsv");

// All short clock lexemes are indexed by the exact sequence of ASCII or
// Gurmukhi digits, including leading zeroes and mixed-script distinctions.
// The fallback retains any other keys present in the embedded source table.
struct ClockTable {
    source: &'static str,
    short: [Option<&'static str>; 420],
}

fn clock_key(s: &str) -> Option<usize> {
    let mut chars = s.chars();
    let first = chars.next()?;
    let a = digit(first)? as usize + if first.is_ascii() { 0 } else { 10 };
    let b = match chars.next() {
        Some(second) => 1 + digit(second)? as usize + if second.is_ascii() { 0 } else { 10 },
        None => 0,
    };
    if chars.next().is_some() {
        return None;
    }
    Some(a * 21 + b)
}

impl ClockTable {
    fn new(source: &'static str) -> Self {
        let mut table = Self { source, short: [None; 420] };
        for row in source.lines() {
            let row = row.trim_end_matches('\r');
            let (key, value) = row.split_once('\t').unwrap_or((row, row));
            if let Some(index) = clock_key(key) {
                if table.short[index].is_none() {
                    table.short[index] = Some(value.split('\t').next().unwrap_or(value));
                }
            }
        }
        table
    }

    fn get(&self, key: &str) -> Option<&'static str> {
        if let Some(index) = clock_key(key) { self.short[index] } else { lookup(self.source, key) }
    }
}

static CLOCK_HOURS: LazyLock<ClockTable> = LazyLock::new(|| ClockTable::new(HOURS));
static CLOCK_MINUTES: LazyLock<ClockTable> = LazyLock::new(|| ClockTable::new(MINUTES));
static CLOCK_SECONDS: LazyLock<ClockTable> = LazyLock::new(|| ClockTable::new(SECONDS));

struct PrefixSet {
    prefixes: HashSet<&'static str>,
    keys: HashSet<&'static str>,
}

impl PrefixSet {
    fn new(tables: &[&'static str]) -> Self {
        let mut index = Self { prefixes: HashSet::new(), keys: HashSet::new() };
        for &table in tables {
            for row in table.lines() {
                if let Some((key, _)) = row.split_once('\t') {
                    for (i, _) in key.char_indices().skip(1) {
                        index.prefixes.insert(&key[..i]);
                    }
                    index.prefixes.insert(key);
                    index.keys.insert(key);
                }
            }
        }
        index
    }

    fn overlaps(&self, s: &str) -> bool {
        s.is_empty() || self.prefixes.contains(s) || s.char_indices().skip(1).any(|(i, _)| self.keys.contains(&s[..i]))
    }
}

static MONTH_PREFIXES: LazyLock<PrefixSet> = LazyLock::new(|| PrefixSet::new(&[NAMES, ABBR]));
static ERA_PREFIXES: LazyLock<PrefixSet> = LazyLock::new(|| PrefixSet::new(&[ERA]));
static DAY_KEYS: LazyLock<HashSet<&'static str>> = LazyLock::new(|| {
    [DAYS, ORD]
        .into_iter()
        .flat_map(|table| table.lines().filter_map(|row| row.split_once('\t').map(|(key, _)| key)))
        .collect()
});

fn year(s: &str) -> Option<(String, Cost)> {
    if s.chars().count() != 4 || digit(s.chars().next()?)? == 0 {
        return None;
    }
    cardinal(s)
}
fn month(s: &str) -> Option<&str> {
    lookup(NAMES, s).or_else(|| lookup(ABBR, s.strip_suffix('.').unwrap_or(s)))
}
fn field(key: &str, val: &str) -> String {
    format!("{key}: \"{val}\"")
}
/// Conservative prefix filter for the written-date and era branches.
pub(crate) fn date_prefix(s: &str) -> bool {
    if s.contains(':') {
        return false;
    }
    let Some((first, rest)) = s.split_once(' ') else { return true };
    if first.chars().next().is_some_and(|c| digit(c).is_none()) && month(first.trim_end_matches(',')).is_some() {
        return true;
    }
    let day = DAY_KEYS.contains(first);
    let era_year = (1..=4).contains(&first.chars().count())
        && first.chars().next().and_then(digit).is_some_and(|d| d != 0)
        && first.chars().all(|c| digit(c).is_some());
    day && MONTH_PREFIXES.overlaps(rest) || era_year && ERA_PREFIXES.overlaps(rest)
}

fn year_weight(s: &str) -> Option<Cost> {
    if s.chars().count() != 4 || digit(s.chars().next()?)? == 0 {
        return None;
    }
    cardinal_input_weight(s)
}

pub(crate) fn date_cost(s: &str) -> Option<Cost> {
    for sep in ['-', '/', '.'] {
        let mut parts = s.split(sep);
        let a = parts.next()?;
        let b = parts.next();
        let c = parts.next();
        let d = parts.next();
        if let (Some(b), Some(c), None) = (b, c, d) {
            if lookup(DAYS, a).is_some() && lookup(MONTHS, b).is_some() {
                if let Some(w) = year_weight(c) {
                    return Some(w);
                }
            }
            if sep != '.' && lookup(MONTHS, b).is_some() && lookup(DAYS, c).is_some() {
                if let Some(w) = year_weight(a) {
                    return Some(w);
                }
            }
        }
        if sep != '.' {
            if let (Some(b), None) = (b, c) {
                if let (Some(a), Some(b)) = (year_weight(a), year_weight(b)) {
                    return Some(a + b);
                }
            }
        }
    }
    let mut era = false;
    let mut base = s;
    for row in ERA.lines() {
        let (key, _) = row.split_once('\t')?;
        if let Some(prefix) = s.strip_suffix(key) {
            base = prefix.strip_suffix(' ').unwrap_or(prefix);
            era = true;
            break;
        }
    }
    let mut words = base.split(' ');
    let a = words.next()?;
    let b = words.next();
    let c = words.next();
    let d = words.next();
    match (b, c, d) {
        (None, None, None) => {
            if era && (1..=4).contains(&base.chars().count()) && digit(base.chars().next()?)? != 0 {
                return cardinal_input_weight(base);
            }
        }
        (Some(b), c, None) => {
            if (lookup(DAYS, a).is_some() || lookup(ORD, a).is_some()) && month(b.trim_end_matches(',')).is_some() {
                if c.is_none() && !era {
                    return Some(cost(0.01));
                }
                if let Some(c) = c {
                    return year_weight(c);
                }
            }
            if c.is_none() && month(a.trim_end_matches(',')).is_some() {
                return year_weight(b);
            }
        }
        _ => {}
    }
    if let Some(decade) = s.strip_suffix('s') {
        let decade = decade.strip_prefix('\'').unwrap_or(decade);
        let mut chars = decade.chars();
        let a = chars.next();
        let b = chars.next();
        let c = chars.next();
        let d = chars.next();
        if let (Some(a), Some(b), None) = (a, b, c) {
            if a.is_ascii() && b == '0' {
                let w = cardinal_input_weight(decade)?;
                if digit(a)? >= 2 {
                    return Some(w + cost(0.1));
                }
            }
        }
        if let (Some(a), Some(_), Some(c), Some(d), None) = (a, b, c, d, chars.next()) {
            if digit(d) == Some(0) {
                let split = decade.char_indices().nth(2)?.0;
                let (left, right) = decade.split_at(split);
                let w = if left == "20" || left == "੨੦" {
                    0
                } else if digit(a) == Some(1) {
                    cardinal_input_weight(left)?
                } else {
                    return None;
                };
                if digit(c) != Some(0) {
                    cardinal_input_weight(right)?;
                }
                return Some(w);
            }
        }
    }
    None
}
pub(crate) fn date_tag(s: &str) -> Option<(String, Cost)> {
    for sep in ['-', '/', '.'] {
        let parts: Vec<&str> = s.split(sep).collect();
        if parts.len() == 3 {
            if let (Some(day), Some(month), Some((year, w))) =
                (lookup(DAYS, parts[0]), lookup(MONTHS, parts[1]), year(parts[2]))
            {
                return Some((
                    format!("date {{ {} {} {} }}", field("day", day), field("month", month), field("year", &year)),
                    w,
                ));
            }
            if sep != '.' {
                if let (Some((year, w)), Some(month), Some(day)) =
                    (year(parts[0]), lookup(MONTHS, parts[1]), lookup(DAYS, parts[2]))
                {
                    return Some((
                        format!("date {{ {} {} {} }}", field("year", &year), field("month", month), field("day", day)),
                        w,
                    ));
                }
            }
        }
        if sep != '.' && parts.len() == 2 {
            if let (Some((a, wa)), Some((b, wb))) = (year(parts[0]), year(parts[1])) {
                return Some((format!("date {{ era: \"{a} ਤੋਂ {b}\" }}"), wa + wb));
            }
        }
    }
    let mut era = None;
    let mut base = s;
    for row in ERA.lines() {
        let (key, value) = row.split_once('\t')?;
        if let Some(prefix) = s.strip_suffix(key) {
            base = prefix.strip_suffix(' ').unwrap_or(prefix);
            era = Some(value);
            break;
        }
    }
    let words: Vec<&str> = base.split(' ').collect();
    let era_field = era.map(|v| format!(" era: \"{v}\"")).unwrap_or_default();
    if words.len() == 1 {
        if era.is_some() && (1..=4).contains(&base.chars().count()) && digit(base.chars().next()?)? != 0 {
            let (y, w) = cardinal(base)?;
            return Some((format!("date {{ year: \"{y}\"{era_field} }}"), w));
        }
    } else if words.len() == 2 || words.len() == 3 {
        if let Some(day) = lookup(DAYS, words[0]).or_else(|| lookup(ORD, words[0])) {
            if let Some(m) = month(words[1].trim_end_matches(',')) {
                if words.len() == 2 && era.is_none() {
                    return Some((format!("date {{ day: \"{day}\" month: \"{m}\" }}"), cost(0.01)));
                }
                if words.len() == 3 {
                    let (y, w) = year(words[2])?;
                    return Some((format!("date {{ day: \"{day}\" month: \"{m}\" year: \"{y}\"{era_field} }}"), w));
                }
            }
        }
        if words.len() == 2 {
            if let Some(m) = month(words[0].trim_end_matches(',')) {
                let (y, w) = year(words[1])?;
                return Some((format!("date {{ month: \"{m}\" year: \"{y}\"{era_field} }}"), w));
            }
        }
    }
    if let Some(decade) = s.strip_suffix('s') {
        let decade = decade.strip_prefix('\'').unwrap_or(decade);
        let chars: Vec<char> = decade.chars().collect();
        if chars.len() == 2 && chars[0].is_ascii() && chars[1] == '0' {
            let (v, w) = cardinal(decade)?;
            if digit(chars[0])? >= 2 {
                return Some((format!("date {{ era: \"{v} ਦਾ ਦਹਾਕਾ\" }}"), w + cost(0.1)));
            }
        }
        if chars.len() == 4 && digit(chars[3]) == Some(0) {
            let a: String = chars[..2].iter().collect();
            let b: String = chars[2..].iter().collect();
            let (prefix, w) = if a == "20" || a == "੨੦" {
                ("ਦੋ ਹਜ਼ਾਰ".to_owned(), 0)
            } else if digit(chars[0]) == Some(1) {
                let (v, w) = cardinal(&a)?;
                (format!("{v} ਸੌ"), w)
            } else {
                return None;
            };
            let tail = if digit(chars[2]) == Some(0) { String::new() } else { format!(" {}", cardinal(&b)?.0) };
            return Some((format!("date {{ era: \"{prefix}{tail} ਦਾ ਦਹਾਕਾ\" }}"), w));
        }
    }
    None
}

fn endpoint(s: &str) -> Option<String> {
    let (h, m) = s.split_once(':')?;
    let h = CLOCK_HOURS.get(h)?;
    if m == "00" || m == "੦੦" {
        Some(format!("{h} ਵਜੇ"))
    } else {
        Some(format!("{h} ਵੱਜ ਕੇ {} ਮਿੰਟ", CLOCK_MINUTES.get(m)?))
    }
}
fn period(n: u32) -> &'static str {
    match n {
        0 => "ਅੱਧੀ ਰਾਤ",
        4..=11 => "ਸਵੇਰੇ",
        12..=15 => "ਦੁਪਹਿਰੇ",
        16..=20 => "ਸ਼ਾਮੀਂ",
        _ => "ਰਾਤੀਂ",
    }
}
const ZONES: &[(&str, &str)] = &[
    ("UTC", "ਯੂ ਟੀ ਸੀ"),
    ("GMT", "ਜੀ ਐਮ ਟੀ"),
    ("IST", "ਆਈ ਐਸ ਟੀ"),
    ("EST", "ਈ ਐਸ ਟੀ"),
    ("EDT", "ਈ ਡੀ ਟੀ"),
    ("CST", "ਸੀ ਐਸ ਟੀ"),
    ("CDT", "ਸੀ ਡੀ ਟੀ"),
    ("MST", "ਐਮ ਐਸ ਟੀ"),
    ("MDT", "ਐਮ ਡੀ ਟੀ"),
    ("PST", "ਪੀ ਐਸ ਟੀ"),
    ("PDT", "ਪੀ ਡੀ ਟੀ"),
    ("CET", "ਸੀ ਈ ਟੀ"),
    ("CEST", "ਸੀ ਈ ਐਸ ਟੀ"),
    ("JST", "ਜੇ ਐਸ ਟੀ"),
    ("KST", "ਕੇ ਐਸ ਟੀ"),
];
const MARKERS: &[(&str, bool)] = &[
    ("a.m.", false),
    ("p.m.", true),
    ("A.M.", false),
    ("P.M.", true),
    ("ਏਐਮ", false),
    ("ਪੀਐਮ", true),
    ("AM", false),
    ("PM", true),
    ("am", false),
    ("pm", true),
];

static CLOCK_SUFFIX_ENDS: LazyLock<Vec<char>> = LazyLock::new(|| {
    let mut ends = Vec::new();
    for suffix in ZONES.iter().map(|(key, _)| *key).chain(MARKERS.iter().map(|(key, _)| *key)) {
        if let Some(c) = suffix.chars().next_back() {
            if !ends.contains(&c) {
                ends.push(c);
            }
        }
    }
    ends
});

fn clock_suffixes(s: &str) -> (&str, Option<bool>, Option<&'static str>) {
    if !s.chars().next_back().is_some_and(|c| CLOCK_SUFFIX_ENDS.contains(&c)) {
        return (s, None, None);
    }
    let mut text = s;
    let mut zone = None;
    for &(key, spoken) in ZONES {
        if let Some(prefix) = text.strip_suffix(key).and_then(|x| x.strip_suffix(' ')) {
            text = prefix;
            zone = Some(spoken);
            break;
        }
    }
    let mut marker = None;
    for &(key, pm) in MARKERS {
        if let Some(prefix) = text.strip_suffix(key) {
            text = prefix.strip_suffix(' ').unwrap_or(prefix);
            marker = Some(pm);
            break;
        }
    }
    (text, marker, zone)
}

pub(crate) fn time_cost(s: &str) -> Option<Cost> {
    if let Some(i) = s.find(|c| matches!(c, '-' | '–' | '—')) {
        let c = s[i..].chars().next()?;
        let valid = |text: &str| {
            let Some((h, m)) = text.trim().split_once(':') else { return false };
            CLOCK_HOURS.get(h).is_some() && (m == "00" || m == "੦੦" || CLOCK_MINUTES.get(m).is_some())
        };
        return (valid(&s[..i]) && valid(&s[i + c.len_utf8()..])).then_some(0);
    }
    let (text, marker, _) = clock_suffixes(s);
    let mut parts = text.split(':');
    let hour = parts.next()?;
    let minute = parts.next()?;
    let second = parts.next();
    if parts.next().is_some() || CLOCK_HOURS.get(hour).is_none() {
        return None;
    }
    if marker.is_some() {
        let mut n = 0u32;
        for c in hour.chars() {
            n = n * 10 + digit(c)? as u32;
        }
        if !(1..=12).contains(&n) {
            return None;
        }
    }
    if second.is_some() || !(minute == "00" || minute == "੦੦") {
        CLOCK_MINUTES.get(minute)?;
    }
    if let Some(second) = second {
        CLOCK_SECONDS.get(second)?;
    }
    Some(if second.is_some() { 0 } else { cost(0.3) })
}

pub(crate) fn time_tag(s: &str) -> Option<(String, Cost)> {
    if let Some(i) = s.find(|c| matches!(c, '-' | '–' | '—')) {
        let a = endpoint(s[..i].trim())?;
        let c = s[i..].chars().next()?;
        let b = endpoint(s[i + c.len_utf8()..].trim())?;
        return Some((format!("time {{ morphosyntactic_features: \"{a} ਤੋਂ {b}\" }}"), 0));
    }
    let (text, marker, zone) = clock_suffixes(s);
    let mut parts = text.split(':');
    let hour = parts.next()?;
    let minute = parts.next()?;
    let second = parts.next();
    if parts.next().is_some() {
        return None;
    }
    let h = CLOCK_HOURS.get(hour)?;
    let mut tag = String::from("time { ");
    if let Some(pm) = marker {
        let mut n = 0u32;
        for c in hour.chars() {
            n = n * 10 + digit(c)? as u32;
        }
        if !(1..=12).contains(&n) {
            return None;
        }
        tag.push_str("suffix: \"");
        tag.push_str(period(n % 12 + if pm { 12 } else { 0 }));
        tag.push_str("\" ");
    }
    tag.push_str("hours: \"");
    tag.push_str(h);
    tag.push('"');
    let zero = |x: &str| x == "00" || x == "੦੦";
    if second.is_some() || !zero(minute) {
        tag.push_str(" minutes: \"");
        tag.push_str(CLOCK_MINUTES.get(minute)?);
        tag.push('"');
    }
    if let Some(second) = second {
        tag.push_str(" seconds: \"");
        tag.push_str(CLOCK_SECONDS.get(second)?);
        tag.push('"');
    }
    if let Some(z) = zone {
        tag.push_str(" zone: \"");
        tag.push_str(z);
        tag.push('"');
    }
    tag.push_str(" }");
    let weight = if second.is_none() { cost(0.3) } else { 0 };
    Some((tag, weight))
}
