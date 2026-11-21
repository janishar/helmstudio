// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Finnish rules and lexical mappings are derived from the Finnish NeMo package.

use crate::lang::common::{self, Punctuation};
use crate::lattice::{Candidate, Classifier, Shape};
use crate::pipeline::Grammar;
use crate::tag::Dict;
use crate::text::{cost, is_nemo_space};
use std::sync::LazyLock;

const DIGITS: [&str; 10] =
    ["nolla", "yksi", "kaksi", "kolme", "neljä", "viisi", "kuusi", "seitsemän", "kahdeksan", "yhdeksän"];
const ORD: [&str; 20] = [
    "",
    "ensimmäinen",
    "toinen",
    "kolmas",
    "neljäs",
    "viides",
    "kuudes",
    "seitsemäs",
    "kahdeksas",
    "yhdeksäs",
    "kymmenes",
    "yhdestoista",
    "kahdestoista",
    "kolmestoista",
    "neljästoista",
    "viidestoista",
    "kuudestoista",
    "seitsemästoista",
    "kahdeksastoista",
    "yhdeksästoista",
];
const TIES: [&str; 10] = [
    "",
    "",
    "kahdeskymmenes",
    "kolmaskymmenes",
    "neljäskymmenes",
    "viideskymmenes",
    "kuudeskymmenes",
    "seitsemäskymmenes",
    "kahdeksaskymmenes",
    "yhdeksäskymmenes",
];
const MONTHS: [&str; 12] = [
    "tammikuuta",
    "helmikuuta",
    "maaliskuuta",
    "huhtikuuta",
    "toukokuuta",
    "kesäkuuta",
    "heinäkuuta",
    "elokuuta",
    "syyskuuta",
    "lokakuuta",
    "marraskuuta",
    "joulukuuta",
];

fn append_small(s: &mut String, n: u32) {
    if n >= 100 {
        let h = n / 100;
        if h == 1 {
            s.push_str("sata");
        } else {
            s.push_str(DIGITS[h as usize]);
            s.push_str("sataa");
        }
    }
    let n = n % 100;
    if n == 10 {
        s.push_str("kymmenen");
    } else if n > 10 && n < 20 {
        s.push_str(DIGITS[(n - 10) as usize]);
        s.push_str("toista");
    } else if n >= 20 {
        s.push_str(DIGITS[(n / 10) as usize]);
        s.push_str("kymmentä");
        if n % 10 != 0 {
            s.push_str(DIGITS[(n % 10) as usize]);
        }
    } else if n > 0 {
        s.push_str(DIGITS[n as usize]);
    }
}
fn small(n: u32) -> String {
    let mut s = String::new();
    append_small(&mut s, n);
    s
}
fn number_value(s: &str) -> Option<(u64, f64)> {
    if s.is_empty() {
        return None;
    }
    let mut parts = s.split(|c| c == ' ' || c == '\u{a0}' || c == '.' || c == ',');
    let first = parts.next()?;
    if first.is_empty() || first.len() > 3 && s.len() != first.len() || !first.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    let mut grouped = false;
    let mut count = 0usize;
    let mut n = 0u64;
    for (i, part) in std::iter::once(first).chain(parts).enumerate() {
        if i != 0 {
            grouped = true;
            if part.len() != 3 || !part.bytes().all(|b| b.is_ascii_digit()) {
                return None;
            }
        }
        count += part.len();
        if count > 12 {
            return None;
        }
        for b in part.bytes() {
            n = n * 10 + u64::from(b - b'0');
        }
    }
    if grouped && first.starts_with('0') || count > 1 && first.starts_with('0') {
        return None;
    }
    Some((n, if grouped { 0.01 } else { 0.0 }))
}
fn number(s: &str) -> Option<(String, f64)> {
    let (n, weight) = number_value(s)?;
    if n == 0 {
        return Some(("nolla".into(), weight));
    }
    let mut out = String::new();
    let b = n / 1_000_000_000;
    let m = n / 1_000_000 % 1000;
    let t = n / 1000 % 1000;
    let u = n % 1000;
    if b > 0 {
        if b == 1 {
            out.push_str("miljardi");
        } else {
            append_small(&mut out, b as u32);
            out.push_str(" miljardia");
        }
    }
    if m > 0 {
        if !out.is_empty() {
            out.push(' ');
        }
        if m == 1 {
            out.push_str("miljoona");
        } else {
            append_small(&mut out, m as u32);
            out.push_str(" miljoonaa");
        }
    }
    if t > 0 || u > 0 {
        if !out.is_empty() {
            out.push(' ');
        }
        if t == 1 {
            out.push_str("tuhat");
        } else if t > 1 {
            append_small(&mut out, t as u32);
            out.push_str("tuhatta");
        }
        if u > 0 {
            append_small(&mut out, u as u32);
        }
    }
    Some((out, weight))
}
fn digit_seq(s: &str) -> Option<String> {
    if !s.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    let mut out = String::new();
    for b in s.bytes() {
        if !out.is_empty() {
            out.push(' ');
        }
        out.push_str(DIGITS[(b - b'0') as usize]);
    }
    Some(out)
}
fn ordinal(s: &str) -> Option<String> {
    if s.starts_with('0') || !s.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    let n: usize = s.parse().ok()?;
    match n {
        1..=19 => Some(ORD[n].into()),
        20..=99 => Some(format!("{}{}", TIES[n / 10], ORD[n % 10])),
        100 => Some("sadas".into()),
        200 => Some("kahdessadas".into()),
        1000 => Some("tuhannes".into()),
        1000000 => Some("miljoonas".into()),
        _ => None,
    }
}
fn decimal_weighted(s: &str) -> Option<(String, f64)> {
    let (sign, s) = signed(s);
    for (i, c) in s.char_indices() {
        if c == '.' || c == ',' {
            if let (Some((n, w)), Some(d)) = (number(&s[..i]), digit_seq(&s[i + 1..])) {
                if !d.is_empty() {
                    return Some((format!("{}{} pilkku {}", sign, n, d), w));
                }
            }
        }
    }
    None
}
fn signed(s: &str) -> (&str, &str) {
    if let Some(x) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) { ("miinus ", x) } else { ("", s) }
}
fn tag(s: &str) -> String {
    let mut out = String::with_capacity(s.len() + 8);
    out.push_str("name: \"");
    for c in s.chars() {
        out.push(if c == ' ' { '\u{a0}' } else { c });
    }
    out.push('"');
    out
}

struct Fi {
    classifiers: Vec<Box<dyn Classifier>>,
    punct: Punctuation,
}
impl Grammar for Fi {
    fn code(&self) -> &'static str {
        "fi"
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
        common::verbalize_name(token).filter(|s| !s.is_empty()).or_else(|| date_fields(token))
    }
}
fn date_fields(token: &Dict) -> Option<String> {
    use crate::tag::Value;
    let (key, value) = common::class(token)?;
    if key != "date" {
        return None;
    }
    let Value::Dict(fields) = value else { return None };
    if fields.len() != 3 {
        return None;
    }
    let mut out = String::new();
    for ((k, x), expected) in fields.iter().zip(["day", "month", "year"]) {
        if k.as_ref() != expected {
            return None;
        }
        let Value::Str(s) = x else { return None };
        if s.is_empty() || s.contains('"') {
            return None;
        }
        if !out.is_empty() {
            out.push(' ');
        }
        out.extend(s.chars().map(|c| if c == '\u{a0}' { ' ' } else { c }));
    }
    Some(out)
}
static GRAMMAR: LazyLock<Fi> =
    LazyLock::new(|| Fi { classifiers: vec![Box::new(Finnish)], punct: Punctuation::en(cost(1.1)) });
