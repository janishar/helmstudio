use super::numbers::{cardinal, decimal, split_range, unsigned};
use std::sync::LazyLock;
struct Currency {
    symbols: &'static [&'static str],
    one: &'static str,
    few: &'static str,
    many: &'static str,
    gender: char,
    minor: bool,
}
const CURRENCIES: &[Currency] = &[
    Currency { symbols: &["Kč", "CZK", "czk"], one: "koruna", few: "koruny", many: "korun", gender: 'f', minor: true },
    Currency { symbols: &["€", "EUR", "eur"], one: "euro", few: "eura", many: "eur", gender: 'n', minor: true },
    Currency { symbols: &["$", "USD", "usd"], one: "dolar", few: "dolary", many: "dolarů", gender: 'm', minor: true },
    Currency { symbols: &["£", "GBP", "gbp"], one: "libra", few: "libry", many: "liber", gender: 'f', minor: false },
    Currency {
        symbols: &["CHF"],
        one: "švýcarský frank",
        few: "švýcarské franky",
        many: "švýcarských franků",
        gender: 'm',
        minor: false,
    },
    Currency {
        symbols: &["CAD", "CA$"],
        one: "kanadský dolar",
        few: "kanadské dolary",
        many: "kanadských dolarů",
        gender: 'm',
        minor: false,
    },
    Currency {
        symbols: &["INR", "₹", "Rs.", "Rs"],
        one: "indická rupie",
        few: "indické rupie",
        many: "indických rupií",
        gender: 'f',
        minor: false,
    },
    Currency {
        symbols: &["AUD", "A$"],
        one: "australský dolar",
        few: "australské dolary",
        many: "australských dolarů",
        gender: 'm',
        minor: false,
    },
    Currency {
        symbols: &["BRL", "R$"],
        one: "brazilský real",
        few: "brazilské realy",
        many: "brazilských realů",
        gender: 'm',
        minor: false,
    },
    Currency {
        symbols: &["MXN", "MX$"],
        one: "mexické peso",
        few: "mexická pesa",
        many: "mexických pes",
        gender: 'n',
        minor: false,
    },
    Currency {
        symbols: &["RUB", "₽", "руб."], one: "rubl", few: "rubly", many: "rublů", gender: 'm', minor: false
    },
    Currency { symbols: &["KRW", "₩"], one: "won", few: "wony", many: "wonů", gender: 'm', minor: false },
    Currency {
        symbols: &["TRY", "₺"],
        one: "turecká lira",
        few: "turecké liry",
        many: "tureckých lir",
        gender: 'f',
        minor: false,
    },
    Currency { symbols: &["ILS", "₪"], one: "šekel", few: "šekely", many: "šekelů", gender: 'm', minor: false },
    Currency { symbols: &["UAH", "₴"], one: "hřivna", few: "hřivny", many: "hřiven", gender: 'f', minor: false },
    Currency { symbols: &["VND", "₫"], one: "dong", few: "dongy", many: "dongů", gender: 'm', minor: false },
    Currency {
        symbols: &["BDT", "৳"],
        one: "bangladéšská taka",
        few: "bangladéšské taky",
        many: "bangladéšských tak",
        gender: 'f',
        minor: false,
    },
    Currency {
        symbols: &["NGN", "₦"],
        one: "nigerijská naira",
        few: "nigerijské nairy",
        many: "nigerijských nair",
        gender: 'f',
        minor: false,
    },
    Currency {
        symbols: &["PHP", "₱"],
        one: "filipínské peso",
        few: "filipínská pesa",
        many: "filipínských pes",
        gender: 'n',
        minor: false,
    },
    Currency {
        symbols: &["THB", "฿"],
        one: "thajský baht",
        few: "thajské bahty",
        many: "thajských bahtů",
        gender: 'm',
        minor: false,
    },
    Currency {
        symbols: &["AMD", "֏"],
        one: "arménský dram",
        few: "arménské dramy",
        many: "arménských dramů",
        gender: 'm',
        minor: false,
    },
    Currency { symbols: &["¢"], one: "cent", few: "centy", many: "centů", gender: 'm', minor: false },
];
#[derive(Clone, Copy)]
struct CurrencyEntry {
    order: usize,
    symbol: &'static str,
    currency: &'static Currency,
}
struct CurrencyIndex {
    prefix: [Vec<CurrencyEntry>; 256],
    suffix: [Vec<CurrencyEntry>; 256],
}
static CURRENCY_INDEX: LazyLock<CurrencyIndex> = LazyLock::new(|| {
    let mut index =
        CurrencyIndex { prefix: std::array::from_fn(|_| Vec::new()), suffix: std::array::from_fn(|_| Vec::new()) };
    let mut order = 0;
    for currency in CURRENCIES {
        for &symbol in currency.symbols {
            let entry = CurrencyEntry { order, symbol, currency };
            let bytes = symbol.as_bytes();
            index.prefix[bytes[0] as usize].push(entry);
            index.suffix[bytes[bytes.len() - 1] as usize].push(entry);
            order += 1;
        }
    }
    index
});

