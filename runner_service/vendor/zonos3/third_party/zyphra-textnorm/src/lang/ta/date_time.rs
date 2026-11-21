//! Tamil date and clock classifiers.
use super::number::{cardinal, digit};
use std::borrow::Cow;
use std::collections::HashMap;
use std::sync::LazyLock;

type Table = HashMap<&'static str, &'static str>;
fn table(data: &'static str) -> Table {
    let mut index = HashMap::new();
    for (key, value) in data.lines().filter_map(|line| line.split_once('\t')) {
        index.entry(key).or_insert(value);
    }
    index
}
static MONTHS: LazyLock<Table> = LazyLock::new(|| table(include_str!("../../../data/ta/date/months.tsv")));
static MONTH_NAMES: LazyLock<Table> = LazyLock::new(|| table(include_str!("../../../data/ta/date/month_names.tsv")));
static ERAS: LazyLock<Table> = LazyLock::new(|| table(include_str!("../../../data/ta/date/year_suffix.tsv")));
static HOURS: LazyLock<Table> = LazyLock::new(|| table(include_str!("../../../data/ta/time/hours.tsv")));
static MINUTES: LazyLock<Table> = LazyLock::new(|| table(include_str!("../../../data/ta/time/minutes.tsv")));
static SECONDS: LazyLock<Table> = LazyLock::new(|| table(include_str!("../../../data/ta/time/seconds.tsv")));
fn ascii(s: &str) -> Option<Cow<'_, str>> {
    if s.is_ascii() {
        return s.bytes().all(|b| b.is_ascii_digit()).then_some(Cow::Borrowed(s));
    }
    s.chars().map(|c| digit(c).map(|n| (b'0' + n) as char)).collect::<Option<String>>().map(Cow::Owned)
}
fn number(s: &str) -> Option<String> {
    cardinal(s).and_then(|(v, leading)| (!leading).then_some(v))
}
fn fld(k: &str, v: &str) -> String {
    format!("{k}: \"{v}\"")
}
fn push_field(out: &mut String, key: &str, value: &str) {
    out.push_str(key);
    out.push_str(": \"");
    out.push_str(value);
    out.push('"');
}
fn day(s: &str) -> Option<String> {
    let d = ascii(s)?;
    if d.len() > 2 {
        return None;
    }
    let n: u8 = d.parse().ok()?;
    if !(1..=31).contains(&n) {
        return None;
    }
    number(&n.to_string())
}
fn year(s: &str, four: bool) -> Option<String> {
    let d = ascii(s)?;
    if d.is_empty()
        || d.len() > 4
        || d.starts_with('0')
        || (four && (d.len() != 4 || !matches!(d.chars().next(), Some('1' | '2'))))
    {
        return None;
    }
    number(&d)
}
fn month(s: &str) -> Option<&'static str> {
    MONTHS.get(ascii(s)?.as_ref()).copied()
}
fn named_month(s: &str) -> Option<&'static str> {
    MONTH_NAMES.get(s).copied()
}
fn era(s: &str) -> Option<&'static str> {
    ERAS.get(s).copied()
}

fn numeric_value(s: &str, max_len: usize) -> Option<(u32, u8, usize)> {
    let mut n = 0u32;
    let mut first = 0u8;
    let mut len = 0usize;
    for c in s.chars() {
        let d = digit(c)?;
        if len == 0 {
            first = d;
        }
        len += 1;
        if len > max_len {
            return None;
        }
        n = n * 10 + d as u32;
    }
    (len > 0).then_some((n, first, len))
}

fn valid_day(s: &str) -> bool {
    numeric_value(s, 2).is_some_and(|(n, _, _)| (1..=31).contains(&n))
}
fn valid_month(s: &str) -> bool {
    numeric_value(s, 2).is_some_and(|(n, _, _)| (1..=12).contains(&n))
}
fn valid_year(s: &str, four: bool) -> bool {
    numeric_value(s, 4).is_some_and(|(_, first, len)| first != 0 && (!four || (len == 4 && matches!(first, 1 | 2))))
}

