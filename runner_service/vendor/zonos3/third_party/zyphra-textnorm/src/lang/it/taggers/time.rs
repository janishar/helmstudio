//! `it/taggers/time.py`.

use crate::lattice::{Candidate, Classifier};
use crate::text::Cost;

pub struct Time {
    pub cost: Cost,
}

fn dig(text: &[char], p: usize) -> Option<u8> {
    text.get(p).filter(|c| c.is_ascii_digit()).map(|&c| c as u8 - b'0')
}

/// `delete_leading_zero_to_double_digit @ graph_hour`: exactly two digits, value 0-24.
fn hour2(text: &[char], p: usize) -> Option<u8> {
    let v = dig(text, p)? * 10 + dig(text, p + 1)?;
    (v <= 24).then_some(v)
}

/// Minutes/seconds: `"0" d` (d 1-9) → `d`, `dd` 10-59 → itself; `"00"` handled by the callers.
fn minute(text: &[char], p: usize) -> Option<u8> {
    let (a, b) = (dig(text, p)?, dig(text, p + 1)?);
    match a {
        0 if b > 0 => Some(b),
        1..=5 => Some(a * 10 + b),
        _ => None,
    }
}

/// Appends the decimal value `v` (0-99).
fn push_num(tag: &mut String, v: u8) {
    if v >= 10 {
        tag.push((b'0' + v / 10) as char);
    }
    tag.push((b'0' + v % 10) as char);
}

/// `time { hours: "h" [minutes: "m"] [seconds: "s" preserve_order: true] }` for a span ending at `end`.
fn tag(end: usize, h: u8, m: Option<u8>, s: Option<u8>) -> String {
    super::tag(end, 80, |t| {
        t.push_str("time { hours: \"");
        push_num(t, h);
        if let Some(m) = m {
            t.push_str("\" minutes: \"");
            push_num(t, m);
        }
        if let Some(s) = s {
            t.push_str("\" seconds: \"");
            push_num(t, s);
            t.push_str("\" preserve_order: true }");
        } else {
            t.push_str("\" }");
        }
    })
}

impl Classifier for Time {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if !text[start].is_ascii_digit() {
            return;
        }
        let mut push = |end: usize, h: u8, m: Option<u8>, s: Option<u8>| {
            out.push(Candidate { end, cost: self.cost, rank: 0, tag: tag(end, h, m, s) })
        };
        // graph_h: "0".."24" verbatim
        if let Some(a) = dig(text, start) {
            push(start + 1, a, None, None);
            if let Some(b) = dig(text, start + 1) {
                if a != 0 && a * 10 + b <= 24 {
                    push(start + 2, a * 10 + b, None, None);
                }
            }
        }
        let Some(h) = hour2(text, start) else { return };
        if text.get(start + 2) != Some(&':') {
            return;
        }
        let mp = start + 3;
        let m00 = dig(text, mp) == Some(0) && dig(text, mp + 1) == Some(0);
        let m = minute(text, mp);
        // graph_hm
        if m00 {
            push(mp + 2, h, None, None);
        } else if let Some(m) = m {
            push(mp + 2, h, Some(m), None);
        }
        // graph_hms
        if (m00 || m.is_some()) && text.get(mp + 2) == Some(&':') {
            let sp = mp + 3;
            let s00 = dig(text, sp) == Some(0) && dig(text, sp + 1) == Some(0);
            let s = if s00 { Some(0) } else { minute(text, sp) };
            if let Some(s) = s {
                push(sp + 2, h, Some(m.unwrap_or(0)), Some(s));
            }
        }
    }
}
