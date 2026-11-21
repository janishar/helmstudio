//! Gujarati text normalization grammar.
mod address;
mod date;
mod electronic;
mod lazy;
mod measure;
mod money;
mod numbers;
mod numeric;
mod roman;
mod serial;
mod telephone;
mod time;
mod verbalize;

use crate::lang::common::Punctuation;
use crate::lattice::{Candidate, Classifier, Shape};
use crate::pipeline::Grammar;
use crate::tag::{Dict, Value};
use crate::text::{cost, is_nemo_space};
use std::sync::LazyLock;

const ABBR: &str = include_str!("../../../data/gu/whitelist/abbreviations.tsv");
fn rows(data: &'static str) -> impl Iterator<Item = (&'static str, &'static str)> {
    data.lines().filter_map(|l| l.trim_end_matches('\r').split_once('\t'))
}
fn whitelist_ends(input: &str) -> Vec<usize> {
    static KEYS: LazyLock<address::KeywordIndex> = LazyLock::new(|| {
        let mut keys = address::KeywordIndex::new();
        for (key, _) in rows(ABBR) {
            keys.insert(key);
        }
        keys
    });
    let Some(first) = input.chars().next() else { return Vec::new() };
    KEYS.get(first).iter().filter_map(|key| input.starts_with(*key).then_some(key.len())).collect()
}
fn whitelist_dynamic(input: &str, first: char) -> bool {
    first.is_ascii_uppercase()
        || match first {
            '©' | '®' | '™' | '§' | '№' => true,
            'n' => input.starts_with("no.") || input.starts_with("n°") || input.starts_with("nº"),
            'ક' => input.starts_with("ક્ર."),
            'ન' => input.starts_with("નં."),
            _ => false,
        }
}
fn named(out: &mut Vec<Candidate>, end: usize, text: &str, c: i64, rank: i32) {
    out.push(Candidate { end, cost: c, rank, tag: lazy::tag(|| format!("name: \"{}\"", text.replace(' ', "\u{a0}"))) });
}
fn whitelist(s: &str, end: usize, out: &mut Vec<Candidate>) {
    static INDEX: LazyLock<std::collections::HashMap<&'static str, Vec<&'static str>>> = LazyLock::new(|| {
        let mut index: std::collections::HashMap<&'static str, Vec<&'static str>> = std::collections::HashMap::new();
        for (key, value) in rows(ABBR) {
            index.entry(key).or_default().push(value);
        }
        index
    });
    if let Some(values) = INDEX.get(s) {
        for value in values {
            named(out, end, value, cost(1.01), 0);
        }
    }
    for (k, v) in [("©", "કૉપિરાઇટ"), ("®", "નોંધાયેલ"), ("™", "ટ્રેડમાર્ક")]
    {
        if s == k {
            named(out, end, v, cost(1.01), 0);
        }
    }
    for (k, v) in [
        ("©", "કૉપિરાઇટ"),
        ("§", "કલમ"),
        ("No.", "નંબર"),
        ("no.", "નંબર"),
        ("N°", "નંબર"),
        ("n°", "નંબર"),
        ("Nº", "નંબર"),
        ("nº", "નંબર"),
        ("№", "નંબર"),
        ("ક્ર.", "ક્રમાંક"),
        ("નં.", "નંબર"),
    ] {
        if let Some(n) = s.strip_prefix(k) {
            let n = n
                .strip_prefix(' ')
                .or_else(|| n.strip_prefix('\u{a0}'))
                .or_else(|| n.strip_prefix('\u{202f}'))
                .unwrap_or(n);
            for (x, c) in numeric::cardinal(n) {
                out.push(Candidate {
                    end,
                    cost: cost(1.01) + c,
                    rank: 0,
                    tag: lazy::tag(|| format!("name: \"{v}\u{a0}{}\"", x.replace(' ', "\u{a0}"))),
                });
            }
        }
    }
    let bytes = s.as_bytes();
    let mut pos = 0;
    let mut count = 0;
    while pos < bytes.len() && bytes[pos].is_ascii_uppercase() {
        count += 1;
        pos += 1;
        if pos == bytes.len() {
            break;
        }
        if bytes[pos] != b'.' {
            break;
        }
        pos += 1;
        if pos < bytes.len() && bytes[pos] == b' ' {
            pos += 1;
        }
    }
    if pos == bytes.len() && count >= 3 {
        out.push(Candidate {
            end,
            cost: cost(1.01),
            rank: 0,
            tag: lazy::tag(|| {
                format!(
                    "name: \"{}\"",
                    s.bytes().filter(|b| b.is_ascii_uppercase()).map(char::from).collect::<String>()
                )
            }),
        });
    }
}
thread_local! {
    static SPAN_INPUT: std::cell::Cell<String> = const { std::cell::Cell::new(String::new()) };
}

