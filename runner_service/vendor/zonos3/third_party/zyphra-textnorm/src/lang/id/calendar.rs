use super::number::cardinal;
use std::collections::HashMap;
use std::sync::LazyLock;

type Index = HashMap<&'static str, &'static str>;

fn index(tsv: &'static str) -> Index {
    let mut entries = HashMap::with_capacity(tsv.lines().count());
    for line in tsv.lines() {
        if let Some((key, value)) = line.split_once('\t') {
            entries.entry(key).or_insert_with(|| value.trim_end_matches('\r'));
        }
    }
    entries
}

static MONTH_NUMBERS: LazyLock<Index> = LazyLock::new(|| index(include_str!("../../../data/id/months/numbers.tsv")));
static MONTH_NAMES: LazyLock<Index> = LazyLock::new(|| index(include_str!("../../../data/id/months/names.tsv")));
static MONTH_ABBREVIATIONS: LazyLock<Index> = LazyLock::new(|| index(include_str!("../../../data/id/months/abbr.tsv")));
static TIME_ZONES: LazyLock<Index> = LazyLock::new(|| index(include_str!("../../../data/id/time/time_zone.tsv")));

fn unpad(s: &str) -> &str {
    s.strip_prefix('0').filter(|r| !r.is_empty()).unwrap_or(s)
}

fn day(s: &str) -> Option<String> {
    let t = unpad(s);
    let n: u8 = t.parse().ok()?;
    if s.len() > 3 || !(1..=31).contains(&n) {
        return None;
    }
    cardinal(t)
}

fn month(s: &str) -> Option<&'static str> {
    let t = unpad(s);
    let n: u8 = t.parse().ok()?;
    if s.len() > 3 || !(1..=12).contains(&n) {
        return None;
    }
    MONTH_NUMBERS.get(t).copied()
}

fn name(s: &str) -> Option<&'static str> {
    MONTH_NAMES.get(s).copied().or_else(|| MONTH_ABBREVIATIONS.get(s.strip_suffix('.').unwrap_or(s)).copied())
}

fn year(s: &str, four: bool) -> Option<String> {
    if !((s.len() == 4 && (s.starts_with('1') || s.starts_with('2'))) || (!four && s.len() == 2)) {
        return None;
    }
    cardinal(s)
}

pub(crate) fn date_word_start(word: &str) -> bool {
    word.as_bytes().first().is_some_and(u8::is_ascii_digit)
        || word.starts_with(['\'', '’'])
        || ["tahun", "Tahun", "era", "Era", "abad"].contains(&word)
        || name(word).is_some()
}

pub(crate) fn date_start(s: &str) -> bool {
    let Some(first) = s.as_bytes().first() else { return false };
    if first.is_ascii_digit() {
        let end = s.bytes().take_while(u8::is_ascii_digit).count();
        let rest = &s[end..];
        return rest.is_empty() || rest.starts_with([' ', '/', '-', '.', '\'']) || matches!(rest, "an" | "s");
    }
    if s.starts_with(['\'', '’'])
        || ["tahun ", "Tahun ", "era ", "Era ", "abad ke-"].iter().any(|prefix| s.starts_with(prefix))
    {
        return true;
    }
    s.split_once(' ').is_some_and(|(month, _)| name(month).is_some())
}

