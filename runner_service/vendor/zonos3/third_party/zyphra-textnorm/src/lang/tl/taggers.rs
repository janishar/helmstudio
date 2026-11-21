//! Classifiers for Tagalog text normalization.
use super::graph_utils::{cardinal, cardinal_with_sep, digit_sequence};
use crate::lang::common::{possible_token_end, possible_token_start};
use crate::lattice::{Candidate, Classifier};
use crate::text::{cost, is_nemo_space};
use std::collections::{HashMap, HashSet};
use std::hash::{BuildHasherDefault, Hasher};
use std::sync::LazyLock;

const WHITELIST: &str = include_str!("../../../data/tl/whitelist.tsv");
const QUANTITIES: &str = include_str!("../../../data/tl/numbers/quantities.tsv");
const MONTHS: &str = include_str!("../../../data/tl/months/names.tsv");
const MONTH_ABBR: &str = include_str!("../../../data/tl/months/abbr.tsv");
const MONTH_NUM: &str = include_str!("../../../data/tl/months/numbers.tsv");
const MEASURES: &str = include_str!("../../../data/tl/measure/measurements.tsv");
const DENOM: &str = include_str!("../../../data/tl/measure/denominators.tsv");
const CURRENCY: &str = include_str!("../../../data/tl/money/currency.tsv");
const MONEY_QTY: &str = include_str!("../../../data/tl/money/quantity_abbr.tsv");
const ZONES: &str = include_str!("../../../data/tl/time/time_zone.tsv");

static QUANTITY_ROWS: LazyLock<Vec<(&'static str, &'static str)>> =
    LazyLock::new(|| QUANTITIES.lines().filter_map(|row| row.split_once('\t')).collect());
static MONEY_QUANTITY_ROWS: LazyLock<Vec<(&'static str, &'static str)>> =
    LazyLock::new(|| MONEY_QTY.lines().filter_map(|row| row.split_once('\t')).collect());

/// The keys are fixed embedded locale tables, not an attacker-controlled map.
/// A lightweight byte hash avoids SipHash on each rejected grammar candidate.
struct TableHasher(u64);
impl Default for TableHasher {
    fn default() -> Self {
        Self(0xcbf29ce484222325)
    }
}
impl Hasher for TableHasher {
    fn finish(&self) -> u64 {
        self.0
    }
    fn write(&mut self, bytes: &[u8]) {
        for &b in bytes {
            self.0 = (self.0 ^ u64::from(b)).wrapping_mul(0x100000001b3);
        }
    }
}
type TableIndex = HashMap<&'static str, &'static str, BuildHasherDefault<TableHasher>>;
fn make_index(source: &'static str) -> TableIndex {
    let mut index = TableIndex::default();
    for (key, value) in source.lines().filter_map(|line| line.split_once('\t')) {
        index.entry(key).or_insert_with(|| value.split('\t').next().unwrap_or(value));
    }
    index
}
static WHITELIST_INDEX: LazyLock<TableIndex> = LazyLock::new(|| make_index(WHITELIST));
static MONTHS_INDEX: LazyLock<TableIndex> = LazyLock::new(|| {
    let mut index = make_index(MONTHS);
    for (key, value) in MONTH_ABBR.lines().filter_map(|row| row.split_once('\t')) {
        index.entry(key).or_insert(value);
    }
    index
});
static MONTH_ABBR_INDEX: LazyLock<TableIndex> = LazyLock::new(|| make_index(MONTH_ABBR));
static MONTH_NUM_INDEX: LazyLock<TableIndex> = LazyLock::new(|| make_index(MONTH_NUM));
static MEASURES_INDEX: LazyLock<TableIndex> = LazyLock::new(|| make_index(MEASURES));
static DENOM_INDEX: LazyLock<TableIndex> = LazyLock::new(|| make_index(DENOM));
static CURRENCY_INDEX: LazyLock<TableIndex> = LazyLock::new(|| make_index(CURRENCY));
static ZONES_INDEX: LazyLock<TableIndex> = LazyLock::new(|| make_index(ZONES));

static WHITELIST_PREFIXES: LazyLock<HashSet<&'static str>> = LazyLock::new(|| {
    WHITELIST
        .lines()
        .filter_map(|row| row.split_once('\t'))
        .filter_map(|(key, _)| key.split_once(' ').map(|(first, _)| first))
        .collect()
});

/// Character gates are derived from the embedded keys and do not limit span
/// lengths or alter which lexical readings are explored.
struct CharacterSet {
    ascii: [bool; 128],
    other: HashSet<char>,
}
impl CharacterSet {
    fn new() -> Self {
        Self { ascii: [false; 128], other: HashSet::new() }
    }
    fn insert(&mut self, c: char) {
        if c.is_ascii() {
            self.ascii[c as usize] = true;
        } else {
            self.other.insert(c);
        }
    }
    #[inline]
    fn contains(&self, c: char) -> bool {
        if c.is_ascii() { self.ascii[c as usize] } else { self.other.contains(&c) }
    }
}
static MEASURE_ENDS: LazyLock<CharacterSet> = LazyLock::new(|| {
    let mut set = CharacterSet::new();
    for (key, _) in MEASURES.lines().chain(DENOM.lines()).filter_map(|row| row.split_once('\t')) {
        if let Some(c) = key.chars().last() {
            set.insert(c);
        }
    }
    set
});
static QUANTITY_ENDS: LazyLock<CharacterSet> = LazyLock::new(|| {
    let mut set = CharacterSet::new();
    for (key, _) in QUANTITIES.lines().filter_map(|row| row.split_once('\t')) {
        if let Some(c) = key.chars().last() {
            set.insert(c);
        }
    }
    set
});
static WHITELIST_STARTS: LazyLock<CharacterSet> = LazyLock::new(|| {
    let mut set = CharacterSet::new();
    for (key, _) in WHITELIST.lines().filter_map(|row| row.split_once('\t')) {
        if let Some(c) = key.chars().next() {
            set.insert(c);
        }
    }
    for c in ['N', 'n', 'B', 'b', '§', '©'] {
        set.insert(c);
    }
    set
});

static NUMBER_LETTERS: LazyLock<[bool; 128]> = LazyLock::new(|| {
    let mut result = [false; 128];
    for (key, _) in QUANTITIES.lines().filter_map(|row| row.split_once('\t')) {
        for byte in key.bytes().filter(u8::is_ascii) {
            result[byte as usize] = true;
        }
    }
    result
});

fn number_char(c: char) -> bool {
    if c.is_ascii() {
        c.is_ascii_digit()
            || NUMBER_LETTERS[c as usize]
            || matches!(c, ' ' | '\t' | '\r' | '\n' | '-' | '~' | ':' | ',' | '.' | '/')
    } else {
        matches!(
            c,
            '\u{a0}'
                | '\u{202f}'
                | '−'
                | '–'
                | '±'
                | '⁄'
                | '½'
                | '¼'
                | '¾'
                | '⅓'
                | '⅔'
                | '⅕'
                | '⅖'
                | '⅗'
                | '⅘'
                | '⅙'
                | '⅚'
                | '⅛'
                | '⅜'
                | '⅝'
                | '⅞'
                | '⅐'
                | '⅑'
                | '⅒'
        )
    }
}
fn field(k: &str, v: &str) -> String {
    format!("{k}: \"{v}\"")
}
fn token(k: &str, v: &str) -> String {
    format!("{k} {{ {v} }}")
}
fn name(v: &str) -> String {
    field("name", &v.replace(' ', "\u{a0}"))
}
fn push(out: &mut Vec<(i64, i32, String)>, weight: f64, rank: i32, tag: String) {
    out.push((cost(weight), rank, tag));
}
fn signed(s: &str) -> (&str, Option<&'static str>) {
    if let Some(v) = s.strip_prefix(['-', '−']) {
        (v, Some("true"))
    } else if let Some(v) = s.strip_prefix('~') {
        (v.strip_prefix(' ').unwrap_or(v), Some("humigit-kumulang"))
    } else if let Some(v) = s.strip_prefix('±') {
        (v.strip_prefix(' ').unwrap_or(v), Some("plus-minus"))
    } else {
        (s, None)
    }
}
fn sign_field(v: Option<&str>) -> String {
    v.map(|x| format!("{} ", field("negative", x))).unwrap_or_default()
}
fn card(s: &str) -> Option<String> {
    let (s, sign) = signed(s);
    let n = cardinal_with_sep(s)?;
    let mut tag = String::with_capacity(n.len() + 45);
    tag.push_str("cardinal { ");
    if let Some(sign) = sign {
        tag.push_str("negative: \"");
        tag.push_str(sign);
        tag.push_str("\" ");
    }
    tag.push_str("integer: \"");
    tag.push_str(&n);
    tag.push_str("\" }");
    Some(tag)
}
fn ordinal(s: &str) -> Option<String> {
    let n = ["ika-", "ika", "Ika-", "Ika", "pang-", "Pang-"]
        .iter()
        .find_map(|p| s.strip_prefix(*p))
        .or_else(|| ["st", "nd", "rd", "th", "ST", "ND", "RD", "TH"].iter().find_map(|p| s.strip_suffix(*p)))?;
    Some(token("ordinal", &field("integer", &cardinal_with_sep(n)?)))
}

