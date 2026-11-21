use super::number::{cardinal, digits, grouped};
static QUANTITIES: std::sync::LazyLock<[Vec<(&'static str, &'static str)>; 256]> = std::sync::LazyLock::new(|| {
    let mut buckets: [Vec<(&'static str, &'static str)>; 256] = std::array::from_fn(|_| Vec::new());
    for (key, value) in
        include_str!("../../../data/ms/numbers/quantities.tsv").lines().filter_map(|line| line.split_once('\t'))
    {
        if let Some(&last) = key.as_bytes().last() {
            buckets[last as usize].push((key, value));
        }
    }
    buckets
});
pub(crate) fn quantity_rows(s: &str) -> impl Iterator<Item = (&'static str, &'static str)> {
    let rows = s.as_bytes().last().map(|&last| QUANTITIES[last as usize].as_slice()).unwrap_or(&[]);
    rows.iter().copied()
}

pub(crate) fn sign(s: &str) -> (&str, Option<&'static str>) {
    if let Some(r) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) {
        (r, Some("true"))
    } else if let Some(r) = s.strip_prefix('~') {
        (r.strip_prefix(' ').unwrap_or(r), Some("sekitar"))
    } else if let Some(r) = s.strip_prefix('±') {
        (r.strip_prefix(' ').unwrap_or(r), Some("lebih kurang"))
    } else {
        (s, None)
    }
}
fn prefix(sign: Option<&str>) -> String {
    sign.map(|v| format!("negative: \"{v}\" ")).unwrap_or_default()
}
fn wrap(kind: &str, body: &str) -> String {
    format!("{kind} {{ {body} }}")
}
pub(crate) fn cardinal_direct(s: &str) -> Option<String> {
    let (rest, neg) = sign(s);
    let n = grouped(rest)?;
    Some(format!("cardinal {{ {}integer: \"{n}\" }}", prefix(neg)))
}
pub(crate) fn cardinal_recognize(s: &str, mut emit: impl FnMut(f64)) {
    let (rest, neg) = sign(s);
    if grouped(rest).is_some() {
        emit(0.0);
    }
    if neg.is_some() {
        return;
    }
    if (s.starts_with('0') && s.len() >= 2 || s.len() >= 16) && digits(s).is_some() {
        emit(0.0);
    }
    if s.bytes().all(|b| b.is_ascii_digit()) {
        return;
    }
    for sep in ["-", "–"] {
        if let Some((a, b)) = s.split_once(sep) {
            if grouped(a).is_some() && grouped(b).is_some() {
                emit(0.0);
                emit(0.0);
            }
        }
    }
    if let Some((a, b)) = s.split_once(':') {
        if grouped(a).is_some() {
            let whole = grouped(b).is_some();
            let digitwise = digits(b).is_some();
            for _ in 0..2 {
                if whole {
                    emit(0.1)
                }
                if digitwise {
                    emit(0.2)
                }
            }
        }
    }
    for sep in ['+', '='] {
        if let Some((a, b)) = s.split_once(sep) {
            let (a, b) = (a.strip_suffix(' ').unwrap_or(a), b.strip_prefix(' ').unwrap_or(b));
            if grouped(a).is_some() && grouped(b).is_some() {
                emit(0.1);
            }
        }
    }
    let mut count = 0;
    if s.split(',').all(|part| {
        count += 1;
        part.len() <= 3 && cardinal(part).is_some()
    }) && count >= 3
    {
        emit(0.0);
    }
}

pub(crate) fn decimal_recognize(s: &str, mut emit: impl FnMut(f64)) {
    let (s, _) = sign(s);
    let variants = std::iter::once((s, false)).chain(quantity_rows(s).filter_map(|(key, _)| {
        let stem = s.strip_suffix(key)?;
        Some((stem.strip_suffix(' ').unwrap_or(stem), true))
    }));
    for (number, q) in variants {
        if q && number.len() <= 3 && number != "0" && cardinal(number).is_some() {
            emit(0.0);
        }
        for (index, c) in number.char_indices() {
            if c != '.' && c != ',' {
                continue;
            }
            let (left, right) = (&number[..index], &number[index + 1..]);
            if right.len() == 3 && c == ',' {
                continue;
            }
            if grouped(left).is_some() && digits(right).is_some() {
                emit(if right.len() == 3 { 0.05 } else { 0.0 });
            }
        }
    }
}

