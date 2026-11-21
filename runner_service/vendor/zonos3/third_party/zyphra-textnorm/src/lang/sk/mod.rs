//! Slovak text-normalization grammar.
// Exploration validates paths and costs without allocating their spoken output.
// The guard restores the previous mode even if a recognizer panics.
thread_local! {
    static BUILDING: std::cell::Cell<bool> = const { std::cell::Cell::new(true) };
    // Branch-local ordinal for each explored candidate; no spoken strings are stored.
    static READINGS: std::cell::RefCell<Vec<(usize, usize, i32, usize)>> = const { std::cell::RefCell::new(Vec::new()) };
    static SCRATCH: std::cell::RefCell<String> = const { std::cell::RefCell::new(String::new()) };
}
fn building() -> bool {
    BUILDING.with(std::cell::Cell::get)
}
struct Restore(bool);
impl Drop for Restore {
    fn drop(&mut self) {
        BUILDING.with(|v| v.set(self.0));
    }
}
struct Scratch(String);
impl Scratch {
    fn take() -> Self {
        Self(SCRATCH.with_borrow_mut(std::mem::take))
    }
}
impl Drop for Scratch {
    fn drop(&mut self) {
        self.0.clear();
        SCRATCH.with_borrow_mut(|slot| *slot = std::mem::take(&mut self.0));
    }
}
fn recognize<T>(f: impl FnOnce() -> T) -> T {
    let previous = BUILDING.with(|v| v.replace(false));
    let _restore = Restore(previous);
    f()
}
macro_rules! spoken {
    ($($args:tt)*) => {{
        if super::building() {
            format!($($args)*)
        } else {
            let _ = format_args!($($args)*);
            String::new()
        }
    }};
}
mod date;
mod electronic;
mod measure;
mod money;
mod number;
mod numeric;
mod ordfrac;
mod roman;
mod time;
mod whitelist;

use crate::lang::common::{self, Fields, Punctuation};
use crate::lattice::{Candidate, Classifier, Shape};
use crate::pipeline::Grammar;
use crate::tag::{Dict, Value};
use crate::text::{cost, is_nemo_space};
use std::sync::LazyLock;

