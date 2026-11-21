//! Swedish cardinal classifier, serial codes, ranges, and quoted numbers.

use super::number::{cardinal, spell_digits};

/// A path through `graph` / `graph_en`, before its output is materialized.
#[derive(Clone, Copy)]
pub(super) struct CardinalRecipe<'a> {
    pub end: usize,
    pub cost: crate::text::Cost,
    pub raw: &'a [char],
    pub common: bool,
    pub counted: bool,
}

impl CardinalRecipe<'_> {
    pub fn render(self) -> String {
        let mut out = String::new();
        self.append(&mut out);
        out
    }

    pub(super) fn append(self, out: &mut String) {
        let mut digits = [0u8; 24];
        let mut len = 0;
        for &c in self.raw {
            if c.is_ascii_digit() {
                digits[len] = c as u8;
                len += 1;
            }
        }
        let digits = std::str::from_utf8(&digits[..len]).expect("ASCII digits");
        super::number::cardinal_into(out, digits, self.common, self.counted).expect("validated cardinal graph input");
    }
}

/// Enumerate the source cardinal graph, including every valid grouped prefix.
pub(super) fn recipes<'a>(
    text: &'a [char],
    start: usize,
    common: bool,
    counted: bool,
    mut emit: impl FnMut(CardinalRecipe<'a>),
) {
    if !text.get(start).is_some_and(|c| c.is_ascii_digit()) {
        return;
    }
    let mut digits = [0u8; 24];
    let mut n = 0usize;
    let mut group = 0usize;
    let mut grouped = false;
    let mut end = start;
    while let Some(&c) = text.get(end) {
        if c.is_ascii_digit() {
            if n == 24 || (n > 0 && digits[0] == 0) {
                break;
            }
            digits[n] = c as u8 - b'0';
            n += 1;
            group += 1;
            end += 1;
            if !grouped || group == 3 {
                let mut magnitude_ones = 0;
                for chunk in digits[..n].rchunks(3).skip(2) {
                    if chunk.last() == Some(&1) && chunk[..chunk.len() - 1].iter().all(|d| *d == 0) {
                        magnitude_ones += 1;
                    }
                }
                emit(CardinalRecipe {
                    end,
                    cost: crate::text::cost(-0.001) * magnitude_ones,
                    raw: &text[start..end],
                    common,
                    counted,
                });
            }
            if grouped && group == 3 {
                if text.get(end) == Some(&' ') {
                    group = 0;
                    end += 1;
                } else {
                    break;
                }
            }
        } else if c == ' ' && !grouped && (1..=3).contains(&group) {
            grouped = true;
            group = 0;
            end += 1;
        } else {
            break;
        }
    }
}

/// Enumerate sentence-level cardinal paths without spelling losing candidates.
pub(super) fn classify(text: &[char], start: usize, sink: &mut super::Sink) {
    let negative = text.get(start) == Some(&'-');
    let first = start + usize::from(negative);
    if !text.get(first).is_some_and(|c| c.is_ascii_digit()) {
        return;
    }
    let mut end = first;
    let mut count = 0usize;
    let mut group = 0usize;
    let mut separator = None;
    let mut digits = [0u8; 24];
    let mut rank = 0;
    while let Some(&ch) = text.get(end) {
        if ch.is_ascii_digit() {
            if count < 24 {
                digits[count] = ch as u8;
            }
            count += 1;
            group += 1;
            end += 1;
            if separator.is_some() && group != 3 {
                continue;
            }
            let leading_zero = count > 1 && text[first] == '0';
            let spelled = leading_zero && !negative && separator.is_none();
            if !spelled && (leading_zero || count > 24) {
                continue;
            }
            let inner_cost = if spelled {
                0
            } else {
                let ones =
                    digits[..count].rchunks(3).skip(2).filter(|chunk| matches!(*chunk, b"1" | b"01" | b"001")).count();
                crate::text::cost(-0.001) * ones as crate::text::Cost
            };
            sink.push(end, crate::text::cost(1.10) + inner_cost, 750 + rank, || {
                let sign = if negative { "negative: \"true\" " } else { "" };
                let mut tag = format!("cardinal {{ {sign}integer: \"");
                if spelled {
                    for (i, &digit) in text[first..end].iter().enumerate() {
                        if i != 0 {
                            tag.push(' ');
                        }
                        tag.push_str(super::number::spell_digit(digit as u8 - b'0'));
                    }
                } else {
                    let mut raw = [0u8; 24];
                    let mut len = 0;
                    for &digit in &text[first..end] {
                        if digit.is_ascii_digit() {
                            raw[len] = digit as u8;
                            len += 1;
                        }
                    }
                    let digits = std::str::from_utf8(&raw[..len]).expect("ASCII digits");
                    super::number::cardinal_into(&mut tag, digits, false, false).expect("validated cardinal");
                }
                tag.push_str("\" }");
                tag
            });
            rank += 1;
        } else if matches!(ch, ' ' | '.') && text.get(end + 1).is_some_and(|c| c.is_ascii_digit()) {
            if group == 0
                || (separator.is_none() && group > 3)
                || (separator.is_some() && group != 3)
                || separator.is_some_and(|old| old != ch)
            {
                break;
            }
            separator = Some(ch);
            group = 0;
            end += 1;
        } else {
            break;
        }
    }
}