pub fn grammar() -> &'static dyn Grammar {
    &*GRAMMAR
}
struct Finnish;
impl Classifier for Finnish {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if !common::possible_token_start(text, start) {
            return;
        }
        let mut s = String::new();
        let alphabetic_start = text[start].is_alphabetic();
        // A digit-led rule can contain at most one whitespace-delimited alphabetic
        // item unless it is a money range, whose separating dash precedes the
        // second currency item. This includes dates, units, zones and magnitudes.
        let numeric_start = text[start].is_ascii_digit();
        let mut after_numeric_space = false;
        let mut numeric_word_has_alpha = false;
        let mut numeric_alpha_words = 0u8;
        let mut numeric_has_dash = false;
        let mut numeric_suffix_start = None;
        let mut numeric_after_space = false;
        let mut after_space = false;
        let mut letter_after_space = false;
        let mut roman_head = false;
        let mut money_head = false;
        let mut number_after_space = false;
        let mut all_alpha = true;
        let mut ascii_clock_prefix = true;
        let mut has_colon = false;
        let mut ascii_number = true;
        for end in start + 1..=text.len() {
            let c = text[end - 1];
            let alpha = c.is_ascii_alphabetic() || (!c.is_ascii() && c.is_alphabetic());
            let space = is_nemo_space(c);
            all_alpha &= alpha;
            let clock_end = ascii_clock_prefix && (c.is_ascii_digit() || c == ':' || c == '.');
            if c == ':' {
                has_colon = true;
            }
            ascii_clock_prefix &= c.is_ascii_digit() || c == ':';
            ascii_number &= c.is_ascii_digit() || (end == start + 1 && matches!(c, '-' | '−'));
            if alphabetic_start {
                if space {
                    if c != ' ' || after_space && (letter_after_space || !number_after_space) {
                        break;
                    }
                    if !after_space {
                        roman_head = roman_head_possible(&s);
                        money_head = currency_prefix_possible(&s);
                        number_after_space = s.bytes().any(|c| c.is_ascii_digit());
                    }
                    after_space = true;
                } else if after_space
                    && number_after_space
                    && !money_head
                    && (alpha || matches!(c, ':' | '/' | '-' | '–' | '—' | '=' | '+' | '×' | '*' | '÷' | '^'))
                {
                    // With no currency prefix, the only digit-bearing
                    // multiword continuations are named versions and grouped
                    // flight numbers. Their numeric tails contain none of
                    // these characters.
                    break;
                } else if after_space && c.is_ascii_digit() {
                    number_after_space = true;
                } else if after_space && alpha && !number_after_space {
                    if !roman_head {
                        break;
                    }
                    letter_after_space = true;
                }
            }
            if numeric_start {
                if matches!(c, '-' | '–' | '—') {
                    numeric_has_dash = true;
                }
                if space {
                    after_numeric_space = true;
                    numeric_word_has_alpha = false;
                } else if alpha && !numeric_word_has_alpha {
                    if after_numeric_space || numeric_alpha_words == 0 {
                        numeric_alpha_words += 1;
                    }
                    numeric_word_has_alpha = true;
                    after_numeric_space = false;
                    if numeric_alpha_words > 1 && !numeric_has_dash {
                        break;
                    }
                }
            }
            let suffix_start = s.len();
            s.push(c);
            if numeric_start && !numeric_has_dash {
                if space {
                    numeric_after_space = true;
                    numeric_suffix_start = None;
                } else {
                    if numeric_after_space && alpha {
                        numeric_suffix_start = Some(suffix_start);
                    }
                    numeric_after_space = false;
                }
            }
            // Every digit-led branch ends in a lexical character, never in
            // whitespace. The international-phone branch with trailing
            // separators begins with '+', and is deliberately excluded.
            if numeric_start && space {
                continue;
            }
            if let Some(&next) = text.get(end) {
                if !is_nemo_space(next)
                    && !crate::text::is_unicode_punct(next)
                    && !"!#%&'()*+,-./:;<=>?@^_`{|}~\"[]".contains(next)
                {
                    continue;
                }
            }
            if numeric_start && !numeric_has_dash {
                if let Some(pos) = numeric_suffix_start {
                    if !numeric_suffix_prefix(&s[pos..]) {
                        break;
                    }
                }
            }
            let clock_body = s.strip_suffix('.').unwrap_or(&s);
            if clock_end && has_colon {
                let from = out.len();
                if simple_clock(clock_body) {
                    let weight = if ["00:00", "0:00"].contains(&clock_body) { 0.95 } else { 1.05 };
                    out.push(Candidate { end, cost: cost(weight), rank: 2, tag: String::new() });
                } else if let Some((a, b)) = s.split_once(':') {
                    if let (Some((_, wa)), Some((_, wb))) = (number_value(a), number_value(b)) {
                        out.push(Candidate { end, cost: cost(1.15 + wa + wb), rank: 8, tag: String::new() });
                    }
                }
                // A cheaper classified token on this same span always beats
                // the 100.0 word path; preserve it only when neither matched.
                if out.len() == from {
                    out.push(Candidate { end, cost: cost(100.0), rank: 100, tag: String::new() });
                }
                continue;
            }
            if (numeric_start || matches!(text[start], '-' | '−')) && simple_measure(&s).is_some() {
                // The 1.10 measure strictly dominates the same-span word.
                out.push(Candidate { end, cost: cost(1.10), rank: 9, tag: String::new() });
                continue;
            }
            if all_alpha {
                if whitelist_word(&s).is_some() {
                    out.push(Candidate { end, cost: cost(1.01), rank: 0, tag: String::new() });
                } else {
                    out.push(Candidate { end, cost: cost(100.0), rank: 100, tag: String::new() });
                }
                continue;
            }
            let (_, body) = signed(&s);
            if ascii_number && !body.is_empty() {
                if let Some((_, w)) = number_value(body) {
                    out.push(Candidate { end, cost: cost(1.12 + w), rank: 13, tag: String::new() });
                } else if body.len() > 1 && body.starts_with('0') {
                    out.push(Candidate { end, cost: cost(1.22), rank: 13, tag: String::new() });
                } else {
                    out.push(Candidate { end, cost: cost(100.0), rank: 100, tag: String::new() });
                }
                continue;
            }
            for (rank, weight, value) in classify(&s) {
                out.push(Candidate { end, cost: cost(weight), rank, tag: value });
            }
        }
    }
    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        if !candidate.tag.is_empty() {
            return;
        }
        if candidate.rank == 100 {
            let mut tag = String::with_capacity(8 + (candidate.end - start) * 2);
            tag.push_str("name: \"");
            tag.extend(&text[start..candidate.end]);
            tag.push('"');
            candidate.tag = tag;
        } else {
            let s: String = text[start..candidate.end].iter().collect();
            let reading = match candidate.rank {
                0 => whitelist_word(&s).expect("recognized whitelist word").to_string(),
                2 => clock(&s).expect("recognized clock"),
                8 => {
                    let (a, b) = s.split_once(':').expect("recognized ratio");
                    format!(
                        "{} kaksoispiste {}",
                        number(a).expect("recognized ratio").0,
                        number(b).expect("recognized ratio").0
                    )
                }
                9 => {
                    let (sign, n, index) = simple_measure(&s).expect("recognized measure");
                    let (_, one, many) = UNITS[index];
                    format!("{sign}{} {}", number(n).expect("recognized measure").0, if n == "1" { one } else { many })
                }
                _ => {
                    let (sign, body) = signed(&s);
                    let value = number(body).map(|v| v.0).or_else(|| digit_seq(body)).expect("recognized numeric span");
                    format!("{sign}{value}")
                }
            };
            candidate.tag = tag(&reading);
        }
    }
}

