//! `en/taggers/telephone.py` (deterministic).

use crate::lang::en::graph_utils::row_index;
use crate::lang::en::lazy::{Reading, building};
use crate::lang::en::taggers::cardinal::DIGIT;
use crate::lattice::{Candidate, Classifier};
use crate::text::{Table, cost, is_alpha, is_digit, is_nemo_space};

crate::tsv!(TELEPHONE_PROMPT, "en/telephone/telephone_prompt.tsv");
crate::tsv!(IP_PROMPT, "en/telephone/ip_prompt.tsv");
crate::tsv!(SSN_PROMPT, "en/telephone/ssn_prompt.tsv");

fn dw(c: char) -> Option<&'static str> {
    is_digit(c).then(|| DIGIT[c as usize - '0' as usize])
}

/// Appends the digit words of `text[q..q + n]` joined by `sep`.
fn spell(out: &mut String, text: &[char], q: usize, n: usize, sep: &str) {
    for (k, &c) in text[q..q + n].iter().enumerate() {
        if k > 0 {
            out.push_str(sep);
        }
        out.push_str(dw(c).unwrap());
    }
}

fn extra_space(text: &[char], p: usize) -> Option<usize> {
    let mut e = p;
    while e < text.len() && is_nemo_space(text[e]) {
        e += 1;
    }
    (e > p).then_some(e)
}

/// A `country_code` value: a `telephone_prompt.tsv` row alone (`digits` empty), or digits `digits` with an optional
/// prompt row before them and an optional `+`.
#[derive(Clone, Copy, Debug)]
struct CcR {
    prompt: Option<u16>,
    plus: bool,
    digits: (u32, u32),
}

fn write_cc(s: &mut String, text: &[char], c: CcR) {
    let prompt = c.prompt.map(|r| TELEPHONE_PROMPT.rows()[r as usize].value.as_str());
    if c.digits.0 == c.digits.1 {
        s.push_str(prompt.unwrap_or(""));
        return;
    }
    if let Some(pr) = prompt {
        s.push_str(pr);
        s.push(' ');
    }
    if c.plus {
        s.push_str("plus ");
    }
    spell(s, text, c.digits.0 as usize, (c.digits.1 - c.digits.0) as usize, " ");
    s.push(',');
}

/// `country_code` readings from `p` whose end is followed (after an optional "-" and whitespace) by an area part:
/// `f(end, reading)`, prompt-only forms first.
fn country_codes(text: &[char], p: usize, f: &mut dyn FnMut(usize, CcR)) {
    let followed = |ce: usize| {
        let mut q = ce;
        if text.get(q) == Some(&'-') {
            q += 1;
        }
        loop {
            if area_at(text, q) {
                return true;
            }
            if q < text.len() && is_nemo_space(text[q]) {
                q += 1;
            } else {
                return false;
            }
        }
    };
    for (r, e) in TELEPHONE_PROMPT.prefix_matches(text, p) {
        if followed(e) {
            f(e, CcR { prompt: Some(row_index(&TELEPHONE_PROMPT, r)), plus: false, digits: (0, 0) });
        }
    }
    let mut digit_forms = |s: usize, prompt: Option<u16>| {
        for plus in [false, true] {
            let q = if plus {
                if text.get(s) != Some(&'+') {
                    continue;
                }
                s + 1
            } else {
                s
            };
            let mut i = q;
            while i < text.len() && i - q < 3 && is_digit(text[i]) {
                i += 1;
                if followed(i) {
                    f(i, CcR { prompt, plus, digits: (q as u32, i as u32) });
                }
            }
        }
    };
    digit_forms(p, None);
    for (r, e) in TELEPHONE_PROMPT.prefix_matches(text, p) {
        if let Some(e2) = extra_space(text, e) {
            digit_forms(e2, Some(row_index(&TELEPHONE_PROMPT, r)));
        }
    }
}

