//! Bengali Time classifier.
use crate::lattice::Candidate;
use crate::text::cost;
use std::sync::LazyLock;

const HOURS_DATA: &str = include_str!("../../../data/bn/time/hours.tsv");
const MINUTES_DATA: &str = include_str!("../../../data/bn/time/minutes.tsv");
const SECONDS_DATA: &str = include_str!("../../../data/bn/time/seconds.tsv");

fn rows(data: &'static str) -> impl Iterator<Item = (&'static str, &'static str)> {
    data.lines().filter_map(|l| l.trim_end_matches('\r').split_once('\t'))
}

static HOURS: LazyLock<std::collections::HashMap<&'static str, &'static str>> =
    LazyLock::new(|| rows(HOURS_DATA).collect());
static MINUTES: LazyLock<std::collections::HashMap<&'static str, &'static str>> =
    LazyLock::new(|| rows(MINUTES_DATA).collect());
static SECONDS: LazyLock<std::collections::HashMap<&'static str, &'static str>> =
    LazyLock::new(|| rows(SECONDS_DATA).collect());

type ShortKeys = std::collections::HashMap<(char, Option<char>), &'static str>;
fn short_key(mut chars: impl Iterator<Item = char>) -> Option<(char, Option<char>)> {
    let first = chars.next()?;
    let second = chars.next();
    if chars.next().is_some() { None } else { Some((first, second)) }
}

fn short_keys(data: &'static str) -> ShortKeys {
    rows(data).filter_map(|(key, value)| short_key(key.chars()).map(|key| (key, value))).collect()
}

static SHORT_HOURS: LazyLock<ShortKeys> = LazyLock::new(|| short_keys(HOURS_DATA));
static SHORT_MINUTES: LazyLock<ShortKeys> = LazyLock::new(|| short_keys(MINUTES_DATA));

const TIMEZONES: &[(&str, &str)] = &[
    ("UTC", "ইউ টি সি"),
    ("GMT", "জি এম টি"),
    ("BST", "বি এস টি"),
    ("IST", "আই এস টি"),
    ("EST", "ই এস টি"),
    ("EDT", "ই ডি টি"),
    ("CST", "সি এস টি"),
    ("CDT", "সি ডি টি"),
    ("MST", "এম এস টি"),
    ("MDT", "এম ডি টি"),
    ("PST", "পি এস টি"),
    ("PDT", "পি ডি টি"),
    ("CET", "সি ই টি"),
    ("CEST", "সি ই এস টি"),
    ("JST", "জে এস টি"),
    ("KST", "কে এস টি"),
];

pub fn day_period(hour24: u32) -> &'static str {
    if (4..6).contains(&hour24) {
        "ভোর"
    } else if (6..12).contains(&hour24) {
        "সকাল"
    } else if (12..16).contains(&hour24) {
        "দুপুর"
    } else if (16..18).contains(&hour24) {
        "বিকাল"
    } else if (18..20).contains(&hour24) {
        "সন্ধ্যা"
    } else {
        "রাত"
    }
}

fn to_ascii_num(s: &str) -> Option<u32> {
    let mut n = 0u32;
    for c in s.chars() {
        let d = if c.is_ascii_digit() {
            c as u32 - '0' as u32
        } else if ('০'..='৯').contains(&c) {
            c as u32 - '০' as u32
        } else {
            return None;
        };
        n = n * 10 + d;
    }
    Some(n)
}

fn is_double_zero(s: &str) -> bool {
    s == "00" || s == "০০"
}

fn parse_zone(s: &str) -> (&str, Option<&'static str>) {
    let trimmed = s.trim_end();
    for &(z_code, z_spoken) in TIMEZONES {
        if let Some(prefix) = trimmed.strip_suffix(z_code) {
            if prefix.ends_with(' ') {
                return (prefix.trim_end(), Some(z_spoken));
            }
        }
    }
    (s, None)
}

fn parse_plain_hm(s: &str) -> Option<(&'static str, Option<&'static str>)> {
    let (h_s, m_s) = s.split_once(':')?;
    let h_word = *HOURS.get(h_s)?;
    if is_double_zero(m_s) { Some((h_word, None)) } else { Some((h_word, Some(*MINUTES.get(m_s)?))) }
}

fn plain_hm_word((hour, minute): (&str, Option<&str>)) -> String {
    match minute {
        Some(minute) => format!("{hour}টা {minute} মিনিট"),
        None => format!("{hour}টা"),
    }
}

fn time_parts(s: &str) -> Option<(&str, Option<&str>, Option<&str>)> {
    let mut parts = s.split(':');
    let hour = parts.next()?;
    let minute = parts.next();
    let second = parts.next();
    if parts.next().is_some() { None } else { Some((hour, minute, second)) }
}

struct PlainTime {
    hour: &'static str,
    minute: Option<&'static str>,
    second: Option<&'static str>,
    weight: crate::text::Cost,
}

fn plain_time(s: &str) -> Option<PlainTime> {
    let (hour, minute, second) = time_parts(s)?;
    let hour = *HOURS.get(hour)?;
    let minute = minute?;
    let minute_zero = is_double_zero(minute);
    let (minute, second, weight) = match second {
        None => (if minute_zero { None } else { Some(*MINUTES.get(minute)?) }, None, cost(0.3)),
        Some(second) => {
            let second_zero = is_double_zero(second);
            let minute = if minute_zero { None } else { Some(*MINUTES.get(minute)?) };
            let second = if second_zero { None } else { Some(*SECONDS.get(second)?) };
            (minute, second, if minute_zero || second_zero { cost(0.1) } else { 0 })
        }
    };
    Some(PlainTime { hour, minute, second, weight })
}