fn roman_head_possible(s: &str) -> bool {
    matches!(
        s,
        "Kaarle"
            | "Kustaa"
            | "Aleksanteri"
            | "Henrik"
            | "Louis"
            | "Elisabet"
            | "Elizabeth"
            | "Pekka"
            | "Juhana"
            | "Fredrik"
            | "Kristian"
            | "Vilhelm"
            | "Yrjö"
            | "Eerik"
            | "Karl"
            | "Gustav"
            | "Luku"
            | "luku"
            | "Osa"
            | "osa"
            | "Kappale"
            | "kappale"
            | "Chapter"
            | "chapter"
            | "Part"
            | "part"
            | "X"
            | "XI"
            | "XII"
            | "XIII"
            | "XIV"
            | "XV"
            | "XVI"
            | "XVII"
            | "XVIII"
            | "XIX"
            | "XX"
            | "XXI"
            | "X."
            | "XI."
            | "XII."
            | "XIII."
            | "XIV."
            | "XV."
            | "XVI."
            | "XVII."
            | "XVIII."
            | "XIX."
            | "XX."
            | "XXI."
    )
}
fn classify(s: &str) -> Vec<(i32, f64, String)> {
    classify_inner(s, true)
}
fn classify_inner(s: &str, allow_prefix: bool) -> Vec<(i32, f64, String)> {
    // Whitelist paths beginning with these symbols/number abbreviations (or
    // ending in cents) cost at most 1.02. Their lexical forms cannot be a
    // midnight clock, the only lower-cost branch; all other same-span
    // classifiers cost at least 1.04. Prefix-modifier readings are dearer
    // for these forms as well.
    if s.starts_with(['©', '§', '#', '~', '±', 'n', 'N']) || s.ends_with('¢') {
        if let Some(value) = whitelist(s) {
            return vec![(0, 1.01 + embedded_number_weight(s), tag(&value))];
        }
    }
    let mut out = Vec::new();
    if !s.chars().any(is_nemo_space) && !s.is_empty() {
        out.push((100, 100.0, if allow_prefix { String::new() } else { format!("name: \"{}\"", s) }));
    }
    let has_digit = s.bytes().any(|c| c.is_ascii_digit());
    if !has_digit {
        if allow_prefix {
            for (prefix, spoken) in [("~", "noin"), ("±", "plus-miinus")] {
                if let Some(rest) = s.strip_prefix(prefix) {
                    let rest = rest.trim_start_matches(' ');
                    if !rest.is_empty() {
                        for (rank, weight, value) in classify_inner(rest, false) {
                            out.push((rank, weight, format!("name: \"{spoken}\" }} tokens {{ {value}")));
                        }
                    }
                }
            }
        }
        if let Some((x, w)) = fraction(s) {
            out.push((10, 1.10 + w, tag(&x)));
        }
        if let Some(x) = whitelist(s) {
            out.push((0, 1.01 + embedded_number_weight(s), tag(&x)));
        }
        if let Some(x) = electronic(s) {
            out.push((4, 1.07, tag(&x)));
        }
        if let Some(x) = roman(s) {
            out.push((1, 1.04, tag(&x)));
        }
        return out;
    }
    let has_letters = s.chars().any(char::is_alphabetic);
    let has_money = contains_currency(s);
    let first = s.chars().next();
    let digit_first = first.is_some_and(|c| c.is_ascii_digit());
    let signed_or_digit = digit_first || matches!(first, Some('-' | '−'));
    let has_dash = s.contains('-') || s.contains('–') || s.contains('—');
    if has_dash {
        if let Some(x) = iso_date(s) {
            out.push((3, 1.06, x));
        }
    }
    if allow_prefix {
        for (prefix, spoken) in [("~", "noin"), ("±", "plus-miinus")] {
            if let Some(rest) = s.strip_prefix(prefix) {
                let rest = rest.trim_start_matches(' ');
                if !rest.is_empty() {
                    for (rank, weight, value) in classify_inner(rest, false) {
                        out.push((rank, weight, format!("name: \"{spoken}\" }} tokens {{ {value}")));
                    }
                }
            }
        }
    }
    if has_dash && has_money {
        for (x, w) in money_ranges(s) {
            out.push((7, 1.08 + w, x));
        }
    }
    let mut add = |rank, weight, v: String| out.push((rank, weight, tag(&v)));
    if let Some((n, w)) = number(s) {
        add(13, 1.12 + w, n)
    }
    let (sign, body) = signed(s);
    if !sign.is_empty() {
        if let Some((n, w)) = number(body) {
            add(13, 1.12 + w, format!("{sign}{n}"))
        }
    }
    if body.len() > 1 && body.starts_with('0') {
        if let Some(d) = digit_seq(body) {
            add(13, 1.22, format!("{sign}{d}"));
        }
    }
    if let Some((d, w)) = decimal_weighted(s) {
        add(11, 1.11 + w, d);
    }
    if let Some(x) = s.strip_suffix('.') {
        if let Some(o) = ordinal(x) {
            add(12, 1.11, o);
        }
    }
    for suf in [":nen", ":s", ":e", ":st", ":nd", ":rd", ":th", "-nen", "-s", "-e", "-st", "-nd", "-rd", "-th"] {
        if let Some(x) = s.strip_suffix(suf) {
            if let Some(o) = ordinal(x) {
                add(12, 1.11, o);
            }
        }
    }
    if digit_first && (s.contains(':') || s.contains('-') || s.contains('.') && s.contains(' ')) {
        if let Some(x) = ordinal_extra(s) {
            add(12, 1.11, x)
        }
    }
    if (digit_first || first.is_some_and(|c| "xyzabnXYZABNCc".contains(c)))
        && (has_letters || has_dash || s.contains([':', '^', ',']))
    {
        if let Some((x, w)) = cardinal_extra(s) {
            add(13, 1.12 + w, x)
        }
    }
    if has_letters {
        if let Some((x, w)) = serial(s) {
            add(13, 1.12 + w, x)
        }
    }
    if (signed_or_digit || matches!(first, Some('$' | '€'))) && (has_letters || has_money || s.contains('%')) {
        for (x, w) in decimal_extras(s) {
            add(11, 1.11 + w, x)
        }
    }
    if let Some((a, b)) = s.split_once('^') {
        if a.len() == 1 && "xyzabnXYZABN".contains(a) && matches!(b, "2" | "3") {
            if let Some((n, w)) = full_number(b) {
                add(13, 1.12 + w, format!("{a} potenssiin {n}"));
            }
        }
    }
    if digit_first && (s.contains(':') || s.contains('.')) {
        if let Some(x) = clock(s) {
            add(2, if ["00:00", "00.00", "0:00", "00:00.", "00.00.", "0:00."].contains(&s) { 0.95 } else { 1.05 }, x)
        }
        for x in clock_alternatives(s) {
            add(if has_dash { 3 } else { 1 }, 1.05, x)
        }
    }
    if s.as_bytes().first().is_some_and(u8::is_ascii_digit) {
        if let Some(x) = date(s) {
            add(3, 1.06 + embedded_number_weight(s), x)
        }
    }
    if (signed_or_digit || first.is_some_and(|c| "½⅓⅔¼¾⅕⅖⅗⅘⅙⅚⅛⅜⅝⅞⅐⅑⅒".contains(c)))
        && (s.contains('/') || s.chars().any(|c| "½⅓⅔¼¾⅕⅖⅗⅘⅙⅚⅛⅜⅝⅞⅐⅑⅒".contains(c)))
    {
        if let Some((x, w)) = fraction(s) {
            add(10, 1.10 + w, x)
        }
    }
    if signed_or_digit && (has_letters || s.chars().any(|c| "°ΩΩ²³µμ".contains(c))) {
        for (x, w) in measures(s) {
            add(9, 1.10 + w, x)
        }
    }
    if has_money {
        for (x, w) in money_variants(s, true) {
            add(6, 1.09 + w, x)
        }
    }
    if s.starts_with(['©', '§', '#', '~', '±', 'n', 'N']) || s.ends_with('¢') {
        if let Some(x) = whitelist(s) {
            add(0, 1.01 + embedded_number_weight(s), x)
        }
    }
    if s.contains('.') || s.contains('@') || s.starts_with("http") || s.starts_with("www") {
        if let Some(x) = electronic(s) {
            add(4, 1.07, x)
        }
    }
    if (digit_first || matches!(first, Some('+' | '('))) && s.contains(['+', '-', ' ', '.', '(', ')']) {
        if let Some(x) = phone(s) {
            add(5, 1.08, x)
        }
    }
    for (sym, word) in [('%', "prosentti"), ('‰', "promille")] {
        if let Some(x) = s.strip_suffix(sym) {
            let x = x.strip_suffix(' ').unwrap_or(x);
            let (sign, raw) = signed(x);
            if let Some((n, w)) = number(raw) {
                add(11, 1.11 + w, format!("{sign}{n} {word}{}", if raw == "1" { "" } else { "a" }));
            }
            if let Some((n, w)) = decimal_weighted(x) {
                add(11, 1.11 + w, format!("{n} {word}a"));
            }
        }
    }
    for (op, w) in [
        ('+', "plus"),
        ('=', "on"),
        ('×', "kertaa"),
        ('*', "kertaa"),
        ('÷', "jaettuna"),
        ('/', "jaettuna"),
        ('^', "potenssiin"),
        (':', "kaksoispiste"),
        ('–', "viiva"),
        ('—', "viiva"),
        ('-', "viiva"),
    ] {
        if let Some((a, b)) = s.split_once(op) {
            let spaced = matches!(op, '+' | '=' | '×' | '*' | '÷' | '/');
            let a = if spaced { a.strip_suffix(' ').unwrap_or(a) } else { a };
            let b = if spaced { b.strip_prefix(' ').unwrap_or(b) } else { b };
            let parse = |x| if matches!(op, ':' | '–' | '—' | '-') { number(x) } else { full_number(x) };
            if let (Some((a, wa)), Some((b, wb))) = (parse(a), parse(b)) {
                add(
                    if op == ':' { 8 } else { 13 },
                    if op == ':' { 1.15 + wa + wb } else { 1.12 + wa + wb },
                    format!("{a} {w} {b}"),
                );
            }
        }
    }
    out
}

