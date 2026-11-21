//! Turkish normalization grammar, ported from the production NeMo `tr` package.
// Lexical tables under data/tr are extracted from the production Turkish
// taggers, Copyright (c) 2026 Zyphra, Inc., Apache-2.0.
mod cardinal;
mod date;
mod decimal;
mod electronic;
mod fraction;
mod graph_utils;
mod measure;
mod money;
mod ordinal;
mod roman;
mod telephone;
mod time;
mod whitelist;

use crate::lang::common::{self, Fields};
use crate::lattice::{Candidate, Classifier, Shape};
use crate::pipeline::Grammar;
use crate::tag::{Dict, Value};
use crate::text::{cost, is_nemo_space};
use std::sync::LazyLock;

struct Turkish {
    classifiers: Vec<Box<dyn Classifier>>,
    punct: Punctuation,
}
thread_local! {
    /// Reused only during recognition; candidate tags are built after winning.
    static SPAN: std::cell::RefCell<String> = const { std::cell::RefCell::new(String::new()) };
    /// Shared start/end boundary predicates for the current lattice input.
    static BOUNDARIES: std::cell::RefCell<Vec<bool>> = const { std::cell::RefCell::new(Vec::new()) };
    /// Last start and cheapest classified-token cost for each end.
    static END_COSTS: std::cell::RefCell<Vec<(usize, i64)>> = const { std::cell::RefCell::new(Vec::new()) };
    /// Only ranks exceeding the lattice's 1000-point member stride need an
    /// explicit owner. Ordinary winners decode the owner directly.
    static LARGE_RANKS: std::cell::RefCell<Vec<(usize, usize, i64, i32, usize, i32)>> =
        const { std::cell::RefCell::new(Vec::new()) };
}
struct Branch(usize);
struct Union;
struct Punctuation;
fn name_tag(s: &str) -> String {
    let mut tag = String::with_capacity(s.len() + 9 + s.bytes().filter(|&c| c == b' ').count());
    tag.push_str("name: \"");
    for c in s.chars() {
        tag.push(if c == ' ' { '\u{a0}' } else { c });
    }
    tag.push('"');
    tag
}
type Reading = (String, i64, bool, Option<&'static str>);
fn branch(i: usize, s: &str) -> Vec<Reading> {
    let plain = |v: Vec<String>| v.into_iter().map(|s| (s, 0, false, None)).collect();
    match i {
        0 => plain(whitelist::readings(s)),
        1 => plain(electronic::readings(s)),
        2 => telephone::readings(s).into_iter().map(|(s, c)| (s, c, false, None)).collect(),
        3 => date::readings(s).into_iter().map(|(s, iso)| (s, if iso { -100_000 } else { 0 }, iso, None)).collect(),
        4 => plain(time::readings(s)),
        5 => plain(money::readings(s)),
        6 => plain(measure::readings(s)),
        7 => plain(roman::readings(s)),
        8 => plain(ordinal::readings(s)),
        9 => plain(decimal::readings(s)),
        10 => plain(fraction::readings(s)),
        11 => plain(cardinal::readings(s)),
        12 => {
            if !s.is_empty() && s.chars().all(|c| !is_nemo_space(c)) {
                vec![(s.to_owned(), 0, false, None)]
            } else {
                Vec::new()
            }
        }
        _ => Vec::new(),
    }
}
fn variants(i: usize, s: &str) -> Vec<Reading> {
    let mut out = branch(i, s);
    for (mark, spoken) in [('~', "yaklaşık"), ('±', "artı\u{a0}eksi")] {
        if let Some(rest) = s.strip_prefix(mark) {
            let rest = rest.trim_start_matches(' ');
            for (value, c, iso, _) in branch(i, rest) {
                out.push((value, c, iso, Some(spoken)));
            }
        }
    }
    out
}
fn token_is(text: &[char], start: usize, words: &[&str]) -> bool {
    let end = text[start..].iter().position(|c| is_nemo_space(*c)).map_or(text.len(), |n| start + n);
    let token = &text[start..end];
    words.iter().any(|word| word.chars().eq(token.iter().copied()))
}

const ROMAN_HEADS: &[&str] = &[
    "Bölüm",
    "bölüm",
    "Chapter",
    "chapter",
    "Cilt",
    "cilt",
    "Volume",
    "volume",
    "Kısım",
    "kısım",
    "Part",
    "part",
    "Fasikül",
    "fasikül",
    "Madde",
    "madde",
    "Louis",
    "Elizabeth",
    "Mustafa",
    "Mehmet",
    "Murat",
    "Henry",
    "George",
    "Edward",
    "Charles",
];
static ROMAN_INITIALS: LazyLock<[bool; 128]> = LazyLock::new(|| {
    let mut initials = [false; 128];
    for word in ROMAN_HEADS {
        if let Some(&first) = word.as_bytes().first() {
            if first.is_ascii() {
                initials[first as usize] = true;
            }
        }
    }
    initials
});
/// Conservative initial-character checks for each union member.
fn possible_start(member: usize, text: &[char], start: usize) -> bool {
    let mut pos = start;
    if matches!(text.get(pos), Some('~' | '±')) {
        pos += 1;
        while text.get(pos) == Some(&' ') {
            pos += 1;
        }
    }
    let Some(&c) = text.get(pos) else { return member == 12 };
    let digit = c.is_ascii_digit();
    match member {
        0 => whitelist::possible_start(text, pos),
        1 => {
            if !(c.is_ascii_alphanumeric() || matches!(c, '.' | '_' | '-' | '+')) {
                return false;
            }
            text[pos..].iter().take_while(|c| !is_nemo_space(**c)).any(|&c| c == '.')
                || ["http://", "https://", "ftp://"]
                    .iter()
                    .any(|prefix| prefix.chars().eq(text[pos..].iter().copied().take(prefix.chars().count())))
        }
        2 => {
            // Without an area, country code or textual prefix the telephone
            // grammar requires an extension. Its three spellings all begin
            // with one of these four-character sequences.
            (digit
                && text[pos..]
                    .windows(4)
                    .any(|w| w == [' ', 'a', 'n', 'k'] || w == [' ', 'd', 'a', 'h'] || w == [' ', 'e', 'x', 't']))
                || matches!(c, '+' | '(')
                || (matches!(c, 't' | 'G' | 'g')
                    && token_is(text, pos, &["tel.", "tel.:", "tel:", "tfn.", "telefon:", "telefon", "GSM:", "gsm:"]))
        }
        3 => digit || matches!(c, 'M' | 'B' | 'A' | 'C'),
        // An unprefixed clock always starts with an hour immediately followed
        // by a colon; bare hours are accepted only after a clock prefix.
        4 => {
            (digit && text[pos..].iter().skip_while(|c| c.is_ascii_digit()).next() == Some(&':'))
                || (matches!(c, 's' | 'S' | 'k') && token_is(text, pos, &["saat", "Saat", "kl."]))
        }
        5 => money::can_start(c),
        6 | 9 => digit || matches!(c, '-' | '−'),
        7 => {
            digit
                || matches!(c, 'I' | 'V' | 'X')
                || (c.is_ascii() && ROMAN_INITIALS[c as usize] && token_is(text, pos, ROMAN_HEADS))
        }
        8 => digit,
        10 => {
            digit
                || matches!(
                    c,
                    '-' | '−'
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
        11 => digit || matches!(c, '-' | '−'),
        _ => true,
    }
}
fn plausible(member: usize, span: &str, has_digit: bool, signal: bool, marker: bool, last: char) -> bool {
    let s = span.strip_prefix('~').or_else(|| span.strip_prefix('±')).map_or(span, |s| s.trim_start_matches(' '));
    match member {
        0 if !has_digit => marker || matches!(s, "&" | "©" | "§") || whitelist::abbreviation_count(s) != 0,
        0 => {
            signal
                || s.starts_with("No")
                || s.starts_with("no")
                || matches!(last, 'G' | 'g' | 'K' | 'k' | 'D' | 'd')
                || matches!(s, "CO2" | "H2O" | "O2" | "N2")
        }
        1 => marker || s.starts_with("http://") || s.starts_with("https://") || s.starts_with("ftp://"),
        2 if s.as_bytes().first().is_some_and(u8::is_ascii_digit) => {
            s.contains(" ankn") || s.contains(" dahili") || s.contains(" ext")
        }
        4 => marker || s.starts_with("saat ") || s.starts_with("Saat ") || s.starts_with("kl. "),
        5 => money::possible(s),
        7 => roman::possible_end(s),
        _ => true,
    }
}

fn word_candidates(text: &[char], start: usize, out: &mut Vec<Candidate>, boundaries: &[bool], best: &[(usize, i64)]) {
    let mut plain = true;
    let prefix = if matches!(text[start], '~' | '±') {
        let mut p = start + 1;
        while text.get(p) == Some(&' ') {
            p += 1;
        }
        Some(p)
    } else {
        None
    };
    let mut prefixed = true;
    for end in start + 1..=text.len() {
        let ch = text[end - 1];
        plain &= !is_nemo_space(ch);
        if prefix.is_some_and(|p| end > p) {
            prefixed &= !is_nemo_space(ch);
        }
        let extra = prefix.is_some_and(|p| end > p) && prefixed;
        if !plain && (prefix.is_none() || !prefixed) {
            break;
        }
        if !boundaries[end] || (best[end].0 == start && best[end].1 < cost(100.0)) {
            continue;
        }
        if plain {
            out.push(Candidate { end, cost: cost(100.0), rank: 0, tag: String::new() });
        }
        if extra {
            out.push(Candidate { end, cost: cost(100.0), rank: i32::from(plain), tag: String::new() });
        }
    }
}
impl Classifier for Union {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if start == 0 {
            LARGE_RANKS.with_borrow_mut(Vec::clear);
            END_COSTS.with_borrow_mut(|best| {
                best.clear();
                best.resize(text.len() + 1, (usize::MAX, i64::MAX));
            });
            BOUNDARIES.with_borrow_mut(|boundaries| {
                boundaries.clear();
                boundaries.extend(text.iter().map(|c| !c.is_alphanumeric()));
                boundaries.push(true);
            });
        }
        BOUNDARIES.with_borrow(|boundaries| {
            if start != 0 && !boundaries[start - 1] {
                return;
            }
            END_COSTS.with_borrow_mut(|best| {
                for member in 0..13 {
                    let first = out.len();
                    Branch(member).candidates(text, start, out, boundaries, best);
                    for candidate in &mut out[first..] {
                        let entry = &mut best[candidate.end];
                        if entry.0 == start {
                            entry.1 = entry.1.min(candidate.cost);
                        } else {
                            *entry = (start, candidate.cost);
                        }
                        let local_rank = candidate.rank;
                        candidate.rank += member as i32 * 1000;
                        if local_rank >= 1000 {
                            LARGE_RANKS.with_borrow_mut(|ranks| {
                                ranks.push((start, candidate.end, candidate.cost, candidate.rank, member, local_rank));
                            });
                        }
                    }
                }
            });
        });
    }
    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        let exceptional = LARGE_RANKS.with_borrow(|ranks| {
            ranks
                .iter()
                .find(|entry| {
                    (entry.0, entry.1, entry.2, entry.3) == (start, candidate.end, candidate.cost, candidate.rank)
                })
                .map(|entry| (entry.4, entry.5))
        });
        let (member, local_rank) = exceptional.unwrap_or_else(|| {
            let member = (candidate.rank / 1000) as usize;
            (member, candidate.rank - member as i32 * 1000)
        });
        let rank = candidate.rank;
        candidate.rank = local_rank;
        Branch(member).materialize(text, start, candidate);
        candidate.rank = rank;
    }
}
impl Branch {
    fn candidates(
        &self,
        text: &[char],
        start: usize,
        out: &mut Vec<Candidate>,
        boundaries: &[bool],
        best: &[(usize, i64)],
    ) {
        if !possible_start(self.0, text, start) {
            return;
        }
        if self.0 == 12 {
            word_candidates(text, start, out, boundaries, best);
            return;
        }
        // A digit-led whitelist expression cannot contain a colon: that mark
        // occurs only in the alphabetic No:/no: prefix.
        let mut core = start;
        if matches!(text.get(core), Some('~' | '±')) {
            core += 1;
            while text.get(core) == Some(&' ') {
                core += 1;
            }
        }
        let numeric_whitelist = self.0 == 0 && text.get(core).is_some_and(char::is_ascii_digit);
        let numeric_date = self.0 == 3 && text.get(core).is_some_and(char::is_ascii_digit);
        let numeric_money = self.0 == 5 && text.get(core).is_some_and(char::is_ascii_digit);
        let weights = [1.01, 1.02, 1.03, 1.04, 1.04, 1.05, 1.05, 1.06, 1.06, 1.07, 1.07, 1.08];
        let marker_char = match self.0 {
            0 => '^',
            1 => '.',
            4 => ':',
            _ => '\0',
        };
        // Complete minimum path costs: ISO dates alone have a -0.1 internal
        // weight; telephone's only internal weight is nonnegative.
        let minimum = [
            1_010_000, 1_020_000, 1_030_000, 940_000, 1_040_000, 1_050_000, 1_050_000, 1_060_000, 1_060_000, 1_070_000,
            1_070_000, 1_080_000,
        ][self.0];
        SPAN.with_borrow_mut(|span| {
            span.clear();
            let mut has_digit = false;
            let mut all_digits = true;
            let mut whitelist_signal = false;
            // These markers are monotone as a candidate span grows. Tracking
            // them once avoids rescanning each progressively longer prefix.
            let mut marker = false;
            let mut cardinal_suffix = false;
            let mut ordinal_colon = false;
            let mut numeric_word_start = None;
            let mut numeric_power = false;
            let mut date_word_start = None;
            let mut measure_word_start = None;
            let mut money_word_start = None;
            let mut money_rate = false;
            for end in start + 1..=text.len() {
                let ch = text[end - 1];
                let money_letter = numeric_money && !money_rate && ch.is_alphabetic();
                if numeric_money && !money_rate {
                    if money_letter {
                        if money_word_start.is_none() {
                            money_word_start = Some(span.len());
                        }
                    } else {
                        if let Some(from) = money_word_start.take() {
                            if !money::numeric_word(&span[from..], true) {
                                break;
                            }
                        }
                        if ch == '/' {
                            money_rate = true;
                        }
                    }
                }
                // Unit keys contain no spaces; once a non-quantity alphabetic
                // component is followed by a space, no longer measure can
                // extend it. Numeric quantities may precede the unit/range.
                if self.0 == 6 {
                    if ch.is_alphabetic() {
                        if measure_word_start.is_none() {
                            measure_word_start = Some(span.len());
                        }
                    } else if let Some(from) = measure_word_start.take() {
                        if ch == ' ' && !measure::is_quantity(&span[from..]) {
                            break;
                        }
                    }
                }
                let date_letter = numeric_date && ch.is_alphabetic();
                if numeric_date {
                    if date_letter {
                        if date_word_start.is_none() {
                            date_word_start = Some(span.len());
                        }
                    } else if let Some(from) = date_word_start.take() {
                        if !date::numeric_word(&span[from..], true) {
                            break;
                        }
                    }
                }
                let numeric_letter = numeric_whitelist && (ch.is_ascii_alphabetic() || matches!(ch, 'ı' | 'ş'));
                if numeric_whitelist && !numeric_power {
                    if numeric_letter {
                        if numeric_word_start.is_none() {
                            numeric_word_start = Some(span.len());
                        }
                    } else {
                        if let Some(from) = numeric_word_start.take() {
                            if !whitelist::numeric_word(&span[from..], true) {
                                break;
                            }
                        }
                        if ch == '^' {
                            numeric_power = true;
                        }
                    }
                }
                // None of these source graphs contains a colon. Once one is
                // encountered, extending this span cannot make it valid.
                if ch == ':' && (numeric_whitelist || matches!(self.0, 3 | 5 | 6 | 7 | 9)) {
                    break;
                }
                // Ordinal colon suffixes consist solely of Turkish letters.
                if self.0 == 8 {
                    if ordinal_colon && !"abcçdefgğhıijklmnoöprsştuüvyzâîû".contains(ch) {
                        break;
                    }
                    if ch == ':' {
                        ordinal_colon = true;
                    }
                }
                if self.0 == 11 {
                    if cardinal_suffix {
                        if !"abcçdefgğhıijklmnoöprsştuüvyzâîû".contains(ch) {
                            break;
                        }
                    } else if matches!(ch, '\'' | '’' | '`' | '´') {
                        cardinal_suffix = true;
                    } else if !ch.is_ascii_digit()
                        && !matches!(ch, '.' | ' ' | '\u{202f}' | '-' | '−' | '–' | '—' | ':' | '~' | '±')
                    {
                        break;
                    }
                }
                if self.0 == 10
                    && !ch.is_ascii_digit()
                    && !matches!(
                        ch,
                        '.' | ' '
                            | '\u{202f}'
                            | '-'
                            | '−'
                            | '/'
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
                if self.0 == 4
                    && !ch.is_ascii_alphanumeric()
                    && !matches!(ch, ':' | '.' | ' ' | '-' | '–' | '—' | '~' | '±' | 'ö' | 'İ')
                {
                    break;
                }
                span.push(ch);
                if money_letter && !money::numeric_word(&span[money_word_start.unwrap()..], false) {
                    break;
                }
                if date_letter && !date::numeric_word(&span[date_word_start.unwrap()..], false) {
                    break;
                }
                if numeric_letter && !numeric_power {
                    if !whitelist::numeric_word(&span[numeric_word_start.unwrap()..], false) {
                        break;
                    }
                }
                if self.0 == 1 && is_nemo_space(ch) {
                    let modifier_only = span
                        .strip_prefix('~')
                        .or_else(|| span.strip_prefix('±'))
                        .is_some_and(|rest| rest.chars().all(|c| c == ' '));
                    if !modifier_only {
                        break;
                    }
                }
                has_digit |= ch.is_ascii_digit();
                all_digits &= ch.is_ascii_digit();
                if self.0 == 0 {
                    whitelist_signal |=
                        matches!(ch, '%' | '‰' | '^' | '+' | '=' | 'x' | '×' | '*' | '.' | '-' | '–' | '—' | '#' | 'ş');
                }
                marker |= ch == marker_char;
                if !boundaries[end] || is_nemo_space(ch) {
                    continue;
                }
                if best[end].0 == start && best[end].1 < minimum {
                    continue;
                }
                if matches!(self.0, 2 | 3 | 4 | 5 | 6 | 8 | 9 | 11) && !has_digit {
                    continue;
                }
                if self.0 == 3 && !date::possible_end(ch) {
                    continue;
                }
                if all_digits {
                    if self.0 == 11 && (graph_utils::is_positive(&span) || (span.len() > 1 && span.starts_with('0'))) {
                        out.push(Candidate { end, cost: cost(weights[11]), rank: 0, tag: String::new() });
                    }
                    continue;
                }
                if !plausible(self.0, &span, has_digit, whitelist_signal, marker, ch) {
                    continue;
                }
                if self.0 == 2 {
                    let mut rank = 0;
                    let mut emit = |extra| {
                        out.push(Candidate { end, cost: cost(weights[2]) + extra, rank, tag: String::new() });
                        rank += 1;
                    };
                    telephone::visit(&span, &mut emit);
                    if let Some(rest) = span.strip_prefix('~').or_else(|| span.strip_prefix('±')) {
                        telephone::visit(rest.trim_start_matches(' '), &mut emit);
                    }
                    continue;
                }
                if self.0 == 3 {
                    let mut rank = 0;
                    let mut emit = |iso| {
                        out.push(Candidate {
                            end,
                            cost: cost(weights[3]) + if iso { -100_000 } else { 0 },
                            rank,
                            tag: String::new(),
                        });
                        rank += 1;
                    };
                    date::visit(&span, &mut emit);
                    if let Some(rest) = span.strip_prefix('~').or_else(|| span.strip_prefix('±')) {
                        date::visit(rest.trim_start_matches(' '), &mut emit);
                    }
                    continue;
                }
                let count: fn(&str) -> usize = match self.0 {
                    0 => whitelist::count,
                    1 => electronic::count,
                    4 => time::count,
                    5 => money::count,
                    6 => measure::count,
                    7 => roman::count,
                    8 => ordinal::count,
                    9 => decimal::count,
                    10 => fraction::count,
                    11 => cardinal::count,
                    _ => unreachable!(),
                };
                let mut paths = count(span);
                if let Some(rest) = span.strip_prefix('~').or_else(|| span.strip_prefix('±')) {
                    paths += count(rest.trim_start_matches(' '));
                }
                for rank in 0..paths {
                    out.push(Candidate { end, cost: cost(weights[self.0]), rank: rank as i32, tag: String::new() });
                }
            }
        });
    }
    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        if self.0 == 12 {
            let mut from = start;
            let modifier = if matches!(text[start], '~' | '±') {
                let mut rest = start + 1;
                while rest < candidate.end && text[rest] == ' ' {
                    rest += 1;
                }
                if candidate.rank != 0 || rest > start + 1 {
                    from = rest;
                    Some(if text[start] == '~' { "yaklaşık" } else { "artı\u{a0}eksi" })
                } else {
                    None
                }
            } else {
                None
            };
            let mut tag = String::with_capacity(48 + (candidate.end - start) * 2);
            if let Some(prefix) = modifier {
                tag.push_str("name: \"");
                tag.push_str(prefix);
                tag.push_str("\" } tokens { ");
            }
            tag.push_str("name: \"");
            tag.extend(&text[from..candidate.end]);
            tag.push('"');
            candidate.tag = tag;
            return;
        }
        let span: String = text[start..candidate.end].iter().collect();
        if self.0 == 11 && span.bytes().all(|c| c.is_ascii_digit()) {
            let number = graph_utils::positive(&span).or_else(|| graph_utils::digit_sequence(&span));
            if let Some(number) = number {
                candidate.tag = name_tag(&number);
            }
            return;
        }
        if candidate.rank == 0 && !matches!(text[start], '~' | '±') {
            let direct_tag = match self.0 {
                4 => time::simple_tag(&span),
                5 => money::simple_tag(&span),
                6 => measure::simple_tag(&span),
                _ => None,
            };
            if let Some(tag) = direct_tag {
                candidate.tag = tag;
                return;
            }
        }
        if let Some((value, _, iso, prefix)) = variants(self.0, &span).into_iter().nth(candidate.rank as usize) {
            let tag = if iso { value } else { name_tag(&value) };
            candidate.tag = match prefix {
                Some(p) => format!("name: \"{p}\" }} tokens {{ {tag}"),
                None => tag,
            };
        }
    }
}
impl Classifier for Punctuation {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        let chars = "!\\\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~—–―…«»“”„‘’";
        for end in start + 1..=text.len() {
            let c = text[end - 1];
            if !chars.contains(c) {
                break;
            }
            out.push(Candidate { end, cost: cost(1.1), rank: 0, tag: String::new() });
        }
    }
    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        let mut tag = String::from("name: \"");
        tag.extend(&text[start..candidate.end]);
        tag.push('"');
        candidate.tag = tag;
    }
}
impl Grammar for Turkish {
    fn code(&self) -> &'static str {
        "tr"
    }
    fn shape(&self) -> Shape {
        Shape::Separated
    }
    fn classifiers(&self) -> &[Box<dyn Classifier>] {
        &self.classifiers
    }
    fn punct(&self) -> Option<&dyn Classifier> {
        Some(&self.punct)
    }
    fn verbalize(&self, token: &Dict) -> Option<String> {
        if let Some(s) = common::verbalize_name(token) {
            return Some(s);
        }
        let [(key, Value::Dict(d))] = token.as_slice() else { return None };
        if key != "date" {
            return None;
        }
        let mut f = Fields::new(d);
        let day = f.take("day")?;
        let month = f.take("month")?;
        let year = f.take("year")?;
        if !f.done() || day.is_empty() || month.is_empty() || year.is_empty() {
            return None;
        }
        Some(format!("{day} {month} {year}").replace('\u{a0}', " "))
    }
    fn verbalize_split(&self, split: &[Dict]) -> Option<String> {
        let mut out = String::with_capacity(16 * split.len());
        for (index, token) in split.iter().enumerate() {
            let [(key, Value::Dict(body))] = token.as_slice() else { return None };
            if key != "tokens" {
                return None;
            }
            if index != 0 {
                out.push(' ');
            }
            if let [(name, Value::Str(value))] = body.as_slice() {
                if name == "name" {
                    if value.contains(' ') {
                        return None;
                    }
                    for c in value.chars() {
                        out.push(if c == '\u{a0}' { ' ' } else { c });
                    }
                    continue;
                }
            }
            let value = crate::permute::first_ordering(body, &mut |ordered: &Dict| self.verbalize(ordered))?;
            out.push_str(&value);
        }
        Some(out)
    }
}
static GRAMMAR: LazyLock<Turkish> =
    LazyLock::new(|| Turkish { classifiers: vec![Box::new(Union)], punct: Punctuation });
pub fn grammar() -> &'static dyn Grammar {
    &*GRAMMAR
}
