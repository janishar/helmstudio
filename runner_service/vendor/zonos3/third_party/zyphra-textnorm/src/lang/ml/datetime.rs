//! Date and clock taggers.
use super::{number, numeric::Reading};
use crate::text::Table;

crate::tsv!(MONTHS, "ml/date/months.tsv");
crate::tsv!(MONTH_NAMES, "ml/date/month_names.tsv");
crate::tsv!(ERAS, "ml/date/year_suffix.tsv");
crate::tsv!(HOURS, "ml/time/hours.tsv");
crate::tsv!(MINUTES, "ml/time/minutes.tsv");
crate::tsv!(SECONDS, "ml/time/seconds.tsv");

fn lookup<'a>(table: &'a Table, s: &str) -> Option<&'a str> {
    table.rows().iter().find(|r| r.key == s).map(|r| r.value.as_str())
}
fn digit_lookup<'a>(table: &'a Table, s: &str) -> Option<&'a str> {
    lookup(table, &number::ascii_digits(s)?)
}
fn day(s: &str) -> Option<String> {
    let a = number::ascii_digits(s)?;
    if a.len() > 2 || a.is_empty() {
        return None;
    }
    let n: u32 = a.parse().ok()?;
    if !(1..=31).contains(&n) {
        return None;
    }
    number::cardinal(&n.to_string())
}
fn year(s: &str, four: bool) -> Option<String> {
    let a = number::ascii_digits(s)?;
    if a.is_empty()
        || a.len() > 4
        || a.starts_with('0')
        || (four && (a.len() != 4 || !(a.starts_with('1') || a.starts_with('2'))))
    {
        return None;
    }
    number::cardinal(&a)
}
fn name(s: &str) -> Option<&str> {
    lookup(&MONTH_NAMES, s.strip_suffix('.').unwrap_or(s))
}
fn emit(out: &mut Vec<Reading>, body: String, delta: i64) {
    out.push((format!("date {{ {body} }}"), 1_080_000 + delta, 5));
}
fn fld(k: &str, v: &str) -> String {
    format!("{k}: \"{v}\"")
}

pub fn date(s: &str, out: &mut Vec<Reading>) {
    for sep in ['/', '-', '.'] {
        if !s.contains(sep) {
            continue;
        }
        let parts: Vec<&str> = s.split(sep).collect();
        if parts.len() == 3 {
            if let Some(m) = digit_lookup(&MONTHS, parts[1])
                && let Some(d) = day(parts[0])
                && let Some(y) = year(parts[2], true)
            {
                emit(out, format!("{} {} {}", fld("day", &d), fld("month", m), fld("year", &y)), 0);
            }
            if sep == '-' {
                if let Some(m) = digit_lookup(&MONTHS, parts[1])
                    && let Some(y) = year(parts[0], true)
                    && let Some(d) = day(parts[2])
                {
                    emit(out, format!("{} {} {}", fld("year", &y), fld("month", m), fld("day", &d)), 0);
                }
            }
        }
    }
    for sep in [" - ", " – ", "-", "–"] {
        if let Some((a, b)) = s.split_once(sep) {
            if let (Some(a), Some(b)) = (year(a, true), year(b, true)) {
                emit(out, fld("year", &format!("{a} മുതൽ {b} വരെ")), -50_000);
            }
        }
    }
    for suffix in ["-കൾ", "കൾ", "s"] {
        if let Some(raw) = s.strip_suffix(suffix) {
            if number::ascii_digits(raw).is_some_and(|n| n.ends_with('0')) {
                if let Some(y) = year(raw, true) {
                    emit(out, fld("year", &format!("{y}കളിലെ ദശകം")), 0);
                }
            }
        }
    }
    if !s.contains(' ') {
        return;
    }
    let parts: Vec<&str> = s.split(' ').collect();
    if parts.iter().any(|p| p.is_empty()) {
        return;
    }
    if parts.len() >= 2 {
        if let Some(m) = name(parts[1].strip_suffix(',').unwrap_or(parts[1]))
            && let Some(d) = day(parts[0])
        {
            if parts.len() == 2 && !parts[1].ends_with(',') {
                emit(out, format!("{} {}", fld("day", &d), fld("month", m)), 0);
            }
            if parts.len() == 3 || parts.len() == 4 {
                if let Some(y) = year(parts[2], false) {
                    let mut body = format!("{} {} {}", fld("day", &d), fld("month", m), fld("year", &y));
                    if parts.len() == 4 {
                        if let Some(e) = lookup(&ERAS, parts[3]) {
                            body.push(' ');
                            body.push_str(&fld("era", e));
                        } else {
                            return;
                        }
                    }
                    emit(out, body, 0);
                }
            }
        }
        if parts.len() == 2 || parts.len() == 3 {
            if let Some(m) = name(parts[0].strip_suffix(',').unwrap_or(parts[0]))
                && let Some(y) = year(parts[1], false)
            {
                let mut body = format!("{} {}", fld("month", m), fld("year", &y));
                if parts.len() == 3 {
                    if let Some(e) = lookup(&ERAS, parts[2]) {
                        body.push(' ');
                        body.push_str(&fld("era", e));
                    } else {
                        return;
                    }
                }
                emit(out, body, 0);
            }
        }
        if parts.len() == 2 {
            if let Some(e) = lookup(&ERAS, parts[1])
                && let Some(y) = year(parts[0], false)
            {
                emit(out, format!("{} {}", fld("year", &y), fld("era", e)), 0);
            }
        }
    }
}

