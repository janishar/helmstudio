use super::*;

pub(in crate::lang::el) const MONTHS: [&str; 12] = [
    "Ιανουαρίου",
    "Φεβρουαρίου",
    "Μαρτίου",
    "Απριλίου",
    "Μαΐου",
    "Ιουνίου",
    "Ιουλίου",
    "Αυγούστου",
    "Σεπτεμβρίου",
    "Οκτωβρίου",
    "Νοεμβρίου",
    "Δεκεμβρίου",
];
pub(in crate::lang::el) fn day(s: &str) -> Option<String> {
    if s.len() > 2 || s.is_empty() || !s.bytes().all(|c| c.is_ascii_digit()) {
        return None;
    }
    let n: usize = s.parse().ok()?;
    if !(1..=31).contains(&n) || (s.len() == 2 && s.starts_with('0') && n >= 10) {
        return None;
    }
    if n == 1 { Some(spoken!("πρώτη")) } else { number::cardinal(s.trim_start_matches('0')) }
}
pub(in crate::lang::el) fn month(s: &str) -> Option<&'static str> {
    if s.len() > 2 || s.is_empty() || !s.bytes().all(|c| c.is_ascii_digit()) {
        return None;
    }
    let n: usize = s.parse().ok()?;
    if !(1..=12).contains(&n) {
        return None;
    }
    Some(MONTHS[n - 1])
}
pub(in crate::lang::el) fn year(s: &str) -> Option<String> {
    if s.len() != 4 {
        return None;
    }
    number::cardinal(s)
}
pub(in crate::lang::el) fn iso_date(s: &str) -> Option<(String, &'static str, String)> {
    let mut parts = s.split('-');
    let (y, m, d) = (parts.next()?, parts.next()?, parts.next()?);
    if parts.next().is_some() || m.len() != 2 || d.len() != 2 {
        return None;
    }
    Some((year(y)?, month(m)?, day(d)?))
}
pub(in crate::lang::el) fn date(s: &str) -> Option<String> {
    let mut parts = s.split(['/', '.', '-']);
    if let (Some(d), Some(m), Some(y), None) = (parts.next(), parts.next(), parts.next(), parts.next()) {
        if let (Some(d), Some(m), Some(y)) = (day(d), month(m), year(y)) {
            return Some(spoken!("{d} {m} {y}"));
        }
    }
    let mut parts = s.split(' ');
    if let (Some(d), Some(m), Some(y), None) = (parts.next(), parts.next(), parts.next(), parts.next()) {
        if MONTHS.contains(&m) {
            return Some(spoken!("{} {} {}", day(d)?, m, year(y)?));
        }
    }
    for sep in [" - ", " – ", "-", "–", "—"] {
        if let Some((a, b)) = s.split_once(sep) {
            if let (Some(a), Some(b)) = (year(a), year(b)) {
                return Some(spoken!("{a} έως {b}"));
            }
        }
    }
    for suf in ["'s", "s", "S"] {
        if let Some(x) = s.strip_suffix(suf) {
            return Some(spoken!("δεκαετία του {}", year(x)?));
        }
    }
    for pre in ["δεκαετία του '", "δεκαετία του "] {
        if let Some(x) = s.strip_prefix(pre) {
            if x.len() == 2 || x.len() == 4 {
                return Some(spoken!("δεκαετία του {}", number::cardinal(x)?));
            }
        }
    }
    for (suffix, word) in [
        ("π.Κ.Χ.", "προ Χριστού"),
        ("π.Χ.", "προ Χριστού"),
        ("π.Χ", "προ Χριστού"),
        ("πΧ", "προ Χριστού"),
        ("B.C.E.", "προ Χριστού"),
        ("B.C.", "προ Χριστού"),
        ("BCE", "προ Χριστού"),
        ("BC", "προ Χριστού"),
        ("μ.Χ.", "μετά Χριστόν"),
        ("μ.Χ", "μετά Χριστόν"),
        ("μΧ", "μετά Χριστόν"),
        ("Κ.Χ.", "μετά Χριστόν"),
        ("A.D.", "μετά Χριστόν"),
        ("C.E.", "μετά Χριστόν"),
        ("AD", "μετά Χριστόν"),
        ("CE", "μετά Χριστόν"),
    ] {
        if let Some(x) = s.strip_suffix(suffix) {
            let x = x.strip_suffix(' ').unwrap_or(x);
            return Some(spoken!("{} {word}", number::cardinal(x)?));
        }
    }
    None
}
