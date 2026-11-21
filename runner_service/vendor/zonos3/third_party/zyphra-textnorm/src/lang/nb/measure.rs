use super::{basic, number};
fn numbers(s: &str, neuter: bool) -> Vec<(String, f64)> {
    let mut v: Vec<_> = basic::decimal_number(s).into_iter().map(|x| (x, 0.0)).collect();
    if let Some(x) = number::cardinal(s, neuter) {
        v.push((x, 0.0))
    }
    if neuter {
        for (a, b) in [("1/2", "ett halvt"), ("½", "ett halvt"), ("-1/2", "minus ett halvt"), ("-½", "minus ett halvt")]
        {
            if s == a {
                v.push((b.into(), 0.0));
            }
        }
        for suffix in ["1/2", "½"] {
            if let Some(prefix) = s.strip_suffix(suffix) {
                let prefix = prefix.strip_suffix(' ').unwrap_or(prefix);
                if let Some(n) = number::cardinal(prefix, false) {
                    v.push((format!("{n} og ett halvt"), 0.0));
                }
            }
        }
    } else {
        v.extend(basic::fraction(s));
    }
    v
}
const UNITS: &[(&str, &str, u8)] = &[
    ("kg", "kilogram", 1),
    ("kilo", "kilo", 1),
    ("g", "gram", 1),
    ("mg", "milligram", 1),
    ("µg", "mikrogram", 1),
    ("μg", "mikrogram", 1),
    ("t", "tonn", 1),
    ("km", "kilometer", 0),
    ("m", "meter", 0),
    ("cm", "centimeter", 0),
    ("mm", "millimeter", 0),
    ("µm", "mikrometer", 0),
    ("μm", "mikrometer", 0),
    ("l", "liter", 0),
    ("L", "liter", 0),
    ("dl", "desiliter", 0),
    ("cl", "centiliter", 0),
    ("ml", "milliliter", 0),
    ("mL", "milliliter", 0),
    ("m²", "kvadratmeter", 0),
    ("km²", "kvadratkilometer", 0),
    ("cm²", "kvadratcentimeter", 0),
    ("m³", "kubikkmeter", 0),
    ("cm³", "kubikkcentimeter", 0),
    ("km/t", "kilometer i timen", 0),
    ("km/h", "kilometer i timen", 0),
    ("m/s", "meter per sekund", 0),
    ("Gbps", "gigabit per sekund", 0),
    ("Mbps", "megabit per sekund", 0),
    ("kbps", "kilobit per sekund", 0),
    ("bps", "bit per sekund", 0),
    ("GB", "gigabyte", 0),
    ("MB", "megabyte", 0),
    ("kB", "kilobyte", 0),
    ("KB", "kilobyte", 0),
    ("TB", "terabyte", 0),
    ("B", "byte", 0),
    ("W", "watt", 0),
    ("kW", "kilowatt", 0),
    ("MW", "megawatt", 0),
    ("GW", "gigawatt", 0),
    ("V", "volt", 0),
    ("kV", "kilovolt", 0),
    ("mV", "millivolt", 0),
    ("A", "ampere", 0),
    ("mA", "milliampere", 0),
    ("Hz", "hertz", 0),
    ("kHz", "kilohertz", 0),
    ("MHz", "megahertz", 0),
    ("GHz", "gigahertz", 0),
    ("hPa", "hektopascal", 0),
    ("Pa", "pascal", 0),
    ("bar", "bar", 0),
    ("dB", "desibel", 0),
    ("Ω", "ohm", 0),
    ("Ω", "ohm", 0),
    ("°C", "celsiusgrader", 0),
    ("°C", "grader celsius", 0),
    ("°F", "grader fahrenheit", 0),
];
const INFLECT: &[(&str, &str, &str)] = &[
    ("ss", "spiseskje", "spiseskjeer"),
    ("ts", "teskje", "teskjeer"),
    ("tsk", "teskje", "teskjeer"),
    ("kcal", "kilokalori", "kilokalorier"),
    ("cal", "kalori", "kalorier"),
    ("kWh", "kilowattime", "kilowattimer"),
    ("Wh", "wattime", "wattimer"),
    ("mAh", "milliamperetime", "milliamperetimer"),
    ("Ah", "amperetime", "amperetimer"),
    ("°", "grad", "grader"),
    ("stk", "stykk", "stykker"),
    ("stk.", "stykk", "stykker"),
];
// Preserve the source table order within each suffix bucket, including
// duplicate temperature readings, without scanning unrelated units.
static INDEX: std::sync::LazyLock<std::collections::HashMap<char, (Vec<usize>, Vec<usize>)>> =
    std::sync::LazyLock::new(|| {
        let mut index: std::collections::HashMap<char, (Vec<usize>, Vec<usize>)> = std::collections::HashMap::new();
        for (i, (key, _, _)) in UNITS.iter().enumerate() {
            index.entry(key.chars().last().expect("unit key")).or_default().0.push(i);
        }
        for (i, (key, _, _)) in INFLECT.iter().enumerate() {
            index.entry(key.chars().last().expect("unit key")).or_default().1.push(i);
        }
        index
    });

