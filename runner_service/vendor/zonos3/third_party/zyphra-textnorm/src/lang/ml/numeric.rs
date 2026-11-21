//! Numeric classifiers: cardinal, decimal, fraction and ordinal.
use super::number;
use crate::text::Cost;

crate::tsv!(QUANTITIES, "ml/numbers/quantities.tsv");
crate::tsv!(FRACTIONS, "ml/fraction/common_fractions.tsv");
crate::tsv!(ORDINALS, "ml/ordinal/ordinals.tsv");
crate::tsv!(ORDINALS_MATHE, "ml/ordinal/ordinals_mathe.tsv");

pub type Reading = (String, Cost, i32);

pub fn sign(s: &str) -> (&str, &'static str) {
    if let Some(s) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) {
        (s, "negative: \"true\" ")
    } else {
        (s, "")
    }
}

pub fn decimal_fields(input: &str) -> Option<String> {
    let (integer, frac) = input.split_once('.')?;
    let integer = number::cardinal(integer)?;
    let frac = number::digitwise(frac)?;
    Some(format!("integer_part: \"{integer}\" fractional_part: \"{frac}\""))
}

pub fn classify(input: &str, out: &mut Vec<Reading>) {
    let (body, neg) = sign(input);
    if let Some((word, weight)) = number::final_cardinal(body) {
        out.push((format!("cardinal {{ {neg}integer: \"{word}\" }}"), 1_100_000 + weight, 0));
    }
    if let Some(fields) = decimal_fields(body) {
        out.push((format!("decimal {{ {neg}{fields} }}"), 1_100_000, 1));
    }
    for row in QUANTITIES.rows() {
        if let Some(prefix) = body.strip_suffix(row.key.as_str()) {
            let prefix = prefix.strip_suffix(' ').unwrap_or(prefix);
            let fields =
                decimal_fields(prefix).or_else(|| number::cardinal(prefix).map(|s| format!("integer_part: \"{s}\"")));
            if let Some(fields) = fields {
                out.push((format!("decimal {{ {neg}{fields} quantity: \"{}\" }}", row.value), 1_100_000 + row.cost, 1));
            }
        }
    }
    if body.contains('/') || body.char_indices().last().is_some_and(|(i, _)| vulgar(&body[i..]).is_some()) {
        for row in FRACTIONS.exact(body) {
            out.push((
                format!("fraction {{ {neg}morphosyntactic_features: \"{}\" }}", row.value),
                750_000 + row.cost,
                2,
            ));
        }
        let mut variants = vec![(None, body)];
        if let Some((a, b)) = body.split_once(' ') {
            if !a.is_empty() && !b.is_empty() {
                variants.push((Some(a), b));
            }
        }
        if let Some((at, _)) = body.char_indices().last() {
            if at > 0 && vulgar(&body[at..]).is_some() && !body[..at].ends_with(' ') {
                variants.push((Some(&body[..at]), &body[at..]));
            }
        }
        for (integer, frac) in variants {
            let mut integer_field = String::new();
            let mut weight = 0;
            if let Some(i) = integer {
                if let Some((w, c)) = number::final_cardinal(i) {
                    integer_field = format!("integer_part: \"{w}\" ");
                    weight += c;
                } else {
                    continue;
                }
            }
            let fraction = if let Some((n, d)) = frac.split_once('/') {
                let spaced = n.ends_with(' ') && d.starts_with(' ');
                if n.ends_with(' ') != d.starts_with(' ') {
                    None
                } else {
                    let n = if spaced { &n[..n.len() - 1] } else { n };
                    let d = if spaced { &d[1..] } else { d };
                    match (number::final_cardinal(n), number::final_cardinal(d)) {
                        (Some((n, nc)), Some((d, dc))) => Some((n, d, nc + dc)),
                        _ => None,
                    }
                }
            } else {
                vulgar(frac).map(|(n, d)| (n.to_owned(), d.to_owned(), 0))
            };
            if let Some((n, d, c)) = fraction {
                out.push((
                    format!("fraction {{ {neg}{integer_field}numerator: \"{n}\" denominator: \"{d}\" }}"),
                    1_050_000 + weight + c,
                    2,
                ));
            }
        }
    }
    for (suffix, table, replacement) in [("ാമത്തെ", &*ORDINALS_MATHE, "ാമത്തെ"), ("ാം", &*ORDINALS, "ാം")]
    {
        if let Some(raw) = input.strip_suffix(suffix) {
            let raw = raw.strip_suffix('-').unwrap_or(raw);
            if let Some(ascii) = number::ascii_digits(raw) {
                for row in table.exact(&ascii) {
                    out.push((format!("ordinal {{ integer: \"{}\" }}", row.value), 1_100_000 + row.cost, 3));
                }
            }
            if let Some((word, cost)) = number::final_cardinal(raw) {
                let word = if word.ends_with('്') || word.ends_with('ം') {
                    format!("{}{replacement}", &word[..word.len() - '്'.len_utf8()])
                } else {
                    word
                };
                out.push((format!("ordinal {{ integer: \"{word}\" }}"), 1_100_000 + cost, 4));
            }
        }
    }
}

fn vulgar(s: &str) -> Option<(&'static str, &'static str)> {
    let pair = match s {
        "½" => (1, 2),
        "¼" => (1, 4),
        "¾" => (3, 4),
        "⅓" => (1, 3),
        "⅔" => (2, 3),
        "⅕" => (1, 5),
        "⅖" => (2, 5),
        "⅗" => (3, 5),
        "⅘" => (4, 5),
        "⅙" => (1, 6),
        "⅚" => (5, 6),
        "⅐" => (1, 7),
        "⅛" => (1, 8),
        "⅜" => (3, 8),
        "⅝" => (5, 8),
        "⅞" => (7, 8),
        "⅑" => (1, 9),
        "⅒" => (1, 10),
        _ => return None,
    };
    const W: [&str; 11] = ["പൂജ്യം", "ഒന്ന്", "രണ്ട്", "മൂന്ന്", "നാല്", "അഞ്ച്", "ആറ്", "ഏഴ്", "എട്ട്", "ഒൻപത്", "പത്ത്"];
    Some((W[pair.0], W[pair.1]))
}
