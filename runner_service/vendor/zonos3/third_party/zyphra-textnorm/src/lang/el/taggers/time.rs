use super::*;

pub(in crate::lang::el) fn clock(s: &str, allow_seconds: bool) -> Option<String> {
    let mut parts = s.split(':');
    let (hours, minutes) = (parts.next()?, parts.next()?);
    let seconds = parts.next();
    if parts.next().is_some()
        || (seconds.is_some() && !allow_seconds)
        || hours.is_empty()
        || hours.len() > 2
        || minutes.len() != 2
        || !hours.bytes().all(|c| c.is_ascii_digit())
        || !minutes.bytes().all(|c| c.is_ascii_digit())
    {
        return None;
    }
    let h: usize = hours.parse().ok()?;
    let m: usize = minutes.parse().ok()?;
    if h > 23 || m > 59 || (hours.len() == 2 && hours.starts_with('0') && h >= 10) {
        return None;
    }
    let hour = if h == 0 { "0" } else { hours.trim_start_matches('0') };
    let mut v = number::agree(&number::cardinal(hour)?, false);
    if m > 0 {
        v.push_str(&spoken!(" και {}", number::cardinal(minutes.trim_start_matches('0'))?));
    }
    if let Some(seconds) = seconds {
        if seconds.len() != 2 || !seconds.bytes().all(|c| c.is_ascii_digit()) {
            return None;
        }
        let sec: usize = seconds.parse().ok()?;
        if sec > 59 {
            return None;
        }
        if sec > 0 {
            v.push_str(&spoken!(" και {} δευτερόλεπτα", number::cardinal(seconds.trim_start_matches('0'))?));
        }
    }
    Some(v)
}
pub(in crate::lang::el) fn time(s: &str) -> Option<String> {
    if s == "00:00" || s == "0:00" {
        return Some(spoken!("μεσάνυχτα"));
    }
    for sep in [" - ", " – ", " — ", "-", "–", "—"] {
        if let Some((a, b)) = s.split_once(sep) {
            if let (Some(a), Some(b)) = (clock(a, false), clock(b, false)) {
                return Some(spoken!("{a} έως {b}"));
            }
        }
    }
    for (suffix, word) in [
        ("π.μ.", "π.μ."),
        ("π.μ", "π.μ."),
        ("πμ", "π.μ."),
        ("μ.μ.", "μ.μ."),
        ("μ.μ", "μ.μ."),
        ("μμ", "μ.μ."),
        ("AM", "π.μ."),
        ("PM", "μ.μ."),
        ("am", "π.μ."),
        ("pm", "μ.μ."),
    ] {
        if let Some(x) = s.strip_suffix(suffix) {
            let x = x.strip_suffix(' ').unwrap_or(x);
            if let Some(x) = clock(x, true) {
                return Some(spoken!("{x} {word}"));
            }
        }
    }
    for tz in ["UTC", "GMT", "EET", "EEST", "CET", "CEST"] {
        if let Some(x) = s.strip_suffix(tz).and_then(|x| x.strip_suffix(' ')) {
            if let Some(x) = clock(x, true) {
                return Some(spoken!("{x} {tz}"));
            }
        }
    }
    clock(s, true)
}
