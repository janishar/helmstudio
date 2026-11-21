//! Malayalam text normalization grammar, ported from the production NeMo package.
mod amount;
mod composite;
mod datetime;
mod lexical;
mod number;
mod numeric;
mod verbalize;

use crate::lang::common::Punctuation;
use crate::lattice::{Candidate, Classifier, Shape};
use crate::pipeline::Grammar;
use crate::tag::Dict;
use crate::text::{cost, is_nemo_space, space_to_nbsp};
use std::sync::LazyLock;

static UNIT_ENDS: LazyLock<Vec<char>> = LazyLock::new(|| {
    let mut ends: Vec<char> = amount::UNITS.rows().iter().filter_map(|r| r.key.chars().last()).collect();
    ends.sort_unstable();
    ends.dedup();
    ends
});
/// Sorted source keys support both "key extends tail" and "tail extends key"
/// without walking each unit, quantity, month and era on every candidate.
static NUMERIC_TAIL_KEYS: LazyLock<Vec<&'static str>> = LazyLock::new(|| {
    let mut keys = Vec::new();
    for table in [&*amount::UNITS, &*numeric::QUANTITIES, &*datetime::MONTH_NAMES, &*datetime::ERAS] {
        keys.extend(table.rows().iter().map(|row| row.key.as_str()));
    }
    keys.push("സമം");
    keys.sort_unstable();
    keys.dedup();
    keys
});

struct Ml {
    classifiers: Vec<Box<dyn Classifier>>,
    punct: MlPunctuation,
}
impl Grammar for Ml {
    fn code(&self) -> &'static str {
        "ml"
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
        verbalize::one(token)
    }
}

struct MlPunctuation(Punctuation);
impl Classifier for MlPunctuation {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        let from = out.len();
        let isolated = |c| matches!(c, '%' | '‰' | '~' | '&' | '§' | '¶');
        let mut end = start;
        let mut has_isolated = false;
        while end < text.len() && self.0.is_mark(text[end]) {
            has_isolated |= isolated(text[end]);
            end += 1;
            if end - start > 1 && has_isolated {
                break;
            }
            out.push(Candidate { end, cost: cost(1.1), rank: 0, tag: String::new() });
        }
        if text.get(start) == Some(&'<') {
            let mut j = start + 1;
            while j < text.len() && !is_nemo_space(text[j]) && text[j] != '<' && text[j] != '>' {
                j += 1;
            }
            if j < text.len()
                && text[j] == '>'
                && j > start + 1
                && !text[start..=j].iter().any(|&c| isolated(c))
                && !out.iter().any(|c| c.end == j + 1)
            {
                out.push(Candidate { end: j + 1, cost: cost(1.1), rank: 0, tag: String::new() });
            }
        }
        out[from..].sort_by_key(|c| c.end);
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        let mut tag = String::with_capacity(8 + text[start..candidate.end].iter().map(|c| c.len_utf8()).sum::<usize>());
        tag.push_str("name: \"");
        tag.extend(&text[start..candidate.end]);
        tag.push('"');
        candidate.tag = tag;
    }
}