pub(crate) fn fraction_recognize(s: &str, mut emit: impl FnMut(f64)) {
    let (s, _) = sign(s);
    let mut accept = |rest: &str| {
        let vulgar = matches!(
            rest,
            "½" | "¼"
                | "¾"
                | "⅓"
                | "⅔"
                | "⅕"
                | "⅖"
                | "⅗"
                | "⅘"
                | "⅙"
                | "⅚"
                | "⅛"
                | "⅜"
                | "⅝"
                | "⅞"
                | "⅐"
                | "⅑"
                | "⅒"
        );
        let ordinary = rest
            .split_once(" / ")
            .or_else(|| rest.split_once('/'))
            .or_else(|| rest.split_once('⁄'))
            .is_some_and(|(a, b)| cardinal(a).is_some() && cardinal(b).is_some());
        if vulgar || ordinary {
            emit(0.0);
        }
    };
    accept(s);
    for (i, c) in s.char_indices() {
        if c == ' ' && grouped(&s[..i]).is_some() {
            accept(&s[i + 1..]);
        }
        if "½¼¾⅓⅔⅕⅖⅗⅘⅙⅚⅛⅜⅝⅞⅐⅑⅒".contains(c) && i > 0 && grouped(&s[..i]).is_some() {
            accept(&s[i..]);
        }
    }
}

pub(crate) fn cardinal_tags(s: &str) -> Vec<(String, f64)> {
    if super::lazy::recognizing() {
        let mut out = Vec::new();
        cardinal_recognize(s, |weight| out.push((String::new(), weight)));
        return out;
    }
    let mut result = Vec::new();
    let (rest, neg) = sign(s);
    if let Some(n) = grouped(rest) {
        result.push((wrap("cardinal", &format!("{}integer: \"{n}\"", prefix(neg))), 0.0));
    }
    if neg.is_none() {
        if (s.starts_with('0') && s.len() >= 2 || s.len() >= 16) && s.bytes().all(|x| x.is_ascii_digit()) {
            if let Some(n) = digits(s) {
                result.push((wrap("cardinal", &format!("integer: \"{n}\"")), 0.0));
            }
        }
        for (sep, word, weight) in [
            ("-", "sampai", 0.0),
            ("–", "sampai", 0.0),
            ("-", "hingga", 0.0),
            ("–", "hingga", 0.0),
            (":", "nisbah", 0.1),
            (":", "banding", 0.1),
            ("+", "tambah", 0.1),
            ("=", "sama dengan", 0.1),
        ] {
            if let Some((a, b)) = s.split_once(sep) {
                let (a, b) = if sep == "+" || sep == "=" {
                    (a.strip_suffix(' ').unwrap_or(a), b.strip_prefix(' ').unwrap_or(b))
                } else {
                    (a, b)
                };
                if let Some(a) = grouped(a) {
                    if let Some(b) = grouped(b) {
                        result.push((wrap("cardinal", &format!("integer: \"{a} {word} {b}\"")), weight));
                    }
                    if sep == ":" {
                        if let Some(b) = digits(b) {
                            result.push((wrap("cardinal", &format!("integer: \"{a} {word} {b}\"")), weight + 0.1));
                        }
                    }
                }
            }
        }
        let list: Vec<_> = s.split(',').collect();
        if list.len() >= 3 && list.iter().all(|p| p.len() <= 3) {
            if let Some(v) = list.iter().map(|p| cardinal(p)).collect::<Option<Vec<_>>>() {
                result.push((wrap("cardinal", &format!("integer: \"{}\"", v.join(", "))), 0.0));
            }
        }
    }
    result
}

