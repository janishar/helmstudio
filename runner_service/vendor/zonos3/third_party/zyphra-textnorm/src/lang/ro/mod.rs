//! Romanian text normalization, ported from the production Romanian grammar.
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
use crate::text::is_nemo_space;
use std::sync::LazyLock;

struct Ro {
    classifiers: Vec<Box<dyn Classifier>>,
    punct: Punctuation,
}
struct Union;
struct Punctuation;

fn tag_name(s: &str) -> String {
    let mut tag = String::with_capacity(s.len() + 16);
    tag.push_str("name: \"");
    for c in s.chars() {
        tag.push(if c == ' ' { '\u{a0}' } else { c });
    }
    tag.push('"');
    tag
}
fn outputs(s: &str, build: bool) -> Vec<(i32, i64, String)> {
    let mut out = Vec::new();
    let tag = |value: &str| if build { tag_name(value) } else { String::new() };
    let has_digit = s.bytes().any(|b| b.is_ascii_digit());
    let has_vulgar = s.chars().any(|c| "½¼¾⅓⅔⅕⅖⅗⅘⅙⅚⅛⅜⅝⅞⅐⅑⅒".contains(c));
    if !has_digit {
        if let Some(v) = whitelist::whitelist(s) {
            out.push((0, 1_010_000, tag(v)));
        }
    }
    if !has_digit {
        for v in roman::roman(s) {
            out.push((1, 1_020_000, tag(&v)));
        }
    }
    if has_digit && (s.contains(['/', '.', '-', '–', '—', '−', ' ', '\'']) || s.chars().any(char::is_alphabetic))
    {
        for (v, tagged) in date::date(s) {
            out.push((
                2,
                1_030_000,
                if !build {
                    String::new()
                } else if tagged {
                    v
                } else {
                    tag(&v)
                },
            ));
        }
    }
    if has_digit
        && (s.contains(':')
            || s.ends_with("am")
            || s.ends_with("pm")
            || s.ends_with("AM")
            || s.ends_with("PM")
            || s.ends_with("a.m.")
            || s.ends_with("p.m.")
            || s.ends_with("A.M.")
            || s.ends_with("P.M."))
    {
        for v in time::time(s) {
            out.push((3, 1_040_000, tag(&v)));
        }
    }
    if s.contains(['.', '@', '/']) {
        for v in electronic::electronic(s) {
            out.push((4, 1_050_000, tag(&v)));
        }
    }
    if has_digit {
        if let Some(v) = telephone::telephone(s) {
            out.push((5, 1_050_000, tag(&v)));
        }
        let has_unit = s.chars().any(|c| c.is_alphabetic() || "$€£¥₹₽₩₺₪₴₫৳₦₱฿֏¢%‰°℃℉ΩΩµμ".contains(c));
        if has_unit && !s.contains(':') && money::may_match(s) {
            for (v, w) in money::money(s) {
                out.push((6, 1_060_000 + w, tag(&v)));
            }
            for (i, c) in s.char_indices() {
                if matches!(c, '-' | '–' | '—') {
                    let a = s[..i].trim_end_matches(' ');
                    let b = s[i + c.len_utf8()..].trim_start_matches(' ');
                    for (a, wa) in money::money(a) {
                        for (b, wb) in money::money(b) {
                            out.push((
                                7,
                                1_065_000 + wa + wb,
                                if build {
                                    format!(
                                        "{} }} tokens {{ name: \"până\u{a0}la\" }} tokens {{ {}",
                                        tag_name(&a),
                                        tag_name(&b)
                                    )
                                } else {
                                    String::new()
                                },
                            ));
                        }
                    }
                }
            }
        }
        if has_unit && !s.contains(':') && measure::may_match(s) {
            for v in measure::measure(s) {
                out.push((8, 1_070_000, tag(&v)));
            }
        }
        if s.contains(['.', ',']) {
            for v in decimal::decimal(s) {
                out.push((9, 1_080_000, tag(&v)));
            }
        }
        if s.contains('/') || has_vulgar {
            for v in fraction::fraction(s) {
                out.push((10, 1_090_000, tag(&v)));
            }
        }
        if let Some(v) = ordinal::ordinal(s) {
            out.push((11, 1_100_000, tag(&v)));
        }
        for (v, w) in cardinal::cardinal(s) {
            out.push((12, 1_120_000 + w, tag(&v)));
        }
    } else if has_vulgar {
        if measure::may_match(s) {
            for v in measure::measure(s) {
                out.push((8, 1_070_000, tag(&v)));
            }
        }
        for v in fraction::fraction(s) {
            out.push((10, 1_090_000, tag(&v)));
        }
    }
    if !s.is_empty() && !s.chars().any(is_nemo_space) {
        out.push((13, 100_000_000, String::new()));
    }
    out
}
// Only these alphabetic initial words can begin a source rule crossing whitespace.
fn contextual_initial(word: &[char]) -> bool {
    const PREFIXES: &[&str] = &[
        "anii",
        "a",
        "A",
        "al",
        "Al",
        "il",
        "Il",
        "Capitolul",
        "capitolul",
        "Volumul",
        "volumul",
        "Partea",
        "partea",
        "Actul",
        "actul",
        "secolul",
        "Secolul",
        "clasa",
        "Clasa",
        "Carol",
        "Ferdinand",
        "Mihai",
        "Ioan",
        "Paul",
        "Papa",
        "Benedict",
        "Gheorghe",
        "Alexandru",
        "Petru",
        "Ludovic",
        "Henric",
        "Filip",
        "Ștefan",
        "Mircea",
        "Vlad",
        "Regele",
        "Regina",
        "Elisabeta",
    ];
    word.iter().all(|c| matches!(c, 'I' | 'V' | 'X')) || PREFIXES.iter().any(|p| p.chars().eq(word.iter().copied()))
}

