//! Hebrew vulgar, named, general and mixed fractions.
use super::cardinal::base_num;

fn named(input: &str) -> Option<&'static str> {
    Some(match input {
        "½" | "1/2" => "חצי",
        "⅓" | "1/3" => "שליש",
        "⅔" | "2/3" => "שני שליש",
        "¼" | "1/4" => "רבע",
        "¾" | "3/4" => "שלושה רבעים",
        "⅕" | "1/5" => "חמישית",
        "⅖" | "2/5" => "שתי חמישיות",
        "⅗" | "3/5" => "שלוש חמישיות",
        "⅘" | "4/5" => "ארבע חמישיות",
        "⅙" | "1/6" => "שישית",
        "⅚" | "5/6" => "חמש שישיות",
        "⅐" | "1/7" => "שביעית",
        "⅛" | "1/8" => "שמינית",
        "⅜" | "3/8" => "שלוש שמיניות",
        "⅝" | "5/8" => "חמש שמיניות",
        "⅞" | "7/8" => "שבע שמיניות",
        "⅑" | "1/9" => "תשיעית",
        "⅒" | "1/10" => "עשירית",
        "3/10" => "שלוש עשיריות",
        "7/10" => "שבע עשיריות",
        "9/10" => "תשע עשיריות",
        _ => return None,
    })
}

/// Return the source union's possible names and weights, including classifier weight.
pub(crate) fn readings(input: &str) -> Vec<(String, i64)> {
    let (sign, rest) = match input.strip_prefix('-').or_else(|| input.strip_prefix('−')) {
        Some(s) => ("מינוס ", s),
        None => ("", input),
    };
    let mut out = Vec::new();
    if let Some(name) = named(rest) {
        out.push((format!("{sign}{name}"), 1_930_000));
    }
    if let Some((a, b)) = rest.split_once('/') {
        if let (Some(a), Some(b)) = (base_num(a, false), base_num(b, false)) {
            out.push((format!("{sign}{a} חלקי {b}"), 2_030_000));
        }
    }
    for (i, c) in rest.char_indices() {
        let (whole, fraction) = if c == ' ' {
            (&rest[..i], &rest[i + 1..])
        } else if !c.is_ascii() {
            (&rest[..i], &rest[i..])
        } else {
            continue;
        };
        if whole.is_empty() {
            continue;
        }
        if c != ' ' && fraction.contains('/') {
            continue;
        }
        if c == ' ' && fraction.starts_with(' ') {
            continue;
        }
        let Some(fraction_name) = named(fraction) else { continue };
        if c == ' ' || !fraction.contains('/') {
            if let Some(name) = base_num(whole, true) {
                out.push((format!("{sign}{name} ו{fraction_name}"), 1_830_000));
                if whole.len() == 1 && !whole.starts_with('0') {
                    out.push((format!("{sign}{name} ו{fraction_name}"), 1_880_000));
                }
            }
            if let Some(name) = base_num(whole, false) {
                out.push((format!("{sign}{name} ו{fraction_name}"), 2_380_000));
            }
        }
    }
    out
}
