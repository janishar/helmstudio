use super::number;

const MAJOR: &[(&[&str], &[&str], &str, &str)] = &[
    (&["kr.", "kr", "NOK"], &["kr.", "kr", "NOK", "kroner"], "kroner", "øre"),
    (&["US$", "USD", "$"], &["$", "USD", "dollar"], "dollar", "cent"),
    (&["€", "EUR"], &["€", "EUR", "euro"], "euro", "cent"),
];
const WORLD: &[(&[&str], &[&str], &str)] = &[
    (&["£", "GBP"], &["£", "GBP", "britiske pund"], "pund"),
    (&["¥", "JPY"], &["¥", "JPY", "yen"], "yen"),
    (&["CA$", "CAD"], &["CAD"], "kanadiske dollar"),
    (&["A$", "AUD"], &["AUD"], "australske dollar"),
    (&[], &["SEK"], "svenske kroner"),
    (&[], &["DKK"], "danske kroner"),
    (&[], &["CHF"], "sveitsiske franc"),
    (&["₹", "INR", "Rs.", "Rs"], &["INR"], "indiske rupier"),
    (&["₽", "RUB"], &["₽", "RUB"], "russiske rubler"),
    (&["R$", "BRL"], &[], "brasilianske real"),
    (&["MX$", "MXN"], &[], "meksikanske pesos"),
    (&[], &["₩", "KRW"], "sørkoreanske won"),
    (&[], &["₺", "TRY"], "tyrkiske lire"),
    (&[], &["₪", "ILS"], "israelske sjekler"),
    (&[], &["₴", "UAH"], "ukrainske hryvnia"),
    (&[], &["₫", "VND"], "vietnamesiske dong"),
    (&[], &["৳", "BDT"], "bangladeshiske taka"),
    (&["₦", "NGN"], &[], "nigerianske naira"),
    (&[], &["₱", "PHP"], "filippinske pesos"),
    (&[], &["฿", "THB"], "thailandske baht"),
    (&[], &["֏", "AMD"], "armenske dram"),
    (&[], &["¢"], "cent"),
];

