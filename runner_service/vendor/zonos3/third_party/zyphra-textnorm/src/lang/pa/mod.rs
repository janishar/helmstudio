//! Punjabi text normalization grammar.
mod commerce;
mod context;
mod date_time;
mod graph_utils;
mod numeric;
mod verbalize;

use crate::lang::common::{Punctuation, possible_token_end, possible_token_start};
use crate::lattice::{Candidate, Classifier, Shape};
use crate::pipeline::Grammar;
use crate::tag::{Dict, Value};
use crate::text::{Cost, cost, is_nemo_space, is_unicode_punct};
use std::cell::RefCell;
use std::sync::LazyLock;

struct Pa {
    classifiers: Vec<Box<dyn Classifier>>,
    punct: Punctuation,
}
impl Grammar for Pa {
    fn code(&self) -> &'static str {
        "pa"
    }
    fn shape(&self) -> Shape {
        Shape::En
    }
    fn classifiers(&self) -> &[Box<dyn Classifier>] {
        &self.classifiers
    }
    fn punct(&self) -> Option<&dyn Classifier> {
        Some(&self.punct)
    }
    fn verbalize(&self, token: &Dict) -> Option<String> {
        verbalize::verbalize(token)
    }
    fn verbalize_split(&self, split: &[Dict]) -> Option<String> {
        let mut out = String::with_capacity(16 * split.len());
        for (i, token) in split.iter().enumerate() {
            let [(key, Value::Dict(body))] = token.as_slice() else { return None };
            if key != "tokens" {
                return None;
            }
            if i != 0 {
                out.push(' ');
            }
            if let [(name, Value::Str(value))] = body.as_slice() {
                if name == "name" && !value.contains(' ') {
                    if value.contains('\u{a0}') {
                        for c in value.chars() {
                            out.push(if c == '\u{a0}' { ' ' } else { c });
                        }
                    } else {
                        out.push_str(value);
                    }
                    continue;
                }
            }
            out.push_str(&crate::permute::first_ordering(body, &mut |ordering: &Dict| self.verbalize(ordering))?);
        }
        Some(out)
    }
}

fn unique_chars(chars: impl Iterator<Item = char>) -> Vec<char> {
    let mut result = Vec::new();
    for c in chars {
        if !result.contains(&c) {
            result.push(c);
        }
    }
    result
}

static WHITELIST_STARTS: LazyLock<Vec<char>> = LazyLock::new(|| {
    unique_chars(
        include_str!("../../../data/pa/whitelist/abbreviations.tsv").lines().filter_map(|row| row.chars().next()),
    )
});
static CURRENCY_STARTS: LazyLock<Vec<char>> = LazyLock::new(|| {
    unique_chars(include_str!("../../../data/pa/money/currency.tsv").lines().filter_map(|row| row.chars().next()))
});
static UNIT_ENDS: LazyLock<Vec<char>> = LazyLock::new(|| {
    unique_chars(
        include_str!("../../../data/pa/measure/unit.tsv")
            .lines()
            .filter_map(|row| row.split_once('\t').and_then(|(key, _)| key.chars().last())),
    )
});
static QUANTITY_ENDS: LazyLock<Vec<char>> = LazyLock::new(|| {
    unique_chars(
        include_str!("../../../data/pa/numbers/quantities.tsv")
            .lines()
            .filter_map(|row| row.split_once('\t').and_then(|(key, _)| key.chars().last())),
    )
});
static ORDINAL_CHARS: LazyLock<Vec<char>> = LazyLock::new(|| {
    let mut chars = Vec::new();
    for table in
        [include_str!("../../../data/pa/ordinal/suffixes.tsv"), include_str!("../../../data/pa/ordinal/exceptions.tsv")]
    {
        for row in table.lines() {
            if let Some((key, _)) = row.split_once('\t') {
                for c in key.chars() {
                    if graph_utils::digit(c).is_none() && !chars.contains(&c) {
                        chars.push(c);
                    }
                }
            }
        }
    }
    for c in "ਵਾstndrh".chars() {
        if !chars.contains(&c) {
            chars.push(c);
        }
    }
    chars
});
// Punctuation in the native script is checked at nearly every candidate end.
// Cache the shared Unicode predicate for this compact block.
static GURMUKHI_PUNCT: LazyLock<[bool; 128]> = LazyLock::new(|| {
    let mut values = [false; 128];
    for (offset, value) in values.iter_mut().enumerate() {
        *value = is_unicode_punct(char::from_u32(0x0a00 + offset as u32).unwrap());
    }
    values
});

