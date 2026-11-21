//! Arabic time classifier and verbalizer.
//! Matching `ar/taggers/time.py` and `ar/verbalizers/time.py`.

use super::char_util::{char_at, token_end, token_start};
use crate::lang::common::{Fields, Punctuation};
use crate::lattice::{Candidate, Classifier};
use crate::tag::Dict;
use crate::text::cost;
use std::sync::LazyLock;

crate::tsv!(HOURS, "ar/time/hours.tsv");
crate::tsv!(MINUTES, "ar/time/minutes.tsv");
crate::tsv!(SECONDS, "ar/time/seconds.tsv");

const TIMEZONES: &[(&str, &str)] = &[
    ("UTC", "بالتوقيت العالمي المنسق"),
    ("GMT", "بتوقيت غرينتش"),
    ("EST", "بتوقيت شرق أمريكا"),
    ("EDT", "بالتوقيت الصيفي لشرق أمريكا"),
    ("CST", "بتوقيت وسط أمريكا"),
    ("CDT", "بالتوقيت الصيفي لوسط أمريكا"),
    ("MST", "بتوقيت الجبال"),
    ("MDT", "بالتوقيت الصيفي للجبال"),
    ("PST", "بتوقيت المحيط الهادئ"),
    ("PDT", "بالتوقيت الصيفي للمحيط الهادئ"),
    ("CEST", "بالتوقيت الصيفي لوسط أوروبا"),
    ("CET", "بتوقيت وسط أوروبا"),
    ("AST", "بالتوقيت العربي"),
    ("GST", "بتوقيت الخليج"),
];

const OFFSET_NUMS: &[&str] =
    &["", "واحد", "اثنين", "ثلاثة", "أربعة", "خمسة", "ستة", "سبعة", "ثمانية", "تسعة", "عشرة", "أحد عشر", "اثني عشر"];

static HOUR_WORDS: LazyLock<[Option<&'static str>; 24]> = LazyLock::new(|| {
    std::array::from_fn(|n| {
        let key = n.to_string();
        HOURS.rows().iter().find(|r| r.key == key).map(|r| r.value.as_str())
    })
});
static MINUTE_WORDS: LazyLock<[Option<&'static str>; 60]> = LazyLock::new(|| {
    std::array::from_fn(|n| {
        let key = format!("{n:02}");
        MINUTES.rows().iter().find(|r| r.key == key).map(|r| r.value.as_str())
    })
});
static SECOND_WORDS: LazyLock<[Option<&'static str>; 60]> = LazyLock::new(|| {
    std::array::from_fn(|n| {
        let key = format!("{n:02}");
        SECONDS.rows().iter().find(|r| r.key == key).map(|r| r.value.as_str())
    })
});

pub fn get_hour_word(h: u32) -> &'static str {
    HOUR_WORDS.get(h as usize).copied().flatten().expect("recognized hour")
}

pub fn get_minute_word(m: u32) -> Option<&'static str> {
    MINUTE_WORDS.get(m as usize).copied().flatten()
}

pub fn get_second_word(s: u32) -> Option<&'static str> {
    SECOND_WORDS.get(s as usize).copied().flatten()
}

pub struct TimeClassifier<'a> {
    pub punct: &'a Punctuation,
}

