// Copyright (c) 2026 Zyphra, Inc. Licensed under Apache-2.0.
use super::graph_utils::{Gender, number};
fn hour(s: &str) -> Option<String> {
    if s.is_empty() || s.len() > 2 || !s.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    let n: u8 = s.parse().ok()?;
    if n > 23 {
        return None;
    }
    Some(if n == 1 { "unu".to_owned() } else { number(&n.to_string(), Gender::Feminine)?.replace("una", "unu") })
}
fn minute_number(s: &str) -> Option<String> {
    if s.len() != 2 || !s.bytes().all(|b| b.is_ascii_digit()) || s.parse::<u8>().ok()? > 59 {
        return None;
    }
    number(s, Gender::Masculine)
}
fn minute(s: &str) -> Vec<String> {
    if s.len() != 2 || !s.bytes().all(|b| b.is_ascii_digit()) || s.parse::<u8>().ok().filter(|x| *x <= 59).is_none() {
        return Vec::new();
    }
    let mut out = Vec::new();
    let fixed = match s {
        "00" => Some(""),
        "01" => Some(" și un minut"),
        "02" => Some(" și două minute"),
        "03" => Some(" și trei minute"),
        "04" => Some(" și patru minute"),
        "05" => Some(" și cinci"),
        "10" => Some(" și zece"),
        "15" => Some(" și cincisprezece"),
        "20" => Some(" și douăzeci de minute"),
        "25" => Some(" și douăzeci și cinci de minute"),
        "30" => Some(" și treizeci de minute"),
        "35" => Some(" și treizeci și cinci de minute"),
        "40" => Some(" și patruzeci de minute"),
        "45" => Some(" și patruzeci și cinci de minute"),
        "50" => Some(" și cincizeci de minute"),
        "55" => Some(" și cincizeci și cinci de minute"),
        _ => None,
    };
    match s {
        "15" => out.push(" și un sfert".to_owned()),
        "30" => out.push(" și jumătate".to_owned()),
        _ => {}
    }
    if let Some(v) = fixed {
        out.push(v.to_owned());
    }
    if let Some(n) = minute_number(s) {
        out.push(format!(" și {} de minute", n));
    }
    out
}
fn core(s: &str, bare_zero: bool) -> Vec<String> {
    let Some((h, m)) = s.split_once(':') else {
        return Vec::new();
    };
    let minutes = minute(m);
    if minutes.is_empty() {
        return Vec::new();
    }
    let Some(h) = hour(h) else {
        return Vec::new();
    };
    let mut out = Vec::with_capacity(minutes.len() + usize::from(bare_zero && m == "00"));
    if bare_zero && m == "00" {
        out.push(h.clone());
    }
    out.extend(minutes.into_iter().map(|x| format!("{}{}", h, x)));
    out
}
/// Count the clock-plus-period readings without constructing their spoken forms.
pub fn period_readings(input: &str) -> Option<usize> {
    let (base, period_count) =
        [("am", 1), ("a.m.", 1), ("AM", 1), ("A.M.", 1), ("pm", 2), ("p.m.", 2), ("PM", 2), ("P.M.", 2)]
            .into_iter()
            .find_map(|(suffix, count)| input.strip_suffix(suffix).map(|base| (base, count)))?;
    let base = base.strip_suffix(' ').unwrap_or(base);
    let (hour, minute) = base.split_once(':')?;
    if hour.is_empty()
        || hour.len() > 2
        || !hour.bytes().all(|b| b.is_ascii_digit())
        || hour.parse::<u8>().ok()? > 23
        || minute.len() != 2
        || !minute.bytes().all(|b| b.is_ascii_digit())
        || minute.parse::<u8>().ok()? > 59
    {
        return None;
    }
    let mut count = usize::from(matches!(minute, "15" | "30"));
    count += usize::from(matches!(
        minute,
        "00" | "01" | "02" | "03" | "04" | "05" | "10" | "15" | "20" | "25" | "30" | "35" | "40" | "45" | "50" | "55"
    ));
    count += usize::from(super::graph_utils::parse_number(minute).is_some());
    (count != 0).then_some(count * period_count)
}

