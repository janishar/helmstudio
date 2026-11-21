//! Native Kannada text-normalization grammar.
mod amount;
mod datetime;
mod graph_utils;
mod numeric;
mod other;
mod verbalize;

use crate::lang::common::Punctuation;
use crate::lattice::{Candidate, Classifier, Shape};
use crate::pipeline::Grammar;
use crate::tag::Dict;
use crate::text::{cost, is_nemo_space};
use std::collections::HashMap;
use std::sync::LazyLock;

struct Kn {
    classifiers: Vec<Box<dyn Classifier>>,
    punct: KnPunctuation,
}
impl Grammar for Kn {
    fn code(&self) -> &'static str {
        "kn"
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
}
struct KnPunctuation(Punctuation);
impl Classifier for KnPunctuation {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        let first = out.len();
        self.0.candidates(text, start, out);
        let mut i = first;
        while i < out.len() {
            let span = &text[start..out[i].end];
            let emphasis = span.first() == Some(&'<') && span.last() == Some(&'>');
            if !emphasis && span.len() > 1 && span.iter().any(|c| "%‰~&§¶".contains(*c)) {
                out.remove(i);
            } else {
                i += 1;
            }
        }
    }
}
/// A token in this grammar can only touch another token through a punctuation
/// item or whitespace. In particular, Kannada vowel signs are not boundaries.
fn boundary(c: char) -> bool {
    if c.is_ascii() {
        return is_nemo_space(c) || (c.is_ascii_punctuation() && c != '$');
    }
    // The frozen Unicode punctuation table contains only U+0C84 in the
    // Kannada block; vowel signs and virama cannot separate tokens.
    if ('\u{0c80}'..='\u{0cff}').contains(&c) {
        return c == '\u{0c84}';
    }
    is_nemo_space(c) || GRAMMAR.punct.0.is_mark(c)
}

static MONTH_INITIALS: LazyLock<Vec<char>> = LazyLock::new(|| {
    include_str!("../../../data/kn/date/month_names.tsv").lines().filter_map(|line| line.chars().next()).collect()
});
static CURRENCY_INITIALS: LazyLock<Vec<char>> = LazyLock::new(|| {
    include_str!("../../../data/kn/money/currency.tsv").lines().filter_map(|line| line.chars().next()).collect()
});

/// Initial characters permitted in a second word after a numeric-class prefix.
/// The lexical initials come from the same input tables as the taggers; the
/// remaining characters are numeric, fraction, range, clock and rate syntax.
static NUMERIC_SECOND_INITIALS: LazyLock<Vec<char>> = LazyLock::new(|| {
    let mut chars = Vec::new();
    for data in [
        include_str!("../../../data/kn/date/month_names.tsv"),
        include_str!("../../../data/kn/date/year_suffix.tsv"),
        include_str!("../../../data/kn/numbers/quantities.tsv"),
        include_str!("../../../data/kn/money/currency.tsv"),
        include_str!("../../../data/kn/measure/unit_one.tsv"),
        include_str!("../../../data/kn/measure/unit_other.tsv"),
        include_str!("../../../data/kn/measure/unit_prefixed_one.tsv"),
        include_str!("../../../data/kn/measure/unit_prefixed_other.tsv"),
        include_str!("../../../data/kn/telephone/credit_context.tsv"),
        include_str!("../../../data/kn/telephone/pincode_context.tsv"),
    ] {
        for line in data.lines() {
            let key = line.split('\t').next().unwrap_or("");
            for word in key.split(' ') {
                if let Some(c) = word.chars().next() {
                    chars.push(c);
                }
            }
        }
    }
    chars.extend("aApPUGIE-–—/½¼¾⅓⅔⅕⅖⅗⅘⅙⅚⅛⅜⅝⅞⅒".chars());
    chars.sort_unstable();
    chars.dedup();
    chars
});
type ClockTable = HashMap<&'static str, &'static str>;
static CLOCK_TABLES: LazyLock<[ClockTable; 3]> = LazyLock::new(|| {
    [
        include_str!("../../../data/kn/time/hours.tsv"),
        include_str!("../../../data/kn/time/minutes.tsv"),
        include_str!("../../../data/kn/time/seconds.tsv"),
    ]
    .map(|data| data.lines().filter_map(|line| line.split_once('\t')).collect())
});