/// Merge the prefix and suffix buckets in the original union order.
struct CurrencyCandidates {
    prefix: &'static [CurrencyEntry],
    suffix: &'static [CurrencyEntry],
    a: usize,
    b: usize,
}
impl CurrencyCandidates {
    fn new(s: &str) -> Self {
        let index = &*CURRENCY_INDEX;
        let bytes = s.as_bytes();
        Self {
            prefix: bytes.first().map_or(&[], |b| index.prefix[*b as usize].as_slice()),
            suffix: bytes.last().map_or(&[], |b| index.suffix[*b as usize].as_slice()),
            a: 0,
            b: 0,
        }
    }
}
impl Iterator for CurrencyCandidates {
    type Item = CurrencyEntry;
    fn next(&mut self) -> Option<Self::Item> {
        match (self.prefix.get(self.a), self.suffix.get(self.b)) {
            (Some(a), Some(b)) if a.order == b.order => {
                self.a += 1;
                self.b += 1;
                Some(*a)
            }
            (Some(a), Some(b)) if a.order < b.order => {
                self.a += 1;
                Some(*a)
            }
            (Some(_), Some(b)) => {
                self.b += 1;
                Some(*b)
            }
            (Some(a), None) => {
                self.a += 1;
                Some(*a)
            }
            (None, Some(b)) => {
                self.b += 1;
                Some(*b)
            }
            (None, None) => None,
        }
    }
}
pub fn may_cross_space(first: &str) -> bool {
    let first = first.strip_prefix('-').or_else(|| first.strip_prefix('−')).unwrap_or(first);
    first
        .as_bytes()
        .first()
        .is_some_and(|b| CURRENCY_INDEX.prefix[*b as usize].iter().any(|entry| first.starts_with(entry.symbol)))
}

fn number_before(s: &str, pos: usize) -> bool {
    let before = s[..pos].as_bytes();
    before.last().is_some_and(u8::is_ascii_digit)
        || (before.last() == Some(&b' ') && before.get(before.len().saturating_sub(2)).is_some_and(u8::is_ascii_digit))
}
pub fn may_match(s: &str) -> bool {
    if s.match_indices('h').any(|(pos, _)| number_before(s, pos)) {
        return true;
    }
    let index = &*CURRENCY_INDEX;
    for (pos, _) in s.char_indices() {
        let suffix = &s[pos..];
        let first = suffix.as_bytes()[0] as usize;
        for entry in &index.prefix[first] {
            let Some(after) = suffix.strip_prefix(entry.symbol) else { continue };
            let before_digit = number_before(s, pos);
            let after = after.strip_prefix(' ').unwrap_or(after);
            let after = after.strip_prefix('-').or_else(|| after.strip_prefix('−')).unwrap_or(after);
            if before_digit || after.as_bytes().first().is_some_and(u8::is_ascii_digit) {
                return true;
            }
        }
    }
    false
}
/// Recognize the ungrouped integer major-unit branches without constructing
#[derive(Clone, Copy)]
pub struct SimpleReading {
    number: u32,
    currency: &'static Currency,
}
pub fn simple_reading(s: &str) -> Option<SimpleReading> {
    for entry in CurrencyCandidates::new(s) {
        let number = if let Some(n) = s.strip_prefix(entry.symbol) {
            n.strip_prefix(' ').unwrap_or(n)
        } else if let Some(n) = s.strip_suffix(entry.symbol) {
            n.strip_suffix(' ').unwrap_or(n)
        } else {
            continue;
        };
        if number.is_empty()
            || number.len() > 6
            || !number.bytes().all(|b| b.is_ascii_digit())
            || (number.len() > 1 && number.starts_with('0'))
        {
            continue;
        }
        return Some(SimpleReading { number: number.parse().ok()?, currency: entry.currency });
    }
    None
}