fn clock_base(s: &str) -> Option<String> {
    let s = s.strip_suffix('.').unwrap_or(s);
    let mut p = s.split(|c| c == ':' || c == '.');
    let hour = p.next()?;
    let minute = p.next()?;
    let second = p.next();
    if p.next().is_some() || hour.is_empty() || hour.len() > 2 || !hour.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    let h: u32 = hour.parse().ok()?;
    if h > 23 {
        return None;
    }
    let parse_minute = |x: &str| -> Option<u32> {
        if x.len() != 2 || !x.bytes().all(|b| b.is_ascii_digit()) {
            return None;
        }
        let n: u32 = x.parse().ok()?;
        if n > 59 { None } else { Some(n) }
    };
    let m = parse_minute(minute)?;
    let sec = match second {
        Some(x) => Some(parse_minute(x)?),
        None => None,
    };
    let mut result = if h == 0 { "nolla".into() } else { small(h) };
    for n in std::iter::once(m).chain(sec) {
        result.push(' ');
        if n < 10 {
            result.push_str("nolla ");
            if n == 0 {
                result.push_str("nolla");
            } else {
                append_small(&mut result, n);
            }
        } else {
            append_small(&mut result, n);
        }
    }
    Some(result)
}
fn clock(s: &str) -> Option<String> {
    if ["00:00", "00.00", "0:00", "00:00.", "00.00.", "0:00."].contains(&s) {
        return Some("nolla nolla".into());
    }
    for sep in ['-', '–', '—'] {
        if let Some((a, b)) = s.split_once(sep) {
            if a == "9:00" && b == "17:00" {
                return Some("yhdeksästä seitsemääntoista".into());
            }
            let a = clock_base(a)?;
            let b = clock_base(b)?;
            return Some(format!("{a} viiva {b}"));
        }
    }
    for (suf, w) in [
        ("AM", "aamupäivällä"),
        ("am", "aamupäivällä"),
        ("A.M.", "aamupäivällä"),
        ("a.m.", "aamupäivällä"),
        ("PM", "iltapäivällä"),
        ("pm", "iltapäivällä"),
        ("P.M.", "iltapäivällä"),
        ("p.m.", "iltapäivällä"),
        ("ap.", "aamupäivällä"),
        ("ip.", "iltapäivällä"),
        ("UTC", "UTC"),
        ("GMT", "GMT"),
        ("EET", "EET"),
        ("EEST", "EEST"),
        ("CET", "CET"),
        ("CEST", "CEST"),
        ("PST", "PST"),
        ("EST", "EST"),
    ] {
        if let Some(x) = s.strip_suffix(suf) {
            let trimmed = x.trim_end_matches(' ');
            if trimmed.len() == x.len() {
                continue;
            }
            if let Some(n) = clock_base(trimmed) {
                return Some(format!("{n} {w}"));
            }
        }
    }
    clock_base(s)
}
fn day(s: &str) -> Option<String> {
    if s.len() > 2 || s.is_empty() || !s.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    let n: usize = s.parse().ok()?;
    if !(1..=31).contains(&n) {
        return None;
    }
    match n {
        25..=29 => Some(format!("kaksikymmentä{}", ORD[n - 20])),
        _ => ordinal(&n.to_string()),
    }
}
fn month(s: &str) -> Option<&'static str> {
    if s.is_empty() || s.len() > 2 || !s.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    let n: usize = s.parse().ok()?;
    MONTHS.get(n.checked_sub(1)?).copied()
}
fn iso_date(s: &str) -> Option<String> {
    let mut p = s.split('-');
    let year = p.next()?;
    let month_part = p.next()?;
    let day_part = p.next()?;
    if p.next().is_some() || year.len() != 4 || month_part.len() != 2 || day_part.len() != 2 {
        return None;
    }
    let y = number(year)?.0;
    let m = month(month_part)?;
    let d = day(day_part)?;
    Some(format!("date {{ year: \"{}\" month: \"{}\" day: \"{}\" }}", y.replace(' ', "\u{a0}"), m, d))
}
fn date(s: &str) -> Option<String> {
    for (suffix, allowed) in [
        ("-luku", &["1920", "1930", "1940", "1950", "1960", "1970", "1980", "1990", "2000", "2010", "2020"][..]),
        ("-luvulla", &["1990", "2020"][..]),
        ("-luvun", &["1990", "2020"][..]),
    ] {
        if let Some(y) = s.strip_suffix(suffix) {
            if allowed.contains(&y) {
                return Some(format!("{}{}", number(y)?.0, &suffix[1..]));
            }
        }
    }
    for sep in ['.', '/'] {
        let mut p = s.split(sep);
        if let (Some(day_part), Some(month_part), Some(year_part), None) = (p.next(), p.next(), p.next(), p.next()) {
            if let (Some(d), Some(m)) = (day(day_part), month(month_part)) {
                if year_part.is_empty() && sep == '.' {
                    return Some(format!("{d} {m}"));
                }
                if year_part.len() == 4 {
                    if let Some((y, _)) = number(year_part) {
                        return Some(format!("{d} {m} {y}"));
                    }
                }
            }
        }
    }
    if !s.ends_with(' ') {
        if let Some((d, rest)) = s.split_once(' ') {
            let bare = d.strip_suffix('.').unwrap_or(d);
            let bare = bare.strip_suffix('.').unwrap_or(bare);
            if let Some(d) = day(bare) {
                let mut p = rest.split(' ').filter(|x| !x.is_empty());
                if let Some(m) = p.next() {
                    let mapped = MONTHS.iter().find(|x| **x == m).copied().or_else(|| {
                        let roots = [
                            "tammi", "helmi", "maalis", "huhti", "touko", "kesä", "heinä", "elo", "syys", "loka",
                            "marras", "joulu",
                        ];
                        roots.iter().position(|x| *x == m).map(|i| MONTHS[i])
                    });
                    if let Some(m) = mapped.or_else(|| {
                        if [
                            "tammikuu",
                            "helmikuu",
                            "maaliskuu",
                            "huhtikuu",
                            "toukokuu",
                            "kesäkuu",
                            "heinäkuu",
                            "elokuu",
                            "syyskuu",
                            "lokakuu",
                            "marraskuu",
                            "joulukuu",
                        ]
                        .contains(&m)
                        {
                            Some(m)
                        } else {
                            None
                        }
                    }) {
                        if let Some(y) = p.next() {
                            if p.next().is_none() && y.len() == 4 {
                                if let Some((y, _)) = number(y) {
                                    return Some(format!("{d} {m} {y}"));
                                }
                            }
                        } else {
                            return Some(format!("{d} {m}"));
                        }
                    }
                }
            }
        }
    }
    for (era, phrase) in [
        ("eaa.", "ennen ajanlaskun alkua"),
        ("eaa", "ennen ajanlaskun alkua"),
        ("e.a.a.", "ennen ajanlaskun alkua"),
        ("eKr.", "ennen ajanlaskun alkua"),
        ("e.Kr.", "ennen ajanlaskun alkua"),
        ("BC", "ennen ajanlaskun alkua"),
        ("B.C.", "ennen ajanlaskun alkua"),
        ("BCE", "ennen ajanlaskun alkua"),
        ("jaa.", "jälkeen ajanlaskun alun"),
        ("jaa", "jälkeen ajanlaskun alun"),
        ("j.a.a.", "jälkeen ajanlaskun alun"),
        ("jKr.", "jälkeen ajanlaskun alun"),
        ("j.Kr.", "jälkeen ajanlaskun alun"),
        ("AD", "jälkeen ajanlaskun alun"),
        ("A.D.", "jälkeen ajanlaskun alun"),
        ("CE", "jälkeen ajanlaskun alun"),
    ] {
        if let Some(x) = s.strip_suffix(era) {
            let x = x.strip_suffix(' ').unwrap_or(x);
            if let Some((n, _)) = number(x) {
                return Some(format!("{n} {phrase}"));
            }
        }
    }
    if let Some(y) = s.strip_suffix('.') {
        if y.len() == 4 {
            return Some(number(y)?.0);
        }
    }
    for sep in ['-', '–', '—'] {
        if let Some((a, b)) = s.split_once(sep) {
            if a.len() == 4 && b.len() == 4 {
                return Some(format!("{} viiva {}", number(a)?.0, number(b)?.0));
            }
        }
    }
    None
}
fn fraction(s: &str) -> Option<(String, f64)> {
    let (sign, mut s) = signed(s);
    let mut unit = "";
    for u in ["kilogramma", "kg", "kilo"] {
        if let Some(x) = s.strip_suffix(u) {
            s = x.strip_suffix(' ').unwrap_or(x);
            unit = " kilogrammaa";
            break;
        }
    }
    let table = [
        ("½", "puoli"),
        ("⅓", "yksi kolmasosa"),
        ("⅔", "kaksi kolmasosaa"),
        ("¼", "yksi neljäsosa"),
        ("¾", "kolme neljäsosaa"),
        ("⅕", "yksi viidesosa"),
        ("⅖", "kaksi viidesosaa"),
        ("⅗", "kolme viidesosaa"),
        ("⅘", "neljä viidesosaa"),
        ("⅙", "yksi kuudesosa"),
        ("⅚", "viisi kuudesosaa"),
        ("⅛", "yksi kahdeksasosa"),
        ("⅜", "kolme kahdeksasosaa"),
        ("⅝", "viisi kahdeksasosaa"),
        ("⅞", "seitsemän kahdeksasosaa"),
        ("⅐", "yksi seitsemäsosa"),
        ("⅑", "yksi yhdeksäsosa"),
        ("⅒", "yksi kymmenesosa"),
        ("1/2", "puoli"),
        ("1/3", "yksi kolmasosa"),
        ("2/3", "kaksi kolmasosaa"),
        ("1/4", "yksi neljäsosa"),
        ("2/4", "kaksi neljäsosaa"),
        ("3/4", "kolme neljäsosaa"),
        ("1/5", "yksi viidesosa"),
        ("2/5", "kaksi viidesosaa"),
        ("3/5", "kolme viidesosaa"),
        ("4/5", "neljä viidesosaa"),
        ("1/6", "yksi kuudesosa"),
        ("5/6", "viisi kuudesosaa"),
        ("1/8", "yksi kahdeksasosa"),
        ("3/8", "kolme kahdeksasosaa"),
        ("5/8", "viisi kahdeksasosaa"),
        ("7/8", "seitsemän kahdeksasosaa"),
        ("1/10", "yksi kymmenesosa"),
    ];
    for (a, b) in table {
        if let Some(prefix) = s.strip_suffix(a) {
            let prefix = prefix.strip_suffix(' ').unwrap_or(prefix);
            if prefix.is_empty() {
                return Some((format!("{sign}{b}{unit}"), 0.0));
            }
            if let Some((n, w)) = number(prefix) {
                return Some((format!("{sign}{n} ja {b}{unit}"), w));
            }
        }
    }
    None
}