struct CurrencyKeys {
    prefix: [Vec<&'static str>; 256],
    suffix: [Vec<&'static str>; 256],
}

fn currency_keys() -> &'static CurrencyKeys {
    static KEYS: std::sync::LazyLock<CurrencyKeys> = std::sync::LazyLock::new(|| {
        let mut keys =
            CurrencyKeys { prefix: std::array::from_fn(|_| Vec::new()), suffix: std::array::from_fn(|_| Vec::new()) };
        let mut add = |prefix: &[&'static str], suffix: &[&'static str]| {
            for &key in prefix {
                keys.prefix[key.as_bytes()[0] as usize].push(key);
            }
            for &key in suffix {
                keys.suffix[key.as_bytes()[key.len() - 1] as usize].push(key);
            }
        };
        for &(prefix, suffix, _, _) in MAJOR {
            add(prefix, suffix);
        }
        for &(prefix, suffix, _) in WORLD {
            add(prefix, suffix);
        }
        keys
    });
    &KEYS
}

// A base amount consists of a source currency key and numeric material, or
// a clean krone amount. This deliberately admits a superset of the numeric
// grammar; it only rules out impossible continuations before rendering.
fn possible_base(s: &str) -> bool {
    let Some((&first, &last)) = s.as_bytes().first().zip(s.as_bytes().last()) else { return false };
    let numeric = |rest: &str| {
        rest.bytes().all(|b| b.is_ascii_digit() || matches!(b, b' ' | b'.' | b',' | b'-' | b'k' | b'K' | b'M' | b'm'))
    };
    if (s.ends_with(",-") || s.ends_with(",00") || s.ends_with(".00")) && numeric(s) {
        return true;
    }
    let keys = currency_keys();
    keys.prefix[first as usize].iter().any(|key| s.strip_prefix(key).is_some_and(numeric))
        || keys.suffix[last as usize].iter().any(|key| s.strip_suffix(key).is_some_and(numeric))
}

/// Input alphabet derived from the same currency keys and source syntax used
/// below. Once a span contains another character no extension can be money.
pub fn possible_char(c: char) -> bool {
    static ALPHABET: std::sync::LazyLock<([bool; 128], Vec<char>)> = std::sync::LazyLock::new(|| {
        let mut ascii = [false; 128];
        let mut non_ascii = Vec::new();
        let mut add = |c: char| {
            if c.is_ascii() {
                ascii[c as usize] = true;
            } else {
                non_ascii.push(c);
            }
        };
        for c in "0123456789 .,-−–—/kKMmht".chars() {
            add(c);
        }
        for &(pre, suf, _, _) in MAJOR {
            for key in pre.iter().chain(suf) {
                for c in key.chars() {
                    add(c);
                }
            }
        }
        for &(pre, suf, _) in WORLD {
            for key in pre.iter().chain(suf) {
                for c in key.chars() {
                    add(c);
                }
            }
        }
        for c in "/kg/kilo".chars() {
            add(c);
        }
        non_ascii.sort_unstable();
        non_ascii.dedup();
        (ascii, non_ascii)
    });
    if c.is_ascii() { ALPHABET.0[c as usize] } else { ALPHABET.1.binary_search(&c).is_ok() }
}

/// Every currency path ends in a digit, a currency key, a rate, a magnitude,
/// or the clean-cash suffix. Index source keys by their last character so
/// unrelated sentence continuations never enter the currency parser.
pub fn possible_end(s: &str) -> bool {
    let Some(&last) = s.as_bytes().last() else { return false };
    if last.is_ascii_digit() {
        return true;
    }
    static SUFFIXES: std::sync::LazyLock<[Vec<&'static str>; 256]> = std::sync::LazyLock::new(|| {
        let mut map: [Vec<&'static str>; 256] = std::array::from_fn(|_| Vec::new());
        let mut add = |key: &'static str| {
            map[key.as_bytes()[key.len() - 1] as usize].push(key);
        };
        for &(_, suffixes, _, _) in MAJOR {
            for &key in suffixes {
                add(key);
            }
        }
        for &(_, suffixes, _) in WORLD {
            for &key in suffixes {
                add(key);
            }
        }
        for key in ["/h", "/t", "/kg", "/kilo", "k", "K", "M", "m", ",-"] {
            add(key);
        }
        map
    });
    SUFFIXES[last as usize].iter().any(|key| s.ends_with(key))
}
fn trim_one_start(s: &str) -> &str {
    s.strip_prefix(' ').unwrap_or(s)
}
fn trim_one_end(s: &str) -> &str {
    s.strip_suffix(' ').unwrap_or(s)
}
fn amount(s: &str, unit: &str, minor: &str) -> Option<String> {
    let integer = |n: &str| -> Option<String> {
        if n == "0" {
            return None;
        }
        let x = number::cardinal(n, false)?;
        Some(format!("{x} {}", if unit == "kroner" && n == "1" { "krone" } else { unit }))
    };
    if let Some(x) = s.strip_suffix(",-").or_else(|| s.strip_suffix(",00")).or_else(|| s.strip_suffix(".00")) {
        return integer(x);
    }
    for sep in [',', '.'] {
        if let Some((a, b)) = s.split_once(sep) {
            if b.len() != 2 || !b.bytes().all(|x| x.is_ascii_digit()) || b == "00" {
                continue;
            }
            let n: u32 = b.parse().ok()?;
            let cents = if n == 1 { "ett".to_string() } else { number::cardinal(&n.to_string(), unit == "kroner")? };
            let cents = format!("{cents} {minor}");
            return if a == "0" { Some(cents) } else { Some(format!("{} og {cents}", integer(a)?)) };
        }
    }
    integer(s)
}
fn base(s: &str) -> Vec<String> {
    // An unprefixed numeric amount ending in a digit has no currency suffix.
    // Only the source's three clean-krone suffixes can accept such a span.
    if s.as_bytes().first().is_some_and(u8::is_ascii_digit)
        && s.as_bytes().last().is_some_and(u8::is_ascii_digit)
        && !s.ends_with(",00")
        && !s.ends_with(".00")
    {
        return Vec::new();
    }
    if !possible_base(s) {
        return Vec::new();
    }
    let mut v = Vec::new();
    for &(pre, suf, unit, minor) in MAJOR {
        for p in pre {
            if let Some(rest) = s.strip_prefix(p) {
                if let Some(w) = amount(trim_one_start(rest), unit, minor) {
                    v.push(w)
                }
            }
        }
        for p in suf {
            if let Some(rest) = s.strip_suffix(p) {
                if let Some(w) = amount(trim_one_end(rest), unit, minor) {
                    v.push(w)
                }
            }
        }
    }
    if s.ends_with(",-") || s.ends_with(",00") || s.ends_with(".00") {
        if let Some(w) = amount(s, "kroner", "øre") {
            v.push(w)
        }
    }
    for &(pre, suf, word) in WORLD {
        for p in pre {
            if let Some(n) = s.strip_prefix(p).and_then(|x| number::cardinal(trim_one_start(x), false)) {
                v.push(format!("{n} {word}"));
            }
        }
        for p in suf {
            if let Some(n) = s.strip_suffix(p).and_then(|x| number::cardinal(trim_one_end(x), false)) {
                v.push(format!("{n} {word}"));
            }
        }
    }
    if let Some(n) = s.strip_suffix("GBP").and_then(|x| number::cardinal(trim_one_end(x), false)) {
        v.push(format!("{n} britiske pund"));
    }
    for (prefix, currency) in [("$", "dollar"), ("€", "euro")] {
        if let Some(x) = s.strip_prefix(prefix) {
            for (suffix, mag) in [("k", "tusen"), ("K", "tusen"), ("M", "millioner"), ("m", "millioner")] {
                if let Some(n) = x.strip_suffix(suffix) {
                    if let Some(num) = number::cardinal(n, false) {
                        v.push(format!("{num} {mag} {currency}"));
                    }
                    if mag == "millioner" {
                        for (i, c) in n.char_indices().filter(|(_, c)| *c == '.' || *c == ',') {
                            if let Some(a) = number::cardinal(&n[..i], false) {
                                for neuter in [false, true] {
                                    if let Some(b) = number::cardinal(&n[i + c.len_utf8()..], neuter) {
                                        v.push(format!("{a} komma {b} {mag} {currency}"));
                                    }
                                }
                            }
                        }
                    }
                }
            }
        }
    }
    v
}
fn single(s: &str) -> Vec<String> {
    let (sign, s) =
        if let Some(x) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) { ("minus ", x) } else { ("", s) };
    let mut v: Vec<String> = base(s).into_iter().map(|x| format!("{sign}{x}")).collect();
    for (suffix, word) in [("/h", "per time"), ("/t", "per time"), ("/kg", "per kilogram"), ("/kilo", "per kilo")] {
        if let Some(x) = s.strip_suffix(suffix) {
            for y in base(x) {
                v.push(format!("{sign}{y} {word}"));
            }
        }
    }
    v
}
/// Count uncomplicated integer currency paths without constructing spoken
/// amounts. Compound, decimal, and rate forms use the general grammar.
pub fn simple_cardinal(input: &str) -> Option<usize> {
    let s = input.strip_prefix('-').or_else(|| input.strip_prefix('−')).unwrap_or(input);
    let first_digit = s.find(|c: char| c.is_ascii_digit())?;
    let (prefix, rest) = s.split_at(first_digit);
    let numeric_len = rest.bytes().take_while(u8::is_ascii_digit).count();
    let n = &rest[..numeric_len];
    let suffix = &rest[numeric_len..];
    let value = number::parse_cardinal(n)?;
    let (key, prefix_form) = if prefix.is_empty() {
        (suffix.strip_prefix(' ').unwrap_or(suffix), false)
    } else if suffix.is_empty() {
        (prefix.strip_suffix(' ').unwrap_or(prefix), true)
    } else {
        return None;
    };
    // A slash, dash, or decimal separator can start a rate, range, cash
    // amount, or magnitude branch; keep those on the full path.
    if key.contains(['/', '-', '–', '—', ',', ' ']) || (key.contains('.') && !matches!(key, "kr." | "Rs.")) {
        return None;
    }
    let special =
        matches!(key, "kr" | "kr." | "NOK" | "$" | "USD" | "US$" | "€" | "EUR" | "kroner" | "dollar" | "euro");
    if special && value == 0 {
        return Some(0);
    }
    let count = if prefix_form {
        usize::from(matches!(
            key,
            "kr" | "kr."
                | "NOK"
                | "$"
                | "USD"
                | "US$"
                | "€"
                | "EUR"
                | "£"
                | "GBP"
                | "¥"
                | "JPY"
                | "CA$"
                | "CAD"
                | "A$"
                | "AUD"
                | "₹"
                | "INR"
                | "Rs"
                | "Rs."
                | "₽"
                | "RUB"
                | "R$"
                | "BRL"
                | "MX$"
                | "MXN"
                | "₦"
                | "NGN"
        ))
    } else {
        match key {
            "GBP" => 2,
            "kr" | "kr." | "NOK" | "kroner" | "$" | "USD" | "dollar" | "€" | "EUR" | "euro" | "£" | "¥" | "JPY"
            | "yen" | "CAD" | "AUD" | "SEK" | "DKK" | "CHF" | "INR" | "₽" | "RUB" | "₩" | "KRW" | "₺" | "TRY" | "₪"
            | "ILS" | "₴" | "UAH" | "₫" | "VND" | "৳" | "BDT" | "₱" | "PHP" | "฿" | "THB" | "֏" | "AMD" | "¢" => {
                1
            }
            _ => 0,
        }
    };
    Some(count)
}

