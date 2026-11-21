use super::{number::positive, numeric::digits};
fn is_letter(c: char) -> bool {
    if c.is_ascii() {
        c.is_ascii_alphabetic()
    } else {
        "áäčďéíĺľňóôŕšťúýžÁÄČĎÉÍĹĽŇÓÔŔŠŤÚÝŽ".contains(c)
    }
}
fn letters(s: &str) -> bool {
    !s.is_empty() && s.chars().all(is_letter)
}
pub(super) fn whitelist(s: &str) -> Vec<String> {
    let mut out = Vec::new();
    whitelist_each(s, |value| out.push(value));
    out
}
pub(super) fn whitelist_each(s: &str, mut emit: impl FnMut(String)) {
    for (a, b) in [
        ("&", "a"),
        ("©", "autorské práva"),
        ("§", "paragraf"),
        ("№", "číslo"),
        ("č.", "číslo"),
        ("p.", "pán"),
        ("pani", "pani"),
        ("Dr.", "doktor"),
        ("Dr", "doktor"),
        ("dr.", "doktor"),
        ("dr", "doktor"),
        ("Doc.", "docent"),
        ("doc.", "docent"),
        ("Prof.", "profesor"),
        ("prof.", "profesor"),
        ("Ing.", "inžinier"),
        ("Mgr.", "magister"),
        ("Bc.", "bakalár"),
        ("MUDr.", "doktor"),
        ("JUDr.", "doktor"),
        ("PhDr.", "doktor"),
        ("PhD.", "pé há dé"),
        ("napr.", "napríklad"),
        ("atď.", "a tak ďalej"),
        ("resp.", "respektíve"),
        ("t. j.", "to jest"),
        ("t.j.", "to jest"),
        ("tzv.", "takzvaný"),
        ("str.", "strana"),
        ("v.", "verzia"),
    ] {
        if s == a {
            emit(if super::building() { b.into() } else { String::new() })
        }
    }
    for (a, b) in [("©", "autorské práva"), ("§", "paragraf"), ("№", "číslo"), ("č.", "číslo"), ("str.", "strana")]
    {
        if let Some(x) = s.strip_prefix(a).and_then(|x| positive(x.trim_start_matches(' '))) {
            emit(spoken!("{b} {x}"));
        }
    }
    if s.chars().next().is_some_and(is_letter) {
        for (i, c) in s.char_indices() {
            if c.is_ascii_digit() && i > 0 && letters(&s[..i]) {
                let rest = &s[i..];
                let end = rest.find(|c: char| !c.is_ascii_digit()).unwrap_or(rest.len());
                let tail = &rest[end..];
                if tail.is_empty() || letters(tail) {
                    if let Some(d) = digits(&rest[..end]) {
                        emit(spoken!(
                            "{} {d}{}",
                            &s[..i],
                            if tail.is_empty() { String::new() } else { spoken!(" {tail}") }
                        ));
                    }
                }
                break;
            }
        }
    }
    for c in ['G', 'K'] {
        if let Some(x) = s.strip_suffix(c).and_then(positive) {
            emit(spoken!("{x} {c}"));
        }
    }
    if s.contains('-') {
        let mut parts = s.split('-');
        let first = parts.next().unwrap_or("");
        let second = parts.next().unwrap_or("");
        let third = parts.next();
        if parts.next().is_none() && letters(first) && third.is_none_or(letters) {
            if let Some(n) = positive(second) {
                emit(spoken!("{first} {n}{}", if let Some(last) = third { spoken!(" {last}") } else { String::new() }));
            }
        }
    }
    for (suffix, exp) in [("^2", "druhú"), ("²", "druhú"), ("^3", "tretiu"), ("³", "tretiu")] {
        if let Some(x) = s.strip_suffix(suffix) {
            if letters(x) || positive(x).is_some() {
                let b = positive(x).unwrap_or_else(|| x.into());
                emit(spoken!("{b} na {exp}"));
            }
        }
    }
    if let Some((a, b)) = s.split_once('^') {
        if b != "2" && b != "3" && (letters(a) || positive(a).is_some()) {
            if let Some(b) = positive(b) {
                emit(spoken!("{} na {b}", positive(a).unwrap_or_else(|| a.into())));
            }
        }
    }
}
