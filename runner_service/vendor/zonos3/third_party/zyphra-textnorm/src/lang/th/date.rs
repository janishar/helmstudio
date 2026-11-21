use super::number::{cardinal, digit};

const MONTHS: [(&str, &str); 12] = [
    ("มกราคม", "ม.ค"),
    ("กุมภาพันธ์", "ก.พ"),
    ("มีนาคม", "มี.ค"),
    ("เมษายน", "เม.ย"),
    ("พฤษภาคม", "พ.ค"),
    ("มิถุนายน", "มิ.ย"),
    ("กรกฎาคม", "ก.ค"),
    ("สิงหาคม", "ส.ค"),
    ("กันยายน", "ก.ย"),
    ("ตุลาคม", "ต.ค"),
    ("พฤศจิกายน", "พ.ย"),
    ("ธันวาคม", "ธ.ค"),
];

pub(crate) fn thai_suffixes() -> impl Iterator<Item = &'static str> {
    MONTHS.iter().flat_map(|&(name, abbr)| [name, abbr]).chain(["ปีก่อนคริสตกาล"])
}

/// Called incrementally only when the current character can finish a month.
pub(crate) fn ends_month(s: &str, last: char) -> bool {
    static INDEX: std::sync::LazyLock<Vec<(char, &'static str)>> = std::sync::LazyLock::new(|| {
        let mut index = Vec::new();
        for (name, abbr) in MONTHS {
            for key in [name, abbr] {
                index.push((key.chars().next_back().unwrap(), key));
            }
        }
        index.sort_unstable_by_key(|row| row.0);
        index
    });
    let i = INDEX.partition_point(|row| row.0 < last);
    INDEX[i..].iter().take_while(|row| row.0 == last).any(|row| s.ends_with(row.1))
}

fn positive(s: &str) -> Option<String> {
    let x = cardinal(s)?;
    (x != "ศูนย์").then_some(x)
}
fn day(s: &str) -> Option<String> {
    let mut chars = s.chars();
    let first = digit(chars.next()?)?;
    let second = match chars.next() {
        Some(c) => Some(digit(c)?),
        None => None,
    };
    if chars.next().is_some() {
        return None;
    }
    let mut buf = [b'0' + first, 0];
    let len = if let Some(second) = second {
        buf[1] = b'0' + second;
        2
    } else {
        1
    };
    let digits = std::str::from_utf8(&buf[..len]).ok()?;
    positive(digits).or_else(|| digits.strip_prefix('0').and_then(positive))
}
fn four(s: &str) -> Option<String> {
    if s.chars().count() != 4 || !s.chars().all(|c| digit(c).is_some()) {
        return None;
    }
    positive(s)
}
fn month_num(s: &str) -> Option<&'static str> {
    let mut chars = s.chars();
    let first = digit(chars.next()?)? as usize;
    let second = match chars.next() {
        Some(c) => Some(digit(c)? as usize),
        None => None,
    };
    if chars.next().is_some() {
        return None;
    }
    let n = match second {
        Some(second) => first * 10 + second,
        None => first,
    };
    if n == 0 || n > 12 {
        return None;
    }
    Some(MONTHS[n - 1].0)
}
fn era(s: &str) -> Option<String> {
    for (p, word) in [("พ.ศ.", "พุทธศักราช"), ("พุทธศักราช", "พุทธศักราช"), ("ค.ศ.", "คริสต์ศักราช"), ("คริสต์ศักราช", "คริสต์ศักราช")]
    {
        if let Some(rest) = s.strip_prefix(p) {
            // Both the explicit prefix and opt_sp may consume one space.
            let rest = rest.strip_prefix(' ').unwrap_or(rest);
            let rest = rest.strip_prefix(' ').unwrap_or(rest);
            if let Some(y) = positive(rest) {
                return Some(format!("{word}{y}"));
            }
        }
    }
    None
}

/// Entries carry either a complete date tag or a spoken name and their internal weight.
pub(crate) fn parse(s: &str) -> Vec<(String, i64, bool)> {
    let mut out = Vec::new();
    let mut iso = s.split('-');
    if let (Some(y), Some(m), Some(d), None) = (iso.next(), iso.next(), iso.next(), iso.next()) {
        if m.chars().count() == 2 && d.chars().count() == 2 {
            if let (Some(y), Some(m), Some(d)) = (four(y), month_num(m), day(d)) {
                out.push((format!("date {{ year: \"{y}\" month: \"{m}\" day: \"วันที่{d}\" }}"), 0, true));
            }
        }
    }
    for sep in ['/', '.'] {
        let mut p = s.split(sep);
        if let (Some(d), Some(m), Some(y), None) = (p.next(), p.next(), p.next(), p.next()) {
            if let (Some(d), Some(m), Some(y)) = (day(d), month_num(m), four(y)) {
                out.push((format!("วันที่{d} {m} {y}"), 1_010_000, false));
            }
        }
    }
    if let Some(y) = s.strip_suffix('s').and_then(four) {
        out.push((format!("ทศวรรษ {y}"), 1_030_000, false));
    }
    if let Some(y) = era(s) {
        out.push((y, 1_040_000, false));
    }
    for (suffix, before) in [
        (" BC", true),
        (" B.C.", true),
        (" BCE", true),
        (" B.C.E.", true),
        (" ปีก่อนคริสตกาล", true),
        (" AD", false),
        (" A.D.", false),
        (" CE", false),
        (" C.E.", false),
    ] {
        if let Some(y) = s.strip_suffix(suffix).and_then(positive) {
            out.push((
                if before {
                    format!("{y}ปีก่อนคริสตกาล")
                } else {
                    format!("คริสต์ศักราช{y}")
                },
                1_040_000,
                false,
            ));
        }
    }
    for (i, c) in s.char_indices() {
        if matches!(c, '-' | '–' | '—') {
            let a = s[..i].strip_suffix(' ').unwrap_or(&s[..i]);
            let b = &s[i + c.len_utf8()..];
            let b = b.strip_prefix(' ').unwrap_or(b);
            if let (Some(a), Some(b)) = (four(a), four(b)) {
                out.push((format!("{a}ถึง{b}"), 1_050_000, false));
            }
        }
    }
    let rest = s.strip_prefix("วันที่").map(|r| r.strip_prefix(' ').unwrap_or(r)).unwrap_or(s);
    if s.starts_with("วันที่") {
        if let Some(d) = day(rest) {
            out.push((format!("วันที่{d}"), 1_030_000, false));
        }
    }
    for (name, abbr) in MONTHS {
        for (m, dotted) in [(name, false), (abbr, false), (abbr, true)] {
            for (i, _) in rest.match_indices(m) {
                if dotted && !rest[i + m.len()..].starts_with('.') {
                    continue;
                }
                let left = &rest[..i];
                let left = left.strip_suffix(' ').unwrap_or(left);
                let Some(d) = day(left) else { continue };
                let right = &rest[i + m.len() + usize::from(dotted)..];
                if right.is_empty() {
                    let mid = if rest[..i].ends_with(' ') { " " } else { "" };
                    out.push((format!("วันที่{d}{mid}{name}"), 1_030_000, false));
                }
                let (space, y) = if let Some(y) = right.strip_prefix(' ') { (" ", y) } else { ("", right) };
                if let Some(y) = era(y).or_else(|| positive(y)) {
                    let mid = if rest[..i].ends_with(' ') { " " } else { "" };
                    out.push((format!("วันที่{d}{mid}{name}{space}{y}"), 1_020_000, false));
                }
            }
        }
    }
    out
}