#[inline]
fn boundary(c: char) -> bool {
    if is_nemo_space(c) {
        return true;
    }
    if c.is_ascii() {
        return c.is_ascii_punctuation() && c != '$';
    }
    if ('\u{a00}'..='\u{a7f}').contains(&c) {
        return GURMUKHI_PUNCT[c as usize - 0x0a00];
    }
    is_unicode_punct(c)
}

#[inline]
fn start_boundary(text: &[char], start: usize) -> bool {
    (start == 0 || boundary(text[start - 1])) && possible_token_start(text, start)
}

#[inline]
fn end_boundary(text: &[char], end: usize) -> bool {
    (end == text.len() || boundary(text[end])) && possible_token_end(text, end)
}

fn start_mask(c: char) -> u16 {
    let digit = graph_utils::digit(c).is_some();
    let signed = digit || matches!(c, '-' | '−' | '~' | '±');
    let mut mask = 1 << 13; // Word.
    if WHITELIST_STARTS.contains(&c) || c.is_ascii_uppercase() || "§©#№N".contains(c) {
        mask |= 1 << 0;
    }
    if signed {
        mask |= (1 << 1) | (1 << 2) | (1 << 6);
    }
    if signed || "½¼¾⅐⅑⅒⅓⅔⅕⅖⅗⅘⅙⅚⅛⅜⅝⅞".contains(c) {
        mask |= 1 << 3;
    }
    if digit || c == '\'' || "ਜਫਮਅਸਨਦ".contains(c) {
        mask |= 1 << 4;
    }
    if digit {
        mask |= (1 << 5) | (1 << 9);
    }
    if signed || CURRENCY_STARTS.contains(&c) {
        mask |= 1 << 7;
    }
    if matches!(c, '+' | '(' | '0' | '੦') {
        mask |= 1 << 8;
    }
    if c.is_ascii_alphanumeric() || digit || ('₀'..='₉').contains(&c) || "/\\.-_$()+–".contains(c) {
        mask |= 1 << 10;
    }
    if digit || c.is_ascii_alphabetic() || ('\u{a00}'..='\u{a7f}').contains(&c) {
        mask |= 1 << 11;
    }
    if "IVXLCDMਅਭਕਜਸ਼ਸਦਧਪਹਲਐਚ".contains(c) {
        mask |= 1 << 12;
    }
    mask
}

fn has_vulgar_fraction(c: char) -> bool {
    "½¼¾⅐⅑⅒⅓⅔⅕⅖⅗⅘⅙⅚⅛⅜⅝⅞".contains(c)
}

fn plausible_end(
    branch: i32,
    input: &str,
    last: char,
    has_number: bool,
    has_currency: bool,
    has_feature: bool,
) -> bool {
    match branch {
        1 | 4 | 6 | 7 | 8 | 9 if !has_number => false,
        4 => has_feature,
        6 => UNIT_ENDS.contains(&last) || has_feature,
        7 => has_currency,
        2 => has_number && (has_feature || QUANTITY_ENDS.contains(&last) && commerce::quantity_suffix(input)),
        3 | 5 => has_number && has_feature,
        9 => graph_utils::digit(last).is_none(),
        _ => true,
    }
}
struct Branch {
    weight: Cost,
    parse: fn(&str) -> Option<(String, Cost)>,
}
#[derive(Default)]
struct BoundaryCache {
    generation: usize,
    entries: Vec<(usize, bool)>,
}

impl BoundaryCache {
    fn begin(&mut self, len: usize) {
        self.generation = self.generation.wrapping_add(1);
        if self.generation == 0 {
            self.entries.clear();
            self.generation = 1;
        }
        self.entries.resize(len + 1, (0, false));
    }

    fn is_end(&mut self, text: &[char], end: usize) -> bool {
        let entry = &mut self.entries[end];
        if entry.0 != self.generation {
            *entry = (self.generation, end_boundary(text, end));
        }
        entry.1
    }
}

#[derive(Default)]
struct Scratch {
    input: String,
    boundaries: BoundaryCache,
}

