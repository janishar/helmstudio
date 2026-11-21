use super::graph_utils::{cardinal, digit, final_cardinal};
use super::numeric::Analysis;
use std::collections::HashMap;
use std::sync::LazyLock;
const MONTHS: &str = include_str!("../../../data/kn/date/months.tsv");
const MONTH_NAMES: &str = include_str!("../../../data/kn/date/month_names.tsv");
const ERAS: &str = include_str!("../../../data/kn/date/year_suffix.tsv");
type Table = HashMap<&'static str, &'static str>;
static TABLES: LazyLock<[Table; 6]> = LazyLock::new(|| {
    [
        MONTHS,
        MONTH_NAMES,
        ERAS,
        include_str!("../../../data/kn/time/hours.tsv"),
        include_str!("../../../data/kn/time/minutes.tsv"),
        include_str!("../../../data/kn/time/seconds.tsv"),
    ]
    .map(|src| {
        let mut table = HashMap::new();
        for (key, value) in src.lines().filter_map(|row| row.split_once('\t')) {
            table.entry(key).or_insert(value);
        }
        table
    })
});

fn digits_lookup(s: &str, index: usize) -> Option<&'static str> {
    let mut bytes = [0u8; 2];
    let mut count = 0;
    for c in s.chars() {
        if count == bytes.len() {
            return None;
        }
        bytes[count] = b'0' + digit(c)?;
        count += 1;
    }
    if count == 0 {
        return None;
    }
    TABLES[index].get(std::str::from_utf8(&bytes[..count]).ok()?).copied()
}

