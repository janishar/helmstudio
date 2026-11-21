use super::*;

pub(in crate::lang::el) fn grouped(s: &str) -> Option<String> {
    for sep in ['.', ',', ' '] {
        let mut chunks = s.split(sep);
        let Some(first) = chunks.next() else { continue };
        if first.is_empty() || first.len() > 3 || !first.bytes().all(|b| b.is_ascii_digit()) {
            continue;
        }
        let mut digits = [0u8; 15];
        digits[..first.len()].copy_from_slice(first.as_bytes());
        let mut len = first.len();
        let mut groups = 0;
        let mut valid = true;
        for chunk in chunks {
            if chunk.len() != 3 || len + 3 > digits.len() || !chunk.bytes().all(|b| b.is_ascii_digit()) {
                valid = false;
                break;
            }
            digits[len..len + 3].copy_from_slice(chunk.as_bytes());
            len += 3;
            groups += 1;
        }
        if valid && groups > 0 {
            let raw = std::str::from_utf8(&digits[..len]).ok()?;
            if let Some(x) = number::cardinal(raw) {
                return Some(x);
            }
        }
    }
    None
}
pub(in crate::lang::el) fn decimal_core(s: &str) -> Option<String> {
    let (a, b) = s.split_once([',', '.'])?;
    let integer = number::cardinal(a)?;
    let fraction = fractional(b)?;
    Some(spoken!("{integer} κόμμα {fraction}"))
}
fn fractional(b: &str) -> Option<String> {
    if b.is_empty() || !b.bytes().all(|c| c.is_ascii_digit()) {
        return None;
    }
    if b.len() == 1 {
        number::cardinal(b)
    } else if b.len() == 2 && b.starts_with('0') {
        number::digits(b)
    } else if b.len() == 2 {
        number::cardinal(b)
    } else {
        number::digits(b)
    }
}
pub(in crate::lang::el) fn decimal(s: &str) -> Option<String> {
    for (p, w) in [("~", "περίπου "), ("±", "συν πλην ")] {
        if let Some(x) = s.strip_prefix(p) {
            return Some(spoken!("{w}{}", decimal_unmodified(x)?));
        }
    }
    decimal_unmodified(s)
}
pub(in crate::lang::el) fn decimal_unmodified(s: &str) -> Option<String> {
    for (p, w) in [("-", "μείον "), ("−", "μείον ")] {
        if let Some(x) = s.strip_prefix(p) {
            return Some(spoken!("{w}{}", decimal_core(x)?));
        }
    }
    if let Some(x) = grouped(s) {
        return Some(x);
    }
    for (sep, dec) in [('.', ','), (',', '.'), (' ', ','), (' ', '.')] {
        if let Some((a, b)) = s.rsplit_once(dec) {
            if a.contains(sep) {
                if let (Some(left), Some(right)) = (grouped(a), fractional(b)) {
                    return Some(spoken!("{left} κόμμα {right}"));
                }
            }
        }
    }
    if let Some(x) = decimal_core(s) {
        return Some(x);
    }
    for (suffix, word) in [
        ("k", "χιλιάδες"),
        ("K", "χιλιάδες"),
        ("M", "εκατομμύρια"),
        ("B", "δισεκατομμύρια"),
        (" εκατ.", "εκατομμύρια"),
        (" εκατ", "εκατομμύρια"),
        (" δισ.", "δισεκατομμύρια"),
        (" δισ", "δισεκατομμύρια"),
        (" εκατομμύριο", "εκατομμύριο"),
        (" εκατομμύρια", "εκατομμύρια"),
        (" δισεκατομμύριο", "δισεκατομμύριο"),
        (" δισεκατομμύρια", "δισεκατομμύρια"),
        (" τρισεκατομμύριο", "τρισεκατομμύριο"),
        (" τρισεκατομμύρια", "τρισεκατομμύρια"),
    ] {
        if let Some(x) = s.strip_suffix(suffix) {
            let x = x.strip_suffix(' ').unwrap_or(x);
            return Some(spoken!("{} {word}", decimal_core(x).or_else(|| number::cardinal(x))?));
        }
    }
    None
}
