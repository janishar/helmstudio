//! Tamil fraction classification and fused mixed halves.
use super::number::{digit, final_graph};

fn f(k: &str, v: &str) -> String {
    format!("{k}: \"{v}\"")
}
fn num(s: &str) -> Option<String> {
    final_graph(s).map(|x| x.0)
}
fn ascii(s: &str) -> Option<String> {
    s.chars().map(|c| digit(c).map(|d| (b'0' + d) as char)).collect()
}
fn glyph(c: char) -> Option<(u8, u8)> {
    Some(match c {
        '½' => (1, 2),
        '⅓' => (1, 3),
        '⅔' => (2, 3),
        '¼' => (1, 4),
        '¾' => (3, 4),
        '⅕' => (1, 5),
        '⅖' => (2, 5),
        '⅗' => (3, 5),
        '⅘' => (4, 5),
        '⅙' => (1, 6),
        '⅚' => (5, 6),
        '⅐' => (1, 7),
        '⅛' => (1, 8),
        '⅜' => (3, 8),
        '⅝' => (5, 8),
        '⅞' => (7, 8),
        '⅑' => (1, 9),
        '⅒' => (1, 10),
        _ => return None,
    })
}
/// Emit the same ordered paths as `classify` without spelling the fields.
pub fn costs(input: &str, mut emit: impl FnMut(f64)) {
    let (negative, s) = input.strip_prefix('-').map(|v| (true, v)).unwrap_or((false, input));
    let single = |v: &str| {
        let mut chars = v.chars();
        let d = chars.next().and_then(digit);
        if chars.next().is_none() { d } else { None }
    };
    let common_slash = s
        .split_once('/')
        .is_some_and(|(a, b)| matches!((single(a), single(b)), (Some(1), Some(2 | 4)) | (Some(3), Some(4))));
    if common_slash || matches!(s, "½" | "¼" | "¾") {
        emit(if negative { 0.0 } else { -0.001 });
    }
    let head = s
        .strip_suffix(" 1/2")
        .or_else(|| s.strip_suffix(" ௧/௨"))
        .or_else(|| s.strip_suffix('½').map(|v| v.strip_suffix(' ').unwrap_or(v)));
    if let Some(head) = head {
        if head == "10" || matches!(single(head), Some(1..=9)) {
            emit(if negative { 0.0 } else { -0.002 });
        }
    }
    if let Some((a, b)) = s.split_once('/') {
        let (whole, numerator) = a.rsplit_once(' ').map(|(i, n)| (Some(i), n)).unwrap_or((None, a));
        if let (Some(n), Some(d)) = (super::number::recognize(numerator), super::number::recognize(b)) {
            let mut weight = (if n { 0.5 } else { 0.0 }) + (if d { 0.5 } else { 0.0 });
            if let Some(whole) = whole {
                let Some(leading) = super::number::recognize(whole) else { return };
                if leading {
                    weight += 0.5;
                }
            }
            emit(weight);
        }
    }
    if let Some(c) = s.chars().last() {
        if glyph(c).is_some() {
            let head = &s[..s.len() - c.len_utf8()];
            let head = head.strip_suffix(' ').unwrap_or(head);
            if !head.is_empty() || !matches!(c, '½' | '¼' | '¾') {
                let weight = if head.is_empty() {
                    0.0
                } else {
                    let Some(leading) = super::number::recognize(head) else { return };
                    if leading { 0.5 } else { 0.0 }
                };
                emit(weight);
            }
        }
    }
}

pub fn classify(input: &str) -> Vec<(String, f64)> {
    let (neg, s) = input.strip_prefix('-').map(|s| (true, s)).unwrap_or((false, input));
    let sign = if neg { "negative: \"true\" " } else { "" };
    let mut out = Vec::new();
    let special = |v: &str| f("morphosyntactic_features", v);
    let common = match ascii(s).as_deref() {
        Some("1/2") => Some("அரை"),
        Some("1/4") => Some("கால்"),
        Some("3/4") => Some("முக்கால்"),
        _ => match s {
            "½" => Some("அரை"),
            "¼" => Some("கால்"),
            "¾" => Some("முக்கால்"),
            _ => None,
        },
    };
    // The slash itself is not a digit; convert each side independently.
    let common = common.or_else(|| {
        s.split_once('/').and_then(|(a, b)| match (ascii(a)?.as_str(), ascii(b)?.as_str()) {
            ("1", "2") => Some("அரை"),
            ("1", "4") => Some("கால்"),
            ("3", "4") => Some("முக்கால்"),
            _ => None,
        })
    });
    if let Some(w) = common {
        out.push((format!("{sign}{}", special(w)), if neg { 0.0 } else { -0.001 }));
    }
    let halves = ["", "ஒன்றரை", "இரண்டரை", "மூன்றரை", "நான்கரை", "ஐந்தரை", "ஆறரை", "ஏழரை", "எட்டரை", "ஒன்பதரை", "பத்தரை"];
    let half_head = s
        .strip_suffix(" 1/2")
        .or_else(|| s.strip_suffix(" ௧/௨"))
        .or_else(|| s.strip_suffix('½').map(|x| x.strip_suffix(' ').unwrap_or(x)));
    if let Some(head) = half_head {
        if let Some(n) = ascii(head).and_then(|x| x.parse::<usize>().ok()).filter(|&n| n > 0 && n <= 10) {
            if head == "10" || head.chars().count() == 1 {
                out.push((format!("{sign}{}", special(halves[n])), if neg { 0.0 } else { -0.002 }));
            }
        }
    }
    if let Some((a, b)) = s.split_once('/') {
        let (int, numer) = a.rsplit_once(' ').map(|(i, n)| (Some(i), n)).unwrap_or((None, a));
        if let (Some((n, nl)), Some((d, dl))) = (final_graph(numer), final_graph(b)) {
            let mut tag = String::from(sign);
            let mut weight = (if nl { 0.5 } else { 0.0 }) + (if dl { 0.5 } else { 0.0 });
            if let Some(i) = int {
                if let Some((v, leading)) = final_graph(i) {
                    if leading {
                        weight += 0.5
                    }
                    tag.push_str(&f("integer_part", &v));
                    tag.push(' ');
                } else {
                    return out;
                }
            }
            tag.push_str(&f("numerator", &n));
            tag.push(' ');
            tag.push_str(&f("denominator", &d));
            out.push((tag, weight));
        }
    }
    if let Some(c) = s.chars().last() {
        if let Some((n, d)) = glyph(c) {
            let head = &s[..s.len() - c.len_utf8()];
            let head = head.strip_suffix(' ').unwrap_or(head);
            if !head.is_empty() || !matches!(c, '½' | '¼' | '¾') {
                let mut tag = String::from(sign);
                let mut weight = 0.0;
                if !head.is_empty() {
                    if let Some((v, leading)) = final_graph(head) {
                        if leading {
                            weight = 0.5
                        }
                        tag.push_str(&f("integer_part", &v));
                        tag.push(' ');
                    } else {
                        return out;
                    }
                }
                tag.push_str(&f("numerator", &num(&n.to_string()).unwrap()));
                tag.push(' ');
                tag.push_str(&f("denominator", &num(&d.to_string()).unwrap()));
                out.push((tag, weight));
            }
        }
    }
    out
}