fn ordinal_input(s: &str) -> bool {
    ["ika-", "ika", "Ika-", "Ika", "pang-", "Pang-"]
        .iter()
        .find_map(|p| s.strip_prefix(*p))
        .or_else(|| ["st", "nd", "rd", "th", "ST", "ND", "RD", "TH"].iter().find_map(|p| s.strip_suffix(*p)))
        .is_some_and(plain_cardinal_input)
}
fn decimal(s: &str) -> Option<String> {
    let (s, sign) = signed(s);
    let mut body = s;
    let mut qty = None;
    for &(k, v) in QUANTITY_ROWS.iter() {
        if let Some(before) = s.strip_suffix(k) {
            body = before.strip_suffix(' ').unwrap_or(before);
            qty = Some(v);
            break;
        }
    }
    let mut value = None;
    for (i, c) in body.char_indices() {
        if c != '.' && c != ',' {
            continue;
        }
        let n = &body[..i];
        let f = &body[i + 1..];
        if c == ',' && f.len() == 3 {
            continue;
        }
        if let (Some(n), Some(f)) = (cardinal_with_sep(n), digit_sequence(f)) {
            value = Some((n, Some(f)));
        }
    }
    let (n, f) = match value {
        Some(v) => v,
        None if qty.is_some() && body.len() <= 3 && body != "0" => (cardinal(body)?, None),
        _ => return None,
    };
    let mut tag = String::with_capacity(n.len() + f.as_ref().map_or(0, String::len) + 100);
    tag.push_str("decimal { ");
    if let Some(sign) = sign {
        tag.push_str("negative: \"");
        tag.push_str(sign);
        tag.push_str("\" ");
    }
    tag.push_str("integer_part: \"");
    tag.push_str(&n);
    tag.push('"');
    if let Some(f) = f {
        tag.push_str(" fractional_part: \"");
        tag.push_str(&f);
        tag.push('"');
    }
    if let Some(q) = qty {
        tag.push_str(" quantity: \"");
        tag.push_str(q);
        tag.push('"');
    }
    tag.push_str(" preserve_order: true }");
    Some(tag)
}
fn fraction(s: &str) -> Option<String> {
    let (s, sign) = signed(s);
    let vulgar = [
        ('½', 1, 2),
        ('¼', 1, 4),
        ('¾', 3, 4),
        ('⅓', 1, 3),
        ('⅔', 2, 3),
        ('⅕', 1, 5),
        ('⅖', 2, 5),
        ('⅗', 3, 5),
        ('⅘', 4, 5),
        ('⅙', 1, 6),
        ('⅚', 5, 6),
        ('⅛', 1, 8),
        ('⅜', 3, 8),
        ('⅝', 5, 8),
        ('⅞', 7, 8),
        ('⅐', 1, 7),
        ('⅑', 1, 9),
        ('⅒', 1, 10),
    ];
    let mut integer = None;
    let (n, d) = if let Some((c, n, d)) = vulgar.iter().find(|(c, _, _)| s.ends_with(*c)) {
        let before = &s[..s.len() - c.len_utf8()];
        if !before.is_empty() {
            integer = Some(cardinal_with_sep(before.strip_suffix(' ').unwrap_or(before))?);
        }
        (cardinal(&n.to_string())?, cardinal(&d.to_string())?)
    } else {
        let (left, right) = if let Some(v) = s.split_once(" / ") {
            v
        } else if let Some(v) = s.split_once('/') {
            v
        } else {
            s.split_once('⁄')?
        };
        let n = if let Some((i, n)) = left.split_once(' ') {
            integer = Some(cardinal_with_sep(i)?);
            n
        } else {
            left
        };
        (cardinal(n)?, cardinal(right)?)
    };
    let mut fields = sign_field(sign);
    if let Some(i) = integer {
        fields.push_str(&field("integer_part", &i));
        fields.push(' ');
    }
    fields.push_str(&field("numerator", &n));
    fields.push(' ');
    fields.push_str(&field("denominator", &d));
    fields.push_str(" preserve_order: true");
    Some(token("fraction", &fields))
}
fn day(s: &str) -> Option<String> {
    if s.is_empty() || s.len() > 2 || !s.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    let n = s.parse::<u32>().ok()?;
    if !(1..=31).contains(&n) {
        return None;
    }
    if n == 1 { Some("una".into()) } else { cardinal(if s.len() == 2 && s.starts_with('0') { &s[1..] } else { s }) }
}
fn month_num(s: &str) -> Option<&'static str> {
    if s.is_empty() || s.len() > 2 || !s.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    let key = if s.len() == 2 && s.starts_with('0') { &s[1..] } else { s };
    MONTH_NUM_INDEX.get(key).copied()
}
fn month_name(s: &str) -> Option<&'static str> {
    MONTHS_INDEX.get(s).copied().or_else(|| s.strip_suffix('.').and_then(|key| MONTH_ABBR_INDEX.get(key).copied()))
}
fn year(s: &str) -> Option<String> {
    if (s.len() == 4 && (s.starts_with('1') || s.starts_with('2'))) || s.len() == 2 { cardinal(s) } else { None }
}
fn date(s: &str) -> Vec<(i32, String)> {
    let mut out = Vec::new();
    for sep in ['/', '-', '.'] {
        let mut pieces = s.split(sep);
        let (Some(a), Some(b), Some(c), None) = (pieces.next(), pieces.next(), pieces.next(), pieces.next()) else {
            continue;
        };
        let p = [a, b, c];
        if let (Some(d), Some(m), Some(y)) = (day(p[0]), month_num(p[1]), year(p[2])) {
            out.push((1, token("date", &format!("{} {} {}", field("day", &d), field("month", m), field("year", &y)))));
        }
        if let (Some(m), Some(d), Some(y)) = (month_num(p[0]), day(p[1]), year(p[2])) {
            out.push((0, token("date", &format!("{} {} {}", field("month", m), field("day", &d), field("year", &y)))));
        }
        if sep == '-' && p[0].len() == 4 {
            if let (Some(y), Some(m), Some(d)) = (year(p[0]), month_num(p[1]), day(p[2])) {
                out.push((
                    1,
                    token("date", &format!("{} {} {}", field("year", &y), field("month", m), field("day", &d))),
                ));
            }
        }
    }
    let mut parts = s.split(' ');
    let first = parts.next().unwrap_or("");
    let second = parts.next();
    let third = parts.next();
    let extra = parts.next();
    let words = [first, second.unwrap_or(""), third.unwrap_or("")];
    let nwords = if third.is_some() {
        3
    } else if second.is_some() {
        2
    } else {
        1
    };
    if extra.is_none() && (2..=3).contains(&nwords) {
        if let (Some(d), Some(m)) = (day(words[0]), month_name(words[1])) {
            let y = if nwords == 3 { year(words[2]) } else { None };
            if nwords == 2 || y.is_some() {
                let mut f = format!("{} {}", field("day", &d), field("month", m));
                if let Some(y) = y {
                    f.push(' ');
                    f.push_str(&field("year", &y));
                }
                out.push((1, token("date", &f)));
            }
        }
        if let Some(m) = month_name(words[0]) {
            if nwords == 2 && words[1].len() == 4 {
                if let Some(y) = year(words[1]) {
                    out.push((1, token("date", &format!("{} {}", field("month", m), field("year", &y)))));
                }
            }
            if let Some(d) = day(words[1].trim_end_matches(',')) {
                let y = if nwords == 3 { year(words[2]) } else { None };
                if nwords == 2 || y.is_some() {
                    let mut f = format!("{} {}", field("month", m), field("day", &d));
                    if let Some(y) = y {
                        f.push(' ');
                        f.push_str(&field("year", &y));
                    }
                    out.push((1, token("date", &f)));
                }
            }
        }
    }
    let decade = s
        .strip_suffix("s'")
        .or_else(|| s.strip_suffix("-an"))
        .or_else(|| s.strip_suffix('s'))
        .or_else(|| ["dekada ", "Dekada ", "mga taong "].iter().find_map(|p| s.strip_prefix(*p)));
    if let Some(d) = decade {
        if d.len() == 4 && d.ends_with('0') {
            if let Some(y) = year(d) {
                out.push((1, token("date", &field("year", &format!("mga taong {y}")))));
            }
        }
    }
    for (codes, meaning, leading) in [
        (&["BC", "B.C.", "BK", "B.K.", "BCE", "B.C.E."][..], "bago si Kristo", false),
        (&["AD", "A.D.", "MK", "M.K.", "CE", "C.E."][..], "ng ating panahon", true),
    ] {
        for code in codes {
            let number = s.strip_suffix(*code).map(|n| n.strip_suffix(' ').unwrap_or(n)).or_else(|| {
                if leading { s.strip_prefix(*code).map(|n| n.strip_prefix(' ').unwrap_or(n)) } else { None }
            });
            if let Some(n) = number {
                if n.len() <= 4 {
                    if let Some(y) = cardinal(n) {
                        out.push((1, token("date", &field("year", &format!("{y} {meaning}")))));
                    }
                }
            }
        }
    }
    out
}

