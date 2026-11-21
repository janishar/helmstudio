use super::number::{cardinal, digits};

fn number(s: &str) -> Option<String> {
    cardinal(s).or_else(|| if s.len() > 1 && s.starts_with('0') || s.len() >= 16 { digits(s) } else { None })
}

#[derive(Clone, Copy)]
enum Exponent<'a> {
    Square,
    Cube,
    Number { raw: &'a str, negative: bool },
}

fn power(s: &str) -> Option<(char, Exponent<'_>)> {
    let letter = s.chars().next()?;
    if !letter.is_ascii_alphabetic() {
        return None;
    }
    let rest = &s[1..];
    match rest {
        "^2" | "²" => Some((letter, Exponent::Square)),
        "^3" | "³" => Some((letter, Exponent::Cube)),
        _ => {
            let raw = rest.strip_prefix('^')?;
            let (raw, negative) = if let Some(raw) = raw.strip_prefix('-').or_else(|| raw.strip_prefix('−')) {
                (raw, true)
            } else {
                (raw, false)
            };
            // The source excludes 2/3 from this branch even after an optional minus.
            if !(1..=4).contains(&raw.len())
                || matches!(raw, "2" | "3")
                || !raw.bytes().all(|c| c.is_ascii_digit())
                || !super::lazy::recognize(|| cardinal(raw).is_some())
            {
                return None;
            }
            Some((letter, Exponent::Number { raw, negative }))
        }
    }
}

// cardinal.py: alnum_seg (delim alnum_seg)*. States 1..=6 are the
// alternating A/D runs (at most three); 7..=9 require a digit after a
// dot internal to D. A delimiter dot costs 0.1; a numeric dot costs zero.
// One first minimum-cost witness per state is sufficient: output does not
// affect any later arc or the enclosing sentence automaton.
fn path(s: &str, mut record: impl FnMut([u8; 10])) -> Option<(usize, usize)> {
    let mut current = [None; 10];
    current[0] = Some(0usize);
    for c in s.bytes() {
        let mut next = [None; 10];
        let mut back = [u8::MAX; 10];
        for (state, value) in current.into_iter().enumerate() {
            let Some(value) = value else { continue };
            let mut arc = |to: usize, extra: usize, numeric_dot: bool| {
                let candidate = value + extra;
                if next[to].is_none_or(|old| candidate < old) {
                    next[to] = Some(candidate);
                    back[to] = state as u8 | (u8::from(numeric_dot) << 4);
                }
            };
            if c.is_ascii_alphabetic() {
                match state {
                    0 | 1 => arc(1, 0, false),
                    2 | 3 => arc(3, 0, false),
                    4 | 5 => arc(5, 0, false),
                    _ => {}
                }
            } else if c.is_ascii_digit() {
                match state {
                    0 | 2 | 7 => arc(2, 0, false),
                    1 | 4 | 8 => arc(4, 0, false),
                    3 | 6 | 9 => arc(6, 0, false),
                    _ => {}
                }
            } else if c == b'.' {
                if (1..=6).contains(&state) {
                    arc(0, 1, false);
                }
                match state {
                    2 => arc(7, 0, true),
                    4 => arc(8, 0, true),
                    6 => arc(9, 0, true),
                    _ => {}
                }
            } else if matches!(c, b'-' | b'/' | b':' | b'_') {
                if (1..=6).contains(&state) {
                    arc(0, 0, false);
                }
            } else {
                return None;
            }
        }
        if next.iter().all(Option::is_none) {
            return None;
        }
        record(back);
        current = next;
    }
    (1..=6).filter_map(|state| current[state].map(|cost| (cost, state))).min()
}

fn append_word(out: &mut String, word: &str) {
    if !out.ends_with('"') {
        out.push(' ');
    }
    out.push_str(word);
}

/// The source serial graph's best reading at this span. All other readings at
/// the same span have a higher cost, or are later equal-key witnesses.
pub(crate) fn serial(s: &str) -> Option<(String, f64)> {
    if let Some((letter, exponent)) = power(s) {
        if super::lazy::recognizing() {
            return Some((String::new(), 0.0));
        }
        let mut tag = String::from("cardinal { integer: \"");
        tag.push(letter);
        match exponent {
            Exponent::Square => tag.push_str(" kuadrat"),
            Exponent::Cube => tag.push_str(" kubik"),
            Exponent::Number { raw, negative } => {
                tag.push_str(" pangkat ");
                if negative {
                    tag.push_str("negatif ");
                }
                tag.push_str(&cardinal(raw)?);
            }
        }
        tag.push_str("\" }");
        return Some((tag, 0.0));
    }
    if !s.bytes().any(|c| c.is_ascii_digit()) {
        return None;
    }
    if super::lazy::recognizing() {
        let (cost, _) = path(s, |_| {})?;
        return Some((String::new(), 0.1 * cost as f64));
    }
    let mut witness = Vec::with_capacity(s.len());
    let (cost, mut state) = path(s, |row| witness.push(row))?;
    for i in (0..s.len()).rev() {
        let previous = witness[i][state];
        debug_assert_ne!(previous, u8::MAX);
        // Reuse the back-pointer rows for selected-dot actions.
        witness[i][0] = previous >> 4;
        state = (previous & 15) as usize;
    }
    let mut tag = String::from("cardinal { integer: \"");
    let bytes = s.as_bytes();
    let mut i = 0;
    while i < bytes.len() {
        if bytes[i].is_ascii_alphabetic() {
            let start = i;
            while i < bytes.len() && bytes[i].is_ascii_alphabetic() {
                i += 1;
            }
            append_word(&mut tag, &s[start..i]);
        } else if bytes[i].is_ascii_digit() {
            let start = i;
            while i < bytes.len() && bytes[i].is_ascii_digit() {
                i += 1;
            }
            append_word(&mut tag, &number(&s[start..i])?);
        } else {
            if bytes[i] == b'.' && witness[i][0] != 0 {
                append_word(&mut tag, "titik");
            }
            i += 1;
        }
    }
    tag.push_str("\" }");
    Some((tag, 0.1 * cost as f64))
}