pub(crate) fn date(s: &str) -> Vec<String> {
    let mut out = Vec::new();
    macro_rules! push {
        ($($arg:tt)*) => { out.push(if super::lazy::recognizing() {String::new()} else {format!($($arg)*)}) };
    }
    for sep in ['/', '-', '.'] {
        let mut parts = s.split(sep);
        if let (Some(a), Some(b), Some(c), None) = (parts.next(), parts.next(), parts.next(), parts.next()) {
            if let Some(d) = day(a) {
                if let Some(m) = month(b) {
                    if let Some(y) = year(c, false) {
                        push!("date {{ day: \"{d}\" month: \"{m}\" year: \"{y}\" }}");
                    }
                }
            }
            if sep == '-' {
                if let Some(y) = year(a, true) {
                    if let Some(m) = month(b) {
                        if let Some(d) = day(c) {
                            push!("date {{ year: \"{y}\" month: \"{m}\" day: \"{d}\" }}");
                        }
                    }
                }
            }
        }
    }
    let mut parts = s.split(' ');
    if let (Some(a), Some(b), third, None) = (parts.next(), parts.next(), parts.next(), parts.next()) {
        if let Some(d) = day(a) {
            if let Some(m) = name(b) {
                if let Some(c) = third {
                    if let Some(y) = year(c, true) {
                        push!("date {{ day: \"{d}\" month: \"{m}\" year: \"{y}\" }}");
                    }
                } else {
                    push!("date {{ day: \"{d}\" month: \"{m}\" }}");
                }
            }
        }
        if third.is_none() {
            if let Some(y) = year(b, true) {
                if let Some(m) = name(a) {
                    push!("date {{ month: \"{m}\" year: \"{y}\" }}");
                }
            }
        }
    }
    let (pre, dec) = ["tahun ", "Tahun ", "era ", "Era "]
        .iter()
        .find_map(|p| s.strip_prefix(*p).map(|r| (*p, r)))
        .unwrap_or(("", s));
    for suffix in ["-an", "an", "'s", "s"] {
        if let Some(raw) = dec.strip_suffix(suffix) {
            let n = raw.strip_prefix('\'').or_else(|| raw.strip_prefix('’')).unwrap_or(raw);
            if (n.len() == 2 && !n.starts_with('0')
                || n.len() == 4 && n == raw && (n.starts_with('1') || n.starts_with('2')))
                && n.ends_with('0')
            {
                if let Some(w) = cardinal(n) {
                    push!("date {{ year: \"{pre}{w}an\" }}");
                }
            }
        }
    }
    let (ep, tail) =
        ["tahun ", "Tahun ", "abad ke-"].iter().find_map(|p| s.strip_prefix(*p).map(|r| (*p, r))).unwrap_or(("", s));
    if let Some((n, e)) = tail.split_once(' ') {
        let meaning = match e {
            "SM" | "S.M." | "BC" | "B.C." | "BCE" | "B.C.E." => Some("sebelum Masehi"),
            "SEU" => Some("sebelum Era Umum"),
            "AD" | "A.D." | "CE" | "C.E." => Some("Masehi"),
            "EU" => Some("Era Umum"),
            "M" => Some("Masehi"),
            "H" => Some("Hijriah"),
            _ => None,
        };
        let keyed = matches!(e, "M" | "H");
        if n.len() <= 4 && !n.starts_with('0') && if keyed { !ep.is_empty() } else { ep != "abad ke-" } {
            if let (Some(n), Some(e)) = (cardinal(n), meaning) {
                push!("date {{ year: \"{ep}{n} {e}\" }}");
            }
        }
    }
    out
}

const HOUR_PREFIXES: &[(&str, Option<&str>)] = &[
    ("pukul ", None),
    ("Pukul ", None),
    ("pkl. ", None),
    ("pkl ", None),
    ("jam ", Some("jam")),
    ("Jam ", Some("Jam")),
];

pub(crate) fn time_start(s: &str) -> bool {
    if s.as_bytes().first().is_some_and(u8::is_ascii_digit) {
        s.contains([':', '.'])
    } else {
        HOUR_PREFIXES.iter().any(|(prefix, _)| s.starts_with(prefix))
    }
}

fn hour(s: &str) -> Option<super::number::Integer> {
    if s.len() > 3 {
        return None;
    }
    let value = super::number::Integer::plain(unpad(s))?;
    (value.value() <= 24).then_some(value)
}

fn minute(s: &str) -> Option<Option<super::number::Integer>> {
    if s == "00" {
        return Some(None);
    }
    if s.len() != 2 {
        return None;
    }
    let value = super::number::Integer::plain(unpad(s))?;
    (value.value() != 0 && value.value() < 60).then_some(Some(value))
}

type Clock = (super::number::Integer, Option<super::number::Integer>);

fn hm(s: &str) -> Option<Clock> {
    let (a, b) = s.split_once(':').or_else(|| s.split_once('.'))?;
    Some((hour(a)?, minute(b)?))
}