pub fn render_period(input: &str, index: usize) -> Option<String> {
    let (base, phrases): (&str, &[&str]) = [
        ("am", &["dimineața"][..]),
        ("a.m.", &["dimineața"][..]),
        ("AM", &["dimineața"][..]),
        ("A.M.", &["dimineața"][..]),
        ("pm", &["seara", "după-amiaza"][..]),
        ("p.m.", &["seara", "după-amiaza"][..]),
        ("PM", &["seara", "după-amiaza"][..]),
        ("P.M.", &["seara", "după-amiaza"][..]),
    ]
    .into_iter()
    .find_map(|(suffix, phrases)| input.strip_suffix(suffix).map(|base| (base, phrases)))?;
    let base = base.strip_suffix(' ').unwrap_or(base);
    let mut value = core(base, false).into_iter().nth(index / phrases.len())?;
    value.push(' ');
    value.push_str(phrases[index % phrases.len()]);
    Some(value)
}

pub fn time(s: &str) -> Vec<String> {
    let mut out = Vec::new();
    if s == "00:00" {
        out.push("miezul nopții".to_owned());
    }
    out.extend(core(s, false));
    for (i, c) in s.char_indices() {
        if matches!(c, '-' | '–' | '—' | '−') {
            let a = s[..i].strip_suffix(' ').unwrap_or(&s[..i]);
            let b = s[i + c.len_utf8()..].strip_prefix(' ').unwrap_or(&s[i + c.len_utf8()..]);
            for x in core(a, true) {
                for y in core(b, true) {
                    out.push(format!("{} la {}", x, y));
                }
            }
        }
    }
    for (suffix, phrases) in [
        ("am", &["dimineața"][..]),
        ("a.m.", &["dimineața"][..]),
        ("AM", &["dimineața"][..]),
        ("A.M.", &["dimineața"][..]),
        ("pm", &["seara", "după-amiaza"][..]),
        ("p.m.", &["seara", "după-amiaza"][..]),
        ("PM", &["seara", "după-amiaza"][..]),
        ("P.M.", &["seara", "după-amiaza"][..]),
    ] {
        if let Some(base) = s.strip_suffix(suffix) {
            let base = base.strip_suffix(' ').unwrap_or(base);
            let mut values = core(base, false);
            if let Some(h) = hour(base) {
                values.push(h);
            }
            for v in values {
                for p in phrases {
                    out.push(format!("{} {}", v, p));
                }
            }
        }
    }
    let p: Vec<_> = s.split(':').collect();
    if p.len() == 3 {
        if let (Some(h), Some(m)) = (hour(p[0]), minute_number(p[1])) {
            let mut seconds = Vec::new();
            match p[2] {
                "00" => seconds.push(String::new()),
                "15" => seconds.push(" și cincisprezece secunde".to_owned()),
                "30" => seconds.push(" și treizeci de secunde".to_owned()),
                _ => {}
            }
            if let Some(n) = minute_number(p[2]) {
                seconds.push(format!(" și {} de secunde", n));
            }
            for sec in seconds {
                out.push(format!("{} și {} minute{}", h, m, sec));
            }
        }
    }
    if let Some((clock, zone)) = s.split_once(' ') {
        for (name, spoken) in [("UTC", "U T C"), ("GMT", "G M T"), ("CET", "C E T"), ("EET", "E E T")] {
            if let Some(offset) = zone.strip_prefix(name) {
                let mut zones = Vec::new();
                if offset.is_empty() {
                    zones.push(spoken.to_owned());
                } else if let Some((sign, rest)) = offset.chars().next().and_then(|c| match c {
                    '-' | '−' => Some(("minus", &offset[c.len_utf8()..])),
                    '+' => Some(("plus", &offset[1..])),
                    _ => None,
                }) {
                    if let Some(n) = number(rest, Gender::Masculine) {
                        zones.push(format!("{} {} {}", spoken, sign, n));
                    }
                    if let Some((h, m)) = rest.split_once(':') {
                        if let (Some(h), Some(m)) = (hour(h), minute_number(m)) {
                            zones.push(format!("{} {} {} și {}", spoken, sign, h, m));
                        }
                    }
                }
                let mut clocks = Vec::new();
                if let Some((h, m)) = clock.split_once(':') {
                    if let Some(h) = hour(h) {
                        if m == "00" {
                            clocks.push(format!("{} fix", h));
                        }
                        if let Some(m) = minute_number(m) {
                            clocks.push(format!("{} și {}", h, m));
                        }
                    }
                }
                clocks.extend(core(clock, false));
                for a in clocks {
                    for b in &zones {
                        out.push(format!("{} {}", a, b));
                    }
                }
            }
        }
    }
    out
}
