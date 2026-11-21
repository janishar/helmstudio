//! Ukrainian text normalization, ported from the production NeMo grammars.
mod cardinal;
mod date;
mod decimal;
mod electronic;
mod fraction;
mod measure;
mod money;
mod ordinal;
mod roman;
mod telephone;
mod time;
mod whitelist;

use crate::lang::common::{self, Punctuation};
use crate::lattice::{Candidate, Classifier, Shape};
use crate::pipeline::Grammar;
use crate::tag::Dict;
use crate::text::cost;
use std::sync::LazyLock;

struct Ukrainian {
    classifiers: Vec<Box<dyn Classifier>>,
    punct: Punctuation,
}
impl Grammar for Ukrainian {
    fn code(&self) -> &'static str {
        "uk"
    }
    fn shape(&self) -> Shape {
        Shape::Separated
    }
    fn verbalize(&self, token: &Dict) -> Option<String> {
        if let Some(w) = common::verbalize_name(token) {
            return Some(w);
        }
        let (name, value) = common::class(token)?;
        let crate::tag::Value::Dict(fields) = value else { return None };
        if name != "date" {
            return None;
        }
        let mut fields = common::Fields::new(fields);
        let day = fields.take("day")?;
        let month = fields.take("month")?;
        let year = fields.take("year")?;
        if !fields.done() || day.is_empty() || month.is_empty() || year.is_empty() {
            return None;
        }
        Some(format!("{day} {month} {year}").replace('\u{a0}', " "))
    }
    fn punct(&self) -> Option<&dyn Classifier> {
        Some(&self.punct)
    }
    fn classifiers(&self) -> &[Box<dyn Classifier>] {
        &self.classifiers
    }
}
static GRAMMAR: LazyLock<Ukrainian> =
    LazyLock::new(|| Ukrainian { classifiers: vec![Box::new(Union)], punct: Punctuation::en(cost(1.1)) });
pub fn grammar() -> &'static dyn Grammar {
    &*GRAMMAR
}