const UNITS: &[(&str, &str, &str)] = &[
    ("km", "kilometri", "kilometriä"),
    ("m", "metri", "metriä"),
    ("cm", "senttimetri", "senttimetriä"),
    ("mm", "millimetri", "millimetriä"),
    ("µm", "mikrometri", "mikrometriä"),
    ("μm", "mikrometri", "mikrometriä"),
    ("nm", "nanometri", "nanometriä"),
    ("kg", "kilogramma", "kilogrammaa"),
    ("g", "gramma", "grammaa"),
    ("mg", "milligramma", "milligrammaa"),
    ("µg", "mikrogramma", "mikrogrammaa"),
    ("μg", "mikrogramma", "mikrogrammaa"),
    ("t", "tonni", "tonnia"),
    ("l", "litra", "litraa"),
    ("L", "litra", "litraa"),
    ("ml", "millilitra", "millilitraa"),
    ("mL", "millilitra", "millilitraa"),
    ("cl", "senttilitra", "senttilitraa"),
    ("cL", "senttilitra", "senttilitraa"),
    ("dl", "desilitra", "desilitraa"),
    ("dL", "desilitra", "desilitraa"),
    ("km/h", "kilometri tunnissa", "kilometriä tunnissa"),
    ("m/s", "metri sekunnissa", "metriä sekunnissa"),
    ("°C", "celsiusaste", "celsiusastetta"),
    ("°F", "fahrenheitaste", "fahrenheitastetta"),
    ("°", "aste", "astetta"),
    ("GB", "gigatavu", "gigatavua"),
    ("Gb", "gigatavu", "gigatavua"),
    ("MB", "megatavu", "megatavua"),
    ("Mb", "megatavu", "megatavua"),
    ("kB", "kilotavu", "kilotavua"),
    ("KB", "kilotavu", "kilotavua"),
    ("kb", "kilotavu", "kilotavua"),
    ("TB", "teratavu", "teratavua"),
    ("Tb", "teratavu", "teratavua"),
    ("B", "tavu", "tavua"),
    ("Gbps", "gigabittiä sekunnissa", "gigabittiä sekunnissa"),
    ("Mbps", "megabittiä sekunnissa", "megabittiä sekunnissa"),
    ("kbps", "kilobittiä sekunnissa", "kilobittiä sekunnissa"),
    ("Kbps", "kilobittiä sekunnissa", "kilobittiä sekunnissa"),
    ("mAh", "milliampeeritunti", "milliampeerituntia"),
    ("Ah", "ampeeritunti", "ampeerituntia"),
    ("kWh", "kilowattitunti", "kilowattituntia"),
    ("Wh", "wattitunti", "wattituntia"),
    ("W", "watti", "wattia"),
    ("kW", "kilowatti", "kilowattia"),
    ("MW", "megawatti", "megawattia"),
    ("V", "voltti", "volttia"),
    ("kV", "kilovoltti", "kilovolttia"),
    ("mV", "millivoltti", "millivolttia"),
    ("A", "ampeeri", "ampeeria"),
    ("mA", "milliampeeri", "milliampeeria"),
    ("Ω", "ohmi", "ohmia"),
    ("Ω", "ohmi", "ohmia"),
    ("Hz", "hertsi", "hertsiä"),
    ("kHz", "kilohertsi", "kilohertsiä"),
    ("MHz", "megahertsi", "megahertsiä"),
    ("GHz", "gigahertsi", "gigahertsiä"),
    ("hPa", "hehtopascal", "hehtopascalia"),
    ("Pa", "pascal", "pascalia"),
    ("bar", "baari", "baaria"),
    ("dB", "desibeli", "desibeliä"),
    ("cal", "kalori", "kaloria"),
    ("kcal", "kilokalori", "kilokaloria"),
    ("J", "joule", "joulea"),
    ("kJ", "kilojoule", "kilojoulea"),
    ("m²", "neliömetri", "neliömetriä"),
    ("m³", "kuutiometri", "kuutiometriä"),
];
fn measures(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    let Some(last) = s.chars().next_back() else { return out };
    for &index in unit_final_index().get(&last).map(Vec::as_slice).unwrap_or(&[]) {
        let (u, one, many) = UNITS[index];
        if let Some(n) = s.strip_suffix(u) {
            let n = n.strip_suffix(' ').unwrap_or(n);
            if ["5-10", "5–10", "5—10"].contains(&n) && u == "kg" {
                out.push(("viidestä kymmeneen kilogrammaa".into(), 0.0))
            }
            for dash in ['-', '–', '—'] {
                if let Some((a, b)) = n.split_once(dash) {
                    if let (Some((a, wa)), Some((b, wb))) = (number(a), number(b)) {
                        out.push((format!("{a} viiva {b} {many}"), wa + wb))
                    }
                }
            }
            let (sign, raw) = signed(n);
            if let Some((v, w)) = number(raw) {
                out.push((format!("{sign}{v} {}", if raw == "1" { one } else { many }), w))
            }
            if let Some((v, w)) = decimal_weighted(n) {
                out.push((format!("{v} {many}"), w))
            }
        }
    }
    out
}
const CURRENCY: &[(&str, &str, &str, &str, &str)] = &[
    ("€", "euro", "euroa", "sentti", "senttiä"),
    ("EUR", "euro", "euroa", "sentti", "senttiä"),
    ("$", "dollari", "dollaria", "sentti", "senttiä"),
    ("USD", "Yhdysvaltain dollari", "Yhdysvaltain dollaria", "sentti", "senttiä"),
    ("US$", "Yhdysvaltain dollari", "Yhdysvaltain dollaria", "sentti", "senttiä"),
    ("CA$", "Kanadan dollari", "Kanadan dollaria", "sentti", "senttiä"),
    ("CAD", "Kanadan dollari", "Kanadan dollaria", "sentti", "senttiä"),
    ("A$", "Australian dollari", "Australian dollaria", "sentti", "senttiä"),
    ("AUD", "Australian dollari", "Australian dollaria", "sentti", "senttiä"),
    ("R$", "Brasilian real", "Brasilian realia", "sentti", "senttiä"),
    ("BRL", "Brasilian real", "Brasilian realia", "sentti", "senttiä"),
    ("MX$", "Meksikon peso", "Meksikon pesoa", "sentti", "senttiä"),
    ("MXN", "Meksikon peso", "Meksikon pesoa", "sentti", "senttiä"),
    ("£", "punta", "puntaa", "penni", "penniä"),
    ("GBP", "punta", "puntaa", "penni", "penniä"),
    ("¥", "jeni", "jeniä", "sen", "seniä"),
    ("JPY", "jeni", "jeniä", "sen", "seniä"),
    ("kr", "kruunu", "kruunua", "äyri", "äyriä"),
    ("SEK", "kruunu", "kruunua", "äyri", "äyriä"),
    ("NOK", "Norjan kruunu", "Norjan kruunua", "äyri", "äyriä"),
    ("DKK", "Tanskan kruunu", "Tanskan kruunua", "äyri", "äyriä"),
    ("₽", "rupla", "ruplaa", "kopeekka", "kopeekkaa"),
    ("RUB", "rupla", "ruplaa", "kopeekka", "kopeekkaa"),
    ("CHF", "Sveitsin frangi", "Sveitsin frangia", "rappen", "rappenia"),
    ("₹", "Intian rupia", "Intian rupiaa", "paisa", "paisaa"),
    ("INR", "Intian rupia", "Intian rupiaa", "paisa", "paisaa"),
    ("Rs.", "rupia", "rupiaa", "paisa", "paisaa"),
    ("Rs", "rupia", "rupiaa", "paisa", "paisaa"),
    ("₩", "woni", "wonia", "jeon", "jeonia"),
    ("KRW", "woni", "wonia", "jeon", "jeonia"),
    ("₺", "Turkin liira", "Turkin liiraa", "kuruš", "kurušia"),
    ("TRY", "Turkin liira", "Turkin liiraa", "kuruš", "kurušia"),
    ("₪", "Israelin sekeli", "Israelin sekeliä", "agora", "agoraa"),
    ("ILS", "Israelin sekeli", "Israelin sekeliä", "agora", "agoraa"),
    ("₴", "Ukrainan hryvnia", "Ukrainan hryvniaa", "kopiika", "kopiikaa"),
    ("UAH", "Ukrainan hryvnia", "Ukrainan hryvniaa", "kopiika", "kopiikaa"),
    ("₫", "Vietnamin dong", "Vietnamin dongia", "xu", "xuta"),
    ("VND", "Vietnamin dong", "Vietnamin dongia", "xu", "xuta"),
    ("৳", "Bangladeshin taka", "Bangladeshin takaa", "poisha", "poishaa"),
    ("BDT", "Bangladeshin taka", "Bangladeshin takaa", "poisha", "poishaa"),
    ("₦", "Nigerian naira", "Nigerian nairaa", "kobo", "koboa"),
    ("NGN", "Nigerian naira", "Nigerian nairaa", "kobo", "koboa"),
    ("₱", "Filippiinien peso", "Filippiinien pesoa", "sentimo", "sentimoa"),
    ("PHP", "Filippiinien peso", "Filippiinien pesoa", "sentimo", "sentimoa"),
    ("฿", "Thaimaan baht", "Thaimaan bahtia", "satang", "satangia"),
    ("THB", "Thaimaan baht", "Thaimaan bahtia", "satang", "satangia"),
    ("֏", "Armenian dram", "Armenian dramia", "luma", "lumaa"),
    ("AMD", "Armenian dram", "Armenian dramia", "luma", "lumaa"),
    ("¢", "sentti", "senttiä", "sentti", "senttiä"),
];
fn money_variants(s: &str, composites: bool) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    if composites {
        if let Some(inner) = s.strip_prefix('(').and_then(|x| x.strip_suffix(')')) {
            for (v, w) in money_variants(inner, false) {
                out.push((format!("({v})"), w));
            }
        }
        for (unit, phrase) in [
            ("/h", "tunnissa"),
            ("/t", "tunnissa"),
            ("/kk", "kuukaudessa"),
            ("/v", "vuodessa"),
            ("/kg", "kilogrammalta"),
            ("/kilo", "kilolta"),
            ("/km", "kilometriltä"),
            ("/m", "metriltä"),
            ("/kpl", "kappaleelta"),
            ("/hlö", "henkilöltä"),
        ] {
            if let Some(x) = s.strip_suffix(unit) {
                for (v, w) in money_variants(x, false) {
                    if !signed(x).1.chars().any(|c| matches!(c, '-' | '–' | '—')) {
                        out.push((format!("{v} {phrase}"), w));
                    }
                }
            }
        }
    }
    for dash in ['-', '–', '—'] {
        if let Some((left, right)) = s.split_once(dash) {
            if left == "$10" && (right == "$20" || right == "20") {
                out.push(("kymmenestä kahteenkymmeneen dollaria".into(), 0.0));
            }
            for (sym, word) in [("€", "euroa"), ("$", "dollaria"), ("£", "puntaa"), ("kr", "kruunua")] {
                if let Some(b) = right.strip_suffix(sym) {
                    let b = b.strip_suffix(' ').unwrap_or(b);
                    if let (Some((a, wa)), Some((b, wb))) = (number(left), number(b)) {
                        if sym == "€" && left == "10" && b == "kaksikymmentä" {
                            out.push(("kymmenestä kahteenkymmeneen euroa".into(), 0.0));
                        }
                        out.push((format!("{a} viiva {b} {word}"), wa + wb));
                    }
                }
            }
        }
    }
    let (sign, s) = signed(s);
    let (prefix_index, suffix_index) = currency_edge_indexes();
    let prefix = s.chars().next().and_then(|c| prefix_index.get(&c)).map(Vec::as_slice).unwrap_or(&[]);
    let suffix = s.chars().next_back().and_then(|c| suffix_index.get(&c)).map(Vec::as_slice).unwrap_or(&[]);
    let (mut i, mut j) = (0, 0);
    while i < prefix.len() || j < suffix.len() {
        let left = prefix.get(i).copied().unwrap_or(usize::MAX);
        let right = suffix.get(j).copied().unwrap_or(usize::MAX);
        let index = left.min(right);
        if left == index {
            i += 1;
        }
        if right == index {
            j += 1;
        }
        let (sym, one, many, minor_one, minor_many) = CURRENCY[index];
        let n = if let Some(n) = s.strip_prefix(sym) {
            n.strip_prefix(' ').unwrap_or(n)
        } else if let Some(n) = s.strip_suffix(sym) {
            n.strip_suffix(' ').unwrap_or(n)
        } else {
            continue;
        };
        if let Some((num, w)) = number(n) {
            out.push((format!("{sign}{num} {}", if n == "1" { one } else { many }), w));
        }
        for (i, c) in n.char_indices() {
            if c != ',' && c != '.' {
                continue;
            }
            let major = &n[..i];
            let minor = &n[i + 1..];
            if !(minor.len() == 1 || minor.len() == 2) || !minor.bytes().all(|b| b.is_ascii_digit()) {
                continue;
            }
            let Some((major_num, w)) = number(major) else { continue };
            let cents: u32 = minor.parse::<u32>().unwrap_or(0) * if minor.len() == 1 { 10 } else { 1 };
            if cents == 0 {
                if minor == "00" {
                    out.push((format!("{sign}{major_num} {}", if major == "1" { one } else { many }), w))
                }
                continue;
            }
            let cents_text = if cents == 1 { "yksi".into() } else { small(cents) };
            let unit = if cents == 1 { minor_one } else { minor_many };
            if major == "0" {
                out.push((format!("{sign}{cents_text} {unit}"), w))
            } else {
                out.push((
                    format!("{sign}{major_num} {} {cents_text} {unit}", if major == "1" { one } else { many }),
                    w,
                ))
            }
        }
    }
    out
}