fn clock_parts(s: &str, sep: char) -> Option<(String, Option<String>, Option<String>)> {
    let mut parts = s.split(sep);
    let hour = parts.next()?;
    let minute_part = parts.next()?;
    let seconds = parts.next();
    if parts.next().is_some() || (seconds.is_some() && sep != ':') {
        return None;
    }
    if hour.is_empty() || hour.len() > 2 || !hour.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    let h = hour.parse::<u32>().ok()?;
    if h > 24 {
        return None;
    }
    let minute = |v: &str| -> Option<Option<String>> {
        if v == "00" {
            return Some(None);
        }
        if v.len() != 2 || !v.bytes().all(|b| b.is_ascii_digit()) {
            return None;
        }
        let n = v.parse::<u32>().ok()?;
        if n == 0 || n >= 60 {
            return None;
        }
        Some(Some(cardinal(if v.starts_with('0') { &v[1..] } else { v })?))
    };
    let m = minute(minute_part)?;
    let sec = match seconds {
        Some(v) => Some(minute(v)??),
        None => None,
    };
    let hour = if hour.len() == 2 && hour.starts_with('0') { &hour[1..] } else { hour };
    Some((cardinal(hour)?, m, sec))
}
fn time(s: &str) -> Option<String> {
    let mut body = s;
    let mut pref = false;
    for p in ["oras ", "Oras ", "ika-", "Ika-"] {
        if let Some(v) = body.strip_prefix(p) {
            body = v;
            pref = true;
            break;
        }
    }
    for dash in ['-', '–'] {
        if let Some((a, b)) = body.split_once(dash) {
            let (a, am, _) = clock_parts(a.trim_end_matches(' '), ':')?;
            let (b, bm, _) = clock_parts(b.trim_start_matches(' '), ':')?;
            let part = |h: String, m: Option<String>| {
                if let Some(m) = m { format!("ika-{h} at {m} minuto") } else { format!("ika-{h}") }
            };
            return Some(token(
                "time",
                &format!("range: \"{} hanggang {}\" preserve_order: true", part(a, am), part(b, bm)),
            ));
        }
    }
    let mut zone = None;
    if let Some((a, b)) = body.rsplit_once(' ') {
        if let Some(z) = ZONES_INDEX.get(b).copied() {
            body = a;
            zone = Some(z);
        }
    }
    let mut suffix = None;
    if let Some((a, b)) = body.rsplit_once(' ') {
        suffix = match b {
            "am" | "a.m." | "AM" | "A.M." | "n.u." => Some("ng umaga"),
            "pm" | "p.m." | "PM" | "P.M." | "n.h." => Some("ng hapon"),
            "n.g." => Some("ng gabi"),
            _ => None,
        };
        if suffix.is_some() {
            body = a;
        }
    }
    let sep = if body.contains(':') { ':' } else { '.' };
    if sep == '.' && !pref && suffix.is_none() && zone.is_none() {
        return None;
    }
    let (h, m, sec) = clock_parts(body, sep)?;
    let mut tag = String::from("time { hours: \"");
    tag.push_str(&h);
    tag.push('"');
    if let Some(v) = m {
        tag.push_str(" minutes: \"");
        tag.push_str(&v);
        tag.push('"');
    }
    if let Some(v) = sec {
        tag.push_str(" seconds: \"");
        tag.push_str(&v);
        tag.push('"');
    }
    if let Some(v) = suffix {
        tag.push_str(" suffix: \"");
        tag.push_str(v);
        tag.push('"');
    }
    if let Some(v) = zone {
        tag.push_str(" zone: \"");
        tag.extend(v.chars().map(|c| if c == ' ' { '\u{a0}' } else { c }));
        tag.push('"');
    }
    tag.push_str(" preserve_order: true }");
    Some(tag)
}

fn clock_input(s: &str, sep: char) -> bool {
    let mut parts = s.split(sep);
    let Some(hour) = parts.next() else {
        return false;
    };
    let Some(minute) = parts.next() else {
        return false;
    };
    if hour.is_empty()
        || hour.len() > 2
        || !hour.bytes().all(|b| b.is_ascii_digit())
        || hour.parse::<u32>().ok().is_none_or(|n| n > 24)
    {
        return false;
    }
    let valid_minute = |s: &str| {
        s.len() == 2 && s.bytes().all(|b| b.is_ascii_digit()) && s.parse::<u32>().ok().is_some_and(|n| n < 60)
    };
    if !valid_minute(minute) {
        return false;
    }
    if let Some(seconds) = parts.next() {
        if sep != ':' || seconds == "00" || !valid_minute(seconds) || parts.next().is_some() {
            return false;
        }
    }
    true
}

fn time_input(s: &str) -> bool {
    let mut body = s;
    let mut pref = false;
    for p in ["oras ", "Oras ", "ika-", "Ika-"] {
        if let Some(v) = body.strip_prefix(p) {
            body = v;
            pref = true;
            break;
        }
    }
    for dash in ['-', '–'] {
        if let Some((a, b)) = body.split_once(dash) {
            return clock_input(a.trim_end_matches(' '), ':') && clock_input(b.trim_start_matches(' '), ':');
        }
    }
    let mut zone = false;
    if let Some((a, b)) = body.rsplit_once(' ') {
        if ZONES_INDEX.contains_key(b) {
            body = a;
            zone = true;
        }
    }
    let mut suffix = false;
    if let Some((a, b)) = body.rsplit_once(' ') {
        if matches!(b, "am" | "a.m." | "AM" | "A.M." | "n.u." | "pm" | "p.m." | "PM" | "P.M." | "n.h." | "n.g.") {
            body = a;
            suffix = true;
        }
    }
    let sep = if body.contains(':') { ':' } else { '.' };
    (sep != '.' || pref || suffix || zone) && clock_input(body, sep)
}

/// Prefixes of the only suffix and zone words that can follow a numeric
/// clock. The table is derived from the same zone data as the classifier.
static TIME_CONTINUATIONS: LazyLock<HashSet<&'static str, BuildHasherDefault<TableHasher>>> = LazyLock::new(|| {
    let mut prefixes = HashSet::default();
    let suffixes = ["am", "a.m.", "AM", "A.M.", "n.u.", "pm", "p.m.", "PM", "P.M.", "n.h.", "n.g."];
    for word in suffixes.into_iter().chain(ZONES.lines().filter_map(|row| row.split_once('\t').map(|(key, _)| key))) {
        for (i, _) in word.char_indices().skip(1) {
            prefixes.insert(&word[..i]);
        }
        prefixes.insert(word);
    }
    prefixes
});

fn possible_time_continuation(s: &str) -> bool {
    let Some((clock, rest)) = s.split_once(' ') else {
        return true;
    };
    if clock.contains(['-', '–']) {
        return true;
    }
    let rest = rest.trim_start_matches(' ');
    if rest.is_empty() || rest.starts_with(['-', '–']) {
        return true;
    }
    let word = rest.split(' ').next().unwrap_or("");
    TIME_CONTINUATIONS.contains(word)
}

fn measure(s: &str) -> Vec<(f64, String)> {
    let mut out = Vec::new();
    for (i, _) in s.char_indices().skip(1) {
        if !number_char(s[..i].chars().last().unwrap()) {
            break;
        }
        let mut num = &s[..i];
        let mut unit = &s[i..];
        let mut spaced = unit.starts_with([' ', '\u{a0}', '\u{202f}']);
        if spaced {
            unit = &unit[unit.chars().next().unwrap().len_utf8()..];
        } else if num.ends_with([' ', '\u{a0}', '\u{202f}']) {
            spaced = true;
            num = &num[..num.len() - num.chars().last().unwrap().len_utf8()];
        }
        let (u, denom) = unit.split_once('/').map_or((unit, None), |(u, d)| (u, Some(d)));
        if !spaced && denom.is_none() && ["s", "t", "A", "J", "N", "cd"].contains(&u) {
            continue;
        }
        let Some(word) = MEASURES_INDEX.get(u).copied() else {
            continue;
        };
        let units = word.replace(' ', "\u{a0}");
        let variants = if let Some(d) = denom {
            let Some(d) = DENOM_INDEX.get(d).copied() else {
                continue;
            };
            let d = d.replace(' ', "\u{a0}");
            vec![format!("{units}\u{a0}kada\u{a0}{d}"), format!("{units}\u{a0}bawat\u{a0}{d}")]
        } else {
            vec![units]
        };
        for (weight, t) in [card(num), decimal(num), fraction(num)]
            .into_iter()
            .flatten()
            .map(|t| (1.11, t))
            .chain(card_compound(num).into_iter().map(|(w, t)| (w + 0.01, t)))
        {
            for units in &variants {
                out.push((weight, token("measure", &format!("{t} {} preserve_order: true", field("units", units)))));
            }
        }
    }
    out
}