/// Whether an area part (`ddd-`, `ddd.`, `(ddd)`) starts at `p`: necessary for every `number_part`.
fn area_at(text: &[char], p: usize) -> bool {
    let d3 = |q: usize| text.get(q..q + 3).is_some_and(|s| s.iter().all(|&c| is_digit(c)));
    (d3(p) && matches!(text.get(p + 3), Some('-' | '.')))
        || (text.get(p) == Some(&'(') && d3(p + 1) && text.get(p + 4) == Some(&')'))
}

/// A `number_part` value: the area digits at `area`, then the number words from `words` with separator `sep`.
#[derive(Clone, Copy, Debug)]
struct NpR {
    area: u32,
    words: u32,
    sep: char,
}

/// `number_words` from `p` with separator `sepc` (7 units): the end, and with `out` the words appended.
fn number_words(text: &[char], p: usize, sepc: char, mut out: Option<&mut String>) -> Option<usize> {
    let mut i = p;
    for _ in 0..7 {
        let &c = text.get(i)?;
        // number_length allows " " / other separators too, but number_words can't consume them
        if let Some(w) = dw(c) {
            i += 1;
            let sep = text.get(i) == Some(&sepc);
            if sep {
                i += 1;
            }
            if let Some(o) = out.as_deref_mut() {
                o.push_str(w);
                o.push_str(if sep { ", " } else { " " });
            }
        } else if is_alpha(c) {
            i += 1;
            let sep = text.get(i) == Some(&sepc);
            if sep {
                i += 1;
            }
            if let Some(o) = out.as_deref_mut() {
                o.push(c);
                if sep {
                    o.push(' ');
                }
            }
        } else {
            return None;
        }
    }
    Some(i)
}

fn write_np(s: &mut String, text: &[char], n: NpR) {
    let a = n.area as usize;
    if text[a..a + 3] == ['8', '0', '0'] {
        s.push_str("eight hundred");
    } else {
        spell(s, text, a, 3, " ");
    }
    s.push_str(", ");
    number_words(text, n.words as usize, n.sep, Some(s));
}

/// `number_part` from `p`: `(end, reading)`, sorted by end, one per end (equal ends spell the same words).
fn number_parts(text: &[char], p: usize) -> Vec<(usize, NpR)> {
    if !area_at(text, p) {
        return Vec::new();
    }
    let d3 = |q: usize| text.get(q..q + 3).is_some_and(|s| s.iter().all(|&c| is_digit(c)));
    // area part: (area digits, number words start)
    let mut areas: Vec<(usize, usize)> = Vec::new();
    if d3(p) && matches!(text.get(p + 3), Some('-' | '.')) {
        areas.push((p, p + 4));
    }
    if text.get(p) == Some(&'(') && d3(p + 1) && text.get(p + 4) == Some(&')') {
        areas.push((p + 1, p + 5));
        if matches!(text.get(p + 5), Some(' ' | '-')) {
            areas.push((p + 1, p + 6));
        }
    }
    let mut res = Vec::new();
    for (area, ae) in areas {
        // number_length: 7 × ((DIGIT|ALPHA) sep?) with sep ∈ {-, " ", .}
        // number_words: closure(digit (ins " " | "-"→", ") | ALPHA | ALPHA "-"→" ") or the "." variant
        for sep in ['-', '.'] {
            if let Some(i) = number_words(text, ae, sep, None) {
                res.push((i, NpR { area: area as u32, words: ae as u32, sep }));
            }
        }
    }
    res.sort_by_key(|x| x.0);
    res.dedup_by_key(|x| x.0);
    res
}

/// `insert_space + extension`: digit{1,4} directly after the number part: the ends.
fn ext(text: &[char], p: usize) -> impl Iterator<Item = usize> + '_ {
    let mut i = p;
    while i < text.len() && i - p < 4 && is_digit(text[i]) {
        i += 1;
    }
    p + 1..=i
}

/// One `TelephoneFst` reading at a start; the span end is the candidate's end.
#[derive(Clone, Copy, Debug)]
pub struct TelR(Tel);

