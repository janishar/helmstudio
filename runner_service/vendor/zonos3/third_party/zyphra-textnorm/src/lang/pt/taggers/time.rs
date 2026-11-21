//! `pt/taggers/time.py`: per hour 0-23 (`h`/`0h`), `:`/`.` minutes, `h`/`H` minutes, `h`/`H` alone, `h:mm:ss`,
//! each optionally followed by ` da <period>` (`_resolve_suffix_hour`).

use std::sync::LazyLock;

use super::cardinal;
use crate::lang::pt::graph_utils::SpanTagger;
use crate::text::{Cost, is_digit, is_nemo_space};

crate::labels!(PERIODS, "pt/time/day_period_suffix.tsv");

struct Period {
    tail: Vec<char>,
    tag: String,
    lo: u32,
    hi: u32,
}

struct Data {
    /// cardinal words of 0-59 (hours use 0-23)
    hours: Vec<String>,
    periods: Vec<Period>,
}

static DATA: LazyLock<Data> = LazyLock::new(|| {
    let hours = (0..60u32)
        .map(|h| {
            let d: Vec<char> = h.to_string().chars().collect();
            cardinal::graph_digits(&d).map(|(w, _)| w).unwrap_or_default()
        })
        .collect();
    let periods = PERIODS
        .iter()
        .filter(|r| r.len() >= 4 && !r[0].trim().is_empty())
        .map(|r| Period {
            tail: r[0].trim().chars().collect(),
            tag: r[1].trim().to_string(),
            lo: r[2].trim().parse().unwrap(),
            hi: r[3].trim().parse().unwrap(),
        })
        .collect();
    Data { hours, periods }
});

/// `TimeFst._resolve_suffix_hour`: `(keep_suffix, hour_index)`.
fn resolve(h: u32, p: &Period) -> (bool, u32) {
    let allowed = |x: u32| (p.lo..=p.hi).contains(&x);
    let tail: String = p.tail.iter().collect();
    match tail.as_str() {
        "manhã" => (allowed(h) || (1..=5).contains(&h), h),
        "tarde" => {
            if allowed(h) {
                (true, if h == 12 { 12 } else { h - 12 })
            } else if (1..=5).contains(&h) && allowed(h + 12) {
                (true, h)
            } else {
                (false, h)
            }
        }
        "noite" => {
            if allowed(h) {
                (true, h - 12)
            } else if (6..=11).contains(&h) && allowed(h + 12) {
                (true, h)
            } else {
                (false, h)
            }
        }
        "madrugada" => (allowed(h), h),
        _ => (false, h),
    }
}

/// `pynutil.delete("0") + graph_minute_single | graph_minute_double` on two digits.
fn minute(d: &[char]) -> Option<&'static str> {
    if d.len() != 2 || !d.iter().all(|c| is_digit(*c)) {
        return None;
    }
    let n = (d[0] as usize - 48) * 10 + d[1] as usize - 48;
    if n == 0 || n >= 60 {
        return None;
    }
    Some(DATA.hours[n].as_str())
}

/// `hour_delete_fsts`: `0`-`23`, or `00`-`09`.
fn hour(d: &[char]) -> Option<u32> {
    if d.is_empty() || d.len() > 2 || !d.iter().all(|c| is_digit(*c)) {
        return None;
    }
    let n = d.iter().fold(0u32, |a, &c| a * 10 + (c as u32 - 48));
    if n >= 24 || (d.len() == 2 && d[0] == '0' && n >= 10) || (d.len() == 2 && n < 10 && d[0] != '0') {
        return None;
    }
    Some(n)
}

/// `delete_space + "da" + delete_space + tail` (with `insert_space`) at the end of the span.
fn suffix(s: &[char]) -> Option<&'static Period> {
    let mut i = 0;
    while i < s.len() && is_nemo_space(s[i]) {
        i += 1;
    }
    if s.get(i) != Some(&'d') || s.get(i + 1) != Some(&'a') {
        return None;
    }
    i += 2;
    while i < s.len() && is_nemo_space(s[i]) {
        i += 1;
    }
    DATA.periods.iter().find(|p| s[i..] == p.tail[..])
}