/// Reconstruct a recognized simple measure without exploring other number
/// branches or allocating the full set of measure alternatives.
fn simple_measure_tag(s: &str, rank: i32) -> Option<String> {
    for (i, _) in s.char_indices().skip(1) {
        let mut num = &s[..i];
        let mut unit = &s[i..];
        let mut spaced = unit.starts_with([' ', '\u{a0}', '\u{202f}']);
        if spaced {
            unit = &unit[unit.chars().next()?.len_utf8()..];
        } else if num.ends_with([' ', '\u{a0}', '\u{202f}']) {
            spaced = true;
            num = &num[..num.len() - num.chars().last()?.len_utf8()];
        }
        if !spaced && ["s", "t", "A", "J", "N", "cd"].contains(&unit) {
            continue;
        }
        let Some(units) = MEASURES_INDEX.get(unit).copied() else {
            continue;
        };
        let number = if rank == 6 && cardinal_input(num) {
            card(num)
        } else if rank == 4 && decimal_input(num) {
            decimal(num).filter(|tag| tag.contains(" quantity: "))
        } else {
            None
        };
        if let Some(number) = number {
            let mut tag = String::with_capacity(number.len() + units.len() + 45);
            tag.push_str("measure { ");
            tag.push_str(&number);
            tag.push_str(" units: \"");
            tag.extend(units.chars().map(|c| if c == ' ' { '\u{a0}' } else { c }));
            tag.push_str("\" preserve_order: true }");
            return Some(tag);
        }
    }
    None
}
fn money_amount(s: &str, currency: &str, prefix: bool) -> Option<String> {
    let mut amount = s;
    let mut qty = None;
    for (k, v) in QUANTITY_ROWS.iter().copied().chain(MONEY_QUANTITY_ROWS.iter().copied()) {
        if let Some(a) = amount.strip_suffix(k) {
            amount = a.strip_suffix(' ').unwrap_or(a);
            qty = Some(v);
            break;
        }
    }
    let mut integer = cardinal_with_sep(amount);
    let mut fractional = None;
    let mut minor = false;
    for (i, c) in amount.char_indices() {
        if c != '.' && c != ',' {
            continue;
        }
        let a = &amount[..i];
        let b = &amount[i + 1..];
        if integer.is_none() && qty.is_none() && (b == "-" || (!b.is_empty() && b.bytes().all(|c| c == b'0'))) {
            if let Some(n) = cardinal_with_sep(a) {
                integer = Some(n);
            }
        }
        if integer.is_none() && b.len() == 2 && b.bytes().all(|c| c.is_ascii_digit()) {
            if let Some(n) = cardinal_with_sep(a) {
                integer = Some(n);
                if b != "00" {
                    fractional = cardinal(&b.parse::<u32>().ok()?.to_string());
                    minor = true;
                }
            }
        }
        if qty.is_some() {
            if let (Some(n), Some(f)) = (cardinal_with_sep(a), digit_sequence(b)) {
                integer = Some(n);
                fractional = Some(f);
                minor = false;
            }
        }
    }
    if integer.is_none() {
        integer = cardinal_with_sep(amount);
    }
    if integer.is_none() {
        for dash in ['-', '–'] {
            if let Some((a, b)) = amount.split_once(dash) {
                integer = Some(format!("{} hanggang {}", cardinal_with_sep(a)?, cardinal_with_sep(b)?));
                break;
            }
        }
    }
    let mut f = field("integer_part", &integer?);
    if prefix {
        if let Some(q) = qty {
            if let Some(v) = fractional.as_ref() {
                f.push(' ');
                f.push_str(&field("fractional_part", v));
            }
            f.push(' ');
            f.push_str(&field("quantity", q));
        }
        f.push(' ');
        f.push_str(&field("currency_maj", currency));
        if minor {
            f.push(' ');
            f.push_str(&field("fractional_part", fractional.as_ref()?));
            f.push_str(" currency_min: \"sentimo\"");
        }
    } else {
        if let Some(v) = fractional.as_ref() {
            f.push(' ');
            f.push_str(&field("fractional_part", v));
            if minor {
                f.push_str(" currency_min: \"sentimo\"");
            }
        }
        if let Some(q) = qty {
            f.push(' ');
            f.push_str(&field("quantity", q));
        }
        f.push(' ');
        f.push_str(&field("currency_maj", &currency.replace(' ', "\u{a0}")));
    }
    Some(f)
}
fn money(s: &str) -> Vec<String> {
    let mut out = Vec::new();
    let (s, negative) = if let Some(v) = s.strip_prefix(['-', '−']) { (v, true) } else { (s, false) };
    let s = if s.starts_with('(') && s.ends_with(')') { &s[1..s.len() - 1] } else { s };
    let mut rate = None;
    let mut s = s;
    for (suffix, v) in [
        ("/oras", "bawat oras"),
        ("/h", "bawat oras"),
        ("/hr", "bawat oras"),
        ("/minuto", "bawat minuto"),
        ("/min", "bawat minuto"),
        ("/segundo", "bawat segundo"),
        ("/s", "bawat segundo"),
        ("/araw", "bawat araw"),
        ("/d", "bawat araw"),
        ("/linggo", "bawat linggo"),
        ("/wk", "bawat linggo"),
        ("/buwan", "bawat buwan"),
        ("/mo", "bawat buwan"),
        ("/taon", "bawat taon"),
        ("/yr", "bawat taon"),
        ("/kilo", "bawat kilo"),
        ("/kg", "bawat kilo"),
        ("/gramo", "bawat gramo"),
        ("/g", "bawat gramo"),
        ("/litro", "bawat litro"),
        ("/l", "bawat litro"),
        ("/L", "bawat litro"),
        ("/metro", "bawat metro"),
        ("/m", "bawat metro"),
        ("/kilometro", "bawat kilometro"),
        ("/km", "bawat kilometro"),
        ("/tao", "bawat tao"),
        ("/unit", "bawat yunit"),
    ] {
        if let Some(v0) = s.strip_suffix(suffix) {
            s = v0.strip_suffix(' ').unwrap_or(v0);
            rate = Some(v);
            break;
        }
    }
    for row in CURRENCY.lines() {
        let Some((sym, curr)) = row.split_once('\t') else {
            continue;
        };
        if let Some(n) = s.strip_prefix(sym) {
            let n = n.strip_prefix(' ').unwrap_or(n);
            if let Some(mut f) = money_amount(n, curr, true) {
                if negative {
                    f = format!("negative: \"minus\" {f}");
                }
                if let Some(r) = rate {
                    f.push(' ');
                    f.push_str(&field("morphosyntactic_features", r));
                }
                out.push(token("money", &f));
            }
        }
        if let Some(n) = s.strip_suffix(sym) {
            let n = n.strip_suffix(' ').unwrap_or(n);
            if let Some(mut f) = money_amount(n, curr, false) {
                if negative {
                    f = format!("negative: \"minus\" {f}");
                }
                if let Some(r) = rate {
                    f.push(' ');
                    f.push_str(&field("morphosyntactic_features", r));
                }
                out.push(token("money", &f));
            }
        }
    }
    let mut add_cents = |n: String| {
        let mut f = format!("{} currency_min: \"sentimo\"", field("fractional_part", &n));
        if negative {
            f = format!("negative: \"minus\" {f}");
        }
        if let Some(r) = rate {
            f.push(' ');
            f.push_str(&field("morphosyntactic_features", r));
        }
        out.push(token("money", &f));
    };
    for symbol in ["₱", "$", "US$", "€", "£"] {
        if let Some(n) = s.strip_prefix(symbol) {
            let n = n.strip_prefix(' ').unwrap_or(n);
            if let Some(v) = n.strip_prefix("0.").or_else(|| n.strip_prefix("0,")) {
                if v.len() == 2 && v != "00" && v.bytes().all(|b| b.is_ascii_digit()) {
                    if let Ok(n) = v.parse::<u32>() {
                        if let Some(n) = cardinal(&n.to_string()) {
                            add_cents(n);
                        }
                    }
                }
            }
        }
    }
    for symbol in ["€", "$", "USD", "EUR", "PHP"] {
        if let Some(n) = s.strip_suffix(symbol) {
            let n = n.strip_suffix(' ').unwrap_or(n);
            if let Some(v) = n.strip_prefix("0.").or_else(|| n.strip_prefix("0,")) {
                if v.len() == 2 && v != "00" && v.bytes().all(|b| b.is_ascii_digit()) {
                    if let Ok(n) = v.parse::<u32>() {
                        if let Some(n) = cardinal(&n.to_string()) {
                            add_cents(n);
                        }
                    }
                }
            }
        }
    }
    if let Some(n) = s.strip_suffix('¢') {
        if let Some(n) = cardinal(n) {
            add_cents(n);
        }
    }
    out
}

