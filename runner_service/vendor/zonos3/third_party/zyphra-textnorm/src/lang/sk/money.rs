use super::{measure::agree, number::positive, numeric::digits};
#[derive(Clone, Copy)]
struct Currency<'a> {
    symbols: &'a str,
    one: &'a str,
    few: &'a str,
    many: &'a str,
    genitive: &'a str,
    gender: &'a str,
    minor_one: &'a str,
    minor_few: &'a str,
    minor_many: &'a str,
    minor_gender: &'a str,
    minor_symbols: &'a str,
}
static CURRENCIES: std::sync::LazyLock<Vec<Currency<'static>>> = std::sync::LazyLock::new(|| {
    include_str!("../../../data/sk/currencies.tsv")
        .lines()
        .filter(|l| !l.starts_with('#'))
        .filter_map(|l| {
            let mut p = l.split('\t');
            Some(Currency {
                symbols: p.next()?,
                one: p.next()?,
                few: p.next()?,
                many: p.next()?,
                genitive: p.next()?,
                gender: p.next()?,
                minor_one: p.next()?,
                minor_few: p.next()?,
                minor_many: p.next()?,
                minor_gender: p.next()?,
                minor_symbols: p.next()?,
            })
        })
        .collect()
});
// Split the source spellings once; matching keeps their original row order.
static SYMBOLS: std::sync::LazyLock<Vec<Vec<&'static str>>> =
    std::sync::LazyLock::new(|| CURRENCIES.iter().map(|c| c.symbols.split('|').collect()).collect());
static MINOR_SYMBOLS: std::sync::LazyLock<Vec<Vec<&'static str>>> =
    std::sync::LazyLock::new(|| CURRENCIES.iter().map(|c| c.minor_symbols.split('|').collect()).collect());
static CURRENCY_WORDS: std::sync::LazyLock<std::collections::HashSet<Vec<char>>> = std::sync::LazyLock::new(|| {
    SYMBOLS.iter().flatten().filter(|s| s.chars().all(char::is_alphabetic)).map(|s| s.chars().collect()).collect()
});
pub(super) fn is_currency_word(word: &[char]) -> bool {
    CURRENCY_WORDS.contains(word)
}
static SCALES: std::sync::LazyLock<Vec<[&'static str; 6]>> = std::sync::LazyLock::new(|| {
    include_str!("../../../data/sk/scales.tsv")
        .lines()
        .filter(|l| !l.starts_with('#'))
        .filter_map(|l| {
            let mut p = l.split('\t');
            Some([p.next()?, p.next()?, p.next()?, p.next()?, p.next()?, p.next()?])
        })
        .collect()
});
static SCALE_SUFFIXES: std::sync::LazyLock<[Vec<(&'static str, [&'static str; 6])>; 256]> =
    std::sync::LazyLock::new(|| {
        let mut index: [Vec<(&'static str, [&'static str; 6])>; 256] = std::array::from_fn(|_| Vec::new());
        for &row in SCALES.iter() {
            for marker in row[0].split('|') {
                if let Some(&last) = marker.as_bytes().last() {
                    index[last as usize].push((marker, row));
                }
            }
        }
        index
    });