// Costs are integer hundredths; all source class and branch weights are exact at this precision.
fn choices(s: &str) -> Vec<(String, i32, i32)> {
    let mut result = Vec::new();
    let has_digit = s.bytes().any(|b| b.is_ascii_digit());
    let has_dot = s.contains('.');
    let has_space = s.contains(' ');
    for (w, c) in whitelist::whitelist(s) {
        result.push((w, 101 + c, 0));
    }
    if (has_space || matches!(s, "XL" | "CD" | "MIX"))
        && let Some((w, c)) = roman::roman(s)
    {
        result.push((w, 102 + c, 1));
    }
    if has_dot || s.contains('@') || s.starts_with("http://") || s.starts_with("https://") {
        for (w, c) in electronic::electronic(s) {
            result.push((w, 103 + c, 2));
        }
    }
    if has_digit && (has_space || s.contains(['+', '-', '(', ')', '.'])) {
        for (w, c) in telephone::telephone(s) {
            result.push((w, 104 + c, 3));
        }
    }
    if has_digit && let Some((w, c)) = date::date(s) {
        result.push((w, 104 + c, 4));
    }
    if has_digit
        && s.contains(':')
        && let Some((w, c)) = time::time(s)
    {
        result.push((w, 105 + c, 5));
    }
    if has_digit && let Some((w, c)) = money::money(s) {
        result.push((w, 105 + c, 6));
    }
    if (has_digit || matches!(s, "°" | "%" | "‰"))
        && let Some((w, c)) = measure::measure(s)
    {
        result.push((w, 106 + c, 7));
    }
    if (has_digit || s.chars().any(|c| "½⅓⅔¼¾⅕⅖⅗⅘⅙⅚⅛⅜⅝⅞⅒".contains(c)))
        && let Some(w) = fraction::fraction(s)
    {
        result.push((w, 107, 8));
    }
    if has_digit && (has_dot || s.contains(',')) {
        for (w, c) in decimal::decimal(s) {
            result.push((w, 107 + c, 9));
        }
    }
    if has_digit
        && s.contains(['-', '–', 's'])
        && let Some(w) = ordinal::tagged(s)
    {
        result.push((w.clone(), 108, 10));
        if s.starts_with("2000-") || s.starts_with("2000–") {
            result.push((w.replacen("двотисяч", "двохтисяч", 1), 108, 10));
        }
    }
    if has_digit || s.contains(['²', '³']) {
        for (w, c) in cardinal::classify(s) {
            result.push((w, 110 + c, 11));
        }
    }
    if !s.is_empty() && !s.chars().any(crate::text::is_nemo_space) {
        result.push((s.to_owned(), 10000, 12));
    }
    result
}
fn outputs(s: &str) -> Vec<(String, i32, i32)> {
    let mut out = choices(s);
    for (sign, spoken) in [('~', "приблизно"), ('±', "плюс-мінус")] {
        if let Some(rest) = s.strip_prefix(sign) {
            let rest = rest.strip_prefix(' ').unwrap_or(rest);
            for (w, c, r) in choices(rest) {
                out.push((format!("{spoken}\u{1f}{w}"), c - 200, r + 20));
            }
        }
    }
    out
}
// A numeric-led grammar can cross whitespace only into one of its written
// month, unit, currency, timezone, scale, era, or year forms (or digits).
fn numeric_continuation(s: &str) -> bool {
    const WORDS: &[&str] = &[
        "січня",
        "лютого",
        "березня",
        "квітня",
        "травня",
        "червня",
        "липня",
        "серпня",
        "вересня",
        "жовтня",
        "листопада",
        "грудня",
        "до",
        "н.",
        "е.",
        "BC",
        "B.C.",
        "AD",
        "A.D.",
        "UTC",
        "GMT",
        "EST",
        "PST",
        "млн",
        "млрд",
        "тис",
        "трлн",
        "р",
        "г",
        "року",
        "года",
    ];
    static INDEX: LazyLock<(std::collections::HashSet<&'static str>, std::collections::HashSet<&'static str>)> =
        LazyLock::new(|| {
            let mut prefixes = std::collections::HashSet::new();
            let mut complete = std::collections::HashSet::new();
            for word in WORDS.iter().copied().chain(measure::unit_names()).chain(money::currency_names()) {
                complete.insert(word);
                prefixes.insert(word);
                for (end, _) in word.char_indices().skip(1) {
                    prefixes.insert(&word[..end]);
                }
            }
            (prefixes, complete)
        });
    INDEX.0.contains(s) || s.char_indices().skip(1).any(|(end, _)| INDEX.1.contains(&s[..end]))
}

