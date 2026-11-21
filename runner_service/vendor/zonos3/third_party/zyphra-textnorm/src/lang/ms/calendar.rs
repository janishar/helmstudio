use super::lookup;
use super::number::cardinal;

fn unpad(s: &str) -> &str {
    s.strip_prefix('0').filter(|r| !r.is_empty()).unwrap_or(s)
}
fn day(s: &str) -> Option<String> {
    let t = unpad(s);
    let n: u8 = t.parse().ok()?;
    if s.len() > 3 || !(1..=31).contains(&n) {
        return None;
    }
    cardinal(t)
}
fn month(s: &str) -> Option<&'static str> {
    let t = unpad(s);
    let n: u8 = t.parse().ok()?;
    if s.len() > 3 || !(1..=12).contains(&n) {
        return None;
    }
    lookup(include_str!("../../../data/ms/months/numbers.tsv"), t)
}
fn name(s: &str) -> Option<&'static str> {
    lookup(include_str!("../../../data/ms/months/names.tsv"), s)
        .or_else(|| lookup(include_str!("../../../data/ms/months/abbr.tsv"), s.strip_suffix('.').unwrap_or(s)))
}
fn year(s: &str, four: bool) -> Option<String> {
    if !((s.len() == 4 && (s.starts_with('1') || s.starts_with('2'))) || (!four && s.len() == 2)) {
        return None;
    }
    cardinal(s)
}
pub(crate) fn date(s: &str) -> Vec<String> {
    let mut out = Vec::new();
    macro_rules! push {
        ($($arg:tt)*) => { out.push(if super::lazy::recognizing() {String::new()} else {format!($($arg)*)}) };
    }
    for sep in ['/', '-', '.'] {
        let mut parts = s.split(sep);
        if let (Some(a), Some(b), Some(c), None) = (parts.next(), parts.next(), parts.next(), parts.next()) {
            if let Some(d) = day(a) {
                if let Some(m) = month(b) {
                    if let Some(y) = year(c, false) {
                        push!("date {{ day: \"{d}\" month: \"{m}\" year: \"{y}\" }}");
                    }
                }
            }
            if sep == '-' {
                if let Some(y) = year(a, true) {
                    if let Some(m) = month(b) {
                        if let Some(d) = day(c) {
                            push!("date {{ year: \"{y}\" month: \"{m}\" day: \"{d}\" }}");
                        }
                    }
                }
            }
        }
    }
    let mut parts = s.split(' ');
    if let (Some(a), Some(b), third, None) = (parts.next(), parts.next(), parts.next(), parts.next()) {
        if let Some(d) = day(a) {
            if let Some(m) = name(b) {
                if let Some(c) = third {
                    if let Some(y) = year(c, true) {
                        push!("date {{ day: \"{d}\" month: \"{m}\" year: \"{y}\" }}");
                    }
                } else {
                    push!("date {{ day: \"{d}\" month: \"{m}\" }}");
                }
            }
        }
        if third.is_none() {
            if let Some(y) = year(b, true) {
                if let Some(m) = name(a) {
                    push!("date {{ month: \"{m}\" year: \"{y}\" }}");
                }
            }
        }
    }
    let (pre, dec) = ["tahun ", "Tahun ", "era ", "Era "]
        .iter()
        .find_map(|p| s.strip_prefix(*p).map(|r| (*p, r)))
        .unwrap_or(("", s));
    for suffix in ["-an", "an", "'s", "s"] {
        if let Some(raw) = dec.strip_suffix(suffix) {
            let n = raw.strip_prefix('\'').or_else(|| raw.strip_prefix('’')).unwrap_or(raw);
            if (n.len() == 2 && !n.starts_with('0')
                || n.len() == 4 && n == raw && (n.starts_with('1') || n.starts_with('2')))
                && n.ends_with('0')
            {
                if let Some(w) = cardinal(n) {
                    push!("date {{ year: \"{pre}{w}an\" }}");
                }
            }
        }
    }
    let (ep, tail) =
        ["tahun ", "Tahun ", "abad ke-"].iter().find_map(|p| s.strip_prefix(*p).map(|r| (*p, r))).unwrap_or(("", s));
    if let Some((n, e)) = tail.split_once(' ') {
        let meaning = match e {
            "SM" | "S.M." | "BC" | "B.C." | "BCE" | "B.C.E." => Some("sebelum Masehi"),
            "SEU" => Some("sebelum Era Umum"),
            "AD" | "A.D." | "CE" | "C.E." => Some("Masehi"),
            "EU" => Some("Era Umum"),
            "M" if !ep.is_empty() => Some("Masehi"),
            "H" if !ep.is_empty() => Some("Hijrah"),
            _ => None,
        };
        if n.len() <= 4 && !n.starts_with('0') && (ep != "abad ke-" || matches!(e, "M" | "H")) {
            if let (Some(n), Some(e)) = (cardinal(n), meaning) {
                push!("date {{ year: \"{ep}{n} {e}\" }}");
            }
        }
    }
    out
}