fn money_ranges(s: &str) -> Vec<String> {
    let mut out = Vec::new();
    for (i, c) in s.char_indices() {
        if !matches!(c, '-' | '–') {
            continue;
        }
        let left = s[..i].strip_suffix(' ').unwrap_or(&s[..i]);
        let right0 = &s[i + c.len_utf8()..];
        let right = right0.strip_prefix(' ').unwrap_or(right0);
        for a in money(left) {
            for b in money(right) {
                out.push(format!("{a} }} tokens {{ name: \"hanggang\" }} tokens {{ {b}"));
            }
        }
    }
    out
}

fn international_phone(s: &str) -> Option<String> {
    let rest = s.strip_prefix('+')?;
    if !rest.chars().all(|c| c.is_ascii_digit() || matches!(c, ' ' | '-' | '.' | '(' | ')')) {
        return None;
    }
    let digits: String = rest.chars().filter(|c| c.is_ascii_digit()).collect();
    if !(7..=15).contains(&digits.len()) {
        return None;
    }
    Some(name(&format!("dagdag {}", digit_sequence(&digits)?)))
}

fn roman_number(s: &str) -> Option<u32> {
    let pairs = [(100, "C"), (90, "XC"), (50, "L"), (40, "XL"), (10, "X"), (9, "IX"), (5, "V"), (4, "IV"), (1, "I")];
    let mut rest = s;
    let mut n = 0;
    for (v, r) in pairs {
        while let Some(t) = rest.strip_prefix(r) {
            n += v;
            rest = t;
        }
    }
    if !rest.is_empty() || !(1..=100).contains(&n) {
        return None;
    }
    let mut rem = n;
    let mut canonical = String::new();
    for (v, r) in pairs {
        while rem >= v {
            rem -= v;
            canonical.push_str(r);
        }
    }
    (canonical == s).then_some(n)
}
fn ordinal_word(n: u32) -> Option<String> {
    let s = cardinal(&n.to_string())?;
    Some(match n {
        1 => "una".into(),
        2 => "ikalawa".into(),
        3 => "ikatlo".into(),
        _ => format!("ika{s}"),
    })
}
fn roman(s: &str) -> Vec<String> {
    let mut out = Vec::new();
    for p in ["ika-", "Ika-", "pang-", "Pang-"] {
        if let Some(v) = s.strip_prefix(p) {
            if let Some(n) = roman_number(v) {
                out.push(name(&ordinal_word(n).unwrap()));
            }
        }
    }
    const KEY: &[&str] = &[
        "kabanata",
        "siglo",
        "bahagi",
        "tomo",
        "aklat",
        "aralin",
        "yugto",
        "baitang",
        "antas",
        "seksiyon",
        "artikulo",
        "batas",
        "bilang",
        "blg.",
        "blg",
        "digmaang pandaigdig",
    ];
    const MON: &[&str] = &[
        "Papa",
        "Hari",
        "Reyna",
        "Pangulo",
        "Heneral",
        "Gobernador",
        "Juan Pablo",
        "Benedicto",
        "Francisco",
        "Pio",
        "Leo",
        "Pablo",
        "Gregorio",
        "Elizabeth",
        "Charles",
        "Henry",
        "George",
        "Edward",
        "William",
        "Richard",
        "James",
        "Victoria",
        "Louis",
        "Napoleon",
        "Philip",
        "Felipe",
        "Carlos",
        "Alfonso",
        "Fernando",
        "Alexander",
        "Nicholas",
    ];
    if let Some((key, r)) = s.rsplit_once(' ') {
        let is_key = KEY.iter().any(|k| {
            key == *k
                || key == k.to_uppercase()
                || key == k.split(' ').map(crate::text::py_capitalize).collect::<Vec<_>>().join(" ")
        });
        let is_mon = MON.iter().any(|k| key == *k || key == k.to_uppercase());
        let mut explicit = false;
        let mut r = r;
        if is_key {
            for p in ["ika-", "Ika-", "pang-", "Pang-"] {
                if let Some(v) = r.strip_prefix(p) {
                    r = v;
                    explicit = true;
                    break;
                }
            }
        }
        if let Some(n) = roman_number(r) {
            if is_key && (explicit || !["C", "D", "L", "M"].contains(&r)) {
                let v = if explicit { ordinal_word(n).unwrap() } else { cardinal(&n.to_string()).unwrap() };
                out.push(name(&format!("{key} {v}")));
            }
            if is_mon {
                out.push(name(&format!("{key} {}", ordinal_word(n).unwrap())));
                if !["C", "D", "L", "M"].contains(&r) {
                    out.push(name(&format!("{key} {}", cardinal(&n.to_string()).unwrap())));
                }
            }
        }
    }
    out
}
fn telephone(s: &str) -> Option<String> {
    let (country, number) = if let Some(v) = s.strip_prefix('+') {
        let i = v.find([' ', '-'])?;
        let code = &v[..i];
        if !(1..=3).contains(&code.len()) {
            return None;
        }
        (Some(format!("plus {}", digit_sequence(code)?)), &v[i + 1..])
    } else {
        (None, s)
    };
    if country.is_none() && !number.starts_with('0') && !number.starts_with("(0") {
        return None;
    }
    let (area, groups) = if let Some(rest) = number.strip_prefix('(') {
        let (a, b) = rest.split_once(')')?;
        if a.is_empty() || !a.bytes().all(|c| c.is_ascii_digit()) {
            return None;
        }
        (Some(a), b.strip_prefix(' ').unwrap_or(b))
    } else {
        (None, number)
    };
    if groups.is_empty()
        || groups.split([' ', '-', '.']).any(|p| p.is_empty() || !p.bytes().all(|c| c.is_ascii_digit()))
    {
        return None;
    }
    let digits: String = number.chars().filter(|c| c.is_ascii_digit()).collect();
    if digits.len() != 7 {
        return None;
    }
    let mut f = String::new();
    if let Some(c) = country {
        f.push_str(&field("country_code", &c));
        f.push(' ');
    }
    let _ = area;
    f.push_str(&field("number_part", &digit_sequence(&digits)?));
    f.push_str(" preserve_order: true");
    Some(token("telephone", &f))
}
fn electronic(s: &str) -> Option<String> {
    let label = |s: &str| !s.is_empty() && s.bytes().all(|b| b.is_ascii_alphanumeric() || b == b'-' || b == b'_');
    let host = |s: &str, need_dot: bool| {
        let (authority, path) = s.split_once('/').map_or((s, None), |(a, p)| (a, Some(p)));
        let (host, port) = authority.split_once(':').map_or((authority, None), |(h, p)| (h, Some(p)));
        if (need_dot && !host.contains('.')) || !host.split('.').all(label) {
            return false;
        }
        if let Some(port) = port {
            if port.is_empty() || port.len() > 5 || !port.bytes().all(|b| b.is_ascii_digit()) {
                return false;
            }
        }
        if let Some(path) = path {
            for part in path.split('/') {
                if !part.is_empty() {
                    if !part.bytes().all(|b| b.is_ascii_alphanumeric() || b".-_:~#+=?&%".contains(&b)) {
                        return false;
                    }
                    if !part.as_bytes().last().is_some_and(|b| b.is_ascii_alphanumeric() || matches!(*b, b'=' | b'_')) {
                        return false;
                    }
                }
            }
        }
        true
    };
    let mut f = String::new();
    if let Some((u, d)) = s.split_once('@') {
        if u.is_empty()
            || !u.bytes().all(|b| b.is_ascii_alphanumeric() || b".-_:~#+=?&%/".contains(&b))
            || !host(d, true)
        {
            return None;
        }
        f.push_str(&field("username", u));
        f.push(' ');
        f.push_str(&field("domain", d));
    } else {
        let (p, d) = if let Some(d) = s.strip_prefix("https://www.") {
            ("https://www.", d)
        } else if let Some(d) = s.strip_prefix("http://www.") {
            ("http://www.", d)
        } else if let Some(d) = s.strip_prefix("https://") {
            ("https://", d)
        } else if let Some(d) = s.strip_prefix("http://") {
            ("http://", d)
        } else if let Some(d) = s.strip_prefix("www.") {
            ("www.", d)
        } else {
            ("", s)
        };
        if !host(d, p.is_empty()) {
            return None;
        }
        if p.is_empty() {
            let host = d.split(['/', ':']).next().unwrap_or("");
            let tld = host.rsplit('.').next()?;
            let domains = include_str!("../../../data/tl/electronic/domain.tsv");
            if !domains.lines().any(|l| l.split('\t').next().unwrap_or("").trim_start_matches('.') == tld) {
                return None;
            }
        }
        if !p.is_empty() {
            f.push_str(&field("protocol", p));
            f.push(' ');
        }
        f.push_str(&field("domain", d));
    }
    f.push_str(" preserve_order: true");
    Some(token("electronic", &f))
}
fn serial(s: &str) -> Option<String> {
    if let Some((v, power)) = s.split_once('^') {
        if v.len() == 1 && v.as_bytes()[0].is_ascii_alphabetic() {
            let val = if power == "2" {
                "kuwadrado".into()
            } else if power == "3" {
                "kubiko".into()
            } else {
                let (p, neg) = if let Some(n) = power.strip_prefix(['-', '−']) { (n, "minus ") } else { (power, "") };
                if p.len() > 4 {
                    return None;
                }
                format!("sa kapangyarihan ng {neg}{}", cardinal(p)?)
            };
            return Some(token("cardinal", &field("integer", &format!("{v} {val}"))));
        }
    }
    for (suf, word) in [('²', "kuwadrado"), ('³', "kubiko")] {
        if let Some(v) = s.strip_suffix(suf) {
            if v.len() == 1 && v.as_bytes()[0].is_ascii_alphabetic() {
                return Some(token("cardinal", &field("integer", &format!("{v} {word}"))));
            }
        }
    }
    if !s.chars().any(|c| c.is_ascii_digit()) {
        return None;
    }
    let mut out = String::new();
    let mut run = String::new();
    let mut kind = 0;
    let flush = |run: &mut String, kind: i32, out: &mut String| -> Option<()> {
        if run.is_empty() {
            return Some(());
        }
        let w = if kind == 1 { run.clone() } else { cardinal(run).or_else(|| digit_sequence(run))? };
        if !out.is_empty() {
            out.push(' ');
        }
        out.push_str(&w);
        run.clear();
        Some(())
    };
    let mut chars = s.chars().peekable();
    let mut after_delimiter = true;
    let mut pieces = 0;
    while let Some(c) = chars.next() {
        let k = if c.is_ascii_alphabetic() {
            1
        } else if c.is_ascii_digit() {
            2
        } else {
            0
        };
        if k == 0 {
            if after_delimiter {
                return None;
            }
            let numeric_dot = c == '.' && kind == 2 && chars.peek().is_some_and(|c| c.is_ascii_digit());
            flush(&mut run, kind, &mut out)?;
            if !matches!(c, '-' | '/' | ':' | '_' | '.') {
                return None;
            }
            if numeric_dot {
                out.push_str(" tuldok");
            } else {
                pieces = 0;
            }
            kind = 0;
            after_delimiter = true;
        } else {
            if kind != k {
                if kind != 0 || pieces == 0 {
                    pieces += 1;
                }
                if pieces > 3 {
                    return None;
                }
                flush(&mut run, kind, &mut out)?;
            }
            run.push(c);
            kind = k;
            after_delimiter = false;
        }
    }
    if after_delimiter {
        return None;
    }
    flush(&mut run, kind, &mut out)?;
    Some(token("cardinal", &field("integer", &out)))
}
fn card_compound(s: &str) -> Vec<(f64, String)> {
    let mut out = Vec::new();
    for sep in ['-', '–', ':'] {
        if let Some((a, b)) = s.split_once(sep) {
            if let (Some(a), Some(b)) = (cardinal_with_sep(a), cardinal_with_sep(b)) {
                let w = if sep == ':' { "laban sa" } else { "hanggang" };
                out.push((
                    if sep == ':' { 1.2 } else { 1.1 },
                    token("cardinal", &field("integer", &format!("{a} {w} {b}"))),
                ));
            }
        }
    }
    if let Some((a, b)) = s.split_once(':') {
        if let (Some(a), Some(b)) = (cardinal_with_sep(a), digit_sequence(b)) {
            out.push((1.3, token("cardinal", &field("integer", &format!("{a} laban sa {b}")))));
        }
    }
    if s.len() > 1 && (s.starts_with('0') || s.len() > 15) {
        if let Some(n) = digit_sequence(s) {
            out.push((1.1, token("cardinal", &field("integer", &n))));
        }
    }
    let parts: Vec<_> = s.split(',').collect();
    if parts.len() == 3 {
        let nums: Option<Vec<_>> = parts.iter().map(|p| if p.len() <= 3 { cardinal(p) } else { None }).collect();
        if let Some(n) = nums {
            out.push((1.1, token("cardinal", &field("integer", &n.join(", ")))));
        }
    }
    out
}
fn whitelist(s: &str) -> Vec<String> {
    let mut out = Vec::new();
    if let Some(v) = WHITELIST_INDEX.get(s).copied() {
        out.push(name(v));
    }
    for (p, v) in [
        ("No.", "bilang"),
        ("no.", "bilang"),
        ("Blg.", "bilang"),
        ("blg.", "bilang"),
        ("Blg", "bilang"),
        ("blg", "bilang"),
        ("N°", "bilang"),
        ("n°", "bilang"),
        ("Nº", "bilang"),
        ("nº", "bilang"),
        ("§", "seksiyon"),
        ("©", "karapatang-ari"),
    ] {
        if let Some(n) = s.strip_prefix(p) {
            let n = n.strip_prefix([' ', '\u{a0}', '\u{202f}']).unwrap_or(n);
            if let Some(n) = cardinal_with_sep(n) {
                out.push(name(&format!("{v} {n}")));
            }
        }
    }
    out
}