pub fn simple_render(reading: SimpleReading) -> String {
    let c = reading.currency;
    let (mut number, unit) = match reading.number {
        1 => (
            (match c.gender {
                'f' => "jedna",
                'n' => "jedno",
                _ => "jeden",
            })
            .to_owned(),
            c.one,
        ),
        2 => ((if c.gender == 'm' { "dva" } else { "dvě" }).to_owned(), c.few),
        3 => ("tři".to_owned(), c.few),
        4 => ("čtyři".to_owned(), c.few),
        n => (super::graph_utils::positive_value(n as u64).expect("recognized Czech amount"), c.many),
    };
    number.push(' ');
    number.push_str(unit);
    number
}
fn amount(s: &str, c: &Currency) -> Option<String> {
    let (n, unit) = match s {
        "1" => (
            (match c.gender {
                'f' => "jedna",
                'n' => "jedno",
                _ => "jeden",
            })
            .to_owned(),
            c.one,
        ),
        "2" => ((if c.gender == 'm' { "dva" } else { "dvě" }).to_owned(), c.few),
        "3" => ("tři".to_owned(), c.few),
        "4" => ("čtyři".to_owned(), c.few),
        _ => (unsigned(s)?, c.many),
    };
    Some(format!("{n} {unit}"))
}
fn minor(s: &str, hal: bool) -> Option<String> {
    let n = if s.len() == 2 && s.starts_with('0') { &s[1..] } else { s };
    let unit = match n {
        "1" => {
            if hal {
                "haléř"
            } else {
                "cent"
            }
        }
        "2" | "3" | "4" => {
            if hal {
                "haléře"
            } else {
                "centy"
            }
        }
        _ => {
            if hal {
                "haléřů"
            } else {
                "centů"
            }
        }
    };
    if n == "0" || n.len() > 3 || n.starts_with('0') {
        return None;
    }
    let number = match n {
        "1" => "jeden".to_owned(),
        "2" => "dva".to_owned(),
        _ => unsigned(n)?,
    };
    Some(format!("{number} {unit}"))
}
fn amount_decimal(s: &str, c: &Currency) -> Option<String> {
    let (a, b) = s.split_once(|x| x == ',' || x == '.')?;
    if b == "00" {
        return amount(a, c);
    }
    let minor = minor(b, c.symbols[0] == "Kč")?;
    if a == "0" { Some(minor) } else { Some(format!("{} {minor}", amount(a, c)?)) }
}
fn base(s: &str) -> Option<String> {
    if s.starts_with('$') || s.starts_with('€') {
        for (prefix, suffix, out) in [
            ("$", "k", "tisíc dolarů"),
            ("$", "K", "tisíc dolarů"),
            ("€", "k", "tisíc eur"),
            ("€", "K", "tisíc eur"),
            ("$", "M", "milionu dolarů"),
            ("$", "m", "milionu dolarů"),
        ] {
            if let Some(n) = s.strip_prefix(prefix).and_then(|s| s.strip_suffix(suffix)) {
                if let Some(n) =
                    cardinal(n).or_else(|| if suffix == "M" || suffix == "m" { decimal(n).map(|x| x.0) } else { None })
                {
                    return Some(format!("{n} {out}"));
                }
            }
        }
    }
    for (suffix, out, pre, post) in
        [("/h", "za hodinu", Some(2usize), true), ("/kg", "za kilogram", Some(1usize), true)]
    {
        if let Some(n) = s.strip_suffix(suffix) {
            let n = n.strip_suffix(' ').unwrap_or(n);
            if let Some(c) = pre {
                for &sym in CURRENCIES[c].symbols {
                    if let Some(v) = n.strip_prefix(sym) {
                        let v = v.strip_prefix(' ').unwrap_or(v);
                        if let Some(v) = amount(v, &CURRENCIES[c]) {
                            return Some(format!("{v} {out}"));
                        }
                    }
                }
            }
            if post {
                for &sym in CURRENCIES[0].symbols {
                    if let Some(v) = n.strip_suffix(sym) {
                        let v = v.strip_suffix(' ').unwrap_or(v);
                        if let Some(v) = amount(v, &CURRENCIES[0]) {
                            return Some(format!("{v} {out}"));
                        }
                    }
                }
            }
        }
    }
    if s.ends_with('h') || s.ends_with('l') || s.ends_with('.') {
        for sym in ["hal.", "hal", "h.", "h"] {
            if let Some(n) = s.strip_suffix(sym) {
                let n = n.strip_suffix(' ').unwrap_or(n);
                if let Some(v) = minor(n, true) {
                    return Some(v);
                }
            }
        }
    }
    for entry in CurrencyCandidates::new(s) {
        let c = entry.currency;
        let (n, pre) = if let Some(n) = s.strip_prefix(entry.symbol) {
            (n.strip_prefix(' ').unwrap_or(n), true)
        } else if let Some(n) = s.strip_suffix(entry.symbol) {
            (n.strip_suffix(' ').unwrap_or(n), false)
        } else {
            continue;
        };
        if let Some(v) = amount(n, c) {
            return Some(v);
        }
        if c.minor && (!pre || c.symbols[0] != "Kč") {
            if let Some(v) = amount_decimal(n, c) {
                return Some(v);
            }
        }
    }
    // Discrete koruna and haléř amounts.
    if !(s.ends_with('h') || s.ends_with("h.") || s.ends_with("hal") || s.ends_with("hal.")) {
        return None;
    }
    for &sym in CURRENCIES[0].symbols {
        if let Some((a, b)) = s.split_once(sym) {
            let a = a.strip_suffix(' ').unwrap_or(a);
            let b = b.strip_prefix(' ').unwrap_or(b);
            for h in ["hal.", "hal", "h.", "h"] {
                if let Some(b) = b.strip_suffix(h) {
                    let b = b.strip_suffix(' ').unwrap_or(b);
                    if let (Some(a), Some(b)) = (amount(a, &CURRENCIES[0]), minor(b, true)) {
                        return Some(format!("{a} {b}"));
                    }
                }
            }
        }
    }
    None
}
pub fn money(s: &str) -> Option<String> {
    if let Some(v) = base(s) {
        return Some(v);
    }
    if let Some(s) = s.strip_prefix('(').and_then(|s| s.strip_suffix(')')) {
        return base(s);
    }
    if let Some(s) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) {
        if let Some(v) = base(s) {
            return Some(format!("mínus {v}"));
        }
    }
    for (a, b) in split_range(s) {
        if let (Some(a), Some(b)) = (base(a), base(b)) {
            return Some(format!("{a} až {b}"));
        }
        if let Some(a) = cardinal(a) {
            for c in &CURRENCIES[..3] {
                for &sym in c.symbols {
                    if let Some(n) = b.strip_suffix(sym) {
                        let n = n.strip_suffix(' ').unwrap_or(n);
                        if let Some(b) = amount(n, c) {
                            return Some(format!("{a} až {b}"));
                        }
                    }
                }
            }
        }
    }
    None
}