struct Union;
impl Classifier for Union {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if !common::possible_token_start(text, start) {
            return;
        }
        let first_end =
            text[start..].iter().position(|&c| crate::text::is_nemo_space(c)).map_or(text.len(), |n| start + n);
        let first = &text[start..first_end];
        let cyrillic_word = first.first().is_some_and(|c| !c.is_ascii() && c.is_alphabetic())
            && first.iter().all(|c| c.is_alphabetic());
        let contextual = [
            "і",
            "Глава",
            "глава",
            "Частина",
            "частина",
            "Розділ",
            "розділ",
            "Том",
            "том",
            "Петро",
            "Володимир",
            "Карл",
            "Людовик",
            "в",
            "у",
            "В",
            "У",
            "около",
            "близько",
            "від",
            "от",
        ]
        .iter()
        .any(|w| w.chars().eq(first.iter().copied()));
        if cyrillic_word && !contextual && !whitelist::plain_match_chars(first) {
            out.push(Candidate { end: first_end, cost: cost(100.0), rank: 12000, tag: String::new() });
            return;
        }
        let limit = if cyrillic_word && !contextual { first_end } else { text.len() };
        let mut s = String::new();
        let mut only_letters = true;
        let mut only_digits = true;
        let mut trailing_word = None;
        let numeric_start = text[start].is_ascii_digit();
        for end in start + 1..=limit {
            let ch = text[end - 1];
            s.push(ch);
            if crate::text::is_nemo_space(ch) {
                trailing_word = Some(s.len());
            } else if numeric_start && let Some(at) = trailing_word {
                let word = &s[at..];
                if word.chars().next().is_some_and(char::is_alphabetic) && !numeric_continuation(word) {
                    break;
                }
            }
            only_letters &= ch.is_alphabetic();
            only_digits &= ch.is_ascii_digit();
            if !common::possible_token_end(text, end) {
                continue;
            }
            if only_letters && !whitelist::plain_match(&s) {
                out.push(Candidate { end, cost: cost(100.0), rank: 12000, tag: String::new() });
                continue;
            }
            if only_digits && s.len() <= 15 && (s.len() == 1 || !s.starts_with('0')) {
                out.push(Candidate { end, cost: cost(1.2), rank: 11000, tag: String::new() });
                out.push(Candidate { end, cost: cost(100.0), rank: 12001, tag: String::new() });
                continue;
            }
            if s.contains(':') && s.bytes().all(|b| b.is_ascii_digit() || b == b':') {
                let mut idx = 0;
                if let Some(weight) = time::simple_weight(&s) {
                    out.push(Candidate {
                        end,
                        cost: (105 + weight) as i64 * 10_000,
                        rank: 5000 + idx,
                        tag: String::new(),
                    });
                    idx += 1;
                }
                if let Some((left, right)) = s.split_once(':') {
                    if cardinal::valid_number(left) && cardinal::valid_number(right) {
                        out.push(Candidate { end, cost: cost(1.3), rank: 11000 + idx, tag: String::new() });
                        idx += 1;
                    }
                }
                out.push(Candidate { end, cost: cost(100.0), rank: 12000 + idx, tag: String::new() });
                continue;
            }
            if s.contains(' ') {
                if let Some(weight) = money::simple_weight(&s) {
                    out.push(Candidate { end, cost: (105 + weight) as i64 * 10_000, rank: 6000, tag: String::new() });
                    continue;
                }
                if measure::simple(&s) {
                    out.push(Candidate { end, cost: cost(1.16), rank: 7000, tag: String::new() });
                    continue;
                }
            }
            for (idx, (_, c, r)) in outputs(&s).into_iter().enumerate() {
                out.push(Candidate { end, cost: c as i64 * 10_000, rank: r * 1000 + idx as i32, tag: String::new() });
            }
        }
    }
    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        // A word candidate copies its accepted characters unchanged. Writing
        // directly avoids constructing, cloning, and replacing an intermediate.
        if candidate.rank / 1000 == 12 {
            let mut tag = String::with_capacity((candidate.end - start) * 3 + 8);
            tag.push_str("name: \"");
            tag.extend(text[start..candidate.end].iter().copied());
            tag.push('"');
            candidate.tag = tag;
            return;
        }
        let s: String = text[start..candidate.end].iter().collect();
        let direct = if candidate.rank == 11000 && cardinal::valid_number(&s) {
            cardinal::basic(&s).map(|(w, _)| w)
        } else if candidate.rank == 5000 {
            time::render_simple(&s)
        } else if candidate.rank == 6000 {
            money::render_simple(&s)
        } else if candidate.rank == 7000 {
            measure::render_simple(&s)
        } else {
            None
        };
        if let Some(w) = direct {
            let mut tag = String::with_capacity(w.len() + 16);
            tag.push_str("name: \"");
            for c in w.chars() {
                tag.push(if c == ' ' { '\u{a0}' } else { c });
            }
            tag.push('"');
            candidate.tag = tag;
            return;
        }
        let idx = (candidate.rank % 1000) as usize;
        if let Some((w, _, _)) = outputs(&s).into_iter().nth(idx) {
            candidate.tag = if let Some((a, b)) = w.split_once('\u{1f}') {
                format!("name: \"{}\" }} tokens {{ name: \"{}\"", a.replace(' ', "\u{a0}"), b.replace(' ', "\u{a0}"))
            } else {
                format!("name: \"{}\"", w.replace(' ', "\u{a0}"))
            };
        }
    }
}
