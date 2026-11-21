//! Gujarati clock and time-zone branches.
use crate::lattice::Candidate;
use crate::text::{Cost, cost};

const HOURS: &str = include_str!("../../../data/gu/time/hours.tsv");
const MINUTES: &str = include_str!("../../../data/gu/time/minutes.tsv");
const SECONDS: &str = include_str!("../../../data/gu/time/seconds.tsv");
const ZONES: &[(&str, &str)] = &[
    ("UTC", "યુ ટી સી"),
    ("GMT", "જી એમ ટી"),
    ("IST", "આઇ એસ ટી"),
    ("EST", "ઈ એસ ટી"),
    ("EDT", "ઈ ડી ટી"),
    ("CST", "સી એસ ટી"),
    ("CDT", "સી ડી ટી"),
    ("MST", "એમ એસ ટી"),
    ("MDT", "એમ ડી ટી"),
    ("PST", "પી એસ ટી"),
    ("PDT", "પી ડી ટી"),
    ("CET", "સી ઈ ટી"),
    ("CEST", "સી ઈ એસ ટી"),
    ("BST", "બી એસ ટી"),
    ("JST", "જે એસ ટી"),
    ("KST", "કે એસ ટી"),
    ("AEST", "એ ઈ એસ ટી"),
];
fn lookup(data: &'static str, key: &str) -> Option<&'static str> {
    use std::collections::HashMap;
    use std::sync::LazyLock;
    fn index(data: &'static str) -> HashMap<&'static str, &'static str> {
        let mut result = HashMap::new();
        for (k, v) in data.lines().filter_map(|l| l.trim_end_matches('\r').split_once('\t')) {
            result.entry(k).or_insert(v);
        }
        result
    }
    static H: LazyLock<HashMap<&'static str, &'static str>> = LazyLock::new(|| index(HOURS));
    static M: LazyLock<HashMap<&'static str, &'static str>> = LazyLock::new(|| index(MINUTES));
    static S: LazyLock<HashMap<&'static str, &'static str>> = LazyLock::new(|| index(SECONDS));
    if std::ptr::eq(data, HOURS) {
        H.get(key).copied()
    } else if std::ptr::eq(data, MINUTES) {
        M.get(key).copied()
    } else {
        S.get(key).copied()
    }
}
fn hour_num(s: &str) -> Option<u32> {
    s.chars().try_fold(0u32, |n, c| {
        let d = if c.is_ascii_digit() {
            c as u32 - '0' as u32
        } else if ('૦'..='૯').contains(&c) {
            c as u32 - '૦' as u32
        } else {
            return None;
        };
        Some(n * 10 + d)
    })
}
fn clock(s: &str) -> Vec<(String, Cost)> {
    // A zone can match only after a space, so at most one zone suffix
    // matches; each resulting form likewise has at most one AM/PM suffix.
    let mut expanded = [(s, None, None); 4];
    let mut count = 1;
    if s.as_bytes().last().is_some_and(u8::is_ascii_uppercase) {
        for (z, v) in ZONES {
            if let Some(pre) = s.strip_suffix(z).and_then(|p| p.strip_suffix(' ')) {
                expanded[count] = (pre, None, Some(*v));
                count += 1;
            }
        }
    }
    let base_count = count;
    for i in 0..base_count {
        let (t, _, z) = expanded[i];
        if !matches!(t.as_bytes().last(), Some(b'm' | b'M' | b'.')) {
            continue;
        }
        for (marker, pm) in [
            ("am", false),
            ("AM", false),
            ("a.m.", false),
            ("A.M.", false),
            ("a.m", false),
            ("A.M", false),
            ("pm", true),
            ("PM", true),
            ("p.m.", true),
            ("P.M.", true),
            ("p.m", true),
            ("P.M", true),
        ] {
            if let Some(x) = t.strip_suffix(marker) {
                expanded[count] = (x.strip_suffix(' ').unwrap_or(x), Some(pm), z);
                count += 1;
            }
        }
    }
    let mut out = Vec::new();
    let building = super::lazy::building();
    for &(input, marker, zone) in &expanded[..count] {
        let mut parts = input.split(':');
        let Some(hour) = parts.next() else { continue };
        let Some(minute) = parts.next() else { continue };
        let seconds = parts.next();
        if parts.next().is_some() {
            continue;
        }
        let Some(h) = lookup(HOURS, hour) else { continue };
        let mut prefix = String::new();
        let mut weight = 0;
        if let Some(pm) = marker {
            let Some(n) = hour_num(hour) else { continue };
            if !(1..=12).contains(&n) {
                continue;
            }
            let period = if pm {
                if (1..=3).contains(&n) || n == 12 {
                    "બપોરે"
                } else if (8..=11).contains(&n) {
                    "રાત્રે"
                } else {
                    "સાંજે"
                }
            } else if n <= 3 || n == 12 {
                "રાત્રે"
            } else {
                "સવારે"
            };
            if building {
                prefix.push_str(&format!("period: \"{period}\" "));
            }
            weight += cost(-0.1);
        }
        if building {
            prefix.push_str(&format!("hours: \"{h}\" "));
        }
        if seconds.is_none() && matches!(minute, "00" | "૦૦") {
            out.push((
                if building {
                    format!("{prefix}{}", zone.map(|z| format!("zone: \"{z}\" ")).unwrap_or_default())
                } else {
                    String::new()
                },
                weight + cost(0.3),
            ));
        }
        let Some(m) = lookup(MINUTES, minute) else { continue };
        let mut fields = if building { format!("{prefix}minutes: \"{m}\" ") } else { String::new() };
        if let Some(seconds) = seconds {
            let Some(sec) = lookup(SECONDS, seconds) else { continue };
            if building {
                fields.push_str(&format!("seconds: \"{sec}\" "));
            }
        } else {
            weight += cost(0.3);
        }
        if building {
            if let Some(z) = zone {
                fields.push_str(&format!("zone: \"{z}\" "));
            }
        }
        out.push((fields, weight));
    }
    out
}
pub fn classify(s: &str, end: usize, out: &mut Vec<Candidate>) {
    if !s.chars().all(|c| {
        super::numbers::digit(c).is_some() || c.is_ascii_alphabetic() || matches!(c, ':' | '.' | ' ' | '-' | '–')
    }) {
        return;
    }
    for (fields, c) in clock(s) {
        out.push(Candidate {
            end,
            cost: cost(1.1) + c,
            rank: 5,
            tag: super::lazy::tag(|| format!("time {{ {} }}", fields.trim_end())),
        });
    }
    for (i, c) in s.char_indices() {
        if c != '-' && c != '–' {
            continue;
        }
        let a = s[..i].strip_suffix(' ').unwrap_or(&s[..i]);
        let rest = &s[i + c.len_utf8()..];
        let b = rest.strip_prefix(' ').unwrap_or(rest);
        for (x, cx) in clock(a) {
            for (y, cy) in clock(b) {
                out.push(Candidate {
                    end,
                    cost: cost(1.09) + cx + cy,
                    rank: 5,
                    tag: super::lazy::tag(|| {
                        format!(
                            "time {{ start {{ {} }} end {{ {} }} preserve_order: true }}",
                            x.trim_end(),
                            y.trim_end()
                        )
                    }),
                });
            }
        }
    }
}
