//! Abbreviations, contextual symbols, percentages and arithmetic.
use super::{abbreviations::ABBREVIATIONS, cardinal::base_num, serial::letters};
use std::collections::HashMap;
use std::sync::LazyLock;

static ABBREV_INDEX: LazyLock<HashMap<&'static str, Vec<&'static str>>> = LazyLock::new(|| {
    let mut index: HashMap<&'static str, Vec<&'static str>> = HashMap::new();
    for &(key, value) in ABBREVIATIONS {
        index.entry(key).or_default().push(value);
    }
    index
});

fn trim(s: &str) -> &str {
    let s = s.strip_prefix(' ').unwrap_or(s);
    s.strip_suffix(' ').unwrap_or(s)
}
pub(crate) fn readings(s: &str) -> Vec<(String, i64)> {
    let mut out = Vec::new();
    if let Some(values) = ABBREV_INDEX.get(s) {
        for &value in values {
            out.push((value.into(), 1_770_000));
        }
    }
    for (a, b) in [
        ("&", "וגם"),
        ("©", "כל הזכויות שמורות"),
        ("§", "סעיף"),
        ("®", "סימן רשום"),
        ("™", "סימן מסחרי"),
        ("~", "בערך"),
        ("±", "פלוס מינוס"),
    ] {
        if s == a {
            out.push((b.into(), 1_850_000));
        }
    }
    for (lead, name) in [
        ("מס'", "מספר"),
        ("מס׳", "מספר"),
        ("מס.", "מספר"),
        ("מס", "מספר"),
        ("מספר", "מספר"),
        ("No.", "מספר"),
        ("No", "מספר"),
        ("no.", "מספר"),
        ("#", "מספר"),
        ("עמ'", "עמוד"),
        ("עמ׳", "עמוד"),
        ("עמ.", "עמוד"),
        ("עמוד", "עמוד"),
        ("p.", "עמוד"),
        ("pp.", "עמודים"),
        ("סע'", "סעיף"),
        ("סע׳", "סעיף"),
        ("סע.", "סעיף"),
        ("סעיף", "סעיף"),
        ("§", "סעיף"),
        ("©", "כל הזכויות שמורות"),
    ] {
        if let Some(n) = s.strip_prefix(lead) {
            let n = n.strip_prefix(' ').unwrap_or(n);
            if let Some(n) = base_num(n, false) {
                out.push((format!("{name} {n}"), 1_730_000));
            }
        }
    }
    for (sym, unit) in [("%", "אחוזים"), ("‰", "פרומיל"), ("¢", "סנט")] {
        if let Some(n) = s.strip_suffix(sym) {
            let n = if sym == "%" { n } else { n.strip_suffix(' ').unwrap_or(n) };
            if let Some(v) = base_num(n, true) {
                out.push((format!("{v} {unit}"), if sym == "%" { 2_640_000 } else { 1_690_000 }));
            }
            if let Some(v) = base_num(n, false) {
                out.push((
                    format!("{v} {}", if sym == "%" { "אחוז" } else { unit }),
                    if sym == "%" { 3_190_000 } else { 2_240_000 },
                ));
            }
            if sym == "%" {
                if let Some((a, b)) = n.split_once('.') {
                    if b.len() == 1 {
                        if let (Some(a), Some(b)) = (base_num(a, false), base_num(b, false)) {
                            out.push((format!("{a} נקודה {b} אחוז"), 2_660_000));
                        }
                    }
                }
                for (i, c) in n.char_indices() {
                    if matches!(c, '-' | '–' | '—' | '־') {
                        if let (Some(a), Some(b)) =
                            (base_num(trim(&n[..i]), true), base_num(trim(&n[i + c.len_utf8()..]), true))
                        {
                            out.push((format!("{a} עד {b} אחוזים"), 2_640_000));
                        }
                    }
                }
            }
        }
    }
    if let Some((a, b)) = s.split_once('&') {
        let a = trim(a);
        let b = trim(b);
        if a.len() == 1 && b.len() == 1 {
            if let (Some(a), Some(b)) = (letters(a), letters(b)) {
                out.push((format!("{a} וגם {b}"), 1_700_000));
            }
        }
    }
    for (op, word, w) in [('+', "ועוד", 2_650_000), ('=', "שווה", 2_650_000), ('^', "בחזקת", 2_670_000)] {
        if let Some((a, b)) = s.split_once(op) {
            if let (Some(a), Some(b)) = (base_num(trim(a), false), base_num(trim(b), false)) {
                out.push((format!("{a} {word} {b}"), w));
            }
        }
    }
    if let Some((a, rest)) = s.split_once('+') {
        if let Some((b, c)) = rest.split_once('=') {
            if let (Some(a), Some(b), Some(c)) =
                (base_num(trim(a), false), base_num(trim(b), false), base_num(trim(c), false))
            {
                out.push((format!("{a} ועוד {b} שווה {c}"), 2_630_000));
            }
        }
    }
    out
}
