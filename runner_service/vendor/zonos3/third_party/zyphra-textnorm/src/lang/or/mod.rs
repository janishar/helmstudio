//! Native Odia grammars corresponding to the production text-normalization package.
mod datetime;
mod electronic;
mod graph_utils;
mod lexical;
mod measure_money;
mod numbers;

use crate::lang::common::{self, Fields};
use crate::lattice::{Candidate, Classifier, Shape};
use crate::pipeline::Grammar;
use crate::tag::Dict;
use crate::text::{cost, is_nemo_space};
use std::collections::HashSet;
use std::sync::LazyLock;

struct Odia {
    members: Vec<Box<dyn Classifier>>,
    punctuation: Punct,
}
struct Union;
struct Punct;

// Words admitted after an internal space by the numeric, date, measure,
// currency, clock and contextual-Roman graphs. A completed word stays
// permissive: punctuation or a glued suffix may follow it.
static INTERNAL_WORDS: LazyLock<(HashSet<&'static str>, HashSet<&'static str>)> = LazyLock::new(|| {
    let mut words = HashSet::new();
    for data in
        [include_str!("../../../data/or/units_list.tsv"), include_str!("../../../data/or/currency_major_minor.tsv")]
    {
        for line in data.lines().filter(|line| !line.starts_with('#')) {
            if let Some((key, _)) = line.split_once('\t') {
                words.insert(key);
            }
        }
    }
    words.extend([
        "ଜାନୁଆରୀ",
        "ଫେବୃଆରୀ",
        "ମାର୍ଚ୍ଚ",
        "ଅପ୍ରେଲ",
        "ମଇ",
        "ଜୁନ",
        "ଜୁଲାଇ",
        "ଅଗଷ୍ଟ",
        "ସେପ୍ଟେମ୍ବର",
        "ଅକ୍ଟୋବର",
        "ନଭେମ୍ବର",
        "ଡିସେମ୍ବର",
        "କୋଟି",
        "ଲକ୍ଷ",
        "ହଜାର",
        "ମିଲିଅନ୍",
        "ମିଲିୟନ",
        "ବିଲିଅନ୍",
        "ବିଲିୟନ",
        "BC",
        "B.C.",
        "BCE",
        "B.C.E.",
        "ଖ୍ରୀ.ପୂ.",
        "ଖ୍ରୀଷ୍ଟପୂର୍ବ",
        "AD",
        "A.D.",
        "CE",
        "C.E.",
        "ଖ୍ରୀଷ୍ଟାବ୍ଦ",
        "ଦଶକ",
        "ର",
        "to",
        "c",
        "ct",
        "UTC",
        "GMT",
        "IST",
        "EST",
        "PST",
        "CST",
        "ଅଧ୍ୟାୟ",
        "ଭାଗ",
        "ଖଣ୍ଡ",
        "ପର୍ବ",
        "ଶ୍ରେଣୀ",
        "ଧାରା",
        "ଅନୁଚ୍ଛେଦ",
        "ପରିଚ୍ଛେଦ",
        "Chapter",
        "Part",
        "Volume",
        "Section",
        "Act",
        "Scene",
        "ଲୁଇ",
        "Louis",
        "ହେନରୀ",
        "Henry",
        "ଏଡୱାର୍ଡ",
        "Edward",
        "ଜର୍ଜ",
        "George",
        "ଚାର୍ଲ୍ସ",
        "Charles",
        "ଏଲିଜାବେଥ",
        "Elizabeth",
        "ପୋପ୍",
        "Pope",
        "ଶତାବ୍ଦୀ",
        "ଶତାବ୍ଦୀର",
        "Century",
        "I",
        "V",
        "X",
        "L",
    ]);
    let mut prefixes = HashSet::new();
    for &word in &words {
        for (i, _) in word.char_indices().skip(1) {
            prefixes.insert(&word[..i]);
        }
    }
    (words, prefixes)
});

const ASCII_PUNCT: [bool; 128] = {
    let mut marks = [false; 128];
    let chars = b"!#$%&'()*+,-./:;<=>?@^_`{|}~\"[]";
    let mut i = 0;
    while i < chars.len() {
        marks[chars[i] as usize] = true;
        i += 1;
    }
    marks
};

