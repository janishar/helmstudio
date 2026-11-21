//! Thai text normalization grammar.
mod date;
mod decimal;
mod electronic;
mod fraction;
mod measure;
mod money;
mod number;
mod other;
mod time;
mod whitelist;

use crate::lang::common::{self, Fields};
use crate::lattice::{Candidate, Classifier, Shape};
use crate::pipeline::Grammar;
use crate::tag::{Dict, Value};
use std::sync::LazyLock;

struct Thai {
    classifiers: [Box<dyn Classifier>; 1],
}
struct Union;
fn push(out: &mut Vec<Candidate>, end: usize, cost: i64, rank: i32, word: String) {
    let mut tag = String::with_capacity(word.len() + 16);
    tag.push_str("name: \"");
    for c in word.chars() {
        tag.push(if c == ' ' { '\u{a0}' } else { c });
    }
    tag.push('"');
    out.push(Candidate { end, cost, rank, tag });
}
fn ranges(s: &str) -> impl Iterator<Item = (&str, &str)> {
    s.char_indices().filter_map(move |(i, c)| {
        if i > 0 && matches!(c, '-' | '–' | '—' | '~' | '〜' | '～') {
            let a = &s[..i];
            let b = &s[i + c.len_utf8()..];
            Some((a.strip_suffix(' ').unwrap_or(a), b.strip_prefix(' ').unwrap_or(b)))
        } else {
            None
        }
    })
}
fn punctuation(c: char) -> bool {
    c.is_ascii_punctuation() || matches!(c, 'ฯ' | 'ๆ' | '๏' | '๚' | '๛')
}
fn word(c: char) -> bool {
    number::digit(c).is_none()
        && !matches!(c, ' ' | '\t' | '\n' | '\r' | '\u{a0}' | '"' | '©' | '§' | '®' | '™')
        && !punctuation(c)
}

fn starts(text: &[char], start: usize, prefix: &str) -> bool {
    let mut input = text[start..].iter();
    prefix.chars().all(|c| input.next() == Some(&c))
}

/// Thai prose can be adjacent to tokens, but only these native prefixes begin a
/// non-word classifier. ASCII starts remain unrestricted (domains and codes).
fn special_word_start(text: &[char], start: usize) -> bool {
    text[start].is_ascii()
        || matches!(
            text[start],
            '฿' | '€'
                | '£'
                | '¥'
                | '₩'
                | '₹'
                | '₽'
                | '₺'
                | '₪'
                | '₴'
                | '₫'
                | '৳'
                | '₦'
                | '₱'
                | '֏'
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
        || match text[start] {
            'ว' => starts(text, start, "วันที่"),
            'พ' => starts(text, start, "พ.ศ.") || starts(text, start, "พุทธศักราช"),
            'ค' => starts(text, start, "ค.ศ.") || starts(text, start, "คริสต์ศักราช"),
            'บ' => starts(text, start, "บทที่"),
            'ภ' => starts(text, start, "ภาคที่"),
            'เ' => starts(text, start, "เล่มที่"),
            'ร' => starts(text, start, "รัชกาลที่"),
            'ศ' => starts(text, start, "ศตวรรษที่"),
            'ด' => starts(text, start, "ดร."),
            'ท' => starts(text, start, "ที่"),
            _ => false,
        }
}

fn money_marker_completed(s: &str, last: char) -> bool {
    const MARKERS: &[&str] = &[
        "฿",
        "$",
        "€",
        "£",
        "¥",
        "円",
        "₩",
        "₹",
        "₽",
        "₺",
        "₪",
        "₴",
        "₫",
        "৳",
        "₦",
        "₱",
        "֏",
        "¢",
        "THB",
        "USD",
        "EUR",
        "GBP",
        "JPY",
        "KRW",
        "INR",
        "RUB",
        "TRY",
        "ILS",
        "UAH",
        "VND",
        "BDT",
        "NGN",
        "PHP",
        "AMD",
        "CHF",
        "CAD",
        "AUD",
        "BRL",
        "MXN",
        "Rs.",
        "บาท",
    ];
    static INDEX: LazyLock<Vec<(char, &'static str)>> = LazyLock::new(|| {
        let mut index: Vec<_> = MARKERS.iter().map(|&key| (key.chars().next_back().unwrap(), key)).collect();
        index.sort_unstable_by_key(|row| row.0);
        index
    });
    let i = INDEX.partition_point(|row| row.0 < last);
    INDEX[i..].iter().take_while(|row| row.0 == last).any(|row| s.ends_with(row.1))
}