// First-byte index of the source currency markers.  A marker may occur at
// either end or inside a range/compound, so retain every overlapping match.
static MARKERS: std::sync::LazyLock<[Vec<(&'static [u8], usize)>; 256]> = std::sync::LazyLock::new(|| {
    let mut index: [Vec<(&'static [u8], usize)>; 256] = std::array::from_fn(|_| Vec::new());
    for (row, currency) in CURRENCIES.iter().enumerate() {
        for symbol in currency.symbols.split('|') {
            let bytes = symbol.as_bytes();
            if let Some(first) = bytes.first() {
                index[*first as usize].push((bytes, row));
            }
        }
    }
    index
});
static END_MARKERS: std::sync::LazyLock<[Vec<(&'static [u8], usize)>; 256]> = std::sync::LazyLock::new(|| {
    let mut index: [Vec<(&'static [u8], usize)>; 256] = std::array::from_fn(|_| Vec::new());
    for entries in MARKERS.iter() {
        for &(symbol, row) in entries {
            if let Some(&last) = symbol.last() {
                index[last as usize].push((symbol, row));
            }
        }
    }
    index
});
pub(super) fn suffix_currencies(s: &str) -> u64 {
    let bytes = s.as_bytes();
    let Some(&last) = bytes.last() else { return 0 };
    let mut mask = 0u64;
    for &(symbol, row) in &END_MARKERS[last as usize] {
        if bytes.ends_with(symbol) {
            mask |= 1u64 << row;
        }
    }
    mask
}
pub(super) fn matching_currencies(s: &str) -> u64 {
    let bytes = s.as_bytes();
    let mut mask = 0u64;
    for (i, &byte) in bytes.iter().enumerate() {
        for &(symbol, row) in &MARKERS[byte as usize] {
            if bytes[i..].starts_with(symbol) {
                mask |= 1u64 << row;
            }
        }
    }
    mask
}
pub(super) fn input_symbols() -> impl Iterator<Item = &'static str> {
    CURRENCIES
        .iter()
        .flat_map(|c| c.symbols.split('|').chain(c.minor_symbols.split('|')))
        .chain(SCALES.iter().flat_map(|s| s[0].split('|')))
}
fn number(s: &str) -> Option<(String, f64)> {
    if let Some(x) = positive(s) {
        return Some((x, 0.0));
    }
    if s.starts_with('0') {
        return None;
    }
    Some((super::number::grouped(s, &[' ', '\u{a0}', '\u{202f}', '.', ','])?, -0.02))
}
fn agreement(s: &str, one: &str, few: &str, many: &str, gender: &str) -> Option<(String, f64)> {
    if let Some(v) = agree(s, one, few, many, gender) {
        return Some((v, 0.0));
    }
    let (v, c) = number(s)?;
    Some((spoken!("{v} {many}"), c))
}
fn minor(s: &str, c: &Currency) -> Option<String> {
    if !(1..=2).contains(&s.len()) || !s.bytes().all(|x| x.is_ascii_digit()) {
        return None;
    }
    let n: usize = s.parse::<usize>().ok()? * if s.len() == 1 { 10 } else { 1 };
    if n == 0 {
        return None;
    }
    if !super::building() {
        return Some(String::new());
    }
    Some(agree(&n.to_string(), c.minor_one, c.minor_few, c.minor_many, c.minor_gender)?)
}
fn spoken_each(s: &str, c: &Currency, mut emit: impl FnMut(String, f64)) {
    if let Some((value, weight)) = agreement(s, c.one, c.few, c.many, c.gender) {
        emit(value, weight)
    }
    // No fractional separator or scale marker can be present in a digit run.
    if s.bytes().all(|b| b.is_ascii_digit()) {
        return;
    }
    for (i, ch) in s.char_indices() {
        if ch != ',' && ch != '.' {
            continue;
        }
        let a = &s[..i];
        let b = &s[i + 1..];
        if let Some((maj, cost)) = agreement(a, c.one, c.few, c.many, c.gender) {
            if b == "0" || b == "00" {
                emit(maj.clone(), cost);
            }
            if let Some(m) = minor(b, c) {
                emit(spoken!("{maj} {m}"), cost);
            }
        }
        if a == "0" {
            if let Some(m) = minor(b, c) {
                emit(m, -0.01);
            }
        }
        if (3..=12).contains(&b.len()) {
            if let (Some((n, cost)), Some(d)) = (number(a), digits(b)) {
                emit(spoken!("{n} čiarka {d} {}", c.genitive), cost);
            }
        }
    }
    if let Some(&last) = s.as_bytes().last() {
        for &(marker, p) in &SCALE_SUFFIXES[last as usize] {
            if let Some(n) = s.strip_suffix(marker) {
                let n = n.trim_end_matches(' ');
                if let Some((x, cost)) = agreement(n, p[1], p[2], p[3], p[5]) {
                    emit(spoken!("{x} {}", c.many), cost);
                }
                for (i, ch) in n.char_indices() {
                    if ch == ',' || ch == '.' {
                        if let (Some((v, cost)), Some(d)) = (number(&n[..i]), digits(&n[i + 1..])) {
                            emit(spoken!("{v} čiarka {d} {} {}", p[4], c.many), cost);
                        }
                    }
                }
            }
        }
    }
}
fn amount(s: &str, mask: u64) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    // A major/minor compound needs two digit runs separated by currency
    // syntax. Grouped and fractional numbers may also satisfy this cheap
    // necessary condition and are handled by the existing recognizer.
    let mut runs = 0;
    let mut in_digits = false;
    for b in s.bytes() {
        let digit = b.is_ascii_digit();
        if digit && !in_digits {
            runs += 1;
            if runs == 2 {
                break;
            }
        }
        in_digits = digit;
    }
    let mut remaining = mask;
    while remaining != 0 {
        let row = remaining.trailing_zeros() as usize;
        remaining &= remaining - 1;
        let c = CURRENCIES[row];
        for &sym in &SYMBOLS[row] {
            if let Some(rest) = s.strip_prefix(sym) {
                spoken_each(rest.trim_start_matches(' '), &c, |value, weight| out.push((value, weight)));
            }
            if let Some(rest) = s.strip_suffix(sym) {
                spoken_each(rest.trim_end_matches(' '), &c, |value, weight| out.push((value, weight)));
            }
        }
        if runs < 2 {
            continue;
        }
        for (i, ch) in s.char_indices() {
            if ch != ' ' {
                continue;
            }
            let left = &s[..i];
            let right = &s[i + 1..];
            // Both split minor-unit forms require at least one ASCII digit on
            // the right; most ordinary whole amounts end at the currency.
            if !right.bytes().any(|b| b.is_ascii_digit()) {
                continue;
            }
            for &sym in &SYMBOLS[row] {
                let major = left
                    .strip_prefix(sym)
                    .map(|x| x.trim_start_matches(' '))
                    .or_else(|| left.strip_suffix(sym).map(|x| x.trim_end_matches(' ')));
                if let Some(major) = major {
                    if let Some((a, cost)) = agreement(major, c.one, c.few, c.many, c.gender) {
                        for &ms in &MINOR_SYMBOLS[row] {
                            let m = right
                                .strip_prefix(ms)
                                .map(|x| x.trim_start_matches(' '))
                                .or_else(|| right.strip_suffix(ms).map(|x| x.trim_end_matches(' ')));
                            if let Some(m) = m.and_then(|x| minor(x, &c)) {
                                out.push((spoken!("{a} {m}"), cost));
                            }
                        }
                    }
                }
            }
        }
    }
    out
}
/// Render an unambiguous whole-number/currency reading without rerunning the
/// full range, scale, decimal and minor-unit union.
pub(super) fn simple_money(s: &str) -> Option<String> {
    let (sign, body) = if let Some(rest) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) {
        ("mínus ", rest)
    } else {
        ("", s)
    };
    let mask = matching_currencies(body);
    if mask.count_ones() != 1 {
        return None;
    }
    let row = mask.trailing_zeros() as usize;
    let currency = CURRENCIES[row];
    let mut found = None;
    for &symbol in &SYMBOLS[row] {
        for number in [
            body.strip_prefix(symbol).map(|s| s.trim_start_matches(' ')),
            body.strip_suffix(symbol).map(|s| s.trim_end_matches(' ')),
        ]
        .into_iter()
        .flatten()
        {
            if number.is_empty() || !number.bytes().all(|b| b.is_ascii_digit()) {
                continue;
            }
            if let Some(value) = agree(number, currency.one, currency.few, currency.many, currency.gender) {
                if found.is_some() {
                    return None;
                }
                found = Some(spoken!("{sign}{value}"));
            }
        }
    }
    found
}
pub(super) fn money(s: &str) -> Vec<(String, f64)> {
    money_with_mask(s, matching_currencies(s))
}
pub(super) fn money_with_mask(s: &str, mask: u64) -> Vec<(String, f64)> {
    let (sign, s) =
        if let Some(x) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) { ("mínus ", x) } else { ("", s) };
    let mut rate = "";
    let mut body = s;
    for (suf, word) in [
        ("/h", " za hodinu"),
        ("/hr", " za hodinu"),
        ("/kg", " za kilogram"),
        ("/day", " za deň"),
        ("/month", " za mesiac"),
        ("/year", " za rok"),
    ] {
        if let Some(x) = s.strip_suffix(suf) {
            body = x.trim_end_matches(' ');
            rate = word;
            break;
        }
    }
    let mut out: Vec<_> = amount(body, mask).into_iter().map(|(x, c)| (spoken!("{sign}{x}{rate}"), c)).collect();
    for (i, ch) in body.char_indices() {
        if "-–—".contains(ch) {
            let a = body[..i].trim_end_matches(' ');
            let b = body[i + ch.len_utf8()..].trim_start_matches(' ');
            let mut left = amount(a, mask);
            if let Some(n) = number(a) {
                left.push(n)
            }
            let right = amount(b, mask);
            for (x, c) in &left {
                for (y, d) in &right {
                    out.push((spoken!("{sign}{x} až {y}{rate}"), c + d));
                }
            }
        }
    }
    out
}