/// Only the million shorthand composes the weighted dot-decimal graph.
pub fn nested_weight(s: &str) -> f64 {
    let mut weight = 0.0;
    for (i, _) in s.match_indices('$') {
        let rest = &s[i + 1..];
        for (j, c) in rest.char_indices() {
            if c == 'M' || c == 'm' {
                let n = &rest[..j];
                if cardinal(n).is_none() {
                    if let Some((_, w)) = decimal(n) {
                        weight += w
                    }
                }
                break;
            }
            if !(c.is_ascii_digit() || matches!(c, ',' | '.' | '-' | '−' | ' ' | '\u{a0}' | '\u{202f}')) {
                break;
            }
        }
    }
    weight
}

pub fn paths(s: &str) -> Vec<(String, f64)> {
    let Some(spoken) = money(s) else { return Vec::new() };
    let mut out = vec![(spoken, nested_weight(s))];
    // Minor agreement graphs overlap with n1_999 on unpadded 1..4.
    let fraction = s.find(|c| c == ',' || c == '.').and_then(|i| {
        let x = &s[i + 1..];
        let end = x.find(|c: char| !c.is_ascii_digit()).unwrap_or(x.len());
        if end == 0 { None } else { Some(&x[..end]) }
    });
    let discrete = ["hal.", "hal", "h.", "h"].iter().find_map(|suffix| {
        let x = s.strip_suffix(*suffix)?.strip_suffix(' ').unwrap_or(s.strip_suffix(*suffix)?);
        let start = x.char_indices().rev().find(|(_, c)| !c.is_ascii_digit()).map_or(0, |(i, c)| i + c.len_utf8());
        if start == x.len() { None } else { Some(&x[start..]) }
    });
    let hal = s.contains("Kč") || s.contains("CZK") || s.contains("czk") || discrete.is_some();
    if let Some(n) = discrete.or(fraction) {
        let replacement = match (n, hal) {
            ("1", true) => Some(("jeden haléř", "jedna haléřů")),
            ("2", true) => Some(("dva haléře", "dva haléřů")),
            ("3", true) => Some(("tři haléře", "tři haléřů")),
            ("4", true) => Some(("čtyři haléře", "čtyři haléřů")),
            ("1", false) => Some(("jeden cent", "jedna centů")),
            ("2", false) => Some(("dva centy", "dva centů")),
            ("3", false) => Some(("tři centy", "tři centů")),
            ("4", false) => Some(("čtyři centy", "čtyři centů")),
            _ => None,
        };
        if let Some((old, new)) = replacement {
            let existing = out.clone();
            for (v, w) in existing {
                if v.contains(old) {
                    let p = (v.replacen(old, new, 1), w);
                    if !out.contains(&p) {
                        out.push(p)
                    }
                }
            }
        }
    }
    // The zero-major branch and the cents-only branch are both present.
    if let Some(i) = s.find(|c| c == ',' || c == '.') {
        let before = s[..i].trim_end();
        let before = before.trim_start_matches(|c: char| !c.is_ascii_digit());
        if before == "0" && fraction.is_some_and(|x| x != "00") && discrete.is_none() {
            let major = if hal {
                "nula korun"
            } else if s.contains('€') || s.contains("EUR") || s.contains("eur") {
                "nula eur"
            } else {
                "nula dolarů"
            };
            let existing = out.clone();
            for (v, w) in existing {
                if !v.contains("haléř") && !v.contains("cent") {
                    continue;
                }
                let (sign, rest) = v.strip_prefix("mínus ").map_or(("", v.as_str()), |x| ("mínus ", x));
                let p = (format!("{sign}{major} {rest}"), w);
                if !out.contains(&p) {
                    out.push(p)
                }
            }
        }
    }
    for (i, _) in s.match_indices('$') {
        let rest = &s[i + 1..];
        for (j, c) in rest.char_indices() {
            if c == 'M' || c == 'm' {
                let n = &rest[..j];
                if let (Some(card), Some((dec, w))) = (cardinal(n), decimal(n)) {
                    let mut idx = 0;
                    while idx < out.len() {
                        let (text, weight) = out[idx].clone();
                        for (pos, _) in text.match_indices(&card) {
                            let before = text[..pos].chars().next_back();
                            let after = text[pos + card.len()..].chars().next();
                            if before.is_some_and(|c| c.is_alphabetic()) || after.is_some_and(|c| c.is_alphabetic()) {
                                continue;
                            }
                            let mut value = String::new();
                            value.push_str(&text[..pos]);
                            value.push_str(&dec);
                            value.push_str(&text[pos + card.len()..]);
                            let path = (value, weight + w);
                            if !out.contains(&path) {
                                out.push(path)
                            }
                        }
                        idx += 1;
                    }
                }
                break;
            }
            if !(c.is_ascii_digit() || matches!(c, ',' | '.' | '-' | '−' | ' ' | '\u{a0}' | '\u{202f}')) {
                break;
            }
        }
    }
    // Both sides of a currency range are independent money graphs.
    for (a, b) in split_range(s) {
        if base(a).is_none() || base(b).is_none() {
            continue;
        }
        for (left, lw) in paths(a) {
            for (right, rw) in paths(b) {
                let p = (format!("{left} až {right}"), lw + rw);
                if !out.contains(&p) {
                    out.push(p)
                }
            }
        }
    }
    out
}
