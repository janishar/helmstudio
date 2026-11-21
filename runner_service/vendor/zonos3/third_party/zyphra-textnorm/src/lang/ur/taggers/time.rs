//! Productive Urdu time grammar.

use super::Sink;
use super::cardinal::enumerate_without_leading_zeros;
use crate::text::cost;
use std::sync::LazyLock;

fn key_index(s: &str) -> Option<usize> {
    match s.as_bytes() {
        [a] if a.is_ascii_digit() => Some((*a - b'0') as usize),
        [a, b] if a.is_ascii_digit() && b.is_ascii_digit() => {
            Some(10 + ((*a - b'0') as usize * 10) + (*b - b'0') as usize)
        }
        _ => None,
    }
}

fn table(data: &'static str) -> [Option<&'static str>; 110] {
    let mut map = [None; 110];
    for (key, value) in data.lines().filter_map(|line| line.trim_end_matches('\r').split_once('\t')) {
        if let Some(index) = key_index(key) {
            if map[index].is_none() {
                map[index] = Some(value);
            }
        }
    }
    map
}

const HOURS_DATA: &str = include_str!("../../../../data/ur/time/hours.tsv");
const MINUTES_DATA: &str = include_str!("../../../../data/ur/time/minutes.tsv");
const SECONDS_DATA: &str = include_str!("../../../../data/ur/time/seconds.tsv");
const PAUNE_DATA: &str = include_str!("../../../../data/ur/whitelist/paune_mappings.tsv");

static HOURS: LazyLock<[Option<&'static str>; 110]> = LazyLock::new(|| table(HOURS_DATA));
static MINUTES: LazyLock<[Option<&'static str>; 110]> = LazyLock::new(|| table(MINUTES_DATA));
static SECONDS: LazyLock<[Option<&'static str>; 110]> = LazyLock::new(|| table(SECONDS_DATA));

static PAUNE_MAP: LazyLock<[Option<&'static str>; 100]> = LazyLock::new(|| {
    let mut out = [None; 100];
    for line in PAUNE_DATA.lines() {
        let Some((k, v)) = line.trim_end_matches('\r').split_once('\t') else { continue };
        if let Ok(n) = k.parse::<usize>() {
            if n < 100 {
                out[n] = Some(v);
            }
        }
    }
    out
});

fn lookup_hour(s: &str) -> Option<&'static str> {
    HOURS[key_index(s)?]
}

fn lookup_minute(s: &str) -> Option<&'static str> {
    MINUTES[key_index(s)?]
}

fn lookup_second(s: &str) -> Option<&'static str> {
    SECONDS[key_index(s)?]
}

pub fn day_period(hour: usize, pm: bool) -> &'static str {
    let hour24 = (hour % 12) + if pm { 12 } else { 0 };
    if (4..12).contains(&hour24) {
        "صبح"
    } else if (12..16).contains(&hour24) {
        "دوپہر"
    } else if (16..18).contains(&hour24) {
        "سہ پہر"
    } else if (18..20).contains(&hour24) {
        "شام"
    } else {
        "رات"
    }
}

const TIMEZONES: &[(&str, &str)] = &[
    ("UTC", "یو ٹی سی"),
    ("GMT", "جی ایم ٹی"),
    ("PKT", "پی کے ٹی"),
    ("IST", "آئی ایس ٹی"),
    ("EST", "ای ایس ٹی"),
    ("EDT", "ای ڈی ٹی"),
    ("CST", "سی ایس ٹی"),
    ("CDT", "سی ڈی ٹی"),
    ("MST", "ایم ایس ٹی"),
    ("MDT", "ایم ڈی ٹی"),
    ("PST", "پی ایس ٹی"),
    ("PDT", "پی ڈی ٹی"),
    ("CET", "سی ای ٹی"),
    ("CEST", "سی ای ایس ٹی"),
    ("GST", "جی ایس ٹی"),
    ("AST", "اے ایس ٹی"),
];

const AM_MARKERS: &[&str] = &["a.m.", "A.M.", "am", "AM"];
const PM_MARKERS: &[&str] = &["p.m.", "P.M.", "pm", "PM"];

static TIME_ENDS: LazyLock<std::collections::HashSet<char>> = LazyLock::new(|| {
    AM_MARKERS
        .iter()
        .chain(PM_MARKERS)
        .filter_map(|marker| marker.chars().last())
        .chain(TIMEZONES.iter().filter_map(|(key, _)| key.chars().last()))
        .collect()
});