/// Recognize the krone/dollar/euro minor-unit and clean-cash branches.
pub fn simple_decimal(input: &str) -> Option<usize> {
    let s = input.strip_prefix('-').or_else(|| input.strip_prefix('−')).unwrap_or(input);
    let first = s.find(|c: char| c.is_ascii_digit())?;
    let (prefix, rest) = s.split_at(first);
    let numeric_len = rest.bytes().take_while(|b| b.is_ascii_digit() || matches!(b, b',' | b'.' | b'-')).count();
    let numeric = &rest[..numeric_len];
    let suffix = &rest[numeric_len..];
    let key = if prefix.is_empty() {
        suffix.strip_prefix(' ').unwrap_or(suffix)
    } else if suffix.is_empty() {
        prefix.strip_suffix(' ').unwrap_or(prefix)
    } else {
        return None;
    };
    let prefix_form = !prefix.is_empty();
    let known = if prefix_form {
        matches!(key, "kr" | "kr." | "NOK" | "$" | "US$" | "USD" | "€" | "EUR")
    } else {
        matches!(key, "" | "kr" | "kr." | "NOK" | "kroner" | "$" | "USD" | "dollar" | "€" | "EUR" | "euro")
    };
    if !known {
        return None;
    }
    if let Some(integer) =
        numeric.strip_suffix(",-").or_else(|| numeric.strip_suffix(",00")).or_else(|| numeric.strip_suffix(".00"))
    {
        let n = number::parse_cardinal(integer)?;
        return Some(usize::from(n != 0 && (key != "" || !prefix_form)));
    }
    if key.is_empty() {
        return None;
    }
    let (integer, minor) = numeric.split_once(|c| c == ',' || c == '.')?;
    if minor.len() != 2 || !minor.bytes().all(|b| b.is_ascii_digit()) || minor == "00" {
        return None;
    }
    number::parse_cardinal(integer)?;
    Some(1)
}

