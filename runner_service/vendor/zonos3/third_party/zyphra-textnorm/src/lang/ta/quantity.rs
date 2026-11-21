//! Unit and currency classifiers.
use super::number::{attributive, digitwise, final_graph};
use std::collections::{HashMap, HashSet};
use std::sync::LazyLock;

type Unit = (&'static str, &'static str, bool, Option<&'static str>);
static UNITS_BY_LAST: LazyLock<HashMap<char, Vec<Unit>>> = LazyLock::new(|| {
    let mut index: HashMap<char, Vec<Unit>> = HashMap::new();
    for (table, one, prefix) in [
        (include_str!("../../../data/ta/measure/unit_one.tsv"), true, false),
        (include_str!("../../../data/ta/measure/unit_other.tsv"), false, false),
        (include_str!("../../../data/ta/measure/unit_prefixed_one.tsv"), true, true),
        (include_str!("../../../data/ta/measure/unit_prefixed_other.tsv"), false, true),
    ] {
        for (unit, spoken) in rows(table) {
            let feature = if prefix {
                rows(include_str!("../../../data/ta/measure/unit_prefix.tsv"))
                    .find_map(|(symbol, word)| (unit == symbol).then_some(word))
            } else {
                None
            };
            if prefix && feature.is_none() {
                continue;
            }
            if let Some(last) = unit.chars().last() {
                index.entry(last).or_default().push((unit, spoken, one, feature));
            }
        }
    }
    index
});
type Currency = (&'static str, &'static str, bool);
static CURRENCIES: LazyLock<Vec<Currency>> = LazyLock::new(|| {
    let mut entries = Vec::new();
    for (table, singular) in [
        (include_str!("../../../data/ta/money/currency.tsv"), false),
        (include_str!("../../../data/ta/money/currency_singular.tsv"), true),
    ] {
        entries.extend(rows(table).map(|(symbol, major)| (symbol, major, singular)));
    }
    entries
});
struct CurrencyIndex {
    first: HashMap<char, Vec<usize>>,
    last: HashMap<char, Vec<usize>>,
}
static CURRENCY_INDEX: LazyLock<CurrencyIndex> = LazyLock::new(|| {
    let mut index = CurrencyIndex { first: HashMap::new(), last: HashMap::new() };
    for (i, &(symbol, _, _)) in CURRENCIES.iter().enumerate() {
        if let Some(c) = symbol.chars().next() {
            index.first.entry(c).or_default().push(i);
        }
        if let Some(c) = symbol.chars().last() {
            index.last.entry(c).or_default().push(i);
        }
    }
    index
});
static MINOR_MAJORS: LazyLock<HashSet<&'static str>> = LazyLock::new(|| {
    rows(include_str!("../../../data/ta/money/major_minor_currencies.tsv")).map(|(major, _)| major).collect()
});
fn f(k: &str, v: &str) -> String {
    format!("{k}: \"{v}\"")
}
fn rows(table: &str) -> impl Iterator<Item = (&str, &str)> {
    table.lines().filter_map(|l| l.split_once('\t'))
}
fn num(s: &str) -> Option<String> {
    final_graph(s).map(|x| x.0)
}
fn neg(s: &str) -> (bool, &str) {
    s.strip_prefix('-').map(|x| (true, x)).unwrap_or((false, s))
}
fn numeric(s: &str, attr: bool) -> Option<String> {
    let (negative, s) = neg(s);
    let sign = if negative { "negative: \"true\" " } else { "" };
    if let Some((a, b)) = s.split_once('.') {
        let a = num(a)?;
        let b = digitwise(b)?;
        if b.is_empty() {
            return None;
        }
        Some(format!("decimal {{ {sign}{} {} }}", f("integer_part", &a), f("fractional_part", &b)))
    } else {
        let n = num(s)?;
        let n = if attr { attributive(&n) } else { n };
        Some(format!("cardinal {{ {sign}{} }}", f("integer", &n)))
    }
}
fn range_part(s: &str, attr: bool) -> Option<String> {
    if let Some((a, b)) = s.split_once('.') {
        if a.is_empty()
            || a.chars().count() > 5
            || !a.chars().all(|c| super::number::digit(c).is_some())
            || b.chars().count() > 3
            || b.is_empty()
        {
            return None;
        }
        Some(format!("{} புள்ளி {}", num(a)?, digitwise(b)?))
    } else {
        if s.is_empty() || s.chars().count() > 5 || !s.chars().all(|c| super::number::digit(c).is_some()) {
            return None;
        }
        let n = num(s)?;
        Some(if attr { attributive(&n) } else { n })
    }
}
fn range(s: &str) -> Option<String> {
    for sep in [" - ", " – ", "-", "–"] {
        if let Some((a, b)) = s.split_once(sep) {
            return Some(format!(
                "cardinal {{ {} }}",
                f("integer", &format!("{} முதல் {}", range_part(a, false)?, range_part(b, true)?))
            ));
        }
    }
    None
}
fn vulgar(s: &str) -> Option<(String, bool)> {
    let value = match s {
        "½" => Some("அரை"),
        "¼" => Some("கால்"),
        "¾" => Some("முக்கால்"),
        _ => None,
    };
    if let Some(v) = value {
        return Some((format!("cardinal {{ {} }}", f("integer", v)), true));
    }
    let head = s.strip_suffix("1/2").or_else(|| s.strip_suffix("௧/௨")).or_else(|| s.strip_suffix('½'))?;
    let head = head.strip_suffix(' ').unwrap_or(head);
    let heads = ["", "ஒன்றரை", "இரண்டரை", "மூன்றரை", "நான்கரை", "ஐந்தரை", "ஆறரை", "ஏழரை", "எட்டரை", "ஒன்பதரை", "பத்தரை"];
    let digits: String =
        head.chars().map(|c| super::number::digit(c).map(|d| (b'0' + d) as char)).collect::<Option<_>>()?;
    if head != "10" && head.chars().count() != 1 {
        return None;
    }
    let n: usize = digits.parse().ok()?;
    if !(1..=10).contains(&n) {
        return None;
    }
    Some((format!("cardinal {{ {} }}", f("integer", heads[n])), false))
}
fn numeric_weight(s: &str) -> f64 {
    let s = s.strip_prefix('-').unwrap_or(s);
    for sep in [" - ", " – ", "-", "–"] {
        if let Some((a, b)) = s.split_once(sep) {
            return numeric_weight(a) + numeric_weight(b);
        }
    }
    let integer = s.split_once('.').map(|(a, _)| a).unwrap_or(s);
    if super::number::recognize(integer).is_some_and(|leading| leading) { 0.5 } else { 0.0 }
}
fn is_vulgar(s: &str) -> Option<bool> {
    if matches!(s, "½" | "¼" | "¾") {
        return Some(true);
    }
    let head = s.strip_suffix("1/2").or_else(|| s.strip_suffix("௧/௨")).or_else(|| s.strip_suffix('½'))?;
    let head = head.strip_suffix(' ').unwrap_or(head);
    if head == "10" {
        return Some(false);
    }
    let mut chars = head.chars();
    let first = chars.next()?;
    if chars.next().is_some() {
        return None;
    }
    matches!(super::number::digit(first), Some(1..=9)).then_some(false)
}