fn hour(s: &str) -> Option<String> {
    let t = unpad(s);
    if s.len() > 3 || t.parse::<u8>().ok()? > 24 {
        return None;
    }
    cardinal(t)
}
fn minute(s: &str) -> Option<Option<String>> {
    if s == "00" {
        return Some(None);
    }
    if s.len() != 2 {
        return None;
    }
    let t = unpad(s);
    let n = t.parse::<u8>().ok()?;
    if n == 0 || n >= 60 {
        return None;
    }
    Some(Some(cardinal(t)?))
}
fn hm(s: &str, allow_dot: bool) -> Option<(String, Option<String>)> {
    let (a, b) = s.split_once(':').or_else(|| if allow_dot { s.split_once('.') } else { None })?;
    Some((hour(a)?, minute(b)?))
}
/// Recognition mirrors the time alternatives without building hour or zone text.
fn time_recognize(s: &str) -> Vec<String> {
    let mut out = Vec::new();
    let s = if let Some((a, b)) = s.rsplit_once(' ') {
        if lookup(include_str!("../../../data/ms/time/time_zone.tsv"), b).is_some() { a } else { s }
    } else {
        s
    };
    let (s, prefixed) = if let Some(r) = s
        .strip_prefix("pukul ")
        .or_else(|| s.strip_prefix("Pukul "))
        .or_else(|| s.strip_prefix("pkl. "))
        .or_else(|| s.strip_prefix("pkl "))
        .or_else(|| s.strip_prefix("jam "))
        .or_else(|| s.strip_prefix("Jam "))
    {
        (r, true)
    } else {
        (s, false)
    };
    if prefixed && hour(s).is_some() {
        out.push(String::new());
    }
    let mut parts = s.split(':');
    if let (Some(a), Some(b), Some(c), None) = (parts.next(), parts.next(), parts.next(), parts.next()) {
        if hour(a).is_some() && minute(b).is_some() && matches!(minute(c), Some(Some(_))) {
            out.push(String::new());
        }
    }
    if hm(s, prefixed || s.ends_with(".00")).is_some() {
        out.push(String::new());
    }
    for dash in ['-', '–'] {
        if let Some((a, b)) = s.split_once(dash) {
            if hm(a.strip_suffix(' ').unwrap_or(a), true).is_some()
                && hm(b.strip_prefix(' ').unwrap_or(b), true).is_some()
            {
                out.push(String::new());
            }
        }
    }
    out
}
pub(crate) fn time(s: &str) -> Vec<String> {
    if super::lazy::recognizing() {
        return time_recognize(s);
    }
    let mut out = Vec::new();
    let (s, zone) = if let Some((a, b)) = s.rsplit_once(' ') {
        if let Some(z) = lookup(include_str!("../../../data/ms/time/time_zone.tsv"), b) {
            (a, Some(z.replace(' ', "\u{a0}")))
        } else {
            (s, None)
        }
    } else {
        (s, None)
    };
    let (s, prefix, had_prefix) = if let Some(r) = s
        .strip_prefix("pukul ")
        .or_else(|| s.strip_prefix("Pukul "))
        .or_else(|| s.strip_prefix("pkl. "))
        .or_else(|| s.strip_prefix("pkl "))
    {
        (r, None, true)
    } else if let Some(r) = s.strip_prefix("jam ") {
        (r, Some("jam"), true)
    } else if let Some(r) = s.strip_prefix("Jam ") {
        (r, Some("Jam"), true)
    } else {
        (s, None, false)
    };
    let mut bodies = Vec::new();
    if had_prefix {
        if let Some(h) = hour(s) {
            bodies.push(format!("hours: \"{h}\""));
        }
    }
    let segments: Vec<_> = s.split(':').collect();
    if segments.len() == 3 {
        if let (Some(h), Some(m), Some(Some(sec))) = (hour(segments[0]), minute(segments[1]), minute(segments[2])) {
            let m = m.map(|x| format!(" minutes: \"{x}\"")).unwrap_or_default();
            bodies.push(format!("hours: \"{h}\"{m} seconds: \"{sec}\""));
        }
    }
    if let Some((h, m)) = hm(s, had_prefix || s.ends_with(".00")) {
        let m = m.map(|x| format!(" minutes: \"{x}\"")).unwrap_or_default();
        bodies.push(format!("hours: \"{h}\"{m}"));
    }
    for dash in ['-', '–'] {
        if let Some((a, b)) = s.split_once(dash) {
            if let (Some((h1, m1)), Some((h2, m2))) =
                (hm(a.strip_suffix(' ').unwrap_or(a), true), hm(b.strip_prefix(' ').unwrap_or(b), true))
            {
                let m1 = m1.map(|v| format!(" {v} minit")).unwrap_or_default();
                let m2 = m2.map(|v| format!(" {v} minit")).unwrap_or_default();
                bodies.push(format!("hours: \"{h1}{m1} hingga pukul {h2}{m2}\""));
            }
        }
    }
    for body in bodies {
        let p = prefix.map(|v| format!("prefix: \"{v}\" ")).unwrap_or_default();
        let z = zone.as_ref().map(|v| format!(" zone: \"{v}\"")).unwrap_or_default();
        out.push(format!("time {{ {p}{body}{z} preserve_order: true }}"));
    }
    out
}
