use super::*;

pub(in crate::lang::el) fn currency_number(s: &str) -> Option<String> {
    number::cardinal(s).or_else(|| grouped(s))
}
pub(in crate::lang::el) fn currency_amount(
    s: &str,
    kind: &str,
    singular: &str,
    plural: &str,
    feminine: bool,
) -> Option<String> {
    if let Some((a, b)) = s.rsplit_once([',', '.']) {
        if (kind == "eur" || kind == "usd") && (1..=2).contains(&b.len()) && b.bytes().all(|c| c.is_ascii_digit()) {
            let cents: usize = b.parse().ok()?;
            if b.len() == 2 && b.starts_with('0') && cents > 1 {
                return None;
            }
            let major = if a == "00" && cents > 0 { String::new() } else { currency_number(a)? };
            let unit = if kind == "eur" {
                "ευρώ"
            } else if a == "1" {
                "δολάριο"
            } else {
                "δολάρια"
            };
            if cents == 0 {
                return Some(spoken!("{major} {unit}"));
            }
            let minor = number::cardinal(if b == "01" { "1" } else { b })?;
            let cent = if kind == "eur" { if cents == 1 { "λεπτό" } else { "λεπτά" } } else { "σεντ" };
            if a == "0" || a == "00" {
                return Some(spoken!("{minor} {cent}"));
            }
            return Some(spoken!("{major} {unit} και {minor} {cent}"));
        }
    }
    for (suffix, word) in [
        ("k", "χιλιάδες"),
        ("K", "χιλιάδες"),
        ("M", "εκατομμύρια"),
        ("m", "εκατομμύρια"),
        ("B", "δισεκατομμύρια"),
        ("b", "δισεκατομμύρια"),
    ] {
        if (kind == "eur" || kind == "usd") && s.ends_with(suffix) {
            let x = s.strip_suffix(suffix)?;
            return Some(spoken!("{} {word} {plural}", decimal_core(x).or_else(|| currency_number(x))?));
        }
    }
    let v = currency_number(s)?;
    let v = if feminine { number::agree(&v, false) } else { v };
    Some(spoken!("{v} {}", if s == "1" { singular } else { plural }))
}
pub(in crate::lang::el) fn money_single(s: &str) -> Option<String> {
    for (key, sing, plural, kind, fem) in [
        ("€", "ευρώ", "ευρώ", "eur", false),
        ("EUR", "ευρώ", "ευρώ", "eur", false),
        ("eur", "ευρώ", "ευρώ", "eur", false),
        ("US$", "δολάριο", "δολάρια", "usd", false),
        ("$", "δολάριο", "δολάρια", "usd", false),
        ("USD", "δολάριο", "δολάρια", "usd", false),
        ("usd", "δολάριο", "δολάρια", "usd", false),
    ] {
        if let Some(x) = s.strip_prefix(key) {
            let x = x.strip_prefix(' ').unwrap_or(x);
            if let Some(v) = currency_amount(x, kind, sing, plural, fem) {
                return Some(v);
            }
        }
        if let Some(x) = s.strip_suffix(key) {
            let x = x.strip_suffix(' ').unwrap_or(x);
            if let Some(v) = currency_amount(x, kind, sing, plural, fem) {
                return Some(v);
            }
        }
    }
    for &(key, sing, plural) in tables::CURRENCIES {
        let fem = ["£", "GBP", "₹", "INR", "Rs.", "Rs", "₺", "TRY", "₴", "UAH"].contains(&key);
        if let Some(x) = s.strip_prefix(key) {
            let x = x.strip_prefix(' ').unwrap_or(x);
            if let Some(v) = currency_amount(x, "other", sing, plural, fem) {
                return Some(v);
            }
        }
        if let Some(x) = s.strip_suffix(key) {
            let x = x.strip_suffix(' ').unwrap_or(x);
            if let Some(v) = currency_amount(x, "other", sing, plural, fem) {
                return Some(v);
            }
        }
    }
    if let Some(x) = s.strip_suffix('¢') {
        let x = x.strip_suffix(' ').unwrap_or(x);
        if x.len() <= 2 && x != "0" && x != "00" {
            let val = if x == "01" { spoken!("ένα") } else { number::cardinal(x)? };
            return Some(spoken!("{val} σεντ"));
        }
    }
    None
}
pub(in crate::lang::el) fn money_alternate(s: &str) -> Option<String> {
    if let Some((a, b)) = split_range(s) {
        if let (Some(a), Some(b)) = (money_single(a), money_single(b)) {
            return Some(spoken!("{a} έως {b}"));
        }
    }
    let (sign, s) =
        if let Some(s) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) { ("μείον ", s) } else { ("", s) };
    for (sym, unit, minor) in [
        ("€", "ευρώ", "λεπτά"),
        ("EUR", "ευρώ", "λεπτά"),
        ("eur", "ευρώ", "λεπτά"),
        ("US$", "δολάρια", "σεντ"),
        ("$", "δολάρια", "σεντ"),
        ("USD", "δολάρια", "σεντ"),
        ("usd", "δολάρια", "σεντ"),
    ] {
        let amount = s
            .strip_prefix(sym)
            .map(|v| v.strip_prefix(' ').unwrap_or(v))
            .or_else(|| s.strip_suffix(sym).map(|v| v.strip_suffix(' ').unwrap_or(v)));
        if let Some(amount) = amount {
            if let Some((a, b)) = amount.split_once([',', '.']) {
                if a == "0" && (1..=2).contains(&b.len()) {
                    let cents: usize = b.parse().ok()?;
                    if cents == 0 || (b.len() == 2 && b.starts_with('0') && cents > 1) {
                        return None;
                    }
                    let cent = if minor == "λεπτά" && cents == 1 { "λεπτό" } else { minor };
                    return Some(spoken!(
                        "{sign}μηδέν {unit} και {} {cent}",
                        number::cardinal(if b == "01" { "1" } else { b })?
                    ));
                }
            }
        }
    }
    None
}
pub(in crate::lang::el) fn money(s: &str) -> Option<String> {
    for (p, w) in [("-", "μείον "), ("−", "μείον ")] {
        if let Some(x) = s.strip_prefix(p) {
            if let Some(v) = money_single(x) {
                return Some(spoken!("{w}{v}"));
            }
        }
    }
    for (sym, unit) in [
        ("€", "ευρώ"),
        ("EUR", "ευρώ"),
        ("eur", "ευρώ"),
        ("$", "δολάρια"),
        ("USD", "δολάρια"),
        ("usd", "δολάρια"),
        ("US$", "δολάρια"),
    ] {
        if let Some(x) = s.strip_prefix(sym) {
            let x = x.strip_prefix(' ').unwrap_or(x);
            if let Some(x) = x.strip_prefix('-').or_else(|| x.strip_prefix('−')) {
                if let Some(n) = currency_number(x) {
                    let unit = if unit == "δολάρια" && x == "1" { "δολάριο" } else { unit };
                    return Some(spoken!("μείον {n} {unit}"));
                }
            }
        }
    }
    for (p, w) in [
        ("/h", "την ώρα"),
        ("/hr", "την ώρα"),
        ("/ώρα", "την ώρα"),
        ("/kg", "ανά κιλό"),
        ("/κιλό", "ανά κιλό"),
        ("/m", "ανά μέτρο"),
        ("/τεμ", "το τεμάχιο"),
        ("/τεμάχιο", "το τεμάχιο"),
    ] {
        if let Some(x) = s.strip_suffix(p) {
            if let Some(v) = money_single(x) {
                return Some(spoken!("{v} {w}"));
            }
        }
    }
    if let Some((a, b)) = split_range(s) {
        for (sym, unit) in [
            ("€", "ευρώ"),
            ("EUR", "ευρώ"),
            ("eur", "ευρώ"),
            ("$", "δολάρια"),
            ("USD", "δολάρια"),
            ("usd", "δολάρια"),
            ("US$", "δολάρια"),
        ] {
            if let Some(x) = b.strip_suffix(sym) {
                let x = x.strip_suffix(' ').unwrap_or(x);
                if let (Some(a), Some(b)) = (currency_number(a), currency_number(x)) {
                    return Some(spoken!("{a} έως {b} {unit}"));
                }
            }
            if let (Some(x), Some(y)) = (a.strip_prefix(sym), b.strip_prefix(sym)) {
                let x = x.strip_prefix(' ').unwrap_or(x);
                let y = y.strip_prefix(' ').unwrap_or(y);
                if let (Some(a), Some(b)) = (currency_number(x), currency_number(y)) {
                    return Some(spoken!("{a} έως {b} {unit}"));
                }
            }
        }
        if let (Some(a), Some(b)) = (money_single(a), money_single(b)) {
            return Some(spoken!("{a} έως {b}"));
        }
    }
    money_single(s)
}