thread_local! {
    static SCRATCH: RefCell<Scratch> = RefCell::new(Scratch::default());
}
impl Branch {
    fn candidates<const RANK: i32>(
        &self,
        text: &[char],
        start: usize,
        out: &mut Vec<Candidate>,
        input: &mut String,
        boundaries: &mut BoundaryCache,
        first_candidate: usize,
    ) {
        input.clear();
        let mut has_number = false;
        let mut has_currency = false;
        let mut has_feature = false;
        let mut cardinal_suffix = None;
        let mut decimal_simple = true;
        let mut decimal_point = false;
        let mut date_needs_prefix = false;
        let mut measure_unit = None;
        let mut money_simple = true;
        let mut money_numeric_end = None;
        for end in start..text.len() {
            let c = text[end];
            if RANK == 11 && is_nemo_space(c) {
                break;
            }
            if RANK == 11
                && graph_utils::digit(c).is_none()
                && !c.is_ascii_alphabetic()
                && !('\u{a00}'..='\u{a7f}').contains(&c)
                && !matches!(c, '-' | '/' | '^' | '.' | '%' | '&' | '@')
            {
                break;
            }
            if RANK == 3
                && graph_utils::digit(c).is_none()
                && !has_vulgar_fraction(c)
                && !matches!(c, '-' | '−' | ',' | '/')
                && !c.is_whitespace()
            {
                break;
            }
            if RANK == 8 && graph_utils::digit(c).is_none() && !matches!(c, '+' | '(' | ')' | ' ' | '-' | '.') {
                break;
            }
            if RANK == 9 && graph_utils::digit(c).is_none() && !ORDINAL_CHARS.contains(&c) {
                break;
            }
            if RANK == 5
                && ('\u{a00}'..='\u{a7f}').contains(&c)
                && graph_utils::digit(c).is_none()
                && !"ਏਐਮਪੀ".contains(c)
            {
                break;
            }
            if RANK == 10 && ('\u{a00}'..='\u{a7f}').contains(&c) && graph_utils::digit(c).is_none() {
                break;
            }
            input.push(c);
            if RANK == 0 && !context::whitelist_prefix(&input) {
                break;
            }
            if RANK == 1 {
                let simple = graph_utils::digit(c).is_some()
                    || matches!(c, ',' | ' ' | '-' | '−' | '–' | '~' | '±' | '+' | '=' | '×' | '÷');
                if !simple && cardinal_suffix.is_none() {
                    cardinal_suffix = Some(input.len() - c.len_utf8());
                }
                if let Some(offset) = cardinal_suffix {
                    if !["ਵਾਂ", "ਵੇਂ", "ਵੀਂ"].iter().any(|suffix| suffix.starts_with(&input[offset..]))
                    {
                        break;
                    }
                }
            }
            if RANK == 2 {
                if graph_utils::digit(c).is_none() && !(end == start && matches!(c, '-' | '−' | '~' | '±')) {
                    if c == '.' && !decimal_point {
                        decimal_point = true;
                    } else {
                        decimal_simple = false;
                    }
                }
                if !decimal_simple && !numeric::decimal_prefix(&input) {
                    break;
                }
            }
            if RANK == 4 {
                date_needs_prefix |= c == ':' || c == ' ';
                if date_needs_prefix && !date_time::date_prefix(&input) {
                    break;
                }
            }
            if RANK == 6 {
                let simple = graph_utils::digit(c).is_some()
                    || c.is_whitespace()
                    || matches!(c, ',' | '.' | '-' | '–' | '—' | 'x' | 'X' | '*')
                    || end == start && matches!(c, '−' | '~' | '±');
                if !simple && measure_unit.is_none() {
                    measure_unit = Some(input.len() - c.len_utf8());
                }
                if let Some(offset) = measure_unit {
                    if !commerce::unit_prefix(&input[offset..]) {
                        break;
                    }
                }
            }
            if RANK == 7 {
                let simple = graph_utils::digit(c).is_some()
                    || matches!(c, ',' | '.')
                    || end == start && matches!(c, '-' | '−' | '~' | '±');
                if money_simple && !simple && graph_utils::digit(text[start]).is_some() {
                    money_numeric_end = Some(input.len() - c.len_utf8());
                }
                money_simple &= simple;
                if !money_simple && !commerce::money_prefix(&input, money_numeric_end) {
                    break;
                }
            }
            if RANK == 12 && !text[start].is_ascii() && !context::roman_key_prefix(&input) {
                break;
            }
            has_number |= graph_utils::digit(c).is_some() || RANK == 3 && has_vulgar_fraction(c);
            has_currency |= RANK == 7 && CURRENCY_STARTS.contains(&c);
            has_feature |= match RANK {
                2 => c == '.',
                3 => c == '/' || has_vulgar_fraction(c),
                4 => graph_utils::digit(c).is_none(),
                5 => c == ':',
                6 => matches!(c, 'x' | 'X' | '*'),
                _ => false,
            };
            if !boundaries.is_end(text, end + 1)
                || !plausible_end(RANK, input, c, has_number, has_currency, has_feature)
            {
                continue;
            }
            // Every electronic reading has internal cost >= 1.0. Earlier
            // classified tokens on this exact span with a lower total cost
            // therefore strictly dominate it; ties must still be evaluated.
            if RANK == 10
                && out[first_candidate..]
                    .iter()
                    .any(|candidate| candidate.end == end + 1 && candidate.cost < self.weight + cost(1.0))
            {
                continue;
            }
            let weight = match RANK {
                0 => context::whitelist_cost(&input),
                1 => numeric::cardinal_cost(&input),
                2 => numeric::decimal_cost(&input),
                3 => numeric::fraction_cost(&input),
                4 => date_time::date_cost(&input),
                5 => date_time::time_cost(&input),
                6 => commerce::measure_cost(&input),
                7 => commerce::money_cost(&input),
                8 => commerce::telephone_cost(&input),
                9 => numeric::ordinal_cost(&input),
                10 => context::electronic_cost(&input),
                11 => context::serial_cost(&input),
                12 => context::roman_cost(&input),
                _ => unreachable!("invalid Punjabi classifier branch"),
            };
            if let Some(w) = weight {
                out.push(Candidate { end: end + 1, cost: self.weight + w, rank: RANK * 1001, tag: String::new() });
            }
        }
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        if candidate.tag.is_empty() {
            SCRATCH.with(|scratch| {
                let mut scratch = scratch.borrow_mut();
                scratch.input.clear();
                scratch.input.extend(text[start..candidate.end].iter().copied());
                if let Some((tag, _)) = (self.parse)(&scratch.input) {
                    candidate.tag = tag;
                }
            });
        }
    }
}
struct Word;
impl Word {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>, first_candidate: usize) {
        let mut pa = true;
        for end in start..text.len() {
            let c = text[end];
            if is_nemo_space(c) || "$€₩£¥₹₨₦৳֏₽₫₺₪₴₱฿¢#%‰°℃℉ΩΩ²³±©§®™½¼¾⅓⅔⅕⅖⅗⅘⅙⅚⅛⅜⅝⅞⅐⅑⅒".contains(c) || c == '"'
            {
                break;
            }
            pa &= ('\u{a00}'..='\u{a7f}').contains(&c);
            if !end_boundary(text, end + 1) {
                continue;
            }
            let weight = cost(100.0) + if pa { cost(-0.0001) } else { 0 };
            if out[first_candidate..].iter().any(|candidate| candidate.end == end + 1 && candidate.cost < weight) {
                continue;
            }
            out.push(Candidate { end: end + 1, cost: weight, rank: 0, tag: String::new() });
        }
    }
    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        let mut tag = String::with_capacity(8 + (candidate.end - start) * 4);
        tag.push_str("name: \"");
        tag.extend(text[start..candidate.end].iter().copied());
        tag.push('"');
        candidate.tag = tag;
    }
}