#[derive(Clone, Copy, Debug)]
enum Tel {
    /// `country_code`? `number_part` `extension`? (the extension's digits start where `number_part` ends).
    Number { cc: Option<CcR>, np: NpR, ext: Option<u32> },
    /// IP: the `ip_prompt.tsv` row, the first group's start and the four group lengths.
    Ip { pre: Option<u16>, s: u32, lens: [u8; 4] },
    /// SSN: the `ssn_prompt.tsv` row and the digits' start.
    Ssn { pre: Option<u16>, s: u32 },
}

/// The `TelephoneFst` fields of reading `r` ending at `end`.
fn fields(text: &[char], end: usize, r: TelR) -> String {
    let mut s = String::with_capacity(96);
    let prompt = |s: &mut String, t: &Table, pre: Option<u16>| {
        if let Some(r) = pre {
            s.push_str("country_code: \"");
            s.push_str(&t.rows()[r as usize].value);
            s.push_str("\" ");
        }
    };
    match r.0 {
        Tel::Number { cc, np, ext } => {
            if let Some(c) = cc {
                s.push_str("country_code: \"");
                write_cc(&mut s, text, c);
                s.push_str("\" ");
            }
            s.push_str("number_part: \"");
            write_np(&mut s, text, np);
            s.push('"');
            if let Some(x) = ext {
                s.push_str(" extension: \"");
                spell(&mut s, text, x as usize, end - x as usize, " ");
                s.push('"');
            }
        }
        Tel::Ip { pre, s: q, lens } => {
            prompt(&mut s, &IP_PROMPT, pre);
            s.push_str("number_part: \"");
            let mut q = q as usize;
            for (k, &n) in lens.iter().enumerate() {
                if k > 0 {
                    s.push_str(" dot ");
                }
                spell(&mut s, text, q, n as usize, " ");
                q += n as usize + 1;
            }
            s.push('"');
        }
        Tel::Ssn { pre, s: q } => {
            prompt(&mut s, &SSN_PROMPT, pre);
            s.push_str("number_part: \"");
            let q = q as usize;
            spell(&mut s, text, q, 3, " ");
            s.push_str(", ");
            spell(&mut s, text, q + 4, 2, " ");
            s.push_str(", ");
            spell(&mut s, text, q + 7, 4, " ");
            s.push('"');
        }
    }
    s
}

