use super::number::{cardinal, digit};

fn padded(s: &str, allow_zero: bool) -> Option<String> {
    let mut chars = s.chars();
    let first = digit(chars.next()?)?;
    let second = match chars.next() {
        Some(c) => Some(digit(c)?),
        None => None,
    };
    if chars.next().is_some() {
        return None;
    }
    let mut bytes = [b'0' + first, 0];
    let len = if let Some(second) = second {
        bytes[1] = b'0' + second;
        2
    } else {
        1
    };
    let digits = std::str::from_utf8(&bytes[..len]).ok()?;
    if digits == "00" && allow_zero {
        return (s == "00").then(String::new);
    }
    if digits.starts_with('0') && digits.len() == 2 {
        let w = cardinal(&digits[1..])?;
        return (w != "ศูนย์").then_some(w);
    }
    cardinal(digits)
}

fn clock(input: &str) -> Option<(String, i64)> {
    let mut s = input;
    let mut zone = "";
    for (suffix, word) in
        [(" UTC", " ยูทีซี"), (" GMT", " จีเอ็มที"), (" EST", " อีเอสที"), (" PST", " พีเอสที"), (" ICT", " ไอซีที")]
    {
        if let Some(rest) = s.strip_suffix(suffix) {
            s = rest;
            zone = word;
            break;
        }
    }
    let mut period = 0;
    if zone.is_empty() {
        for suffix in [" AM", " am", " A.M.", " a.m.", "AM", "am"] {
            if let Some(rest) = s.strip_suffix(suffix) {
                s = rest;
                period = 1;
                break;
            }
        }
        for suffix in [" PM", " pm", " P.M.", " p.m.", "PM", "pm"] {
            if let Some(rest) = s.strip_suffix(suffix) {
                s = rest;
                period = 2;
                break;
            }
        }
    }
    let (hour_input, rest) = s.split_once(':')?;
    let (minute_input, second_input) = match rest.split_once(':') {
        Some((m, sec)) if period == 0 && !sec.contains(':') => (m, Some(sec)),
        Some(_) => return None,
        None => (rest, None),
    };
    let hour = if period == 2 {
        if !hour_input.chars().all(|c| c.is_ascii_digit()) {
            return None;
        }
        let h: u32 = hour_input.parse().ok()?;
        if h == 0 || h > 12 || hour_input.len() > 2 {
            return None;
        }
        cardinal(&(if h == 12 { 12 } else { h + 12 }).to_string())?
    } else if hour_input.chars().count() == 2 && hour_input.chars().all(|c| digit(c) == Some(0)) {
        "ศูนย์".to_string()
    } else {
        padded(hour_input, false)?
    };
    if minute_input.chars().count() != 2 {
        return None;
    }
    let minute = padded(minute_input, true)?;
    let mut out = format!("{hour}นาฬิกา");
    if !minute.is_empty() {
        out.push_str(&minute);
        out.push_str("นาที");
    }
    if let Some(second_input) = second_input {
        if second_input.chars().count() != 2 {
            return None;
        }
        let second = padded(second_input, true)?;
        if !second.is_empty() {
            out.push_str(&second);
            out.push_str("วินาที");
        }
    }
    out.push_str(zone);
    let weight = if !zone.is_empty() {
        1_030_000
    } else if second_input.is_some() || period != 0 {
        1_040_000
    } else {
        1_050_000
    };
    Some((out, weight))
}

/// Validate the same clock shapes as `clock` without spelling any fields.
fn clock_cost(input: &str) -> Option<i64> {
    let mut s = input;
    let mut zone = false;
    for suffix in [" UTC", " GMT", " EST", " PST", " ICT"] {
        if let Some(rest) = s.strip_suffix(suffix) {
            s = rest;
            zone = true;
            break;
        }
    }
    let mut period = 0;
    if !zone {
        for suffix in [" AM", " am", " A.M.", " a.m.", "AM", "am"] {
            if let Some(rest) = s.strip_suffix(suffix) {
                s = rest;
                period = 1;
                break;
            }
        }
        for suffix in [" PM", " pm", " P.M.", " p.m.", "PM", "pm"] {
            if let Some(rest) = s.strip_suffix(suffix) {
                s = rest;
                period = 2;
                break;
            }
        }
    }
    let (hour, rest) = s.split_once(':')?;
    let (minute, second) = match rest.split_once(':') {
        Some((m, sec)) if period == 0 && !sec.contains(':') => (m, Some(sec)),
        Some(_) => return None,
        None => (rest, None),
    };
    let mut hs = hour.chars();
    let first = hs.next()?;
    let h1 = digit(first)?;
    let h2 = match hs.next() {
        Some(c) => Some(digit(c)?),
        None => None,
    };
    if hs.next().is_some() {
        return None;
    }
    if period == 2 {
        if !hour.is_ascii() {
            return None;
        }
        let h = u32::from(h1) * if h2.is_some() { 10 } else { 1 } + u32::from(h2.unwrap_or(0));
        if !(1..=12).contains(&h) {
            return None;
        }
    }
    let valid_minute = |part: &str| {
        let mut chars = part.chars();
        let Some(a) = chars.next().and_then(digit) else { return false };
        let Some(b) = chars.next().and_then(digit) else { return false };
        chars.next().is_none() && (a != 0 || b != 0 || part == "00")
    };
    if !valid_minute(minute) || second.is_some_and(|s| !valid_minute(s)) {
        return None;
    }
    Some(if zone {
        1_030_000
    } else if second.is_some() || period != 0 {
        1_040_000
    } else {
        1_050_000
    })
}

pub(crate) fn costs(s: &str, mut accept: impl FnMut(i64)) {
    if let Some(cost) = clock_cost(s) {
        accept(cost);
    }
    for (i, c) in s.char_indices() {
        if matches!(c, '-' | '–' | '—') {
            let a = s[..i].strip_suffix(' ').unwrap_or(&s[..i]);
            let rest = &s[i + c.len_utf8()..];
            let b = rest.strip_prefix(' ').unwrap_or(rest);
            if clock_cost(a).is_some() && clock_cost(b).is_some() {
                accept(1_020_000);
            }
        }
    }
}

pub(crate) fn render(s: &str, cost: i64) -> Option<String> {
    if cost != 1_020_000 {
        return clock(s).and_then(|(word, actual)| (actual == cost).then_some(word));
    }
    for (i, c) in s.char_indices() {
        if matches!(c, '-' | '–' | '—') {
            let a = s[..i].strip_suffix(' ').unwrap_or(&s[..i]);
            let rest = &s[i + c.len_utf8()..];
            let b = rest.strip_prefix(' ').unwrap_or(rest);
            if let (Some((a, _)), Some((b, _))) = (clock(a), clock(b)) {
                return Some(format!("{a}ถึง{b}"));
            }
        }
    }
    None
}