/// Emit date path costs without spelling any of their numeric fields.
pub fn date_costs(s: &str, mut emit: impl FnMut(f64)) {
    for sep in ['/', '-', '.'] {
        let Some((first, rest)) = s.split_once(sep) else { continue };
        let Some((middle, last)) = rest.split_once(sep) else { continue };
        if last.contains(sep) {
            continue;
        }
        if valid_day(first) && valid_month(middle) && valid_year(last, true) {
            emit(0.0);
        }
        if sep == '-' && valid_year(first, true) && valid_month(middle) && valid_day(last) {
            emit(0.0);
        }
    }
    for sep in [" - ", " – ", "-", "–"] {
        if let Some((a, b)) = s.split_once(sep) {
            if valid_year(a, true) && valid_year(b, true) {
                emit(-0.05);
            }
        }
    }
    for suffix in ["'s", "s", "-கள்", "கள்"] {
        if let Some(year) = s.strip_suffix(suffix) {
            if valid_year(year, true) && year.chars().last().and_then(digit) == Some(0) {
                emit(-0.05);
            }
        }
    }
    let mut words = [""; 4];
    let mut count = 0;
    for word in s.split(' ') {
        if count == words.len() {
            return;
        }
        words[count] = word;
        count += 1;
    }
    let p = &words[..count];
    if p.iter().any(|v| v.is_empty()) || !(2..=4).contains(&p.len()) {
        return;
    }
    let month = named_month(p[1].strip_suffix(',').unwrap_or(p[1]));
    if month.is_some() && valid_day(p[0]) {
        if p.len() == 2 && !p[1].ends_with(',') {
            emit(0.0);
        }
        if p.len() >= 3 && valid_year(p[2], false) {
            if p.len() == 4 && era(p[3]).is_none() {
                return;
            }
            emit(0.0);
        }
    }
    if p.len() <= 3 && named_month(p[0].strip_suffix(',').unwrap_or(p[0])).is_some() && valid_year(p[1], false) {
        if p.len() == 3 && era(p[2]).is_none() {
            return;
        }
        emit(0.0);
    }
    if p.len() == 2 && era(p[1]).is_some() && valid_year(p[0], false) {
        emit(0.0);
    }
}

pub fn date(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    for sep in ['/', '-', '.'] {
        let Some((first, rest)) = s.split_once(sep) else { continue };
        let Some((middle, last)) = rest.split_once(sep) else { continue };
        if last.contains(sep) {
            continue;
        }
        if let (Some(d), Some(m), Some(y)) = (day(first), month(middle), year(last, true)) {
            out.push((format!("{} {} {}", fld("day", &d), fld("month", m), fld("year", &y)), 0.0));
        }
        if sep == '-' {
            if let (Some(y), Some(m), Some(d)) = (year(first, true), month(middle), day(last)) {
                out.push((format!("{} {} {}", fld("year", &y), fld("month", m), fld("day", &d)), 0.0));
            }
        }
    }
    for sep in [" - ", " – ", "-", "–"] {
        if let Some((a, b)) = s.split_once(sep) {
            if let (Some(a), Some(b)) = (year(a, true), year(b, true)) {
                out.push((fld("year", &format!("{a} முதல் {b} வரை")), -0.05));
            }
        }
    }
    for suffix in ["'s", "s", "-கள்", "கள்"] {
        if let Some(y) = s.strip_suffix(suffix) {
            if let Some(mut v) = year(y, true) {
                if ascii(y).is_some_and(|a| a.ends_with('0')) {
                    if let Some(stem) = v.strip_suffix('ு') {
                        v = format!("{stem}ுகளின் காலகட்டம்");
                    }
                    out.push((fld("year", &v), -0.05));
                }
            }
        }
    }
    let mut parts = [""; 4];
    let mut len = 0;
    for word in s.split(' ') {
        if len == parts.len() {
            return out;
        }
        parts[len] = word;
        len += 1;
    }
    let p = &parts[..len];
    if p.iter().all(|v| !v.is_empty()) {
        if p.len() >= 2 && p.len() <= 4 {
            if let Some((d, m)) =
                named_month(p[1].strip_suffix(',').unwrap_or(p[1])).and_then(|m| day(p[0]).map(|d| (d, m)))
            {
                if p.len() == 2 && !p[1].ends_with(',') {
                    out.push((format!("{} {}", fld("day", &d), fld("month", m)), 0.0));
                }
                if p.len() >= 3 {
                    if let Some(y) = year(p[2], false) {
                        let mut v = format!("{} {} {}", fld("day", &d), fld("month", m), fld("year", &y));
                        if p.len() == 4 {
                            if let Some(e) = era(p[3]) {
                                v.push(' ');
                                push_field(&mut v, "era", e);
                            } else {
                                return out;
                            }
                        }
                        out.push((v, 0.0));
                    }
                }
            }
            if p.len() <= 3 {
                if let (Some(m), Some(y)) = (named_month(p[0].strip_suffix(',').unwrap_or(p[0])), year(p[1], false)) {
                    let mut v = format!("{} {}", fld("month", m), fld("year", &y));
                    if p.len() == 3 {
                        if let Some(e) = era(p[2]) {
                            v.push(' ');
                            push_field(&mut v, "era", e);
                        } else {
                            return out;
                        }
                    }
                    out.push((v, 0.0));
                }
            }
            if p.len() == 2 {
                if let Some((y, e)) = era(p[1]).and_then(|e| year(p[0], false).map(|y| (y, e))) {
                    out.push((format!("{} {}", fld("year", &y), fld("era", e)), 0.0));
                }
            }
        }
    }
    out
}

