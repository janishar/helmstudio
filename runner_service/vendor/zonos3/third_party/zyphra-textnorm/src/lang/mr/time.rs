//! Marathi clock and time-zone branches.
use crate::lattice::Candidate;
use crate::text::{Cost, cost};
use std::sync::LazyLock;

const HOURS: &str = include_str!("../../../data/mr/time/hours.tsv");
const MINUTES: &str = include_str!("../../../data/mr/time/minutes.tsv");
const SECONDS: &str = include_str!("../../../data/mr/time/seconds.tsv");

const ZONES: &[(&str, &str)] = &[
    ("UTC", "यू टी सी"),
    ("GMT", "जी एम टी"),
    ("IST", "आय एस टी"),
    ("EST", "ई एस टी"),
    ("EDT", "ई डी टी"),
    ("CST", "सी एस टी"),
    ("CDT", "सी डी टी"),
    ("MST", "एम एस टी"),
    ("MDT", "एम डी टी"),
    ("PST", "पी एस टी"),
    ("PDT", "पी डी टी"),
    ("CET", "सी ई टी"),
    ("CEST", "सी ई एस टी"),
    ("BST", "बी एस टी"),
    ("JST", "जे एस टी"),
    ("KST", "के एस टी"),
    ("AEST", "ए ई एस टी"),
];

fn rows(data: &'static str) -> impl Iterator<Item = (&'static str, &'static str)> {
    data.lines().filter_map(|l| l.trim_end_matches('\r').split_once('\t'))
}

static H_MAP: LazyLock<std::collections::HashMap<&'static str, &'static str>> = LazyLock::new(|| {
    let mut out = std::collections::HashMap::new();
    for (k, v) in rows(HOURS) {
        out.insert(k, v);
    }
    out
});

static M_MAP: LazyLock<std::collections::HashMap<&'static str, &'static str>> = LazyLock::new(|| {
    let mut out = std::collections::HashMap::new();
    for (k, v) in rows(MINUTES) {
        out.insert(k, v);
    }
    out
});

static S_MAP: LazyLock<std::collections::HashMap<&'static str, &'static str>> = LazyLock::new(|| {
    let mut out = std::collections::HashMap::new();
    for (k, v) in rows(SECONDS) {
        out.insert(k, v);
    }
    out
});

fn hour_num(s: &str) -> Option<u32> {
    s.chars().try_fold(0u32, |n, c| {
        let d = if c.is_ascii_digit() {
            c as u32 - '0' as u32
        } else if ('०'..='९').contains(&c) {
            c as u32 - '०' as u32
        } else {
            return None;
        };
        Some(n * 10 + d)
    })
}

fn am_period(h: u32) -> &'static str {
    match h {
        12 | 1 | 2 | 3 => "रात्री",
        4 | 5 => "पहाटे",
        _ => "सकाळी",
    }
}

fn pm_period(h: u32) -> &'static str {
    match h {
        12 | 1 | 2 | 3 => "दुपारी",
        9 | 10 | 11 => "रात्री",
        _ => "संध्याकाळी",
    }
}

fn clock(s: &str) -> Vec<(String, Cost)> {
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

    for &(input, marker, zone) in &expanded[..count] {
        let mut parts = input.split(':');
        let Some(hour) = parts.next() else { continue };
        let Some(minute) = parts.next() else { continue };
        let second = parts.next();
        if parts.next().is_some() {
            continue;
        }

        let Some(h_word) = H_MAP.get(hour).copied() else { continue };
        let h_num = hour_num(hour);

        let mut period_prefix = String::new();
        let mut extra_w = 0.0;
        if let Some(pm) = marker {
            let Some(hn) = h_num else { continue };
            if hn < 1 || hn > 12 {
                continue;
            }
            extra_w = -0.1;
            let p = if pm { pm_period(hn) } else { am_period(hn) };
            period_prefix = format!("period: \"{p}\" ");
        }

        let zone_suffix = zone.map(|z| format!(" zone: \"{z}\"")).unwrap_or_default();

        if let Some(second) = second {
            let Some(m_word) = M_MAP.get(minute).copied() else { continue };
            let Some(s_word) = S_MAP.get(second).copied() else { continue };
            out.push((
                super::lazy::tag(|| {
                    format!(
                        "{period_prefix}hours: \"{h_word}\" minutes: \"{m_word}\" seconds: \"{s_word}\"{zone_suffix}"
                    )
                }),
                cost(extra_w),
            ));
        } else if minute == "00" || minute == "००" {
            out.push((
                super::lazy::tag(|| format!("{period_prefix}hours: \"{h_word}\"{zone_suffix}")),
                cost(0.3 + extra_w),
            ));
        } else if let Some(m_word) = M_MAP.get(minute).copied() {
            out.push((
                super::lazy::tag(|| format!("{period_prefix}hours: \"{h_word}\" minutes: \"{m_word}\"{zone_suffix}")),
                cost(0.3 + extra_w),
            ));
        }
    }

    out
}

/// Materialize an unqualified hour/minute clock without replaying other clock branches.
pub(crate) fn materialize_simple(s: &str, selected_cost: Cost) -> Option<String> {
    if selected_cost != cost(1.1) + cost(0.3) {
        return None;
    }
    let (hour, minute) = s.split_once(':')?;
    let hour = H_MAP.get(hour)?;
    let minute = if minute == "00" || minute == "००" { None } else { Some(*M_MAP.get(minute)?) };
    let mut tag =
        String::with_capacity("time { hours: \"\" minutes: \"\" }".len() + hour.len() + minute.map_or(0, str::len));
    tag.push_str("time { hours: \"");
    tag.push_str(hour);
    tag.push('"');
    if let Some(minute) = minute {
        tag.push_str(" minutes: \"");
        tag.push_str(minute);
        tag.push('"');
    }
    tag.push_str(" }");
    Some(tag)
}

pub fn classify(s: &str, end: usize, out: &mut Vec<Candidate>) {
    // 1. Single time
    for (val, c) in clock(s) {
        out.push(Candidate {
            end,
            cost: cost(1.1) + c,
            rank: 5,
            tag: super::lazy::tag(|| format!("time {{ {val} }}")),
        });
    }

    // 2. Time range: "start - end" or "start – end"
    for sep in ['–', '-'] {
        if let Some((a, b)) = s.split_once(sep) {
            let a = a.trim();
            let b = b.trim();
            for (v1, c1) in clock(a) {
                for (v2, c2) in clock(b) {
                    out.push(Candidate {
                        end,
                        cost: cost(1.1 - 0.01) + c1 + c2,
                        rank: 5,
                        tag: super::lazy::tag(|| {
                            format!("time {{ start {{ {v1} }} end {{ {v2} }} preserve_order: true }}")
                        }),
                    });
                }
            }
        }
    }
}
