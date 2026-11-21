use super::numbers::{cardinal, decimal, split_range, unsigned};
fn numeric(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    if let Some(v) = cardinal(s) {
        out.push((v, 0.0))
    }
    if let Some(v) = decimal(s) {
        if !out.contains(&v) {
            out.push(v)
        }
    }
    out
}
fn single(s: &str, percent: bool) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    if percent {
        let specific = match s {
            "1" => Some("jedno procento"),
            "2" => Some("dvě procenta"),
            "3" => Some("tři procenta"),
            "4" => Some("čtyři procenta"),
            _ => None,
        };
        if let Some(v) = specific {
            out.push((v.to_owned(), 0.0))
        } else if let Some(v) = unsigned(s) {
            out.push((format!("{v} procent"), 0.0))
        }
        if let Some((v, w)) = decimal(s) {
            out.push((format!("{v} procent"), w))
        }
    } else {
        for (v, w) in numeric(s) {
            out.push((format!("{v} promile"), w))
        }
    }
    out
}
pub fn paths(s: &str) -> Vec<(String, f64)> {
    let (percent, stem) = if let Some(x) = s.strip_suffix('%') {
        (true, x)
    } else if let Some(x) = s.strip_suffix('‰') {
        (false, x)
    } else {
        return Vec::new();
    };
    let stem = stem.strip_suffix(' ').unwrap_or(stem);
    let mut out = single(stem, percent);
    if !percent {
        return out;
    }
    for (marker, spoken) in [("-", "mínus "), ("−", "mínus "), ("±", "plus mínus "), ("+/-", "plus mínus ")] {
        if let Some(x) = stem.strip_prefix(marker) {
            let x = if marker == "±" || marker == "+/-" { x.strip_prefix(' ').unwrap_or(x) } else { x };
            for (v, w) in single(x, true) {
                out.push((format!("{spoken}{v}"), w))
            }
        }
    }
    for (a, b) in split_range(stem) {
        for (av, aw) in numeric(a) {
            for (bv, bw) in single(b, true) {
                out.push((format!("{av} až {bv}"), aw + bw))
            }
        }
    }
    out
}