fn literal_lookup(s: &str, index: usize) -> Option<&'static str> {
    TABLES[index].get(s).copied()
}
fn field(k: &str, v: &str) -> String {
    format!("{k}: \"{v}\"")
}
fn emit(out: &mut Vec<Analysis>, kind: &str, fields: Vec<String>, cost: i64) {
    out.push((cost, 0, format!("{kind} {{ {} }}", fields.join(" "))));
}
fn day(s: &str) -> Option<String> {
    let first = s.chars().next()?;
    let t = if digit(first) == Some(0) { &s[first.len_utf8()..] } else { s };
    if t.chars().count() > 3 || t.is_empty() {
        return None;
    }
    cardinal(t)
}
fn year(s: &str) -> Option<String> {
    if !s.chars().all(|c| digit(c).is_some()) {
        return None;
    }
    final_cardinal(s).map(|v| v.0)
}
fn year_weight(s: &str) -> i64 {
    if s.chars().count() > 1 && s.chars().next().is_some_and(|c| matches!(c, '0' | '೦')) { 500_000 } else { 0 }
}
fn ascii_month(s: &str) -> Option<&'static str> {
    digits_lookup(s, 0)
}
fn date(s: &str, out: &mut Vec<Analysis>) {
    if s.starts_with(' ') || s.ends_with(' ') {
        return;
    }
    let mut parts = s.split(|c| matches!(c, '/' | '.' | '-'));
    if let (Some(a), Some(b), Some(c), None) = (parts.next(), parts.next(), parts.next(), parts.next()) {
        if let Some(m) = ascii_month(b) {
            if let (Some(d), Some(y)) = (day(a), year(c)) {
                emit(
                    out,
                    "date",
                    vec![field("day", &d), field("month", m), field("year", &y)],
                    1_080_000 + year_weight(c),
                );
            }
            if s.chars().filter(|c| *c == '-').count() == 2 && a.chars().count() == 4 {
                if let (Some(y), Some(d)) = (year(a), day(c)) {
                    emit(
                        out,
                        "date",
                        vec![field("year", &y), field("month", m), field("day", &d)],
                        1_080_000 + year_weight(a),
                    );
                }
            }
        }
    }
    let mut parts = s.split(' ').filter(|v| !v.is_empty());
    if let (Some(a), Some(b), c, None) = (parts.next(), parts.next(), parts.next(), parts.next()) {
        if let Some(m) = literal_lookup(b, 1) {
            if let Some(d) = day(a) {
                let mut f = vec![field("day", &d), field("month", m)];
                if let Some(c) = c {
                    if let Some(y) = year(c) {
                        f.push(field("year", &y));
                        emit(out, "date", f, 1_080_000 + year_weight(c));
                    }
                } else {
                    emit(out, "date", f, 1_080_000);
                }
            }
        }
        if c.is_none() {
            if let Some(m) = literal_lookup(a, 1) {
                if let Some(y) = year(b) {
                    emit(out, "date", vec![field("month", m), field("year", &y)], 1_080_000 + year_weight(b));
                }
            }
            if let Some(e) = literal_lookup(b, 2) {
                if let Some(y) = year(a) {
                    emit(out, "date", vec![field("year", &y), field("era", e)], 1_080_000 + year_weight(a));
                }
            }
        }
    }
    for sep in ['-', '–', '—'] {
        if let Some((a, b)) = s.split_once(sep) {
            let a = a.strip_suffix(' ').unwrap_or(a);
            let b = b.strip_prefix(' ').unwrap_or(b);
            if a.chars().count() == 4 && b.chars().count() == 4 {
                if let (Some(x), Some(y)) = (year(a), year(b)) {
                    emit(
                        out,
                        "date",
                        vec![field("year", &format!("{x} ರಿಂದ {y}"))],
                        1_080_000 + year_weight(a) + year_weight(b),
                    );
                }
            }
        }
    }
    for suffix in ["s", "ರ"] {
        if let Some(n) = s.strip_suffix(suffix) {
            if let Some(y) = year(n) {
                let y = if let Some(pre) = y.strip_suffix("ತ್ತು") {
                    format!("{pre}ತ್ತರ ದಶಕ")
                } else {
                    y
                };
                emit(out, "date", vec![field("year", &y)], 1_080_000 + year_weight(n));
            }
        }
    }
}
fn time(s: &str, out: &mut Vec<Analysis>) {
    if s.starts_with(' ') || s.ends_with(' ') {
        return;
    }
    let mut text = s;
    let mut zone = None;
    for (key, val) in [("UTC", "ಯು ಟಿ ಸಿ"), ("GMT", "ಜಿ ಎಮ್ ಟಿ"), ("IST", "ಐ ಎಸ್ ಟಿ"), ("PST", "ಪಿ ಎಸ್ ಟಿ"), ("EST", "ಇ ಎಸ್ ಟಿ")]
    {
        if let Some(v) = text.strip_suffix(key).and_then(|v| v.strip_suffix(' ')) {
            text = v;
            zone = Some(val);
            break;
        }
    }
    let mut period = None;
    let mut marker = None;
    for m in ["a.m.", "A.M.", "p.m.", "P.M.", "am", "AM", "Am", "pm", "PM", "Pm"] {
        if let Some(v) = text.strip_suffix(m) {
            text = v.strip_suffix(' ').unwrap_or(v);
            marker = Some(m);
            break;
        }
    }
    let mut parts = [""; 3];
    let mut count = 0;
    for part in text.split(':') {
        if count == parts.len() {
            return;
        }
        parts[count] = part;
        count += 1;
    }
    let h = match digits_lookup(parts[0], 3) {
        Some(v) => v,
        None => return,
    };
    if let Some(m) = marker {
        if !parts[0].is_ascii() {
            return;
        }
        let n: u8 = match parts[0].parse() {
            Ok(n @ 1..=12) => n,
            _ => return,
        };
        let am = m.starts_with('a') || m.starts_with('A');
        period = Some(if am {
            if (4..=11).contains(&n) { "ಬೆಳಗ್ಗೆ" } else { "ರಾತ್ರಿ" }
        } else if n == 12 || n <= 3 {
            "ಮಧ್ಯಾಹ್ನ"
        } else if n <= 8 {
            "ಸಂಜೆ"
        } else {
            "ರಾತ್ರಿ"
        });
    } else if count < 2 {
        return;
    }
    if marker.is_some() && count > 2 {
        return;
    }
    let mut f = Vec::new();
    if let Some(p) = period {
        f.push(field("day_period", p));
    }
    f.push(field("hours", &h));
    if count >= 2 {
        let m = match digits_lookup(parts[1], 4) {
            Some(v) => v,
            None => return,
        };
        if count == 3 || !(parts[1] == "00" || parts[1] == "೦೦") {
            f.push(field("minutes", m));
        }
    }
    if count == 3 {
        let sec = match digits_lookup(parts[2], 5) {
            Some(v) => v,
            None => return,
        };

        f.push(field("seconds", sec));
    }
    if count == 2 && (parts[1] == "00" || parts[1] == "೦೦") {
        let mut full = f.clone();
        full.push(field("minutes", "ಸೊನ್ನೆ"));
        if let Some(z) = zone {
            full.push(field("zone", z));
        }
        let tag = format!("time {{ {} }}", full.join(" "));
        out.push((if marker.is_some() { 1_000_000 } else { 1_100_000 }, 1, tag));
    }
    if let Some(z) = zone {
        f.push(field("zone", z));
    }
    emit(out, "time", f, if marker.is_some() { 1_000_000 } else { 1_100_000 });
}
fn spoken_clock(s: &str) -> Option<String> {
    let mut p = [""; 3];
    let mut count = 0;
    for part in s.split(':') {
        if count == p.len() {
            return None;
        }
        p[count] = part;
        count += 1;
    }
    if !(2..=3).contains(&count)
        || !(1..=2).contains(&p[0].chars().count())
        || p[1].chars().count() != 2
        || (count == 3 && p[2].chars().count() != 2)
    {
        return None;
    }
    let h = digits_lookup(p[0], 3)?;
    let minute = digits_lookup(p[1], 4)?;
    let mut v = format!("{h} ಗಂಟೆ");
    if count == 3 || (p[1] != "00" && p[1] != "೦೦") {
        v.push_str(&format!(" {minute} ನಿಮಿಷ"));
    }
    if count == 3 {
        v.push_str(&format!(" {} ಸೆಕೆಂಡ್", digits_lookup(p[2], 5)?));
    }
    Some(v)
}
fn time_ranges(s: &str, out: &mut Vec<Analysis>) {
    for sep in ['-', '–', '—'] {
        if let Some((a, b)) = s.split_once(sep) {
            let a = a.strip_suffix(' ').unwrap_or(a);
            let b = b.strip_prefix(' ').unwrap_or(b);
            if let (Some(x), Some(y)) = (spoken_clock(a), spoken_clock(b)) {
                let v = format!("{x} ರಿಂದ {y}").replace(' ', "\u{a0}");
                out.push((1_030_000, 0, format!("name: \"{v}\"")));
            }
            if sep != '—' && (a.ends_with(":00") || a.ends_with(":೦೦")) && (b.ends_with(":00") || b.ends_with(":೦೦"))
            {
                if let (Some(x), Some(y)) = (
                    digits_lookup(a.split_once(':').map(|v| v.0).unwrap_or(""), 3),
                    digits_lookup(b.split_once(':').map(|v| v.0).unwrap_or(""), 3),
                ) {
                    emit(out, "time", vec![field("hours", &format!("{x} ಗಂಟೆ ರಿಂದ {y}"))], 1_100_000);
                }
            }
        }
    }
}
pub fn analyze(s: &str, out: &mut Vec<Analysis>) {
    date(s, out);
    time(s, out);
    time_ranges(s, out);
}