fn ml_char(c: char) -> bool {
    (('\u{d00}'..='\u{d7f}').contains(&c) && number::digit(c).is_none()) || c == '\u{200c}' || c == '\u{200d}'
}
fn word(s: &str, punct: &Punctuation, out: &mut Vec<numeric::Reading>) {
    if s.is_empty() || s.chars().any(is_nemo_space) {
        return;
    }
    let (mut core, mut segment, mut segment_ml, mut valid, mut runs) = (false, false, true, true, 0);
    for c in s.chars() {
        if punct.is_mark(c) || matches!(c, '‰' | '§' | '¶') {
            if segment && segment_ml {
                runs += 1;
            }
            segment = false;
            segment_ml = true;
        } else if number::digit(c).is_some() || matches!(c, '$' | '€' | '£' | '¥' | '₹' | '%' | '‰' | '°' | '℃' | '℉')
        {
            valid = false;
            break;
        } else {
            core = true;
            segment = true;
            segment_ml &= ml_char(c);
        }
    }
    if segment && segment_ml {
        runs += 1;
    }
    if core && valid {
        out.push((format!("name: \"{}\"", space_to_nbsp(s)), 100_000_000 - runs * 100, 13));
    }
    out.push((format!("name: \"{}\"", space_to_nbsp(s)), 200_000_000, 14));
}
fn all(s: &str, skip_measure: bool, out: &mut Vec<numeric::Reading>) {
    let has_digit = s.chars().any(|c| number::digit(c).is_some());
    let first = s.chars().next().unwrap();
    if s.ends_with(' ') {
        if (number::digit(first).is_some() || first == '+' || first == '(')
            && s.chars().all(|c| number::digit(c).is_some() || matches!(c, ' ' | '+' | '-' | '.' | '(' | ')'))
        {
            lexical::telephone(s, out);
        }
        return;
    }
    let numeric_start = number::digit(first).is_some() || matches!(first, '-' | '−');
    lexical::whitelist(s, out);
    if has_digit {
        if (numeric_start || matches!(first, '©' | '§'))
            && s.chars().all(|c| {
                number::digit(c).is_some()
                    || matches!(
                        c,
                        ' ' | '+'
                            | '-'
                            | '−'
                            | '–'
                            | '—'
                            | 'x'
                            | '*'
                            | '×'
                            | '/'
                            | '÷'
                            | '='
                            | '^'
                            | ':'
                            | '©'
                            | '§'
                            | 'സ'
                            | 'മ'
                            | 'ം'
                    )
            })
        {
            composite::classify(s, out);
        }
        if numeric_start && !s.contains(':') {
            numeric::classify(s, out);
        }
        if !skip_measure
            && numeric_start
            && s.chars().last().is_some_and(|c| UNIT_ENDS.binary_search(&c).is_ok())
            && amount::UNITS.rows().iter().any(|r| s.ends_with(r.key.as_str()))
        {
            amount::measure(s, out);
        }
        let currency = s.chars().any(|c| {
            c.is_ascii_uppercase()
                || matches!(
                    c,
                    '₹' | '$'
                        | '€'
                        | '£'
                        | '¥'
                        | '₽'
                        | '₩'
                        | '₺'
                        | '₪'
                        | '₴'
                        | '₫'
                        | '৳'
                        | '₦'
                        | '₱'
                        | '฿'
                        | '֏'
                        | '¢'
                )
        }) && amount::CURRENCIES.rows().iter().any(|r| s.contains(r.key.as_str()));
        if currency && (numeric_start || amount::CURRENCIES.rows().iter().any(|r| s.starts_with(r.key.as_str()))) {
            amount::money(s, out);
        }
        if (numeric_start || datetime::MONTH_NAMES.rows().iter().any(|r| s.starts_with(r.key.as_str())))
            && (s.contains([' ', '/', '-', '.', '–']) || s.ends_with('s') || s.ends_with("കൾ"))
        {
            datetime::date(s, out);
        }
        if number::digit(first).is_some() && (s.contains(':') || s.chars().any(|c| matches!(c, 'a' | 'A' | 'p' | 'P')))
        {
            datetime::time(s, out);
        }
        if (number::digit(first).is_some() || first == '+' || first == '(')
            && s.chars().all(|c| number::digit(c).is_some() || matches!(c, ' ' | '+' | '-' | '.' | '(' | ')'))
        {
            lexical::telephone(s, out);
        }
        if (numeric_start || first.is_ascii_alphabetic() || ml_char(first))
            && s.chars().all(|c| {
                number::digit(c).is_some()
                    || c.is_ascii_alphabetic()
                    || ml_char(c)
                    || matches!(c, '-' | '.' | '^' | ' ')
            })
            && (!s.contains(' ') || first.is_ascii_alphabetic())
        {
            lexical::serial(s, out);
        }
    } else if s.chars().any(|c| {
        matches!(
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
        )
    }) {
        numeric::classify(s, out);
    }
    if s.contains('@') || s.contains('.') || s.contains('/') {
        lexical::electronic(s, out);
    }
    if s.chars().any(|c| matches!(c, 'I' | 'V' | 'X' | 'L' | 'C' | 'D' | 'M')) {
        lexical::roman(s, out);
    }
    for separator in ['-', '–', '—'] {
        for (at, _) in s.match_indices(separator) {
            let a = s[..at].trim_end_matches(' ');
            let b = s[at + separator.len_utf8()..].trim_start_matches(' ');
            let mut left = Vec::new();
            let mut right = Vec::new();
            amount::money(a, &mut left);
            amount::money(b, &mut right);
            for (la, lc, _) in &left {
                for (rb, rc, _) in &right {
                    out.push((
                        format!("{la} }} tokens {{ name: \"മുതൽ\" }} tokens {{ {rb} }} tokens {{ name: \"വരെ\""),
                        1_040_000 + (lc - 1_080_000) + (rc - 1_080_000),
                        4,
                    ));
                }
            }
        }
    }
}
/// A Malayalam word can continue across a space only in a month/date or Roman-key branch.
fn word_can_continue(word: &str) -> bool {
    let month = word.strip_suffix(',').unwrap_or(word);
    let month = month.strip_suffix('.').unwrap_or(month);
    datetime::MONTH_NAMES.contains_key(month)
        || lexical::roman_key(word)
        || word == "ലോക"
        || lexical::WHITELIST.rows().iter().any(|r| r.key.starts_with(word) && r.key.contains(' '))
}