pub fn verbalize_simple(input: &str) -> Option<String> {
    let (sign, s) = if let Some(s) = input.strip_prefix('-').or_else(|| input.strip_prefix('−')) {
        ("minus ", s)
    } else {
        ("", input)
    };
    let first = s.find(|c: char| c.is_ascii_digit())?;
    let (prefix, rest) = s.split_at(first);
    let numeric_len = rest.bytes().take_while(|b| b.is_ascii_digit() || matches!(b, b',' | b'.' | b'-')).count();
    let numeric = &rest[..numeric_len];
    let suffix = &rest[numeric_len..];
    let key = if prefix.is_empty() {
        suffix.strip_prefix(' ').unwrap_or(suffix)
    } else {
        prefix.strip_suffix(' ').unwrap_or(prefix)
    };
    let spoken = match key {
        "" | "kr" | "kr." | "NOK" | "kroner" => amount(numeric, "kroner", "øre")?,
        "$" | "US$" | "USD" | "dollar" => amount(numeric, "dollar", "cent")?,
        "€" | "EUR" | "euro" => amount(numeric, "euro", "cent")?,
        _ => {
            let unit = match key {
                "£" | "GBP" => "pund",
                "¥" | "JPY" | "yen" => "yen",
                "CA$" | "CAD" => "kanadiske dollar",
                "A$" | "AUD" => "australske dollar",
                "SEK" => "svenske kroner",
                "DKK" => "danske kroner",
                "CHF" => "sveitsiske franc",
                "₹" | "INR" | "Rs" | "Rs." => "indiske rupier",
                "₽" | "RUB" => "russiske rubler",
                "R$" | "BRL" => "brasilianske real",
                "MX$" | "MXN" => "meksikanske pesos",
                "₩" | "KRW" => "sørkoreanske won",
                "₺" | "TRY" => "tyrkiske lire",
                "₪" | "ILS" => "israelske sjekler",
                "₴" | "UAH" => "ukrainske hryvnia",
                "₫" | "VND" => "vietnamesiske dong",
                "৳" | "BDT" => "bangladeshiske taka",
                "₦" | "NGN" => "nigerianske naira",
                "₱" | "PHP" => "filippinske pesos",
                "฿" | "THB" => "thailandske baht",
                "֏" | "AMD" => "armenske dram",
                "¢" => "cent",
                _ => return None,
            };
            format!("{} {unit}", number::cardinal(numeric, false)?)
        }
    };
    if sign.is_empty() { Some(spoken) } else { Some(format!("{sign}{spoken}")) }
}