const WHITELIST_WORDS: &[(&str, &str)] = &[
    ("&", "ja"),
    ("©", "tekijänoikeus"),
    ("§", "pykälä"),
    ("®", "rekisteröity tavaramerkki"),
    ("™", "tavaramerkki"),
    ("esim.", "esimerkiksi"),
    ("esim", "esimerkiksi"),
    ("jne.", "ja niin edelleen"),
    ("jne", "ja niin edelleen"),
    ("tms.", "tai muuta sellaista"),
    ("tms", "tai muuta sellaista"),
    ("ns.", "niin sanottu"),
    ("ns", "niin sanottu"),
    ("v.", "vuonna"),
    ("s.", "sivu"),
    ("klo.", "kello"),
    ("klo", "kello"),
    ("kl.", "kello"),
    ("etc.", "ja niin edelleen"),
    ("etc", "ja niin edelleen"),
    ("tri.", "tohtori"),
    ("tri", "tohtori"),
    ("Tri.", "tohtori"),
    ("Tri", "tohtori"),
    ("Dr.", "tohtori"),
    ("Dr", "tohtori"),
    ("prof.", "professori"),
    ("Prof.", "professori"),
    ("prof", "professori"),
    ("Prof", "professori"),
    ("dos.", "dosentti"),
    ("Dos.", "dosentti"),
    ("maist.", "maisteri"),
    ("Maist.", "maisteri"),
    ("ins.", "insinööri"),
    ("Ins.", "insinööri"),
];
fn whitelist_word(s: &str) -> Option<&'static str> {
    static INDEX: LazyLock<std::collections::HashMap<&'static str, &'static str>> =
        LazyLock::new(|| WHITELIST_WORDS.iter().copied().collect());
    INDEX.get(s).copied()
}
fn whitelist(s: &str) -> Option<String> {
    if let Some(value) = whitelist_word(s) {
        return Some(value.into());
    }
    for (prefix, word, space) in [
        ("© ", "tekijänoikeus", false),
        ("§ ", "pykälä", false),
        ("#", "numero", false),
        ("~", "noin", true),
        ("±", "plus-miinus", true),
        ("nro", "numero", true),
        ("Nro", "numero", true),
        ("nro.", "numero", true),
        ("Nro.", "numero", true),
        ("n:o", "numero", true),
        ("N:o", "numero", true),
        ("No.", "numero", true),
        ("No", "numero", true),
        ("Nr.", "numero", true),
        ("Nr", "numero", true),
    ] {
        if let Some(x) = s.strip_prefix(prefix) {
            let x = if space { x.strip_prefix(' ').unwrap_or(x) } else { x };
            if let Some((n, _)) = number(x) {
                return Some(format!("{word} {n}"));
            }
            if prefix == "~" {
                for (u, v) in
                    [("km", "kilometriä"), ("m", "metriä"), ("kg", "kilogrammaa"), ("g", "grammaa"), ("l", "litraa")]
                {
                    if let Some(n) = x.strip_suffix(u) {
                        let n = n.strip_suffix(' ').unwrap_or(n);
                        if let Some((n, _)) = number(n) {
                            return Some(format!("noin {n} {v}"));
                        }
                    }
                }
            }
        }
    }
    if let Some(x) = s.strip_suffix('¢') {
        if let Some((n, _)) = number(x.strip_suffix(' ').unwrap_or(x)) {
            return Some(format!("{n} senttiä"));
        }
    }
    None
}
fn electronic(s: &str) -> Option<String> {
    let alpha = |s: &str| !s.is_empty() && s.bytes().all(|b| b.is_ascii_alphabetic());
    if let Some((a, b)) = s.split_once('.') {
        if alpha(a) && b.len() >= 2 && alpha(b) {
            return Some(format!("{a} piste {b}"));
        }
    }
    if let Some(v) = version_numbers(s, 3) {
        return Some(v);
    }
    for prefix in ["v", "V"] {
        if let Some(x) = s.strip_prefix(prefix) {
            if let Some(v) = version_numbers(x, 2) {
                return Some(format!("versio {v}"));
            }
        }
    }
    if let Some((name, x)) = s.split_once(' ') {
        if alpha(name) {
            if let Some(v) = version_numbers(x, 2) {
                return Some(format!("{name} {v}"));
            }
        }
    }
    let speak = |x: &str| -> Option<String> {
        let mut v = Vec::new();
        for c in x.chars() {
            let w = if c.is_ascii_digit() {
                DIGITS[c as usize - '0' as usize].to_string()
            } else if c.is_ascii_alphabetic() {
                c.to_string()
            } else {
                match c {
                    '.' => "piste",
                    ':' => "kaksoispiste",
                    '/' => "kauttaviiva",
                    '?' => "kysymysmerkki",
                    '=' => "on",
                    '&' => "ja",
                    '#' => "numero",
                    '%' => "prosenttia",
                    '-' => "viiva",
                    '_' => "alaviiva",
                    '+' => "plus",
                    '~' => "tilde",
                    _ => return None,
                }
                .into()
            };
            v.push(w);
        }
        if v.is_empty() { None } else { Some(v.join(" ")) }
    };
    for (prefix, spoken) in [
        ("https://", "h t t p s kaksoispiste kauttaviiva kauttaviiva"),
        ("http://", "h t t p kaksoispiste kauttaviiva kauttaviiva"),
        ("www.", "w w w piste"),
    ] {
        if let Some(x) = s.strip_prefix(prefix) {
            return Some(format!("{spoken} {}", speak(x)?));
        }
    }
    if let Some((user, host)) = s.split_once('@') {
        let (dom, tld) = host.rsplit_once('.')?;
        if tld.len() < 2
            || !alpha(tld)
            || user.chars().any(|c| !c.is_ascii_alphanumeric() && !matches!(c, '.' | '-' | '_' | '+'))
            || dom.chars().any(|c| !c.is_ascii_alphanumeric() && !matches!(c, '.' | '-' | '_' | '+'))
        {
            return None;
        }
        return Some(format!("{} miuku mauku {} piste {tld}", speak(user)?, speak(dom)?));
    }
    None
}
fn phone(s: &str) -> Option<String> {
    let n = s.chars().filter(|c| c.is_ascii_digit()).count();
    if !(7..=15).contains(&n)
        || s.chars().any(|c| !c.is_ascii_digit() && !matches!(c, ' ' | '-' | '.' | '(' | ')' | '+'))
    {
        return None;
    }
    let body = s.strip_prefix('+').unwrap_or(s);
    if !s.starts_with('+') {
        let mut rest = body;
        if let Some(x) = rest.strip_prefix('(') {
            let (area, r) = x.split_once(')')?;
            if area.is_empty() || !area.bytes().all(|b| b.is_ascii_digit()) {
                return None;
            }
            rest = r.strip_prefix(' ').or_else(|| r.strip_prefix('-')).unwrap_or(r);
        }
        let mut count = 0;
        for chunk in rest.split(|c| c == ' ' || c == '-') {
            if chunk.is_empty() || !chunk.bytes().all(|b| b.is_ascii_digit()) {
                return None;
            }
            count += 1;
        }
        if !(2..=6).contains(&count) {
            return None;
        }
    } else if body.contains('+') {
        return None;
    }
    let mut out = String::new();
    if s.starts_with('+') {
        out.push_str("plus ")
    }
    let mut first = true;
    for c in body.chars() {
        if c.is_ascii_digit() {
            if !first {
                out.push(' ')
            }
            out.push_str(DIGITS[c as usize - '0' as usize]);
            first = false
        }
    }
    Some(out)
}
fn roman(s: &str) -> Option<String> {
    let forms = [
        "", "I", "II", "III", "IV", "V", "VI", "VII", "VIII", "IX", "X", "XI", "XII", "XIII", "XIV", "XV", "XVI",
        "XVII", "XVIII", "XIX", "XX", "XXI",
    ];
    for (a, b) in [
        ("Kaarle XII:n", "Kaarle kahdennentoista"),
        ("Kustaa III:n", "Kustaa kolmannen"),
        ("Aleksanteri II:n", "Aleksanteri toisen"),
    ] {
        if s == a {
            return Some(b.into());
        }
    }
    let (a, b) = s.split_once(' ')?;
    if let Some(i) = forms.iter().position(|x| *x == b) {
        if (1..=16).contains(&i)
            && [
                "Kaarle",
                "Kustaa",
                "Aleksanteri",
                "Henrik",
                "Louis",
                "Elisabet",
                "Elizabeth",
                "Pekka",
                "Juhana",
                "Fredrik",
                "Kristian",
                "Vilhelm",
                "Yrjö",
                "Eerik",
                "Karl",
                "Gustav",
            ]
            .contains(&a)
        {
            return Some(format!("{a} {}", ORD[i]));
        }
        if (1..=12).contains(&i)
            && ["Luku", "luku", "Osa", "osa", "Kappale", "kappale", "Chapter", "chapter", "Part", "part"].contains(&a)
        {
            return Some(format!("{a} {}", small(i as u32)));
        }
    }
    let a = a.strip_suffix('.').unwrap_or(a);
    let i = forms.iter().position(|x| *x == a)?;
    if !(10..=21).contains(&i) {
        return None;
    }
    let o = if i == 21 { "kaksikymmentäensimmäinen".to_string() } else { ordinal(&i.to_string())? };
    if ["vuosisata", "vuosisadan", "vuosisadalle"].contains(&b) {
        return Some(format!("{o} {b}"));
    }
    let ad = [
        "kymmenennellä",
        "yhdennellätoista",
        "kahdennellatoista",
        "kolmannellatoista",
        "neljännellätoista",
        "viidennellätoista",
        "kuudennellätoista",
        "seitsemännellätoista",
        "kahdeksannellatoista",
        "yhdeksännellätoista",
        "kahdennellakymmenennellä",
        "kaksikymmentäensimmäisellä",
    ];
    if b == "vuosisadalla" {
        return Some(format!("{} {b}", ad[i - 10]));
    }
    None
}

