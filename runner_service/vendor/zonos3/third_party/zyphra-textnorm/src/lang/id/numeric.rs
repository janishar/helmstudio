use super::number::{Integer, cardinal, digits, grouped};

static QUANTITIES: std::sync::LazyLock<[Vec<(&'static str, &'static str)>; 256]> = std::sync::LazyLock::new(|| {
    let mut buckets: [Vec<(&'static str, &'static str)>; 256] = std::array::from_fn(|_| Vec::new());
    for (key, value) in
        include_str!("../../../data/id/numbers/quantities.tsv").lines().filter_map(|line| line.split_once('\t'))
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
        (r.strip_prefix(' ').unwrap_or(r), Some("kurang lebih"))
    } else {
        (s, None)
    }
}

fn prefix(sign: Option<&str>) -> String {
    sign.map(|v| format!("negative: \"{v}\" ")).unwrap_or_default()
}

pub(crate) fn cardinal_direct(s: &str) -> Option<String> {
    let (rest, neg) = sign(s);
    let value = Integer::grouped(rest)?;
    let mut tag = String::with_capacity(rest.len() * 24 + 64);
    tag.push_str("cardinal { ");
    if let Some(negative) = neg {
        tag.push_str("negative: \"");
        tag.push_str(negative);
        tag.push_str("\" ");
    }
    tag.push_str("integer: \"");
    value.write(&mut tag);
    tag.push_str("\" }");
    Some(tag)
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
            }
        }
    }
    if let Some((a, b)) = s.split_once(':') {
        if grouped(a).is_some() {
            let whole = grouped(b).is_some();
            let digitwise = digits(b).is_some();
            if whole {
                emit(0.1);
            }
            if digitwise {
                emit(0.2);
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

pub(crate) fn cardinal_tags(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    let (rest, neg) = sign(s);
    if let Some(n) = grouped(rest) {
        out.push((format!("cardinal {{ {}integer: \"{n}\" }}", prefix(neg)), 0.0));
    }
    if neg.is_some() {
        return out;
    }
    if s.starts_with('0') && s.len() >= 2 || s.len() >= 16 {
        if let Some(d) = digits(s) {
            out.push((format!("cardinal {{ integer: \"{d}\" }}"), 0.0));
        }
    }
    for sep in ["-", "–"] {
        if let Some((a, b)) = s.split_once(sep) {
            if let (Some(a), Some(b)) = (grouped(a), grouped(b)) {
                out.push((format!("cardinal {{ integer: \"{a} sampai {b}\" }}"), 0.0));
            }
        }
    }
    if let Some((a, b)) = s.split_once(':') {
        if let Some(a) = grouped(a) {
            if let Some(b) = grouped(b) {
                out.push((format!("cardinal {{ integer: \"{a} banding {b}\" }}"), 0.1));
            }
            if let Some(b) = digits(b) {
                out.push((format!("cardinal {{ integer: \"{a} banding {b}\" }}"), 0.2));
            }
        }
    }
    let parts: Vec<&str> = s.split(',').collect();
    if parts.len() >= 3 && parts.iter().all(|p| p.len() <= 3 && cardinal(p).is_some()) {
        let spelled: Vec<String> = parts.into_iter().map(|p| cardinal(p).unwrap()).collect();
        out.push((format!("cardinal {{ integer: \"{}\" }}", spelled.join(", ")), 0.0));
    }
    out
}

pub(crate) fn ordinal_recognize(s: &str, mut emit: impl FnMut(f64)) {
    let prefix = if let Some(r) = s.strip_prefix("ke-").or_else(|| s.strip_prefix("Ke-")) {
        Some(r)
    } else if let Some(r) = s.strip_prefix("ke").or_else(|| s.strip_prefix("Ke")) {
        Some(r)
    } else {
        None
    };
    if let Some(rest) = prefix {
        if grouped(rest).is_some() {
            emit(0.0);
        }
    }
}

pub(crate) fn ordinal_tags(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    let prefix = if let Some(r) = s.strip_prefix("ke-").or_else(|| s.strip_prefix("Ke-")) {
        Some(r)
    } else if let Some(r) = s.strip_prefix("ke").or_else(|| s.strip_prefix("Ke")) {
        Some(r)
    } else {
        None
    };
    if let Some(rest) = prefix {
        if let Some(n) = grouped(rest) {
            out.push((format!("ordinal {{ integer: \"{n}\" }}"), 0.0));
        }
    }
    out
}

pub(crate) fn decimal_direct(s: &str, _target_cost: crate::text::Cost) -> Option<String> {
    let (rest, neg) = sign(s);
    let p = prefix(neg);
    if let Some((a, b)) = rest.split_once(',') {
        if let (Some(a), Some(b)) = (grouped(a), digits(b)) {
            return Some(format!(
                "decimal {{ {p}integer_part: \"{a}\" fractional_part: \"{b}\" preserve_order: true }}"
            ));
        }
    }
    if let Some((a, b)) = rest.split_once('.') {
        if b.len() != 3 {
            if let (Some(a), Some(b)) = (cardinal(a), digits(b)) {
                return Some(format!(
                    "decimal {{ {p}integer_part: \"{a}\" point_part: \"{b}\" preserve_order: true }}"
                ));
            }
        }
    }
    None
}

pub(crate) fn decimal_recognize(s: &str, mut emit: impl FnMut(f64)) {
    let (s, _) = sign(s);
    let variants = std::iter::once((s, None)).chain(quantity_rows(s).filter_map(|(key, val)| {
        let stem = s.strip_suffix(key)?;
        Some((stem.strip_suffix(' ').unwrap_or(stem), Some(val)))
    }));
    for (number, q) in variants {
        if q.is_some() && number.len() <= 3 && number != "0" && cardinal(number).is_some() {
            emit(0.0);
        }
        if let Some((left, right)) = number.split_once(',') {
            if grouped(left).is_some() && digits(right).is_some() {
                emit(0.0);
            }
        }
        if let Some((left, right)) = number.split_once('.') {
            if right.len() != 3 && cardinal(left).is_some() && digits(right).is_some() {
                emit(0.05);
            }
        }
    }
}

pub(crate) fn decimal_tags(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    let (s, neg) = sign(s);
    let p = prefix(neg);
    let variants = std::iter::once((s, None)).chain(quantity_rows(s).filter_map(|(key, val)| {
        let stem = s.strip_suffix(key)?;
        Some((stem.strip_suffix(' ').unwrap_or(stem), Some(val)))
    }));
    for (number, q) in variants {
        let q_tag = q.map(|v| format!(" quantity: \"{v}\"")).unwrap_or_default();
        if q.is_some() && number.len() <= 3 && number != "0" {
            if let Some(n) = cardinal(number) {
                out.push((format!("decimal {{ {p}integer_part: \"{n}\"{q_tag} preserve_order: true }}"), 0.0));
            }
        }
        if let Some((left, right)) = number.split_once(',') {
            if let (Some(a), Some(b)) = (grouped(left), digits(right)) {
                out.push((
                    format!(
                        "decimal {{ {p}integer_part: \"{a}\" fractional_part: \"{b}\"{q_tag} preserve_order: true }}"
                    ),
                    0.0,
                ));
            }
        }
        if let Some((left, right)) = number.split_once('.') {
            if right.len() != 3 {
                if let (Some(a), Some(b)) = (cardinal(left), digits(right)) {
                    out.push((
                        format!(
                            "decimal {{ {p}integer_part: \"{a}\" point_part: \"{b}\"{q_tag} preserve_order: true }}"
                        ),
                        0.05,
                    ));
                }
            }
        }
    }
    out
}

const VULGAR: &[(&str, &str, &str)] = &[
    ("½", "satu", "dua"),
    ("¼", "satu", "empat"),
    ("¾", "tiga", "empat"),
    ("⅓", "satu", "tiga"),
    ("⅔", "dua", "tiga"),
    ("⅕", "satu", "lima"),
    ("⅖", "dua", "lima"),
    ("⅗", "tiga", "lima"),
    ("⅘", "empat", "lima"),
    ("⅙", "satu", "enam"),
    ("⅚", "lima", "enam"),
    ("⅛", "satu", "delapan"),
    ("⅜", "tiga", "delapan"),
    ("⅝", "lima", "delapan"),
    ("⅞", "tujuh", "delapan"),
    ("⅐", "satu", "tujuh"),
    ("⅑", "satu", "sembilan"),
    ("⅒", "satu", "sepuluh"),
];

fn mixed_vulgar(s: &str) -> Option<(String, &'static str)> {
    for &(symbol, _, _) in VULGAR {
        if let Some(integer) = s.strip_suffix(symbol) {
            return grouped(integer).map(|spoken| (spoken, symbol));
        }
    }
    None
}

pub(crate) fn fraction_recognize(s: &str, mut emit: impl FnMut(f64)) {
    let (s, _) = sign(s);
    let mut accept = |rest: &str| {
        if VULGAR.iter().any(|(v, _, _)| *v == rest) {
            emit(0.0);
            return;
        }
        for slash in [" / ", "/", "⁄"] {
            if let Some((a, b)) = rest.split_once(slash) {
                if cardinal(a).is_some() && cardinal(b).is_some() {
                    emit(0.0);
                    return;
                }
            }
        }
    };
    accept(s);
    if let Some((int_part, rest)) = s.split_once(' ') {
        if grouped(int_part).is_some() {
            accept(rest);
        }
    }
    if !s.contains(' ') && mixed_vulgar(s).is_some() {
        emit(0.0);
    }
}

pub(crate) fn fraction_tags(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    let (s, neg) = sign(s);
    let p = prefix(neg);
    let mut build = |int_prefix: Option<&str>, rest: &str| {
        let int_tag = int_prefix.map(|v| format!("integer_part: \"{v}\" ")).unwrap_or_default();
        for &(v, num, den) in VULGAR {
            if rest == v {
                out.push((
                    format!(
                        "fraction {{ {p}{int_tag}numerator: \"{num}\" denominator: \"{den}\" preserve_order: true }}"
                    ),
                    0.0,
                ));
                return;
            }
        }
        for slash in [" / ", "/", "⁄"] {
            if let Some((a, b)) = rest.split_once(slash) {
                if let (Some(num), Some(den)) = (cardinal(a), cardinal(b)) {
                    out.push((
                        format!("fraction {{ {p}{int_tag}numerator: \"{num}\" denominator: \"{den}\" preserve_order: true }}"),
                        0.0,
                    ));
                    return;
                }
            }
        }
    };
    build(None, s);
    if !s.contains(' ') {
        if let Some((integer, symbol)) = mixed_vulgar(s) {
            build(Some(&integer), symbol);
        }
    }
    if let Some((int_part, rest)) = s.split_once(' ') {
        if let Some(int_spelled) = grouped(int_part) {
            build(Some(&int_spelled), rest);
        }
    }
    out
}