struct Union {
    members: Vec<Branch>,
}

impl Classifier for Union {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if !start_boundary(text, start) {
            return;
        }
        let mut mask = start_mask(text[start]);
        if graph_utils::digit(text[start]).is_some() {
            // A digit-initial electronic token can only be an email,
            // domain/IP, or relative Unix path. These require @, . or /;
            // their character classes exclude whitespace. URLs and Windows
            // paths do not start with a digit, and formulas cannot start with
            // a digit before their first element.
            // A clock's first colon must also precede whitespace: the hour
            // itself is a table key, including in a trimmed range endpoint.
            let mut electronic = false;
            let mut clock = false;
            let mut chunk = true;
            let mut fraction_scan = true;
            let mut fraction = false;
            let mut first_nondigit = None;
            for &c in &text[start..] {
                let digit = graph_utils::digit(c).is_some();
                if !digit && first_nondigit.is_none() {
                    first_nondigit = Some(c);
                }
                if chunk {
                    if c.is_whitespace() {
                        chunk = false;
                    } else {
                        electronic |= matches!(c, '@' | '.' | '/');
                        clock |= c == ':';
                    }
                }
                if fraction_scan {
                    if c == '/' || has_vulgar_fraction(c) {
                        fraction = true;
                        fraction_scan = false;
                    } else if !digit && !matches!(c, '-' | '−' | ',') && !c.is_whitespace() {
                        fraction_scan = false;
                    }
                }
                if !chunk && !fraction_scan {
                    break;
                }
            }
            if !electronic {
                mask &= !(1 << 10);
            }
            if !clock {
                mask &= !(1 << 5);
            }
            // The fraction scanner stops on exactly the same invalid
            // characters, and its end gate requires a slash or vulgar digit.
            if !fraction {
                mask &= !(1 << 3);
            }
            // Ordinals must reach a permitted non-digit suffix; serials
            // cannot accept a digits-only prefix.
            if !first_nondigit.is_some_and(|c| ORDINAL_CHARS.contains(&c)) {
                mask &= !(1 << 9);
            }
            if !first_nondigit.is_some_and(|c| {
                c.is_ascii_alphabetic()
                    || ('\u{a00}'..='\u{a7f}').contains(&c)
                    || matches!(c, '-' | '/' | '^' | '.' | '%' | '&' | '@')
            }) {
                mask &= !(1 << 11);
            }
            // An integer-only decimal needs an immediately attached quantity;
            // a space before a quantity is accepted only after a point.
            if first_nondigit.is_none() || first_nondigit == Some(' ') {
                mask &= !(1 << 2);
            }
        }
        let first_candidate = out.len();
        SCRATCH.with(|scratch| {
            let mut scratch = scratch.borrow_mut();
            let Scratch { input, boundaries } = &mut *scratch;
            boundaries.begin(text.len());
            for (index, member) in self.members.iter().enumerate() {
                if mask & (1 << index) == 0 {
                    continue;
                }
                macro_rules! scan {
                    ($rank:literal) => {
                        member.candidates::<$rank>(text, start, out, input, boundaries, first_candidate)
                    };
                }
                match index {
                    0 => scan!(0),
                    1 => scan!(1),
                    2 => scan!(2),
                    3 => scan!(3),
                    4 => scan!(4),
                    5 => scan!(5),
                    6 => scan!(6),
                    7 => scan!(7),
                    8 => scan!(8),
                    9 => scan!(9),
                    10 => scan!(10),
                    11 => scan!(11),
                    12 => scan!(12),
                    _ => unreachable!("invalid Punjabi classifier branch"),
                }
            }
        });
        if mask & (1 << self.members.len()) != 0 {
            let from = out.len();
            Word.candidates(text, start, out, first_candidate);
            for candidate in &mut out[from..] {
                candidate.rank += self.members.len() as i32 * 1000;
            }
        }
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        let index = (candidate.rank / 1000) as usize;
        let base = index as i32 * 1000;
        candidate.rank -= base;
        if index == self.members.len() {
            Word.materialize(text, start, candidate);
        } else {
            self.members[index].materialize(text, start, candidate);
        }
        candidate.rank += base;
    }
}
static GRAMMAR: LazyLock<Pa> = LazyLock::new(|| {
    let specs: &[(fn(&str) -> Option<(String, Cost)>, f64)] = &[
        (context::whitelist_tag, 1.01),
        (numeric::cardinal_tag, 1.1),
        (numeric::decimal_tag, 1.1),
        (numeric::fraction_tag, 1.05),
        (date_time::date_tag, 1.1),
        (date_time::time_tag, 1.1),
        (commerce::measure_tag, 1.1),
        (commerce::money_tag, 1.1),
        (commerce::telephone_tag, 1.1),
        (numeric::ordinal_tag, 1.1),
        (context::electronic_tag, 1.1),
        (context::serial_tag, 1.11),
        (context::roman_tag, 1.1),
    ];
    let classifiers: Vec<Branch> = specs.iter().map(|(parse, w)| Branch { weight: cost(*w), parse: *parse }).collect();
    Pa {
        classifiers: vec![Box::new(Union { members: classifiers })],
        punct: Punctuation::new("!#%&'()*+,-./:;<=>?@^_`{|}~\"।॥", true, std::iter::empty(), cost(2.1)),
    }
});
pub fn grammar() -> &'static dyn Grammar {
    &*GRAMMAR
}