/// Enumerate range endpoints with borrowed cardinal recipes.
pub(super) fn classify_range(text: &[char], start: usize, sink: &mut super::Sink) {
    let first_negative = text.get(start) == Some(&'-');
    let first_start = start + usize::from(first_negative);
    let mut rank = 0;
    recipes(text, first_start, false, false, |first| {
        if !matches!(text.get(first.end), Some('-' | '–' | '—')) {
            return;
        }
        let second_start = first.end + 1;
        let second_negative = text.get(second_start) == Some(&'-');
        recipes(text, second_start + usize::from(second_negative), false, false, |second| {
            sink.push(second.end, crate::text::cost(1.10) + first.cost + second.cost, 700 + rank, || {
                let a = first.render();
                let b = second.render();
                let a_sign = if first_negative { "minus " } else { "" };
                let b_sign = if second_negative { "minus " } else { "" };
                format!("cardinal {{ integer: \"{a_sign}{a} till {b_sign}{b}\" }}")
            });
            rank += 1;
        });
    });
}

/// Recognize serials without constructing their spoken components.
pub(super) fn classify_serial(text: &[char], start: usize, sink: &mut super::Sink) {
    let rem = &text[start..];
    let base_len = rem.iter().take_while(|c| c.is_alphabetic() || c.is_ascii_digit()).count();
    if base_len > 0 && rem.get(base_len) == Some(&'^') {
        let mut p = base_len + 1;
        if matches!(rem.get(p), Some('-' | '−')) {
            p += 1;
        }
        let first = p;
        while rem.get(p).is_some_and(|c| c.is_ascii_digit()) {
            p += 1;
        }
        if p > first {
            let significant = rem[first..p].iter().position(|c| *c != '0').map_or(0, |n| p - first - n);
            if significant > 24 {
                return;
            }
            sink.push(start + p, crate::text::cost(2.00), 1500, || {
                render_serial(text, start).expect("validated serial").1
            });
            return;
        }
    }
    let mut end = 0;
    let (mut digit, mut alpha, mut separator) = (false, false, false);
    while let Some(&ch) = rem.get(end) {
        if ch.is_alphabetic() {
            alpha = true;
        } else if ch.is_ascii_digit() {
            digit = true;
        } else if matches!(ch, '-' | '–' | '/' | ':' | '.' | '_')
            && rem.get(end + 1).is_some_and(|c| c.is_alphabetic() || c.is_ascii_digit())
        {
            separator = true;
        } else {
            break;
        }
        end += 1;
    }
    if digit && (alpha || separator) {
        sink.push(start + end, crate::text::cost(2.00), 1500, || {
            render_serial(text, start).expect("validated serial").1
        });
    }
}
/// Matches serial alphanumeric codes (weight 2.0).
/// e.g. "v2.0", "3.11.2", "COVID-19", "A4", "BA249", "x^2", "y^3".
fn render_serial(text: &[char], start: usize) -> Option<(usize, String)> {
    let rem = &text[start..];
    if rem.is_empty() {
        return None;
    }

    // Variable power: <alpha|number>^<number>
    if let Some(caret_idx) = rem.iter().position(|&c| c == '^') {
        if caret_idx > 0 && caret_idx + 1 < rem.len() {
            let base_slice = &rem[..caret_idx];
            let exp_slice = &rem[caret_idx + 1..];
            let base_valid = base_slice.iter().all(|c| c.is_alphabetic() || c.is_ascii_digit());
            if base_valid {
                let (exp_neg, exp_s) = match exp_slice.first() {
                    Some(&'-' | &'−') => (true, &exp_slice[1..]),
                    _ => (false, exp_slice),
                };
                let exp_len = exp_s.iter().take_while(|c| c.is_ascii_digit()).count();
                if exp_len > 0 {
                    let exp_digits: String = exp_s[..exp_len].iter().collect();
                    let exp_spoken = cardinal(&exp_digits, false, false)?;
                    let exp_str = if exp_neg { format!("minus {exp_spoken}") } else { exp_spoken };
                    let base_str: String = base_slice.iter().collect();
                    let base_spoken =
                        if let Some(b_card) = cardinal(&base_str, false, false) { b_card } else { base_str };
                    let total_end = caret_idx + 1 + if exp_neg { 1 } else { 0 } + exp_len;
                    let tag = format!("cardinal {{ integer: \"{base_spoken} upphöjt till {exp_str}\" }}");
                    return Some((start + total_end, tag));
                }
            }
        }
    }

    // General serial: contains at least one digit and letters/separators
    let mut end = 0;
    let mut has_digit = false;
    let mut has_alpha = false;
    let mut has_sep = false;

    while end < rem.len() {
        let c = rem[end];
        if c.is_alphabetic() {
            has_alpha = true;
            end += 1;
        } else if c.is_ascii_digit() {
            has_digit = true;
            end += 1;
        } else if matches!(c, '-' | '–' | '/' | ':' | '.' | '_') {
            // Check if followed by alphanumeric
            if end + 1 < rem.len() && (rem[end + 1].is_alphabetic() || rem[end + 1].is_ascii_digit()) {
                has_sep = true;
                end += 1;
            } else {
                break;
            }
        } else {
            break;
        }
    }

    if end == 0 || !has_digit || (!has_alpha && !has_sep) {
        return None;
    }

    // Split the serial into components
    let slice = &rem[..end];
    let mut pieces = Vec::new();
    let mut idx = 0;
    while idx < slice.len() {
        let c = slice[idx];
        if c.is_alphabetic() {
            let start_alpha = idx;
            while idx < slice.len() && slice[idx].is_alphabetic() {
                idx += 1;
            }
            let s: String = slice[start_alpha..idx].iter().collect();
            pieces.push(s);
        } else if c.is_ascii_digit() {
            let start_digit = idx;
            while idx < slice.len() && slice[idx].is_ascii_digit() {
                idx += 1;
            }
            let s: String = slice[start_digit..idx].iter().collect();
            if s.starts_with('0') && s.len() > 1 {
                pieces.push(spell_digits(&s));
            } else if let Some(card_str) = cardinal(&s, false, false) {
                pieces.push(card_str);
            } else {
                pieces.push(s);
            }
        } else if c == '.'
            && idx > 0
            && idx + 1 < slice.len()
            && slice[idx - 1].is_ascii_digit()
            && slice[idx + 1].is_ascii_digit()
        {
            pieces.push("punkt".to_string());
            idx += 1;
        } else {
            // other separators become spaces
            idx += 1;
        }
    }

    if pieces.is_empty() {
        return None;
    }
    let spoken = pieces.join(" ");
    Some((start + end, format!("cardinal {{ integer: \"{spoken}\" }}")))
}
