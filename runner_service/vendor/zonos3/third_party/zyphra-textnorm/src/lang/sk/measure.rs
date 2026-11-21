use super::{number::positive, numeric::decimal};
#[derive(Clone, Copy)]
struct Unit<'a> {
    symbols: &'a str,
    one: &'a str,
    few: &'a str,
    many: &'a str,
    genitive: &'a str,
    gender: &'a str,
}
static UNITS: std::sync::LazyLock<Vec<Unit<'static>>> = std::sync::LazyLock::new(|| {
    include_str!("../../../data/sk/units.tsv")
        .lines()
        .filter(|l| !l.starts_with('#'))
        .filter_map(|l| {
            let mut p = l.split('\t');
            Some(Unit {
                symbols: p.next()?,
                one: p.next()?,
                few: p.next()?,
                many: p.next()?,
                genitive: p.next()?,
                gender: p.next()?,
            })
        })
        .collect()
});
static UNIT_SUFFIXES: std::sync::LazyLock<[Vec<(&'static str, Unit<'static>)>; 256]> = std::sync::LazyLock::new(|| {
    let mut index: [Vec<(&'static str, Unit<'static>)>; 256] = std::array::from_fn(|_| Vec::new());
    for unit in UNITS.iter().copied() {
        for symbol in unit.symbols.split('|') {
            if let Some(&last) = symbol.as_bytes().last() {
                index[last as usize].push((symbol, unit));
            }
        }
    }
    index
});
pub(super) fn symbols() -> impl Iterator<Item = &'static str> {
    UNITS.iter().flat_map(|unit| unit.symbols.split('|'))
}
pub(super) fn agree(n: &str, one: &str, few: &str, many: &str, gender: &str) -> Option<String> {
    if !super::building() {
        positive(n)?;
        return Some(String::new());
    }
    let word = match n {
        "1" => match gender {
            "f" => "jedna",
            "n" => "jedno",
            _ => "jeden",
        }
        .into(),
        "2" => if gender == "m" { "dva" } else { "dve" }.into(),
        _ => positive(n)?,
    };
    let form = match n {
        "1" => one,
        "2" | "3" | "4" => few,
        _ => many,
    };
    Some(spoken!("{word} {form}"))
}
fn core(s: &str, genitive: bool) -> Option<String> {
    let last = *s.as_bytes().last()? as usize;
    for &(sym, u) in &UNIT_SUFFIXES[last] {
        if let Some(raw) = s.strip_suffix(sym) {
            let n = raw.trim_end_matches(' ');
            if let Some(d) = decimal(n) {
                return Some(spoken!("{d} {}", u.genitive));
            }
            if genitive && n == "1" {
                let word = if u.gender == "f" { "jednej" } else { "jedného" };
                return Some(spoken!("{word} {}", u.genitive));
            }
            if let Some(v) = agree(n, u.one, u.few, u.many, u.gender) {
                return Some(v);
            }
        }
    }
    None
}
pub(super) fn measure(s: &str) -> Vec<String> {
    let mut out = Vec::new();
    measure_each(s, |value| out.push(value));
    out
}
pub(super) fn measure_each(s: &str, mut emit: impl FnMut(String)) {
    for (a, b) in [("°", "stupeň"), ("%", "percento"), ("‰", "promile")] {
        if s == a {
            emit(if super::building() { b.into() } else { String::new() });
        }
    }
    for p in ["okolo", "Okolo"] {
        if let Some(x) = s.strip_prefix(p) {
            let x = x.strip_prefix(' ').unwrap_or(x);
            if let Some(y) = core(x, true) {
                emit(spoken!("okolo {y}"));
            }
        }
    }
    let (prefix, s) = if let Some(x) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) {
        ("mínus ", x.trim_start_matches(' '))
    } else if let Some(x) = s.strip_prefix('~') {
        ("približne ", x.trim_start_matches(' '))
    } else if let Some(x) = s.strip_prefix('±') {
        ("plus-mínus ", x.trim_start_matches(' '))
    } else {
        ("", s)
    };
    if let Some(x) = core(s, false) {
        emit(spoken!("{prefix}{x}"));
    }
    for (i, c) in s.char_indices() {
        if "-–—".contains(c) {
            let a = s[..i].trim_end_matches(' ');
            let b = s[i + c.len_utf8()..].trim_start_matches(' ');
            if let (Some(x), Some(y)) = (positive(a).or_else(|| decimal(a)), core(b, false)) {
                emit(spoken!("{prefix}{x} až {y}"));
            }
        }
    }
}
