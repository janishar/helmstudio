//! Dutch units, temperatures, ranges and approximation markers.
use super::numeric::{self, Reading};
use std::collections::{HashMap, HashSet};
use std::sync::LazyLock;

const UNITS: &[(&str, &str)] = &[
    ("km/h", "kilometer per uur"),
    ("kmh", "kilometer per uur"),
    ("km", "kilometer"),
    ("m", "meter"),
    ("cm", "centimeter"),
    ("mm", "millimeter"),
    ("µm", "micrometer"),
    ("μm", "micrometer"),
    ("kg", "kilogram"),
    ("g", "gram"),
    ("mg", "milligram"),
    ("µg", "microgram"),
    ("μg", "microgram"),
    ("l", "liter"),
    ("ml", "milliliter"),
    ("cl", "centiliter"),
    ("dl", "deciliter"),
    ("m²", "vierkante meter"),
    ("km²", "vierkante kilometer"),
    ("cm²", "vierkante centimeter"),
    ("mm²", "vierkante millimeter"),
    ("m³", "kubieke meter"),
    ("km³", "kubieke kilometer"),
    ("cm³", "kubieke centimeter"),
    ("mm³", "kubieke millimeter"),
    ("TB", "terabyte"),
    ("GB", "gigabyte"),
    ("MB", "megabyte"),
    ("KB", "kilobyte"),
    ("kB", "kilobyte"),
    ("GHz", "gigahertz"),
    ("MHz", "megahertz"),
    ("kHz", "kilohertz"),
    ("Hz", "hertz"),
    ("Gbps", "gigabit per seconde"),
    ("Mbps", "megabit per seconde"),
    ("kbps", "kilobit per seconde"),
    ("bps", "bit per seconde"),
    ("mAh", "milliampère-uur"),
    ("Ah", "ampère-uur"),
    ("kWh", "kilowattuur"),
    ("Wh", "wattuur"),
    ("kW", "kilowatt"),
    ("W", "watt"),
    ("MW", "megawatt"),
    ("GW", "gigawatt"),
    ("V", "volt"),
    ("mV", "millivolt"),
    ("kV", "kilovolt"),
    ("A", "ampère"),
    ("mA", "milliampère"),
    ("Ω", "ohm"),
    ("Ω", "ohm"),
    ("hPa", "hectopascal"),
    ("kPa", "kilopascal"),
    ("MPa", "megapascal"),
    ("Pa", "pascal"),
    ("bar", "bar"),
    ("mbar", "millibar"),
    ("kcal", "kilocalorieën"),
    ("cal", "calorieën"),
    ("kJ", "kilojoule"),
    ("J", "joule"),
    ("dB", "decibel"),
    ("sec", "seconden"),
    ("s", "seconden"),
    ("min", "minuten"),
    ("h", "uur"),
    ("uur", "uur"),
    ("¢", "cent"),
    ("‰", "promille"),
    ("%", "procent"),
];

const fn max_unit_bytes() -> usize {
    let mut i = 0;
    let mut max = 0;
    while i < UNITS.len() {
        let len = UNITS[i].0.len();
        if len > max {
            max = len;
        }
        i += 1;
    }
    max
}

static UNIT_WORDS: LazyLock<HashMap<&'static str, &'static str>> = LazyLock::new(|| UNITS.iter().copied().collect());
static UNIT_PREFIXES: LazyLock<HashSet<&'static str>> = LazyLock::new(|| {
    let mut prefixes = HashSet::new();
    for &(unit, _) in UNITS {
        for (i, c) in unit.char_indices() {
            prefixes.insert(&unit[..i + c.len_utf8()]);
        }
    }
    prefixes
});

pub(super) fn unit_prefix(s: &str) -> bool {
    UNIT_PREFIXES.contains(s)
}

/// Recognize the common unsigned integer + unit path without spelling it.
pub(super) fn simple_weight(s: &str) -> Option<f64> {
    let digits = s.bytes().take_while(u8::is_ascii_digit).count();
    let n = &s[..digits];
    if n.is_empty() || n.len() > 3 || (n.len() > 1 && n.starts_with('0')) {
        return None;
    }
    let suffix = s[digits..].strip_prefix(' ').unwrap_or(&s[digits..]);
    UNIT_WORDS.get(suffix)?;
    let value = n.parse::<u16>().ok()?;
    let numeric = if (101..=109).contains(&value) { 0.01 } else { 0.0 };
    Some(numeric + if matches!(suffix, "%" | "‰") { 0.98 } else { 1.0 })
}