#[derive(Clone, Copy)]
enum Reading {
    Hour(super::number::Integer),
    Clock(Clock, Option<super::number::Integer>),
    Range(Clock, Clock),
}

#[derive(Clone, Copy)]
struct TimePath {
    prefix: Option<&'static str>,
    zone: Option<&'static str>,
    reading: Reading,
}

impl TimePath {
    fn render(self) -> String {
        let mut tag = String::with_capacity(256 + self.zone.map_or(0, |zone| zone.len() * 2));
        tag.push_str("time { ");
        if let Some(prefix) = self.prefix {
            tag.push_str("prefix: \"");
            tag.push_str(prefix);
            tag.push_str("\" ");
        }
        tag.push_str("hours: \"");
        match self.reading {
            Reading::Hour(hour) => {
                hour.write(&mut tag);
                tag.push('"');
            }
            Reading::Clock((hour, minute), second) => {
                hour.write(&mut tag);
                tag.push('"');
                for (key, value) in [(" minutes: \"", minute), (" seconds: \"", second)] {
                    if let Some(value) = value {
                        tag.push_str(key);
                        value.write(&mut tag);
                        tag.push('"');
                    }
                }
            }
            Reading::Range((h1, m1), (h2, m2)) => {
                for (index, (hour, minute)) in [(h1, m1), (h2, m2)].into_iter().enumerate() {
                    if index != 0 {
                        tag.push_str(" sampai pukul ");
                    }
                    hour.write(&mut tag);
                    if let Some(minute) = minute {
                        tag.push_str(" lewat ");
                        minute.write(&mut tag);
                        tag.push_str(" menit");
                    }
                }
                tag.push('"');
            }
        }
        if let Some(zone) = self.zone {
            tag.push_str(" zone: \"");
            tag.extend(zone.chars().map(|c| if c == ' ' { '\u{a0}' } else { c }));
            tag.push('"');
        }
        tag.push_str(" preserve_order: true }");
        tag
    }
}

fn scan_time(s: &str, mut emit: impl FnMut(TimePath) -> bool) {
    let (s, zone) = if let Some((a, b)) = s.rsplit_once(' ') {
        if let Some(zone) = TIME_ZONES.get(b).copied() { (a, Some(zone)) } else { (s, None) }
    } else {
        (s, None)
    };
    let (s, prefix, prefixed) = HOUR_PREFIXES
        .iter()
        .find_map(|(key, prefix)| s.strip_prefix(key).map(|rest| (rest, *prefix, true)))
        .unwrap_or((s, None, false));
    let mut offer = |reading| emit(TimePath { prefix, zone, reading });
    if prefixed {
        if let Some(hour) = hour(s) {
            if offer(Reading::Hour(hour)) {
                return;
            }
        }
    }
    let mut parts = s.split(':');
    if let (Some(a), Some(b), Some(c), None) = (parts.next(), parts.next(), parts.next(), parts.next()) {
        if let (Some(hour), Some(minute), Some(Some(second))) = (hour(a), minute(b), minute(c)) {
            if offer(Reading::Clock((hour, minute), Some(second))) {
                return;
            }
        }
    }
    if let Some(clock) = hm(s) {
        if offer(Reading::Clock(clock, None)) {
            return;
        }
    }
    for dash in ['-', '–'] {
        if let Some((a, b)) = s.split_once(dash) {
            if let (Some(left), Some(right)) =
                (hm(a.strip_suffix(' ').unwrap_or(a)), hm(b.strip_prefix(' ').unwrap_or(b)))
            {
                if offer(Reading::Range(left, right)) {
                    return;
                }
            }
        }
    }
}

pub(crate) fn time(s: &str, mut emit: impl FnMut(String)) {
    let recognizing = super::lazy::recognizing();
    scan_time(s, |path| {
        emit(if recognizing { String::new() } else { path.render() });
        false
    });
}

pub(crate) fn selected_time(s: &str) -> Option<String> {
    let mut selected = None;
    scan_time(s, |path| {
        selected = Some(path);
        true
    });
    selected.map(TimePath::render)
}