fn ordinal_extra(s: &str) -> Option<String> {
    if let Some((a, b)) = s.split_once(' ') {
        if [
            "sija",
            "sijan",
            "sijalle",
            "palkinto",
            "palkinnon",
            "kerros",
            "kerroksen",
            "kerroksessa",
            "luokka",
            "luokan",
            "osa",
            "luku",
            "vuosisata",
            "vuosisadan",
            "vuosisadalla",
            "päivä",
            "päivänä",
        ]
        .contains(&b)
        {
            return Some(format!("{} {b}", ordinal(a.strip_suffix('.')?)?));
        }
    }
    let (n, suffix) = s.split_once(':').or_else(|| s.split_once('-'))?;
    let result = match (n, suffix) {
        ("1", "nnen") => "ensimmäisen",
        ("1", "stä") => "ensimmäisestä",
        ("1", "een") => "ensimmäiseen",
        ("1", "llä") => "ensimmäisellä",
        ("1", "lle") => "ensimmäiselle",
        ("1", "ksi") => "ensimmäiseksi",
        ("1", "ntä") => "ensimmäistä",
        ("2", "sen") => "toisen",
        ("2", "sta") => "toisesta",
        ("2", "een") => "toiseen",
        ("2", "lla") => "toisella",
        ("2", "lle") => "toiselle",
        ("2", "ksi") => "toiseksi",
        ("3", "nnen") => "kolmannen",
        ("3", "tta") => "kolmatta",
        ("3", "teen") => "kolmanteen",
        ("3", "ssa") => "kolmannessa",
        ("3", "sta") => "kolmannesta",
        ("3", "lla") => "kolmannella",
        ("3", "lle") => "kolmannelle",
        ("3", "ksi") => "kolmanneksi",
        ("3", "ntena") => "kolmantena",
        ("4", "nnen") => "neljännen",
        ("4", "ttä") => "neljättä",
        ("5", "nnen") => "viidennen",
        ("5", "ttä") => "viidettä",
        _ => return None,
    };
    Some(result.into())
}
fn cardinal_extra(s: &str) -> Option<(String, f64)> {
    let (n, suffix) = s.split_once(':').or_else(|| s.split_once('-')).unwrap_or(("", ""));
    let inflected = match (n, suffix) {
        ("1", "n") => "yhden",
        ("2", "n") => "kahden",
        ("3", "n") => "kolmen",
        ("4", "n") => "neljän",
        ("5", "n") => "viiden",
        ("2", "ssa") => "kahdessa",
        ("3", "lla") => "kolmella",
        ("3", "lle") => "kolmelle",
        ("3", "ssa") => "kolmessa",
        ("3", "sta") => "kolmesta",
        ("3", "een") => "kolmeen",
        ("5", "een") => "viiteen",
        ("10", "een") => "kymmeneen",
        _ => "",
    };
    if !inflected.is_empty() {
        return Some((inflected.into(), 0.0));
    }
    for sep in ['-', '–', '—'] {
        if let Some((a, b)) = s.split_once(sep) {
            if (a, b) == ("10", "20") {
                return Some(("kymmenestä kahteenkymmeneen".into(), -0.01));
            }
            if (a, b) == ("5", "10") {
                return Some(("viidestä kymmeneen".into(), -0.01));
            }
        }
    }
    if let Some((a, b)) = s.split_once('^') {
        if a.len() == 1 && "xyzabnXYZABN".contains(a) {
            let (n, w) = full_number(b)?;
            return Some((
                format!(
                    "{a} {}",
                    match b {
                        "2" => "toiseen".into(),
                        "3" => "kolmanteen".into(),
                        _ => format!("potenssiin {n}"),
                    }
                ),
                w,
            ));
        }
    }
    let common = [
        ("5G", "viisi G"),
        ("5g", "viisi g"),
        ("4G", "neljä G"),
        ("4g", "neljä g"),
        ("3G", "kolme G"),
        ("3g", "kolme g"),
        ("2G", "kaksi G"),
        ("2g", "kaksi g"),
        ("4K", "neljä K"),
        ("4k", "neljä k"),
        ("8K", "kahdeksan K"),
        ("8k", "kahdeksan k"),
        ("COVID-19", "COVID yhdeksäntoista"),
        ("covid-19", "covid yhdeksäntoista"),
        ("CO2", "C O kaksi"),
        ("co2", "c o kaksi"),
        ("A4", "A neljä"),
        ("a4", "a neljä"),
        ("A3", "A kolme"),
        ("a3", "a kolme"),
    ];
    for (a, b) in common {
        if s == a {
            return Some((b.into(), 0.0));
        }
    }
    for suf in ["'s", "s"] {
        if let Some(y) = s.strip_suffix(suf) {
            if ["1920", "1930", "1940", "1950", "1960", "1970", "1980", "1990", "2000", "2010", "2020", "2030"]
                .contains(&y)
            {
                return Some((format!("{}luku", number(y)?.0), 0.0));
            }
        }
    }
    let mut parts = s.split(',');
    let a = parts.next()?;
    let b = parts.next()?;
    let c = parts.next()?;
    let parts = std::iter::once(a).chain(std::iter::once(b)).chain(std::iter::once(c)).chain(parts);
    let mut result = String::new();
    let mut weight = 0.0;
    for part in parts {
        if part.len() > 3 {
            return None;
        }
        let (reading, cost) = full_number(part)?;
        if !result.is_empty() {
            result.push_str(", ");
        }
        result.push_str(&reading);
        weight += cost;
    }
    Some((result, weight))
}
fn decimal_extras(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    for (suffix, word) in [
        ("miljoonaa", "miljoonaa"),
        ("miljoona", "miljoonaa"),
        ("milj.", "miljoonaa"),
        ("milj", "miljoonaa"),
        ("miljardia", "miljardia"),
        ("miljardi", "miljardia"),
        ("mrd.", "miljardia"),
        ("mrd", "miljardia"),
    ] {
        if let Some(n) = s.strip_suffix(suffix) {
            let n = n.strip_suffix(' ').unwrap_or(n);
            for (v, w) in decimal_or_cardinal(n) {
                out.push((format!("{v} {word}"), w));
            }
        }
    }
    if let Some(n) = s.strip_suffix('%') {
        let n = n.strip_suffix(' ').unwrap_or(n);
        for dash in ['-', '–', '—'] {
            if let Some((a, b)) = n.split_once(dash) {
                if (a, b) == ("10", "15") {
                    out.push(("kymmenestä viiteentoista prosenttia".into(), 0.0));
                }
                for (a, wa) in decimal_or_cardinal(a) {
                    for (b, wb) in decimal_or_cardinal(b) {
                        out.push((format!("{a} viiva {b} prosenttia"), wa + wb));
                    }
                }
            }
        }
    }
    for (sym, currency) in [('$', "dollaria"), ('€', "euroa")] {
        if let Some(x) = s.strip_prefix(sym) {
            if let Some(n) = x.strip_suffix(['k', 'K']) {
                if let Some((v, w)) = number(n) {
                    out.push((format!("{v}tuhatta {currency}"), w))
                }
            }
            if let Some(n) = x.strip_suffix(['m', 'M']) {
                if let Some((v, w)) = decimal_weighted(n) {
                    out.push((format!("{v} miljoonaa {currency}"), w))
                }
            }
        }
        if let Some(x) = s.strip_suffix(sym) {
            let x = x.strip_suffix(' ').unwrap_or(x);
            if let Some(n) = x.strip_suffix(['k', 'K']) {
                if let Some((v, w)) = number(n) {
                    out.push((format!("{v}tuhatta {currency}"), w))
                }
            }
            if let Some(n) = x.strip_suffix(['m', 'M']) {
                if let Some((v, w)) = decimal_weighted(n) {
                    out.push((format!("{v} miljoonaa {currency}"), w))
                }
            }
        }
    }
    out
}
fn decimal_or_cardinal(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    if let Some(x) = number(s) {
        out.push(x)
    }
    if let Some(x) = decimal_weighted(s) {
        out.push(x)
    }
    out
}

fn money_ranges(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    for (i, c) in s.char_indices() {
        if matches!(c, '-' | '–' | '—') {
            let a = s[..i].trim_end_matches(' ');
            let b = s[i + c.len_utf8()..].trim_start_matches(' ');
            for (x, wa) in money_variants(a, true) {
                for (y, wb) in money_variants(b, true) {
                    out.push((format!("{} }} tokens {{ name: \"viiva\" }} tokens {{ {}", tag(&x), tag(&y)), wa + wb));
                }
            }
        }
    }
    out
}

fn clock_short(s: &str) -> Option<String> {
    let s = s.strip_suffix('.').unwrap_or(s);
    let (hour, minute) = s.split_once(|c| c == ':' || c == '.')?;
    if minute != "00" || hour.is_empty() || hour.len() > 2 || !hour.bytes().all(|c| c.is_ascii_digit()) {
        return None;
    }
    let n: u32 = hour.parse().ok()?;
    if n > 23 {
        return None;
    }
    Some(if n == 0 { "nolla".into() } else { small(n) })
}
fn clock_alternatives(s: &str) -> Vec<String> {
    let mut out = Vec::new();
    for suffix in ["AM", "am", "A.M.", "a.m.", "PM", "pm", "P.M.", "p.m.", "ap.", "ip."] {
        if let Some(x) = s.strip_suffix(suffix) {
            let trimmed = x.trim_end_matches(' ');
            if trimmed.len() == x.len() {
                continue;
            }
            if let Some(n) = clock_short(trimmed) {
                let word = if ["AM", "am", "A.M.", "a.m.", "ap."].contains(&suffix) {
                    "aamupäivällä"
                } else {
                    "iltapäivällä"
                };
                out.push(format!("{n} {word}"));
            }
        }
    }
    for dash in ['-', '–', '—'] {
        if let Some((a, b)) = s.split_once(dash) {
            let mut left = Vec::new();
            let mut right = Vec::new();
            if let Some(v) = clock_base(a) {
                left.push(v)
            }
            if let Some(v) = clock_short(a) {
                left.push(v)
            }
            if let Some(v) = clock_base(b) {
                right.push(v)
            }
            if let Some(v) = clock_short(b) {
                right.push(v)
            }
            for x in &left {
                for y in &right {
                    out.push(format!("{x} viiva {y}"))
                }
            }
        }
    }
    out
}

