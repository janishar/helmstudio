use super::graph_utils::{CARDINALS, cardinal, digit};

const MONTHS: [&str; 12] =
    ["ଜାନୁଆରୀ", "ଫେବୃଆରୀ", "ମାର୍ଚ୍ଚ", "ଅପ୍ରେଲ", "ମଇ", "ଜୁନ", "ଜୁଲାଇ", "ଅଗଷ୍ଟ", "ସେପ୍ଟେମ୍ବର", "ଅକ୍ଟୋବର", "ନଭେମ୍ବର", "ଡିସେମ୍ବର"];
fn numeric(s: &str, max: usize, zero: bool) -> Option<usize> {
    let mut n = 0usize;
    let mut len = 0;
    for c in s.chars() {
        n = n.checked_mul(10)?.checked_add(digit(c)? as usize)?;
        len += 1;
    }
    if len == 0
        || len > 2
        || n > max
        || (!zero && n == 0)
        || (len == 2 && n < 10 && !s.chars().next().is_some_and(|c| digit(c) == Some(0)))
    {
        return None;
    }
    Some(n)
}
fn year(s: &str) -> Option<(String, i64)> {
    let ds: Vec<u8> = s.chars().map(digit).collect::<Option<_>>()?;
    if ds.len() == 4 && ds[0] == 1 && ds[1] == 9 {
        let v = ds[2] as usize * 10 + ds[3] as usize;
        return Some((if v == 0 { "ଉନିଶ ଶହ".into() } else { format!("ଉନିଶ ଶହ {}", CARDINALS[v]) }, -10_000));
    }
    Some((cardinal(s)?, 0))
}
fn split_range(s: &str) -> Vec<(&str, &str)> {
    let mut r = Vec::new();
    for (i, c) in s.char_indices() {
        if matches!(c, '-' | '–' | '—') {
            r.push((
                s[..i].strip_suffix(' ').unwrap_or(&s[..i]),
                s[i + c.len_utf8()..].strip_prefix(' ').unwrap_or(&s[i + c.len_utf8()..]),
            ));
        }
    }
    if let Some((a, b)) = s.split_once(" to ") {
        r.push((a.strip_suffix(' ').unwrap_or(a), b.strip_prefix(' ').unwrap_or(b)));
    }
    r
}

pub enum Date {
    Name(String, i64),
    Fields(String, String, String),
}
pub fn date(s: &str) -> Vec<Date> {
    let mut out = Vec::new();
    let p: Vec<_> = s.split(['/', '-', '.']).collect();
    if p.len() == 3 {
        let month_is_one_script =
            p[1].chars().all(|c| c.is_ascii_digit()) || p[1].chars().all(|c| ('୦'..='୯').contains(&c));
        if month_is_one_script {
            if let (Some(d), Some(m), Some((y, w))) = (numeric(p[0], 31, false), numeric(p[1], 12, false), year(p[2])) {
                if [2, 4].contains(&p[2].chars().count()) {
                    out.push(Date::Name(format!("{} {} {y}", CARDINALS[d], MONTHS[m - 1]), w));
                }
            }
        }
        if p[0].chars().count() == 4 && p[1].chars().count() == 2 && p[2].chars().count() == 2 {
            if let (Some(y), Some(m), Some(d)) = (cardinal(p[0]), numeric(p[1], 12, false), numeric(p[2], 31, false)) {
                out.push(Date::Fields(y, MONTHS[m - 1].to_string(), CARDINALS[d].to_string()));
            }
        }
    }
    let p: Vec<_> = s.split(' ').collect();
    if (p.len() == 2 || p.len() == 3) && MONTHS.contains(&p[1]) {
        if let Some(d) = numeric(p[0], 31, false) {
            if p.len() == 2 {
                out.push(Date::Name(format!("{} {}", CARDINALS[d], p[1]), 0));
            } else if let Some(y) = cardinal(p[2]) {
                out.push(Date::Name(format!("{} {} {y}", CARDINALS[d], p[1]), 0));
            }
        }
    }
    for (marker, label) in [
        ("BC", "ଖ୍ରୀଷ୍ଟପୂର୍ବ"),
        ("B.C.", "ଖ୍ରୀଷ୍ଟପୂର୍ବ"),
        ("BCE", "ଖ୍ରୀଷ୍ଟପୂର୍ବ"),
        ("B.C.E.", "ଖ୍ରୀଷ୍ଟପୂର୍ବ"),
        ("ଖ୍ରୀ.ପୂ.", "ଖ୍ରୀଷ୍ଟପୂର୍ବ"),
        ("ଖ୍ରୀଷ୍ଟପୂର୍ବ", "ଖ୍ରୀଷ୍ଟପୂର୍ବ"),
        ("AD", "ଖ୍ରୀଷ୍ଟାବ୍ଦ"),
        ("A.D.", "ଖ୍ରୀଷ୍ଟାବ୍ଦ"),
        ("CE", "ଖ୍ରୀଷ୍ଟାବ୍ଦ"),
        ("C.E.", "ଖ୍ରୀଷ୍ଟାବ୍ଦ"),
        ("ଖ୍ରୀଷ୍ଟାବ୍ଦ", "ଖ୍ରୀଷ୍ଟାବ୍ଦ"),
    ] {
        if let Some(a) = s.strip_suffix(marker) {
            if let Some(y) = cardinal(a.strip_suffix(' ').unwrap_or(a)) {
                out.push(Date::Name(format!("{label} {y}"), 0));
            }
        }
    }
    for suffix in ["s", "'s", " ଦଶକ", " ର ଦଶକ"] {
        if let Some(a) = s.strip_suffix(suffix) {
            let a = a.strip_prefix('\'').unwrap_or(a);
            let ds: Option<Vec<_>> = a.chars().map(digit).collect();
            if let Some(ds) = ds {
                if [2, 4].contains(&ds.len()) && ds.last() == Some(&0) {
                    if let Some((w, c)) = year(a) {
                        out.push(Date::Name(format!("{w} ଦଶକ"), c));
                    }
                }
            }
        }
    }
    for (a, b) in split_range(s) {
        if a.chars().count() == 4 && b.chars().count() == 4 {
            if let (Some((x, c)), Some((y, d))) = (year(a), year(b)) {
                out.push(Date::Name(format!("{x} ରୁ {y}"), c + d));
            }
        }
    }
    out
}
fn accepts_clock(s: &str, allow_seconds: bool) -> bool {
    let s = if let Some((clock, zone)) = s.split_once(' ') {
        if !["UTC", "GMT", "IST", "EST", "PST", "CST"].contains(&zone) {
            return false;
        }
        clock
    } else {
        s
    };
    let mut parts = s.split(':');
    let Some(hour) = parts.next() else { return false };
    let Some(minute) = parts.next() else { return false };
    if numeric(hour, 23, true).is_none() {
        return false;
    }
    let Some(m) = numeric(minute, 59, true) else { return false };
    if m == 0 && minute.chars().count() != 2 {
        return false;
    }
    if let Some(second) = parts.next() {
        if !allow_seconds {
            return false;
        }
        let Some(sec) = numeric(second, 59, true) else { return false };
        if sec == 0 && second.chars().count() != 2 {
            return false;
        }
    }
    parts.next().is_none()
}

