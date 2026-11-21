//! Dutch vulgar, slash and mixed fractions.
use super::cardinal;

fn vulgar(s: &str) -> Option<&'static str> {
    Some(match s {
        "½" => "een half",
        "⅓" => "een derde",
        "⅔" => "twee derde",
        "¼" => "een kwart",
        "¾" => "drie kwart",
        "⅕" => "een vijfde",
        "⅖" => "twee vijfde",
        "⅗" => "drie vijfde",
        "⅘" => "vier vijfde",
        "⅙" => "een zesde",
        "⅚" => "vijf zesde",
        "⅛" => "een achtste",
        "⅜" => "drie achtste",
        "⅝" => "vijf achtste",
        "⅞" => "zeven achtste",
        "⅑" => "een negende",
        "⅒" => "een tiende",
        _ => return None,
    })
}

fn simple(s: &str) -> Option<String> {
    if let Some(v) = vulgar(s) {
        return Some(v.to_owned());
    }
    let (num, den) = s.split_once('/')?;
    if num.len() > 2 || num.starts_with('0') {
        return None;
    }
    let n = cardinal::raw(num)?;
    let d = match den {
        "2" => "half",
        "3" => "derde",
        "4" => "kwart",
        "5" => "vijfde",
        "6" => "zesde",
        "7" => "zevende",
        "8" => "achtste",
        "9" => "negende",
        "10" => "tiende",
        "12" => "twaalfde",
        "16" => "zestiende",
        "20" => "twintigste",
        "100" => "honderdste",
        _ => return None,
    };
    Some(format!("{n} {d}"))
}

/// Returns every spoken form and its internal tropical weight.
pub(super) fn parse(s: &str) -> Vec<(String, f64)> {
    if let Some(rest) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) {
        return positive(rest)
            .into_iter()
            .map(|(v, w)| (format!("min {v}"), w + 0.98 - if simple(rest).is_some() { 0.01 } else { 0.0 }))
            .collect();
    }
    positive(s).into_iter().map(|(v, w)| (v, w + 0.99)).collect()
}

// Relative weight: mixed is zero, standalone is 0.01.
fn positive(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    if let Some((whole, fraction)) = s.rsplit_once(' ') {
        if let Some(f) = simple(fraction) {
            for (w, c) in super::numeric::integer(whole) {
                out.push((format!("{w} en {f}"), c));
            }
        }
    }
    if let Some((i, c)) = s.char_indices().last() {
        if let Some(f) = vulgar(&s[i..i + c.len_utf8()]) {
            for (w, c) in super::numeric::integer(&s[..i]) {
                out.push((format!("{w} en {f}"), c));
            }
        }
    }
    if let Some(v) = simple(s) {
        out.push((v, 0.01));
    }
    out
}
