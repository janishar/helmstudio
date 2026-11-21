//! Polish text normalization, ported from the production NeMo grammar.
mod context;
mod datetime;
mod number_names;
mod numeric;
mod quantities;
mod serial;

use crate::lang::common::{Fields, Punctuation, possible_token_end, possible_token_start, verbalize_name};
use crate::lattice::{Candidate, Classifier, Shape};
use crate::pipeline::Grammar;
use crate::tag::Dict;
use crate::text::{cost, is_nemo_space};
use std::sync::LazyLock;

struct Polish {
    classifiers: Vec<Box<dyn Classifier>>,
    punct: PolishPunctuation,
}
struct Branch<const N: u8>;
// Unlike the English punctuation graph, Polish has no emphasis-tag branch.
struct PolishPunctuation {
    inner: Punctuation,
}
impl Classifier for PolishPunctuation {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        for end in start + 1..=text.len() {
            let c = text[end - 1];
            if !self.inner.is_mark(c) {
                break;
            }
            out.push(Candidate { end, cost: cost(1.1), rank: 0, tag: String::new() });
        }
    }
    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        let mut tag = String::with_capacity(8 + candidate.end - start);
        tag.push_str("name: \"");
        tag.extend(&text[start..candidate.end]);
        tag.push('"');
        candidate.tag = tag;
    }
}
fn numeric_start(c: char) -> bool {
    c.is_ascii_digit() || matches!(c, '-' | '−' | '~' | '±')
}

fn fraction_start(c: char) -> bool {
    numeric_start(c) || "½⅓⅔¼¾⅕⅖⅗⅘⅙⅚⅛⅜⅝⅞⅐⅑⅒".contains(c)
}

// Recognition for the overwhelmingly common cardinal paths. Other expressions are
// handed to the full grammar; no number names are built during lattice exploration.
fn simple_cardinal(s: &str) -> Option<bool> {
    let (body, sign) = numeric::signed(s);
    if !body.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    if body.is_empty() {
        return Some(false);
    }
    if body.len() <= 12 && (body == "0" || !body.starts_with('0')) {
        return Some(true);
    }
    Some(sign.is_empty() && ((body.starts_with('0') && body.len() > 1) || body.len() >= 13))
}
fn simple_time(s: &str) -> Option<usize> {
    let (hour, minute) = s.split_once(':')?;
    if hour.is_empty()
        || minute.is_empty()
        || !hour.bytes().all(|b| b.is_ascii_digit())
        || !minute.bytes().all(|b| b.is_ascii_digit())
    {
        return None;
    }
    if hour.len() > 2 || minute.len() != 2 {
        return Some(0);
    }
    let h = hour.parse::<u8>().ok()?;
    if h > 23 {
        return Some(0);
    }
    Some(if s == "00:00" { 2 } else { 1 })
}

fn branch_starts(branch: u8, text: &[char], start: usize) -> bool {
    let c = text[start];
    match branch {
        0 => c.is_alphanumeric() || "&©§+=×÷№".contains(c),
        1 => c.is_alphabetic(),
        2 => c.is_ascii_digit() || c == 'l',
        3 | 8 => c.is_ascii_digit(),
        4 => numeric_start(c) || quantities::currency_start(c),
        5 | 6 => fraction_start(c),
        7 => numeric_start(c),
        9 => numeric_start(c) || (c.is_ascii_alphabetic() && text.get(start + 1) == Some(&'^')),
        10 | 12 => c.is_ascii_alphanumeric(),
        11 => c.is_ascii_digit() || c == '+' || c == '(',
        _ => true,
    }
}
thread_local! {
    static SCAN_BUFFER: std::cell::RefCell<String> = const { std::cell::RefCell::new(String::new()) };
    static TOKEN_COSTS: std::cell::RefCell<TokenCosts> = const {
        std::cell::RefCell::new(TokenCosts { input: 0, len: 0, start: 0, generation: 0, slots: Vec::new() })
    };
}