fn same_currency(s: &str) -> Vec<String> {
    if !possible_base(s) {
        return Vec::new();
    }
    let mut out = Vec::new();
    for (prefixes, suffixes, unit, minor) in [
        (&["kr.", "kr", "NOK"][..], &["kr.", "kr", "NOK", "kroner"][..], "kroner", "øre"),
        (&["US$", "USD", "$"][..], &["$", "USD", "dollar"][..], "dollar", "cent"),
        (&["€", "EUR"][..], &["€", "EUR", "euro"][..], "euro", "cent"),
    ] {
        for p in prefixes {
            if let Some(rest) = s.strip_prefix(p) {
                if let Some(x) = amount(trim_one_start(rest), unit, minor) {
                    out.push(x)
                }
            }
        }
        for p in suffixes {
            if let Some(rest) = s.strip_suffix(p) {
                if let Some(x) = amount(trim_one_end(rest), unit, minor) {
                    out.push(x)
                }
            }
        }
    }
    if s.ends_with(",-") || s.ends_with(",00") || s.ends_with(".00") {
        if let Some(x) = amount(s, "kroner", "øre") {
            out.push(x)
        }
    }
    out
}
pub fn parse(s: &str) -> Vec<String> {
    let mut v = single(s);
    for (i, c) in s.char_indices().filter(|(_, c)| matches!(c, '-' | '–' | '—')) {
        let a = trim_one_end(&s[..i]);
        let b = trim_one_start(&s[i + c.len_utf8()..]);
        if !possible_base(b) {
            continue;
        }
        let left = base(a);
        if !left.is_empty() {
            let right = base(b);
            for x in &left {
                for y in &right {
                    v.push(format!("{x} til {y}"));
                }
            }
        }
        if number::parse_cardinal(a).is_some() {
            let right = same_currency(b);
            if !right.is_empty() {
                if let Some(x) = number::cardinal(a, false) {
                    for y in right {
                        v.push(format!("{x} til {y}"));
                    }
                }
            }
        }
    }
    v
}