fn range_part_valid(s: &str) -> bool {
    let (integer, fractional) = s.split_once('.').map(|(a, b)| (a, Some(b))).unwrap_or((s, None));
    if integer.is_empty()
        || integer.chars().count() > 5
        || !integer.chars().all(|c| super::number::digit(c).is_some())
        || super::number::recognize(integer).is_none()
    {
        return false;
    }
    fractional
        .is_none_or(|f| !f.is_empty() && f.chars().count() <= 3 && f.chars().all(|c| super::number::digit(c).is_some()))
}

fn measure_number_valid(s: &str) -> bool {
    let (_, unsigned) = neg(s);
    let numeric = if let Some((integer, fractional)) = unsigned.split_once('.') {
        super::number::recognize(integer).is_some()
            && !fractional.is_empty()
            && fractional.chars().all(|c| super::number::digit(c).is_some())
    } else {
        super::number::recognize(unsigned).is_some()
    };
    if numeric {
        return true;
    }
    for sep in [" - ", " – ", "-", "–"] {
        if let Some((a, b)) = s.split_once(sep) {
            return range_part_valid(a) && range_part_valid(b);
        }
    }
    is_vulgar(s).is_some()
}

/// Recognize unit paths and their exact costs without spelling their numbers.
pub fn measure_costs(s: &str, mut emit: impl FnMut(f64)) {
    let Some(entries) = s.chars().last().and_then(|last| UNITS_BY_LAST.get(&last)) else { return };
    for &(unit, _, one, feature) in entries {
        let Some(head) = s.strip_suffix(unit) else { continue };
        let number = head.strip_suffix(' ').unwrap_or(head);
        if number.is_empty() {
            continue;
        }
        let special = is_vulgar(number);
        let isone = matches!(number, "1" | "௧" | "-1" | "-௧") || special == Some(true);
        if one != isone {
            continue;
        }
        if unit == "s"
            && number.chars().count() == 4
            && matches!(number.chars().last(), Some('0' | '௦'))
            && matches!(number.chars().next(), Some('1' | '2' | '௧' | '௨'))
        {
            continue;
        }
        if measure_number_valid(number) {
            emit(numeric_weight(number) + if feature.is_some() { -0.01 } else { 0.0 });
        }
    }
}
pub fn measure(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    let Some(entries) = s.chars().last().and_then(|last| UNITS_BY_LAST.get(&last)) else { return out };
    for &(unit, spoken, one, feature) in entries {
        let Some(head) = s.strip_suffix(unit) else { continue };
        let head = head.strip_suffix(' ').unwrap_or(head);
        let number = head;
        if number.is_empty() {
            continue;
        }
        let special = vulgar(number);
        let isone = matches!(number, "1" | "௧" | "-1" | "-௧") || special.as_ref().is_some_and(|x| x.1);
        if one != isone {
            continue;
        }
        if unit == "s"
            && head.chars().count() == 4
            && matches!(head.chars().last(), Some('0' | '௦'))
            && matches!(head.chars().next(), Some('1' | '2' | '௧' | '௨'))
        {
            continue;
        }
        let value = numeric(number, true).or_else(|| range(number)).or_else(|| special.map(|x| x.0));
        if let Some(n) = value {
            let p = feature.map(|v| format!("{} ", f("morphosyntactic_features", v))).unwrap_or_default();
            out.push((
                format!("{p}{n} {} preserve_order: true", f("units", spoken)),
                numeric_weight(number) + if feature.is_some() { -0.01 } else { 0.0 },
            ));
        }
    }
    out
}
fn shorthand(s: &str) -> Option<String> {
    let scale = s.chars().last()?;
    let input = &s[..s.len() - scale.len_utf8()];
    if matches!(scale, 'k' | 'K') {
        if input.is_empty() || input.chars().count() > 3 {
            return None;
        }
        return num(&format!("{input}000")).map(|s| attributive(&s));
    }
    let label = match scale {
        'm' | 'M' => "மில்லியன்",
        'b' | 'B' => "பில்லியன்",
        't' | 'T' => "ட்ரில்லியன்",
        _ => return None,
    };
    let (i, fr) = input.split_once('.').map(|(a, b)| (a, Some(b))).unwrap_or((input, None));
    if i.is_empty() || i.chars().count() > 3 {
        return None;
    }
    let mut v = attributive(&num(i)?);
    if let Some(fr) = fr {
        if fr.is_empty() || fr.chars().count() > 3 {
            return None;
        }
        v.push_str(" புள்ளி ");
        v.push_str(&digitwise(fr)?);
    }
    v.push(' ');
    v.push_str(label);
    Some(v)
}
fn money_range_part(s: &str) -> Option<String> {
    if let Some(v) = shorthand(s) {
        return Some(v);
    }
    if let Some((a, b)) = s.split_once('.') {
        if b.is_empty() || b.chars().count() > 2 {
            return None;
        }
        Some(format!("{} புள்ளி {}", num(a)?, digitwise(b)?))
    } else {
        num(s)
    }
}
fn strip_rate(mut source: &str) -> (&str, Option<&'static str>) {
    for (suffix, word) in [
        ("/hour", "மணிக்கு"),
        ("/hr", "மணிக்கு"),
        ("/h", "மணிக்கு"),
        ("/kg", "ஒரு கிலோவிற்கு"),
        ("/day", "ஒரு நாளைக்கு"),
        ("/month", "ஒரு மாதத்திற்கு"),
        ("/year", "ஒரு வருடத்திற்கு"),
        ("/yr", "ஒரு ஆண்டிற்கு"),
        ("/km", "ஒரு கிலோமீட்டருக்கு"),
        ("/m", "ஒரு மீட்டருக்கு"),
    ] {
        if let Some(value) = source.strip_suffix(suffix) {
            source = value.strip_suffix(' ').unwrap_or(value);
            return (source, Some(word));
        }
    }
    (source, None)
}