/// Malayalam following a number can only extend a unit, magnitude, month, era or math word.
fn numeric_tail_can_continue(tail: &str) -> bool {
    let keys = &*NUMERIC_TAIL_KEYS;
    let first = keys.partition_point(|key| *key < tail);
    if keys.get(first).is_some_and(|key| key.starts_with(tail)) {
        return true;
    }
    tail.char_indices().map(|(end, _)| end).skip(1).any(|end| keys.binary_search(&&tail[..end]).is_ok())
}
/// Fast recognition of the overlapping ratio and 24-hour clock branches. Rendering is
/// deferred until the lattice has selected a reading.
fn digit_clock(text: &[char], end: usize, out: &mut Vec<Candidate>) {
    let mut lengths = [0usize; 3];
    let mut values = [0u32; 3];
    let mut zeros = [false; 3];
    let mut part = 0;
    for &c in text {
        if c == ':' {
            if lengths[part] == 0 || part == 2 {
                return;
            }
            part += 1;
        } else {
            let Some(d) = number::digit(c) else { return };
            if lengths[part] == 0 {
                zeros[part] = d == 0;
            }
            lengths[part] += 1;
            values[part] = values[part].saturating_mul(10).saturating_add(d as u32);
        }
    }
    if part == 0 || lengths[part] == 0 {
        return;
    }
    if part == 1 && lengths[0] <= 6 && lengths[1] <= 6 {
        let leading =
            (if zeros[0] && lengths[0] > 1 { 1 } else { 0 }) + (if zeros[1] && lengths[1] > 1 { 1 } else { 0 });
        out.push(Candidate { end, cost: 1_130_000 + leading * 500_000, rank: 1, tag: String::new() });
    }
    if lengths[0] > 2 || values[0] > 23 || lengths[1] != 2 || values[1] > 59 {
        return;
    }
    if part == 1 {
        if values[1] == 0 {
            out.push(Candidate { end, cost: 1_220_000, rank: 6, tag: String::new() });
        }
        out.push(Candidate { end, cost: 1_320_000, rank: 6, tag: String::new() });
    } else if lengths[2] <= 2 && values[2] <= 59 {
        out.push(Candidate { end, cost: 1_020_000, rank: 6, tag: String::new() });
    }
}
fn clock_field(out: &mut String, label: &str, digits: &str, table: &crate::text::Table) {
    let mut bytes = [0u8; 2];
    let mut len = 0;
    for c in digits.chars() {
        bytes[len] = b'0' + number::digit(c).expect("recognized clock digit");
        len += 1;
    }
    let key = std::str::from_utf8(&bytes[..len]).expect("ASCII digits");
    let row = table.exact(key).next().expect("recognized clock field");
    out.push_str(label);
    out.push_str(": \"");
    out.push_str(&row.value);
    out.push('"');
}
/// Cardinal-unit paths need only the numeric shape, unit row and path weight
/// during exploration; the attributed spelling is produced for the winner.
fn simple_measure(s: &str, end: usize, out: &mut Vec<Candidate>) -> bool {
    let unsigned = s.strip_prefix('-').or_else(|| s.strip_prefix('−')).unwrap_or(s);
    let digit_end =
        unsigned.char_indices().find(|(_, c)| number::digit(*c).is_none()).map(|(i, _)| i).unwrap_or(unsigned.len());
    if digit_end == 0 || digit_end == unsigned.len() {
        return false;
    }
    let digits = &unsigned[..digit_end];
    let unit = &unsigned[digit_end..];
    let unit = unit.strip_prefix(' ').unwrap_or(unit);
    let count = digits.chars().count();
    let first = number::digit(digits.chars().next().unwrap()).unwrap();
    if count > 18 && first != 0 {
        return false;
    }
    let extra = if first == 0 && count > 1 { 500_000 } else { 0 };
    let is_decade = unsigned.len() == s.len()
        && count == 4
        && unit == "s"
        && matches!(first, 1 | 2)
        && digits.chars().last().and_then(number::digit) == Some(0);
    let mut found = false;
    for row in amount::UNITS.exact(unit) {
        found = true;
        if is_decade {
            continue;
        }
        out.push(Candidate { end, cost: 1_080_000 + extra + row.cost, rank: 7, tag: String::new() });
        if unit == "km/h" {
            out.push(Candidate { end, cost: 1_070_000 + extra, rank: 6, tag: String::new() });
        }
    }
    found
}