pub(crate) fn possible_end(last: char) -> bool {
    last.is_ascii_digit() || TIME_ENDS.contains(&last)
}

static TIME_CHARS: LazyLock<std::collections::HashSet<char>> = LazyLock::new(|| {
    let mut chars: std::collections::HashSet<char> = " :-–+".chars().collect();
    for marker in AM_MARKERS.iter().chain(PM_MARKERS) {
        chars.extend(marker.chars());
    }
    for &(key, _) in TIMEZONES {
        chars.extend(key.chars());
    }
    chars
});

pub(crate) fn continuation(c: char) -> bool {
    c.is_ascii_digit() || TIME_CHARS.contains(&c)
}

pub(crate) fn first_space_can_continue(rest: &[char]) -> bool {
    if matches!(rest, ['-' | '–', ' ', ..]) {
        return true;
    }
    AM_MARKERS
        .iter()
        .chain(PM_MARKERS)
        .chain(TIMEZONES.iter().map(|(key, _)| key))
        .any(|key| rest.len() >= key.len() && rest[..key.len()].iter().copied().eq(key.chars()))
}

fn check_tz(remaining: &[char]) -> Option<(usize, String)> {
    if !remaining.starts_with(&[' ']) {
        return None;
    }
    let after_sp = &remaining[1..];
    for &(tz_code, tz_spoken) in TIMEZONES {
        if after_sp.len() >= tz_code.len() && after_sp[..tz_code.len()].iter().copied().eq(tz_code.chars()) {
            let pos = 1 + tz_code.len();
            let after_code = &remaining[pos..];
            // Check optional offset: +1..12 or -1..12
            if after_code.starts_with(&['+']) || after_code.starts_with(&['-']) {
                let sign_word = if after_code[0] == '+' { " جمع " } else { " منفی " };
                let num_str = &after_code[1..];
                let mut n_len = 0;
                while n_len < num_str.len() && num_str[n_len].is_ascii_digit() {
                    n_len += 1;
                }
                if n_len > 0 && n_len <= 2 {
                    let bytes = [num_str[0] as u8, if n_len == 2 { num_str[1] as u8 } else { 0 }];
                    let s = std::str::from_utf8(&bytes[..n_len]).unwrap();
                    if !s.starts_with('0') && s.parse::<u8>().is_ok_and(|n| (1..=12).contains(&n)) {
                        let h_word = lookup_hour(&s)?;
                        return Some((
                            pos + 1 + n_len,
                            crate::lang::ur::lazy::tag(|| format!("{tz_spoken}{sign_word}{h_word}")),
                        ));
                    }
                }
            }
            return Some((pos, crate::lang::ur::lazy::tag(|| tz_spoken.to_owned())));
        }
    }
    None
}

