//! Number, range, unit and temperature expressions.
use super::{cardinal::base_num, decimal::decimal_num};
use std::collections::HashMap;
use std::sync::LazyLock;

/// Prefix recognition and exact-key membership share one lexicon-derived index.
static UNIT_PREFIXES: LazyLock<HashMap<&'static str, bool>> = LazyLock::new(|| {
    let mut prefixes = HashMap::new();
    for (unit, _) in UNITS {
        for (i, ch) in unit.char_indices() {
            let end = i + ch.len_utf8();
            let complete = prefixes.entry(&unit[..end]).or_insert(false);
            *complete |= end == unit.len();
        }
    }
    prefixes
});

pub(crate) fn unit_state(s: &str) -> Option<bool> {
    UNIT_PREFIXES.get(s).copied()
}

const UNITS: &[(&str, &str)] = &[
    ("km", "קילומטר"),
    ("ק\"מ", "קילומטר"),
    ("ק״מ", "קילומטר"),
    ("m", "מטר"),
    ("מטר", "מטר"),
    ("cm", "סנטימטר"),
    ("ס\"מ", "סנטימטר"),
    ("ס״מ", "סנטימטר"),
    ("mm", "מילימטר"),
    ("מ\"מ", "מילימטר"),
    ("מ״מ", "מילימטר"),
    ("µm", "מיקרומטר"),
    ("μm", "מיקרומטר"),
    ("km²", "קילומטר רבוע"),
    ("קמ\"ר", "קילומטר רבוע"),
    ("קמ״ר", "קילומטר רבוע"),
    ("m²", "מטר רבוע"),
    ("מ\"ר", "מטר רבוע"),
    ("מ״ר", "מטר רבוע"),
    ("cm²", "סנטימטר רבוע"),
    ("סמ\"ר", "סנטימטר רבוע"),
    ("סמ״ר", "סנטימטר רבוע"),
    ("mm²", "מילימטר רבוע"),
    ("m³", "מטר מעוקב"),
    ("מ\"ק", "מטר מעוקב"),
    ("מ״ק", "מטר מעוקב"),
    ("cm³", "סנטימטר מעוקב"),
    ("סמ\"ק", "סנטימטר מעוקב"),
    ("סמ״ק", "סנטימטר מעוקב"),
    ("ml", "מיליליטר"),
    ("mL", "מיליליטר"),
    ("מ\"ל", "מיליליטר"),
    ("מ״ל", "מיליליטר"),
    ("l", "ליטר"),
    ("L", "ליטר"),
    ("ליטר", "ליטר"),
    ("kg", "קילוגרם"),
    ("ק\"ג", "קילוגרם"),
    ("ק״ג", "קילוגרם"),
    ("g", "גרם"),
    ("גרם", "גרם"),
    ("mg", "מיליגרם"),
    ("מ\"ג", "מיליגרם"),
    ("מ״ג", "מיליגרם"),
    ("µg", "מיקרוגרם"),
    ("μg", "מיקרוגרם"),
    ("t", "טון"),
    ("טון", "טון"),
    ("km/h", "קילומטר לשעה"),
    ("קמ\"ש", "קילומטר לשעה"),
    ("קמ״ש", "קילומטר לשעה"),
    ("mph", "מיל לשעה"),
    ("s", "שניות"),
    ("sec", "שניות"),
    ("שנ'", "שניות"),
    ("min", "דקות"),
    ("דק'", "דקות"),
    ("h", "שעות"),
    ("hr", "שעות"),
    ("ש'", "שעות"),
    ("GB", "גיגהבייט"),
    ("ג\"ב", "גיגהבייט"),
    ("ג״ב", "גיגהבייט"),
    ("MB", "מגהבייט"),
    ("מ\"ב", "מגהבייט"),
    ("מ״ב", "מגהבייט"),
    ("KB", "קילובייט"),
    ("ק\"ב", "קילובייט"),
    ("ק״ב", "קילובייט"),
    ("TB", "טרבייט"),
    ("ט\"ב", "טרבייט"),
    ("ט״ב", "טרבייט"),
    ("B", "בייט"),
    ("Gbps", "גיגהביט לשנייה"),
    ("Mbps", "מגהביט לשנייה"),
    ("kbps", "קילוביט לשנייה"),
    ("kWh", "קילוואט שעה"),
    ("Wh", "וואט שעה"),
    ("mAh", "מיליאמפר שעה"),
    ("Ah", "אמפר שעה"),
    ("kW", "קילוואט"),
    ("W", "וואט"),
    ("V", "וולט"),
    ("A", "אמפר"),
    ("GHz", "גיגההרץ"),
    ("MHz", "מגההרץ"),
    ("kHz", "קילוהרץ"),
    ("Hz", "הרץ"),
    ("hPa", "הקטופסקל"),
    ("kcal", "קילו קלוריות"),
    ("dB", "דציבל"),
    ("Ω", "אוהם"),
    ("Ω", "אוהם"),
    ("°C", "מעלות צלזיוס"),
    ("℃", "מעלות צלזיוס"),
    ("°F", "מעלות פרנהייט"),
    ("℉", "מעלות פרנהייט"),
    ("°", "מעלות"),
];