#[inline]
fn punctuation(c: char) -> bool {
    if c.is_ascii() {
        ASCII_PUNCT[c as usize]
    } else {
        matches!(c, '।' | '॥' | '–' | '—' | '…' | '«' | '»' | '“' | '”' | '‘' | '’' | '„')
    }
}

fn name_tag(s: &str) -> String {
    let mut tag = String::with_capacity(s.len() + 8 + s.bytes().filter(|&c| c == b' ').count());
    tag.push_str("name: \"");
    for c in s.chars() {
        tag.push(if c == ' ' { '\u{a0}' } else { c });
    }
    tag.push('"');
    tag
}
fn push(out: &mut Vec<Candidate>, end: usize, cost: i64, rank: i32, word: String) {
    if word.contains("ଶହେ") {
        let alternate = word.replace("ଶହେ", "ଏକ ଶହ");
        let alternate_rank = if word.starts_with("ଋଣ ") { rank - 1 } else { rank + 1 };
        out.push(Candidate { end, cost, rank: alternate_rank, tag: name_tag(&alternate) });
    }
    out.push(Candidate { end, cost, rank, tag: name_tag(&word) });
}
impl Classifier for Union {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if start > 0 && !is_nemo_space(text[start - 1]) && !punctuation(text[start - 1]) {
            return;
        }
        let mut s = String::new();
        let mut has_digit = false;
        let mut has_space = false;
        let mut has_vulgar = false;
        let mut has_colon = false;
        let mut has_dash = false;
        let mut has_slash = false;
        let mut has_decimal_separator = false;
        let mut has_electronic_mark = false;
        let mut has_ascii_alpha = false;
        let mut has_currency_symbol = false;
        let mut all_digits = true;
        let mut digit_count = 0usize;
        let mut value = 0u64;
        let mut leading_zero = false;
        let mut component_start = 0usize;
        let mut component_open = false;
        for end in start + 1..=text.len() {
            let c = text[end - 1];
            if c == '\n' || c == '\r' || c == '\t' || c == '\u{a0}' {
                break;
            }
            s.push(c);
            has_digit |= graph_utils::digit(c).is_some();
            has_space |= is_nemo_space(c);
            has_vulgar |= matches!(c, '½' | '¼' | '¾' | '⅓' | '⅔' | '⅕' | '⅛' | '⅝' | '⅞' | '⅒');
            has_colon |= c == ':';
            has_dash |= c == '-';
            has_slash |= c == '/';
            has_decimal_separator |= matches!(c, '.' | ',');
            has_electronic_mark |= matches!(c, '.' | '^' | '²' | '³' | '@');
            has_ascii_alpha |= c.is_ascii_alphabetic();
            has_currency_symbol |= matches!(
                c,
                '₹' | '$'
                    | '€'
                    | '£'
                    | '¥'
                    | '৳'
                    | '₽'
                    | '₩'
                    | '₺'
                    | '₪'
                    | '₴'
                    | '₫'
                    | '₦'
                    | '₱'
                    | '฿'
                    | '֏'
                    | '¢'
            );
            if c == ' ' {
                if !has_digit && !has_vulgar && !s[..s.len() - 1].contains(' ') {
                    let first = &s[..s.len() - 1];
                    let potential_roman = first.chars().all(|x| matches!(x, 'I' | 'V' | 'X' | 'L'));
                    let initial_symbol = first.chars().next().is_some_and(|x| !x.is_alphabetic());
                    if !potential_roman && !initial_symbol && !INTERNAL_WORDS.0.contains(first) {
                        break;
                    }
                }
                component_start = s.len();
                component_open = true;
            } else if component_open {
                let part = &s[component_start..];
                if part.chars().next().is_some_and(|first| first.is_alphabetic() && graph_utils::digit(first).is_none())
                {
                    let (words, prefixes) = &*INTERNAL_WORDS;
                    if words.contains(part) {
                        component_open = false;
                    } else if !prefixes.contains(part) {
                        break;
                    }
                } else {
                    component_open = false;
                }
            }
            if all_digits {
                if let Some(d) = graph_utils::digit(c) {
                    if digit_count == 0 {
                        leading_zero = d == 0;
                    }
                    digit_count += 1;
                    value = value.saturating_mul(10).saturating_add(d as u64);
                } else {
                    all_digits = false;
                }
            }
            if end < text.len() && !is_nemo_space(text[end]) && !punctuation(text[end]) {
                continue;
            }
            if all_digits {
                if leading_zero && digit_count > 1 {
                    out.push(Candidate { end, cost: cost(1.07), rank: 11000, tag: String::new() });
                } else if digit_count <= 13 {
                    if value % 1000 == 100 {
                        out.push(Candidate { end, cost: cost(1.12), rank: 11001, tag: String::new() });
                    }
                    out.push(Candidate { end, cost: cost(1.12), rank: 11000, tag: String::new() });
                }
                out.push(Candidate { end, cost: cost(100.0), rank: 12000, tag: String::new() });
                continue;
            }
            if !has_space {
                let signed = [('-', true), ('−', true), ('~', false), ('±', false)]
                    .into_iter()
                    .find_map(|(sign, negative)| s.strip_prefix(sign).map(|rest| (rest, negative)));
                if let Some((rest, negative)) = signed {
                    let mut n = 0u64;
                    let mut len = 0usize;
                    let mut valid = true;
                    let mut zero = false;
                    for ch in rest.chars() {
                        if let Some(d) = graph_utils::digit(ch) {
                            if len == 0 {
                                zero = d == 0;
                            }
                            len += 1;
                            n = n.saturating_mul(10).saturating_add(d as u64);
                        } else {
                            valid = false;
                            break;
                        }
                    }
                    if valid && len > 0 && len <= 13 && !(zero && len > 1) {
                        if n % 1000 == 100 {
                            out.push(Candidate {
                                end,
                                cost: cost(1.02),
                                rank: if negative { 10999 } else { 11001 },
                                tag: String::new(),
                            });
                        }
                        out.push(Candidate { end, cost: cost(1.02), rank: 11000, tag: String::new() });
                        out.push(Candidate { end, cost: cost(100.0), rank: 12000, tag: String::new() });
                        continue;
                    }
                }
            }
            if !has_space {
                if lexical::whitelist(&s).is_some() {
                    out.push(Candidate { end, cost: cost(1.01), rank: 0, tag: String::new() });
                }
            }
            if has_space || has_dash {
                if let Some(w) = lexical::roman(&s) {
                    push(out, end, cost(1.08), 1000, w);
                }
            }
            if has_digit {
                if has_colon && datetime::accepts_time(&s) {
                    out.push(Candidate { end, cost: cost(1.09), rank: 2000, tag: String::new() });
                }
                if !has_colon {
                    for d in datetime::date(&s) {
                        match d {
                            datetime::Date::Name(w, extra) => push(out, end, cost(1.09) + extra, 3000, w),
                            datetime::Date::Fields(y, m, d) => out.push(Candidate {
                                end,
                                cost: cost(1.09),
                                rank: 3000,
                                tag: format!("date {{ year: \"{y}\" month: \"{m}\" day: \"{d}\" }}"),
                            }),
                        }
                    }
                }
                if has_ascii_alpha || has_currency_symbol {
                    for w in measure_money::money(&s) {
                        push(out, end, cost(1.09), 4000, w);
                    }
                }
                if ["ମ", "ୟ", "ର୍ଥ", "ଷ୍ଠ", "th", "st", "nd", "rd"].iter().any(|suffix| s.ends_with(*suffix))
                {
                    for w in numbers::ordinal_forms(&s) {
                        push(out, end, cost(1.10), 7000, w);
                    }
                }
                if has_decimal_separator
                    || ["କୋଟି", "ଲକ୍ଷ", "ହଜାର", "ମିଲିଅନ୍", "ମିଲିୟନ", "ବିଲିଅନ୍", "ବିଲିୟନ", "k", "K", "M", "B"]
                        .iter()
                        .any(|suffix| s.ends_with(*suffix))
                {
                    if let Some(w) = numbers::decimal(&s) {
                        push(out, end, cost(1.10), 8000, w);
                    }
                }
                if s.starts_with('+') || s.starts_with('(') {
                    if let Some(w) = lexical::telephone(&s) {
                        push(out, end, cost(1.10), 9000, w);
                    }
                }
                if !has_colon {
                    for (w, extra) in numbers::cardinal_forms(&s) {
                        push(out, end, cost(1.12) + extra, 11000, w);
                    }
                }
            }
            if has_digit || has_vulgar {
                if measure_money::may_measure(&s) {
                    for w in measure_money::measure(&s) {
                        push(out, end, cost(1.10), 5000, w);
                    }
                }
                if has_vulgar || has_slash {
                    for w in numbers::fraction_forms(&s) {
                        push(out, end, cost(1.10), 6000, w);
                    }
                }
            }
            if !has_space
                && (has_electronic_mark
                    || s.starts_with("http://")
                    || s.starts_with("https://")
                    || (has_digit && has_ascii_alpha))
            {
                for w in electronic::electronic(&s) {
                    push(out, end, cost(1.11), 10000, w);
                }
            }
            if !has_space {
                out.push(Candidate { end, cost: cost(100.0), rank: 12000, tag: String::new() });
            }
        }
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        if candidate.rank == 12000 {
            candidate.tag.reserve(8 + text[start..candidate.end].iter().map(|c| c.len_utf8()).sum::<usize>());
            candidate.tag.push_str("name: \"");
            candidate.tag.extend(&text[start..candidate.end]);
            candidate.tag.push('"');
        } else if candidate.rank == 0 && candidate.tag.is_empty() {
            let span: String = text[start..candidate.end].iter().collect();
            if let Some(word) = lexical::whitelist(&span) {
                candidate.tag = name_tag(word);
            }
        } else if candidate.rank == 2000 && candidate.tag.is_empty() {
            candidate.tag.reserve(96);
            let span: String = text[start..candidate.end].iter().collect();
            datetime::write_time(&span, &mut candidate.tag);
        } else if candidate.tag.is_empty() {
            let chars = &text[start..candidate.end];
            candidate.tag.reserve(32 + chars.len() * 18);
            candidate.tag.push_str("name: \"");
            let mut from = 0;
            if let Some(&sign) = chars.first() {
                let prefix = match sign {
                    '-' | '−' => Some("ଋଣ\u{a0}"),
                    '~' => Some("ପ୍ରାୟ\u{a0}"),
                    '±' => Some("ପ୍ଲସ୍\u{a0}ମାଇନସ୍\u{a0}"),
                    _ => None,
                };
                if let Some(prefix) = prefix {
                    candidate.tag.push_str(prefix);
                    from = 1;
                }
            }
            if candidate.cost == cost(1.07) {
                for (i, &c) in chars[from..].iter().enumerate() {
                    if i != 0 {
                        candidate.tag.push('\u{a0}');
                    }
                    if let Some(d) = graph_utils::digit(c) {
                        candidate.tag.push_str(graph_utils::CARDINALS[d as usize]);
                    }
                }
            } else {
                let mut n = 0u64;
                for &c in &chars[from..] {
                    if let Some(d) = graph_utils::digit(c) {
                        n = n * 10 + d as u64;
                    }
                }
                graph_utils::spell_into(&mut candidate.tag, n, "\u{a0}", candidate.rank != 11000);
            }
            candidate.tag.push('"');
        }
    }
}
impl Classifier for Punct {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if let Some(&c) = text.get(start) {
            if punctuation(c) {
                out.push(Candidate { end: start + 1, cost: cost(1.1), rank: 0, tag: format!("name: \"{c}\"") });
            }
        }
    }
}
impl Grammar for Odia {
    fn code(&self) -> &'static str {
        "or"
    }
    fn shape(&self) -> Shape {
        Shape::Separated
    }
    fn classifiers(&self) -> &[Box<dyn Classifier>] {
        &self.members
    }
    fn punct(&self) -> Option<&dyn Classifier> {
        Some(&self.punctuation)
    }
    fn verbalize(&self, token: &Dict) -> Option<String> {
        if let Some(s) = common::verbalize_name(token) {
            return Some(s);
        }
        let mut outer = Fields::new(token);
        let fields = outer.dict("date")?;
        if !outer.done() {
            return None;
        }
        let mut f = Fields::new(fields);
        let d = f.take("day")?;
        let m = f.take("month")?;
        let y = f.take("year")?;
        if !f.done() || d.is_empty() || m.is_empty() || y.is_empty() {
            return None;
        }
        Some(format!("{d} {m} {y}").replace('\u{a0}', " "))
    }
}
static GRAMMAR: LazyLock<Odia> = LazyLock::new(|| Odia { members: vec![Box::new(Union)], punctuation: Punct });
pub fn grammar() -> &'static dyn Grammar {
    &*GRAMMAR
}
