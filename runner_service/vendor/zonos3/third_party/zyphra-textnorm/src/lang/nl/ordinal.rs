//! Productive Dutch ordinal number morphology.

use super::cardinal;

const SMALL: [&str; 20] = [
    "",
    "eerste",
    "tweede",
    "derde",
    "vierde",
    "vijfde",
    "zesde",
    "zevende",
    "achtste",
    "negende",
    "tiende",
    "elfde",
    "twaalfde",
    "dertiende",
    "veertiende",
    "vijftiende",
    "zestiende",
    "zeventiende",
    "achttiende",
    "negentiende",
];

fn under_thousand(n: usize) -> Option<String> {
    if n == 0 || n >= 1000 {
        return None;
    }
    if n < 20 {
        return Some(SMALL[n].to_owned());
    }
    if n < 100 {
        return Some(format!("{}ste", cardinal::raw(&n.to_string())?));
    }
    let hundreds = n / 100;
    let prefix =
        if hundreds == 1 { "honderd".to_owned() } else { format!("{}honderd", cardinal::raw(&hundreds.to_string())?) };
    let remainder = n % 100;
    if remainder == 0 { Some(format!("{prefix}ste")) } else { Some(format!("{prefix}{}", under_thousand(remainder)?)) }
}

/// Matches `all_numeric_ordinals`, before its written suffix.
pub(super) fn raw(input: &str) -> Option<String> {
    if input.is_empty() || input.len() > 9 || input.starts_with('0') || !input.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    let n: usize = input.parse().ok()?;
    if n < 1000 {
        return under_thousand(n);
    }
    if n >= 1_000_000 {
        if n % 1_000_000 != 0 || n > 999_000_000 {
            return None;
        }
        if n == 1_000_000 {
            return Some("miljoenste".to_owned());
        }
        return Some(format!("{} miljoenste", cardinal::raw(&(n / 1_000_000).to_string())?));
    }
    let thousands = n / 1000;
    let rem = n % 1000;
    if rem == 0 {
        if thousands == 1 {
            return Some("duizendste".to_owned());
        }
        return Some(format!("{}duizendste", cardinal::raw(&thousands.to_string())?));
    }
    let prefix = if thousands == 1 {
        "duizend".to_owned()
    } else {
        format!("{}duizend", cardinal::raw(&thousands.to_string())?)
    };
    Some(format!("{prefix} {}", under_thousand(rem)?))
}

pub(super) fn paths(input: &str) -> Vec<(String, f64)> {
    if raw(input).is_none() {
        return Vec::new();
    }
    let n: usize = input.parse().unwrap();
    if n < 1000 {
        return vec![(under_thousand(n).unwrap(), 0.0)];
    }
    if n >= 1_000_000 {
        let mut out = Vec::new();
        if n == 1_000_000 {
            out.push(("miljoenste".to_owned(), 0.0));
        }
        out.extend(
            cardinal::paths(&(n / 1_000_000).to_string()).into_iter().map(|(v, w)| (format!("{v} miljoenste"), w)),
        );
        return out;
    }
    let high = n / 1000;
    let rem = n % 1000;
    let mut out = Vec::new();
    if high == 1 {
        if rem == 0 {
            out.push(("duizendste".to_owned(), 0.0));
        } else {
            out.push((format!("duizend {}", under_thousand(rem).unwrap()), 0.0));
        }
    }
    for (v, w) in cardinal::paths(&high.to_string()) {
        if rem == 0 {
            out.push((format!("{v}duizendste"), w));
        } else {
            out.push((format!("{v}duizend {}", under_thousand(rem).unwrap()), w));
        }
    }
    out
}