fn clock_field(chars: &[char], table: &ClockTable) -> Option<&'static str> {
    let mut bytes = [0u8; 2];
    if chars.is_empty() || chars.len() > bytes.len() {
        return None;
    }
    for (i, &c) in chars.iter().enumerate() {
        bytes[i] = b'0' + graph_utils::digit(c)?;
    }
    table.get(std::str::from_utf8(&bytes[..chars.len()]).ok()?).copied()
}

fn write_clock(chars: &[char], rank: i32) -> Option<String> {
    let mut fields = chars.split(|&c| c == ':');
    let h = clock_field(fields.next()?, &CLOCK_TABLES[0])?;
    let min_chars = fields.next()?;
    let m = clock_field(min_chars, &CLOCK_TABLES[1])?;
    let sec = match fields.next() {
        Some(chars) => Some(clock_field(chars, &CLOCK_TABLES[2])?),
        None => None,
    };
    if fields.next().is_some() {
        return None;
    }
    let zero = min_chars == ['0', '0'] || min_chars == ['೦', '೦'];
    let mut out = String::with_capacity(h.len() + m.len() + sec.map_or(0, str::len) + 65);
    out.push_str("time { hours: \"");
    out.push_str(h);
    out.push('"');
    if sec.is_some() || !zero || rank == 1 {
        out.push_str(" minutes: \"");
        out.push_str(m);
        out.push('"');
    }
    if let Some(sec) = sec {
        out.push_str(" seconds: \"");
        out.push_str(sec);
        out.push('"');
    }
    out.push_str(" }");
    Some(out)
}

/// For a bare digit/colon span, the only accepted semiotic class is a clock.
/// Return whether the two-field zero-minute alternative is also present.
fn plain_clock(s: &str) -> Option<bool> {
    let mut fields = s.split(':');
    let h = fields.next()?;
    let m = fields.next()?;
    let sec = fields.next();
    if fields.next().is_some() {
        return None;
    }
    fn number(s: &str, max: u8) -> Option<u8> {
        let mut n = 0u8;
        let mut count = 0;
        for c in s.chars() {
            n = n.checked_mul(10)?.checked_add(graph_utils::digit(c)?)?;
            count += 1;
            if count > 2 {
                return None;
            }
        }
        (count > 0 && n <= max).then_some(n)
    }
    number(h, 24)?;
    number(m, 59)?;
    if let Some(sec) = sec {
        number(sec, 59)?;
    }
    Some(sec.is_none() && (m == "00" || m == "೦೦"))
}

fn may_be_datetime(s: &str, first: char) -> bool {
    let Some((head, rest)) = s.split_once(' ') else {
        return graph_utils::digit(first).is_some();
    };
    if graph_utils::digit(first).is_none() {
        return graph_utils::table(include_str!("../../../data/kn/date/month_names.tsv"), head).is_some();
    }
    let Some(next) = rest.trim_start_matches(' ').chars().next() else { return false };
    MONTH_INITIALS.contains(&next) || "ಕಸCABaApPUGIE-–—".contains(next)
}

