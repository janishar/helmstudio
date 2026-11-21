//! Time classifier for Hindi.
use crate::lattice::Candidate;
use crate::text::cost;
use std::collections::HashMap;
use std::sync::LazyLock;

const HOURS_DATA: &str = include_str!("../../../data/hi/time/hours.tsv");
const MINUTES_DATA: &str = include_str!("../../../data/hi/time/minutes.tsv");
const SECONDS_DATA: &str = include_str!("../../../data/hi/time/seconds.tsv");
const PAUNE_DATA: &str = include_str!("../../../data/hi/whitelist/paune_mappings.tsv");

fn rows(data: &'static str) -> impl Iterator<Item = (&'static str, &'static str)> {
    data.lines().filter_map(|l| l.trim_end_matches('\r').split_once('\t'))
}

static HOURS_MAP: LazyLock<HashMap<&'static str, &'static str>> = LazyLock::new(|| rows(HOURS_DATA).collect());
static MINUTES_MAP: LazyLock<HashMap<&'static str, &'static str>> = LazyLock::new(|| rows(MINUTES_DATA).collect());
static SECONDS_MAP: LazyLock<HashMap<&'static str, &'static str>> = LazyLock::new(|| rows(SECONDS_DATA).collect());
static PAUNE_MAP: LazyLock<HashMap<&'static str, &'static str>> = LazyLock::new(|| rows(PAUNE_DATA).collect());
fn small_hour_word(s: &str) -> Option<(&'static str, crate::text::Cost)> {
    let mut chars = s.chars();
    let first = chars.next()?;
    match (chars.next(), chars.next()) {
        (None, None) if !super::numbers::zero(first) => super::numbers::small_word(&[first]),
        (Some(second), None) => super::numbers::small_word(&[first, second]),
        _ => None,
    }
}
/// Render the first time witness with the selected cost, in classifier order.
pub(crate) fn materialize(s: &str, candidate: &mut Candidate) {
    let selected = candidate.cost;
    if selected == cost(1.1 + 0.1) {
        let special = match s {
            "१:३०" | "१:30" | "1:३०" | "1:30" => Some("डेढ़"),
            "२:३०" | "२:30" | "2:३०" | "2:30" => Some("ढाई"),
            _ => None,
        };
        if let Some(value) = special {
            candidate.tag = format!("time {{ morphosyntactic_features: \"{value}\" }}");
            return;
        }
    }
    let Some((hour, rest)) = s.split_once(':') else { return };
    if !rest.contains(':') {
        let prefix = match rest {
            "15" | "१५" => Some("सवा"),
            "30" | "३०" => Some("साढ़े"),
            _ => None,
        };
        if let Some(prefix) = prefix {
            if let Some((spoken, weight)) = small_hour_word(hour) {
                if selected == cost(1.1 + 0.2) + weight {
                    candidate.tag = format!("time {{ morphosyntactic_features: \"{prefix} {spoken}\" }}");
                    return;
                }
            }
        }
        if matches!(rest, "45" | "४५") && selected == cost(1.1 + 0.1) {
            if let Some(&spoken) = PAUNE_MAP.get(hour) {
                candidate.tag = format!("time {{ morphosyntactic_features: \"पौने {spoken}\" }}");
                return;
            }
        }
        if selected == cost(1.1 + 0.3) {
            if matches!(rest, "00" | "००") {
                if let Some(&spoken) = HOURS_MAP.get(hour) {
                    candidate.tag = format!("time {{ hours: \"{spoken}\" }}");
                    return;
                }
            }
            if let (Some(&h), Some(&m)) = (HOURS_MAP.get(hour), MINUTES_MAP.get(rest)) {
                candidate.tag = format!("time {{ hours: \"{h}\" minutes: \"{m}\" }}");
            }
        }
    } else if selected == cost(1.1 + 0.0) {
        if let Some((minute, second)) = rest.split_once(':').filter(|(_, second)| !second.contains(':')) {
            if let (Some(&h), Some(&m), Some(&sec)) =
                (HOURS_MAP.get(hour), MINUTES_MAP.get(minute), SECONDS_MAP.get(second))
            {
                candidate.tag = format!("time {{ hours: \"{h}\" minutes: \"{m}\" seconds: \"{sec}\" }}");
            }
        }
    }
}