/// Render the first decimal reading with the winning cost, preserving union order.
pub(crate) fn decimal_direct(s: &str, target: crate::text::Cost) -> Option<String> {
    let (s, neg) = sign(s);
    let variants = std::iter::once((s, None)).chain(quantity_rows(s).filter_map(|(key, value)| {
        let stem = s.strip_suffix(key)?;
        Some((stem.strip_suffix(' ').unwrap_or(stem), Some(value)))
    }));
    for (number, q) in variants {
        if target == crate::text::cost(1.1) && q.is_some() && number.len() <= 3 && number != "0" {
            if let Some(n) = cardinal(number) {
                return Some(format!(
                    "decimal {{ {}integer_part: \"{n}\" quantity: \"{}\" preserve_order: true }}",
                    prefix(neg),
                    q?
                ));
            }
        }
        for (index, c) in number.char_indices() {
            if c != '.' && c != ',' {
                continue;
            }
            let (left, right) = (&number[..index], &number[index + 1..]);
            if right.len() == 3 && c == ',' {
                continue;
            }
            let weight = if right.len() == 3 { 0.05 } else { 0.0 };
            if target != crate::text::cost(1.1 + weight) {
                continue;
            }
            if let (Some(a), Some(b)) = (grouped(left), digits(right)) {
                let quantity = q.map(|v| format!(" quantity: \"{v}\"")).unwrap_or_default();
                return Some(format!(
                    "decimal {{ {}integer_part: \"{a}\" fractional_part: \"{b}\"{quantity} preserve_order: true }}",
                    prefix(neg)
                ));
            }
        }
    }
    None
}
pub(crate) fn decimal_tags(s: &str) -> Vec<(String, f64)> {
    if super::lazy::recognizing() {
        let mut out = Vec::new();
        decimal_recognize(s, |weight| out.push((String::new(), weight)));
        return out;
    }
    let (s, neg) = sign(s);
    let mut result = Vec::new();
    let mut variants = vec![(s, None)];
    for (key, val) in quantity_rows(s) {
        if let Some(stem) = s.strip_suffix(key) {
            variants.push((stem.strip_suffix(' ').unwrap_or(stem), Some(val)));
        }
    }
    for (number, q) in variants {
        let quantity = q.map(|v| format!(" quantity: \"{v}\"")).unwrap_or_default();
        if q.is_some() && number.len() <= 3 && number != "0" {
            if let Some(n) = cardinal(number) {
                result.push((
                    wrap("decimal", &format!("{}integer_part: \"{n}\"{quantity} preserve_order: true", prefix(neg))),
                    0.0,
                ));
            }
        }
        for (index, c) in number.char_indices() {
            if c != '.' && c != ',' {
                continue;
            }
            let (left, right) = (&number[..index], &number[index + 1..]);
            if right.len() == 3 && c == ',' {
                continue;
            }
            if let (Some(a), Some(b)) = (grouped(left), digits(right)) {
                result.push((
                    wrap(
                        "decimal",
                        &format!(
                            "{}integer_part: \"{a}\" fractional_part: \"{b}\"{quantity} preserve_order: true",
                            prefix(neg)
                        ),
                    ),
                    if right.len() == 3 { 0.05 } else { 0.0 },
                ));
            }
        }
    }
    result
}

pub(crate) fn fraction_tags(s: &str) -> Vec<(String, f64)> {
    if super::lazy::recognizing() {
        let mut out = Vec::new();
        fraction_recognize(s, |weight| out.push((String::new(), weight)));
        return out;
    }
    let (s, neg) = sign(s);
    let mut result = Vec::new();
    let mut starts = vec![(None, s)];
    for (i, c) in s.char_indices() {
        if c == ' ' {
            if let Some(v) = grouped(&s[..i]) {
                starts.push((Some(v), &s[i + 1..]));
            }
        }
        if "½¼¾⅓⅔⅕⅖⅗⅘⅙⅚⅛⅜⅝⅞⅐⅑⅒".contains(c) && i > 0 {
            if let Some(v) = grouped(&s[..i]) {
                starts.push((Some(v), &s[i..]));
            }
        }
    }
    for (int, rest) in starts {
        let vulgar = match rest {
            "½" => Some(("satu", "dua")),
            "¼" => Some(("satu", "empat")),
            "¾" => Some(("tiga", "empat")),
            "⅓" => Some(("satu", "tiga")),
            "⅔" => Some(("dua", "tiga")),
            "⅕" => Some(("satu", "lima")),
            "⅖" => Some(("dua", "lima")),
            "⅗" => Some(("tiga", "lima")),
            "⅘" => Some(("empat", "lima")),
            "⅙" => Some(("satu", "enam")),
            "⅚" => Some(("lima", "enam")),
            "⅛" => Some(("satu", "lapan")),
            "⅜" => Some(("tiga", "lapan")),
            "⅝" => Some(("lima", "lapan")),
            "⅞" => Some(("tujuh", "lapan")),
            "⅐" => Some(("satu", "tujuh")),
            "⅑" => Some(("satu", "sembilan")),
            "⅒" => Some(("satu", "sepuluh")),
            _ => None,
        };
        let pair = if let Some((a, b)) = vulgar {
            Some((a.to_owned(), b.to_owned()))
        } else {
            rest.split_once(" / ")
                .or_else(|| rest.split_once('/'))
                .or_else(|| rest.split_once('⁄'))
                .and_then(|(a, b)| Some((cardinal(a)?, cardinal(b)?)))
        };
        if let Some((a, b)) = pair {
            let integer = int.map(|v| format!("integer_part: \"{v}\" ")).unwrap_or_default();
            result.push((
                wrap(
                    "fraction",
                    &format!("{}{integer}numerator: \"{a}\" denominator: \"{b}\" preserve_order: true", prefix(neg)),
                ),
                0.0,
            ));
        }
    }
    result
}

pub(crate) fn ordinal(s: &str) -> Option<String> {
    let n = s
        .strip_prefix("ke-")
        .or_else(|| s.strip_prefix("Ke-"))
        .or_else(|| s.strip_prefix("ke"))
        .or_else(|| s.strip_prefix("Ke"))?;
    let n = grouped(n)?;
    if super::lazy::recognizing() { Some(String::new()) } else { Some(wrap("ordinal", &format!("integer: \"{n}\""))) }
}