/// Validate the clock paths and their weights without rendering field values.
/// The first flag denotes the equal-cost hour-only interpretation of `:00`.
pub fn recognize_clock(s: &str) -> Option<(bool, f64)> {
    let mut body = s;
    for zone in [
        "UTC", "GMT", "IST", "EST", "EDT", "CST", "CDT", "MST", "MDT", "PST", "PDT", "CET", "CEST", "BST", "JST", "KST",
    ] {
        if let Some(before) = body.strip_suffix(zone).and_then(|v| v.strip_suffix(' ')) {
            body = before;
            break;
        }
    }
    let mut period = false;
    for marker in ["a.m.", "A.M.", "p.m.", "P.M.", "am", "AM", "Am", "pm", "PM", "Pm"] {
        if let Some(before) = body.strip_suffix(marker) {
            period = true;
            body = before.strip_suffix(' ').unwrap_or(before);
            break;
        }
    }
    let mut parts = body.split(':');
    let hour = parts.next()?;
    let minute = parts.next();
    let second = parts.next();
    if parts.next().is_some() {
        return None;
    }
    let parse = |part: &str, max: u8| -> Option<u8> {
        let mut value = 0u8;
        let mut len = 0;
        for c in part.chars() {
            value = value.checked_mul(10)?.checked_add(digit(c)?)?;
            len += 1;
            if len > 2 {
                return None;
            }
        }
        (len > 0 && value <= max).then_some(value)
    };
    let h = parse(hour, 24)?;
    if period && (second.is_some() || h == 0 || h > 12 || !hour.is_ascii()) {
        return None;
    }
    if let Some(m) = minute {
        parse(m, 59)?;
    }
    if let Some(sec) = second {
        parse(sec, 59)?;
    }
    if minute.is_none() && !period {
        return None;
    }
    let omit = second.is_none()
        && minute.is_some_and(|m| {
            (!period || m == "00") && m.chars().count() == 2 && m.chars().all(|c| digit(c) == Some(0))
        });
    let weight = if period {
        -0.1
    } else if second.is_some() {
        0.0
    } else {
        0.3
    };
    Some((omit, weight))
}