pub fn classify(s: &str, end: usize, out: &mut Vec<Candidate>) {
    if !s.contains(':') {
        return;
    }

    // Dedh / Dhai
    if s == "१:३०" || s == "१:30" || s == "1:३०" || s == "1:30" {
        out.push(Candidate {
            end,
            cost: cost(1.1 + 0.1),
            rank: 5,
            tag: super::lazy::tag(|| "time { morphosyntactic_features: \"डेढ़\" }".to_owned()),
        });
    } else if s == "२:३०" || s == "२:30" || s == "2:३०" || s == "2:30" {
        out.push(Candidate {
            end,
            cost: cost(1.1 + 0.1),
            rank: 5,
            tag: super::lazy::tag(|| "time { morphosyntactic_features: \"ढाई\" }".to_owned()),
        });
    }

    let (h_str, rest) = s.split_once(':').unwrap();
    if !rest.contains(':') {
        let m_str = rest;

        // Savva (:15 or :१५)
        if m_str == "15" || m_str == "१५" {
            // cardinal graph: digit | teens_and_ties (up to 99)
            if let Some((spoken, weight)) = small_hour_word(h_str) {
                out.push(Candidate {
                    end,
                    cost: cost(1.1 + 0.2) + weight,
                    rank: 5,
                    tag: super::lazy::tag(|| format!("time {{ morphosyntactic_features: \"सवा {spoken}\" }}")),
                });
            }
        }

        // Sadhe (:30 or :३०)
        if m_str == "30" || m_str == "३०" {
            if let Some((spoken, weight)) = small_hour_word(h_str) {
                out.push(Candidate {
                    end,
                    cost: cost(1.1 + 0.2) + weight,
                    rank: 5,
                    tag: super::lazy::tag(|| format!("time {{ morphosyntactic_features: \"साढ़े {spoken}\" }}")),
                });
            }
        }

        // Paune (:45 or :४५) - note: paune_mappings.tsv has Devanagari keys only
        if m_str == "45" || m_str == "४५" {
            if let Some(&next_num) = PAUNE_MAP.get(h_str) {
                out.push(Candidate {
                    end,
                    cost: cost(1.1 + 0.1),
                    rank: 5,
                    tag: super::lazy::tag(|| format!("time {{ morphosyntactic_features: \"पौने {next_num}\" }}")),
                });
            }
        }

        // Hour only (H:00 or H:००)
        if m_str == "00" || m_str == "००" {
            if let Some(&h_word) = HOURS_MAP.get(h_str) {
                out.push(Candidate {
                    end,
                    cost: cost(1.1 + 0.3),
                    rank: 5,
                    tag: super::lazy::tag(|| format!("time {{ hours: \"{h_word}\" }}")),
                });
            }
        }

        // Hour and Minute (H:M)
        if let (Some(&h_word), Some(&m_word)) = (HOURS_MAP.get(h_str), MINUTES_MAP.get(m_str)) {
            out.push(Candidate {
                end,
                cost: cost(1.1 + 0.3),
                rank: 5,
                tag: super::lazy::tag(|| format!("time {{ hours: \"{h_word}\" minutes: \"{m_word}\" }}")),
            });
        }
    } else if let Some((m_str, s_str)) = rest.split_once(':').filter(|(_, seconds)| !seconds.contains(':')) {
        if let (Some(&h_word), Some(&m_word), Some(&s_word)) =
            (HOURS_MAP.get(h_str), MINUTES_MAP.get(m_str), SECONDS_MAP.get(s_str))
        {
            out.push(Candidate {
                end,
                cost: cost(1.1 + 0.0),
                rank: 5,
                tag: super::lazy::tag(|| {
                    format!("time {{ hours: \"{h_word}\" minutes: \"{m_word}\" seconds: \"{s_word}\" }}")
                }),
            });
        }
    }
}