/// Every `(end, reading)` of `TelephoneFst` from `start` (fields by [`fields`]).
fn telephone_spans(text: &[char], start: usize, f: &mut dyn FnMut(usize, TelR)) {
    // priority unions: number_part + extension  >  cc + number + ext  >  cc + number  >  number
    // These are domain priorities on the same input; collect each branch's accepted (end, reading).
    let mut by_end: Vec<(usize, u8, Tel)> = Vec::new();
    for (ne, np) in number_parts(text, start) {
        by_end.push((ne, 3, Tel::Number { cc: None, np, ext: None }));
        for ee in ext(text, ne) {
            by_end.push((ee, 0, Tel::Number { cc: None, np, ext: Some(ne as u32) }));
        }
    }
    country_codes(text, start, &mut |ce, c| {
        // the number part follows after an optional "-" and whitespace
        let mut q = ce;
        if text.get(q) == Some(&'-') {
            q += 1;
        }
        let mut k = q;
        while k < text.len() && is_nemo_space(text[k]) {
            k += 1;
        }
        // the number starts at q or anywhere inside the whitespace run after it
        for q in q..=k {
            for (ne, np) in number_parts(text, q) {
                by_end.push((ne, 2, Tel::Number { cc: Some(c), np, ext: None }));
                for ee in ext(text, ne) {
                    by_end.push((ee, 1, Tel::Number { cc: Some(c), np, ext: Some(ne as u32) }));
                }
            }
        }
    });
    let mut ends: Vec<usize> = by_end.iter().map(|x| x.0).collect();
    ends.sort_unstable();
    ends.dedup();
    for e in ends {
        let best = by_end.iter().filter(|x| x.0 == e).map(|x| x.1).min().unwrap();
        for x in by_end.iter().filter(|x| x.0 == e && x.1 == best) {
            f(e, TelR(x.2));
        }
    }
    let digits = |q: usize| {
        let mut e = q;
        while e < text.len() && is_digit(text[e]) {
            e += 1;
        }
        e - q
    };
    // prompt forms: f(start after the prompt and whitespace, prompt row)
    let starts = |t: &Table, f: &mut dyn FnMut(usize, Option<u16>)| {
        if digits(start) != 0 {
            return f(start, None);
        }
        for (r, e) in t.prefix_matches(text, start) {
            if let Some(e2) = extra_space(text, e) {
                f(e2, Some(row_index(t, r)));
            }
        }
    };
    // IP: digit{1,3} ("." digit{1,3}){3}; an inner group is followed by ".", so it is its whole digit run; only the
    // last group may stop inside a run
    starts(&IP_PROMPT, &mut |s, pre| {
        let mut lens = [0u8; 4];
        let mut q = s;
        for len in lens.iter_mut().take(3) {
            let run = digits(q);
            if !(1..=3).contains(&run) || text.get(q + run) != Some(&'.') {
                return;
            }
            *len = run as u8;
            q += run + 1;
        }
        for n in 1..=digits(q).min(3) {
            lens[3] = n as u8;
            f(q + n, TelR(Tel::Ip { pre, s: s as u32, lens }));
        }
    });
    // SSN
    starts(&SSN_PROMPT, &mut |s, pre| {
        if digits(s) == 3
            && text.get(s + 3) == Some(&'-')
            && digits(s + 4) >= 2
            && text.get(s + 6) == Some(&'-')
            && digits(s + 7) >= 4
        {
            f(s + 11, TelR(Tel::Ssn { pre, s: s as u32 }));
        }
    });
}

pub struct Telephone;

/// This member's id in the branch stash (`lazy::record`).
const MEMBER: u8 = 10;

fn tag(text: &[char], end: usize, r: TelR) -> String {
    format!("telephone {{ {} }}", fields(text, end, r))
}

impl Classifier for Telephone {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        let c = text[start];
        if !(is_digit(c) || c == '+' || c == '(')
            && TELEPHONE_PROMPT.prefix_matches(text, start).next().is_none()
            && IP_PROMPT.prefix_matches(text, start).next().is_none()
            && SSN_PROMPT.prefix_matches(text, start).next().is_none()
        {
            return;
        }
        // every branch holds an area part (`ddd-`, `ddd.`, `(ddd)`), an IP (`d.`) or an SSN (`ddd-`) before the
        // next whitespace-free stretch ends; a token without "-", "." or "(" + digit cannot match
        if is_digit(c) || c == '+' {
            let mut i = start;
            let mut sep = false;
            while i < text.len() && !is_nemo_space(text[i]) {
                if matches!(text[i], '-' | '.' | '(' | ')') {
                    sep = true;
                    break;
                }
                i += 1;
            }
            // "+1 (555) …": a country code may be followed by spaces, then the area part
            if !sep && !(c == '+' || text[start..i].len() <= 3) {
                return;
            }
        }
        let build = building();
        telephone_spans(text, start, &mut |e, r| {
            if !crate::lang::en::lazy::keep_end(text, e) {
                return;
            }
            crate::lang::en::lazy::record(MEMBER, start, e, cost(1.1), 0, Reading::Telephone(r));
            let tag = if build { tag(text, e, r) } else { String::new() };
            out.push(Candidate { end: e, cost: cost(1.1), rank: 0, tag });
        });
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        let end = candidate.end;
        crate::lang::en::lazy::materialize_recorded(self, MEMBER, &[], text, start, candidate, |r| match r {
            Reading::Telephone(r) => Some(tag(text, end, r)),
            _ => None,
        })
    }
}