fn zone(s: &str) -> Option<&'static str> {
    Some(match s {
        "UTC" => "യു ടി സി",
        "GMT" => "ജി എം ടി",
        "IST" => "ഐ എസ് ടി",
        "EST" => "ഇ എസ് ടി",
        "EDT" => "ഇ ഡി ടി",
        "CST" => "സി എസ് ടി",
        "CDT" => "സി ഡി ടി",
        "PST" => "പി എസ് ടി",
        "PDT" => "പി ഡി ടി",
        "CET" => "സി ഇ ടി",
        "BST" => "ബി എസ് ടി",
        _ => return None,
    })
}

pub fn time(s: &str, out: &mut Vec<Reading>) {
    let (body, z) = if let Some((a, b)) = s.rsplit_once(' ') {
        if let Some(z) = zone(b) { (a, Some(z)) } else { (s, None) }
    } else {
        (s, None)
    };
    let mut clock = body;
    let mut period = None;
    for marker in ["a.m.", "A.M.", "p.m.", "P.M.", "am", "AM", "Am", "pm", "PM", "Pm"] {
        if let Some(x) = body.strip_suffix(marker) {
            clock = x.strip_suffix(' ').unwrap_or(x);
            let hour = clock.split(':').next().unwrap_or("");
            if !hour.is_ascii() {
                return;
            }
            let Ok(h) = hour.parse::<u8>() else { return };
            if !(1..=12).contains(&h) || hour.len() > 2 {
                return;
            }
            let pm = marker.starts_with('p') || marker.starts_with('P');
            period = Some(if !pm {
                if (6..=11).contains(&h) { "രാവിലെ" } else { "രാത്രി" }
            } else if h == 12 || h <= 3 {
                "ഉച്ചയ്ക്ക്"
            } else if h <= 7 {
                "വൈകുന്നേരം"
            } else {
                "രാത്രി"
            });
            break;
        }
    }
    let p: Vec<&str> = clock.split(':').collect();
    if p.is_empty() || p.len() > 3 || (period.is_none() && p.len() < 2) || (period.is_some() && p.len() > 2) {
        return;
    }
    let Some(h) = digit_lookup(&HOURS, p[0]) else { return };
    let mut fields = String::new();
    if let Some(period) = period {
        fields.push_str(&fld("day_period", period));
        fields.push(' ');
    }
    fields.push_str(&fld("hours", h));
    let mut delta = if period.is_some() { -100_000 } else { 0 };
    if p.len() >= 2 {
        if number::ascii_digits(p[1]).is_none_or(|x| x.len() != 2) {
            return;
        }
        let Some(m) = digit_lookup(&MINUTES, p[1]) else { return };
        if p.len() == 2 && number::ascii_digits(p[1]).as_deref() == Some("00") {
            let mut short = fields.clone();
            if let Some(z) = z {
                short.push(' ');
                short.push_str(&fld("zone", z));
            }
            out.push((format!("time {{ {short} }}"), 1_020_000 + if period.is_some() { -100_000 } else { 200_000 }, 6));
        }
        fields.push(' ');
        fields.push_str(&fld("minutes", m));
        if p.len() == 2 && period.is_none() {
            delta += 300_000;
        }
    }
    if p.len() == 3 {
        let Some(sec) = digit_lookup(&SECONDS, p[2]) else { return };
        fields.push(' ');
        fields.push_str(&fld("seconds", sec));
    }
    if let Some(z) = z {
        fields.push(' ');
        fields.push_str(&fld("zone", z));
    }
    out.push((format!("time {{ {fields} }}"), 1_020_000 + delta, 6));
}
