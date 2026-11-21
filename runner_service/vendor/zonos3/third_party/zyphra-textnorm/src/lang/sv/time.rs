//! Swedish time classifier: 24h clock, 12h clock with suffix/zone, "klockan", and time ranges.

use super::number::under_hundred;

crate::tsv!(SUFFIX, "sv/time/suffix.tsv");
crate::tsv!(TIME_ZONE, "sv/time/time_zone.tsv");

/// Match suffix (e.m., f.m., am, pm, etc.) at `text[start..]`. Returns `(spoken, len)`.
pub fn match_time_suffix(text: &[char], start: usize) -> Option<(&str, usize)> {
    let rem = &text[start..];
    SUFFIX.prefix_matches(rem, 0).filter(|(_, end)| rem.get(*end).map_or(true, |c| !c.is_alphabetic())).fold(
        None,
        |best, (row, end)| match best {
            Some((_, previous)) if previous >= end => best,
            _ => Some((row.value.as_str(), end)),
        },
    )
}

/// Match timezone (est, cet, utc, etc.) at `text[start..]`. Returns `(spoken, len)`.
pub fn match_time_zone(text: &[char], start: usize) -> Option<(&str, usize)> {
    let rem = &text[start..];
    TIME_ZONE.prefix_matches(rem, 0).filter(|(_, end)| rem.get(*end).map_or(true, |c| !c.is_alphabetic())).fold(
        None,
        |best, (row, end)| match best {
            Some((_, previous)) if previous >= end => best,
            _ => Some((row.value.as_str(), end)),
        },
    )
}

#[derive(Clone, Copy)]
struct TimeRecipe<'a> {
    end: usize,
    prefix: &'static str,
    hour: u32,
    minute: u32,
    second: Option<u32>,
    suffix: Option<&'a str>,
    zone: Option<&'a str>,
}

impl TimeRecipe<'_> {
    fn render(self) -> String {
        fn append_number(out: &mut String, n: u32) {
            if n == 0 {
                out.push_str("noll");
            } else {
                under_hundred(out, n as u8, false);
            }
        }
        let mut tag = String::with_capacity(96);
        tag.push_str("time { hours: \"");
        tag.push_str(self.prefix);
        append_number(&mut tag, self.hour);
        tag.push_str("\" minutes: \"");
        append_number(&mut tag, self.minute);
        tag.push('"');
        if let Some(second) = self.second {
            tag.push_str(" seconds: \"");
            append_number(&mut tag, second);
            tag.push('"');
        }
        if let Some(suffix) = self.suffix {
            tag.push_str(" suffix: \"");
            tag.push_str(suffix);
            tag.push('"');
        }
        if let Some(zone) = self.zone {
            tag.push_str(" zone: \"");
            tag.push_str(zone);
            tag.push('"');
        }
        tag.push_str(" }");
        tag
    }
}

fn single_time(text: &[char], start: usize) -> Option<TimeRecipe<'_>> {
    let rem = &text[start..];
    let (mut pos, prefix) = if rem.starts_with(&['k', 'l', 'o', 'c', 'k', 'a', 'n', ' ', 'ä', 'r']) {
        (10, "klockan är ")
    } else if rem.starts_with(&['k', 'l', 'o', 'c', 'k', 'a', 'n']) {
        (7, "klockan ")
    } else if rem.starts_with(&['k', 'l', '.']) {
        (3, "klockan ")
    } else if rem.starts_with(&['k', 'l']) {
        (2, "klockan ")
    } else {
        (0, "")
    };
    if !prefix.is_empty() {
        while pos < rem.len() && crate::text::is_nemo_space(rem[pos]) {
            pos += 1;
        }
    }
    let h_len = rem[pos..].iter().take_while(|c| c.is_ascii_digit()).count();
    if h_len == 0 || h_len > 2 {
        return None;
    }
    let hour = rem[pos..pos + h_len].iter().fold(0u32, |n, c| n * 10 + (*c as u8 - b'0') as u32);
    if hour >= 24 {
        return None;
    }
    pos += h_len;
    if matches!(rem.get(pos), Some(':' | '.')) {
        let sep = rem[pos];
        let after = &rem[pos + 1..];
        let len = after.iter().take_while(|c| c.is_ascii_digit()).count();
        if len == 2 {
            let minute = after[..2].iter().fold(0u32, |n, c| n * 10 + (*c as u8 - b'0') as u32);
            if minute < 60 {
                pos += 3;
                let seconds_start = pos;
                let mut second = None;
                if matches!(rem.get(pos), Some(':' | '.')) && (prefix.is_empty() || rem[pos] == sep) {
                    let after = &rem[pos + 1..];
                    if after.iter().take_while(|c| c.is_ascii_digit()).count() == 2 {
                        let value = after[..2].iter().fold(0u32, |n, c| n * 10 + (*c as u8 - b'0') as u32);
                        if value < 60 {
                            pos += 3;
                            second = Some(value);
                        }
                    }
                }
                let (suffix, zone) = parse_suffix_and_zone(rem, &mut pos, !prefix.is_empty());
                if second.is_some()
                    && prefix.is_empty()
                    && suffix.is_none()
                    && zone.is_none()
                    && (sep != ':' || rem[seconds_start] != ':')
                {
                    pos = seconds_start;
                    second = None;
                }
                return Some(TimeRecipe { end: start + pos, prefix, hour, minute, second, suffix, zone });
            }
        }
    }
    let (suffix, zone) = parse_suffix_and_zone(rem, &mut pos, !prefix.is_empty());
    if prefix.is_empty() && suffix.is_none() && zone.is_none() {
        return None;
    }
    Some(TimeRecipe { end: start + pos, prefix, hour, minute: 0, second: None, suffix, zone })
}

pub(super) fn classify(text: &[char], start: usize, sink: &mut super::Sink) {
    let Some(first) = single_time(text, start) else { return };
    let mut pos = first.end;
    if text.get(pos) == Some(&' ') {
        pos += 1;
    }
    if matches!(text.get(pos), Some('-' | '–' | '—')) {
        pos += 1;
        if text.get(pos) == Some(&' ') {
            pos += 1;
        }
        if let Some(second) = single_time(text, pos) {
            sink.push(second.end, crate::text::cost(1.09), 300, || {
                format!("{} }} tokens {{ name: \"till\" }} tokens {{ {}", first.render(), second.render())
            });
        }
    }
    sink.push(first.end, crate::text::cost(1.10), 400, || first.render());
}

fn parse_suffix_and_zone<'a>(rem: &'a [char], pos: &mut usize, prefixed: bool) -> (Option<&'a str>, Option<&'a str>) {
    let mut suffix_start = *pos;
    while suffix_start < rem.len() && crate::text::is_nemo_space(rem[suffix_start]) {
        suffix_start += 1;
    }
    let suffix = match_time_suffix(rem, suffix_start);
    let sfx_opt = suffix.map(|(sfx, len)| {
        *pos = suffix_start + len;
        sfx
    });
    let zone_start = if sfx_opt.is_some() || prefixed {
        if rem.get(*pos) == Some(&' ') { Some(*pos + 1) } else { None }
    } else {
        Some(suffix_start)
    };
    let zone_opt = zone_start.and_then(|at| {
        match_time_zone(rem, at).map(|(zone, len)| {
            *pos = at + len;
            zone
        })
    });
    (sfx_opt, zone_opt)
}
