use super::number::positive;

pub(super) fn digits(s: &str) -> Option<String> {
    if s.is_empty() || !s.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    if !super::building() {
        return Some(String::new());
    }
    const DIGITS: [&str; 10] = ["nula", "jeden", "dva", "tri", "štyri", "päť", "šesť", "sedem", "osem", "deväť"];
    let mut out = String::new();
    for b in s.bytes() {
        if !out.is_empty() {
            out.push(' ');
        }
        out.push_str(DIGITS[(b - b'0') as usize]);
    }
    Some(out)
}

pub(super) fn plain(s: &str) -> Option<String> {
    let (prefix, s) =
        if let Some(s) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) { ("mínus ", s) } else { ("", s) };
    let direct = positive(s)
        .or_else(|| {
            let stripped = s.trim_start_matches('0');
            if stripped.len() == s.len() || stripped.is_empty() { None } else { positive(stripped) }
        })
        .or_else(|| super::number::grouped(s, &[' ', '\u{a0}', '\u{202f}', '.']))?;
    Some(spoken!("{prefix}{direct}"))
}

pub(super) fn cardinal(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    cardinal_each(s, |value, weight| out.push((value, weight)));
    out
}
pub(super) fn cardinal_each(s: &str, mut emit: impl FnMut(String, f64)) {
    if let Some(x) = plain(s) {
        emit(x, 1.1);
    }
    if s.bytes().all(|b| b.is_ascii_digit()) {
        return;
    }
    for (symbol, word) in [('~', "približne "), ('±', "plus-mínus ")] {
        if let Some(x) = s.strip_prefix(symbol).and_then(plain) {
            emit(spoken!("{word}{x}"), 1.05);
        }
    }
    for (i, c) in s.char_indices() {
        if i == 0 {
            continue;
        }
        if "-–—".contains(c) {
            let a = &s[..i];
            let b = &s[i + c.len_utf8()..];
            if let (Some(x), Some(y)) =
                (plain(a.strip_suffix(' ').unwrap_or(a)), plain(b.strip_prefix(' ').unwrap_or(b)))
            {
                emit(spoken!("{x} až {y}"), 1.08);
            }
        }
        if c == ':' {
            if let (Some(x), Some(y)) = (positive(&s[..i]), positive(&s[i + 1..])) {
                emit(spoken!("{x} dvojbodka {y}"), 0.0);
            }
        }
    }
    if s.contains(',') {
        let parts =
            s.split(',').enumerate().map(|(i, x)| plain(if i > 0 { x.strip_prefix(' ').unwrap_or(x) } else { x }));
        if super::building() {
            if let Some(parts) = parts.collect::<Option<Vec<_>>>() {
                emit(parts.join(", "), 1.09);
            }
        } else if parts.into_iter().all(|p| p.is_some()) {
            emit(String::new(), 1.09);
        }
    }
    let is_letter = |c: char| {
        if c.is_ascii() {
            c.is_ascii_alphabetic()
        } else {
            "áäčďéíĺľňóôŕšťúýžÁÄČĎÉÍĹĽŇÓÔŔŠŤÚÝŽ".contains(c)
        }
    };
    let letters = |x: &str| !x.is_empty() && x.chars().all(is_letter);
    if s.contains('-') {
        let mut parts = s.split('-');
        let first = parts.next().unwrap_or("");
        let second = parts.next().unwrap_or("");
        let third = parts.next();
        if parts.next().is_none() {
            if let Some(third) = third {
                if letters(second) {
                    if let (Some(a), Some(b)) = (plain(first), plain(third)) {
                        emit(spoken!("{a} {second} {b}"), 1.15);
                    }
                }
            } else if letters(first) {
                if let Some(n) = plain(second) {
                    emit(spoken!("{first} {n}"), 1.15);
                }
            }
        }
    }
    if let Some((i, c)) = s.char_indices().rev().find(|&(_, c)| !is_letter(c)) {
        let start = i + c.len_utf8();
        if start < s.len() {
            if let Some(n) = plain(&s[..start]) {
                emit(spoken!("{n} {}", &s[start..]), 1.15);
            }
        }
    }
}

pub(super) fn decimal(s: &str) -> Option<String> {
    let (negative, s) =
        if let Some(x) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) { ("mínus ", x) } else { ("", s) };
    let mut suffix = "";
    let mut input = s;
    for (markers, word) in [
        (&["mld.", "mld", "miliardy", "miliárd", "млрд.", "млрд"][..], "miliardy"),
        (&["mil.", "mil", "mln", "milióny", "miliónov", "млн.", "млн"][..], "miliónov"),
    ] {
        if let Some(m) = markers.iter().find(|m| s.ends_with(**m)) {
            input = s[..s.len() - m.len()].strip_suffix(' ').unwrap_or(&s[..s.len() - m.len()]);
            suffix = word;
            break;
        }
    }
    let split = input.find([',', '.']);
    if split.is_none() {
        if suffix.is_empty() {
            return None;
        }
        return Some(spoken!("{negative}{} {}", plain(input)?, if suffix == "miliardy" { "miliárd" } else { suffix }));
    }
    let i = split?;
    let a = &input[..i];
    let b = &input[i + 1..];
    let a_num = positive(a)?;
    if a.len() > 9 || b.is_empty() || b.len() > 4 || !b.bytes().all(|x| x.is_ascii_digit()) {
        return None;
    }
    let n: usize = b.parse().ok()?;
    if n == 0 || (b.len() == 4 && n >= 1000) {
        return None;
    }
    if !super::building() {
        return Some(String::new());
    }
    let integer = match a {
        "0" => "nula celých".to_string(),
        "1" => "jedna celá".into(),
        "2" => "dve celé".into(),
        "3" => "tri celé".into(),
        "4" => "štyri celé".into(),
        _ => spoken!("{a_num} celých"),
    };
    let names = match b.len() {
        1 => ["desatina", "desatiny", "desatín"],
        2 => ["stotina", "stotiny", "stotín"],
        3 => ["tisícina", "tisíciny", "tisícin"],
        _ => ["desaťtisícina", "desaťtisíciny", "desaťtisícin"],
    };
    let (value, form) = match n {
        1 => ("jedna".to_string(), names[0]),
        2 => ("dve".into(), names[1]),
        3..=4 => (positive(&n.to_string())?, names[1]),
        _ => (positive(&n.to_string())?, names[2]),
    };
    Some(spoken!(
        "{negative}{integer} {value} {form}{}",
        if suffix.is_empty() { String::new() } else { spoken!(" {suffix}") }
    ))
}
