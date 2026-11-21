//! Telugu time classification grammar.

use super::lazy;
use super::numbers::{self, parse_digits_u64};
use crate::lattice::Candidate;
use crate::text::cost;

const TIMEZONES: [(&str, &str); 16] = [
    ("UTC", "యు టి సి"),
    ("GMT", "జి ఎం టి"),
    ("IST", "ఐ ఎస్ టి"),
    ("EST", "ఇ ఎస్ టి"),
    ("EDT", "ఇ డి టి"),
    ("CST", "సి ఎస్ టి"),
    ("CDT", "సి డి టి"),
    ("MST", "ఎం ఎస్ టి"),
    ("MDT", "ఎం డి టి"),
    ("PST", "పి ఎస్ టి"),
    ("PDT", "పి డి టి"),
    ("CET", "సి ఇ టి"),
    ("CEST", "సి ఇ ఎస్ టి"),
    ("BST", "బి ఎస్ టి"),
    ("JST", "జె ఎస్ టి"),
    ("KST", "కె ఎస్ టి"),
];

const AM_PM_MARKERS: [&str; 10] = ["a.m.", "A.M.", "p.m.", "P.M.", "am", "AM", "Am", "pm", "PM", "Pm"];

fn parse_hours(s: &str) -> Option<&'static str> {
    if !(1..=2).contains(&s.chars().count()) {
        return None;
    }
    let val = parse_digits_u64(s)?;
    if val == 0 {
        Some(numbers::DIGITS[0])
    } else if val <= 24 {
        numbers::spell_two_digits(val as u32)
    } else {
        None
    }
}

fn parse_minutes(s: &str) -> Option<&'static str> {
    if s.chars().count() != 2 {
        return None;
    }
    let val = parse_digits_u64(s)?;
    if (1..=59).contains(&val) { numbers::spell_two_digits(val as u32) } else { None }
}

fn zone_field(zone: Option<&str>) -> String {
    zone.map(|z| format!("zone: \"{z}\" ")).unwrap_or_default()
}

fn resolve_12h_period(is_am: bool, h: u64) -> Option<&'static str> {
    if is_am {
        if (6..=11).contains(&h) {
            Some("ఉదయం")
        } else if h == 12 || (1..=5).contains(&h) {
            Some("రాత్రి")
        } else {
            None
        }
    } else if h == 12 || (1..=5).contains(&h) {
        Some("మధ్యాహ్నం")
    } else if (6..=8).contains(&h) {
        Some("సాయంత్రం")
    } else if (9..=11).contains(&h) {
        Some("రాత్రి")
    } else {
        None
    }
}

pub fn classify(s: &str, end: usize, out: &mut Vec<Candidate>) {
    // Check timezone suffix
    let mut core = s;
    let mut tz_spoken = None;
    for &(tz_code, spoken) in &TIMEZONES {
        if let Some(rest) = s.strip_suffix(tz_code) {
            if let Some(trimmed) = rest.strip_suffix(' ') {
                core = trimmed;
                tz_spoken = Some(spoken);
                break;
            }
        }
    }

    let zone = || zone_field(tz_spoken);

    // 1. 12-hour clock with am/pm
    for marker in AM_PM_MARKERS {
        if let Some(rest) = core.strip_suffix(marker) {
            let body = rest.strip_suffix(' ').unwrap_or(rest);
            let hour_text = body.split(':').next().unwrap_or("");
            if !(1..=2).contains(&hour_text.len()) || !hour_text.bytes().all(|b| b.is_ascii_digit()) {
                continue;
            }
            let is_am = marker.starts_with('a') || marker.starts_with('A');

            // Case A: body has colon
            if let Some((h_s, m_s)) = body.split_once(':') {
                if let Some(h_val) = parse_digits_u64(h_s) {
                    if let Some(period) = resolve_12h_period(is_am, h_val) {
                        let h_sp = parse_hours(h_s).unwrap();
                        if m_s == "00" {
                            out.push(Candidate {
                                end,
                                cost: cost(1.1) - cost(0.1),
                                rank: 6,
                                tag: lazy::tag(|| {
                                    format!("time {{ day_period: \"{period}\" hours: \"{h_sp}\" {}}}", zone())
                                }),
                            });
                            return;
                        } else if let Some(m_sp) = parse_minutes(m_s) {
                            out.push(Candidate {
                                end,
                                cost: cost(1.1) - cost(0.1),
                                rank: 6,
                                tag: lazy::tag(|| {
                                    format!(
                                        "time {{ day_period: \"{period}\" hours: \"{h_sp}\" minutes: \"{m_sp}\" {}}}",
                                        zone()
                                    )
                                }),
                            });
                            return;
                        }
                    }
                }
            } else if let Some(h_val) = parse_digits_u64(body) {
                // Case B: bare hour with am/pm (e.g. "7 pm")
                if let Some(period) = resolve_12h_period(is_am, h_val) {
                    if let Some(h_sp) = parse_hours(body) {
                        out.push(Candidate {
                            end,
                            cost: cost(1.1) - cost(0.1),
                            rank: 6,
                            tag: lazy::tag(|| {
                                format!("time {{ day_period: \"{period}\" hours: \"{h_sp}\" {}}}", zone())
                            }),
                        });
                        return;
                    }
                }
            }
        }
    }

    // 2. 24-hour clock
    if core.contains(':') {
        let mut parts = core.split(':');
        let first = parts.next().unwrap_or("");
        let second = parts.next();
        let third = parts.next();
        if let (Some(m_s), Some(s_s), None) = (second, third, parts.next()) {
            // HMS: hours:minutes:seconds
            if let (Some(h_sp), Some(m_sp), Some(s_sp)) = (parse_hours(first), parse_minutes(m_s), parse_minutes(s_s)) {
                out.push(Candidate {
                    end,
                    cost: cost(1.1),
                    rank: 6,
                    tag: lazy::tag(|| {
                        format!("time {{ hours: \"{h_sp}\" minutes: \"{m_sp}\" seconds: \"{s_sp}\" {}}}", zone())
                    }),
                });
            }
        } else if let (Some(m_s), None) = (second, third) {
            // HM or H:00
            if let Some(h_sp) = parse_hours(first) {
                if m_s.chars().count() == 2 && m_s.chars().all(numbers::is_zero) {
                    out.push(Candidate {
                        end,
                        cost: cost(1.1) + cost(0.3),
                        rank: 6,
                        tag: lazy::tag(|| format!("time {{ hours: \"{h_sp}\" {}}}", zone())),
                    });
                } else if let Some(m_sp) = parse_minutes(m_s) {
                    out.push(Candidate {
                        end,
                        cost: cost(1.1) + cost(0.3),
                        rank: 6,
                        tag: lazy::tag(|| format!("time {{ hours: \"{h_sp}\" minutes: \"{m_sp}\" {}}}", zone())),
                    });
                }
            }
        }
    }
}
