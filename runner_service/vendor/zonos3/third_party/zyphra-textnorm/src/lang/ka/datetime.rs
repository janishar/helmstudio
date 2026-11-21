use super::{
    graph_utils::{cardinal, glue_suffix},
    lookup,
    numeric::split_suffix,
};
use crate::text::is_nemo_space;
const MONTHS: &str = include_str!("../../../data/ka/dates/month_number.tsv");
const NAMES: &str = include_str!("../../../data/ka/dates/month_name.tsv");
const HOURS: &str = include_str!("../../../data/ka/time/hours.tsv");
const MINUTES: &str = include_str!("../../../data/ka/time/minutes.tsv");

/// Date candidates either contain a numeric/era marker or a month from the
/// source table. A trailing four-digit year can be adjacent to the month.
pub fn may_be_date(s: &str) -> bool {
    if s.contains(['.', '/', '-', '–', '—', '֊', 'წ']) || s.contains("BC") || s.contains("AD") {
        return true;
    }
    s.split(' ').skip(1).any(|word| {
        if lookup(NAMES, word).is_some() {
            return true;
        }
        if word.len() < 4 {
            return false;
        }
        let split = word.len() - 4;
        word.as_bytes()[split..].iter().all(u8::is_ascii_digit)
            && lookup(NAMES, word[..split].trim_end_matches(is_nemo_space)).is_some()
    })
}

fn day(s: &str) -> Option<String> {
    let (base, suf) = split_suffix(s);
    if base.len() > 2 || !base.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    let n: usize = base.parse().ok()?;
    if !(1..=31).contains(&n) {
        return None;
    }
    let val = cardinal(&n.to_string())?;
    Some(suf.map_or(val.clone(), |x| glue_suffix(&val, x)))
}
fn year(s: &str) -> Option<String> {
    if s.len() != 4 {
        return None;
    }
    cardinal(s)
}
fn year_suffix(s: &str) -> (&str, Option<&str>) {
    for ending in [" წლის", " წელს", " წწ.", " წ.", " წელი", " წ"] {
        if let Some(a) = s.strip_suffix(ending) {
            return (a, Some(ending));
        }
    }
    (s, None)
}

pub fn date_tags(s: &str) -> Vec<String> {
    let mut out = Vec::new();
    let (s0, _) = year_suffix(s);
    for sep in ['.', '/', '-'] {
        let p: Vec<_> = s0.split(sep).collect();
        if p.len() != 3 {
            continue;
        }
        if split_suffix(p[0]).1.is_none() {
            if let (Some(d), Some(m), Some(y)) = (day(p[0]), lookup(MONTHS, p[1]), year(p[2])) {
                out.push(format!("date {{ day: \"{d}\" month: \"{m}\" year: \"{y}\" }}"));
            }
        }
        if sep != '/' && s0 == s && split_suffix(p[2]).1.is_none() {
            if let (Some(y), Some(m), Some(d)) = (year(p[0]), lookup(MONTHS, p[1]), day(p[2])) {
                out.push(format!("date {{ year: \"{y}\" month: \"{m}\" day: \"{d}\" }}"));
            }
        }
    }
    if let Some((a, b)) = s.split_once(' ') {
        if let Some((y, tail)) = b
            .strip_prefix("წლის")
            .or_else(|| b.strip_prefix("წელს"))
            .or_else(|| b.strip_prefix("წ."))
            .or_else(|| b.strip_prefix("წწ."))
            .or_else(|| b.strip_prefix("წელი"))
            .or_else(|| b.strip_prefix("წ"))
            .and_then(|tail| year(a).map(|y| (y, tail)))
        {
            let tail = tail.trim_start_matches(is_nemo_space);
            if let Some((d, m)) = tail.split_once(' ') {
                if let (Some(d), Some(m)) = (day(d), lookup(NAMES, m)) {
                    out.push(format!("date {{ year: \"{y}\" day: \"{d}\" month: \"{m}\" }}"));
                }
            }
        }
        if let Some(d) = day(a) {
            if let Some(m) = lookup(NAMES, b) {
                out.push(format!("date {{ day: \"{d}\" month: \"{m}\" }}"));
            }
            for line in NAMES.lines() {
                if let Some((key, m)) = line.split_once('\t') {
                    if let Some(rest) = b.strip_prefix(key) {
                        let rest = rest.trim_start_matches(is_nemo_space);
                        let (y, _) = year_suffix(rest);
                        if let Some(y) = year(y) {
                            out.push(format!("date {{ day: \"{d}\" month: \"{m}\" year: \"{y}\" }}"));
                        }
                    }
                }
            }
        }
    }
    for (marker, era) in [
        ("წლის", "წლის"),
        ("წელს", "წელს"),
        ("წ.", "წელს"),
        ("წელი", "წელი"),
        ("ძვ. წ.", "ჩვენს ერამდე"),
        ("ძვ.წ.", "ჩვენს ერამდე"),
        ("BC", "ჩვენს ერამდე"),
        ("B.C.", "ჩვენს ერამდე"),
        ("ახ. წ.", "ჩვენი ერით"),
        ("ახ.წ.", "ჩვენი ერით"),
        ("ჩვ. წ.", "ჩვენი ერით"),
        ("AD", "ჩვენი ერით"),
        ("A.D.", "ჩვენი ერით"),
    ] {
        if let Some(raw) = s.strip_suffix(marker) {
            let raw = raw.trim_end_matches(is_nemo_space);
            let y = if matches!(marker, "წლის" | "წელს" | "წ." | "წელი") {
                year(raw)
            } else if raw.len() <= 4 {
                cardinal(raw)
            } else {
                None
            };
            if let Some(y) = y {
                out.push(format!("date {{ year: \"{y}\" era: \"{era}\" }}"));
            }
        }
    }
    if let Some((i, c)) = s0.char_indices().find(|(_, c)| matches!(c, '-' | '–' | '—' | '֊')) {
        if let (Some(a), Some(b)) = (year(&s0[..i]), year(&s0[i + c.len_utf8()..])) {
            out.push(format!("date {{ year: \"{} {}\" }}", glue_suffix(&a, "დან"), glue_suffix(&b, "მდე")));
        }
    }
    out
}