struct Union {
    word_punct: Punctuation,
}
impl Classifier for Union {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if start >= text.len()
            || is_nemo_space(text[start])
            || (start > 0 && !is_nemo_space(text[start - 1]) && !self.word_punct.is_mark(text[start - 1]))
        {
            return;
        }
        let mut s = String::new();
        let mut all_digits = true;
        let mut clock_chars = number::digit(text[start]).is_some();
        let mut first_space = None;
        let (mut word_live, mut core, mut segment, mut segment_ml, mut valid, mut runs) =
            (true, false, false, true, true, 0i64);
        for end in start + 1..=text.len() {
            let c = text[end - 1];
            if is_nemo_space(c) && c != ' ' {
                break;
            }
            if c == ' ' && first_space.is_none() {
                if ml_char(text[start]) && !word_can_continue(&s) {
                    break;
                }
                first_space = Some(s.len());
            }
            s.push(c);
            if let Some(at) = first_space {
                let at_boundary =
                    c == ' ' || end == text.len() || is_nemo_space(text[end]) || self.word_punct.is_mark(text[end]);
                if at_boundary {
                    let tail = &s[at + 1..];
                    if number::digit(text[start]).is_some() && tail.chars().next().is_some_and(ml_char) {
                        if s[..at].contains(':') || !numeric_tail_can_continue(tail) {
                            break;
                        }
                    }
                }
            }
            all_digits &= number::digit(c).is_some();
            clock_chars &= number::digit(c).is_some() || c == ':';
            if word_live {
                if is_nemo_space(c) {
                    word_live = false;
                } else if self.word_punct.is_mark(c) || matches!(c, '‰' | '§' | '¶') {
                    if segment && segment_ml {
                        runs += 1;
                    }
                    segment = false;
                    segment_ml = true;
                } else if number::digit(c).is_some()
                    || matches!(c, '$' | '€' | '£' | '¥' | '₹' | '%' | '‰' | '°' | '℃' | '℉')
                {
                    valid = false;
                } else if valid {
                    core = true;
                    segment = true;
                    segment_ml &= ml_char(c);
                }
            }
            if end < text.len() && !is_nemo_space(text[end]) && !self.word_punct.is_mark(text[end]) {
                continue;
            }
            if all_digits {
                let len = end - start;
                let first = number::digit(text[start]).unwrap();
                if first == 0 && len > 1 {
                    out.push(Candidate { end, cost: 1_600_000, rank: 0, tag: String::new() });
                } else if len <= 18 {
                    out.push(Candidate { end, cost: 1_100_000, rank: 0, tag: String::new() });
                }
                if (first >= 6 && len == 10) || (first == 0 && (8..=13).contains(&len)) {
                    out.push(Candidate { end, cost: 1_100_000, rank: -1, tag: String::new() });
                }
            } else if clock_chars {
                digit_clock(&text[start..end], end, out);
            } else {
                let mut readings = Vec::new();
                let simple = simple_measure(&s, end, out);
                all(&s, simple, &mut readings);
                for (tag, cost, rank) in readings {
                    out.push(Candidate { end, cost, rank, tag });
                }
            }
            if word_live {
                if core && valid {
                    let n = runs + if segment && segment_ml { 1 } else { 0 };
                    out.push(Candidate { end, cost: 100_000_000 - n * 100, rank: 13, tag: String::new() });
                }
                out.push(Candidate { end, cost: 200_000_000, rank: 14, tag: String::new() });
            }
            for (prefix, spoken) in [("~", "ഏകദേശം"), ("±", "പ്ലസ് ഓർ മൈനസ്")] {
                if let Some(rest) = s.strip_prefix(prefix) {
                    let rest = rest.strip_prefix(' ').unwrap_or(rest);
                    if !rest.is_empty() {
                        let mut nested = Vec::new();
                        all(rest, false, &mut nested);
                        word(rest, &self.word_punct, &mut nested);
                        for (tag, cost, rank) in nested {
                            out.push(Candidate {
                                end,
                                cost,
                                rank,
                                tag: format!("name: \"{}\" }} tokens {{ {tag}", space_to_nbsp(spoken)),
                            });
                        }
                    }
                }
            }
        }
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        if !candidate.tag.is_empty() {
            return;
        }
        if matches!(candidate.rank, 13 | 14) {
            let mut tag =
                String::with_capacity(8 + text[start..candidate.end].iter().map(|c| c.len_utf8()).sum::<usize>());
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
            13 | 14 => unreachable!("word candidates are handled above"),
            0 => {
                let (number, _) = number::final_cardinal(&s).expect("recognized cardinal");
                format!("cardinal {{ integer: \"{number}\" }}")
            }
            1 => {
                let (left, right) = s.split_once(':').expect("recognized ratio");
                let (mut left, _) = number::final_cardinal(left).expect("recognized ratio numerator");
                let (right, _) = number::final_cardinal(right).expect("recognized ratio denominator");
                if left.ends_with('്') {
                    left.pop();
                    left.push('േ');
                }
                let mut tag = String::with_capacity(left.len() + right.len() + 12);
                tag.push_str("name: \"");
                for c in left.chars().chain(std::iter::once(' ')).chain(right.chars()) {
                    tag.push(if c == ' ' { '\u{a0}' } else { c });
                }
                tag.push('"');
                tag
            }
            6 if s.contains(':') => {
                let mut parts = s.split(':');
                let mut tag = String::from("time { ");
                clock_field(&mut tag, "hours", parts.next().unwrap(), &datetime::HOURS);
                let minutes = parts.next().unwrap();
                if candidate.cost != 1_220_000 {
                    tag.push(' ');
                    clock_field(&mut tag, "minutes", minutes, &datetime::MINUTES);
                    if let Some(seconds) = parts.next() {
                        tag.push(' ');
                        clock_field(&mut tag, "seconds", seconds, &datetime::SECONDS);
                    }
                }
                tag.push_str(" }");
                tag
            }
            6 | 7 => {
                let (unsigned, sign) = numeric::sign(&s);
                let split = unsigned
                    .char_indices()
                    .find(|(_, c)| number::digit(*c).is_none())
                    .map(|(i, _)| i)
                    .expect("recognized unit");
                let digits = &unsigned[..split];
                let unit = unsigned[split..].strip_prefix(' ').unwrap_or(&unsigned[split..]);
                let (words, extra) = number::attributive(digits).expect("recognized measure number");
                if candidate.rank == 6 {
                    format!(
                        "measure {{ morphosyntactic_features: \"മണിക്കൂറിൽ\" cardinal {{ {sign}integer: \"{words}\" }} units: \"കിലോമീറ്റർ\" preserve_order: true }}"
                    )
                } else {
                    let row = amount::UNITS
                        .exact(unit)
                        .find(|r| 1_080_000 + extra + r.cost == candidate.cost)
                        .expect("recognized unit");
                    format!(
                        "measure {{ cardinal {{ {sign}integer: \"{words}\" }} units: \"{}\" preserve_order: true }}",
                        row.value
                    )
                }
            }
            -1 => {
                let words = number::digitwise(&s).expect("recognized telephone");
                format!("telephone {{ number_part: \"{words} \" }}")
            }
            _ => unreachable!("unexpected lazy Malayalam candidate"),
        };
    }
}
static GRAMMAR: LazyLock<Ml> = LazyLock::new(|| {
    let make = || Punctuation::new("!#%&'()*+,-./:;<=>?@^_`{|}~\"", true, [], cost(1.1));
    Ml { classifiers: vec![Box::new(Union { word_punct: make() })], punct: MlPunctuation(make()) }
});
pub fn grammar() -> &'static dyn Grammar {
    &*GRAMMAR
}
