//! Clock, AM/PM, zone and clock-range paths.
use super::cardinal;
use super::numeric::Reading;

/// Unambiguous ordinary clock path; no spoken fields are built here.
pub(super) fn simple(s: &str) -> bool {
    let Some((h, m)) = s.split_once(':') else { return false };
    if h.is_empty()
        || h.len() > 2
        || !h.bytes().all(|b| b.is_ascii_digit())
        || h.parse::<u8>().ok().is_none_or(|n| n > 23)
    {
        return false;
    }
    !m.is_empty() && m.len() <= 2 && !m.starts_with('0') && m.bytes().all(|b| b.is_ascii_digit())
}

/// Decode an already-recognized `simple` clock directly from its input span.
pub(super) fn simple_chars(s: &[char]) -> Option<(usize, usize)> {
    let colon = s.iter().position(|&c| c == ':')?;
    let (h, rest) = s.split_at(colon);
    let m = &rest[1..];
    fn number(s: &[char]) -> Option<usize> {
        if s.is_empty() || s.len() > 2 {
            return None;
        }
        let mut n = 0;
        for &c in s {
            if !c.is_ascii_digit() {
                return None;
            }
            n = n * 10 + (c as u8 - b'0') as usize;
        }
        Some(n)
    }
    let hour = number(h)?;
    if hour > 23 || m.first() == Some(&'0') {
        return None;
    }
    Some((hour, number(m)?))
}

fn hour(s: &str) -> Option<String> {
    if s.len() > 2 || s.is_empty() || !s.bytes().all(|c| c.is_ascii_digit()) {
        return None;
    }
    let n: u8 = s.parse().ok()?;
    if n > 23 {
        return None;
    }
    cardinal::raw(&n.to_string())
}
fn minutes(s: &str) -> Vec<String> {
    let mut v = Vec::new();
    if s == "00" {
        v.push(String::new());
    }
    if s.len() == 2 && s.starts_with('0') {
        if let Some(d) = s.chars().nth(1).and_then(cardinal::digit) {
            v.push(d.to_owned());
            v.push(format!("nul {d}"));
        }
    }
    if s.len() <= 2 && !s.starts_with('0') {
        if let Some(n) = cardinal::raw(s) {
            v.push(n);
        }
    }
    v
}
fn hm(s: &str) -> Vec<String> {
    let Some((h, m)) = s.split_once(':') else { return vec![] };
    let Some(h) = hour(h) else { return vec![] };
    minutes(m).into_iter().map(|m| if m.is_empty() { format!("{h} uur") } else { format!("{h} uur {m}") }).collect()
}
fn hms(s: &str) -> Vec<String> {
    let v: Vec<_> = s.split(':').collect();
    if v.len() != 3 {
        return vec![];
    }
    let Some(h) = hour(v[0]) else { return vec![] };
    let mut seconds = Vec::new();
    if v[2].len() == 2 && v[2].starts_with('0') {
        if let Some(d) = v[2].chars().nth(1).and_then(cardinal::digit) {
            seconds.push(format!("nul {d}"));
        }
    }
    if v[2].len() <= 2 && !(v[2].len() > 1 && v[2].starts_with('0')) {
        if let Some(x) = cardinal::raw(v[2]) {
            seconds.push(x);
        }
    }
    let mut out = Vec::new();
    for m in minutes(v[1]) {
        for sec in &seconds {
            out.push(format!("{h} uur {} en {sec} seconden", if m.is_empty() { "nul" } else { &m }));
        }
    }
    out
}
fn with_ampm(s: &str) -> Vec<String> {
    let mut out = Vec::new();
    for (suffix, spoken) in [
        ("AM", "'s ochtends"),
        ("A.M.", "'s ochtends"),
        ("am", "'s ochtends"),
        ("a.m.", "'s ochtends"),
        ("PM", "'s middags"),
        ("P.M.", "'s middags"),
        ("pm", "'s middags"),
        ("p.m.", "'s middags"),
    ] {
        if let Some(v) = s.strip_suffix(suffix) {
            let v = v.strip_suffix(' ').unwrap_or(v);
            for x in hm(v).into_iter().chain(hms(v)) {
                out.push(format!("{x} {spoken}"));
            }
        }
    }
    out
}
pub(super) fn parse(s: &str) -> Vec<Reading> {
    let mut out = Vec::new();
    out.extend(hm(s).into_iter().map(|v| (v, 1.0)));
    out.extend(hms(s).into_iter().map(|v| (v, 0.97)));
    out.extend(with_ampm(s).into_iter().map(|v| (v, 0.95)));
    for (zone, word) in [("UTC", "u t c"), ("GMT", "g m t"), ("CET", "c e t"), ("CEST", "c e s t")] {
        for (i, _) in s.match_indices(zone) {
            let before = s[..i].strip_suffix(' ').unwrap_or(&s[..i]);
            let after = &s[i + zone.len()..];
            let offsets: Vec<String> = if after.is_empty() {
                vec![String::new()]
            } else {
                let sign = if after.starts_with('+') {
                    Some("plus")
                } else if after.starts_with('-') || after.starts_with('−') {
                    Some("min")
                } else {
                    None
                };
                if let Some(sign) = sign {
                    let rest = &after[after.chars().next().unwrap().len_utf8()..];
                    if rest.len() <= 2 && !rest.starts_with('0') {
                        cardinal::raw(rest).map(|v| vec![format!(" {sign} {v}")]).unwrap_or_default()
                    } else {
                        vec![]
                    }
                } else {
                    vec![]
                }
            };
            for v in hm(before).into_iter().chain(hms(before)).chain(with_ampm(before)) {
                for offset in &offsets {
                    out.push((format!("{v} {word}{offset}"), 0.94));
                }
            }
        }
    }
    for (i, c) in s.char_indices().filter(|(_, c)| matches!(c, '-' | '–' | '—')) {
        let a = s[..i].trim_end_matches(crate::text::is_nemo_space);
        let b = s[i + c.len_utf8()..].trim_start_matches(crate::text::is_nemo_space);
        let left = hm(a);
        let right = hm(b);
        for x in &left {
            for y in &right {
                out.push((format!("{x} tot {y}"), 0.96));
            }
            if let Some(h) = hour(b) {
                out.push((format!("{x} tot {h} uur"), 0.96));
            }
        }
        if let Some(h) = hour(a) {
            for y in &right {
                out.push((format!("{h} uur tot {y}"), 0.96));
            }
        }
    }
    out
}
