//! Swedish ordinal classifier: dot ordinals, suffixed ordinals (:a, :e), and "kapitlet".

use super::number::ordinal;

/// Enumerate ordinal paths before spelling the selected one.
pub(super) fn classify(text: &[char], start: usize, sink: &mut super::Sink) {
    let rem = &text[start..];
    let len = rem.iter().take_while(|c| c.is_ascii_digit()).count();
    if len == 0 || len > 24 {
        return;
    }
    let mut bytes = [0u8; 24];
    for (out, ch) in bytes.iter_mut().zip(&rem[..len]) {
        *out = *ch as u8;
    }
    let digits = std::str::from_utf8(&bytes[..len]).expect("ASCII digits");
    let mut end = len;
    while end > 3 {
        end -= 3;
        let begin = end.saturating_sub(3);
        let group = bytes[begin..end].iter().fold(0u16, |n, b| n * 10 + (b - b'0') as u16);
        if (2..100).contains(&group) {
            return;
        }
    }
    let mut rank = 0;
    let mut emit = |end: usize, chapter: bool| {
        sink.push(start + end, crate::text::cost(1.10), 800 + rank, || {
            let spoken = ordinal(digits).expect("validated ordinal");
            let suffix = if chapter { " kapitlet" } else { "" };
            format!("ordinal {{ integer: \"{spoken}{suffix}\" }}")
        });
        rank += 1;
    };
    if rem.get(len) == Some(&'.') && (digits == "0" || rem[0] != '0') {
        emit(len + 1, false);
    }
    if rem.get(len) == Some(&':') && rem.get(len + 1).is_some_and(|&s| valid_suffix(digits, s)) {
        emit(len + 2, false);
    }
    if rem.get(len) == Some(&' ') && rem[0] != '0' {
        let after = &rem[len + 1..];
        let chapter = if after.starts_with(&['k', 'a', 'p', 'i', 't', 'l', 'e', 't']) {
            Some(8)
        } else if after.starts_with(&['k', 'a', 'p']) && after.get(3).map_or(true, |c| !c.is_alphabetic()) {
            Some(3)
        } else {
            None
        };
        if let Some(n) = chapter {
            emit(len + 1 + n, true);
        }
    }
}

pub(super) fn valid_suffix(digits: &str, suffix: char) -> bool {
    if digits.is_empty() || digits.len() > 24 || (digits.len() > 1 && digits.starts_with('0')) {
        return false;
    }
    let bytes = digits.as_bytes();
    let last = bytes[bytes.len() - 1] - b'0';
    let last_two = if bytes.len() > 1 { (bytes[bytes.len() - 2] - b'0') * 10 + last } else { last };
    match suffix {
        'a' | 'A' => (last == 1 || last == 2) && last_two != 11 && last_two != 12,
        'e' | 'E' => e_format(bytes),
        _ => false,
    }
}

// The source e-suffix input is a closure over single non-1/2 digits and
// longer cardinal-format pieces followed by the source's two-digit endings.
fn e_format(digits: &[u8]) -> bool {
    if digits.len() > 24 {
        return false;
    }
    let mut reachable = [false; 25];
    reachable[0] = true;
    for start in 0..digits.len() {
        if !reachable[start] {
            continue;
        }
        if digits[start] != b'1' && digits[start] != b'2' {
            reachable[start + 1] = true;
        }
        if digits[start] == b'0' {
            continue;
        }
        for end in start + 3..=digits.len() {
            let a = digits[end - 2];
            let b = digits[end - 1];
            if a == b'1' || (a != b'1' && b != b'1' && b != b'2') {
                reachable[end] = true;
            }
        }
    }
    reachable[digits.len()]
}