/// A number followed by a Thai character can continue only into a localized
/// month, unit, currency suffix or the BC suffix.  If every matching key is
/// followed by an ordinary Thai word, no source continuation can cross that
/// word: date continuations start with a year or an era; measure and money
/// continuations start with a range operator or a per-unit slash.
fn thai_suffix_cap(text: &[char], pos: usize) -> Option<usize> {
    static INDEX: LazyLock<Vec<(char, &'static str)>> = LazyLock::new(|| {
        let mut rows: Vec<_> = date::thai_suffixes()
            .chain(measure::thai_suffixes())
            .chain(money::thai_suffixes())
            .map(|key| (key.chars().next().unwrap(), key))
            .collect();
        rows.sort_unstable_by_key(|row| row.0);
        rows
    });
    let first = text[pos];
    let i = INDEX.partition_point(|row| row.0 < first);
    let mut last_end = pos;
    for &(_, key) in INDEX[i..].iter().take_while(|row| row.0 == first) {
        if !starts(text, pos, key) {
            continue;
        }
        let end = pos + key.chars().count();
        last_end = last_end.max(end);
        let after = text.get(end..);
        match after {
            Some([' ', c, ..])
                if ('\u{0e00}'..='\u{0e7f}').contains(c) && number::digit(*c).is_none() && !matches!(*c, 'พ' | 'ค') =>
                {}
            _ => return None,
        }
    }
    Some(last_end)
}
impl Classifier for Union {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        let first = text[start];
        // Whitespace is consumed by the lattice and no Thai class begins
        // with a whitespace character.
        if matches!(first, ' ' | '\t' | '\n' | '\r' | '\u{a0}') {
            return;
        }
        if word(first) && !special_word_start(text, start) {
            for end in start + 1..=text.len() {
                if !word(text[end - 1]) {
                    break;
                }
                out.push(Candidate { end, cost: 100_000_000, rank: 13, tag: String::new() });
            }
            return;
        }
        let numeric_start = number::digit(first).is_some() || matches!(first, '-' | '−');
        let digit_start = number::digit(first).is_some();
        let money_start = money::possible_start(first);
        let date_prefix = match first {
            'ว' => starts(text, start, "วันที่"),
            'พ' => starts(text, start, "พ.ศ.") || starts(text, start, "พุทธศักราช"),
            'ค' => starts(text, start, "ค.ศ.") || starts(text, start, "คริสต์ศักราช"),
            _ => false,
        };
        let ordinal_prefix = first == 'ท' && starts(text, start, "ที่");
        let title_prefix = first == 'ด' && starts(text, start, "ดร.");
        let mut number_prefix = number::NumberPrefix::new(matches!(first, '-' | '−'));
        let mut s = String::with_capacity((text.len() - start).min(64) * 3);
        let mut has_digit = false;
        let mut has_dot = false;
        let mut has_colon = false;
        let mut has_slash = false;
        let mut has_at = false;
        let mut has_space = false;
        let mut has_range = false;
        let mut has_vulgar = false;
        let mut all_word = true;
        let mut all_punct = true;
        let mut clock_chars = true;
        let mut ascii_letter_or_power = false;
        let mut digit_count = 0usize;
        let mut phone_chars = true;
        let mut has_money = false;
        let mut has_month = false;
        let mut has_era_suffix = false;
        let mut thai_letter = false;
        let mut after_number_space = false;
        let mut suffix_cap = None;
        for end in start + 1..=text.len() {
            if suffix_cap.is_some_and(|limit| end > limit) {
                break;
            }
            let c = text[end - 1];
            let is_digit = number::digit(c).is_some();
            let after_number = numeric_start && number_prefix.accepted();
            if (after_number || after_number_space) && ('\u{0e00}'..='\u{0e7f}').contains(&c) && !is_digit {
                suffix_cap = thai_suffix_cap(text, end - 1);
                if suffix_cap == Some(end - 1) {
                    break;
                }
            }
            after_number_space = after_number && c == ' ';
            s.push(c);
            if numeric_start {
                number_prefix.advance(c, end == start + 1);
            }
            has_digit |= is_digit;
            digit_count += usize::from(is_digit);
            if phone_chars {
                phone_chars = is_digit || matches!(c, '+' | ' ' | '-' | '.' | '(' | ')');
            }
            // An accepted cardinal prefix contains only digits, commas and
            // an optional initial minus; other classes require a delimiter.
            if numeric_start && number_prefix.accepted() {
                all_word = false;
                all_punct = false;
                if !is_digit {
                    clock_chars = false;
                }
                out.push(Candidate { end, cost: 2_200_000, rank: 10, tag: String::new() });
                continue;
            }
            has_at |= c == '@';
            has_space |= c == ' ';
            ascii_letter_or_power |= c.is_ascii_alphabetic() || c == '^';
            thai_letter |= ('ก'..='๛').contains(&c) && !is_digit;
            if money_start && !has_money && !is_digit {
                has_money = money_marker_completed(&s, c);
            }
            if digit_start && thai_letter && !has_month {
                has_month = date::ends_month(&s, c);
            }
            if !has_era_suffix && matches!(c, 'C' | 'D' | 'E' | 'ล') {
                has_era_suffix = [" BC", " AD", " CE", " BCE", " ปีก่อนคริสตกาล"].iter().any(|key| s.ends_with(key));
            }
            has_dot |= c == '.';
            has_colon |= c == ':';
            has_slash |= c == '/';
            has_range |= matches!(c, '-' | '–' | '—' | '~' | '〜' | '～');
            has_vulgar |= matches!(
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
                    | '⅛'
                    | '⅜'
                    | '⅝'
                    | '⅞'
                    | '⅐'
                    | '⅑'
                    | '⅒'
            );
            if all_word {
                all_word = word(c);
            }
            if all_punct {
                all_punct = punctuation(c);
            }
            if clock_chars {
                clock_chars = is_digit || c.is_ascii_alphabetic() || matches!(c, ':' | '.' | '-' | '–' | '—' | ' ');
            }
            // A digit-initial span containing a colon can only be a clock or
            // cardinal ratio; neither can recover outside this alphabet.
            if digit_start && has_colon && !clock_chars {
                break;
            }
            if !has_colon
                && (first.is_ascii_alphabetic()
                    || matches!(first, '#' | '©' | '§' | '&' | 'ฯ')
                    || (digit_start && ascii_letter_or_power)
                    || title_prefix)
            {
                let mut dominates = false;
                for (v, cost) in whitelist::parse(&s) {
                    dominates |= cost < 20_000;
                    push(out, end, 1_010_000 + cost, 0, v);
                }
                // The cheapest other branch is an ISO date at 1.03; the
                // remaining measures' -1.5 range weight still gives >=1.56.
                if dominates {
                    continue;
                }
            }
            if first.is_ascii_alphanumeric() && (has_dot || has_at || has_slash) {
                for (v, cost) in electronic::parse(&s) {
                    push(out, end, 1_020_000 + cost, 1, v);
                }
            }
            if !has_colon
                && has_digit
                && (digit_start
                    && (has_dot || has_slash || has_range || s.ends_with('s') || has_era_suffix || has_month)
                    || date_prefix)
            {
                for (v, cost, tagged) in date::parse(&s) {
                    if tagged {
                        out.push(Candidate { end, cost: 1_030_000 + cost, rank: 2, tag: v });
                    } else {
                        push(out, end, 1_030_000 + cost, 2, v);
                    }
                }
            }
            if digit_start && has_colon {
                let mut accepted_clock = false;
                time::costs(&s, |cost| {
                    accepted_clock = true;
                    out.push(Candidate { end, cost: 1_040_000 + cost, rank: 3, tag: String::new() });
                });
                // Digit-initial colon spans can only add a cardinal ratio
                // below; its 3.60 cost is strictly above every clock cost
                // (at most 2.09). Other remaining branches reject the colon.
                if accepted_clock {
                    continue;
                }
            }
            if !has_colon && has_digit && has_money && money::possible_end(c) {
                if money::simple(&s) {
                    out.push(Candidate { end, cost: 2_130_000, rank: 4, tag: String::new() });
                } else {
                    for (v, cost) in money::parse(&s) {
                        push(out, end, 1_050_000 + cost, 4, v);
                    }
                }
            }
            if !has_colon
                && (has_digit || has_vulgar)
                && (numeric_start || has_vulgar || matches!(first, '~' | '±'))
                && (matches!(first, '~' | '±') && number::digit(c).is_some() || measure::possible_end(c))
            {
                match measure::recognize_unit(&s) {
                    measure::UnitMatch::Integer => {
                        out.push(Candidate { end, cost: 3_220_000, rank: 5, tag: String::new() });
                    }
                    measure::UnitMatch::Other => {
                        for (v, cost) in measure::parse(&s) {
                            push(out, end, 1_060_000 + cost, 5, v);
                        }
                    }
                    measure::UnitMatch::Absent if matches!(first, '~' | '±') => {
                        for (v, cost) in measure::parse(&s) {
                            push(out, end, 1_060_000 + cost, 5, v);
                        }
                    }
                    measure::UnitMatch::Absent => {}
                }
            }
            if (first.is_ascii_alphabetic() || matches!(first, 'บ' | 'ภ' | 'เ' | 'ร' | 'ศ'))
                && matches!(c, 'I' | 'V' | 'X')
            {
                if let Some(v) = other::roman(&s) {
                    push(out, end, 1_070_000, 6, v);
                }
            }
            if !has_colon && (has_vulgar || has_slash) && (numeric_start || has_vulgar) {
                if let Some(v) = fraction::fraction(&s) {
                    push(out, end, 1_080_000, 7, v);
                }
            }
            if !has_colon && numeric_start && has_dot {
                if let Some(v) = decimal::decimal(&s) {
                    push(out, end, 2_180_000, 8, v);
                }
            }
            if !has_colon && digit_start && has_range {
                for (a, b) in ranges(&s) {
                    if has_dot {
                        if let (Some(a), Some(b)) = (decimal::positive(a), decimal::positive(b)) {
                            push(out, end, 2_130_000, 8, format!("{a}ถึง{b}"));
                        }
                    }
                    if number::valid_positive(a) && number::valid_positive(b) {
                        let a = number::positive(a).unwrap();
                        let b = number::positive(b).unwrap();
                        push(out, end, 2_150_000, 10, format!("{a}ถึง{b}"));
                    }
                }
            }
            if !has_colon && has_digit && ((digit_start && matches!(c, 't' | 'd' | 'h')) || ordinal_prefix) {
                if let Some(v) = other::ordinal(&s) {
                    push(out, end, 1_090_000, 9, v);
                }
            }
            if digit_start && has_colon {
                if let Some((a, b)) = s.split_once(':') {
                    if number::valid_positive(a) && number::valid_positive(b) {
                        out.push(Candidate { end, cost: 3_600_000, rank: 10, tag: String::new() });
                    }
                }
            }
            if !has_colon
                && digit_count >= 7
                && phone_chars
                && (digit_start || first == '+')
                && (has_range || has_space || first == '+')
            {
                for v in other::telephone(&s) {
                    push(out, end, 1_110_000, 11, v);
                }
            }
            if all_punct {
                out.push(Candidate { end, cost: 1_150_000, rank: 12, tag: String::new() });
            }
            if all_word {
                out.push(Candidate { end, cost: 100_000_000, rank: 13, tag: String::new() });
            }
        }
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        if !candidate.tag.is_empty() {
            return;
        }
        if matches!(candidate.rank, 12 | 13) {
            let mut tag = String::with_capacity(8 + (candidate.end - start) * 3);
            tag.push_str("name: \"");
            for &c in &text[start..candidate.end] {
                tag.push(if c == ' ' { '\u{a0}' } else { c });
            }
            tag.push('"');
            candidate.tag = tag;
            return;
        }
        let input: String = text[start..candidate.end].iter().collect();
        let value = if candidate.rank == 10 && candidate.cost == 3_600_000 {
            let (a, b) = input.split_once(':').expect("validated Thai ratio");
            format!("{}ต่อ{}", number::positive(a).unwrap(), number::positive(b).unwrap())
        } else if candidate.rank == 10 {
            number::number(&input).expect("validated Thai cardinal")
        } else if candidate.rank == 3 {
            time::render(&input, candidate.cost - 1_040_000).expect("validated Thai time")
        } else if candidate.rank == 4 {
            money::render_simple(&input).expect("validated Thai money")
        } else if candidate.rank == 5 {
            measure::render_simple(&input).expect("validated Thai measure")
        } else {
            input
        };
        let mut tag = String::with_capacity(value.len() + 16);
        tag.push_str("name: \"");
        for c in value.chars() {
            tag.push(if c == ' ' { '\u{a0}' } else { c });
        }
        tag.push('"');
        candidate.tag = tag;
    }
}
impl Grammar for Thai {
    fn code(&self) -> &'static str {
        "th"
    }
    fn shape(&self) -> Shape {
        Shape::Thai
    }
    fn classifiers(&self) -> &[Box<dyn Classifier>] {
        &self.classifiers
    }
    fn punct(&self) -> Option<&dyn Classifier> {
        None
    }
    fn verbalize(&self, token: &Dict) -> Option<String> {
        if let Some(name) = common::verbalize_name(token) {
            return Some(name);
        }
        let [(k, Value::Dict(d))] = token.as_slice() else { return None };
        if k != "date" {
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
        let mut out = String::with_capacity(split.len().saturating_mul(24));
        for token in split {
            let [(key, Value::Dict(body))] = token.as_slice() else { return None };
            if key != "tokens" {
                return None;
            }
            if let [(name, Value::Str(value))] = body.as_slice() {
                if name == "name" {
                    if value.contains(' ') {
                        return None;
                    }
                    if !value.contains('\u{a0}') {
                        out.push_str(value);
                    } else {
                        for c in value.chars() {
                            out.push(if c == '\u{a0}' { ' ' } else { c });
                        }
                    }
                    continue;
                }
            }
            let word = crate::permute::first_ordering(body, &mut |ordered: &Dict| self.verbalize(ordered))?;
            out.push_str(&word);
        }
        Some(out)
    }
    fn token_join(&self) -> &'static str {
        ""
    }
}
static GRAMMAR: LazyLock<Thai> = LazyLock::new(|| Thai { classifiers: [Box::new(Union)] });
pub fn grammar() -> &'static dyn Grammar {
    &*GRAMMAR
}