struct Sk {
    classifiers: Vec<Box<dyn Classifier>>,
    punct: Punctuation,
}
impl Grammar for Sk {
    fn code(&self) -> &'static str {
        "sk"
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
        if let Some(name) = common::verbalize_name(token) {
            return Some(name);
        }
        let mut outer = Fields::new(token);
        let d = outer.dict("date")?;
        if !outer.done() {
            return None;
        }
        let mut f = Fields::new(d);
        let day = f.take("day")?;
        let month = f.take("month")?;
        let year = f.take("year")?;
        if !f.done() {
            return None;
        }
        Some(format!("{day} {month} {year}").replace('\u{a0}', " "))
    }
    fn verbalize_split(&self, split: &[Dict]) -> Option<String> {
        let mut out = String::with_capacity(split.len() * 16);
        for (i, token) in split.iter().enumerate() {
            let [(key, Value::Dict(body))] = token.as_slice() else { return None };
            if key != "tokens" {
                return None;
            }
            if i != 0 {
                out.push(' ');
            }
            if let [(key, Value::Str(value))] = body.as_slice() {
                if key == "name" && !value.contains(' ') {
                    out.extend(value.chars().map(|c| if c == '\u{a0}' { ' ' } else { c }));
                    continue;
                }
            }
            out.push_str(&crate::permute::first_ordering(body, &mut |fields| self.verbalize(fields))?);
        }
        Some(out)
    }
}
struct Union;
fn name(s: &str) -> String {
    if !building() {
        return String::new();
    }
    let mut out = String::with_capacity(s.len() + 8 + s.bytes().filter(|b| *b == b' ').count());
    out.push_str("name: \"");
    out.extend(s.chars().map(|c| if c == ' ' { '\u{a0}' } else { c }));
    out.push('"');
    out
}
#[derive(Default)]
struct SpanFlags {
    digit: bool,
    not_digit: bool,
    non_numeric: bool,
    non_alpha: bool,
    space: bool,
    date_char: bool,
    dot: bool,
    comma: bool,
    at: bool,
    slash: bool,
    colon: bool,
    dash: bool,
    vulgar: bool,
    money_mask: u64,
}
impl SpanFlags {
    fn push(&mut self, c: char) {
        self.digit |= c.is_ascii_digit();
        self.not_digit |= !c.is_ascii_digit();
        let alpha = c.is_alphabetic();
        self.non_numeric |= alpha
            || matches!(
                c,
                '€' | '$'
                    | '£'
                    | '¥'
                    | '₽'
                    | '₩'
                    | '₪'
                    | '₴'
                    | '₫'
                    | '৳'
                    | '₦'
                    | '₱'
                    | '฿'
                    | '֏'
                    | '₺'
                    | '₹'
                    | '¢'
                    | 'z'
                    | 'ł'
            );
        self.non_alpha |= !alpha;
        self.space |= is_nemo_space(c);
        self.date_char |= matches!(c, ' ' | '.' | '/' | '-' | '–' | '—' | 's');
        self.dot |= c == '.';
        self.comma |= c == ',';
        self.at |= c == '@';
        self.slash |= c == '/';
        self.colon |= c == ':';
        self.dash |= c == '-';
        self.vulgar |= matches!(
            c,
            '½' | '⅓'
                | '⅔'
                | '¼'
                | '¾'
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
}
fn branches(s: &str, flags: &SpanFlags, mut emit: impl FnMut(i32, f64)) {
    if !flags.not_digit {
        // Telephone 1.04, plain cardinal 2.21 and word 100 are the only
        // digit-only paths.  Each accepted cheaper path dominates the rest.
        if s.len() == 10 {
            emit(5000, 1.04);
        } else if numeric::plain(s).is_some() {
            emit(12000, 2.21);
        } else {
            emit(13000, 100.0);
        }
        return;
    }
    // Pure alphabetic input has no numeric/electronic path.  Only the three
    // bare whitelist words and the Roman safety strings can beat WordFst.
    if !flags.digit && !flags.non_alpha && !matches!(s, "pani" | "Dr" | "dr" | "XL" | "MIX" | "CD" | "DJ") {
        emit(13000, 100.0);
        return;
    }
    let has_digit = flags.digit;
    let non_numeric = flags.non_numeric;
    let money_mask = if has_digit && non_numeric { flags.money_mask } else { 0 };
    let money = if money_mask != 0 { money::money_with_mask(s, money_mask) } else { Vec::new() };
    let has_money = !money.is_empty();
    if money.is_empty()
        && ((has_digit && flags.not_digit) || s == "pani" || s == "Dr" || s == "dr" || (!has_digit && flags.non_alpha))
        && (!s.starts_with(|c: char| c.is_ascii_digit())
            || s.chars().last().is_some_and(|c| matches!(c, 'G' | 'K' | '²' | '³'))
            || s.contains('^'))
    {
        let mut accepted = false;
        whitelist::whitelist_each(s, |_| {
            accepted = true;
            emit(0, 1.01);
        });
        // Money inputs are excluded from the whitelist above.  Every other
        // token class starts at 1.02 or higher.
        if accepted {
            return;
        }
    }
    if has_digit && flags.date_char {
        date::date_each(s, |_, c| emit(1000, 1.02 + c));
        if flags.dash && date::iso(s).is_some() {
            emit(2000, 1.02);
            // Grouped money ranges can tie 1.02, so only stop when absent.
            if !has_money {
                return;
            }
        }
    }
    if flags.dot || flags.at || s.starts_with("http://") || s.starts_with("https://") {
        let mut accepted = false;
        for _ in electronic::electronic(s) {
            accepted = true;
            emit(3000, 1.025);
        }
        // All later classes cost at least 1.04, except money which was
        // already recognized above and may carry negative grouped weights.
        if accepted && !has_money {
            return;
        }
    }
    if !has_digit && (s.contains(' ') || matches!(s, "XL" | "MIX" | "CD" | "DJ")) && roman::roman(s).is_some() {
        emit(4000, 1.03);
    }
    if has_digit {
        if s.len() >= 7 && electronic::telephone(s).is_some() {
            emit(5000, 1.04);
        }
        if flags.colon {
            time::time_each(s, |_, c| emit(6000, 1.05 + c));
        }
    }
    for (_, c) in money {
        emit(7000, 1.06 + c);
    }
    // Money's internal weights are non-positive; accepted money is <= 1.06.
    // All remaining token classes cost >= 1.07.
    if has_money {
        return;
    }
    if (has_digit && (!s.ends_with(|c: char| c.is_ascii_digit()) || s.ends_with("m2") || s.ends_with("m3")))
        || matches!(s, "°" | "%" | "‰")
    {
        let mut accepted = false;
        measure::measure_each(s, |_| {
            accepted = true;
            emit(8000, 1.07);
        });
        // Fraction, decimal, ordinal, cardinal and word are strictly dearer.
        if accepted {
            return;
        }
    }
    if ((has_digit && flags.slash) || flags.vulgar) && ordfrac::fraction(s).is_some() {
        emit(9000, 1.08);
        // All following branches have minimum cost 1.09 or greater.
        return;
    }
    if has_digit {
        if (flags.comma || flags.dot || non_numeric) && numeric::decimal(s).is_some() {
            emit(10000, 1.09);
            // Ordinal, cardinal and word cost at least 1.10.
            return;
        }
        if (flags.dot || flags.dash) && ordfrac::ordinal(s).is_some() {
            emit(11000, 1.10);
            // Cardinal starts at 1.11 and word at 100.
            return;
        }
        if flags.colon {
            // The only cardinal path containing a colon is the unweighted
            // `all_positive ":" all_positive` branch.
            if let Some((a, b)) = s.split_once(':') {
                if number::positive(a).is_some() && number::positive(b).is_some() {
                    emit(12000, 1.11);
                    return;
                }
            }
        } else {
            let mut accepted = false;
            numeric::cardinal_each(s, |_, c| {
                accepted = true;
                emit(12000, 1.11 + c);
            });
            // Every cardinal alternative costs at most 2.26, below word 100.
            if accepted {
                return;
            }
        }
    }
    if !flags.space {
        emit(13000, 100.0);
    }
}
fn render_branch(s: &str, group: i32, index: usize) -> Option<String> {
    let spoken = match group {
        0 => whitelist::whitelist(s).into_iter().nth(index)?,
        1 => date::date(s).into_iter().nth(index)?.0,
        2 => return date::iso(s),
        3 => electronic::electronic(s).into_iter().nth(index)?,
        4 => roman::roman(s)?,
        5 => electronic::telephone(s)?,
        6 => time::time(s).into_iter().nth(index)?.0,
        7 => {
            if index == 0 {
                money::simple_money(s).or_else(|| money::money(s).into_iter().next().map(|item| item.0))?
            } else {
                money::money(s).into_iter().nth(index)?.0
            }
        }
        8 => measure::measure(s).into_iter().nth(index)?,
        9 => ordfrac::fraction(s)?,
        10 => numeric::decimal(s)?,
        11 => ordfrac::ordinal(s)?,
        12 => numeric::cardinal(s).into_iter().nth(index)?.0,
        13 => return Some(format!("name: \"{s}\"")),
        _ => return None,
    };
    Some(name(&spoken))
}
// Only these grammar literals can follow an initial numeric expression after
// whitespace.  The data-backed portions come from the very same unit,
// currency, scale and month tables used by the recognizers.
static NUMERIC_WORDS: LazyLock<std::collections::HashSet<Vec<char>>> = LazyLock::new(|| {
    let literals = [
        "roku",
        "r",
        "g",
        "г",
        "pred",
        "n",
        "l",
        "BC",
        "BCE",
        "AD",
        "CE",
        "roky",
        "UTC",
        "GMT",
        "CET",
        "CEST",
        "EET",
        "mld",
        "miliardy",
        "miliárd",
        "млрд",
        "mil",
        "mln",
        "milióny",
        "miliónov",
        "млн",
    ];
    literals
        .into_iter()
        .chain(date::written_months().iter().copied())
        .chain(measure::symbols())
        .chain(money::input_symbols())
        .map(|s| s.chars().take_while(|c| c.is_alphabetic()).collect::<Vec<_>>())
        .filter(|s| !s.is_empty())
        .collect()
});

fn numeric_scan_end(text: &[char], start: usize) -> usize {
    let mut i = start;
    while i < text.len() {
        if is_nemo_space(text[i]) {
            let gap = i;
            while i < text.len() && is_nemo_space(text[i]) {
                i += 1;
            }
            let begin = i;
            while i < text.len() && text[i].is_alphabetic() {
                i += 1;
            }
            if i > begin && !NUMERIC_WORDS.contains(&text[begin..i]) {
                return gap;
            }
            if i == begin && i < text.len() {
                i += 1;
            }
        } else {
            i += 1;
        }
    }
    text.len()
}
// Alphabetic first words have only a small number of multiword continuations:
// named Roman contexts, "v roku", "okolo", and named electronic versions.
fn scan_end(text: &[char], start: usize) -> usize {
    let Some(end) = (start..text.len()).find(|&i| is_nemo_space(text[i])) else { return text.len() };
    let first_char = text[start];
    if !first_char.is_alphanumeric() && !"-−~±+€$£¥₽₩₪₴₫৳₦₱฿֏₺₹¢©§№".contains(first_char)
    {
        return end;
    }
    if end == start || !text[start..end].iter().all(|c| c.is_alphabetic()) {
        return text.len();
    }
    let first = &text[start..end];
    let special = [
        "v",
        "okolo",
        "Okolo",
        "Kapitola",
        "kapitola",
        "Hlava",
        "hlava",
        "Časť",
        "časť",
        "Diel",
        "diel",
        "Oddiel",
        "oddiel",
        "Článok",
        "článok",
        "Zväzok",
        "zväzok",
        "Karol",
        "Ján",
        "Ľudovít",
        "František",
        "Alžbeta",
        "Henrich",
        "Juraj",
        "Alexander",
        "Mikuláš",
        "Peter",
        "Viliam",
        "Ferdinand",
        "Štefan",
        "Václav",
        "Matej",
        "Žigmund",
    ]
    .iter()
    .any(|word| first.iter().copied().eq(word.chars()));
    if special || first.iter().all(|c| matches!(*c, 'I' | 'V' | 'X')) || money::is_currency_word(first) {
        return text.len();
    }
    // The only other multiword branch beginning with a bare alphabetic word
    // is an ASCII named version: letters, one ASCII space, digits and dots.
    if text[end] == ' '
        && end + 1 < text.len()
        && text[end + 1].is_ascii_digit()
        && first.iter().all(char::is_ascii_alphabetic)
    {
        let mut i = end + 1;
        let mut has_version_separator = false;
        while i < text.len() && (text[i].is_ascii_digit() || text[i] == '.') {
            if text[i] == '.' && i + 1 < text.len() && text[i + 1].is_ascii_digit() {
                has_version_separator = true;
            }
            i += 1;
        }
        return if has_version_separator { i } else { end };
    }
    end
}
impl Classifier for Union {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        // The source additionally accepts one punctuation item as the entire graph.
        // Represent that exceptional path as a token so the separated-chunk
        // automaton can otherwise require a classified token in every chunk.
        if start == 0 {
            READINGS.with_borrow_mut(Vec::clear);
            let mut punctuation = Vec::new();
            GRAMMAR.punct.spans(text, start, &mut punctuation);
            out.extend(punctuation.into_iter().filter(|c| c.end == text.len()));
        }
        // In the separated shape, a token can only start after whitespace or
        // the end of a punctuation item.  Emphasis items also end in `>`.
        if start != 0
            && !is_nemo_space(text[start - 1])
            && !GRAMMAR.punct.is_mark(text[start - 1])
            && text[start - 1] != '>'
        {
            return;
        }
        let limit = if text[start].is_ascii_digit() { numeric_scan_end(text, start) } else { scan_end(text, start) };
        let word = &text[start..limit];
        if !word.is_empty()
            && (limit == text.len() || is_nemo_space(text[limit]))
            && word.iter().all(|c| c.is_alphabetic())
            && !["pani", "Dr", "dr", "XL", "MIX", "CD", "DJ"]
                .iter()
                .any(|candidate| word.iter().copied().eq(candidate.chars()))
        {
            // No internal alphanumeric boundary can end a token; the only
            // path for this entire alphabetic chunk is the word classifier.
            out.push(Candidate { end: limit, cost: cost(100.0), rank: 13000, tag: String::new() });
            return;
        }
        let mut s = Scratch::take();
        let mut flags = SpanFlags::default();
        let may_have_digit = word.iter().any(char::is_ascii_digit);
        for end in start + 1..=limit {
            s.0.push(text[end - 1]);
            flags.push(text[end - 1]);
            if may_have_digit {
                flags.money_mask |= money::suffix_currencies(&s.0);
            }
            // Only the international-telephone branch consumes trailing
            // whitespace: its separator closure follows the final digit.
            // All other branches end in a number, symbol, letter or mark.
            if is_nemo_space(text[end - 1]) && text[start] != '+' {
                continue;
            }
            // Likewise, a following character must begin whitespace or a
            // punctuation item; excluded whitelist symbols cannot do so.
            if end != text.len() && !is_nemo_space(text[end]) && !GRAMMAR.punct.is_mark(text[end]) && text[end] != '<' {
                continue;
            }
            let mut previous_group = -1;
            let mut local = 0;
            let mut i = 0;
            recognize(|| {
                branches(&s.0, &flags, |rank, weight| {
                    if rank != previous_group {
                        local = 0;
                        previous_group = rank;
                    }
                    let candidate_rank = rank + i;
                    if rank != 13000 {
                        READINGS.with_borrow_mut(|readings| readings.push((start, end, candidate_rank, local)));
                    }
                    out.push(Candidate { end, cost: cost(weight), rank: candidate_rank, tag: String::new() });
                    local += 1;
                    i += 1;
                })
            });
        }
    }
    fn materialize(&self, text: &[char], start: usize, c: &mut Candidate) {
        if !c.tag.is_empty() {
            return;
        }
        let group = c.rank / 1000;
        if group == 13 {
            let mut tag = String::with_capacity(8 + (c.end - start) * 2);
            tag.push_str("name: \"");
            tag.extend(text[start..c.end].iter());
            tag.push('"');
            c.tag = tag;
            return;
        }
        let s: String = text[start..c.end].iter().collect();
        let offset = READINGS.with_borrow(|readings| {
            readings
                .iter()
                .rev()
                .find(|&&(a, b, rank, _)| a == start && b == c.end && rank == c.rank)
                .map(|entry| entry.3)
        });
        if let Some(offset) = offset {
            if let Some(tag) = render_branch(&s, group, offset) {
                c.tag = tag;
            }
        }
    }
}
static GRAMMAR: LazyLock<Sk> =
    LazyLock::new(|| Sk { classifiers: vec![Box::new(Union)], punct: Punctuation::en(cost(1.1)) });
pub fn grammar() -> &'static dyn Grammar {
    &*GRAMMAR
}