/// Count simple cardinal-plus-unit paths without spelling the number.
/// `None` leaves decimals, fractions, signed amounts, and ranges to `parse`.
pub fn simple_cardinal(s: &str) -> Option<usize> {
    if !s.as_bytes().first().is_some_and(u8::is_ascii_digit) {
        return None;
    }
    let boundary = s.char_indices().find(|(_, c)| !c.is_ascii_digit() && *c != ' ').map_or(s.len(), |(i, _)| i);
    let n = s[..boundary].strip_suffix(' ').unwrap_or(&s[..boundary]);
    number::parse_cardinal(n)?;
    let unit = &s[boundary..];
    if unit.contains(['-', '−', '–', '—', '~', '.', ',', '/', '½']) {
        // The unit lexicon itself contains periods and slashes. These forms
        // stay on the general path rather than constraining its alphabet.
        return None;
    }
    let Some((units, inflect)) = unit.chars().last().and_then(|c| INDEX.get(&c)) else { return Some(0) };
    Some(
        units.iter().filter(|&&i| UNITS[i].0 == unit).count()
            + inflect.iter().filter(|&&i| INFLECT[i].0 == unit).count(),
    )
}

fn single(s: &str) -> Vec<(String, f64)> {
    let Some((units, inflect)) = s.chars().last().and_then(|c| INDEX.get(&c)) else { return Vec::new() };
    let mut out = Vec::new();
    for &i in units {
        let (u, w, neuter) = UNITS[i];
        if let Some(n) = s.strip_suffix(u) {
            let n = n.strip_suffix(' ').unwrap_or(n);
            for (x, cost) in numbers(n, neuter == 1) {
                out.push((format!("{x} {w}"), cost));
            }
        }
    }
    for &i in inflect {
        let (u, one, many) = INFLECT[i];
        if let Some(n) = s.strip_suffix(u) {
            let n = n.strip_suffix(' ').unwrap_or(n);
            if n == "1" {
                out.push((format!("{} {one}", if u.starts_with("stk") { "ett" } else { "en" }), 0.0));
            } else {
                for (x, cost) in numbers(n, false) {
                    if !u.starts_with("stk") || basic::fraction(n).is_empty() {
                        out.push((format!("{x} {many}"), cost));
                    }
                }
            }
        }
    }
    out
}
pub fn verbalize_simple(s: &str) -> Option<String> {
    let boundary = s.char_indices().find(|(_, c)| !c.is_ascii_digit() && *c != ' ')?;
    let n = s[..boundary.0].strip_suffix(' ').unwrap_or(&s[..boundary.0]);
    let unit = &s[boundary.0..];
    let (units, inflect) = INDEX.get(&unit.chars().last()?)?;
    for &i in units {
        let (key, word, neuter) = UNITS[i];
        if key == unit {
            return Some(format!("{} {word}", number::cardinal(n, neuter == 1)?));
        }
    }
    for &i in inflect {
        let (key, one, many) = INFLECT[i];
        if key == unit {
            if n == "1" {
                return Some(format!("{} {one}", if key.starts_with("stk") { "ett" } else { "en" }));
            }
            return Some(format!("{} {many}", number::cardinal(n, false)?));
        }
    }
    None
}

pub fn parse(s: &str) -> Vec<(String, f64)> {
    let mut out = single(s);
    for (prefix, word) in [("~", "omtrent "), ("ca.", "cirka "), ("ca. ", "cirka ")] {
        if let Some(rest) = s.strip_prefix(prefix) {
            for (x, cost) in single(rest) {
                out.push((format!("{word}{x}"), cost));
            }
        }
    }
    for (i, c) in s.char_indices().filter(|(_, c)| matches!(c, '-' | '–' | '—')) {
        let a = s[..i].strip_suffix(' ').unwrap_or(&s[..i]);
        let b = &s[i + c.len_utf8()..];
        let b = b.strip_prefix(' ').unwrap_or(b);
        let right = single(b);
        if right.is_empty() {
            continue;
        }
        for (x, cost) in numbers(a, false).into_iter().chain(numbers(a, true)) {
            for (y, other) in &right {
                out.push((format!("{x} til {y}"), cost + other));
            }
        }
    }
    out
}