fn render_plain(time: &PlainTime, zone_tag: &str) -> String {
    let mut tag = String::with_capacity(
        48 + time.hour.len() + time.minute.map_or(0, str::len) + time.second.map_or(0, str::len) + zone_tag.len(),
    );
    tag.push_str("time { hours: \"");
    tag.push_str(time.hour);
    tag.push('"');
    if let Some(minute) = time.minute {
        tag.push_str(" minutes: \"");
        tag.push_str(minute);
        tag.push('"');
    }
    if let Some(second) = time.second {
        tag.push_str(" seconds: \"");
        tag.push_str(second);
        tag.push('"');
    }
    tag.push_str(zone_tag);
    tag.push_str(" }");
    tag
}

pub fn materialize_plain(s: &str, selected_cost: crate::text::Cost) -> Option<String> {
    let time = plain_time(s)?;
    (cost(1.1) + time.weight == selected_cost).then(|| render_plain(&time, ""))
}

pub fn materialize_plain_chars(chars: &[char], selected_cost: crate::text::Cost) -> Option<String> {
    if selected_cost != cost(1.1) + cost(0.3) {
        return None;
    }
    let mut parts = chars.split(|&c| c == ':');
    let hour = parts.next()?;
    let minute = parts.next()?;
    if parts.next().is_some() {
        return None;
    }
    let hour = *SHORT_HOURS.get(&short_key(hour.iter().copied())?)?;
    let minute = if minute == ['0', '0'] || minute == ['০', '০'] {
        None
    } else {
        Some(*SHORT_MINUTES.get(&short_key(minute.iter().copied())?)?)
    };
    Some(render_plain(&PlainTime { hour, minute, second: None, weight: cost(0.3) }, ""))
}

pub fn classify(s: &str, end: usize, out: &mut Vec<Candidate>) {
    let top_weight = cost(1.1);

    let (time_part, zone_opt) = parse_zone(s);
    let zone_tag = match zone_opt {
        Some(z) => super::lazy::tag(|| format!(" zone: \"{z}\"")),
        None => String::new(),
    };

    // 1. Time range: plain_hm + ("-" | "–") + plain_hm
    for dash in ["-", "–"] {
        if let Some((start_s, end_s)) = time_part.split_once(dash) {
            let start_s = start_s.trim();
            let end_s = end_s.trim();
            if let (Some(start_hm), Some(end_hm)) = (parse_plain_hm(start_s), parse_plain_hm(end_s)) {
                let zone_tag_c = zone_tag.clone();
                out.push(Candidate {
                    end,
                    cost: top_weight + cost(-0.05),
                    rank: 5,
                    tag: super::lazy::tag(move || {
                        let start_hm = plain_hm_word(start_hm);
                        let end_hm = plain_hm_word(end_hm);
                        format!("time {{ morphosyntactic_features: \"{start_hm} থেকে {end_hm}\"{zone_tag_c} }}")
                    }),
                });
            }
        }
    }

    // 2. 12-hour clock with AM / PM:
    for (marker, is_pm) in [
        ("am", false),
        ("AM", false),
        ("a.m.", false),
        ("A.M.", false),
        ("এএম", false),
        ("pm", true),
        ("PM", true),
        ("p.m.", true),
        ("P.M.", true),
        ("পিএম", true),
    ] {
        if let Some(prefix) = time_part.strip_suffix(marker) {
            let num_time = prefix.trim_end_matches(' ');
            let Some((h_s, minute, second)) = time_parts(num_time) else { continue };
            let Some(h12) = to_ascii_num(h_s) else { continue };
            if !(1..=12).contains(&h12) {
                continue;
            }
            let Some(&h_word) = HOURS.get(h_s) else { continue };
            let hour24 = (h12 % 12) + if is_pm { 12 } else { 0 };
            let period = day_period(hour24);

            let zone_tag_c = zone_tag.clone();
            if let (Some(m_s), None) = (minute, second) {
                if is_double_zero(m_s) {
                    // H:00
                    let zone_tag_c = zone_tag_c.clone();
                    out.push(Candidate {
                        end,
                        cost: top_weight + cost(-0.01) + cost(0.3),
                        rank: 5,
                        tag: super::lazy::tag(move || {
                            format!("time {{ suffix: \"{period}\" hours: \"{h_word}\"{zone_tag_c} }}")
                        }),
                    });
                } else if let Some(&m_word) = MINUTES.get(m_s) {
                    // H:M
                    let zone_tag_c = zone_tag_c.clone();
                    out.push(Candidate {
                        end,
                        cost: top_weight + cost(-0.01) + cost(0.3),
                        rank: 5,
                        tag: super::lazy::tag(move || {
                            format!(
                                "time {{ suffix: \"{period}\" hours: \"{h_word}\" minutes: \"{m_word}\"{zone_tag_c} }}"
                            )
                        }),
                    });
                }
            } else if let (Some(m_s), Some(s_s)) = (minute, second) {
                if let (Some(&m_word), Some(&s_word)) = (MINUTES.get(m_s), SECONDS.get(s_s)) {
                    let zone_tag_c = zone_tag_c.clone();
                    out.push(Candidate {
                        end,
                        cost: top_weight + cost(-0.01),
                        rank: 5,
                        tag: super::lazy::tag(move || {
                            format!("time {{ suffix: \"{period}\" hours: \"{h_word}\" minutes: \"{m_word}\" seconds: \"{s_word}\"{zone_tag_c} }}")
                        }),
                    });
                }
            }
        }
    }

    // 3. 24-hour clock (plain H:M or H:M:S)
    if let Some(time) = plain_time(time_part) {
        out.push(Candidate {
            end,
            cost: top_weight + time.weight,
            rank: 5,
            tag: super::lazy::tag(|| render_plain(&time, &zone_tag)),
        });
    }
}
