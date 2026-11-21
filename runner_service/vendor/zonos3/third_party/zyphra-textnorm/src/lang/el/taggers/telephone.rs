use super::*;

struct PhoneDigits {
    bytes: [u8; 15],
    len: usize,
}
impl PhoneDigits {
    fn new() -> Self {
        Self { bytes: [0; 15], len: 0 }
    }
    fn push(&mut self, c: char) -> Option<()> {
        if self.len == self.bytes.len() {
            return None;
        }
        self.bytes[self.len] = c as u8;
        self.len += 1;
        Some(())
    }
    fn as_str(&self) -> &str {
        std::str::from_utf8(&self.bytes[..self.len]).unwrap()
    }
}
fn phone_segment(s: &str, min: usize, max: usize) -> Option<PhoneDigits> {
    let mut digits = PhoneDigits::new();
    let mut last_separator = false;
    for c in s.chars() {
        if c.is_ascii_digit() {
            digits.push(c)?;
            last_separator = false;
        } else if matches!(c, ' ' | '-' | '.' | '(' | ')') && digits.len != 0 && !last_separator {
            last_separator = true;
        } else {
            return None;
        }
    }
    if last_separator || !(min..=max).contains(&digits.len) {
        return None;
    }
    Some(digits)
}
pub(in crate::lang::el) fn telephone_local_country(s: &str, greek: bool) -> Option<String> {
    let rest = s.strip_prefix('+')?;
    let (spoken, body) = if greek {
        (spoken!("τριάντα"), rest.strip_prefix("30")?)
    } else {
        let digit = rest.chars().next()?;
        if !digit.is_ascii_digit() {
            return None;
        }
        (number::digits(&rest[..1])?, &rest[1..])
    };
    let body = if body.starts_with([' ', '-', '.', '(', ')']) { &body[1..] } else { body };
    let digits = phone_segment(body, 7, 13)?;
    Some(spoken!("συν {spoken} {}", number::digits(digits.as_str())?))
}
pub(in crate::lang::el) fn telephone(s: &str) -> Option<(String, f64)> {
    let sep = |c: char| matches!(c, ' ' | '-' | '.' | '(' | ')');
    if let Some(x) = s.strip_prefix('+') {
        let mut digits = PhoneDigits::new();
        for c in x.chars() {
            if c.is_ascii_digit() {
                digits.push(c)?;
            } else if !sep(c) {
                return None;
            }
        }
        if (7..=15).contains(&digits.len) {
            return Some((spoken!("συν {}", number::digits(digits.as_str())?), -0.1));
        }
    }
    if let Some(x) = s.strip_prefix('(') {
        let (area, body) = x.split_once(')')?;
        let a = phone_segment(area, 2, 5)?;
        let body = if body.starts_with([' ', '-', '.', '(', ')']) { &body[1..] } else { body };
        let b = phone_segment(body, 7, 13)?;
        return Some((spoken!("{} {}", number::digits(a.as_str())?, number::digits(b.as_str())?), 0.0));
    }
    if let Some((a, b)) = s.split_once('-') {
        if (2..=4).contains(&a.len()) && a.bytes().all(|c| c.is_ascii_digit()) {
            if let Some(digits) = phone_segment(b, 6, 9) {
                return Some((spoken!("{} {}", number::digits(a)?, number::digits(digits.as_str())?), 0.0));
            }
        }
    }
    None
}
