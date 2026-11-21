use super::number::{cardinal, digits};
fn number(s: &str) -> Option<String> {
    cardinal(s).or_else(|| if s.len() > 1 && s.starts_with('0') || s.len() >= 16 { digits(s) } else { None })
}
fn dotnum(s: &str) -> Option<String> {
    let values = s.split('.').map(number).collect::<Option<Vec<_>>>()?;
    Some(values.join(" titik "))
}
fn segment(s: &str) -> Option<String> {
    if s.is_empty() {
        return None;
    }
    let mut runs = Vec::new();
    let mut i = 0;
    while i < s.len() {
        let c = s.as_bytes()[i];
        if !c.is_ascii_alphanumeric() {
            return None;
        }
        let digit = c.is_ascii_digit();
        let start = i;
        while i < s.len() && s.as_bytes()[i].is_ascii_alphanumeric() && s.as_bytes()[i].is_ascii_digit() == digit {
            i += 1
        }
        runs.push((digit, &s[start..i]));
    }
    if runs.len() > 3 {
        return None;
    }
    let mut out = Vec::new();
    for (digit, s) in runs {
        out.push(if digit { number(s)? } else { s.to_owned() });
    }
    Some(out.join(" "))
}
fn segment_dot(s: &str) -> Option<String> {
    let mut parts = Vec::new();
    let mut i = 0;
    let bytes = s.as_bytes();
    while i < bytes.len() {
        let digit = bytes[i].is_ascii_digit();
        if !digit && !bytes[i].is_ascii_alphabetic() {
            return None;
        }
        let start = i;
        while i < bytes.len()
            && if digit { bytes[i].is_ascii_digit() || bytes[i] == b'.' } else { bytes[i].is_ascii_alphabetic() }
        {
            i += 1
        }
        let part = &s[start..i];
        parts.push(if digit { dotnum(part)? } else { part.to_owned() });
        if parts.len() > 3 {
            return None;
        }
    }
    if parts.is_empty() { None } else { Some(parts.join(" ")) }
}
fn pieces(s: &str) -> Option<(String, f64)> {
    let mut out = Vec::new();
    let mut weight = 0.0;
    for item in s.split(['-', '/', ':', '_']) {
        if let Some(v) = segment(item).or_else(|| segment_dot(item)) {
            out.push(v);
            continue;
        }
        for part in item.split('.') {
            out.push(segment(part)?);
        }
        weight += 0.1 * (item.matches('.').count() as f64);
    }
    Some((out.join(" "), weight))
}
fn segment_valid(s: &str, dots: bool) -> bool {
    if s.is_empty() {
        return false;
    }
    let bytes = s.as_bytes();
    let mut i = 0;
    let mut runs = 0;
    while i < bytes.len() {
        let digit = bytes[i].is_ascii_digit();
        if !digit && !bytes[i].is_ascii_alphabetic() {
            return false;
        }
        let start = i;
        while i < bytes.len()
            && if digit {
                bytes[i].is_ascii_digit() || (dots && bytes[i] == b'.')
            } else {
                bytes[i].is_ascii_alphabetic()
            }
        {
            i += 1;
        }
        runs += 1;
        if runs > 3 {
            return false;
        }
        if digit && !s[start..i].split('.').all(|part| number(part).is_some()) {
            return false;
        }
    }
    true
}
fn serial_recognize(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    for power in ['^', '²', '³'] {
        if let Some((base, exp)) = s.split_once(power) {
            let base_ok = base.len() == 1 && base.as_bytes()[0].is_ascii_alphabetic()
                || base.len() <= 3 && base != "0" && cardinal(base).is_some();
            let exp_ok = if power == '²' || power == '³' {
                exp.is_empty()
            } else {
                let exp = exp.strip_prefix('-').or_else(|| exp.strip_prefix('−')).unwrap_or(exp);
                exp.len() <= 4 && cardinal(exp).is_some()
            };
            if base_ok && exp_ok {
                out.push((String::new(), 0.0));
            }
        }
    }
    if s.bytes().any(|b| b.is_ascii_digit()) {
        let mut weight = 0.0;
        let mut valid = true;
        for item in s.split(['-', '/', ':', '_']) {
            if segment_valid(item, false) || segment_valid(item, true) {
                continue;
            }
            if !item.split('.').all(|part| segment_valid(part, false)) {
                valid = false;
                break;
            }
            weight += 0.1 * item.matches('.').count() as f64;
        }
        if valid {
            out.push((String::new(), weight));
        }
    }
    out
}
pub(crate) fn serial(s: &str) -> Vec<(String, f64)> {
    if super::lazy::recognizing() {
        return serial_recognize(s);
    }
    let mut out = Vec::new();
    for power in ['^', '²', '³'] {
        if let Some((base, exp)) = s.split_once(power) {
            let base = if base.len() == 1 && base.as_bytes()[0].is_ascii_alphabetic() {
                Some(base.to_owned())
            } else if base.len() <= 3 && base != "0" {
                cardinal(base)
            } else {
                None
            };
            let exp = if power == '²' {
                if exp.is_empty() { Some("dua".to_owned()) } else { None }
            } else if power == '³' {
                if exp.is_empty() { Some("tiga".to_owned()) } else { None }
            } else {
                let (exp, neg) = if let Some(x) = exp.strip_prefix('-').or_else(|| exp.strip_prefix('−')) {
                    (x, "negatif ")
                } else {
                    (exp, "")
                };
                if exp.len() > 4 { None } else { cardinal(exp).map(|x| format!("{neg}{x}")) }
            };
            if let (Some(base), Some(exp)) = (base, exp) {
                out.push((format!("cardinal {{ integer: \"{base} kuasa {exp}\" }}"), 0.0));
            }
        }
    }
    if s.bytes().any(|b| b.is_ascii_digit()) {
        if let Some((body, w)) = pieces(s) {
            out.push((format!("cardinal {{ integer: \"{body}\" }}"), w));
        }
    }
    out
}