pub fn accepts_time(s: &str) -> bool {
    if accepts_clock(s, true) {
        return true;
    }
    for (i, c) in s.char_indices() {
        if matches!(c, '-' | '–' | '—') {
            let a = &s[..i];
            let b = &s[i + c.len_utf8()..];
            if accepts_clock(a.strip_suffix(' ').unwrap_or(a), false)
                && accepts_clock(b.strip_prefix(' ').unwrap_or(b), false)
            {
                return true;
            }
        }
    }
    if let Some((a, b)) = s.split_once(" to ") {
        return accepts_clock(a.strip_suffix(' ').unwrap_or(a), false)
            && accepts_clock(b.strip_prefix(' ').unwrap_or(b), false);
    }
    false
}

fn write_clock(out: &mut String, s: &str) {
    let (clock, zone) = s.split_once(' ').map_or((s, None), |(a, b)| (a, Some(b)));
    let mut parts = clock.split(':');
    let h = numeric(parts.next().expect("recognized hour"), 23, true).expect("recognized hour");
    let m = numeric(parts.next().expect("recognized minute"), 59, true).expect("recognized minute");
    out.push_str(CARDINALS[h]);
    out.push_str("ଟା");
    if let Some(second) = parts.next() {
        let sec = numeric(second, 59, true).expect("recognized second");
        out.push('\u{a0}');
        if m != 0 {
            out.push_str(CARDINALS[m]);
            out.push_str("\u{a0}ମିନିଟ୍");
        }
        out.push('\u{a0}');
        if sec != 0 {
            out.push_str(CARDINALS[sec]);
            out.push_str("\u{a0}ସେକେଣ୍ଡ");
        }
    } else if m != 0 {
        out.push('\u{a0}');
        out.push_str(CARDINALS[m]);
    }
    if let Some(zone) = zone {
        let name = match zone {
            "UTC" => "ୟୁ\u{a0}ଟି\u{a0}ସି",
            "GMT" => "ଜି\u{a0}ଏମ୍\u{a0}ଟି",
            "IST" => "ଆଇ\u{a0}ଏସ୍\u{a0}ଟି",
            "EST" => "ଇ\u{a0}ଏସ୍\u{a0}ଟି",
            "PST" => "ପି\u{a0}ଏସ୍\u{a0}ଟି",
            "CST" => "ସି\u{a0}ଏସ୍\u{a0}ଟି",
            _ => return,
        };
        out.push('\u{a0}');
        out.push_str(name);
    }
}

/// Materialize the accepted clock branch without intermediate spoken strings.
pub fn write_time(s: &str, out: &mut String) {
    out.push_str("name: \"");
    if accepts_clock(s, true) {
        write_clock(out, s);
    } else {
        let mut range = None;
        for (i, c) in s.char_indices() {
            if matches!(c, '-' | '–' | '—') {
                let a = &s[..i];
                let b = &s[i + c.len_utf8()..];
                let a = a.strip_suffix(' ').unwrap_or(a);
                let b = b.strip_prefix(' ').unwrap_or(b);
                if accepts_clock(a, false) && accepts_clock(b, false) {
                    range = Some((a, b));
                    break;
                }
            }
        }
        if range.is_none() {
            if let Some((a, b)) = s.split_once(" to ") {
                range = Some((a.strip_suffix(' ').unwrap_or(a), b.strip_prefix(' ').unwrap_or(b)));
            }
        }
        if let Some((a, b)) = range {
            write_clock(out, a);
            out.push_str("\u{a0}ରୁ\u{a0}");
            write_clock(out, b);
        }
    }
    out.push('"');
}