/// Recognition of the cardinal graph without spelling its number.
fn cardinal_input(s: &str) -> bool {
    let (s, _) = signed(s);
    if s == "0" {
        return true;
    }
    if s.is_empty() {
        return false;
    }
    let mut groups = s.split([',', '.']);
    let first = groups.next().unwrap_or("");
    if first.is_empty() || first.starts_with('0') || !first.bytes().all(|b| b.is_ascii_digit()) {
        return false;
    }
    let mut digits = first.len();
    let mut count = 0;
    for group in groups {
        count += 1;
        if group.len() != 3 || !group.bytes().all(|b| b.is_ascii_digit()) {
            return false;
        }
        digits += 3;
    }
    digits <= 15 && (count == 0 || (first.len() <= 3 && count <= 4))
}

fn plain_cardinal_input(s: &str) -> bool {
    s.as_bytes().first().is_some_and(u8::is_ascii_digit) && cardinal_input(s)
}

fn decimal_input(s: &str) -> bool {
    let (s, _) = signed(s);
    let mut body = s;
    let mut quantity = false;
    for &(key, _) in QUANTITY_ROWS.iter() {
        if let Some(before) = s.strip_suffix(key) {
            body = before.strip_suffix(' ').unwrap_or(before);
            quantity = true;
            break;
        }
    }
    for (i, c) in body.char_indices() {
        if !matches!(c, '.' | ',') {
            continue;
        }
        let fractional = &body[i + 1..];
        if c == ',' && fractional.len() == 3 {
            continue;
        }
        if plain_cardinal_input(&body[..i]) && !fractional.is_empty() && fractional.bytes().all(|b| b.is_ascii_digit())
        {
            return true;
        }
    }
    quantity && body.len() <= 3 && body != "0" && plain_cardinal_input(body)
}

fn compound_candidates(s: &str, end: usize, out: &mut Vec<Candidate>) {
    let mut emit = |weight| out.push(Candidate { end, cost: cost(weight), rank: 7, tag: String::new() });
    for separator in ['-', '–', ':'] {
        if let Some((a, b)) = s.split_once(separator) {
            if plain_cardinal_input(a) && plain_cardinal_input(b) {
                emit(if separator == ':' { 1.2 } else { 1.1 });
            }
        }
    }
    if let Some((a, b)) = s.split_once(':') {
        if plain_cardinal_input(a) && !b.is_empty() && b.bytes().all(|x| x.is_ascii_digit()) {
            emit(1.3);
        }
    }
    if s.len() > 1 && (s.starts_with('0') || s.len() > 15) && s.bytes().all(|b| b.is_ascii_digit()) {
        emit(1.1);
    }
    let mut parts = s.split(',');
    if let (Some(a), Some(b), Some(c), None) = (parts.next(), parts.next(), parts.next(), parts.next()) {
        if [a, b, c].into_iter().all(|part| part.len() <= 3 && plain_cardinal_input(part) && !part.contains(['.', ',']))
        {
            emit(1.1);
        }
    }
}

fn serial_input(s: &str) -> Option<usize> {
    if let Some((v, power)) = s.split_once('^') {
        if v.len() == 1 && v.as_bytes()[0].is_ascii_alphabetic() {
            let p = power.strip_prefix(['-', '−']).unwrap_or(power);
            if matches!(power, "2" | "3") || (p.len() <= 4 && plain_cardinal_input(p) && !p.contains(['.', ','])) {
                return Some(0);
            }
        }
    }
    if let Some(v) = s.strip_suffix(['²', '³']) {
        if v.len() == 1 && v.as_bytes()[0].is_ascii_alphabetic() {
            return Some(0);
        }
    }
    if !s.bytes().any(|b| b.is_ascii_digit()) {
        return None;
    }
    let mut chars = s.chars().peekable();
    let mut kind = 0;
    let mut after_delimiter = true;
    let mut pieces = 0;
    let mut weighted_dots = 0;
    while let Some(c) = chars.next() {
        let k = if c.is_ascii_alphabetic() {
            1
        } else if c.is_ascii_digit() {
            2
        } else {
            0
        };
        if k == 0 {
            if after_delimiter || !matches!(c, '-' | '/' | ':' | '_' | '.') {
                return None;
            }
            let numeric_dot = c == '.' && kind == 2 && chars.peek().is_some_and(|c| c.is_ascii_digit());
            if c == '.' && !numeric_dot {
                weighted_dots += 1;
            }
            if !numeric_dot {
                pieces = 0;
            }
            kind = 0;
            after_delimiter = true;
        } else {
            if kind != k {
                if kind != 0 || pieces == 0 {
                    pieces += 1;
                }
                if pieces > 3 {
                    return None;
                }
            }
            kind = k;
            after_delimiter = false;
        }
    }
    (!after_delimiter).then_some(weighted_dots)
}

