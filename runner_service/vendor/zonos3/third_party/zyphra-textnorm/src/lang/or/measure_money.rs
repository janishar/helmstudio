use super::graph_utils::{CARDINALS, digit};
use super::numbers::{core, decimal, fraction_forms};
use std::collections::HashMap;
use std::sync::LazyLock;

type Unit = (&'static str, &'static str, &'static str);
static UNITS: LazyLock<HashMap<char, Vec<Unit>>> = LazyLock::new(|| {
    let mut units: Vec<_> = rows(include_str!("../../../data/or/units_list.tsv")).collect();
    units.sort_by_key(|(key, _, _)| std::cmp::Reverse(key.chars().count()));
    let mut by_last: HashMap<char, Vec<Unit>> = HashMap::new();
    for unit in units {
        if let Some(last) = unit.0.chars().last() {
            by_last.entry(last).or_default().push(unit);
        }
    }
    by_last
});

fn rows(s: &'static str) -> impl Iterator<Item = (&'static str, &'static str, &'static str)> {
    s.lines().filter(|l| !l.starts_with('#')).filter_map(|l| {
        let (key, rest) = l.split_once('\t')?;
        let (value, extra) = rest.split_once('\t').unwrap_or((rest, ""));
        Some((key, value, extra))
    })
}
fn number(s: &str) -> Vec<String> {
    fn base(s: &str) -> Vec<String> {
        let mut v = Vec::new();
        if let Some(w) = core(s) {
            v.push(w);
        }
        if let Some(w) = decimal(s) {
            if !v.contains(&w) {
                v.push(w);
            }
        }
        for w in fraction_forms(s) {
            if !v.contains(&w) {
                v.push(w);
            }
        }
        v
    }
    let mut v = base(s);
    if let Some(r) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) {
        for w in base(r) {
            let w = format!("ଋଣ {w}");
            if !v.contains(&w) {
                v.push(w);
            }
        }
    }
    v
}
fn ranges(s: &str) -> Vec<(&str, &str)> {
    let mut r = Vec::new();
    for (i, c) in s.char_indices() {
        if matches!(c, '-' | '–' | '—') && i > 0 {
            let a = &s[..i];
            let b = &s[i + c.len_utf8()..];
            r.push((a.strip_suffix(' ').unwrap_or(a), b.strip_prefix(' ').unwrap_or(b)));
        }
    }
    if let Some((a, b)) = s.split_once(" to ") {
        r.push((a.strip_suffix(' ').unwrap_or(a), b.strip_prefix(' ').unwrap_or(b)));
    }
    r
}
fn single_measure(s: &str) -> Vec<String> {
    let mut out = Vec::new();
    let Some(units) = s.chars().last().and_then(|c| UNITS.get(&c)) else { return out };
    for &(key, val, _) in units {
        if let Some(a) = s.strip_suffix(key) {
            let a = a.strip_suffix(' ').unwrap_or(a);
            for w in number(a) {
                out.push(format!("{w} {val}"));
            }
        }
    }
    out
}

