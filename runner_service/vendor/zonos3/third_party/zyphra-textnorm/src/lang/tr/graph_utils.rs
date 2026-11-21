//! Native Turkish number and suffix morphology from `tr/graph_utils.py` and
//! `tr/taggers/cardinal.py`.

const DIGITS: [&str; 10] = ["sıfır", "bir", "iki", "üç", "dört", "beş", "altı", "yedi", "sekiz", "dokuz"];
const TENS: [&str; 10] = ["", "on", "yirmi", "otuz", "kırk", "elli", "altmış", "yetmiş", "seksen", "doksan"];

fn push_word(out: &mut String, word: &str) {
    if !out.is_empty() {
        out.push(' ');
    }
    out.push_str(word);
}

/// Parse the cardinal input without allocating, including group separators.
fn parse_positive(input: &str) -> Option<([u8; 15], usize)> {
    let mut digits = [0u8; 15];
    let mut len = 0usize;
    let mut boundaries = 0u16;
    for ch in input.chars() {
        if ch.is_ascii_digit() {
            if len == digits.len() {
                return None;
            }
            digits[len] = ch as u8 - b'0';
            len += 1;
        } else if matches!(ch, '.' | ' ' | '\u{202f}') {
            let bit = 1u16 << len;
            if boundaries & bit != 0 {
                return None;
            }
            boundaries |= bit;
        } else {
            return None;
        }
    }
    if len == 0 || (len > 1 && digits[0] == 0) {
        return None;
    }
    if boundaries != 0 {
        let mut valid = 0u16;
        let mut b = len;
        while b > 3 {
            b -= 3;
            valid |= 1u16 << b;
        }
        if boundaries & !valid != 0 {
            return None;
        }
    }
    Some((digits, len))
}

/// Validate the cardinal graph without constructing a spoken number.
pub fn is_positive(input: &str) -> bool {
    let bytes = input.as_bytes();
    if bytes.is_empty() || (bytes.len() > 1 && bytes[0] == b'0') {
        return false;
    }
    if bytes.len() <= 15 && bytes.iter().all(u8::is_ascii_digit) {
        return true;
    }
    parse_positive(input).is_some()
}

/// Append one cardinal reading using the requested word separator.
pub fn append_positive(out: &mut String, input: &str, separator: char) -> Option<()> {
    let (digits, len) = parse_positive(input)?;
    let start = out.len();
    let mut word = |s: &str| {
        if out.len() != start {
            out.push(separator);
        }
        out.push_str(s);
    };
    if len == 1 && digits[0] == 0 {
        word(DIGITS[0]);
        return Some(());
    }
    let mut pos = 0;
    let mut width = (len - 1) % 3 + 1;
    while pos < len {
        let mut n = 0u16;
        for d in &digits[pos..pos + width] {
            n = n * 10 + u16::from(*d);
        }
        let scale = (len - pos - width) / 3;
        if n != 0 {
            if !(scale == 1 && n == 1) {
                let h = n / 100;
                if h != 0 {
                    if h != 1 {
                        word(DIGITS[h as usize]);
                    }
                    word("yüz");
                }
                let t = n / 10 % 10;
                if t != 0 {
                    word(TENS[t as usize]);
                }
                let d = n % 10;
                if d != 0 {
                    word(DIGITS[d as usize]);
                }
            }
            if scale != 0 {
                word(["", "bin", "milyon", "milyar", "trilyon"][scale]);
            }
        }
        pos += width;
        width = 3;
    }
    Some(())
}

/// `positive_cardinals`: up to 999 trillion, optionally grouped.
pub fn positive(input: &str) -> Option<String> {
    let mut result = String::new();
    append_positive(&mut result, input, ' ')?;
    Some(result)
}

pub fn digit_sequence(input: &str) -> Option<String> {
    if input.is_empty() || !input.bytes().all(|c| c.is_ascii_digit()) {
        return None;
    }
    let mut result = String::new();
    for c in input.bytes() {
        push_word(&mut result, DIGITS[(c - b'0') as usize]);
    }
    Some(result)
}

/// Apply the source's ordered phonological rewrites to a marked suffix.
pub fn glue(stem: &str, suffix: &str) -> String {
    let mut s = format!("{stem}+{suffix}");
    for (from, to) in [
        ("dört+e", "dörde"),
        ("dört+ü", "dördü"),
        ("dört+i", "dördi"),
        ("dört+a", "dörda"),
        ("iki+inci", "ikinci"),
        ("iki+nci", "ikinci"),
        ("altı+ıncı", "altıncı"),
        ("altı+ncı", "altıncı"),
        ("yedi+inci", "yedinci"),
        ("yedi+nci", "yedinci"),
    ] {
        s = s.replace(from, to);
    }
    s.replace('+', "")
}

pub fn ordinal(input: &str) -> Option<String> {
    let mut s = positive(input)?;
    let endings = [
        ("sıfır", "sıfırıncı"),
        ("bir", "birinci"),
        ("iki", "ikinci"),
        ("üç", "üçüncü"),
        ("dört", "dördüncü"),
        ("beş", "beşinci"),
        ("altı", "altıncı"),
        ("yedi", "yedinci"),
        ("sekiz", "sekizinci"),
        ("dokuz", "dokuzuncu"),
        ("on", "onuncu"),
        ("yirmi", "yirminci"),
        ("otuz", "otuzuncu"),
        ("kırk", "kırkıncı"),
        ("elli", "ellinci"),
        ("altmış", "altmışıncı"),
        ("yetmiş", "yetmişinci"),
        ("seksen", "sekseninci"),
        ("doksan", "doksanıncı"),
        ("yüz", "yüzüncü"),
        ("bin", "bininci"),
        ("milyon", "milyonuncu"),
        ("milyar", "milyarıncı"),
        ("trilyon", "trilyonuncu"),
    ];
    if let Some((from, to)) = endings
        .iter()
        .find(|(from, _)| s.strip_suffix(*from).is_some_and(|prefix| prefix.is_empty() || prefix.ends_with(' ')))
    {
        s.truncate(s.len() - from.len());
        s.push_str(to);
    }
    Some(s)
}
