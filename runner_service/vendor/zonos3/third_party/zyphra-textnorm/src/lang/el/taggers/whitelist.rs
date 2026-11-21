use super::*;

pub(in crate::lang::el) fn lookup<'a>(table: &'a [(&str, &str)], s: &str) -> Option<&'a str> {
    table.iter().find(|(k, _)| *k == s).map(|(_, v)| *v)
}
pub(in crate::lang::el) fn whitelist(s: &str) -> Option<String> {
    for tab in [tables::ABBREVS, tables::TITLES] {
        if let Some(v) = lookup(tab, s) {
            return Some(spoken!("{v}"));
        }
    }
    if let Some((title, name)) = s.split_once(' ') {
        if !name.is_empty() && name.bytes().all(|b| b.is_ascii_alphabetic()) {
            if let Some(t) = lookup(tables::TITLES, title) {
                return Some(spoken!("{t} {name}"));
            }
        }
        if let Some(p) = lookup(tables::VER_PREFIX, title) {
            let parts = name.split('.');
            if (3..=5).contains(&parts.clone().count()) && parts.clone().all(|x| number::cardinal(x).is_some()) {
                if !writing_tags() {
                    return Some(String::new());
                }
                let values = parts.map(number::cardinal).collect::<Option<Vec<_>>>()?;
                return Some(format!("{p} {}", values.join(" τελεία ")));
            }
        }
    }
    for &(prefix, word) in tables::NO_PREFIX {
        if let Some(x) = s.strip_prefix(prefix) {
            let x = x.strip_prefix(' ').unwrap_or(x);
            if let Some(n) = number::cardinal(x) {
                return Some(spoken!("{word} {n}"));
            }
        }
    }
    match s {
        "&" => return Some(spoken!("και")),
        "©" => return Some(spoken!("πνευματικά δικαιώματα")),
        "§" => return Some(spoken!("παράγραφος")),
        "CO2" => return Some(spoken!("CO δύο")),
        _ => {}
    }
    for (p, w) in [("© ", "πνευματικά δικαιώματα"), ("§ ", "παράγραφος")] {
        if let Some(x) = s.strip_prefix(p) {
            return Some(spoken!("{w} {}", number::cardinal(x)?));
        }
    }
    for suffix in ["G", "K"] {
        if let Some(x) = s.strip_suffix(suffix) {
            let x = x.strip_suffix(' ').unwrap_or(x);
            if let Some(v) = number::cardinal(x) {
                return Some(spoken!("{v} {suffix}"));
            }
        }
    }
    let mut parts = s.split('-');
    if let (Some(a), Some(n), Some(b), None) = (parts.next(), parts.next(), parts.next(), parts.next()) {
        if (1..=3).contains(&a.len())
            && (1..=3).contains(&b.len())
            && a.bytes().all(|x| x.is_ascii_alphabetic())
            && b.bytes().all(|x| x.is_ascii_alphabetic())
        {
            if let Some(v) = number::cardinal(n) {
                return Some(spoken!("{a} {v} {b}"));
            }
        }
    }
    if let Some((a, b)) = s.split_once('^') {
        if !a.is_empty() && a.bytes().all(|c| c.is_ascii_alphabetic()) {
            return Some(spoken!(
                "{a} {}",
                match b {
                    "2" => "στο τετράγωνο",
                    "3" => "στον κύβο",
                    _ => return None,
                }
            ));
        }
        let n = number::cardinal(a)?;
        let exp = match b {
            "2" => "δευτέρα",
            "3" => "τρίτη",
            "4" => "τετάρτη",
            "5" => "πέμπτη",
            "6" => "έκτη",
            "7" => "εβδόμη",
            "8" => "ογδόη",
            "9" => "ενάτη",
            "10" => "δεκάτη",
            _ => return None,
        };
        return Some(spoken!("{n} στην {exp}"));
    }
    for (sep, word) in [("+", "συν"), (" = ", "ίσον"), ("=", "ίσον")] {
        if let Some((a, b)) = s.split_once(sep) {
            if let (Some(a), Some(b)) = (number::cardinal(a), number::cardinal(b)) {
                return Some(spoken!("{a} {word} {b}"));
            }
        }
    }
    None
}
