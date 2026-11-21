// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Vietnamese time classifier: digital (H:M, H:M:S), word forms, abbreviations (Hh, Hg, Mp, Ss), zones.

use super::super::graph_utils::{SpokenNumber, WEIGHT_TIME};
use crate::lattice::{Candidate, Classifier};

pub struct Time;

crate::labels!(TIME_ZONES_LABELS, "vi/time/time_zones.tsv");

static ZONE_INDEX: std::sync::LazyLock<std::collections::HashMap<char, Vec<&'static Vec<String>>>> =
    std::sync::LazyLock::new(|| {
        let mut index: std::collections::HashMap<char, Vec<&'static Vec<String>>> = std::collections::HashMap::new();
        for row in TIME_ZONES_LABELS.iter() {
            if let Some(c) = row.first().and_then(|s| s.chars().next()) {
                index.entry(c).or_default().push(row);
            }
        }
        index
    });

struct TimeNumber(u32);
impl std::fmt::Display for TimeNumber {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        let digits = [b'0' + (self.0 / 10) as u8, b'0' + (self.0 % 10) as u8];
        let digits = if self.0 < 10 { &digits[1..] } else { &digits[..] };
        write!(f, "{}", SpokenNumber { digits, grouped: false })
    }
}

fn parse_two_digit(text: &[char], start: usize, max_val: u32) -> impl Iterator<Item = (usize, u32)> {
    let mut found = [None, None];
    if start < text.len() && text[start].is_ascii_digit() {
        let d1 = (text[start] as u8 - b'0') as u32;
        found[0] = Some((start + 1, d1));
        if start + 1 < text.len() && text[start + 1].is_ascii_digit() {
            let val = d1 * 10 + (text[start + 1] as u8 - b'0') as u32;
            if val <= max_val {
                found[1] = Some((start + 2, val));
            }
        }
    }
    found.into_iter().flatten()
}