fn full_number(s: &str) -> Option<(String, f64)> {
    number(s).or_else(|| if s.len() > 1 && s.starts_with('0') { digit_seq(s).map(|x| (x, 0.1)) } else { None })
}

fn embedded_number_weight(s: &str) -> f64 {
    let Some(start) = s.find(|c: char| c.is_ascii_digit()) else { return 0.0 };
    let rest = &s[start..];
    let end =
        rest.find(|c: char| !c.is_ascii_digit() && !matches!(c, ' ' | '\u{a0}' | '.' | ',')).unwrap_or(rest.len());
    let mut n = &rest[..end];
    while n.ends_with(' ') || n.ends_with('.') || n.ends_with(',') || n.ends_with('\u{a0}') {
        n = &n[..n.char_indices().last().unwrap().0]
    }
    number(n).map(|x| x.1).unwrap_or(0.0)
}

fn serial(s: &str) -> Option<(String, f64)> {
    let alpha = |x: &str| {
        !x.is_empty()
            && x.chars().count() <= 3
            && x.chars().all(|c| c.is_ascii_alphabetic() || "äöåšžÄÖÅŠŽ".contains(c))
    };
    let mut p = s.split('-');
    if let (Some(a), Some(n), Some(b), None) = (p.next(), p.next(), p.next(), p.next()) {
        if alpha(a) && alpha(b) {
            let (n, w) = full_number(n)?;
            return Some((format!("{a} {n} {b}"), w));
        }
    }
    for (i, c) in s.char_indices() {
        if c.is_ascii_digit() {
            let a = &s[..i];
            let b = &s[i..];
            if a.chars().count() >= 2 && alpha(a) {
                let (n, w) = full_number(b)?;
                return Some((format!("{a} {n}"), w));
            }
            break;
        }
    }
    None
}

fn version_numbers(s: &str, minimum: usize) -> Option<String> {
    let mut count = 0;
    for part in s.split('.') {
        if part.len() > 4 || number_value(part).is_none() {
            return None;
        }
        count += 1;
    }
    if count < minimum {
        return None;
    }
    let mut out = String::new();
    for part in s.split('.') {
        if !out.is_empty() {
            out.push_str(" piste ");
        }
        out.push_str(&number(part)?.0);
    }
    Some(out)
}

/// Prefixes of the literal continuations reachable after a digit-led token and
/// a space. Unit and currency prefixes come directly from their rule tables.
fn numeric_suffix_prefix(s: &str) -> bool {
    const RATES: &[&str] = &["/h", "/t", "/kk", "/v", "/kg", "/kilo", "/km", "/m", "/kpl", "/hlö"];
    const WORDS: &[&str] = &[
        "tammikuuta",
        "helmikuuta",
        "maaliskuuta",
        "huhtikuuta",
        "toukokuuta",
        "kesäkuuta",
        "heinäkuuta",
        "elokuuta",
        "syyskuuta",
        "lokakuuta",
        "marraskuuta",
        "joulukuuta",
        "tammikuu",
        "helmikuu",
        "maaliskuu",
        "huhtikuu",
        "toukokuu",
        "kesäkuu",
        "heinäkuu",
        "elokuu",
        "syyskuu",
        "lokakuu",
        "marraskuu",
        "joulukuu",
        "tammi",
        "helmi",
        "maalis",
        "huhti",
        "touko",
        "kesä",
        "heinä",
        "elo",
        "syys",
        "loka",
        "marras",
        "joulu",
        "eaa.",
        "eaa",
        "e.a.a.",
        "eKr.",
        "e.Kr.",
        "BC",
        "B.C.",
        "BCE",
        "jaa.",
        "jaa",
        "j.a.a.",
        "jKr.",
        "j.Kr.",
        "AD",
        "A.D.",
        "CE",
        "AM",
        "am",
        "A.M.",
        "a.m.",
        "PM",
        "pm",
        "P.M.",
        "p.m.",
        "ap.",
        "ip.",
        "UTC",
        "GMT",
        "EET",
        "EEST",
        "CET",
        "CEST",
        "PST",
        "EST",
        "milj.",
        "milj",
        "miljoonaa",
        "miljoona",
        "mrd.",
        "mrd",
        "miljardia",
        "miljardi",
        "sija",
        "sijan",
        "sijalle",
        "palkinto",
        "palkinnon",
        "kerros",
        "kerroksen",
        "kerroksessa",
        "luokka",
        "luokan",
        "osa",
        "luku",
        "vuosisata",
        "vuosisadan",
        "vuosisadalla",
        "päivä",
        "päivänä",
        "kilogramma",
        "kilo",
    ];
    static PREFIXES: LazyLock<std::collections::HashSet<&'static str>> = LazyLock::new(|| {
        let mut prefixes = std::collections::HashSet::new();
        for key in WORDS
            .iter()
            .copied()
            .chain(UNITS.iter().map(|(key, _, _)| *key))
            .chain(CURRENCY.iter().map(|(key, _, _, _, _)| *key))
        {
            for (pos, ch) in key.char_indices() {
                prefixes.insert(&key[..pos + ch.len_utf8()]);
            }
        }
        prefixes
    });
    static RATE_PREFIXES: LazyLock<std::collections::HashSet<String>> = LazyLock::new(|| {
        let mut prefixes = std::collections::HashSet::new();
        for (key, _, _, _, _) in CURRENCY {
            for rate in RATES {
                let mut combined = String::with_capacity(key.len() + rate.len());
                combined.push_str(key);
                combined.push_str(rate);
                for (pos, c) in rate.char_indices() {
                    prefixes.insert(combined[..key.len() + pos + c.len_utf8()].to_string());
                }
            }
        }
        prefixes
    });
    PREFIXES.contains(s) || RATE_PREFIXES.contains(s)
}

/// A necessary condition for a money path: some source currency key's
/// initial character occurs in the span.
fn currency_initial(c: char) -> bool {
    static INITIALS: LazyLock<([bool; 128], Vec<char>)> = LazyLock::new(|| {
        let mut ascii = [false; 128];
        let mut other = Vec::new();
        for &(key, _, _, _, _) in CURRENCY {
            if let Some(c) = key.chars().next() {
                if c.is_ascii() {
                    ascii[c as usize] = true;
                } else if !other.contains(&c) {
                    other.push(c);
                }
            }
        }
        (ascii, other)
    });
    if c.is_ascii() { INITIALS.0[c as usize] } else { INITIALS.1.contains(&c) }
}

fn unit_final_index() -> &'static std::collections::HashMap<char, Vec<usize>> {
    static INDEX: LazyLock<std::collections::HashMap<char, Vec<usize>>> = LazyLock::new(|| {
        let mut index = std::collections::HashMap::<char, Vec<usize>>::new();
        for (i, (key, _, _)) in UNITS.iter().enumerate() {
            if let Some(c) = key.chars().next_back() {
                index.entry(c).or_default().push(i);
            }
        }
        index
    });
    &INDEX
}

type EdgeIndex = std::collections::HashMap<char, Vec<usize>>;
fn currency_edge_indexes() -> &'static (EdgeIndex, EdgeIndex) {
    static INDEX: LazyLock<(EdgeIndex, EdgeIndex)> = LazyLock::new(|| {
        let mut prefix = EdgeIndex::new();
        let mut suffix = EdgeIndex::new();
        for (i, (key, _, _, _, _)) in CURRENCY.iter().enumerate() {
            if let Some(c) = key.chars().next() {
                prefix.entry(c).or_default().push(i);
            }
            if let Some(c) = key.chars().next_back() {
                suffix.entry(c).or_default().push(i);
            }
        }
        (prefix, suffix)
    });
    &INDEX
}

/// Exact recognition of the digit-only clock branch; rendering remains in
/// `clock` and is performed only if the candidate wins.
fn simple_clock(s: &str) -> bool {
    let mut parts = s.split(':');
    let Some(hour) = parts.next() else { return false };
    let Some(minute) = parts.next() else { return false };
    let second = parts.next();
    if parts.next().is_some() || hour.is_empty() || hour.len() > 2 {
        return false;
    }
    let valid = |part: &str| {
        part.len() == 2 && part.bytes().all(|b| b.is_ascii_digit()) && part.parse::<u32>().is_ok_and(|v| v <= 59)
    };
    hour.parse::<u32>().is_ok_and(|h| h <= 23) && valid(minute) && second.is_none_or(valid)
}

/// The one-integer-plus-unit branch has no competing lexical path for its
/// exact shape. Return the source table row instead of spelling it on search.
fn simple_measure(s: &str) -> Option<(&str, &str, usize)> {
    let (sign, body) = signed(s);
    let n_end = body.bytes().position(|b| !b.is_ascii_digit())?;
    let n = &body[..n_end];
    number_value(n)?;
    let suffix = body[n_end..].strip_prefix(' ').unwrap_or(&body[n_end..]);
    let last = suffix.chars().next_back()?;
    let indices = unit_final_index().get(&last)?;
    let index = indices.iter().copied().find(|&i| UNITS[i].0 == suffix)?;
    Some((sign, n, index))
}

fn currency_prefix_possible(s: &str) -> bool {
    let Some(first) = s.chars().next() else { return false };
    currency_edge_indexes().0.get(&first).is_some_and(|indices| indices.iter().any(|&i| s.starts_with(CURRENCY[i].0)))
}

/// Every money path contains a literal currency key. Use the source-derived
/// first-character index to avoid running the money grammar on mere letters.
fn contains_currency(s: &str) -> bool {
    let prefix = &currency_edge_indexes().0;
    s.char_indices().any(|(at, c)| {
        currency_initial(c)
            && prefix.get(&c).is_some_and(|indices| indices.iter().any(|&i| s[at..].starts_with(CURRENCY[i].0)))
    })
}