impl Classifier for TimeClassifier<'_> {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if !token_start(text, start, self.punct) {
            return;
        }

        let digit = |i: usize| {
            text.get(i).map(|_| char_at(text, i)).filter(char::is_ascii_digit).map(|c| c as u32 - '0' as u32)
        };
        let Some(first) = digit(start) else { return };
        let mut pos = start + 1;
        let mut hour = first;
        if let Some(second) = digit(pos) {
            hour = hour * 10 + second;
            pos += 1;
        }
        if hour >= 24 || text.get(pos) != Some(&':') {
            return;
        }
        let (Some(m1), Some(m2)) = (digit(pos + 1), digit(pos + 2)) else { return };
        if m1 * 10 + m2 >= 60 {
            return;
        }
        let hm_end = pos + 3;
        let mut emit = |end: usize, rank: i32| {
            let c = if rank == 1 { cost(1.0) } else { cost(1.1) };
            if token_end(text, end, self.punct) {
                out.push(Candidate { end, cost: c, rank, tag: String::new() });
            }
            if text.get(end) != Some(&' ') {
                return;
            }
            let zone_start = end + 1;
            for &(zone, _) in TIMEZONES {
                let n = zone.chars().count();
                if zone_start + n > text.len() || !text[zone_start..zone_start + n].iter().copied().eq(zone.chars()) {
                    continue;
                }
                let zone_end = zone_start + n;
                if token_end(text, zone_end, self.punct) {
                    out.push(Candidate { end: zone_end, cost: c, rank, tag: String::new() });
                }
                if matches!(text.get(zone_end), Some('+' | '-')) {
                    let off_start = zone_end + 1;
                    if let Some(first) = digit(off_start) {
                        if first != 0 && token_end(text, off_start + 1, self.punct) {
                            out.push(Candidate { end: off_start + 1, cost: c, rank, tag: String::new() });
                        }
                        if first == 1 {
                            if let Some(second) = digit(off_start + 1) {
                                if second <= 2 && token_end(text, off_start + 2, self.punct) {
                                    out.push(Candidate { end: off_start + 2, cost: c, rank, tag: String::new() });
                                }
                            }
                        }
                    }
                }
            }
        };
        emit(hm_end, 0);
        if (1..=12).contains(&hour) {
            for suffix in ["ص", "م", "am", "AM", "a.m.", "pm", "PM", "p.m."] {
                for space in 0..=1 {
                    if space == 1 && text.get(hm_end) != Some(&' ') {
                        continue;
                    }
                    let s = hm_end + space;
                    let end = s + suffix.chars().count();
                    if end <= text.len() && text[s..end].iter().copied().eq(suffix.chars()) {
                        emit(end, 1);
                    }
                }
            }
        }
        if text.get(hm_end) == Some(&':') {
            if let (Some(s1), Some(s2)) = (digit(hm_end + 1), digit(hm_end + 2)) {
                if s1 * 10 + s2 < 60 {
                    emit(hm_end + 3, 0);
                }
            }
        }
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        let mut idx = start;
        let h_start = idx;
        while idx < candidate.end && char_at(text, idx).is_ascii_digit() {
            idx += 1;
        }
        let h_val = (h_start..idx).fold(0u32, |n, i| n * 10 + (char_at(text, i) as u32 - '0' as u32));
        idx += 1; // skip ':'
        let m_start = idx;
        while idx < candidate.end && char_at(text, idx).is_ascii_digit() {
            idx += 1;
        }
        let m_val = (m_start..idx).fold(0u32, |n, i| n * 10 + (char_at(text, i) as u32 - '0' as u32));

        let mut s_val: Option<u32> = None;
        if idx < candidate.end && text[idx] == ':' {
            let s_start = idx + 1;
            idx += 1;
            while idx < candidate.end && char_at(text, idx).is_ascii_digit() {
                idx += 1;
            }
            let s = (s_start..idx).fold(0u32, |n, i| n * 10 + (char_at(text, i) as u32 - '0' as u32));
            s_val = Some(s);
        }

        let time_num_end = idx;
        let mut suf_idx = time_num_end;
        if suf_idx < candidate.end && text[suf_idx] == ' ' {
            suf_idx += 1;
        }
        let mut suf_word: Option<&'static str> = None;
        if suf_idx < candidate.end {
            let rest: String = text[suf_idx..candidate.end].iter().collect();
            if rest.starts_with('ص') || rest.starts_with("am") || rest.starts_with("AM") || rest.starts_with("a.m.") {
                suf_word = Some("صباحًا");
            } else if rest.starts_with('م')
                || rest.starts_with("pm")
                || rest.starts_with("PM")
                || rest.starts_with("p.m.")
            {
                suf_word = Some("مساءً");
            }
        }

        // 24-hour default period if no explicit suffix
        let period_24 = if suf_word.is_some() {
            suf_word
        } else if h_val >= 13 {
            Some("مساءً")
        } else if h_val == 0 {
            Some("ليلًا")
        } else {
            None
        };

        // Hours & minutes calculation:
        // In HH:MM, :15 is والربع, :30 is والنصف, :45 is إلا ربعًا with hour incremented by 1 (mod 12 or 12)!
        let (display_h, min_spoken) = if s_val.is_none() && m_val == 45 {
            let base_12 = h_val % 12;
            let next_12 = (base_12 % 12) + 1;
            (next_12, Some("إلا ربعًا"))
        } else {
            let base_12 = if h_val % 12 == 0 { 12 } else { h_val % 12 };
            let min_s = if m_val == 0 {
                None
            } else if s_val.is_none() && m_val == 15 {
                Some("والربع")
            } else if s_val.is_none() && m_val == 30 {
                Some("والنصف")
            } else {
                get_minute_word(m_val)
            };
            (base_12, min_s)
        };

        let h_spoken = get_hour_word(display_h);
        let sec_spoken = s_val.and_then(|s| if s == 0 { None } else { get_second_word(s) });

        // Check zone
        let mut zone_spoken: Option<String> = None;
        if time_num_end < candidate.end {
            let rest_after_time: String = text[time_num_end..candidate.end].iter().collect();
            for &(tz, tz_name) in TIMEZONES {
                if let Some(pos) = rest_after_time.find(tz) {
                    let mut z = tz_name.to_string();
                    let after_tz = &rest_after_time[pos + tz.len()..];
                    if after_tz.starts_with('+') || after_tz.starts_with('-') {
                        let sign = if after_tz.starts_with('+') { " زائد " } else { " ناقص " };
                        let off_str: String = after_tz[1..].chars().take_while(|c| c.is_ascii_digit()).collect();
                        let off_val: usize = off_str.parse().unwrap_or(0);
                        if off_val >= 1 && off_val <= 12 {
                            z.push_str(sign);
                            z.push_str(OFFSET_NUMS[off_val]);
                        }
                    }
                    zone_spoken = Some(z);
                    break;
                }
            }
        }

        let mut tag = String::with_capacity(128);
        tag.push_str("time { hours: \"");
        tag.push_str(h_spoken);
        tag.push('"');
        if let Some(m) = min_spoken {
            tag.push_str(" minutes: \"");
            tag.push_str(m);
            tag.push('"');
        }
        if let Some(s) = sec_spoken {
            tag.push_str(" seconds: \"");
            tag.push_str(s);
            tag.push('"');
        }
        if let Some(p) = period_24 {
            tag.push_str(" suffix: \"");
            tag.push_str(p);
            tag.push('"');
        }
        if let Some(z) = zone_spoken {
            tag.push_str(" zone: \"");
            tag.push_str(&z);
            tag.push('"');
        }
        tag.push_str(" preserve_order: true }");
        candidate.tag = tag;
    }
}

pub fn verbalize(fields: &Dict) -> Option<String> {
    let mut f = Fields::new(fields);
    let hours = f.take("hours")?;
    let minutes = f.take("minutes");
    let seconds = f.take("seconds");
    let suffix = f.take("suffix");
    let zone = f.take("zone");
    while f.preserve_order() {}
    if !f.done() || hours.is_empty() || [minutes, seconds, suffix, zone].into_iter().flatten().any(str::is_empty) {
        return None;
    }

    let mut out = String::with_capacity(64);
    out.push_str("الساعة ");
    out.push_str(hours);
    if let Some(m) = minutes {
        if !m.is_empty() {
            out.push(' ');
            out.push_str(m);
        }
    }
    if let Some(s) = seconds {
        if !s.is_empty() {
            out.push(' ');
            out.push_str(s);
        }
    }
    if let Some(suf) = suffix {
        if !suf.is_empty() {
            out.push(' ');
            out.push_str(suf);
        }
    }
    if let Some(z) = zone {
        if !z.is_empty() {
            out.push(' ');
            out.push_str(z);
        }
    }
    Some(out.replace('\u{a0}', " "))
}