fn classify_base(text: &[char], start: usize, sink: &mut Sink) {
    if start >= text.len() || !text[start].is_ascii_digit() {
        return;
    }
    let remaining = &text[start..];

    // Find colon
    let mut h_len = 0;
    while h_len < remaining.len() && remaining[h_len].is_ascii_digit() {
        h_len += 1;
    }
    if h_len == 0 || h_len > 2 {
        return;
    }

    let h_bytes = [remaining[0] as u8, if h_len == 2 { remaining[1] as u8 } else { 0 }];
    let h_str = std::str::from_utf8(&h_bytes[..h_len]).unwrap();
    let h_val: usize = match h_str.parse() {
        Ok(v) => v,
        Err(_) => return,
    };

    // 1. Check Special quarter/half forms: 1:30, 2:30, H:15, H:30, H:45
    if h_len + 3 <= remaining.len() && remaining[h_len] == ':' {
        let min_part = &remaining[h_len + 1..h_len + 3];
        let end_clock = h_len + 3;

        // Dedh / Dhai: 1:30, 2:30 (weight 0.1)
        if h_str == "1" && min_part == ['3', '0'] {
            let tz_info = check_tz(&remaining[end_clock..]);
            let (end, zone_field, tz_weight) = match &tz_info {
                Some((len, z)) => {
                    (start + end_clock + len, crate::lang::ur::lazy::tag(|| format!(" zone: \"{z}\"")), cost(-0.05))
                }
                None => (start + end_clock, String::new(), 0),
            };
            let c = cost(1.1) + cost(0.1) + tz_weight;
            sink.push(end, c, 0, move || format!("time {{ morphosyntactic_features: \"ڈیڑھ\"{zone_field} }}"));
        }
        if h_str == "2" && min_part == ['3', '0'] {
            let tz_info = check_tz(&remaining[end_clock..]);
            let (end, zone_field, tz_weight) = match &tz_info {
                Some((len, z)) => {
                    (start + end_clock + len, crate::lang::ur::lazy::tag(|| format!(" zone: \"{z}\"")), cost(-0.05))
                }
                None => (start + end_clock, String::new(), 0),
            };
            let c = cost(1.1) + cost(0.1) + tz_weight;
            sink.push(end, c, 0, move || format!("time {{ morphosyntactic_features: \"ڈھائی\"{zone_field} }}"));
        }

        // Savva: H:15 -> سوا <cardinal> (weight 0.2)
        if min_part == ['1', '5'] && remaining[0] != '0' {
            enumerate_without_leading_zeros(&remaining[..h_len], &mut |h_word, number_cost| {
                let tz_info = check_tz(&remaining[end_clock..]);
                let (end, zone_field, tz_weight) = match &tz_info {
                    Some((len, z)) => {
                        (start + end_clock + len, crate::lang::ur::lazy::tag(|| format!(" zone: \"{z}\"")), cost(-0.05))
                    }
                    None => (start + end_clock, String::new(), 0),
                };
                let c = cost(1.1) + cost(0.2) + number_cost + tz_weight;
                sink.push(end, c, 0, || format!("time {{ morphosyntactic_features: \"سوا {h_word}\"{zone_field} }}"));
            });
        }

        // Sadhe: H:30 -> ساڑھے <cardinal> (weight 0.2)
        if min_part == ['3', '0'] && remaining[0] != '0' && h_str != "1" && h_str != "2" {
            enumerate_without_leading_zeros(&remaining[..h_len], &mut |h_word, number_cost| {
                let tz_info = check_tz(&remaining[end_clock..]);
                let (end, zone_field, tz_weight) = match &tz_info {
                    Some((len, z)) => {
                        (start + end_clock + len, crate::lang::ur::lazy::tag(|| format!(" zone: \"{z}\"")), cost(-0.05))
                    }
                    None => (start + end_clock, String::new(), 0),
                };
                let c = cost(1.1) + cost(0.2) + number_cost + tz_weight;
                sink.push(end, c, 0, || format!("time {{ morphosyntactic_features: \"ساڑھے {h_word}\"{zone_field} }}"));
            });
        }

        // Paune: H:45 -> پونے <paune(H)> (weight 0.1)
        if min_part == ['4', '5'] && h_val < 100 && (h_len == 1 || remaining[0] != '0') {
            if let Some(paune_word) = PAUNE_MAP[h_val] {
                let tz_info = check_tz(&remaining[end_clock..]);
                let (end, zone_field, tz_weight) = match &tz_info {
                    Some((len, z)) => {
                        (start + end_clock + len, crate::lang::ur::lazy::tag(|| format!(" zone: \"{z}\"")), cost(-0.05))
                    }
                    None => (start + end_clock, String::new(), 0),
                };
                let c = cost(1.1) + cost(0.1) + tz_weight;
                sink.push(end, c, 0, move || {
                    format!("time {{ morphosyntactic_features: \"پونے {paune_word}\"{zone_field} }}")
                });
            }
        }
    }

    // 2. Regular digital clock: HH:MM[:SS] and HH:00
    if let Some(h_word) = lookup_hour(&h_str) {
        if h_len < remaining.len() && remaining[h_len] == ':' {
            let after_colon = &remaining[h_len + 1..];
            let mut m_len = 0;
            while m_len < after_colon.len() && after_colon[m_len].is_ascii_digit() {
                m_len += 1;
            }
            if m_len == 2 {
                let m_bytes = [after_colon[0] as u8, after_colon[1] as u8];
                let m_str = std::str::from_utf8(&m_bytes).unwrap();

                // 2a. HH:00 -> hours: "<h>" (weight 0.3)
                if m_str == "00" {
                    let end_clock = h_len + 1 + 2;
                    let tz_info = check_tz(&remaining[end_clock..]);
                    let (end, zone_field, tz_weight) = match &tz_info {
                        Some((len, z)) => (
                            start + end_clock + len,
                            crate::lang::ur::lazy::tag(|| format!(" zone: \"{z}\"")),
                            cost(-0.05),
                        ),
                        None => (start + end_clock, String::new(), 0),
                    };
                    let c = cost(1.1) + cost(0.3) + tz_weight;
                    sink.push(end, c, 0, move || format!("time {{ hours: \"{h_word}\"{zone_field} }}"));
                }

                if let Some(m_word) = lookup_minute(&m_str) {
                    let pos_after_m = h_len + 1 + 2;

                    // 2b. Check seconds: HH:MM:SS (weight 0.0)
                    if pos_after_m < remaining.len() && remaining[pos_after_m] == ':' {
                        let after_sec_colon = &remaining[pos_after_m + 1..];
                        let mut s_len = 0;
                        while s_len < after_sec_colon.len() && after_sec_colon[s_len].is_ascii_digit() {
                            s_len += 1;
                        }
                        if s_len == 2 {
                            let s_bytes = [after_sec_colon[0] as u8, after_sec_colon[1] as u8];
                            let s_str = std::str::from_utf8(&s_bytes).unwrap();
                            if let Some(s_word) = lookup_second(&s_str) {
                                let end_clock = pos_after_m + 1 + 2;
                                let tz_info = check_tz(&remaining[end_clock..]);
                                let (end, zone_field, tz_weight) = match &tz_info {
                                    Some((len, z)) => (
                                        start + end_clock + len,
                                        crate::lang::ur::lazy::tag(|| format!(" zone: \"{z}\"")),
                                        cost(-0.05),
                                    ),
                                    None => (start + end_clock, String::new(), 0),
                                };
                                let c = cost(1.1) + tz_weight;
                                sink.push(end, c, 0, move || {
                                    format!("time {{ hours: \"{h_word}\" minutes: \"{m_word}\" seconds: \"{s_word}\"{zone_field} }}")
                                });
                            }
                        }
                    }

                    // 2c. HH:MM (weight 0.3)
                    let end_clock = pos_after_m;
                    let tz_info = check_tz(&remaining[end_clock..]);
                    let (end, zone_field, tz_weight) = match &tz_info {
                        Some((len, z)) => (
                            start + end_clock + len,
                            crate::lang::ur::lazy::tag(|| format!(" zone: \"{z}\"")),
                            cost(-0.05),
                        ),
                        None => (start + end_clock, String::new(), 0),
                    };
                    let c = cost(1.1) + cost(0.3) + tz_weight;
                    sink.push(end, c, 0, move || {
                        format!("time {{ hours: \"{h_word}\" minutes: \"{m_word}\"{zone_field} }}")
                    });
                }
            }
        }
    }

    // 3. 12-hour clock with AM / PM: "10 am", "10:30 am", "10:00 am", "10:30:30 am"
    if (1..=12).contains(&h_val) {
        if let Some(h_word) = lookup_hour(&h_str) {
            // Can have :MM[:SS] or :00 or nothing after H
            let mut sub_clock_options = [(0, None, None); 4];
            let mut option_count = 1;
            sub_clock_options[0] = (h_len, None, None);

            if h_len < remaining.len() && remaining[h_len] == ':' {
                let after_colon = &remaining[h_len + 1..];
                if after_colon.starts_with(&['0', '0']) {
                    sub_clock_options[option_count] = (h_len + 3, None, None);
                    option_count += 1;
                }
                if after_colon.len() >= 2 && after_colon[..2].iter().all(|c| c.is_ascii_digit()) {
                    let m_bytes = [after_colon[0] as u8, after_colon[1] as u8];
                    let m_str = std::str::from_utf8(&m_bytes).unwrap();
                    if let Some(m_word) = lookup_minute(&m_str) {
                        sub_clock_options[option_count] = (h_len + 3, Some(m_word), None);
                        option_count += 1;
                        // with seconds
                        if after_colon.len() >= 5
                            && after_colon[2] == ':'
                            && after_colon[3..5].iter().all(|c| c.is_ascii_digit())
                        {
                            let s_bytes = [after_colon[3] as u8, after_colon[4] as u8];
                            let s_str = std::str::from_utf8(&s_bytes).unwrap();
                            if let Some(s_word) = lookup_second(&s_str) {
                                sub_clock_options[option_count] = (h_len + 6, Some(m_word), Some(s_word));
                                option_count += 1;
                            }
                        }
                    }
                }
            }

            for &(clock_len, minute, second) in &sub_clock_options[..option_count] {
                let after_clock = &remaining[clock_len..];
                let (sp_len, marker_rem) =
                    if after_clock.starts_with(&[' ']) { (1, &after_clock[1..]) } else { (0, after_clock) };

                // Check AM / PM
                for (is_pm, markers) in [(false, AM_MARKERS), (true, PM_MARKERS)] {
                    for &marker in markers {
                        let marker_len = marker.len();
                        if marker_rem.len() >= marker_len && marker_rem[..marker_len].iter().copied().eq(marker.chars())
                        {
                            let pos_after_marker = clock_len + sp_len + marker_len;
                            let period = day_period(h_val, is_pm);
                            let tz_info = check_tz(&remaining[pos_after_marker..]);
                            let (end, zone_field, tz_weight) = match &tz_info {
                                Some((len, z)) => (
                                    start + pos_after_marker + len,
                                    crate::lang::ur::lazy::tag(|| format!(" zone: \"{z}\"")),
                                    cost(-0.05),
                                ),
                                None => (start + pos_after_marker, String::new(), 0),
                            };
                            let c = cost(1.1) + tz_weight;
                            sink.push(end, c, 0, move || {
                                let ms_tag = match (minute, second) {
                                    (Some(m), Some(s)) => format!("minutes: \"{m}\" seconds: \"{s}\" "),
                                    (Some(m), None) => format!("minutes: \"{m}\" "),
                                    _ => String::new(),
                                };
                                format!("time {{ suffix: \"{period}\" hours: \"{h_word}\" {ms_tag}{zone_field} }}")
                            });
                        }
                    }
                }
            }
        }
    }
}