impl Classifier for Union {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if !common::possible_token_start(text, start) {
            return;
        }
        let run_end = text[start..].iter().position(|&c| is_nemo_space(c)).map_or(text.len(), |n| start + n);
        let run = &text[start..run_end];
        if !run.is_empty()
            && run.iter().all(|c| c.is_alphabetic())
            && !contextual_initial(run)
            && !["Dr", "Dna", "Dl", "Dra", "Prof", "Nr", "nr", "etc"]
                .iter()
                .any(|word| word.chars().eq(run.iter().copied()))
        {
            out.push(Candidate { end: run_end, cost: 100_000_000, rank: 130_000, tag: String::new() });
            return;
        }
        let mut s = String::new();
        let mut numeric_clock = text[start].is_ascii_digit();
        let mut colon_seen = false;
        let mut clock_suffix = false;
        for end in start + 1..=text.len() {
            let ch = text[end - 1];
            if text[start].is_ascii_digit() && ch.is_alphabetic() && s.ends_with(' ') {
                let previous = s.trim_end_matches(' ').rsplit(' ').next().unwrap_or("");
                if !previous.is_empty()
                    && previous.chars().all(char::is_alphabetic)
                    && !matches!(previous, "grade" | "metri" | "kilometri")
                {
                    break;
                }
            }
            // A digit-led colon expression can contain grouped digits or a clock/zone,
            // but after a clock's alphabetic suffix no source branch consumes another space.
            if numeric_clock && colon_seen && clock_suffix && is_nemo_space(ch) {
                break;
            }
            if numeric_clock {
                if ch == ':' {
                    colon_seen = true;
                } else if colon_seen && ch.is_alphabetic() {
                    clock_suffix = true;
                } else if !colon_seen && !ch.is_ascii_digit() && !matches!(ch, '.' | ',' | ' ' | '\u{a0}') {
                    numeric_clock = false;
                }
            }
            s.push(ch);
            if !common::possible_token_end(text, end) {
                continue;
            }
            if s.bytes().all(|b| b.is_ascii_digit()) {
                let mut index = 0;
                if s.starts_with('0') && (7..=15).contains(&s.len()) {
                    out.push(Candidate { end, cost: 1_050_000, rank: 50_000 + index, tag: String::new() });
                    index += 1;
                }
                if graph_utils::parse_number(&s).is_some() || (s.len() > 1 && s.starts_with('0')) {
                    out.push(Candidate { end, cost: 2_170_000, rank: 120_000 + index, tag: String::new() });
                    index += 1;
                }
                out.push(Candidate { end, cost: 100_000_000, rank: 130_000 + index, tag: String::new() });
                continue;
            }
            if let Some((hour, minute)) = s.split_once(':') {
                if !hour.is_empty()
                    && hour.len() <= 2
                    && hour.bytes().all(|b| b.is_ascii_digit())
                    && !minute.is_empty()
                    && minute.bytes().all(|b| b.is_ascii_digit())
                {
                    let mut index = 0;
                    if minute.len() == 2
                        && hour.parse::<u8>().is_ok_and(|h| h <= 23)
                        && minute.parse::<u8>().is_ok_and(|m| m <= 59)
                    {
                        let mut readings = usize::from(s == "00:00");
                        if matches!(minute, "15" | "30") {
                            readings += 1;
                        }
                        if matches!(
                            minute,
                            "00" | "01"
                                | "02"
                                | "03"
                                | "04"
                                | "05"
                                | "10"
                                | "15"
                                | "20"
                                | "25"
                                | "30"
                                | "35"
                                | "40"
                                | "45"
                                | "50"
                                | "55"
                        ) {
                            readings += 1;
                        }
                        if graph_utils::parse_number(minute).is_some() {
                            readings += 1;
                        }
                        for _ in 0..readings {
                            out.push(Candidate { end, cost: 1_040_000, rank: 30_000 + index, tag: String::new() });
                            index += 1;
                        }
                    }
                    if graph_utils::parse_number(hour).is_some() && graph_utils::parse_number(minute).is_some() {
                        out.push(Candidate { end, cost: 2_020_000, rank: 120_000 + index, tag: String::new() });
                        index += 1;
                    }
                    out.push(Candidate { end, cost: 100_000_000, rank: 130_000 + index, tag: String::new() });
                    continue;
                }
            }
            if let Some(readings) = time::period_readings(&s) {
                for index in 0..readings {
                    out.push(Candidate { end, cost: 1_040_000, rank: 30_000 + index as i32, tag: String::new() });
                }
                if !s.chars().any(is_nemo_space) {
                    out.push(Candidate { end, cost: 100_000_000, rank: 130_000 + readings as i32, tag: String::new() });
                }
                continue;
            }
            if let Some(currency) = money::simple(&s) {
                out.push(Candidate { end, cost: 1_060_000, rank: 60_000, tag: String::new() });
                let mut index = 1;
                if currency.serial {
                    out.push(Candidate { end, cost: 2_220_000, rank: 120_000 + index, tag: String::new() });
                    index += 1;
                }
                if !currency.spaced {
                    out.push(Candidate { end, cost: 100_000_000, rank: 130_000 + index, tag: String::new() });
                }
                continue;
            }
            if let Some(unit) = measure::simple(&s) {
                out.push(Candidate { end, cost: 1_070_000, rank: 80_000, tag: String::new() });
                let mut index = 1;
                if unit.serial {
                    out.push(Candidate { end, cost: 2_220_000, rank: 120_000 + index, tag: String::new() });
                    index += 1;
                }
                if !unit.spaced && !s.contains(' ') {
                    out.push(Candidate { end, cost: 100_000_000, rank: 130_000 + index, tag: String::new() });
                }
                continue;
            }
            for (index, (branch, cost, _)) in outputs(&s, false).into_iter().enumerate() {
                out.push(Candidate { end, cost, rank: branch * 10_000 + index as i32, tag: String::new() });
            }
        }
    }
    fn materialize(&self, text: &[char], start: usize, c: &mut Candidate) {
        if c.rank / 10_000 == 13 {
            let mut tag = String::from("name: \"");
            tag.extend(text[start..c.end].iter().map(|c| match c {
                'ş' => 'ș',
                'Ş' => 'Ș',
                'ţ' => 'ț',
                'Ţ' => 'Ț',
                _ => *c,
            }));
            tag.push('"');
            c.tag = tag;
            return;
        }
        let s: String = text[start..c.end].iter().collect();
        let branch = c.rank / 10_000;
        if s.bytes().all(|b| b.is_ascii_digit()) {
            if branch == 5 {
                if let Some(value) = telephone::telephone(&s) {
                    c.tag = tag_name(&value);
                    return;
                }
            } else if branch == 12 {
                if let Some((value, _)) = cardinal::cardinal(&s).into_iter().next() {
                    c.tag = tag_name(&value);
                    return;
                }
            }
        }
        if branch == 3 {
            let index = (c.rank % 10_000) as usize;
            if time::period_readings(&s).is_some() {
                if let Some(value) = time::render_period(&s, index) {
                    c.tag = tag_name(&value);
                    return;
                }
            } else if s.bytes().all(|b| b.is_ascii_digit() || b == b':') {
                if let Some(value) = time::time(&s).into_iter().nth(index) {
                    c.tag = tag_name(&value);
                    return;
                }
            }
        }
        if c.rank == 60_000 {
            if let Some(currency) = money::simple(&s) {
                if let Some(value) = money::render_simple(&currency) {
                    c.tag = tag_name(&value);
                    return;
                }
            }
        }
        if c.rank == 80_000 {
            if let Some(unit) = measure::simple(&s) {
                if let Some(value) = measure::render_simple(&unit) {
                    c.tag = tag_name(&value);
                    return;
                }
            }
        }
        let index = (c.rank % 10_000) as usize;
        if let Some((_, _, tag)) = outputs(&s, true).into_iter().nth(index) {
            c.tag = tag;
        }
    }
}
impl Classifier for Punctuation {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        let mut end = start;
        while end < text.len() && "!#%&'()*+,-./:;<=>?@[\\]^_`{|}~\"«»“”„’‘–—…•".contains(text[end])
        {
            end += 1;
            out.push(Candidate { end, cost: 1_100_000, rank: 0, tag: String::new() });
        }
    }
    fn materialize(&self, text: &[char], start: usize, c: &mut Candidate) {
        let s: String = text[start..c.end].iter().collect();
        c.tag = format!("name: \"{}\"", s);
    }
}
impl Grammar for Ro {
    fn code(&self) -> &'static str {
        "ro"
    }
    fn shape(&self) -> Shape {
        Shape::SeparatedOrPunct
    }
    fn classifiers(&self) -> &[Box<dyn Classifier>] {
        &self.classifiers
    }
    fn punct(&self) -> Option<&dyn Classifier> {
        Some(&self.punct)
    }
    fn verbalize(&self, token: &Dict) -> Option<String> {
        if let Some(v) = common::verbalize_name(token) {
            return Some(v);
        }
        let (name, value) = common::class(token)?;
        if name != "date" {
            return None;
        }
        let Value::Dict(fields) = value else {
            return None;
        };
        let mut fields = Fields::new(fields);
        let day = fields.take("day")?;
        let month = fields.take("month")?;
        let year = fields.take("year")?;
        if !fields.done() {
            return None;
        }
        Some(format!("{} {} {}", day, month, year).replace('\u{a0}', " "))
    }
}
static GRAMMAR: LazyLock<Ro> = LazyLock::new(|| Ro { classifiers: vec![Box::new(Union)], punct: Punctuation });
pub fn grammar() -> &'static dyn Grammar {
    &*GRAMMAR
}
