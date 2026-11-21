use super::graph_utils::{Gender, cardinal};
use super::numeric::number;
use std::collections::HashMap;
use std::sync::LazyLock;

fn minor(s: &str, lev: bool) -> Option<String> {
    if s.len() != 2 || !s.bytes().all(|c| c.is_ascii_digit()) {
        return None;
    }
    let n: usize = s.parse().ok()?;
    if n == 0 || n > 99 {
        return None;
    }
    let g = if lev { Gender::Feminine } else { Gender::Masculine };
    let v = number(&n.to_string(), g)?;
    Some(format!(
        "{v} {}",
        if lev {
            if n == 1 { "стотинка" } else { "стотинки" }
        } else if n == 1 {
            "цент"
        } else {
            "цента"
        }
    ))
}
fn amount(n: &str, kind: &str) -> Option<String> {
    let (g, one, many) = match kind {
        "lev" => (Gender::Masculine, "лев", "лева"),
        "stot" => (Gender::Feminine, "стотинка", "стотинки"),
        "eur" => (Gender::Neuter, "евро", "евро"),
        "usd" => (Gender::Masculine, "долар", "долара"),
        "gbp" => (Gender::Masculine, "паунд", "паунда"),
        "rub" => (Gender::Feminine, "рубла", "рубли"),
        "jpy" => (Gender::Feminine, "йени", "йени"),
        _ => (Gender::Masculine, "", kind),
    };
    let mut v = if kind == "eur" { cardinal(n, g)? } else { number(n, g)? };
    v.push(' ');
    v.push_str(if n == "1" { one } else { many });
    Some(v)
}

fn lev_parts(s: &str) -> Option<(&str, &'static str)> {
    let split = s.bytes().take_while(u8::is_ascii_digit).count();
    let (n, suffix) = s.split_at(split);
    if super::plain_cardinal(n) != Some(1.2) {
        return None;
    }
    let suffix = suffix.strip_prefix(' ').unwrap_or(suffix);
    let kind = match suffix {
        "лв." | "лв" | "лева" | "лев" | "BGN" => "lev",
        "ст." | "ст" | "стотинки" | "стотинка" => "stot",
        _ => return None,
    };
    Some((n, kind))
}

/// Direct unsigned lev/stotinka integer paths; spelling is deferred until selected.
pub(crate) fn simple_lev(s: &str) -> Option<usize> {
    let (n, _) = lev_parts(s)?;
    Some(if n == "1" { 2 } else { 1 })
}

pub(crate) fn render_simple_lev(s: &str) -> Option<String> {
    let (n, kind) = lev_parts(s)?;
    amount(n, kind)
}
const SYMBOLS: &[(&str, &str, bool, bool)] = &[
    ("лв.", "lev", false, true),
    ("лв", "lev", false, true),
    ("лева", "lev", false, true),
    ("лев", "lev", false, true),
    ("BGN", "lev", false, true),
    ("ст.", "stot", false, true),
    ("ст", "stot", false, true),
    ("стотинки", "stot", false, true),
    ("стотинка", "stot", false, true),
    ("€", "eur", true, true),
    ("EUR", "eur", true, true),
    ("евро", "eur", true, true),
    ("$", "usd", true, true),
    ("USD", "usd", true, true),
    ("щ.д.", "usd", true, true),
    ("£", "gbp", true, true),
    ("GBP", "gbp", true, true),
    ("₽", "rub", true, true),
    ("руб.", "rub", true, true),
    ("руб", "rub", true, true),
    ("рубли", "rub", true, true),
    ("RUB", "rub", true, true),
    ("¥", "jpy", true, true),
    ("JPY", "jpy", true, true),
    ("CHF", "швейцарски франка", false, true),
    ("CAD", "канадски долара", false, true),
    ("CA$", "канадски долара", true, true),
    ("AUD", "австралийски долара", false, true),
    ("A$", "австралийски долара", true, true),
    ("BRL", "бразилски реала", false, true),
    ("R$", "бразилски реала", true, true),
    ("MXN", "мексикански песо", false, true),
    ("MX$", "мексикански песо", true, true),
    ("INR", "индийски рупии", false, true),
    ("₹", "индийски рупии", true, true),
    ("Rs.", "индийски рупии", true, false),
    ("Rs", "индийски рупии", true, false),
    ("KRW", "вона", false, true),
    ("₩", "вона", false, true),
    ("TRY", "турски лири", false, true),
    ("₺", "турски лири", false, true),
    ("ILS", "шекела", false, true),
    ("₪", "шекела", false, true),
    ("UAH", "гривни", false, true),
    ("₴", "гривни", false, true),
    ("VND", "виетнамски донга", false, true),
    ("₫", "виетнамски донга", false, true),
    ("BDT", "бангладешки таки", false, true),
    ("৳", "бангладешки таки", false, true),
    ("₦", "нигерийски найри", true, false),
    ("PHP", "филипински песо", false, true),
    ("₱", "филипински песо", false, true),
    ("THB", "тайландски бата", false, true),
    ("฿", "тайландски бата", false, true),
    ("AMD", "арменски драма", false, true),
    ("֏", "арменски драма", false, true),
    ("¢", "цента", false, true),
];

