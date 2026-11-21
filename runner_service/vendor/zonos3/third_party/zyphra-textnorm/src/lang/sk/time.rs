use super::number::positive;
fn unit(n: usize, one: &str, few: &str, many: &str) -> String {
    let v = match n {
        1 => "jedna".into(),
        2 => "dve".into(),
        _ => positive(&n.to_string()).unwrap(),
    };
    spoken!(
        "{v} {}",
        if n == 1 {
            one
        } else if n <= 4 && n > 0 {
            few
        } else {
            many
        }
    )
}
fn base(s: &str) -> Option<String> {
    let (s, zone) = for_zone(s);
    let mut parts = s.split(':');
    let hour = parts.next()?;
    let minute = parts.next()?;
    let second = parts.next();
    if parts.next().is_some()
        || hour.is_empty()
        || hour.len() > 2
        || minute.len() != 2
        || !hour.bytes().all(|b| b.is_ascii_digit())
        || !minute.bytes().all(|b| b.is_ascii_digit())
    {
        return None;
    }
    let h: usize = hour.parse().ok()?;
    let m: usize = minute.parse().ok()?;
    if h > 23 || m > 59 {
        return None;
    }
    let sec = if let Some(value) = second {
        if value.len() != 2 || !value.bytes().all(|b| b.is_ascii_digit()) {
            return None;
        }
        let sec: usize = value.parse().ok()?;
        if sec > 59 {
            return None;
        }
        Some(sec)
    } else {
        None
    };
    if !super::building() {
        return Some(String::new());
    }
    let mut out = unit(h, "hodina", "hodiny", "hodín");
    if let Some(sec) = sec {
        out.push(' ');
        out.push_str(&unit(m, "minúta", "minúty", "minút"));
        out.push(' ');
        out.push_str(&unit(sec, "sekunda", "sekundy", "sekúnd"));
    } else if m != 0 {
        out.push(' ');
        out.push_str(&unit(m, "minúta", "minúty", "minút"));
    }
    if let Some(z) = zone {
        out.push(' ');
        out.push_str(z);
    }
    Some(out)
}
fn for_zone(s: &str) -> (&str, Option<&str>) {
    for z in ["UTC", "GMT", "CET", "CEST", "EET"] {
        if let Some(t) = s.strip_suffix(z).and_then(|x| x.strip_suffix(' ')) {
            return (t, Some(z));
        }
    }
    (s, None)
}
pub(super) fn time(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    time_each(s, |value, weight| out.push((value, weight)));
    out
}
pub(super) fn time_each(s: &str, mut emit: impl FnMut(String, f64)) {
    if let Some(t) = base(s) {
        emit(t, 0.08);
    }
    for (i, c) in s.char_indices() {
        if "-–".contains(c) {
            let a = s[..i].strip_suffix(' ').unwrap_or(&s[..i]);
            let b = s[i + c.len_utf8()..].strip_prefix(' ').unwrap_or(&s[i + c.len_utf8()..]);
            if let (Some(x), Some(y)) = (base(a), base(b)) {
                emit(spoken!("{x} až {y}"), 0.05);
            }
        }
    }
}