fn currency_indices(source: &str) -> impl Iterator<Item = usize> {
    let first = source.chars().next().and_then(|c| CURRENCY_INDEX.first.get(&c)).map(Vec::as_slice).unwrap_or(&[]);
    let last = source.chars().last().and_then(|c| CURRENCY_INDEX.last.get(&c)).map(Vec::as_slice).unwrap_or(&[]);
    let (mut i, mut j) = (0, 0);
    std::iter::from_fn(move || match (first.get(i).copied(), last.get(j).copied()) {
        (None, None) => None,
        (Some(x), None) => {
            i += 1;
            Some(x)
        }
        (None, Some(y)) => {
            j += 1;
            Some(y)
        }
        (Some(x), Some(y)) if x < y => {
            i += 1;
            Some(x)
        }
        (Some(x), Some(y)) if y < x => {
            j += 1;
            Some(y)
        }
        (Some(x), Some(_)) => {
            i += 1;
            j += 1;
            Some(x)
        }
    })
}

fn shorthand_valid(s: &str) -> bool {
    let Some(scale) = s.chars().last() else { return false };
    let input = &s[..s.len() - scale.len_utf8()];
    if matches!(scale, 'k' | 'K') {
        return !input.is_empty()
            && input.chars().count() <= 3
            && input.chars().all(|c| super::number::digit(c).is_some());
    }
    if !matches!(scale, 'm' | 'M' | 'b' | 'B' | 't' | 'T') {
        return false;
    }
    let (integer, fraction) = input.split_once('.').map(|(a, b)| (a, Some(b))).unwrap_or((input, None));
    !integer.is_empty()
        && integer.chars().count() <= 3
        && super::number::recognize(integer).is_some()
        && fraction.is_none_or(|f| {
            !f.is_empty() && f.chars().count() <= 3 && f.chars().all(|c| super::number::digit(c).is_some())
        })
}