static SYMBOL_INDEX: LazyLock<(HashMap<char, u128>, HashMap<char, u128>)> = LazyLock::new(|| {
    let mut prefix = HashMap::new();
    let mut suffix = HashMap::new();
    for (index, &(symbol, _, leading, trailing)) in SYMBOLS.iter().enumerate() {
        if leading {
            *prefix.entry(symbol.chars().next().unwrap()).or_insert(0u128) |= 1u128 << index;
        }
        if trailing {
            *suffix.entry(symbol.chars().next_back().unwrap()).or_insert(0u128) |= 1u128 << index;
        }
    }
    (prefix, suffix)
});

static END_CHARS: LazyLock<Vec<char>> = LazyLock::new(|| {
    let mut chars: Vec<_> = SYMBOLS
        .iter()
        .filter(|(_, _, _, trailing)| *trailing)
        .filter_map(|(symbol, _, _, _)| symbol.chars().next_back())
        .collect();
    chars.extend(
        ["k", "K", "M", "млн", "млн.", "/h", "/ч", "/час", "/kg", "/кг"]
            .iter()
            .filter_map(|suffix| suffix.chars().next_back()),
    );
    chars.sort_unstable();
    chars.dedup();
    chars
});

/// End characters admitted by a number, currency, magnitude, or rate branch.
pub(crate) fn possible_end(c: char) -> bool {
    c.is_ascii_digit() || c == ' ' || END_CHARS.binary_search(&c).is_ok()
}

fn numeric_area(s: &str) -> bool {
    s.chars().all(|c| c.is_ascii_digit() || matches!(c, ' ' | '\u{a0}' | '.' | ','))
}

/// Reject basic currency spans whose numeric body already contains nonnumeric
/// material. Ranges, rates and magnitude branches retain their own recognizers.
pub(crate) fn possible_body(s: &str) -> bool {
    if s.chars().any(|c| matches!(c, '-' | '−' | '–' | '—' | '/')) {
        return true;
    }
    if (s.starts_with('$') || s.starts_with('€'))
        && ["k", "K", "M", "млн", "млн."].iter().any(|suffix| s.ends_with(suffix))
    {
        return true;
    }
    if numeric_area(s) {
        return true;
    }
    let mut leading = s.chars().next().and_then(|c| SYMBOL_INDEX.0.get(&c)).copied().unwrap_or(0);
    let trailing = s.chars().next_back().and_then(|c| SYMBOL_INDEX.1.get(&c)).copied().unwrap_or(0);
    let mut suffixes = trailing;
    while suffixes != 0 {
        let index = suffixes.trailing_zeros() as usize;
        suffixes &= suffixes - 1;
        if s.strip_suffix(SYMBOLS[index].0).is_some_and(numeric_area) {
            return true;
        }
    }
    while leading != 0 {
        let index = leading.trailing_zeros() as usize;
        leading &= leading - 1;
        if let Some(rest) = s.strip_prefix(SYMBOLS[index].0) {
            if numeric_area(rest) {
                return true;
            }
            let mut suffixes = trailing;
            while suffixes != 0 {
                let index = suffixes.trailing_zeros() as usize;
                suffixes &= suffixes - 1;
                if rest.strip_suffix(SYMBOLS[index].0).is_some_and(numeric_area) {
                    return true;
                }
            }
        }
    }
    false
}

