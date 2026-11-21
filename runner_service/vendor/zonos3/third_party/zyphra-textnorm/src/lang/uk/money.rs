// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
use super::cardinal::{Gender, number};
struct Currency {
    names: &'static [&'static str],
    forms: [&'static str; 3],
    feminine: bool,
    weight: i32,
}
const CURRENCIES: &[Currency] = &[
    Currency {
        names: &["₴", "грн.", "грн", "UAH"],
        forms: ["гривня", "гривні", "гривень"],
        feminine: true,
        weight: 10,
    },
    Currency {
        names: &["коп.", "коп"], forms: ["копійка", "копійки", "копійок"], feminine: true, weight: 10
    },
    Currency {
        names: &["$", "USD"], forms: ["долар", "долари", "доларів"], feminine: false, weight: 10
    },
    Currency { names: &["€", "EUR"], forms: ["євро", "євро", "євро"], feminine: false, weight: 10 },
    Currency {
        names: &["₽", "руб.", "руб", "RUB"], forms: ["рубль", "рублі", "рублів"], feminine: false, weight: 10
    },
    Currency {
        names: &["CHF"],
        forms: ["швейцарський франк", "швейцарські франки", "швейцарських франків"],
        feminine: false,
        weight: 12,
    },
    Currency {
        names: &["CAD", "CA$"],
        forms: ["канадський долар", "канадські долари", "канадських доларів"],
        feminine: false,
        weight: 12,
    },
    Currency {
        names: &["A$"],
        forms: ["австралійський долар", "австралійські долари", "австралійських доларів"],
        feminine: false,
        weight: 12,
    },
    Currency {
        names: &["R$"],
        forms: ["бразильський реал", "бразильські реали", "бразильських реалів"],
        feminine: false,
        weight: 12,
    },
    Currency {
        names: &["MX$", "MXN"],
        forms: ["мексиканський песо", "мексиканські песо", "мексиканських песо"],
        feminine: false,
        weight: 12,
    },
    Currency {
        names: &["INR", "₹", "Rs.", "Rs"], forms: ["рупія", "рупії", "рупій"], feminine: true, weight: 12
    },
    Currency { names: &["₩", "KRW"], forms: ["вона", "вони", "вон"], feminine: true, weight: 12 },
    Currency {
        names: &["₺", "TRY"],
        forms: ["турецька ліра", "турецькі ліри", "турецьких лір"],
        feminine: true,
        weight: 12,
    },
    Currency {
        names: &["₪", "ILS"],
        forms: ["новий ізраїльський шекель", "нові ізраїльські шекелі", "нових ізраїльських шекелів"],
        feminine: false,
        weight: 12,
    },
    Currency {
        names: &["₫", "VND"],
        forms: ["в'єтнамський донг", "в'єтнамські донги", "в'єтнамських донгів"],
        feminine: false,
        weight: 12,
    },
    Currency {
        names: &["৳", "BDT"],
        forms: ["бангладеська така", "бангладеські таки", "бангладеських так"],
        feminine: true,
        weight: 12,
    },
    Currency {
        names: &["₦", "NGN"],
        forms: ["нігерійська найра", "нігерійські найри", "нігерійських найр"],
        feminine: true,
        weight: 12,
    },
    Currency {
        names: &["₱", "PHP"],
        forms: ["філіппінський песо", "філіппінські песо", "філіппінських песо"],
        feminine: false,
        weight: 12,
    },
    Currency {
        names: &["฿", "THB"],
        forms: ["тайський бат", "тайські бати", "тайських батів"],
        feminine: false,
        weight: 12,
    },
    Currency {
        names: &["֏", "AMD"],
        forms: ["вірменський драм", "вірменські драми", "вірменських драмів"],
        feminine: false,
        weight: 12,
    },
];
fn find_currency(name: &str) -> Option<&'static Currency> {
    static INDEX: std::sync::LazyLock<std::collections::HashMap<&'static str, usize>> =
        std::sync::LazyLock::new(|| {
            let mut index = std::collections::HashMap::new();
            for (i, currency) in CURRENCIES.iter().enumerate() {
                for &name in currency.names {
                    index.entry(name).or_insert(i);
                }
            }
            index
        });
    INDEX.get(name).map(|&i| &CURRENCIES[i])
}
/// Source weight of a separated, positive integer currency amount.
pub(super) fn simple_weight(s: &str) -> Option<i32> {
    let (n, symbol) = s.split_once(' ')?;
    if !super::cardinal::valid_number(n) {
        return None;
    }
    find_currency(symbol).map(|c| c.weight)
}
pub(super) fn render_simple(s: &str) -> Option<String> {
    let (n, symbol) = s.split_once(' ')?;
    let currency = find_currency(symbol)?;
    amount(n, currency)
}
pub(super) fn currency_names() -> impl Iterator<Item = &'static str> {
    CURRENCIES.iter().flat_map(|c| c.names.iter().copied())
}
fn amount(s: &str, c: &Currency) -> Option<String> {
    let mut compact = String::new();
    let digits = if s.bytes().all(|b| b.is_ascii_digit()) {
        s
    } else {
        let mut groups = s.split([' ', '\u{a0}', '.']);
        let first = groups.next()?;
        if first.is_empty() || first.len() > 3 || !first.bytes().all(|b| b.is_ascii_digit()) {
            return None;
        }
        compact.push_str(first);
        let mut count = 0;
        for group in groups {
            if group.len() != 3 || !group.bytes().all(|b| b.is_ascii_digit()) {
                return None;
            }
            compact.push_str(group);
            count += 1;
        }
        if count == 0 {
            return None;
        }
        compact.as_str()
    };
    let b = digits.as_bytes();
    let last = *b.last()?;
    let form = if b.len() > 1 && b[b.len() - 2] == b'1' {
        2
    } else {
        match last {
            b'1' => 0,
            b'2'..=b'4' => 1,
            _ => 2,
        }
    };
    let mut spoken = number(digits, if c.feminine { Gender::Feminine } else { Gender::Masculine }, false)?;
    spoken.push(' ');
    spoken.push_str(c.forms[form]);
    Some(spoken)
}
fn strip_symbol<'a>(s: &'a str, symbol: &str) -> Option<&'a str> {
    if let Some(x) = s.strip_prefix(symbol) {
        Some(x.strip_prefix(' ').unwrap_or(x))
    } else {
        s.strip_suffix(symbol).map(|x| x.strip_suffix(' ').unwrap_or(x))
    }
}
pub(super) fn money(s: &str) -> Option<(String, i32)> {
    for (symbol, unit, gender) in [
        ("$", "доларів", Gender::Masculine),
        ("USD", "доларів", Gender::Masculine),
        ("€", "євро", Gender::Masculine),
        ("EUR", "євро", Gender::Masculine),
        ("₴", "гривень", Gender::Feminine),
        ("грн.", "гривень", Gender::Feminine),
        ("грн", "гривень", Gender::Feminine),
    ] {
        for dash in ['-', '–', '—'] {
            if let Some((a, b)) = s.split_once(dash) {
                let (a, b) = if let Some(a) = a.strip_prefix(symbol) {
                    (
                        a.trim_matches(' '),
                        b.trim_matches(' ').strip_prefix(symbol).unwrap_or(b.trim_matches(' ')).trim_matches(' '),
                    )
                } else if let Some(b) = b.trim_matches(' ').strip_suffix(symbol) {
                    (a.trim_matches(' '), b.trim_matches(' '))
                } else {
                    continue;
                };
                if let (Some(a), Some(b)) =
                    (super::cardinal::graph(a, gender, true), super::cardinal::graph(b, gender, true))
                {
                    return Some((format!("від {a} до {b} {unit}"), 7));
                }
            }
        }
    }
    for (left, right, aunit, bunit) in [("$", "€", "доларів", "євро"), ("€", "$", "євро", "доларів")]
    {
        for dash in ['-', '–', '—'] {
            if let Some((a, b)) = s.split_once(dash) {
                if let (Some(a), Some(b)) = (a.strip_prefix(left), b.strip_prefix(right)) {
                    if let (Some(a), Some(b)) = (
                        super::cardinal::graph(a, Gender::Masculine, true),
                        super::cardinal::graph(b, Gender::Masculine, true),
                    ) {
                        return Some((format!("від {a} {aunit} до {b} {bunit}"), 7));
                    }
                }
            }
        }
    }
    for (suffix, unit, index) in
        [("/h", "на годину", 2), ("/год", "на годину", 2), ("/kg", "за кілограм", 3), ("/кг", "за кілограм", 3)]
    {
        if let Some(x) = s.strip_suffix(suffix) {
            for sym in CURRENCIES[index].names {
                if let Some(n) = strip_symbol(x, sym) {
                    if n.bytes().all(|b| b.is_ascii_digit()) {
                        if let Some(w) = amount(n, &CURRENCIES[index]) {
                            return Some((format!("{w} {unit}"), 6));
                        }
                    }
                }
            }
        }
    }
    for (sym, unit) in [("$", "доларів"), ("USD", "доларів"), ("€", "євро"), ("EUR", "євро")] {
        if let Some(x) = s.strip_prefix(sym) {
            if let Some(n) = x.strip_suffix('k').or_else(|| x.strip_suffix('K')) {
                if let Some(w) = number(n, Gender::Masculine, false) {
                    return Some((format!("{w} тисяч {unit}"), 5));
                }
            }
            if let Some(n) = x.strip_suffix('m').or_else(|| x.strip_suffix('M')) {
                if let Some((integer, f)) = n.split_once([',', '.']) {
                    if integer.bytes().all(|b| b.is_ascii_digit()) && f.len() == 1 && f != "0" {
                        if let Some(mut w) = super::decimal::core(n) {
                            if integer.len() == 2 && integer.ends_with('1') && !integer.starts_with('1') {
                                w = w.replacen(" ціла ", " цілих ", 1);
                            }
                            return Some((format!("{w} мільйона {unit}"), 5));
                        }
                    }
                }
            }
        }
    }
    if let Some(n) = s.strip_suffix('¢') {
        let cent =
            Currency { names: &[], forms: ["цент", "центи", "центів"], feminine: false, weight: 12 };
        if n.bytes().all(|b| b.is_ascii_digit()) {
            if let Some(w) = amount(n, &cent) {
                return Some((w, 12));
            }
        }
    }
    let (minus, s) =
        if let Some(x) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) { ("мінус ", x) } else { ("", s) };
    for &major_sym in CURRENCIES[0].names {
        if let Some((major, minor)) = s.split_once(major_sym) {
            let major = major.strip_suffix(' ').unwrap_or(major);
            let minor = minor.strip_prefix(' ').unwrap_or(minor);
            for &sym in CURRENCIES[1].names {
                if let Some(minor) = minor.strip_suffix(sym) {
                    let minor = minor.strip_suffix(' ').unwrap_or(minor);
                    if major.bytes().all(|b| b.is_ascii_digit()) && minor.bytes().all(|b| b.is_ascii_digit()) {
                        if let (Some(a), Some(b)) = (amount(major, &CURRENCIES[0]), amount(minor, &CURRENCIES[1])) {
                            return Some((format!("{minus}{a} {b}"), 10));
                        }
                    }
                }
            }
        }
    }
    for c in CURRENCIES {
        for sym in c.names {
            if let Some(x) = strip_symbol(s, sym) {
                if let Some(w) = amount(x, c) {
                    return Some((format!("{minus}{w}"), c.weight));
                }
                if ["₴", "грн.", "грн", "UAH", "$", "USD", "€", "EUR"].contains(sym) {
                    if let Some((major, minor)) = x.split_once([',', '.']) {
                        if minor.len() == 2
                            && minor != "00"
                            && (c.feminine || !matches!(minor, "06" | "07" | "08" | "09"))
                        {
                            let mn = minor.trim_start_matches('0');
                            let sub = if c.feminine {
                                &CURRENCIES[1]
                            } else {
                                &Currency {
                                    names: &[],
                                    forms: ["цент", "центи", "центів"],
                                    feminine: false,
                                    weight: 10,
                                }
                            };
                            if let (Some(a), Some(b)) = (amount(major, c), amount(mn, sub)) {
                                return Some((format!("{minus}{a} {b}"), 10));
                            }
                        }
                    }
                }
            }
        }
    }
    None
}