/// Render the first equal-cost clock path. Recognition retains both paths for
/// `:00`, but the lattice selects the hour-only path first.
pub fn clock(s: &str, wrapped: bool) -> Option<(String, f64)> {
    let (omit_minutes, weight) = recognize_clock(s)?;
    let zones = [
        ("UTC", "யு டி சி"),
        ("GMT", "ஜி எம் டி"),
        ("IST", "ஐ எஸ் டி"),
        ("EST", "இ எஸ் டி"),
        ("EDT", "இ டி டி"),
        ("CST", "சி எஸ் டி"),
        ("CDT", "சி டி டி"),
        ("MST", "எம் எஸ் டி"),
        ("MDT", "எம் டி டி"),
        ("PST", "பி எஸ் டி"),
        ("PDT", "பி டி டி"),
        ("CET", "சி இ டி"),
        ("CEST", "சி இ எஸ் டி"),
        ("BST", "பி எஸ் டி"),
        ("JST", "ஜே எஸ் டி"),
        ("KST", "கே எஸ் டி"),
    ];
    let (body, zone) = zones
        .iter()
        .find_map(|(key, value)| s.strip_suffix(*key).and_then(|v| v.strip_suffix(' ')).map(|v| (v, Some(*value))))
        .unwrap_or((s, None));
    let mut base = body;
    let mut period = None;
    for marker in ["a.m.", "A.M.", "p.m.", "P.M.", "am", "AM", "Am", "pm", "PM", "Pm"] {
        if let Some(before) = body.strip_suffix(marker) {
            period = Some(matches!(marker.chars().next(), Some('p' | 'P')));
            base = before.strip_suffix(' ').unwrap_or(before);
            break;
        }
    }
    let mut parts = base.split(':');
    let hour = ascii(parts.next()?)?;
    let hour_word = *HOURS.get(hour.as_ref())?;
    let mut fields = String::with_capacity(96);
    if wrapped {
        fields.push_str("time { ");
    }
    if let Some(pm) = period {
        let n = hour.parse::<u8>().ok()?;
        let label = if pm {
            if n == 12 || n <= 5 {
                "மதியம்"
            } else if n <= 7 {
                "மாலை"
            } else {
                "இரவு"
            }
        } else if (6..=11).contains(&n) {
            "காலை"
        } else {
            "இரவு"
        };
        push_field(&mut fields, "day_period", label);
        fields.push(' ');
    }
    push_field(&mut fields, "hours", hour_word);
    if !omit_minutes {
        if let Some(minute) = parts.next() {
            let minute = ascii(minute)?;
            fields.push(' ');
            push_field(&mut fields, "minutes", MINUTES.get(minute.as_ref())?);
        }
        if let Some(second) = parts.next() {
            let second = ascii(second)?;
            fields.push(' ');
            push_field(&mut fields, "seconds", SECONDS.get(second.as_ref())?);
        }
    }
    if let Some(zone) = zone {
        fields.push(' ');
        push_field(&mut fields, "zone", zone);
    }
    if wrapped {
        fields.push_str(" }");
    }
    Some((fields, weight))
}

/// Verbalize an unmarked clock inside the composite range branch.
pub fn spoken_clock(s: &str) -> Option<(String, f64)> {
    let mut parts = s.split(':');
    let hours = parts.next()?;
    let minutes = parts.next()?;
    let seconds = parts.next();
    if parts.next().is_some()
        || !(1..=2).contains(&hours.chars().count())
        || minutes.chars().count() != 2
        || seconds.is_some_and(|s| s.chars().count() != 2)
    {
        return None;
    }
    let (tag, mut weight) = clock(s, false)?;
    let get = |key: &str| tag.split_once(key).and_then(|(_, rest)| rest.split_once('"').map(|(v, _)| v));
    let h = get("hours: \"")?;
    let mut out = if h == "ஒன்று" {
        weight -= 0.01;
        "ஒரு மணி".to_owned()
    } else {
        format!("{h} மணி")
    };
    if let Some(m) = get("minutes: \"") {
        out.push(' ');
        if m == "ஒன்று" {
            weight -= 0.01;
            out.push_str("ஒரு நிமிடம்");
        } else {
            out.push_str(m);
            out.push_str(" நிமிடங்கள்");
        }
    }
    if let Some(s) = get("seconds: \"") {
        out.push(' ');
        if s == "ஒன்று" {
            weight -= 0.01;
            out.push_str("ஒரு விநாடி");
        } else {
            out.push_str(s);
            out.push_str(" விநாடிகள்");
        }
    }
    Some((out, weight))
}