// Candidate costs from earlier union branches at this same start.  A strictly
// cheaper token on the same span has the identical lattice transition, so a
// later branch whose complete minimum cost is higher cannot win that span.
struct TokenCosts {
    input: usize,
    len: usize,
    start: usize,
    generation: u64,
    slots: Vec<(u64, crate::text::Cost)>,
}
impl TokenCosts {
    fn reset(&mut self, text: &[char], start: usize) {
        self.input = text.as_ptr() as usize;
        self.len = text.len();
        self.start = start;
        self.generation = self.generation.wrapping_add(1);
        if self.generation == 0 {
            self.slots.clear();
            self.generation = 1;
        }
        self.slots.resize(text.len() + 1, (0, 0));
    }
    fn dominated(&self, end: usize, min_cost: crate::text::Cost) -> bool {
        let (generation, best) = self.slots[end];
        generation == self.generation && best < min_cost
    }
    fn record(&mut self, candidates: &[Candidate]) {
        for c in candidates {
            if let Some(slot) = self.slots.get_mut(c.end) {
                if slot.0 != self.generation || c.cost < slot.1 {
                    *slot = (self.generation, c.cost);
                }
            }
        }
    }
}

/// Reuse UTF-8 scan storage without imposing a source-span length limit.
struct Scratch {
    buffer: String,
}
impl Scratch {
    fn new() -> Self {
        Self { buffer: SCAN_BUFFER.with_borrow_mut(std::mem::take) }
    }
    fn push(&mut self, c: char) {
        self.buffer.push(c);
    }
    fn as_str(&self) -> &str {
        &self.buffer
    }
}
impl Drop for Scratch {
    fn drop(&mut self) {
        self.buffer.clear();
        SCAN_BUFFER.with_borrow_mut(|buffer| {
            if self.buffer.capacity() > buffer.capacity() {
                *buffer = std::mem::take(&mut self.buffer);
            }
        });
    }
}
impl<const N: u8> Branch<N> {
    fn candidates_inner(&self, text: &[char], start: usize, out: &mut Vec<Candidate>, earlier: &TokenCosts) {
        // Money's zero-major branch has -0.2; serial's fixed branch has -0.5.
        // Other branch minima are their top-level union weights.
        let minimum = cost(match N {
            0 => 1.01,
            1 => 1.04,
            2 => 1.06,
            3 => 1.07,
            4 => 0.88,
            5 | 6 => 1.09,
            7..=9 => 1.10,
            10 | 11 => 1.11,
            12 => 1.5,
            _ => 100.0,
        });
        if N == 13 {
            for end in start + 1..=text.len() {
                if is_nemo_space(text[end - 1]) {
                    break;
                }
                if possible_token_end(text, end) && !earlier.dominated(end, minimum) {
                    out.push(Candidate { end, cost: cost(100.0), rank: 0, tag: String::new() });
                }
            }
            return;
        }
        let mut scratch = Scratch::new();
        let mut unit_start = None;
        let mut date_alpha = None;
        let mut money_alpha = None;
        let mut cardinal_alpha = false;
        let mut phone_digits = 0usize;
        let mut fraction_mark = false;
        let mut decimal_mark = false;
        for end in start + 1..=text.len() {
            let c = text[end - 1];
            if N == 11 && c.is_ascii_digit() {
                phone_digits += 1;
            }
            if N == 6 && (c == '/' || "½⅓⅔¼¾⅕⅖⅗⅘⅙⅚⅛⅜⅝⅞⅐⅑⅒".contains(c)) {
                fraction_mark = true;
            }
            if N == 7 && matches!(c, '.' | ',' | '%' | '‰') {
                decimal_mark = true;
            }
            // Neither date nor money accepts a colon.
            if matches!(N, 2 | 4) && c == ':' {
                break;
            }
            if N == 6 && !(c.is_ascii_digit() || " .\u{a0}/-−~±½⅓⅔¼¾⅕⅖⅗⅘⅙⅚⅛⅜⅝⅞⅐⅑⅒".contains(c))
            {
                break;
            }
            if N == 7 && !(c.is_ascii_digit() || " .\u{a0},%-−–—~±‰".contains(c)) {
                break;
            }
            if matches!(N, 6 | 7) && c.is_alphabetic() {
                break;
            }
            if N == 11 && !(c.is_ascii_digit() || " +-().".contains(c)) {
                break;
            }
            if N == 12 && !(c.is_ascii_alphanumeric() || "-/:_.".contains(c)) {
                break;
            }
            if N == 10 && !(c.is_ascii_alphanumeric() || "-/:_.@".contains(c)) {
                break;
            }
            if N == 3 && !(c.is_ascii_digit() || " :.-−–—+AMPMampUTCGESD".contains(c)) {
                break;
            }
            if matches!(N, 0 | 8 | 10 | 12) && is_nemo_space(c) {
                break;
            }
            scratch.push(c);
            let s = scratch.as_str();
            if N == 1 && !context::roman_prefix(s) {
                break;
            }
            if N == 9 {
                cardinal_alpha |= c.is_alphabetic();
                if cardinal_alpha && !numeric::cardinal_alpha_prefix(&s) {
                    break;
                }
            }
            if N == 2 {
                if date_alpha.is_none() && c.is_alphabetic() {
                    date_alpha = Some(s.len() - c.len_utf8());
                }
                if let Some(i) = date_alpha {
                    if !datetime::alphabetic_prefix(&s[i..]) {
                        break;
                    }
                }
            }
            if N == 4 {
                if money_alpha.is_none() && c.is_alphabetic() {
                    money_alpha = Some(s.len() - c.len_utf8());
                }
                if let Some(i) = money_alpha {
                    let after_slash = i > 0 && s.as_bytes()[i - 1] == b'/';
                    if !quantities::money_alpha_prefix(&s[i..], after_slash) {
                        break;
                    }
                }
            }
            if N == 5 {
                if unit_start.is_none() && c.is_alphabetic() {
                    unit_start = Some(s.len() - c.len_utf8());
                }
                if let Some(i) = unit_start {
                    if !quantities::unit_prefix(&s[i..]) {
                        break;
                    }
                }
            }
            if N == 0 && !context::whitelist_prefix(&s) {
                break;
            }
            if !possible_token_end(text, end) {
                continue;
            }
            if earlier.dominated(end, minimum) {
                continue;
            }
            if N == 12 && s.bytes().all(|b| b.is_ascii_alphabetic()) {
                out.push(Candidate { end, cost: cost(2.0), rank: 0, tag: String::new() });
                continue;
            }
            if N == 9 {
                if let Some(accepted) = simple_cardinal(&s) {
                    if accepted {
                        out.push(Candidate { end, cost: cost(1.1), rank: 0, tag: String::new() });
                    }
                    continue;
                }
                if let Some(extra) = numeric::simple_binary(&s) {
                    out.push(Candidate { end, cost: cost(1.1) + cost(extra), rank: 0, tag: String::new() });
                    continue;
                }
            }
            if N == 1 && !s.contains(['I', 'V', 'X']) {
                continue;
            }
            if N == 2 && s.bytes().all(|b| b.is_ascii_digit()) {
                continue;
            }
            if N == 10 && !s.contains(['.', '@']) && !s.contains("://") {
                continue;
            }
            if N == 3 && !s.contains(':') {
                continue;
            }
            if N == 5 && !quantities::unit_end(c) {
                continue;
            }
            if N == 4 {
                let (body, _) = numeric::signed(&s);
                let leading_currency = body.chars().next().is_some_and(quantities::currency_start);
                if !leading_currency && !quantities::currency_end(c) && !s.contains('/') {
                    continue;
                }
            }
            if N == 6 && !(c.is_numeric() || "½⅓⅔¼¾⅕⅖⅗⅘⅙⅚⅛⅜⅝⅞⅐⅑⅒".contains(c)) {
                continue;
            }
            if N == 7 && !(c.is_numeric() || matches!(c, '%' | '‰')) {
                continue;
            }
            if N == 8 && !matches!(c, '.' | 'y' | 'i' | 'a') {
                continue;
            }
            if N == 9 && !c.is_numeric() {
                continue;
            }
            if N == 11 && !c.is_numeric() {
                continue;
            }
            if N == 11 && phone_digits < 7 {
                continue;
            }
            if N == 6 && !fraction_mark {
                continue;
            }
            if N == 7 && !decimal_mark {
                continue;
            }
            if N == 12 {
                let mut rank = 0;
                serial::recognize(&s, |extra| {
                    out.push(Candidate { end, cost: cost(2.0) + cost(extra), rank, tag: String::new() });
                    rank += 1;
                });
                continue;
            }
            if N == 3 {
                if let Some(count) = simple_time(&s) {
                    for rank in 0..count {
                        out.push(Candidate { end, cost: cost(1.07), rank: rank as i32, tag: String::new() });
                    }
                    continue;
                }
            }
            if N == 4 && quantities::simple_money(&s) {
                out.push(Candidate { end, cost: cost(1.08), rank: 0, tag: String::new() });
                continue;
            }
            if N == 5 && quantities::simple_measure(&s) {
                out.push(Candidate { end, cost: cost(1.09), rank: 0, tag: String::new() });
                continue;
            }
            let (base, values) = match N {
                0 => (1.01, context::whitelist(&s)),
                1 => (1.04, context::roman(&s)),
                2 => (1.06, datetime::date(&s)),
                3 => (1.07, datetime::time(&s)),
                4 => (1.08, quantities::money(&s)),
                5 => (1.09, quantities::measure(&s)),
                6 => (1.09, numeric::fraction(&s)),
                7 => (1.10, numeric::decimal(&s)),
                8 => (1.10, numeric::ord(&s)),
                9 => (1.10, numeric::card(&s)),
                10 => (1.11, context::electronic(&s)),
                11 => (1.11, context::telephone(&s)),
                _ => (2.0, serial::serial(&s)),
            };
            for (rank, (word, extra)) in values.into_iter().enumerate() {
                out.push(Candidate {
                    end,
                    cost: cost(base) + cost(extra),
                    rank: rank as i32,
                    tag: format!("name: \"{}\"", word.replace(' ', "\u{a0}")),
                });
            }
            if N == 2 {
                if let Some((year, month, day)) = datetime::iso(&s) {
                    out.push(Candidate {
                        end,
                        cost: cost(1.06),
                        rank: 0,
                        tag: format!("date {{ year: \"{year}\" month: \"{month}\" day: \"{day}\" }}"),
                    });
                }
            }
        }
    }
}
impl<const N: u8> Classifier for Branch<N> {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        let allowed = possible_token_start(text, start) && branch_starts(N, text, start);
        // Branch zero starts a new same-position cost epoch even if it does not
        // recognize this initial character. Other impossible starts need no TLS.
        if N != 0 && !allowed {
            return;
        }
        TOKEN_COSTS.with_borrow_mut(|earlier| {
            if N == 0 || earlier.input != text.as_ptr() as usize || earlier.len != text.len() || earlier.start != start
            {
                earlier.reset(text, start);
            }
            if !allowed {
                return;
            }
            let first = out.len();
            self.candidates_inner(text, start, out, earlier);
            earlier.record(&out[first..]);
        });
    }
    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        if !candidate.tag.is_empty() {
            return;
        }
        if N == 13 || (N == 12 && text[start..candidate.end].iter().all(|c| c.is_ascii_alphabetic())) {
            let mut tag = String::with_capacity(8 + candidate.end - start);
            tag.push_str("name: \"");
            tag.extend(&text[start..candidate.end]);
            tag.push('"');
            candidate.tag = tag;
        } else {
            let word: String = text[start..candidate.end].iter().collect();
            let spoken = match N {
                3 => datetime::simple_time_words(&word, candidate.rank),
                4 => quantities::simple_money_words(&word),
                5 => quantities::simple_measure_words(&word),
                9 if simple_cardinal(&word) == Some(true) => numeric::simple_cardinal_words(&word),
                9 => numeric::card(&word).into_iter().nth(candidate.rank as usize).map(|(spoken, _)| spoken),
                12 => serial::serial(&word).into_iter().nth(candidate.rank as usize).map(|(spoken, _)| spoken),
                _ => None,
            };
            if let Some(spoken) = spoken {
                let mut tag = String::with_capacity(8 + spoken.len());
                tag.push_str("name: \"");
                for c in spoken.chars() {
                    tag.push(if c == ' ' { '\u{a0}' } else { c });
                }
                tag.push('"');
                candidate.tag = tag;
            }
        }
    }
}
impl Grammar for Polish {
    fn code(&self) -> &'static str {
        "pl"
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
        if let Some(v) = verbalize_name(token) {
            return Some(v);
        }
        let mut outer = Fields::new(token);
        let date = outer.dict("date")?;
        if !outer.done() {
            return None;
        }
        let mut f = Fields::new(date);
        let day = f.take("day")?;
        let month = f.take("month")?;
        let year = f.take("year")?;
        if !f.done() || day.is_empty() || month.is_empty() || year.is_empty() {
            return None;
        }
        let mut out = String::with_capacity(day.len() + month.len() + year.len() + 2);
        for (i, field) in [day, month, year].into_iter().enumerate() {
            if i != 0 {
                out.push(' ');
            }
            if field.contains('\u{a0}') {
                for c in field.chars() {
                    out.push(if c == '\u{a0}' { ' ' } else { c });
                }
            } else {
                out.push_str(field);
            }
        }
        Some(out)
    }
    fn verbalize_split(&self, split: &[Dict]) -> Option<String> {
        let mut out = String::with_capacity(split.len() * 16);
        for (i, token) in split.iter().enumerate() {
            let [(key, crate::tag::Value::Dict(body))] = token.as_slice() else {
                return None;
            };
            if key != "tokens" {
                return None;
            }
            if i != 0 {
                out.push(' ');
            }
            if let [(name, crate::tag::Value::Str(value))] = body.as_slice() {
                if name == "name" {
                    if value.contains(' ') {
                        return None;
                    }
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
            let value = crate::permute::first_ordering(body, &mut |ordering: &Dict| self.verbalize(ordering))?;
            out.push_str(&value);
        }
        Some(out)
    }
}
static GRAMMAR: LazyLock<Polish> = LazyLock::new(|| Polish {
    classifiers: vec![
        Box::new(Branch::<0>) as Box<dyn Classifier>,
        Box::new(Branch::<1>),
        Box::new(Branch::<2>),
        Box::new(Branch::<3>),
        Box::new(Branch::<4>),
        Box::new(Branch::<5>),
        Box::new(Branch::<6>),
        Box::new(Branch::<7>),
        Box::new(Branch::<8>),
        Box::new(Branch::<9>),
        Box::new(Branch::<10>),
        Box::new(Branch::<11>),
        Box::new(Branch::<12>),
        Box::new(Branch::<13>),
    ],
    punct: PolishPunctuation {
        inner: Punctuation::new("!#%&'()*+,-./:;<=>?@^_`{|}~\"„”«»…", true, std::iter::empty(), cost(1.1)),
    },
});
pub fn grammar() -> &'static dyn Grammar {
    &*GRAMMAR
}
