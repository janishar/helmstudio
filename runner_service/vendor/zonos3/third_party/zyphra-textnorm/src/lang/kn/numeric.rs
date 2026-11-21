use super::graph_utils::{ascii_digits, cardinal, digit, digitwise, final_cardinal, ordinal, table};
use std::sync::LazyLock;

static QUANTITIES: LazyLock<std::collections::HashMap<char, Vec<(&'static str, &'static str)>>> = LazyLock::new(|| {
    let mut index: std::collections::HashMap<char, Vec<(&'static str, &'static str)>> =
        std::collections::HashMap::new();
    for (key, value) in
        include_str!("../../../data/kn/numbers/quantities.tsv").lines().filter_map(|line| line.split_once('\t'))
    {
        if let Some(last) = key.chars().last() {
            index.entry(last).or_default().push((key, value));
        }
    }
    index
});

pub type Analysis = (i64, i32, String);
fn tag(class: &str, body: &str) -> String {
    format!("{class} {{ {body} }}")
}
fn sign(s: &str) -> (&str, &str) {
    if let Some(rest) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) {
        (rest, "negative: \"true\" ")
    } else {
        (s, "")
    }
}
pub fn cardinal_tag(s: &str) -> Option<(String, i64)> {
    let (num, neg) = sign(s);
    let (word, weight) = final_cardinal(num)?;
    Some((format!("{neg}integer: \"{word}\""), weight))
}
pub fn decimal_tag(s: &str) -> Option<String> {
    let (s, neg) = sign(s);
    let rows = s.chars().last().and_then(|c| QUANTITIES.get(&c));
    if !s.contains('.') && rows.is_none() {
        return None;
    }
    let (number, quantity) = rows
        .and_then(|rows| {
            rows.iter()
                .find_map(|&(key, value)| s.strip_suffix(key).map(|n| (n.strip_suffix(' ').unwrap_or(n), Some(value))))
        })
        .unwrap_or((s, None));
    let (integer, fraction) = if let Some((a, b)) = number.split_once('.') {
        (cardinal(a)?, Some(digitwise(b)?))
    } else {
        (cardinal(number)?, None)
    };
    if fraction.is_none() && quantity.is_none() {
        return None;
    }
    let mut result = format!("{neg}integer_part: \"{integer}\"");
    if let Some(f) = fraction {
        result.push_str(&format!(" fractional_part: \"{f}\""));
    }
    if let Some(q) = quantity {
        result.push_str(&format!(" quantity: \"{q}\""));
    }
    Some(result)
}
fn fraction(s: &str) -> Vec<Analysis> {
    let (s, neg) = sign(s);
    let mut result = Vec::new();
    let half = ["ಒಂದೂವರೆ", "ಎರಡುವರೆ", "ಮೂರೂವರೆ", "ನಾಲ್ಕೂವರೆ", "ಐದೂವರೆ", "ಆರೂವರೆ", "ಏಳೂವರೆ", "ಎಂಟೂವರೆ", "ಒಂಬತ್ತೂವರೆ", "ಹತ್ತೂವರೆ"];
    let glyph = [
        ("½", 1, 2),
        ("⅓", 1, 3),
        ("⅔", 2, 3),
        ("¼", 1, 4),
        ("¾", 3, 4),
        ("⅕", 1, 5),
        ("⅖", 2, 5),
        ("⅗", 3, 5),
        ("⅘", 4, 5),
        ("⅙", 1, 6),
        ("⅚", 5, 6),
        ("⅛", 1, 8),
        ("⅜", 3, 8),
        ("⅝", 5, 8),
        ("⅞", 7, 8),
        ("⅒", 1, 10),
    ];
    let mut special = None;
    let mut pair = None;
    let mut integer = None;
    for (g, n, d) in glyph {
        if let Some(prefix) = s.strip_suffix(g) {
            pair = Some((n.to_string(), d.to_string()));
            if !prefix.is_empty() {
                integer = Some(prefix.trim_end_matches(' '));
            } else {
                special = match g {
                    "½" => Some("ಅರ್ಧ".to_owned()),
                    "¼" => Some("ಕಾಲು".to_owned()),
                    "¾" => Some("ಮುಕ್ಕಾಲು".to_owned()),
                    _ => None,
                };
            }
            break;
        }
    }
    if pair.is_none() {
        if let Some((a, b)) = s.split_once('/') {
            let (i, n) = a.rsplit_once(' ').map_or((None, a), |(i, n)| (Some(i), n));
            let na = ascii_digits(n);
            let nb = ascii_digits(b);
            if let (Some(na), Some(nb)) = (na, nb) {
                if i.is_none() {
                    special =
                        table(include_str!("../../../data/kn/fraction/common_fractions.tsv"), &format!("{na}/{nb}"))
                            .map(str::to_owned);
                }
                integer = i;
                pair = Some((na, nb));
            }
        }
    }
    if let (Some(i), Some((n, d))) = (integer, pair.as_ref()) {
        if n == "1" && d == "2" {
            if let Some(v) = ascii_digits(i)
                .and_then(|x| x.parse::<usize>().ok())
                .and_then(|x| x.checked_sub(1))
                .and_then(|x| half.get(x))
            {
                special = Some((*v).to_owned());
            }
        } else if (n == "1" || n == "3") && d == "4" {
            if let Some(v) = final_cardinal(i) {
                special = Some(format!("{} {}", v.0, if n == "1" { "ಕಾಲು" } else { "ಮುಕ್ಕಾಲು" }));
            }
        }
    }
    if let Some(v) = special {
        result.push((
            if v.ends_with("ಕಾಲು") && integer.is_some() { -40_000 } else { -50_000 },
            0,
            tag("fraction", &format!("{neg}morphosyntactic_features: \"{v}\"")),
        ));
    }
    if let Some((n, d)) = pair {
        if let (Some((n, nw)), Some((d, dw))) = (final_cardinal(&n), final_cardinal(&d)) {
            let (prefix, iw) = match integer {
                Some(i) => match final_cardinal(i) {
                    Some((v, w)) => (format!("integer_part: \"{v}\" "), w),
                    None => return result,
                },
                None => (String::new(), 0),
            };
            result.push((
                nw + dw + iw,
                1,
                tag("fraction", &format!("{neg}{prefix}numerator: \"{n}\" denominator: \"{d}\"")),
            ));
        }
    }
    result
}
pub fn analyze(s: &str, out: &mut Vec<Analysis>) {
    if let Some((body, w)) = cardinal_tag(s) {
        out.push((1_100_000 + w, 0, tag("cardinal", &body)));
    }
    if s.contains('.') || s.chars().last().is_some_and(|c| QUANTITIES.contains_key(&c)) {
        if let Some(body) = decimal_tag(s) {
            out.push((1_100_000, 1, tag("decimal", &body)));
        }
    }
    if s.contains('/') || s.chars().last().is_some_and(|c| "½¼¾⅓⅔⅕⅖⅗⅘⅙⅚⅛⅜⅝⅞⅒".contains(c))
    {
        for (w, r, t) in fraction(s) {
            out.push((1_050_000 + w, 2 + r, t));
        }
    }
    for suffix in ["ನೇ", "ನೆ", "st", "nd", "rd", "th"] {
        if let Some(n) = s.strip_suffix(suffix).and_then(ordinal) {
            out.push((1_100_000, 4, tag("ordinal", &format!("integer: \"{n}\""))));
        }
    }
    if let Some((a, b)) = s.split_once('–') {
        let a = a.strip_suffix(' ').unwrap_or(a);
        let b = b.strip_prefix(' ').unwrap_or(b);
        if (1..=6).contains(&a.chars().count())
            && (1..=6).contains(&b.chars().count())
            && a.chars().all(|c| digit(c).is_some())
            && b.chars().all(|c| digit(c).is_some())
        {
            if let (Some((x, wx)), Some((y, wy))) = (final_cardinal(a), final_cardinal(b)) {
                out.push((
                    1_030_000 + wx + wy,
                    0,
                    format!("name: \"{}\"", format!("{x} ರಿಂದ {y}").replace(' ', "\u{a0}")),
                ));
            }
        }
    }
    for (sym, word) in [("©", "ಕಾಪಿರೈಟ್"), ("§", "ವಿಭಾಗ")] {
        if let Some(n) = s.strip_prefix(sym) {
            if (1..=6).contains(&n.chars().count()) && n.chars().all(|c| digit(c).is_some()) {
                if let Some((v, w)) = final_cardinal(n) {
                    out.push((1_030_000 + w, 0, format!("name: \"{}\"", format!("{word} {v}").replace(' ', "\u{a0}"))));
                }
            }
        }
    }
}