fn spoken_base(text: &[char], start: usize, candidate: &crate::lattice::Candidate) -> Option<String> {
    let mut scratch = Vec::new();
    let mut sink = Sink { out: &mut scratch, target: Some((candidate.end, candidate.cost, 0)), found: None };
    crate::lang::ur::lazy::with_build(true, || classify_base(text, start, &mut sink));
    let tag = sink.found?;
    if tag.contains(" zone: ") {
        return None;
    }
    let wrapped = format!("tokens {{ {tag} }}");
    let parsed = crate::tag::parse(&wrapped).ok()?;
    let [(_, crate::tag::Value::Dict(body))] = parsed.first()?.as_slice() else { return None };
    crate::lang::ur::verbalize::verbalize(body)
}

pub fn classify(text: &[char], start: usize, sink: &mut Sink) {
    if !text.get(start).is_some_and(char::is_ascii_digit) {
        return;
    }
    classify_base(text, start, sink);
    for dash in start..text.len() {
        if !matches!(text[dash], '-' | '–') {
            continue;
        }
        let left_end = if dash > start && text[dash - 1] == ' ' { dash - 1 } else { dash };
        let right_start = if left_end != dash && text.get(dash + 1) == Some(&' ') {
            dash + 2
        } else if left_end == dash {
            dash + 1
        } else {
            continue;
        };
        if left_end <= start || right_start >= text.len() {
            continue;
        }
        let mut left = Vec::new();
        classify_base(&text[..left_end], start, &mut Sink { out: &mut left, target: None, found: None });
        for left_candidate in left.iter().filter(|c| c.end == left_end) {
            let Some(left_spoken) = spoken_base(&text[..left_end], start, left_candidate) else { continue };
            for right_end in right_start + 1..=text.len() {
                let mut right = Vec::new();
                classify_base(
                    &text[..right_end],
                    right_start,
                    &mut Sink { out: &mut right, target: None, found: None },
                );
                for right_candidate in right.iter().filter(|c| c.end == right_end) {
                    let Some(right_spoken) = spoken_base(&text[..right_end], right_start, right_candidate) else {
                        continue;
                    };
                    let c = left_candidate.cost + right_candidate.cost - cost(1.1) + cost(-0.05);
                    sink.push(right_end, c, 0, || {
                        format!("time {{ morphosyntactic_features: \"{} سے {}\" }}", left_spoken, right_spoken)
                    });
                    if let Some((zone_len, zone)) = check_tz(&text[right_end..]) {
                        sink.push(right_end + zone_len, c, 0, || {
                            format!(
                                "time {{ morphosyntactic_features: \"{} سے {}\" zone: \"{}\" }}",
                                left_spoken, right_spoken, zone
                            )
                        });
                    }
                }
            }
        }
    }
}