fn zones(text: &[char], start: usize) -> impl Iterator<Item = (usize, &'static str)> + '_ {
    let pos = start + usize::from(text.get(start) == Some(&' '));
    let rows = text.get(pos).and_then(|c| ZONE_INDEX.get(c)).map_or(&[][..], Vec::as_slice);
    rows.iter().filter_map(move |row| {
        let key = row.first()?;
        let value = row.get(1)?;
        let end = pos + key.chars().count();
        if text.get(pos..end)?.iter().copied().eq(key.chars()) { Some((end, value.as_str())) } else { None }
    })
}

impl Time {
    pub fn scan_spans(text: &[char], start: usize, f: impl FnMut(usize, String)) {
        Self::scan(text, start, true, None, f);
    }

    fn scan(text: &[char], start: usize, render: bool, target: Option<usize>, mut f: impl FnMut(usize, String)) {
        let found = std::cell::Cell::new(false);
        macro_rules! emit {
            ($end:expr, $body:expr) => {{
                let end = $end;
                if !found.get() && target.is_none_or(|wanted| wanted == end) {
                    f(end, $body);
                    if target.is_some() {
                        found.set(true);
                    }
                }
            }};
        }
        macro_rules! format {
            ($($arg:tt)*) => { if render { std::format!($($arg)*) } else { String::new() } };
        }
        if start >= text.len() || !text[start].is_ascii_digit() {
            return;
        }

        // 1. Hours-led patterns
        for (h_end, h_val) in parse_two_digit(text, start, 24) {
            let h_str = TimeNumber(h_val);
            {
                // 1a. Digital format: H:M [:S]
                if h_end < text.len() && text[h_end] == ':' {
                    for (m_end, m_val) in parse_two_digit(text, h_end + 1, 59) {
                        let m_str = TimeNumber(m_val);
                        {
                            // H:M:S
                            if m_end < text.len() && text[m_end] == ':' {
                                for (s_end, s_val) in parse_two_digit(text, m_end + 1, 59) {
                                    let s_str = TimeNumber(s_val);
                                    {
                                        emit!(
                                            s_end,
                                            format!(
                                                "hours: \"{}\" minutes: \"{}\" seconds: \"{}\" preserve_order: true",
                                                h_str, m_str, s_str
                                            )
                                        );
                                        for (z_end, z_val) in zones(text, s_end) {
                                            emit!(
                                                z_end,
                                                format!(
                                                    "hours: \"{}\" minutes: \"{}\" seconds: \"{}\" zone: \"{}\" preserve_order: true",
                                                    h_str, m_str, s_str, z_val
                                                )
                                            );
                                        }
                                    }
                                }
                            }

                            // H:M
                            emit!(m_end, format!("hours: \"{}\" minutes: \"{}\"", h_str, m_str));
                            for (z_end, z_val) in zones(text, m_end) {
                                emit!(
                                    z_end,
                                    format!("hours: \"{}\" minutes: \"{}\" zone: \"{}\"", h_str, m_str, z_val)
                                );
                            }
                        }
                    }
                }

                // 1b. Abbreviated format: Hh, Hg
                if h_end < text.len() && (text[h_end] == 'h' || text[h_end] == 'g') {
                    let s_end = h_end + 1;
                    // Hh alone
                    emit!(s_end, format!("hours: \"{}\"", h_str));
                    for (z_end, z_val) in zones(text, s_end) {
                        emit!(z_end, format!("hours: \"{}\" zone: \"{}\"", h_str, z_val));
                    }

                    // HhM or HgM (e.g. 14h30, 9g15, 14h30p)
                    for (m_end, m_val) in parse_two_digit(text, s_end, 59) {
                        let m_str = TimeNumber(m_val);
                        {
                            emit!(m_end, format!("hours: \"{}\" minutes: \"{}\"", h_str, m_str));
                            for (z_end, z_val) in zones(text, m_end) {
                                emit!(
                                    z_end,
                                    format!("hours: \"{}\" minutes: \"{}\" zone: \"{}\"", h_str, m_str, z_val)
                                );
                            }
                            if m_end < text.len() && text[m_end] == 'p' {
                                let end_with_p = m_end + 1;
                                emit!(end_with_p, format!("hours: \"{}\" minutes: \"{}\"", h_str, m_str));
                                for (z_end, z_val) in zones(text, end_with_p) {
                                    emit!(
                                        z_end,
                                        format!("hours: \"{}\" minutes: \"{}\" zone: \"{}\"", h_str, m_str, z_val)
                                    );
                                }
                            }
                        }
                    }
                }

                // 1c. Vietnamese word formats: H giờ [M phút] [S giây]
                const GIO: [char; 4] = [' ', 'g', 'i', 'ờ'];
                if h_end + 4 <= text.len() && text[h_end..h_end + 4] == GIO {
                    let gio_end = h_end + 4;
                    // H giờ alone
                    emit!(gio_end, format!("hours: \"{}\"", h_str));
                    for (z_end, z_val) in zones(text, gio_end) {
                        emit!(z_end, format!("hours: \"{}\" zone: \"{}\"", h_str, z_val));
                    }

                    // with M phút
                    if gio_end < text.len() && text[gio_end] == ' ' {
                        let m_start = gio_end + 1;
                        for (m_end, m_val) in parse_two_digit(text, m_start, 59) {
                            const PHUT: [char; 5] = [' ', 'p', 'h', 'ú', 't'];
                            if m_end + 5 <= text.len() && text[m_end..m_end + 5] == PHUT {
                                let phut_end = m_end + 5;
                                let m_str = TimeNumber(m_val);
                                {
                                    emit!(phut_end, format!("hours: \"{}\" minutes: \"{}\"", h_str, m_str));
                                    for (z_end, z_val) in zones(text, phut_end) {
                                        emit!(
                                            z_end,
                                            format!("hours: \"{}\" minutes: \"{}\" zone: \"{}\"", h_str, m_str, z_val)
                                        );
                                    }

                                    // with S giây
                                    if phut_end < text.len() && text[phut_end] == ' ' {
                                        let s_start = phut_end + 1;
                                        for (s_end, s_val) in parse_two_digit(text, s_start, 59) {
                                            const GIAY: [char; 5] = [' ', 'g', 'i', 'â', 'y'];
                                            if s_end + 5 <= text.len() && text[s_end..s_end + 5] == GIAY {
                                                let giay_end = s_end + 5;
                                                let s_str = TimeNumber(s_val);
                                                {
                                                    emit!(
                                                        giay_end,
                                                        format!(
                                                            "hours: \"{}\" minutes: \"{}\" seconds: \"{}\" preserve_order: true",
                                                            h_str, m_str, s_str
                                                        )
                                                    );
                                                    for (z_end, z_val) in zones(text, giay_end) {
                                                        emit!(
                                                            z_end,
                                                            format!(
                                                                "hours: \"{}\" minutes: \"{}\" seconds: \"{}\" zone: \"{}\" preserve_order: true",
                                                                h_str, m_str, s_str, z_val
                                                            )
                                                        );
                                                    }
                                                }
                                            }
                                        }
                                    }
                                }
                            }
                        }
                    }
                }
            }
        }

        // 2. Minutes-led or seconds-led formats (Mp, Ss, M phút, S giây)
        for (m_end, m_val) in parse_two_digit(text, start, 59) {
            let m_str = TimeNumber(m_val);
            {
                // Mp
                if m_end < text.len() && text[m_end] == 'p' {
                    let p_end = m_end + 1;
                    emit!(p_end, format!("minutes: \"{}\"", m_str));

                    // Mp Ss (e.g. 3p 20s or 3p20s)
                    let s_start = p_end;
                    for (s_end, s_val) in parse_two_digit(text, s_start, 59) {
                        if s_end < text.len() && text[s_end] == 's' {
                            let s_str = TimeNumber(s_val);
                            {
                                emit!(s_end + 1, format!("minutes: \"{}\" seconds: \"{}\"", m_str, s_str));
                            }
                        }
                    }
                }

                // Ss
                if m_end < text.len() && text[m_end] == 's' {
                    emit!(m_end + 1, format!("seconds: \"{}\"", m_str));
                }

                // M phút
                const PHUT: [char; 5] = [' ', 'p', 'h', 'ú', 't'];
                if m_end + 5 <= text.len() && text[m_end..m_end + 5] == PHUT {
                    let phut_end = m_end + 5;
                    emit!(phut_end, format!("minutes: \"{}\"", m_str));

                    // M phút S giây
                    if phut_end < text.len() && text[phut_end] == ' ' {
                        let s_start = phut_end + 1;
                        for (s_end, s_val) in parse_two_digit(text, s_start, 59) {
                            const GIAY: [char; 5] = [' ', 'g', 'i', 'â', 'y'];
                            if s_end + 5 <= text.len() && text[s_end..s_end + 5] == GIAY {
                                let s_str = TimeNumber(s_val);
                                {
                                    emit!(s_end + 5, format!("minutes: \"{}\" seconds: \"{}\"", m_str, s_str));
                                }
                            }
                        }
                    }
                }

                // S giây
                const GIAY: [char; 5] = [' ', 'g', 'i', 'â', 'y'];
                if m_end + 5 <= text.len() && text[m_end..m_end + 5] == GIAY {
                    emit!(m_end + 5, format!("seconds: \"{}\"", m_str));
                }
            }
        }
    }
}

impl Classifier for Time {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        Self::scan(text, start, false, None, |end, _| {
            out.push(Candidate { end, cost: WEIGHT_TIME, rank: 0, tag: String::new() });
        });
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        Self::scan(text, start, true, Some(candidate.end), |end, body| {
            if end == candidate.end && candidate.tag.is_empty() {
                candidate.tag = format!("time {{ {} }}", body);
            }
        });
    }
}