fn numbers(s: &str) -> Vec<(String, i64)> {
    let (sign, n) = match s.strip_prefix('-').or_else(|| s.strip_prefix('−')) {
        Some(n) => ("מינוס ", n),
        None => ("", s),
    };
    let mut out = Vec::new();
    for (n, w) in decimal_num(n) {
        out.push((format!("{sign}{n}"), w + 900_000));
    }
    if let Some(n) = base_num(n, true) {
        out.push((format!("{sign}{n}"), 950_000));
    }
    if let Some(n) = base_num(n, false) {
        out.push((format!("{sign}{n}"), 1_050_000));
    }
    out
}
pub(crate) fn readings(s: &str) -> Vec<(String, i64)> {
    let mut out = Vec::new();
    for &(unit, spoken) in UNITS {
        let Some(n) = s.strip_suffix(unit) else { continue };
        let n = n.strip_suffix(' ').unwrap_or(n);
        for (v, w) in numbers(n) {
            out.push((format!("{v} {spoken}"), w + 1_830_000));
        }
        if matches!(unit, "°C" | "℃" | "°F" | "℉" | "°") {
            let (sign, n) = match n.strip_prefix('-').or_else(|| n.strip_prefix('−')) {
                Some(x) => ("מינוס ", x),
                None => ("", n),
            };
            if let Some(v) = base_num(n, false) {
                out.push((format!("{sign}{v} {spoken}"), 1_800_000));
            }
        }
        let plural = match unit {
            "km" | "ק\"מ" | "ק״מ" => Some("קילומטרים"),
            "km/h" | "קמ\"ש" | "קמ״ש" => Some("קילומטרים לשעה"),
            "kg" | "ק\"ג" | "ק״ג" => Some("קילוגרמים"),
            _ => None,
        };
        let short = |s: &str| -> Option<String> {
            let k = s.parse::<u8>().ok()?;
            if !(3..=10).contains(&k) || s != k.to_string() {
                return None;
            }
            base_num(s, true)
        };
        if let Some(p) = plural {
            if let Some(v) = short(n) {
                out.push((format!("{v} {p}"), 1_780_000));
            }
        }
        for (i, c) in n.char_indices() {
            if !matches!(c, '-' | '–' | '—' | '־') || i == 0 {
                continue;
            };
            let a = n[..i].strip_suffix(' ').unwrap_or(&n[..i]);
            let b = &n[i + c.len_utf8()..];
            let b = b.strip_prefix(' ').unwrap_or(b);
            for (a, aw) in numbers(a) {
                for (b, bw) in numbers(b) {
                    out.push((format!("{a} עד {b} {spoken}"), aw + bw + 1_820_000));
                }
            }
            if let (Some(p), Some(a), Some(b)) = (plural, short(a), short(b)) {
                out.push((format!("{a} עד {b} {p}"), 1_750_000));
            }
        }
    }
    out
}