fn money_range_valid(s: &str) -> bool {
    if shorthand_valid(s) {
        return true;
    }
    if let Some((integer, fraction)) = s.split_once('.') {
        !fraction.is_empty()
            && fraction.chars().count() <= 2
            && fraction.chars().all(|c| super::number::digit(c).is_some())
            && super::number::recognize(integer).is_some()
    } else {
        super::number::recognize(s).is_some()
    }
}

/// Emit currency path costs without allocating spoken values or tagged fields.
pub fn money_costs(s: &str, mut emit: impl FnMut(f64)) {
    let (negative, s) = neg(s);
    let (source, _) = strip_rate(s);
    for index in currency_indices(source) {
        let (symbol, major, singular) = CURRENCIES[index];
        let (value, prefix) = if let Some(v) = source.strip_prefix(symbol) {
            (v.strip_prefix(' ').unwrap_or(v), true)
        } else if let Some(v) = source.strip_suffix(symbol) {
            (v.strip_suffix(' ').unwrap_or(v), false)
        } else {
            continue;
        };
        if !singular && shorthand_valid(value) {
            emit(-0.002);
        }
        if !singular && !negative {
            for separator in [" - ", " – ", "-", "–"] {
                if let Some((a, b)) = value.split_once(separator) {
                    let b = if prefix {
                        rows(include_str!("../../../data/ta/money/currency.tsv"))
                            .find_map(|(sym, name)| {
                                if name == major {
                                    b.strip_prefix(sym).map(|v| v.strip_prefix(' ').unwrap_or(v))
                                } else {
                                    None
                                }
                            })
                            .unwrap_or(b)
                    } else {
                        b
                    };
                    if money_range_valid(a) && money_range_valid(b) {
                        emit(-0.002);
                    }
                    break;
                }
            }
        }
        let (integer, fraction) = value.split_once('.').map(|(a, b)| (a, Some(b))).unwrap_or((value, None));
        let Some(leading) = super::number::recognize(integer) else { continue };
        if singular && !matches!(integer, "1" | "௧") {
            continue;
        }
        let weight = if leading { 0.5 } else { 0.0 };
        if fraction.is_none() || matches!(fraction, Some("00" | "௦௦")) {
            emit(weight + if singular { -0.002 } else { 0.0 });
        }
        if let Some(f) = fraction {
            if !f.is_empty() && f.chars().all(|c| super::number::digit(c).is_some()) {
                emit(weight + 0.001);
            }
            if f.chars().count() == 2
                && f.chars().all(|c| super::number::digit(c).is_some())
                && f.chars().next().and_then(super::number::digit) != Some(0)
                && MINOR_MAJORS.contains(major)
            {
                emit(weight + if singular { -0.002 } else { -0.001 });
            }
        }
    }
}