fn simple(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    let mut matching = s.chars().next().and_then(|c| SYMBOL_INDEX.0.get(&c)).copied().unwrap_or(0)
        | s.chars().next_back().and_then(|c| SYMBOL_INDEX.1.get(&c)).copied().unwrap_or(0);
    while matching != 0 {
        let index = matching.trailing_zeros() as usize;
        matching &= matching - 1;
        let (sym, kind, leading, trailing) = SYMBOLS[index];
        let prefix = leading.then(|| s.strip_prefix(sym)).flatten().map(|n| n.strip_prefix(' ').unwrap_or(n));
        let suffix = trailing.then(|| s.strip_suffix(sym)).flatten().map(|n| n.strip_suffix(' ').unwrap_or(n));
        for n in [prefix, suffix].into_iter().flatten() {
            let internal = if matches!(kind, "lev" | "stot" | "eur" | "usd") {
                1.05
            } else if matches!(kind, "gbp" | "rub" | "jpy") {
                1.08
            } else {
                1.1
            };
            let g = match kind {
                "индийски рупии" | "турски лири" | "гривни" | "бангладешки таки" | "нигерийски найри" => {
                    Gender::Feminine
                }
                "мексикански песо" | "филипински песо" => Gender::Neuter,
                _ => Gender::Masculine,
            };
            let v = if internal == 1.1 {
                let n = if matches!(kind, "мексикански песо" | "филипински песо") {
                    cardinal(n, g)
                } else {
                    number(n, g)
                };
                n.map(|v| format!("{v} {kind}"))
            } else {
                amount(n, kind)
            };
            if let Some(v) = v {
                out.push((v, internal));
            }
            if n == "1" {
                let alt = match kind {
                    "lev" => "един лева",
                    "stot" => "една стотинки",
                    "usd" => "един долара",
                    "gbp" => "един паунда",
                    "rub" => "една рубли",
                    _ => "",
                };
                if !alt.is_empty() {
                    out.push((alt.to_owned(), internal));
                }
            }
            if matches!(kind, "lev" | "eur" | "usd") {
                for (i, c) in n.char_indices() {
                    if !matches!(c, '.' | ',') {
                        continue;
                    }
                    let major = &n[..i];
                    let mn = &n[i + 1..];
                    if mn.len() != 2 {
                        continue;
                    }
                    let alt = if major == "1" {
                        match kind {
                            "lev" => Some("един лева"),
                            "usd" => Some("един долара"),
                            _ => None,
                        }
                    } else {
                        None
                    };
                    if mn == "00" {
                        if let Some(v) = amount(major, kind) {
                            out.push((v, internal));
                        }
                        if let Some(v) = alt {
                            out.push((v.to_owned(), internal));
                        }
                    } else if let Some(m) = minor(mn, kind == "lev") {
                        if major == "0" {
                            out.push((m.clone(), internal - 0.01));
                        }
                        if let Some(v) = amount(major, kind) {
                            out.push((format!("{v} и {m}"), internal));
                        }
                        if let Some(v) = alt {
                            out.push((format!("{v} и {m}"), internal));
                        }
                    }
                }
            }
        }
    }
    // The zero-major and zero-minor euro/USD paths explicitly allow omitted symbols.
    if s.contains(['.', ',']) {
        for (kind, symbols) in [("eur", &["", "€", "EUR", "евро"][..]), ("usd", &["", "$", "USD", "щ.д."][..])]
        {
            for &prefix in symbols {
                if let Some(t) = s.strip_prefix(prefix) {
                    let t = t.strip_prefix(' ').unwrap_or(t);
                    for &suffix in symbols {
                        if let Some(n) = t.strip_suffix(suffix) {
                            let n = n.strip_suffix(' ').unwrap_or(n);
                            for (i, c) in n.char_indices() {
                                if !matches!(c, '.' | ',') {
                                    continue;
                                }
                                let a = &n[..i];
                                let b = &n[i + 1..];
                                if b.len() != 2 {
                                    continue;
                                }
                                if a == "0" {
                                    if let Some(m) = minor(b, false) {
                                        out.push((m, 1.04));
                                    }
                                }
                                if b == "00" {
                                    if let Some(v) = amount(a, kind) {
                                        out.push((v, 1.05));
                                    }
                                }
                            }
                        }
                    }
                }
            }
        }
    }
    out
}
fn is_lev(s: &str) -> bool {
    ["лв.", "лв", "лева", "лев", "BGN", "ст.", "ст", "стотинки", "стотинка"].iter().any(|&x| s.ends_with(x))
}
fn is_euro(s: &str) -> bool {
    ["€", "EUR", "евро"].iter().any(|&x| s.starts_with(x) || s.ends_with(x))
        || s.bytes().all(|c| c.is_ascii_digit() || matches!(c, b'.' | b',' | b' '))
}
fn primary(s: &str) -> Vec<(String, f64)> {
    let mut out: Vec<_> = simple(s).into_iter().filter(|(_, w)| *w <= 1.05).map(|(v, w)| (v, w - 1.05)).collect();
    if is_lev(s) {
        if let Some(t) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) {
            for (v, w) in simple(t) {
                if w <= 1.05 {
                    out.push((format!("минус {v}"), w - 1.05));
                }
            }
        }
    }
    out
}
pub(crate) fn classify(s: &str) -> Vec<(String, f64)> {
    let mut out = simple(s);
    if let Some(t) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) {
        for (v, w) in primary(t) {
            out.push((format!("минус {v}"), 1.0 + w));
        }
        if is_lev(t) {
            for (v, w) in primary(t) {
                out.push((format!("минус {v}"), 1.05 + w));
            }
        }
    }
    for (suffix, word) in
        [("/h", "на час"), ("/ч", "на час"), ("/час", "на час"), ("/kg", "на килограм"), ("/кг", "на килограм")]
    {
        if let Some(t) = s.strip_suffix(suffix) {
            for (v, w) in primary(t.strip_suffix(' ').unwrap_or(t)) {
                out.push((format!("{v} {word}"), 0.96 + w));
            }
        }
    }
    for (symbol, currency) in [("$", "долара"), ("€", "евро")] {
        if let Some(t) = s.strip_prefix(symbol) {
            let t = t.strip_prefix(' ').unwrap_or(t);
            for (suffix, mag, g) in [
                ("k", "хиляди", Gender::Feminine),
                ("K", "хиляди", Gender::Feminine),
                ("M", "милиона", Gender::Masculine),
                ("млн.", "милиона", Gender::Masculine),
                ("млн", "милиона", Gender::Masculine),
            ] {
                if let Some(n) = t.strip_suffix(suffix) {
                    if let Some(v) = number(n, g) {
                        out.push((format!("{v} {mag} {currency}"), 0.95));
                    }
                    if mag == "милиона" {
                        let n = n.strip_suffix(' ').unwrap_or(n);
                        if let Some((a, b)) = n.split_once(|c| c == '.' || c == ',') {
                            if b.len() == 1 {
                                if let (Some(x), Some(y)) = (number(a, Gender::Masculine), number(b, Gender::Masculine))
                                {
                                    out.push((format!("{x} цяло и {y} милиона {currency}"), 0.95));
                                }
                            }
                        }
                    }
                }
            }
        }
    }
    for (i, c) in s.char_indices() {
        if !matches!(c, '-' | '–' | '—') {
            continue;
        }
        let a = s[..i].strip_suffix(' ').unwrap_or(&s[..i]);
        let tail = &s[i + c.len_utf8()..];
        let b = tail.strip_prefix(' ').unwrap_or(tail);
        // The dollar/dollar branch omits the first denomination. It has the
        // same weight as the expanded range, and wins the source's tie.
        if let (Some(a), Some(b)) = (a.strip_prefix('$'), b.strip_prefix('$')) {
            if let (Some(x), Some(y)) = (number(a, Gender::Masculine), number(b, Gender::Masculine)) {
                out.push((format!("{x} до {y} долара"), 0.98));
            }
        }
        let left = primary(a);
        let right = primary(b);
        for (x, wx) in &left {
            for (y, wy) in &right {
                out.push((format!("{x} до {y}"), 0.98 + wx + wy));
            }
        }
        if is_lev(b) {
            if let Some(x) = number(a, Gender::Masculine) {
                for (y, wy) in &right {
                    out.push((format!("{x} до {y}"), 0.98 + wy));
                }
            }
        }
        if is_euro(b) {
            if let Some(x) = cardinal(a, Gender::Neuter) {
                for (y, wy) in &right {
                    out.push((format!("{x} до {y}"), 0.98 + wy));
                }
            }
        }
    }
    out
}