fn numeric_initial(c: char) -> bool {
    c.is_ascii_digit()
        || matches!(
            c,
            '-' | '−'
                | '~'
                | '±'
                | '('
                | '½'
                | '¼'
                | '¾'
                | '⅓'
                | '⅔'
                | '⅕'
                | '⅖'
                | '⅗'
                | '⅘'
                | '⅙'
                | '⅚'
                | '⅛'
                | '⅜'
                | '⅝'
                | '⅞'
                | '⅐'
                | '⅑'
                | '⅒'
        )
}

struct UnitSuffixLengths {
    ascii: [Vec<usize>; 128],
    other: HashMap<char, Vec<usize>, BuildHasherDefault<TableHasher>>,
}
static UNIT_SUFFIX_LENGTHS: LazyLock<UnitSuffixLengths> = LazyLock::new(|| {
    let mut index = UnitSuffixLengths { ascii: std::array::from_fn(|_| Vec::new()), other: HashMap::default() };
    for (key, _) in MEASURES.lines().filter_map(|row| row.split_once('\t')) {
        let Some(last) = key.chars().last() else {
            continue;
        };
        let lengths =
            if last.is_ascii() { &mut index.ascii[last as usize] } else { index.other.entry(last).or_default() };
        if !lengths.contains(&key.len()) {
            lengths.push(key.len());
        }
    }
    index
});

fn possible_measure(s: &str, number_prefix_end: usize) -> bool {
    fn unit_suffix(s: &str, number_prefix_end: usize, has_denominator: bool) -> bool {
        let Some(last) = s.chars().last() else {
            return false;
        };
        let lengths = if last.is_ascii() {
            Some(&UNIT_SUFFIX_LENGTHS.ascii[last as usize])
        } else {
            UNIT_SUFFIX_LENGTHS.other.get(&last)
        };
        let Some(lengths) = lengths else {
            return false;
        };
        for &len in lengths {
            let Some(start) = s.len().checked_sub(len) else {
                continue;
            };
            if start == 0 || start > number_prefix_end || !s.is_char_boundary(start) {
                continue;
            }
            let unit = &s[start..];
            if !has_denominator
                && ["s", "t", "A", "J", "N", "cd"].contains(&unit)
                && !s[..start].ends_with([' ', '\u{a0}', '\u{202f}'])
            {
                continue;
            }
            if MEASURES_INDEX.contains_key(unit) {
                return true;
            }
        }
        false
    }
    if unit_suffix(s, number_prefix_end, false) {
        return true;
    }
    if let Some((numerator, denominator)) = s.rsplit_once('/') {
        return DENOM_INDEX.contains_key(denominator) && unit_suffix(numerator, number_prefix_end, true);
    }
    false
}

fn possible_named_date(s: &str) -> bool {
    let first = s.split(' ').next().unwrap_or("");
    month_name(first).is_some()
        || matches!(first, "dekada" | "Dekada" | "mga")
        || ["AD", "A.D.", "MK", "M.K.", "CE", "C.E."].iter().any(|p| s.starts_with(p))
}

fn possible_numeric_date(s: &str) -> bool {
    if ['/', '-', '.'].into_iter().any(|sep| s.matches(sep).count() >= 2) {
        return true;
    }
    if s.ends_with('s') || s.ends_with("s'") || s.ends_with("-an") {
        return true;
    }
    if ["BC", "B.C.", "BK", "B.K.", "BCE", "B.C.E.", "AD", "A.D.", "MK", "M.K.", "CE", "C.E."]
        .iter()
        .any(|era| s.ends_with(era))
    {
        return true;
    }
    let Some((day, rest)) = s.split_once(' ') else {
        return false;
    };
    if day.is_empty()
        || day.len() > 2
        || !day.bytes().all(|b| b.is_ascii_digit())
        || day.parse::<u32>().ok().is_none_or(|n| !(1..=31).contains(&n))
    {
        return false;
    }
    rest.split(' ').next().is_some_and(|month| month_name(month).is_some())
}

/// Only these lexical contexts can extend an alphabetic token across whitespace.
fn named_phrase_start(s: &str) -> bool {
    const CONTEXTS: &[&str] = &[
        "kabanata",
        "siglo",
        "bahagi",
        "tomo",
        "aklat",
        "aralin",
        "yugto",
        "baitang",
        "antas",
        "seksiyon",
        "artikulo",
        "batas",
        "bilang",
        "blg.",
        "blg",
        "digmaang",
        "papa",
        "hari",
        "reyna",
        "pangulo",
        "heneral",
        "gobernador",
        "juan",
        "benedicto",
        "francisco",
        "pio",
        "leo",
        "pablo",
        "gregorio",
        "elizabeth",
        "charles",
        "henry",
        "george",
        "edward",
        "william",
        "richard",
        "james",
        "victoria",
        "louis",
        "napoleon",
        "philip",
        "felipe",
        "carlos",
        "alfonso",
        "fernando",
        "alexander",
        "nicholas",
        "no.",
        "n°",
        "nº",
        "oras",
        "dekada",
        "mga",
        "ad",
        "a.d.",
        "mk",
        "m.k.",
        "ce",
        "c.e.",
    ];
    static BY_INITIAL: LazyLock<[Vec<&'static str>; 26]> = LazyLock::new(|| {
        let mut by_initial: [Vec<&'static str>; 26] = std::array::from_fn(|_| Vec::new());
        for &entry in CONTEXTS {
            by_initial[(entry.as_bytes()[0] - b'a') as usize].push(entry);
        }
        by_initial
    });
    s.as_bytes()
        .first()
        .filter(|c| c.is_ascii_alphabetic())
        .is_some_and(|c| BY_INITIAL[(c.to_ascii_lowercase() - b'a') as usize].iter().any(|v| s.eq_ignore_ascii_case(v)))
        || month_name(s).is_some()
        || CURRENCY_INDEX.contains_key(s)
        || WHITELIST_PREFIXES.contains(s)
}