/// Resolve the already-recognized simple path directly from its character
/// span, without rebuilding the source string or exploring other branches.
pub(super) fn simple_chars(s: &[char]) -> Option<(usize, &'static str, f64)> {
    let mut end = 0;
    let mut value = 0usize;
    while end < s.len() && s[end].is_ascii_digit() {
        value = value * 10 + (s[end] as u8 - b'0') as usize;
        end += 1;
        if end > 3 {
            return None;
        }
    }
    if end == 0 || (end > 1 && s[0] == '0') {
        return None;
    }
    if s.get(end) == Some(&' ') {
        end += 1;
    }
    let mut bytes = [0; max_unit_bytes()];
    let mut used = 0;
    for &c in &s[end..] {
        let mut encoded = [0; 4];
        let piece = c.encode_utf8(&mut encoded).as_bytes();
        if used + piece.len() > bytes.len() {
            return None;
        }
        bytes[used..used + piece.len()].copy_from_slice(piece);
        used += piece.len();
    }
    let unit = std::str::from_utf8(&bytes[..used]).ok()?;
    let spoken = *UNIT_WORDS.get(unit)?;
    let numeric = if (101..=109).contains(&value) { 0.01 } else { 0.0 };
    Some((value, spoken, numeric + if matches!(unit, "%" | "‰") { 0.98 } else { 1.0 }))
}

fn numbers(s: &str) -> Vec<Reading> {
    let mut v = numeric::decimal(s);
    v.extend(numeric::integer(s));
    if let Some(rest) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) {
        v.extend(numeric::integer(rest).into_iter().map(|(w, c)| (format!("min {w}"), c)));
        v.extend(numeric::decimal(rest).into_iter().map(|(w, c)| (format!("min {w}"), c)));
    }
    v
}

fn unspace(s: &str) -> &str {
    s.strip_suffix(' ').unwrap_or(s)
}

fn base(s: &str) -> Vec<Reading> {
    let mut out = Vec::new();
    for &(suffix, word) in UNITS {
        if let Some(prefix) = s.strip_suffix(suffix) {
            for (num, w) in numbers(unspace(prefix)) {
                out.push((format!("{num} {word}"), w + if matches!(suffix, "%" | "‰") { 0.98 } else { 1.0 }));
            }
        }
    }
    for (suffix, noun) in [("°C", "Celsius"), ("°F", "Fahrenheit"), ("°", "")] {
        if let Some(prefix) = s.strip_suffix(suffix) {
            let number = unspace(prefix);
            for (num, w) in numbers(number) {
                if number == "1" && !noun.is_empty() {
                    out.push((format!("{num} graad {noun}"), w + 0.97 + 0.95));
                }
                out.push((
                    if noun.is_empty() { format!("{num} graden") } else { format!("{num} graden {noun}") },
                    w + 0.97 + if noun.is_empty() { 1.0 } else { 0.98 },
                ));
            }
        }
    }
    out
}
fn outer_weight(s: &str) -> f64 {
    if s.ends_with("°C") || s.ends_with("°F") || s.ends_with('°') {
        0.97
    } else if s.ends_with('%') || s.ends_with('‰') {
        0.98
    } else {
        1.0
    }
}

pub(super) fn parse(s: &str) -> Vec<Reading> {
    let mut out = base(s);
    for (symbol, word) in [('~', "ongeveer"), ('±', "plusminus")] {
        if let Some(rest) = s.strip_prefix(symbol) {
            let rest = rest.strip_prefix(' ').unwrap_or(rest);
            for (v, w) in base(rest) {
                out.push((format!("{word} {v}"), w + 0.94 - outer_weight(rest)));
            }
        }
    }
    for (i, c) in s.char_indices().filter(|(_, c)| matches!(c, '-' | '–' | '—')) {
        let left = s[..i].trim_end_matches(crate::text::is_nemo_space);
        let right = s[i + c.len_utf8()..].trim_start_matches(crate::text::is_nemo_space);
        let mut lefts = numeric::integer(left);
        lefts.extend(numeric::decimal(left));
        if outer_weight(right) != 0.97 {
            for (a, aw) in &lefts {
                for (b, bw) in base(right) {
                    out.push((format!("{a} tot {b}"), aw + bw + 0.95 - outer_weight(right)));
                }
            }
        }
    }
    out
}
