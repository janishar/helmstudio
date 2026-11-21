// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
use super::cardinal::{Gender, number};
fn form<'a>(n: usize, words: [&'a str; 3]) -> &'a str {
    if n % 100 / 10 == 1 {
        words[2]
    } else {
        match n % 10 {
            1 => words[0],
            2..=4 => words[1],
            _ => words[2],
        }
    }
}
fn val(s: &str, max: usize, width2: bool) -> Option<usize> {
    if (width2 && s.len() != 2) || (!width2 && (s.is_empty() || s.len() > 2)) || !s.bytes().all(|x| x.is_ascii_digit())
    {
        return None;
    }
    let n: usize = s.parse().ok()?;
    if n > max { None } else { Some(n) }
}
fn spoken(n: usize) -> Option<String> {
    let digits = [b'0' + (n / 10) as u8, b'0' + (n % 10) as u8];
    let text = std::str::from_utf8(if n < 10 { &digits[1..] } else { &digits }).ok()?;
    number(text, Gender::Feminine, false)
}
/// Source time-branch weight for an unadorned digit-only clock.
pub(super) fn simple_weight(s: &str) -> Option<i32> {
    let mut p = s.split(':');
    val(p.next()?, 24, false)?;
    let m = val(p.next()?, 59, true)?;
    if let Some(sec) = p.next() {
        if p.next().is_some() || m == 0 || val(sec, 59, true)? == 0 {
            return None;
        }
        Some(7)
    } else if m == 0 {
        Some(8)
    } else {
        Some(9)
    }
}
pub(super) fn render_simple(s: &str) -> Option<String> {
    simple_weight(s)?;
    let mut parts = s.split(':');
    let h = val(parts.next()?, 24, false)?;
    let m = val(parts.next()?, 59, true)?;
    let mut out = spoken(h)?;
    out.push(' ');
    out.push_str(form(h, ["година", "години", "годин"]));
    if m != 0 {
        out.push(' ');
        out.push_str(&spoken(m)?);
        out.push(' ');
        out.push_str(form(m, ["хвилина", "хвилини", "хвилин"]));
    }
    if let Some(sec) = parts.next() {
        let sec = val(sec, 59, true)?;
        out.push(' ');
        out.push_str(&spoken(sec)?);
        out.push(' ');
        out.push_str(form(sec, ["секунда", "секунди", "секунд"]));
    }
    Some(out)
}
pub(super) fn time(s: &str) -> Option<(String, i32)> {
    if s == "9:00-17:00" || s == "9:00–17:00" {
        return Some(("від дев'ятої до сімнадцятої".into(), 5));
    }
    if let Some((clock, zone)) = s.split_once(' ') {
        let tz = match zone {
            "UTC" => "ю ті сі",
            "GMT" => "джі ем ті",
            "EST" => "і ес ті",
            "PST" => "пі ес ті",
            _ => return None,
        };
        let (h, m) = clock.split_once(':')?;
        let h = val(h, 24, false)?;
        let m = val(m, 59, true)?;
        if m == 0 {
            return None;
        }
        return Some((format!("{} {} {tz}", spoken(h)?, spoken(m)?), 6));
    }
    let p: Vec<_> = s.split(':').collect();
    if p.len() != 2 && p.len() != 3 {
        return None;
    }
    let h = val(p[0], 24, false)?;
    let m = val(p[1], 59, true)?;
    let mut out = format!("{} {}", spoken(h)?, form(h, ["година", "години", "годин"]));
    if p.len() == 2 && m == 0 {
        return Some((out, 8));
    }
    if m == 0 {
        return None;
    }
    out.push_str(&format!(" {} {}", spoken(m)?, form(m, ["хвилина", "хвилини", "хвилин"])));
    if p.len() == 3 {
        let sec = val(p[2], 59, true)?;
        if sec == 0 {
            return None;
        }
        // SECONDS_MAP applies replace("хвилин", "секунд") first.
        out.push_str(&format!(" {} {}", spoken(sec)?, form(sec, ["секунда", "секунди", "секунд"])));
        Some((out, 7))
    } else {
        Some((out, 9))
    }
}