/// Render the already-recognized two-field clock directly into one tag.
pub fn simple_clock_tag(s: &str) -> Option<String> {
    let (hour, minute) = s.split_once(':')?;
    let hour = lookup(HOURS, hour)?;
    let minute_word = lookup(MINUTES, minute)?;
    let mut tag = String::with_capacity(44 + hour.len() + minute_word.len());
    tag.push_str("time { hours: \"");
    tag.push_str(hour);
    tag.push('"');
    if minute != "00" {
        tag.push_str(" minutes: \"");
        tag.push_str(minute_word);
        tag.push('"');
    }
    tag.push_str(" }");
    Some(tag)
}

pub fn clock(s: &str) -> Option<(String, String)> {
    let (raw, suf) = split_suffix(s);
    let p: Vec<_> = raw.split(':').collect();
    if !(2..=3).contains(&p.len())
        || !(1..=2).contains(&p[0].len())
        || p[1].len() != 2
        || (p.len() == 3 && p[2].len() != 2)
    {
        return None;
    }
    if (p[1] == "00" && p[0] == "0" && (p.len() == 2 || p[2] == "00"))
        || (suf.is_some() && p.len() == 3 && (p[1] != "00" || p[2] != "00"))
    {
        return None;
    }
    let h = lookup(HOURS, p[0])?;
    let m = lookup(MINUTES, p[1])?;
    let mut fields = format!("hours: \"{h}\"");
    let mut spoken = h.to_owned();
    if p[1] != "00" {
        let m = suf.map_or(m.to_owned(), |x| glue_suffix(m, x));
        fields.push_str(&format!(" minutes: \"{m}\""));
        spoken.push_str(&format!(" და {m}"));
    } else if let Some(x) = suf {
        let h = glue_suffix(h, x);
        fields = format!("hours: \"{h}\"");
        spoken = h;
    }
    if p.len() == 3 {
        if !p[2].bytes().all(|b| b.is_ascii_digit()) {
            return None;
        }
        let sec = p[2].parse::<u32>().ok()?;
        let sec = cardinal(&sec.to_string())?;
        if p[1] == "00" && p[2] == "00" {
        } else if p[1] != "00" && p[2] == "00" {
        } else {
            fields.push_str(&format!(" seconds: \"{sec}\""));
            spoken.push_str(&format!(" და {sec} წამი"));
        }
    }
    Some((fields, spoken))
}
pub fn time_tags(s: &str) -> Vec<String> {
    let mut out = Vec::new();
    if let Some((body, _)) = clock(s) {
        out.push(format!("time {{ {body} }}"));
    }
    if let Some(base) = s.strip_suffix(":00") {
        if let Some((_, minute)) = base.split_once(':') {
            if minute != "00" {
                if let Some((body, _)) = clock(base) {
                    out.push(format!("time {{ {body} seconds: \"ნული\" }}"));
                }
            }
        }
    }
    for (marker, is_am) in [
        ("am", true),
        ("AM", true),
        ("a.m.", true),
        ("A.M.", true),
        ("Am", true),
        ("pm", false),
        ("PM", false),
        ("p.m.", false),
        ("P.M.", false),
        ("Pm", false),
    ] {
        if let Some(raw) = s.strip_suffix(marker) {
            let raw = raw.trim_end_matches(is_nemo_space);
            if split_suffix(raw).1.is_none() {
                if let Some((body, _)) = clock(raw) {
                    let hour = raw.split(':').next().and_then(|x| x.parse::<u8>().ok()).unwrap_or(0);
                    if (1..=12).contains(&hour) {
                        let first = hour <= 5 || hour == 12;
                        let period = match (is_am, first) {
                            (true, true) => "ღამის",
                            (true, false) => "დილის",
                            (false, true) => "დღის",
                            (false, false) => "საღამოს",
                        };
                        out.push(format!("time {{ {body} period: \"{period}\" }}"));
                        if let Some(base) = raw.strip_suffix(":00") {
                            if base.split_once(':').is_some_and(|(_, minute)| minute != "00") {
                                if let Some((body, _)) = clock(base) {
                                    out.push(format!("time {{ {body} seconds: \"ნული\" period: \"{period}\" }}"));
                                }
                            }
                        }
                    }
                }
            }
        }
    }
    for zone in ["UTC", "GMT", "EST", "PST", "CET", "EET", "MSK"] {
        if let Some(i) = s.find(zone) {
            let raw = s[..i].trim_end_matches(is_nemo_space);
            let tail = &s[i + zone.len()..];
            let valid = tail.is_empty()
                || tail
                    .strip_prefix(['+', '-', '−'])
                    .is_some_and(|v| (1..=2).contains(&v.len()) && v.bytes().all(|b| b.is_ascii_digit()));
            let clock_ok = split_suffix(raw).1.is_none() && (raw.matches(':').count() == 1 || raw.ends_with(":00:00"));
            if valid && clock_ok {
                if let Some((body, _)) = clock(raw) {
                    out.push(format!("time {{ {body} zone: \"{}\" }}", &s[i..]));
                }
            }
        }
    }
    out
}