pub fn may_measure(s: &str) -> bool {
    s.chars().last().is_some_and(|c| UNITS.contains_key(&c)) || s.contains(['x', 'X', '×'])
}
pub fn measure(s: &str) -> Vec<String> {
    let mut out = Vec::new();
    for (prefix, spoken) in [("~", "ପ୍ରାୟ "), ("±", "ପ୍ଲସ୍ ମାଇନସ୍ ")] {
        if let Some(a) = s.strip_prefix(prefix) {
            for w in single_measure(a) {
                out.push(format!("{spoken}{w}"));
            }
        }
    }
    // Prefer an explicit measure modifier over absorbing it into the number.
    out.extend(single_measure(s));
    for (a, b) in ranges(s) {
        let left = single_measure(a);
        let right = single_measure(b);
        for x in &left {
            for y in &right {
                out.push(format!("{x} ରୁ {y}"));
            }
        }
        for x in number(a) {
            for y in &right {
                out.push(format!("{x} ରୁ {y}"));
            }
        }
    }
    for (i, c) in s.char_indices() {
        if matches!(c, 'x' | 'X' | '×') {
            let a = &s[..i];
            let b = &s[i + c.len_utf8()..];
            for x in number(a.strip_suffix(' ').unwrap_or(a)) {
                for y in number(b.strip_prefix(' ').unwrap_or(b)) {
                    out.push(format!("{x} ବାଇ {y}"));
                }
            }
        }
    }
    out
}
fn money_amount(s: &str, major: &str, minor: &str) -> Vec<String> {
    let mut v = Vec::new();
    if let Some(w) = core(s) {
        v.push(format!("{w} {major}"));
    }
    for (i, c) in s.char_indices() {
        if c == '.' || c == ',' {
            let a = &s[..i];
            let b = &s[i + 1..];
            let ds: Option<Vec<_>> = b.chars().map(digit).collect();
            if let Some(ds) = ds {
                if ds.len() == 2 {
                    if let Some(w) = core(a) {
                        let m = CARDINALS[ds[0] as usize * 10 + ds[1] as usize];
                        if a.chars().count() == 1 && a.chars().next().and_then(digit) == Some(0) {
                            v.push(format!("{m} {minor}"));
                        }
                        v.push(format!("{w} {major} {m} {minor}"));
                    }
                }
            }
        }
    }
    for (suffix, word) in
        [("k", "ହଜାର"), ("K", "ହଜାର"), ("M", "ମିଲିଅନ୍"), ("B", "ବିଲିଅନ୍"), ("କୋଟି", "କୋଟି"), ("ଲକ୍ଷ", "ଲକ୍ଷ"), ("ହଜାର", "ହଜାର")]
    {
        if let Some(a) = s.strip_suffix(suffix) {
            let a = a.strip_suffix(' ').unwrap_or(a);
            if let Some(w) = decimal(a).or_else(|| core(a)) {
                v.push(format!("{w} {word} {major}"));
            }
        }
    }
    v
}
fn money_single(s: &str) -> Vec<String> {
    let mut out = Vec::new();
    let (neg, base) =
        if let Some(a) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) { ("ଋଣ ", a) } else { ("", s) };
    for suffix in ["¢", "c", "ct"] {
        if let Some(a) = base.strip_suffix(suffix) {
            if let Some(w) = core(a.strip_suffix(' ').unwrap_or(a)) {
                out.push(format!("{neg}{w} ସେଣ୍ଟ"));
            }
        }
    }
    let mut per = "";
    let mut base = base;
    for (k, v) in [
        ("/h", " ପ୍ରତି ଘଣ୍ଟା"),
        ("/hr", " ପ୍ରତି ଘଣ୍ଟା"),
        ("/kg", " ପ୍ରତି କିଲୋଗ୍ରାମ୍"),
        ("/m", " ପ୍ରତି ମିଟର"),
        ("/day", " ପ୍ରତି ଦିନ"),
        ("/yr", " ପ୍ରତି ବର୍ଷ"),
    ] {
        if let Some(a) = base.strip_suffix(k) {
            base = a;
            per = v;
            break;
        }
    }
    for (key, major, minor) in rows(include_str!("../../../data/or/currency_major_minor.tsv")) {
        if let Some(a) = base.strip_prefix(key) {
            for w in money_amount(a.strip_prefix(' ').unwrap_or(a), major, minor) {
                out.push(format!("{neg}{w}{per}"));
            }
        }
        if let Some(a) = base.strip_suffix(key) {
            for w in money_amount(a.strip_suffix(' ').unwrap_or(a), major, minor) {
                out.push(format!("{neg}{w}{per}"));
            }
        }
    }
    out
}
pub fn money(s: &str) -> Vec<String> {
    let mut out = money_single(s);
    for (a, b) in ranges(s) {
        for x in money_single(a) {
            for y in money_single(b) {
                out.push(format!("{x} ରୁ {y}"));
            }
        }
    }
    out
}