struct Classify;
impl Classifier for Classify {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if start > 0 && !boundary(text[start - 1]) {
            return;
        }
        let first = text[start];
        // A run made exclusively of Kannada graph characters has no internal
        // punctuation boundary. If no source lexical/context key can start
        // there, the word graph is its only possible classified reading.
        let native_word_char = |c: char| {
            (('\u{0c80}'..='\u{0cff}').contains(&c) && c != '\u{0c84}' && graph_utils::digit(c).is_none())
                || matches!(c, '\u{200c}' | '\u{200d}')
        };
        if native_word_char(first) {
            let mut end = start + 1;
            while end < text.len() && native_word_char(text[end]) {
                end += 1;
            }
            let mut finish = end;
            while finish < text.len() && !is_nemo_space(text[finish]) && boundary(text[finish]) {
                finish += 1;
            }
            if finish == text.len() || is_nemo_space(text[finish]) {
                let lexical = (end..=finish)
                    .any(|stop| other::might_classify_word(&text[start..stop], text.get(stop) == Some(&' ')));
                if !lexical {
                    out.push(Candidate { end, cost: 99_999_900, rank: 100, tag: String::new() });
                    let mut ordinary = true;
                    let mut kannada = true;
                    for i in end..finish {
                        let c = text[i];
                        if c == '"' {
                            break;
                        }
                        if "₹$€£¥%‰°&~±@#§¶©®™^²³½¼¾⅓⅔⅕⅖⅗⅘⅙⅚⅛⅜⅝⅞".contains(c)
                        {
                            ordinary = false;
                        }
                        if !(('\u{0c80}'..='\u{0cff}').contains(&c) && graph_utils::digit(c).is_none()
                            || matches!(c, '\u{200c}' | '\u{200d}'))
                        {
                            kannada = false;
                        }
                        if i + 1 == text.len() || boundary(text[i + 1]) {
                            out.push(Candidate {
                                end: i + 1,
                                cost: if ordinary { 100_000_000 - if kannada { 100 } else { 0 } } else { 200_000_000 },
                                rank: 100,
                                tag: String::new(),
                            });
                        }
                    }
                    return;
                }
            }
        }
        let numeric_start = graph_utils::digit(first).is_some() || "-−©§½¼¾⅓⅔⅕⅖⅗⅘⅙⅚⅛⅜⅝⅞⅒".contains(first);
        let date_start = graph_utils::digit(first).is_some() || MONTH_INITIALS.contains(&first);
        let amount_start =
            graph_utils::digit(first).is_some() || "-−½¼¾".contains(first) || CURRENCY_INITIALS.contains(&first);
        let capacity: usize = text[start..].iter().take_while(|&&c| !is_nemo_space(c)).map(|c| c.len_utf8()).sum();
        let mut span = String::with_capacity(capacity);
        let mut has_digit = false;
        let mut bare = true;
        let mut first_digit = None;
        let mut digit_count = 0usize;
        let mut clock_alphabet = true;
        let mut has_colon = false;
        let mut found = Vec::new();
        let mut crossed_space = false;
        let mut checked_second = false;
        let mut other_cross = false;
        for end in start..text.len() {
            let c = text[end];
            if c == ' ' && !crossed_space {
                if first.is_ascii_alphabetic() {
                    other_cross = other::can_cross_space(&span);
                }
                crossed_space = true;
                let month = date_start
                    && graph_utils::digit(first).is_none()
                    && graph_utils::table(include_str!("../../../data/kn/date/month_names.tsv"), &span).is_some();
                if !numeric_start
                    && !amount_start
                    && !matches!(first, '~' | '±')
                    && !month
                    && !other::can_cross_space(&span)
                {
                    break;
                }
            }
            if crossed_space && !checked_second && c != ' ' {
                checked_second = true;
                if (numeric_start || amount_start)
                    && !other_cross
                    && graph_utils::digit(c).is_none()
                    && NUMERIC_SECOND_INITIALS.binary_search(&c).is_err()
                {
                    break;
                }
            }
            span.push(c);
            let digit = graph_utils::digit(c);
            has_digit |= digit.is_some();
            if let Some(d) = digit {
                first_digit.get_or_insert(d);
                digit_count += 1;
            } else if end != start || !matches!(c, '-' | '−') {
                bare = false;
            }
            clock_alphabet &= digit.is_some() || c == ':';
            has_colon |= c == ':';
            if matches!(c, '\n' | '\r' | '\t' | '\u{a0}') {
                break;
            }
            if end + 1 < text.len() && !boundary(text[end + 1]) {
                continue;
            }
            if has_colon && clock_alphabet {
                if let Some(zero) = plain_clock(&span) {
                    if zero {
                        out.push(Candidate { end: end + 1, cost: 1_100_000, rank: 1, tag: String::new() });
                    }
                    out.push(Candidate { end: end + 1, cost: 1_100_000, rank: 0, tag: String::new() });
                }
                continue;
            }
            if bare && digit_count != 0 {
                let zero = first_digit == Some(0);
                if zero || digit_count <= 18 {
                    let w = if zero && digit_count > 1 { 500_000 } else { 0 };
                    out.push(Candidate { end: end + 1, cost: 1_100_000 + w, rank: 0, tag: String::new() });
                }
                if digit_count >= 10 && graph_utils::digit(first).is_some() {
                    found.clear();
                    other::analyze(&span, &mut found);
                    for (weight, rank, tag) in found.drain(..) {
                        out.push(Candidate { end: end + 1, cost: weight, rank, tag });
                    }
                }
                continue;
            }
            found.clear();
            if numeric_start && !has_colon {
                numeric::analyze(&span, &mut found);
            }
            if date_start && has_digit && may_be_datetime(&span, first) {
                datetime::analyze(&span, &mut found);
            }
            if amount_start && !has_colon && (has_digit || "½¼¾".contains(first)) {
                amount::analyze(&span, &mut found);
            }
            other::analyze(&span, &mut found);
            for (weight, rank, tag) in found.drain(..) {
                out.push(Candidate { end: end + 1, cost: weight, rank, tag });
            }
            if let Some((rest, modifier)) = span
                .strip_prefix('~')
                .map(|v| (v, "ಸುಮಾರು"))
                .or_else(|| span.strip_prefix('±').map(|v| (v, "ಪ್ಲಸ್\u{a0}ಮೈನಸ್")))
            {
                let rest = rest.strip_prefix(' ').unwrap_or(rest);
                let mut numeric = Vec::new();
                numeric::analyze(rest, &mut numeric);
                amount::analyze(rest, &mut numeric);
                for (weight, rank, tag) in numeric {
                    if !tag.starts_with("name:") && !tag.starts_with("ordinal") {
                        out.push(Candidate {
                            end: end + 1,
                            cost: weight - 2_000_000,
                            rank,
                            tag: format!("name: \"{modifier}\" }} tokens {{ {tag}"),
                        });
                    }
                }
            }
        }
        // The source word graph accepts punctuation internally and has a higher-cost unrestricted fallback.
        let mut quoted = false;
        let mut ordinary = true;
        let mut kannada = true;
        for end in start..text.len() {
            let c = text[end];
            if is_nemo_space(c) {
                break;
            }
            quoted |= c == '"';
            if "₹$€£¥%‰°&~±@#§¶©®™^²³½¼¾⅓⅔⅕⅖⅗⅘⅙⅚⅛⅜⅝⅞".contains(c) {
                ordinary = false;
            }
            if !((0x0c80..=0x0cff).contains(&(c as u32)) && graph_utils::digit(c).is_none()
                || c == '\u{200c}'
                || c == '\u{200d}')
            {
                kannada = false;
            }
            if !quoted && (end + 1 == text.len() || boundary(text[end + 1])) {
                out.push(Candidate {
                    end: end + 1,
                    cost: if ordinary { 100_000_000 - if kannada { 100 } else { 0 } } else { 200_000_000 },
                    rank: 100,
                    tag: String::new(),
                });
            }
        }
    }
    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        if !candidate.tag.is_empty() {
            return;
        }
        let chars = &text[start..candidate.end];
        if candidate.rank == 100 {
            let mut tag = String::with_capacity(chars.iter().map(|c| c.len_utf8()).sum::<usize>() + 8);
            tag.push_str("name: \"");
            tag.extend(chars.iter().copied());
            tag.push('"');
            candidate.tag = tag;
        } else if chars.contains(&':') {
            candidate.tag = write_clock(chars, candidate.rank).expect("recognized clock");
        } else {
            let mut tag = String::with_capacity(chars.len() * 12 + 32);
            tag.push_str("cardinal { ");
            graph_utils::append_plain_cardinal(&mut tag, chars);
            tag.push_str(" }");
            candidate.tag = tag;
        }
    }
}
static GRAMMAR: LazyLock<Kn> = LazyLock::new(|| Kn {
    classifiers: vec![Box::new(Classify)],
    punct: KnPunctuation(Punctuation::new("!#%&'()*+,-./:;<=>?@^_`{|}~\"", true, [], cost(2.1))),
});
pub fn grammar() -> &'static dyn Grammar {
    &*GRAMMAR
}