/// Recognize the common plain-number/unit paths without spelling their values.
/// Compound numbers and rates remain with the full classifier.
fn simple_measure(s: &str, end: usize, out: &mut Vec<Candidate>) -> bool {
    let digits = s.bytes().take_while(|b| b.is_ascii_digit()).count();
    if digits == 0 || digits == s.len() {
        return false;
    }
    let number = &s[..digits];
    if number.len() > 15 || (number.len() > 1 && number.starts_with('0')) {
        return false;
    }
    let rest = &s[digits..];
    let spaced = rest.starts_with([' ', '\u{a0}', '\u{202f}']);
    let unit = if spaced { &rest[rest.chars().next().unwrap().len_utf8()..] } else { rest };
    if unit.is_empty()
        || unit.chars().any(|c| {
            is_nemo_space(c)
                || matches!(
                    c,
                    '\u{202f}'
                        | '/'
                        | '.'
                        | ','
                        | '-'
                        | '–'
                        | '⁄'
                        | '½'
                        | '¼'
                        | '¾'
                        | '⅓'
                        | '⅔'
                        | '⅕'
                        | '⅖'
                        | '⅗'
                        | '⅘'
                        | '⅙'
                        | '⅚'
                        | '⅛'
                        | '⅜'
                        | '⅝'
                        | '⅞'
                        | '⅐'
                        | '⅑'
                        | '⅒'
                )
        })
    {
        return false;
    }
    if cardinal_input(number)
        && MEASURES_INDEX.contains_key(unit)
        && (spaced || !["s", "t", "A", "J", "N", "cd"].contains(&unit))
    {
        out.push(Candidate { end, cost: cost(1.11), rank: 6, tag: String::new() });
    }
    if number.len() <= 3 && number != "0" && cardinal_input(number) {
        for &(prefix, _) in QUANTITY_ROWS.iter() {
            if let Some(base_unit) = unit.strip_prefix(prefix) {
                if MEASURES_INDEX.contains_key(base_unit) && !["s", "t", "A", "J", "N", "cd"].contains(&base_unit) {
                    out.push(Candidate { end, cost: cost(1.11), rank: 4, tag: String::new() });
                }
            }
        }
    }
    true
}
pub struct Taggers;
impl Classifier for Taggers {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if !possible_token_start(text, start) {
            return;
        }
        let first = text[start];
        let numeric = numeric_initial(first);
        let whitelist_possible = WHITELIST_STARTS.contains(first);
        let mut s = String::with_capacity((text.len() - start).min(64));
        let mut has_digit = false;
        let mut has_space = false;
        let mut electronic_marker = false;
        let mut decimal_separator = false;
        let mut decimal_valid = true;
        let mut currency_marker = false;
        let mut phone_shape = true;
        let mut number_valid = true;
        let mut number_prefix_end = 0;
        for end in start + 1..=text.len() {
            let c = text[end - 1];
            if !has_space
                && is_nemo_space(c)
                && first.is_ascii_alphabetic()
                && !s.starts_with("ika-")
                && !s.starts_with("Ika-")
                && !named_phrase_start(&s)
            {
                break;
            }
            s.push(c);
            has_digit |= c.is_ascii_digit();
            has_space |= is_nemo_space(c);
            electronic_marker |= matches!(c, '.' | '@');
            decimal_separator |= matches!(c, '.' | ',');
            decimal_valid &= !matches!(c, ':' | '/' | '–' | '⁄');
            phone_shape &= c.is_ascii_digit() || matches!(c, '+' | ' ' | '-' | '.' | '(' | ')');
            if numeric && number_valid {
                number_valid = number_char(c);
                if number_valid {
                    number_prefix_end = s.len();
                }
            }
            currency_marker |= c.is_ascii_uppercase()
                || matches!(
                    c,
                    '₱' | '$'
                        | '€'
                        | '£'
                        | '¥'
                        | '₹'
                        | '₨'
                        | '₩'
                        | '฿'
                        | '₽'
                        | '₺'
                        | '₪'
                        | '₴'
                        | '₫'
                        | '৳'
                        | '₦'
                        | '₵'
                        | '֏'
                        | '₿'
                        | '¢'
                );
            if !possible_token_end(text, end) {
                continue;
            }
            let span_start = out.len();
            let mut found = Vec::new();
            if whitelist_possible {
                for v in whitelist(&s) {
                    push(&mut found, 1.01, 0, v);
                }
            }
            // All other classified branches cost at least 1.08.  Keep all
            // whitelist alternatives at 1.01, but do not explore dominated
            // readings of this exact span and transition.
            if !found.is_empty() {
                for (cost, rank, tag) in found {
                    out.push(Candidate { end, cost, rank, tag });
                }
                continue;
            }
            if first.is_ascii_alphabetic()
                && matches!(c, 'I' | 'V' | 'X' | 'L' | 'C' | 'D' | 'M')
                && (has_space
                    || s.starts_with("ika-")
                    || s.starts_with("Ika-")
                    || s.starts_with("pang-")
                    || s.starts_with("Pang-"))
            {
                for v in roman(&s) {
                    push(&mut found, 1.08, 1, v);
                }
            }
            if has_digit {
                if (numeric
                    || s.starts_with("oras ")
                    || s.starts_with("Oras ")
                    || s.starts_with("ika-")
                    || s.starts_with("Ika-"))
                    && (s.contains(':') || s.contains('.'))
                    && (!numeric || !has_space || possible_time_continuation(&s))
                {
                    if time_input(&s) {
                        out.push(Candidate { end, cost: cost(1.09), rank: 2, tag: String::new() });
                    }
                }
                if (numeric && possible_numeric_date(&s)) || (first.is_ascii_alphabetic() && possible_named_date(&s)) {
                    for (rank, v) in date(&s) {
                        push(&mut found, if rank == 0 { 1.08 } else { 1.09 }, 3 + rank, v);
                    }
                }
                if currency_marker
                    && (numeric
                        || CURRENCY
                            .lines()
                            .filter_map(|line| line.split_once('\t'))
                            .any(|(symbol, _)| s.starts_with(symbol)))
                {
                    for v in money(&s) {
                        let rank = if v.contains("fractional_part:") && !v.contains("integer_part:") { 4 } else { 5 };
                        push(&mut found, 1.09, rank, v);
                    }
                    if s.contains(['-', '–']) {
                        for v in money_ranges(&s) {
                            push(&mut found, 1.08, 5, v);
                        }
                    }
                }
            }
            // Date and repeated-money ranges can cost 1.08; ordinary time
            // and money cost 1.09.  Every remaining branch is at least 1.10.
            let mut best =
                out[span_start..].iter().map(|c| c.cost).chain(found.iter().map(|v| v.0)).min().unwrap_or(i64::MAX);
            if numeric && best >= cost(1.1) {
                if MEASURE_ENDS.contains(c) && !simple_measure(&s, end, out) && possible_measure(&s, number_prefix_end)
                {
                    for (weight, v) in measure(&s) {
                        let rank = if v.starts_with("measure { decimal {") && v.contains(" quantity: ") {
                            4
                        } else if v.starts_with("measure { decimal {") {
                            5
                        } else {
                            6
                        };
                        push(&mut found, weight, rank, v);
                    }
                }
                if number_valid {
                    if cardinal_input(&s) {
                        out.push(Candidate { end, cost: cost(1.1), rank: 7, tag: String::new() });
                    }
                    if !has_space
                        && (s.contains(['-', '–', ':', ',']) || (s.len() > 1 && (s.starts_with('0') || s.len() > 15)))
                    {
                        compound_candidates(&s, end, out);
                    }
                    if s.contains([
                        '/', '⁄', '½', '¼', '¾', '⅓', '⅔', '⅕', '⅖', '⅗', '⅘', '⅙', '⅚', '⅛', '⅜', '⅝', '⅞', '⅐', '⅑',
                        '⅒',
                    ]) {
                        if let Some(v) = fraction(&s) {
                            push(&mut found, 1.1, 8, v);
                        }
                    }
                    if decimal_valid && (decimal_separator || QUANTITY_ENDS.contains(c)) && decimal_input(&s) {
                        out.push(Candidate { end, cost: cost(1.1), rank: 10, tag: String::new() });
                    }
                }
            }
            best = best.min(
                out[span_start..].iter().map(|c| c.cost).chain(found.iter().map(|v| v.0)).min().unwrap_or(i64::MAX),
            );
            if best >= cost(1.1)
                && has_digit
                && (numeric
                    || s.starts_with("ika")
                    || s.starts_with("Ika")
                    || s.starts_with("pang-")
                    || s.starts_with("Pang-"))
            {
                if ordinal_input(&s) {
                    out.push(Candidate { end, cost: cost(1.1), rank: 9, tag: String::new() });
                }
            }
            if best >= cost(1.1) && has_digit && phone_shape && (numeric || first == '+') {
                if let Some(v) = telephone(&s) {
                    push(&mut found, 1.1, 11, v);
                }
                if first == '+' {
                    if let Some(v) = international_phone(&s) {
                        push(&mut found, 1.1, 11, v);
                    }
                }
            }
            best = best.min(
                out[span_start..].iter().map(|c| c.cost).chain(found.iter().map(|v| v.0)).min().unwrap_or(i64::MAX),
            );
            if best >= cost(1.11)
                && !has_space
                && (electronic_marker || s.starts_with("http://") || s.starts_with("https://"))
            {
                if let Some(v) = electronic(&s) {
                    push(&mut found, 1.11, 12, v);
                }
            }
            best = best.min(found.iter().map(|v| v.0).min().unwrap_or(i64::MAX));
            if best >= cost(2.0) && !has_space && (has_digit || s.ends_with(['²', '³'])) {
                if let Some(dots) = serial_input(&s) {
                    out.push(Candidate { end, cost: cost(2.0 + 0.1 * dots as f64), rank: 13, tag: String::new() });
                }
            }
            best = best.min(out[span_start..].iter().map(|c| c.cost).min().unwrap_or(i64::MAX));
            for (cost, rank, tag) in found {
                out.push(Candidate { end, cost, rank, tag });
            }
            if best >= cost(100.0) && !has_space {
                out.push(Candidate { end, cost: cost(100.0), rank: 14, tag: String::new() });
            }
        }
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        if !candidate.tag.is_empty() {
            return;
        }
        if candidate.rank == 14 {
            let mut tag = String::with_capacity((candidate.end - start) * 2 + 8);
            tag.push_str("name: \"");
            for &c in &text[start..candidate.end] {
                tag.push(if c == ' ' { '\u{a0}' } else { c });
            }
            tag.push('"');
            candidate.tag = tag;
            return;
        }
        let s: String = text[start..candidate.end].iter().collect();
        candidate.tag = match candidate.rank {
            2 => time(&s).unwrap_or_default(),
            4 | 6 => simple_measure_tag(&s, candidate.rank).unwrap_or_default(),
            7 => card(&s)
                .or_else(|| {
                    card_compound(&s)
                        .into_iter()
                        .find_map(|(weight, tag)| (cost(weight) == candidate.cost).then_some(tag))
                })
                .unwrap_or_default(),
            9 => ordinal(&s).unwrap_or_default(),
            10 => decimal(&s).unwrap_or_default(),
            13 => serial(&s).unwrap_or_default(),
            _ => String::new(),
        };
    }
}