/// The minutes/seconds fields after the hours: `(input consumed, minutes, seconds)`. A zero field of `h:mm:ss`
/// is `Zero` (`minutes_zero`/`seconds_zero`, written without a leading space); `h:00` / `h00` write nothing.
#[derive(Clone, Copy)]
enum F {
    None,
    Zero,
    Word(&'static str),
}

fn mid(s: &[char]) -> Option<(usize, F, F)> {
    let delim = |c: char| c == ':' || c == '.';
    let zero_or = |d: &[char]| if d == ['0', '0'] { Some(F::Zero) } else { minute(d).map(F::Word) };
    match s.first() {
        Some(&c) if delim(c) => {
            // mid_hms (2 + delim + 2) or mid_hm (2 digits)
            let m = s.get(1..3)?;
            if s.len() >= 6 && delim(s[3]) && s[4..6].iter().all(|c| is_digit(*c)) && m.iter().all(|c| is_digit(*c)) {
                return Some((6, zero_or(m)?, zero_or(&s[4..6])?));
            }
            if m == ['0', '0'] {
                return Some((3, F::None, F::None));
            }
            Some((3, F::Word(minute(m)?), F::None))
        }
        Some('h' | 'H') => match s.get(1..3) {
            Some(['0', '0']) => Some((3, F::None, F::None)),
            Some(m) if m.iter().all(|c| is_digit(*c)) => Some((3, F::Word(minute(m)?), F::None)),
            _ => Some((1, F::None, F::None)),
        },
        _ => None,
    }
}

fn push_field(tag: &mut String, key: &str, f: F) {
    match f {
        F::None => {}
        F::Zero => {
            tag.push_str(key);
            tag.push_str(": \"");
            tag.push_str(&DATA.hours[0]);
            tag.push('"');
        }
        F::Word(w) => {
            tag.push(' ');
            tag.push_str(key);
            tag.push_str(": \"");
            tag.push_str(w);
            tag.push('"');
        }
    }
}

pub struct Time;

impl SpanTagger for Time {
    fn max_words(&self, c: char) -> usize {
        if is_digit(c) { 3 } else { 0 }
    }
    fn span_limit(&self, text: &[char], start: usize) -> usize {
        if self.max_words(text[start]) == 0 {
            return 0;
        }
        // `<time> da <period>`
        let mut i = start + super::word_len(text, start);
        while i < text.len() && is_nemo_space(text[i]) {
            i += 1;
        }
        if crate::text::starts_with(text, i, "da") { 3 } else { 1 }
    }
    fn accept(&self, span: &[char]) -> Option<(Cost, String)> {
        let nd = span.iter().take_while(|c| is_digit(**c)).count();
        let h = hour(&span[..nd])?;
        let (used, minutes, seconds) = mid(&span[nd..])?;
        let rest = &span[nd + used..];
        let (hour_idx, suffix_tag) = if rest.is_empty() {
            (h, None)
        } else {
            let p = suffix(rest)?;
            let (keep, idx) = resolve(h, p);
            (if keep { idx } else { h }, Some(p.tag.as_str()))
        };
        let mut tag = String::with_capacity(96);
        tag.push_str("time { hours: \"");
        tag.push_str(&DATA.hours[hour_idx as usize]);
        tag.push('"');
        push_field(&mut tag, "minutes", minutes);
        push_field(&mut tag, "seconds", seconds);
        if let Some(t) = suffix_tag {
            tag.push_str(" suffix: \"");
            tag.push_str(t);
            tag.push('"');
        }
        tag.push_str(" preserve_order: true }");
        Some((0, tag))
    }
}