pub fn money(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    let (negative, s) = neg(s);
    let sign = if negative { "negative: \"true\" " } else { "" };
    let (source, per) = strip_rate(s);
    for index in currency_indices(source) {
        let (symbol, major, singular) = CURRENCIES[index];
        let (value, prefix) = if let Some(v) = source.strip_prefix(symbol) {
            (v.strip_prefix(' ').unwrap_or(v), true)
        } else if let Some(v) = source.strip_suffix(symbol) {
            (v.strip_suffix(' ').unwrap_or(v), false)
        } else {
            continue;
        };
        if !singular {
            if let Some(v) = shorthand(value) {
                let tail = per.map(|p| format!(" {}", f("morphosyntactic_features", p))).unwrap_or_default();
                let tag = if prefix {
                    format!("{sign}{} {}{tail}", f("currency_maj", major), f("integer_part", &v))
                } else {
                    format!("{sign}{} {}{tail}", f("integer_part", &v), f("currency_maj", major))
                };
                out.push((tag, -0.002));
            }
        }
        if !singular && !negative {
            for separator in [" - ", " – ", "-", "–"] {
                if let Some((a, b)) = value.split_once(separator) {
                    let b = if prefix {
                        rows(include_str!("../../../data/ta/money/currency.tsv"))
                            .find_map(|(sym, m)| {
                                if m == major {
                                    b.strip_prefix(sym).map(|x| x.strip_prefix(' ').unwrap_or(x))
                                } else {
                                    None
                                }
                            })
                            .unwrap_or(b)
                    } else {
                        b
                    };
                    if let (Some(a), Some(b)) = (money_range_part(a), money_range_part(b)) {
                        let amount = format!("{a} முதல் {b}");
                        let tag = if prefix {
                            format!("{} {}", f("currency_maj", major), f("integer_part", &amount))
                        } else {
                            format!("{} {}", f("integer_part", &amount), f("currency_maj", major))
                        };
                        let suffix = per.map(|p| format!(" {}", f("morphosyntactic_features", p))).unwrap_or_default();
                        out.push((format!("{tag}{suffix}"), -0.002));
                    }
                    break;
                }
            }
        }
        let (integer, frac) = value.split_once('.').map(|(a, b)| (a, Some(b))).unwrap_or((value, None));
        let Some((raw, leading)) = final_graph(integer) else { continue };
        let weight = if leading { 0.5 } else { 0.0 };
        if singular && !matches!(integer, "1" | "௧") {
            continue;
        }
        let int = attributive(&raw);
        let suffix = per.map(|p| format!(" {}", f("morphosyntactic_features", p))).unwrap_or_default();
        let make = |extra: &str| {
            if prefix {
                format!("{sign}{} {}{extra}{suffix}", f("currency_maj", major), f("integer_part", &int))
            } else {
                format!("{sign}{}{extra} {}{suffix}", f("integer_part", &int), f("currency_maj", major))
            }
        };
        if frac.is_none() || frac == Some("00") || frac == Some("௦௦") {
            out.push((make(""), weight + if singular { -0.002 } else { 0.0 }));
        }
        if let Some(frac) = frac {
            if let Some(words) = digitwise(frac).filter(|v| !v.is_empty()) {
                let extra = format!(" {}", f("fractional_part", &words));
                out.push((make(&extra), weight + 0.001));
            }
            if frac.chars().count() == 2 {
                let ds: Option<String> =
                    frac.chars().map(|c| super::number::digit(c).map(|d| (b'0' + d) as char)).collect();
                if let Some(ds) = ds {
                    if let Some(spoken) = num(&ds).filter(|_| !ds.starts_with('0')) {
                        if let Some((_, minor)) =
                            rows(include_str!("../../../data/ta/money/major_minor_currencies.tsv"))
                                .find(|(k, _)| *k == major)
                        {
                            let extra = format!(" {}", f("fractional_part", &spoken));
                            let mut t = make(&extra);
                            t.push(' ');
                            t.push_str(&f("currency_min", minor));
                            out.push((t, weight + if singular { -0.002 } else { -0.001 }));
                        }
                    }
                }
            }
        }
    }
    out
}