struct Union {
    marks: Punctuation,
}
impl Union {
    fn generate(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        // Build UTF-8 once for this start; all candidate spans borrow prefixes.
        let suffix = &text[start..];
        let mut input = SPAN_INPUT.take();
        input.extend(suffix);
        let address_bounds = address::keyword_bounds(&input);
        let city_end = measure::city_prefix_end(&input);
        let phone_possible = telephone::possible_start(&input);
        let mut byte_end = 0;
        let mut has_digit = false;
        let mut digit_count = 0usize;
        let mut has_colon = false;
        let mut date_separator = false;
        let mut has_dimension = false;
        let mut numeric_structural = true;
        let mut phone_plain = true;
        let mut phone_keyword = None;
        let first = text[start];
        let whitelist_ends = whitelist_ends(&input);
        let whitelist_dynamic = whitelist_dynamic(&input, first);
        let first_is_digit = numbers::digit(first).is_some();
        let mut time_possible = first_is_digit;
        let date_possible = date::possible_start(&input);
        let mut date_live = date_possible;
        let measure_number_start = first_is_digit || matches!(first, '-' | '−' | '~' | '±');
        let money_start = money::possible_start(first);
        let currency_prefix = money_start && money::has_currency_prefix(&input);
        let money_possible = money_start && (measure_number_start || currency_prefix);
        let mut measure_live = measure_number_start;
        let mut money_live = money_possible;
        let mut phone_live = phone_possible;
        let numeric_start = first_is_digit
            || matches!(
                first,
                '-' | '−'
                    | '~'
                    | '±'
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
                    | '⅐'
                    | '⅛'
                    | '⅜'
                    | '⅝'
                    | '⅞'
                    | '⅑'
                    | '⅒'
            );
        let mut numeric_live = numeric_start;
        let mut numeric_lexical_end = None;
        let any_digit_branch = numeric_start || date_possible || money_possible || phone_possible;
        let mut has_roman = false;
        let mut has_electronic_marker = false;
        let mut electronic_possible = true;
        let mut serial_possible = serial::possible_prefix(&input);
        let mut serial_marker = false;
        let mut previous_digit = false;
        let mut previous_alpha = false;
        let mut roman_possible = input.bytes().any(|c| matches!(c, b'I' | b'V' | b'X' | b'L' | b'C' | b'D' | b'M'));
        for end in start + 1..=text.len() {
            let c = text[end - 1];
            byte_end += c.len_utf8();
            let is_digit = numbers::digit(c).is_some();
            if any_digit_branch && !has_digit {
                has_digit = is_digit
                    || matches!(
                        c,
                        '½' | '¼'
                            | '¾'
                            | '⅓'
                            | '⅔'
                            | '⅕'
                            | '⅖'
                            | '⅗'
                            | '⅘'
                            | '⅙'
                            | '⅚'
                            | '⅐'
                            | '⅛'
                            | '⅜'
                            | '⅝'
                            | '⅞'
                            | '⅑'
                            | '⅒'
                    );
            }
            if phone_live && is_digit {
                digit_count += 1;
            }
            if date_live || time_possible {
                has_colon |= c == ':';
            }
            if date_live {
                date_live = date::possible_char(c);
            }
            if time_possible {
                time_possible = is_digit || c.is_ascii_alphabetic() || matches!(c, ':' | '.' | ' ' | '-' | '–');
            }
            if date_live && first_is_digit {
                date_separator |= matches!(c, '-' | '/' | '.' | '–' | ' ' | '\'' | 's');
            }
            if measure_live {
                measure_live = measure::number_char(c);
            }
            if money_live {
                money_live = money::possible_char(c);
            }
            if measure_live {
                has_dimension |= matches!(c, 'x' | 'X' | '*');
            }
            if numeric_live && numeric_structural && !numeric::structural_char(c) {
                numeric_structural = false;
                numeric_lexical_end = numeric::last_lexical_end(&input, byte_end - c.len_utf8());
            }
            if numeric_live && !numeric_structural && numeric_lexical_end.is_none_or(|last| byte_end > last) {
                numeric_live = false;
            }
            if phone_live && phone_plain {
                phone_plain = telephone::plain_char(c);
            }
            if electronic_possible {
                has_electronic_marker |= matches!(c, '.' | '@' | '/' | '\\') || ('₀'..='₉').contains(&c);
                electronic_possible = c.is_ascii_alphanumeric()
                    || is_digit
                    || ('₀'..='₉').contains(&c)
                    || ".-_/$\\()+–#?&=%:@ ".contains(c);
            }
            if serial_possible {
                serial_possible = !c.is_whitespace();
                if serial_possible && !serial_marker {
                    let alpha = serial::alpha(c);
                    serial_marker |= matches!(c, '-' | '/' | '.' | '^' | '²' | '³')
                        || (previous_alpha && is_digit)
                        || (previous_digit && alpha);
                    previous_alpha = alpha;
                    previous_digit = is_digit;
                }
            }
            if roman_possible {
                has_roman |= matches!(c, 'I' | 'V' | 'X' | 'L' | 'C' | 'D' | 'M');
                roman_possible = roman::possible_char(c);
            }
            // Every remaining union branch is impossible once its input
            // alphabet/initial or its last exact whitelist match is exhausted.
            // The independent word graph below still runs for this start.
            if !numeric_live
                && !date_live
                && !time_possible
                && !money_live
                && !measure_live
                && !phone_live
                && city_end.is_none_or(|last| byte_end > last)
                && address_bounds.is_none_or(|(_, last)| byte_end > last)
                && !serial_possible
                && !electronic_possible
                && !roman_possible
                && !whitelist_dynamic
                && whitelist_ends.iter().all(|&last| last < byte_end)
            {
                break;
            }
            if (end < text.len() && !is_nemo_space(text[end]) && !self.marks.is_mark(text[end]))
                || !crate::lang::common::possible_token_end(text, end)
            {
                continue;
            }
            let s = &input[..byte_end];
            if whitelist_dynamic || whitelist_ends.contains(&byte_end) {
                whitelist(s, end, out);
            }
            if has_digit {
                if numeric_live && (numeric_structural || numeric::lexical_ending(s)) {
                    numeric::classify(s, end, out);
                }
                if date_live && !has_colon && (!first_is_digit || date_separator) && date::might_match(s) {
                    date::classify(s, end, out);
                }
                if has_colon && time_possible {
                    time::classify(s, end, out);
                }
                if money_live && (currency_prefix || (money::possible_end(c) && money::matching_suffix(s, c))) {
                    money::classify(s, end, out);
                }
                if phone_live && digit_count >= 4 {
                    let keyword = if phone_plain {
                        true
                    } else {
                        let found = *phone_keyword.get_or_insert_with(|| telephone::first_keyword_end(&input));
                        if found.is_none() {
                            phone_live = false;
                        }
                        found.is_some_and(|offset| offset <= byte_end)
                    };
                    if keyword {
                        telephone::classify_hinted(s, end, out, phone_plain);
                    }
                }
            }
            let address_keyword = address_bounds.is_some_and(|(first, last)| first <= byte_end && byte_end <= last);
            let city_possible = city_end.is_some_and(|last| byte_end <= last);
            if measure_live || city_possible || address_keyword {
                measure::classify_hinted(s, end, out, city_possible, address_keyword, has_dimension);
            }
            if electronic_possible && has_electronic_marker {
                electronic::classify(s, end, out);
            }
            if serial_possible && serial_marker {
                serial::classify(s, end, out);
            }
            if roman_possible && has_roman {
                roman::classify(s, end, out);
            }
        }
        input.clear();
        SPAN_INPUT.set(input);
        // The word graph accepts punctuation internally only when another word follows.
        let mut has_word = false;
        let mut gu_chars = 0i64;
        for end in start + 1..=text.len() {
            let c = text[end - 1];
            if is_nemo_space(c)
                || matches!(
                    c,
                    '$' | '€'
                        | '₩'
                        | '£'
                        | '¥'
                        | '₹'
                        | '₨'
                        | '₦'
                        | '৳'
                        | '֏'
                        | '₽'
                        | '₫'
                        | '₺'
                        | '₪'
                        | '₴'
                        | '₱'
                        | '฿'
                        | '¢'
                        | '#'
                        | '%'
                        | '‰'
                        | '°'
                        | '℃'
                        | '℉'
                        | 'Ω'
                        | 'Ω'
                        | '²'
                        | '³'
                        | '±'
                        | '©'
                        | '§'
                        | '®'
                        | '™'
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
            {
                break;
            }
            let mark = self.marks.is_mark(c);
            if mark {
                if !has_word {
                    break;
                }
            } else {
                has_word = true;
                if ('\u{a81}'..='\u{aff}').contains(&c) && serial::gu(c) {
                    gu_chars += 1;
                }
            }
            if !mark
                && (end == text.len() || is_nemo_space(text[end]) || self.marks.is_mark(text[end]))
                && crate::lang::common::possible_token_end(text, end)
            {
                out.push(Candidate {
                    end,
                    cost: cost(100.0) + cost(-0.0001) * gu_chars,
                    rank: 13,
                    tag: lazy::tag(|| format!("name: \"{}\"", text[start..end].iter().collect::<String>())),
                });
            }
        }
    }
}
impl Classifier for Union {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if (start > 0 && !is_nemo_space(text[start - 1]) && !self.marks.is_mark(text[start - 1]))
            || !crate::lang::common::possible_token_start(text, start)
        {
            return;
        }
        lazy::with_build(false, || self.generate(text, start, out));
    }
    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        if !candidate.tag.is_empty() {
            return;
        }
        if candidate.rank == 13 {
            candidate.tag.push_str("name: \"");
            candidate.tag.extend(&text[start..candidate.end]);
            candidate.tag.push('"');
            return;
        }
        let input: String = text[start..candidate.end].iter().collect();
        let mut matches = Vec::new();
        lazy::with_build(true, || match candidate.rank {
            0 => {
                whitelist(&input, candidate.end, &mut matches);
                numeric::classify(&input, candidate.end, &mut matches);
            }
            1 | 2 | 9 => numeric::classify(&input, candidate.end, &mut matches),
            4 => date::classify(&input, candidate.end, &mut matches),
            5 => time::classify(&input, candidate.end, &mut matches),
            6 => measure::classify(&input, candidate.end, &mut matches),
            7 => money::classify(&input, candidate.end, &mut matches),
            8 => telephone::classify(&input, candidate.end, &mut matches),
            10 => electronic::classify(&input, candidate.end, &mut matches),
            11 => serial::classify(&input, candidate.end, &mut matches),
            12 => roman::classify(&input, candidate.end, &mut matches),
            _ => {}
        });
        if let Some(found) =
            matches.into_iter().find(|c| c.end == candidate.end && c.cost == candidate.cost && c.rank == candidate.rank)
        {
            candidate.tag = found.tag;
        }
    }
}
struct Gu {
    classifiers: Vec<Box<dyn Classifier>>,
    punct: Punctuation,
}
impl Grammar for Gu {
    fn code(&self) -> &'static str {
        "gu"
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
                if name == "name" {
                    if value.is_empty() || value.contains(' ') {
                        return None;
                    }
                    out.push_str(value);
                    continue;
                }
            }
            if let [(class, Value::Dict(fields))] = body.as_slice() {
                if class == "cardinal" {
                    if let [(key, Value::Str(value))] = fields.as_slice() {
                        if key == "integer" && !value.contains('"') {
                            out.push_str(value);
                            continue;
                        }
                    }
                }
            }
            let word = crate::permute::first_ordering(body, &mut |ordered: &Dict| self.verbalize(ordered))?;
            out.push_str(&word);
        }
        Some(out)
    }
}
static GRAMMAR: LazyLock<Gu> = LazyLock::new(|| {
    let make =
        || Punctuation::new("!#%&'()*+,-./:;<=>?@^_`{|}~\"", true, std::iter::empty::<&'static str>(), cost(2.1));
    Gu { classifiers: vec![Box::new(Union { marks: make() })], punct: make() }
});
pub fn grammar() -> &'static dyn Grammar {
    &*GRAMMAR
}
