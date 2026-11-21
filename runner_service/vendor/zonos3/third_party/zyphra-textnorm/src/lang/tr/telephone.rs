//! Telephone chunk alternatives and their internal weights.
use super::graph_utils::{digit_sequence, is_positive, positive};
const PREFIXES: [&str; 8] = ["tel. ", "tel.: ", "tel: ", "tfn. ", "telefon: ", "telefon ", "GSM: ", "gsm: "];
const EXTENSIONS: [&str; 6] = [" ankn. ", " ankn ", " dahili: ", " dahili ", " ext. ", " ext "];
const SEPARATORS: [&str; 9] = [" ", "-", "  ", " -", "- ", "--", "   ", " --", " - "];

fn visit_chunk(s: &str, mut emit: impl FnMut(i64)) {
    if s.is_empty() || s.len() > 4 || !s.bytes().all(|c| c.is_ascii_digit()) {
        return;
    }
    if is_positive(s) {
        emit(0);
    }
    if s.strip_prefix('0').is_some_and(|rest| !rest.is_empty() && is_positive(rest)) {
        emit(0);
    }
    if s.len() >= 2 {
        emit(500_000);
    }
}
fn visit_body(s: &str, parts: usize, cost: i64, emit: &mut impl FnMut(i64)) {
    if s.is_empty() {
        if (2..=6).contains(&parts) {
            emit(cost);
        }
        return;
    }
    if parts >= 6 {
        return;
    }
    for end in 1..=s.len().min(4) {
        if !s.is_char_boundary(end) {
            continue;
        }
        visit_chunk(&s[..end], |w| {
            if end == s.len() {
                visit_body("", parts + 1, cost + w, emit);
            } else {
                let rest = &s[end..];
                for sep in SEPARATORS {
                    if let Some(tail) = rest.strip_prefix(sep) {
                        visit_body(tail, parts + 1, cost + w, emit);
                    }
                }
            }
        });
    }
}
fn visit_extensions(s: &str, allowed: bool, area_cost: i64, emit: &mut impl FnMut(i64)) {
    if allowed {
        visit_body(s, 0, area_cost, emit);
    }
    for prefix in EXTENSIONS {
        if let Some((body, n)) = s.split_once(prefix) {
            if n.len() <= 4 && is_positive(n) {
                visit_body(body, 0, area_cost, emit);
            }
        }
    }
}
fn visit_areas(s: &str, allowed: bool, emit: &mut impl FnMut(i64)) {
    visit_extensions(s, allowed, 0, emit);
    if let Some(rest) = s.strip_prefix('(') {
        if let Some((area, body)) = rest.split_once(')') {
            let body = body.strip_prefix(' ').unwrap_or(body);
            visit_chunk(area, |w| visit_extensions(body, true, w, emit));
        }
    }
}
fn visit_country(s: &str, allowed: bool, emit: &mut impl FnMut(i64)) {
    visit_areas(s, allowed, emit);
    if let Some(rest) = s.strip_prefix('+') {
        for n in 1..=3.min(rest.len()) {
            if !rest.is_char_boundary(n) || !is_positive(&rest[..n]) {
                continue;
            }
            let body = &rest[n..];
            visit_areas(body.strip_prefix(' ').unwrap_or(body), true, emit);
        }
    }
}
/// Visit every telephone path in source order, preserving chunk weights.
pub fn visit(s: &str, mut emit: impl FnMut(i64)) {
    for prefix in PREFIXES {
        if let Some(rest) = s.strip_prefix(prefix) {
            if rest.len() == 3 && rest.bytes().all(|c| c.is_ascii_digit()) {
                emit(0);
            }
        }
    }
    visit_country(s, false, &mut emit);
    for prefix in PREFIXES {
        if let Some(rest) = s.strip_prefix(prefix) {
            visit_country(rest, true, &mut emit);
        }
    }
}
fn chunk(s: &str) -> Vec<(String, i64)> {
    let mut out = Vec::new();
    if s.is_empty() || s.len() > 4 || !s.bytes().all(|c| c.is_ascii_digit()) {
        return out;
    }
    if let Some(n) = positive(s) {
        out.push((n, 0));
    }
    if let Some(rest) = s.strip_prefix('0') {
        if !rest.is_empty() {
            if let Some(n) = positive(rest) {
                out.push((format!("sıfır {n}"), 0));
            }
        }
    }
    if s.len() >= 2 {
        if let Some(n) = digit_sequence(s) {
            out.push((n, 500_000));
        }
    }
    out
}
fn body(s: &str) -> Vec<(String, i64)> {
    let mut result = Vec::new();
    fn rec(s: &str, parts: usize, text: String, cost: i64, out: &mut Vec<(String, i64)>) {
        if s.is_empty() {
            if (2..=6).contains(&parts) {
                out.push((text, cost));
            }
            return;
        }
        if parts >= 6 {
            return;
        }
        for end in 1..=s.len().min(4) {
            if !s.is_char_boundary(end) {
                continue;
            }
            for (word, w) in chunk(&s[..end]) {
                let text = if text.is_empty() { word } else { format!("{text} {word}") };
                if end == s.len() {
                    rec("", parts + 1, text, cost + w, out);
                    continue;
                }
                let rest = &s[end..];
                for sep in [" ", "-", "  ", " -", "- ", "--", "   ", " --", " - "] {
                    if let Some(tail) = rest.strip_prefix(sep) {
                        rec(tail, parts + 1, text.clone(), cost + w, out);
                    }
                }
            }
        }
    }
    rec(s, 0, String::new(), 0, &mut result);
    result
}
pub fn readings(s: &str) -> Vec<(String, i64)> {
    let mut out = Vec::new();
    let mut prefixes = vec![(s, String::new(), false)];
    for (p, v) in [
        ("tel. ", "telefon "),
        ("tel.: ", "telefon "),
        ("tel: ", "telefon "),
        ("tfn. ", "telefon "),
        ("telefon: ", "telefon "),
        ("telefon ", "telefon "),
        ("GSM: ", "g s m "),
        ("gsm: ", "g s m "),
    ] {
        if let Some(rest) = s.strip_prefix(p) {
            prefixes.push((rest, v.into(), true));
            if rest.len() == 3 {
                if let Some(n) = digit_sequence(rest) {
                    out.push((format!("{v}{n}"), 0));
                }
            }
        }
    }
    for (rest, prefix, has_prefix) in prefixes {
        let mut countries = vec![(rest, prefix.clone(), false)];
        if let Some(rest) = rest.strip_prefix('+') {
            for n in 1..=3.min(rest.len()) {
                if !rest.is_char_boundary(n) {
                    continue;
                }
                if let Some(word) = positive(&rest[..n]) {
                    let rest = &rest[n..];
                    let rest = rest.strip_prefix(' ').unwrap_or(rest);
                    countries.push((rest, format!("{prefix}artı {word} "), true));
                }
            }
        }
        for (rest, prefix, has_country) in countries {
            let mut areas = vec![(rest, prefix.clone(), 0, false)];
            if let Some(rest) = rest.strip_prefix('(') {
                if let Some((a, b)) = rest.split_once(')') {
                    let b = b.strip_prefix(' ').unwrap_or(b);
                    for (v, w) in chunk(a) {
                        areas.push((b, format!("{prefix}{v} "), w, true));
                    }
                }
            }
            for (rest, prefix, aw, has_area) in areas {
                let mut extensions = vec![(rest, String::new(), false)];
                for (p, v) in [
                    (" ankn. ", "anknytning"),
                    (" ankn ", "anknytning"),
                    (" dahili: ", "dahili"),
                    (" dahili ", "dahili"),
                    (" ext. ", "dahili"),
                    (" ext ", "dahili"),
                ] {
                    if let Some((b, n)) = rest.split_once(p) {
                        if n.len() <= 4 {
                            if let Some(n) = positive(n) {
                                extensions.push((b, format!(" {v} {n}"), true));
                            }
                        }
                    }
                }
                for (rest, ext, has_ext) in extensions {
                    if has_prefix || has_country || has_area || has_ext {
                        for (v, w) in body(rest) {
                            out.push((format!("{prefix}{v}{ext}"), aw + w));
                        }
                    }
                }
            }
        }
    }
    out
}
