use super::graph_utils::{ascii_digits, cardinal, digitwise, final_cardinal, table};
use super::numeric::{Analysis, cardinal_tag, decimal_tag};
use std::collections::HashMap;
use std::sync::LazyLock;
const UNITS: &str = include_str!("../../../data/kn/measure/unit_other.tsv");
const UNITS_ONE: &str = include_str!("../../../data/kn/measure/unit_one.tsv");
const PREF: &str = include_str!("../../../data/kn/measure/unit_prefix.tsv");
const PREF_OTHER: &str = include_str!("../../../data/kn/measure/unit_prefixed_other.tsv");
const PREF_ONE: &str = include_str!("../../../data/kn/measure/unit_prefixed_one.tsv");
const CURRENCY: &str = include_str!("../../../data/kn/money/currency.tsv");
const SINGULAR: &str = include_str!("../../../data/kn/money/currency_singular.tsv");
const MINORS: &str = include_str!("../../../data/kn/money/major_minor_currencies.tsv");
type UnitRow = (bool, bool, &'static str, &'static str);
static UNIT_ENDS: LazyLock<HashMap<char, Vec<UnitRow>>> = LazyLock::new(|| {
    let mut index: HashMap<char, Vec<UnitRow>> = HashMap::new();
    for (pref, units, one) in
        [(false, UNITS, false), (true, PREF_OTHER, false), (false, UNITS_ONE, true), (true, PREF_ONE, true)]
    {
        for (key, value) in units.lines().filter_map(|line| line.split_once('\t')) {
            if let Some(last) = key.chars().last() {
                index.entry(last).or_default().push((pref, one, key, value));
            }
        }
    }
    index
});
static CURRENCY_EDGES: LazyLock<(Vec<char>, Vec<char>)> = LazyLock::new(|| {
    let mut starts = Vec::new();
    let mut ends = Vec::new();
    for (key, _) in CURRENCY.lines().filter_map(|line| line.split_once('\t')) {
        if let Some(c) = key.chars().next() {
            starts.push(c);
        }
        if let Some(c) = key.chars().last() {
            ends.push(c);
        }
    }
    starts.sort_unstable();
    starts.dedup();
    ends.sort_unstable();
    ends.dedup();
    (starts, ends)
});
fn tag(class: &str, body: &str) -> String {
    format!("{class} {{ {body} }}")
}
fn measure(s: &str, out: &mut Vec<Analysis>) {
    let Some(rows) = s.chars().last().and_then(|c| UNIT_ENDS.get(&c)) else { return };
    for &(pref, one, key, val) in rows {
        let Some(n) = s.strip_suffix(key) else { continue };
        let n = n.strip_suffix(' ').unwrap_or(n);
        if n.is_empty() {
            continue;
        }
        let base = n.trim_start_matches(['-', '−']);
        if one && base != "1" && base != "೧" {
            continue;
        }
        if !one
            && key == "s"
            && n.chars().count() == 4
            && n.chars().next().is_some_and(|c| matches!(c, '1' | '2' | '೧' | '೨'))
            && n.chars().last().is_some_and(|c| matches!(c, '0' | '೦'))
        {
            continue;
        }
        let prefix = if pref {
            match table(PREF, key) {
                Some(v) => format!("morphosyntactic_features: \"{v}\" "),
                None => continue,
            }
        } else {
            String::new()
        };
        let mut values = Vec::new();
        if let Some((v, w)) = cardinal_tag(n) {
            values.push(("cardinal", v, w));
        }
        if !one {
            if let Some(v) = decimal_tag(n) {
                values.push(("decimal", v, 0));
            }
        }
        if !pref && !one {
            let h = match n {
                "½" | "1/2" => Some("ಅರ್ಧ"),
                "2½" | "2 1/2" => Some("ಎರಡುವರೆ"),
                "1½" | "1 1/2" => Some("ಒಂದೂವರೆ"),
                _ => None,
            };
            if let Some(v) = h {
                values.push(("cardinal", format!("integer: \"{v}\""), 0));
            }
            for sep in ['-', '–', '—'] {
                if let Some((a, b)) = n.split_once(sep) {
                    let a = a.strip_suffix(' ').unwrap_or(a);
                    let b = b.strip_prefix(' ').unwrap_or(b);
                    if (1..=6).contains(&a.chars().count())
                        && (1..=6).contains(&b.chars().count())
                        && a.chars().all(|c| super::graph_utils::digit(c).is_some())
                        && b.chars().all(|c| super::graph_utils::digit(c).is_some())
                    {
                        if let (Some((a, wa)), Some((b, wb))) = (final_cardinal(a), final_cardinal(b)) {
                            values.push(("cardinal", format!("integer: \"{a} ರಿಂದ {b}\""), wa + wb));
                        }
                    }
                }
            }
        }
        for (class, v, w) in values {
            out.push((
                1_080_000 + w,
                if pref { -1 } else { 0 },
                tag("measure", &format!("{prefix}{class} {{ {v} }} units: \"{val}\" preserve_order: true")),
            ));
        }
    }
}
fn per_unit(s: &str) -> (&str, Option<&'static str>) {
    let Some((amount, unit)) = s.rsplit_once('/') else { return (s, None) };
    let unit = unit.strip_prefix(' ').unwrap_or(unit);
    let value = match unit {
        "h" | "hr" => "ಗಂಟೆಗೆ",
        "s" | "sec" => "ಸೆಕೆಂಡಿಗೆ",
        "min" => "ನಿಮಿಷಕ್ಕೆ",
        "d" | "day" => "ದಿನಕ್ಕೆ",
        "m" | "mo" | "month" => "ತಿಂಗಳಿಗೆ",
        "y" | "yr" | "year" => "ವರ್ಷಕ್ಕೆ",
        "kg" => "ಕಿಲೋಗ್ರಾಂಗೆ",
        "g" => "ಗ್ರಾಂಗೆ",
        "l" => "ಲೀಟರ್‌ಗೆ",
        "km" => "ಕಿಲೋಮೀಟರ್‌ಗೆ",
        _ => return (s, None),
    };
    (amount.strip_suffix(' ').unwrap_or(amount), Some(value))
}
fn range_amount(s: &str) -> Option<String> {
    for (suf, word) in [
        ("k", "ಸಾವಿರ"),
        ("K", "ಸಾವಿರ"),
        ("m", "ಮಿಲಿಯನ್"),
        ("M", "ಮಿಲಿಯನ್"),
        ("b", "ಬಿಲಿಯನ್"),
        ("B", "ಬಿಲಿಯನ್"),
        ("t", "ಟ್ರಿಲಿಯನ್"),
        ("T", "ಟ್ರಿಲಿಯನ್"),
    ] {
        if let Some(n) = s.strip_suffix(suf) {
            let v = if let Some((a, b)) = n.split_once('.') {
                format!("{} ದಶಮಾಂಶ {}", cardinal(a)?, digitwise(b)?)
            } else {
                cardinal(n)?
            };
            return Some(format!("{v} {word}"));
        }
    }
    if let Some((a, b)) = s.split_once('.') {
        if b.chars().count() > 2 {
            return None;
        }
        return Some(format!("{} ದಶಮಾಂಶ {}", final_cardinal(a)?.0, digitwise(b)?));
    }
    Some(final_cardinal(s)?.0)
}
fn money_ranges(s: &str, out: &mut Vec<Analysis>, negative: &str, per: &str) {
    for sep in ['-', '–'] {
        let Some((left, right)) = s.split_once(sep) else { continue };
        let left = left.trim_end_matches(' ');
        let right = right.trim_start_matches(' ');
        for row in CURRENCY.lines() {
            let Some((sym, major)) = row.split_once('\t') else { continue };
            if let Some(a) = left.strip_prefix(sym) {
                let a = a.strip_prefix(' ').unwrap_or(a);
                let mut b = right;
                for other in CURRENCY.lines() {
                    let Some((key, val)) = other.split_once('\t') else { continue };
                    if val == major {
                        if let Some(r) = b.strip_prefix(key) {
                            b = r.strip_prefix(' ').unwrap_or(r);
                            break;
                        }
                    }
                }
                if let (Some(x), Some(y)) = (range_amount(a), range_amount(b)) {
                    out.push((
                        1_078_000,
                        0,
                        tag(
                            "money",
                            &format!("{negative}currency_maj: \"{major}\" integer_part: \"{x} ರಿಂದ {y}\"{per}"),
                        ),
                    ));
                }
            }
            if let Some(b) = right.strip_suffix(sym) {
                let b = b.strip_suffix(' ').unwrap_or(b);
                if let (Some(x), Some(y)) = (range_amount(left), range_amount(b)) {
                    out.push((
                        1_078_000,
                        0,
                        tag(
                            "money",
                            &format!("{negative}integer_part: \"{x} ರಿಂದ {y}\" currency_maj: \"{major}\"{per}"),
                        ),
                    ));
                }
            }
        }
    }
}
fn money(s: &str, out: &mut Vec<Analysis>) {
    let (s, negative) = if let Some(v) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) {
        (v, "negative: \"true\" ")
    } else {
        (s, "")
    };
    let (s, per) = per_unit(s);
    let per = per.map(|p| format!(" morphosyntactic_features: \"{p}\"")).unwrap_or_default();
    money_ranges(s, out, negative, &per);
    for suffix in ["¢", "c"] {
        if let Some(n) = s.strip_suffix(suffix) {
            let n = n.strip_suffix(' ').unwrap_or(n);
            if n.chars().count() <= 2 {
                if let Some(v) = cardinal(n) {
                    out.push((1_080_000,0,tag("money",&format!("{negative}integer_part: \"ಸೊನ್ನೆ\" fractional_part: \"{v}\" currency_maj: \"ಡಾಲರ್\" currency_min: \"ಸೆಂಟ್\""))));
                }
            }
        }
    }
    for line in CURRENCY.lines() {
        let Some((symbol, plural)) = line.split_once('\t') else { continue };
        for before in [true, false] {
            let amount = if before {
                s.strip_prefix(symbol).map(|v| v.strip_prefix(' ').unwrap_or(v))
            } else {
                s.strip_suffix(symbol).map(|v| v.strip_suffix(' ').unwrap_or(v))
            };
            let Some(amount) = amount else { continue };
            let singular = table(SINGULAR, symbol).unwrap_or(plural);
            let major = if before && matches!(symbol, "USD" | "US$") {
                "ಯುಎಸ್ ಡಾಲರ್"
            } else if amount == "1" || amount == "೧" {
                singular
            } else {
                plural
            };
            let mut cases: Vec<(String, i64)> = Vec::new();
            let cleaned = amount
                .split_once('.')
                .and_then(|(a, b)| {
                    (!b.is_empty() && b.chars().all(|c| super::graph_utils::digit(c) == Some(0))).then_some(a)
                })
                .unwrap_or(amount);
            if let Some((v, w)) = final_cardinal(cleaned) {
                cases.push((format!("integer_part: \"{v}\""), w));
            }
            if let Some((a, b)) = amount.split_once('.') {
                if let (Some((int, _)), Some(frac)) = (final_cardinal(a), ascii_digits(b)) {
                    let sig = frac.trim_end_matches('0');
                    if !sig.is_empty() && sig.len() <= 2 {
                        let canonical = if sig.len() == 1 {
                            format!("{sig}0")
                        } else if sig.starts_with('0') {
                            sig[1..].to_owned()
                        } else {
                            sig.to_owned()
                        };
                        if let (Some(f), Some(min)) = (cardinal(&canonical), table(MINORS, major)) {
                            cases.push((
                                format!("integer_part: \"{int}\" fractional_part: \"{f}\" currency_min: \"{min}\""),
                                -1_000,
                            ));
                        }
                    }
                    if before && sig.len() >= 3 {
                        if let Some(f) = digitwise(b) {
                            cases.push((format!("integer_part: \"{int}\" fractional_part: \"{f}\""), 1_000));
                        }
                    }
                }
            }
            if before {
                for (suf, word) in [
                    ("k", "ಸಾವಿರ"),
                    ("K", "ಸಾವಿರ"),
                    ("m", "ಮಿಲಿಯನ್"),
                    ("M", "ಮಿಲಿಯನ್"),
                    ("b", "ಬಿಲಿಯನ್"),
                    ("B", "ಬಿಲಿಯನ್"),
                    ("t", "ಟ್ರಿಲಿಯನ್"),
                    ("T", "ಟ್ರಿಲಿಯನ್"),
                ] {
                    if let Some(n) = amount.strip_suffix(suf) {
                        let v = if let Some((a, b)) = n.split_once('.') {
                            cardinal(a).zip(digitwise(b)).map(|(a, b)| format!("{a} ದಶಮಾಂಶ {b}"))
                        } else {
                            cardinal(n)
                        };
                        if let Some(v) = v {
                            cases.push((format!("integer_part: \"{v} {word}\""), 0));
                        }
                    }
                }
            }
            for (body, w) in cases {
                let (int, rest) = body.split_once(" fractional_part:").unwrap_or((&body, ""));
                let fields = if rest.is_empty() {
                    if before {
                        format!("currency_maj: \"{major}\" {body}")
                    } else {
                        format!("{body} currency_maj: \"{major}\"")
                    }
                } else if before {
                    format!("currency_maj: \"{major}\" {body}")
                } else if let Some((f, min)) = rest.split_once(" currency_min:") {
                    format!("{int} fractional_part:{f} currency_maj: \"{major}\" currency_min:{min}")
                } else {
                    format!("{body} currency_maj: \"{major}\"")
                };
                out.push((1_080_000 + w, 0, tag("money", &format!("{negative}{fields}{per}"))));
            }
        }
    }
}
pub fn analyze(s: &str, out: &mut Vec<Analysis>) {
    measure(s, out);
    let unsigned = s.strip_prefix('-').or_else(|| s.strip_prefix('−')).unwrap_or(s);
    let (starts, ends) = &*CURRENCY_EDGES;
    if unsigned.chars().next().is_some_and(|c| starts.binary_search(&c).is_ok()) {
        money(s, out);
        return;
    }
    let is_currency_end = |c| c == 'c' || ends.binary_search(&c).is_ok();
    if unsigned.chars().last().is_some_and(is_currency_end) {
        money(s, out);
    } else if unsigned.contains('/') {
        let (amount, _) = per_unit(unsigned);
        if amount.chars().last().is_some_and(is_currency_end) {
            money(s, out);
        }
    }
}
