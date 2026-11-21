//! `CardinalFst.graph` (`graph_num`, `ko/taggers/cardinal.py`) as a function on a digit string.
//!
//! The upstream union is functional: it accepts `0` and every 1-17-digit string without a leading zero. Each
//! component of length L reads its leading 1-4 digits and then a tail; every tail alternative is "delete the
//! leading zeros, then the component of the remaining length" (or delete everything when the tail is all zeros).

const DIGIT: [&str; 10] = ["", "일", "이", "삼", "사", "오", "육", "칠", "팔", "구"];
/// `data/number/tens.tsv`.
const TENS: [&str; 10] = ["", "십", "이십", "삼십", "사십", "오십", "육십", "칠십", "팔십", "구십"];

/// Where a reading is written: a `String`, or [`CharCount`] to measure it without building it.
pub trait Emit {
    fn push_str(&mut self, s: &str);
    fn push(&mut self, c: char);
}

impl Emit for String {
    fn push_str(&mut self, s: &str) {
        String::push_str(self, s)
    }
    fn push(&mut self, c: char) {
        String::push(self, c)
    }
}

/// Counts the chars of a reading.
pub struct CharCount(pub usize);

impl Emit for CharCount {
    fn push_str(&mut self, s: &str) {
        self.0 += s.chars().count()
    }
    fn push(&mut self, _: char) {
        self.0 += 1
    }
}

fn d(c: u8) -> usize {
    (c - b'0') as usize
}

/// `graph_1_to_99` on 1-2 digits without a leading zero.
fn h1_99<O: Emit>(s: &[u8], out: &mut O) {
    if s.len() == 2 {
        out.push_str(TENS[d(s[0])]);
        out.push_str(DIGIT[d(s[1])]);
    } else {
        out.push_str(DIGIT[d(s[0])]);
    }
}

/// `1 → unit | [2-9] → digit + unit` (the 백/천/만 leads).
fn lead_omit_one<O: Emit>(c: u8, unit: char, out: &mut O) {
    if c != b'1' {
        out.push_str(DIGIT[d(c)]);
    }
    out.push(unit);
}

/// A component (`graph_*_component`) on a string with a nonzero first digit, 1-17 digits.
fn component<O: Emit>(s: &[u8], out: &mut O) {
    match s.len() {
        1 | 2 => h1_99(s, out),
        3 => {
            lead_omit_one(s[0], '백', out);
            tail(&s[1..], out);
        }
        4 => {
            lead_omit_one(s[0], '천', out);
            tail(&s[1..], out);
        }
        5 => {
            lead_omit_one(s[0], '만', out);
            tail(&s[1..], out);
        }
        6..=8 => {
            component(&s[..s.len() - 4], out);
            out.push('만');
            tail(&s[s.len() - 4..], out);
        }
        9..=12 => {
            component_digit_lead(&s[..s.len() - 8], out);
            out.push('억');
            tail(&s[s.len() - 8..], out);
        }
        13..=16 => {
            component_digit_lead(&s[..s.len() - 12], out);
            out.push('조');
            tail(&s[s.len() - 12..], out);
        }
        _ => {
            out.push_str(DIGIT[d(s[0])]);
            out.push('경');
            tail(&s[1..], out);
        }
    }
}

/// The lead of the 억/조 components: `graph_digit` (keeps 일) for one digit, else the 2-4 digit component.
fn component_digit_lead<O: Emit>(s: &[u8], out: &mut O) {
    if s.len() == 1 {
        out.push_str(DIGIT[d(s[0])]);
    } else {
        component(s, out);
    }
}

fn tail<O: Emit>(s: &[u8], out: &mut O) {
    if let Some(p) = s.iter().position(|&c| c != b'0') {
        component(&s[p..], out);
    }
}

/// `graph_num` on an ASCII digit string, appended to `out`; `false` (and `out` untouched) when the FST rejects it.
pub fn read_into<O: Emit>(s: &[u8], out: &mut O) -> bool {
    if s == b"0" {
        out.push('영');
        return true;
    }
    if s.is_empty() || s.len() > 17 || s[0] == b'0' || !s.iter().all(u8::is_ascii_digit) {
        return false;
    }
    component(s, out);
    true
}

/// `graph_num` on chars, appended to `out`; `false` (and `out` untouched) when rejected.
pub fn read_chars_into(t: &[char], out: &mut String) -> bool {
    if t.is_empty() || t.len() > 17 || !t.iter().all(char::is_ascii_digit) {
        return false;
    }
    let mut buf = [0u8; 17];
    for (b, &c) in buf.iter_mut().zip(t) {
        *b = c as u8;
    }
    read_into(&buf[..t.len()], out)
}
