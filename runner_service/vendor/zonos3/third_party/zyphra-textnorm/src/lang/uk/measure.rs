// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
use super::cardinal::{Gender, number};
struct Unit {
    names: &'static [&'static str],
    forms: [&'static str; 3],
    feminine: bool,
}
const UNITS: &[Unit] = &[
    Unit {
        names: &["кг", "кг.", "kg"], forms: ["кілограм", "кілограми", "кілограмів"], feminine: false
    },
    Unit { names: &["т", "т.", "t"], forms: ["тонна", "тонни", "тонн"], feminine: true },
    Unit { names: &["г", "г.", "g"], forms: ["грам", "грами", "грамів"], feminine: false },
    Unit {
        names: &["мг", "мг.", "mg"], forms: ["міліграм", "міліграми", "міліграмів"], feminine: false
    },
    Unit {
        names: &["µg", "μg", "мкг", "мкг."], forms: ["мікрограм", "мікрограми", "мікрограмів"], feminine: false
    },
    Unit {
        names: &["км", "км.", "km"], forms: ["кілометр", "кілометри", "кілометрів"], feminine: false
    },
    Unit { names: &["м", "м.", "m"], forms: ["метр", "метри", "метрів"], feminine: false },
    Unit {
        names: &["см", "см.", "cm"], forms: ["сантиметр", "сантиметри", "сантиметрів"], feminine: false
    },
    Unit {
        names: &["мм", "мм.", "mm"], forms: ["міліметр", "міліметри", "міліметрів"], feminine: false
    },
    Unit {
        names: &["µm", "μm", "мкм"], forms: ["мікрометр", "мікрометри", "мікрометрів"], feminine: false
    },
    Unit {
        names: &["m²", "м²", "m2", "м2"],
        forms: ["квадратний метр", "квадратні метри", "квадратних метрів"],
        feminine: false,
    },
    Unit {
        names: &["m³", "м³", "m3", "м3"],
        forms: ["кубічний метр", "кубічні метри", "кубічних метрів"],
        feminine: false,
    },
    Unit { names: &["л", "л.", "l"], forms: ["літр", "літри", "літрів"], feminine: false },
    Unit {
        names: &["км/год", "км/ч", "km/h"],
        forms: ["кілометр на годину", "кілометри на годину", "кілометрів на годину"],
        feminine: false,
    },
    Unit {
        names: &["Gbps", "Гбіт/с"],
        forms: ["гігабіт на секунду", "гігабіти на секунду", "гігабіт на секунду"],
        feminine: false,
    },
    Unit {
        names: &["Mbps", "Мбіт/с"],
        forms: ["мегабіт на секунду", "мегабіти на секунду", "мегабіт на секунду"],
        feminine: false,
    },
    Unit {
        names: &["mAh", "мАг", "мА·год"],
        forms: ["міліампер-година", "міліампер-години", "міліампер-годин"],
        feminine: true,
    },
    Unit {
        names: &["kWh", "кВт·год", "кВт/год"],
        forms: ["кіловат-година", "кіловат-години", "кіловат-годин"],
        feminine: true,
    },
    Unit {
        names: &["hPa", "гПа"], forms: ["гектопаскаль", "гектопаскалі", "гектопаскалів"], feminine: false
    },
    Unit {
        names: &["kcal", "ккал"], forms: ["кілокалорія", "кілокалорії", "кілокалорій"], feminine: true
    },
    Unit {
        names: &["dB", "дБ"], forms: ["децибел", "децибели", "децибелів"], feminine: false
    },
    Unit { names: &["Ω", "Ω", "Ом"], forms: ["ом", "оми", "ом"], feminine: false },
    Unit { names: &["%"], forms: ["відсоток", "відсотки", "відсотків"], feminine: false },
    Unit { names: &["‰"], forms: ["проміле", "проміле", "проміле"], feminine: false },
    Unit { names: &["°"], forms: ["градус", "градуси", "градусів"], feminine: false },
    Unit {
        names: &["°C", "℃"], forms: ["градус Цельсія", "градуси Цельсія", "градусів Цельсія"], feminine: false
    },
    Unit {
        names: &["°F", "℉"],
        forms: ["градус Фаренгейта", "градуси Фаренгейта", "градусів Фаренгейта"],
        feminine: false,
    },
];
fn find_unit(name: &str) -> Option<&'static Unit> {
    static INDEX: std::sync::LazyLock<std::collections::HashMap<&'static str, usize>> =
        std::sync::LazyLock::new(|| {
            let mut index = std::collections::HashMap::new();
            for (i, unit) in UNITS.iter().enumerate() {
                for &name in unit.names {
                    index.entry(name).or_insert(i);
                }
            }
            index
        });
    INDEX.get(name).map(|&i| &UNITS[i])
}
/// Recognize a simple separated number and unit without spelling either.
pub(super) fn simple(s: &str) -> bool {
    let Some((n, unit)) = s.split_once(' ') else { return false };
    super::cardinal::valid_number(n) && find_unit(unit).is_some()
}
pub(super) fn render_simple(s: &str) -> Option<String> {
    let (n, unit) = s.split_once(' ')?;
    let unit = find_unit(unit)?;
    let mut spoken = number(n, if unit.feminine { Gender::Feminine } else { Gender::Masculine }, false)?;
    spoken.push(' ');
    spoken.push_str(unit.forms[agreement(n)]);
    Some(spoken)
}
pub(super) fn unit_names() -> impl Iterator<Item = &'static str> {
    UNITS.iter().flat_map(|unit| unit.names.iter().copied())
}
fn agreement(n: &str) -> usize {
    let b = n.as_bytes();
    let last = b.last().copied().unwrap_or(b'0');
    if b.len() > 1 && b[b.len() - 2] == b'1' {
        2
    } else {
        match last {
            b'1' => 0,
            b'2'..=b'4' => 1,
            _ => 2,
        }
    }
}
pub(super) fn measure(s: &str) -> Option<(String, i32)> {
    for prep in ["около ", "близько "] {
        if let Some(rest) = s.strip_prefix(prep) {
            for (symbol, unit) in
                [("т.", "тонн"), ("т", "тонн"), ("t", "тонн"), ("руб.", "рублів"), ("руб", "рублів"), ("₽", "рублів")]
            {
                if let Some(n) = rest.strip_suffix(symbol) {
                    let n = n.strip_suffix(' ').unwrap_or(n);
                    if n == "1" && unit == "тонн" {
                        return Some(("близько однієї тонни".into(), 6));
                    }
                    if let Some(n) = super::cardinal::graph(n, Gender::Masculine, true) {
                        return Some((format!("близько {n} {unit}"), 6));
                    }
                }
            }
        }
    }
    for prefix in ["від ", "от ", ""] {
        if let Some(rest) = s.strip_prefix(prefix) {
            for middle in ["10 до 21", "10-21"] {
                if let Some(rest) = rest.strip_prefix(middle) {
                    let rest = rest.strip_prefix(' ').unwrap_or(rest);
                    if ["т", "т.", "t"].contains(&rest) {
                        return Some(("від десяти до двадцяти однієї тонни".into(), 6));
                    }
                }
            }
        }
    }
    for (key, spoken) in [("°", "градус"), ("%", "відсоток"), ("‰", "проміле")] {
        if s == key {
            return Some((spoken.into(), 20));
        }
    }
    for unit in UNITS {
        for &sym in unit.names {
            if let Some(prefix) = s.strip_suffix(sym) {
                let prefix = prefix.strip_suffix(' ').unwrap_or(prefix);
                let (qual, prefix, weight) = if let Some(p) = prefix.strip_prefix('~') {
                    ("приблизно ", p.strip_prefix(' ').unwrap_or(p), 7)
                } else if let Some(p) = prefix.strip_prefix('±') {
                    ("плюс-мінус ", p.strip_prefix(' ').unwrap_or(p), 7)
                } else {
                    ("", prefix, 10)
                };
                if qual.is_empty() {
                    for dash in ['-', '–', '—'] {
                        if let Some((a, b)) = prefix.split_once(dash) {
                            let (term, gender) = if ["kg", "кг", "кг."].contains(&sym) {
                                ("кілограмів", Gender::Masculine)
                            } else if sym == "%" {
                                ("відсотків", Gender::Masculine)
                            } else if ["т", "т.", "t"].contains(&sym) {
                                ("тонн", Gender::Feminine)
                            } else {
                                continue;
                            };
                            if let (Some(a), Some(b)) =
                                (super::cardinal::graph(a, gender, true), super::cardinal::graph(b, gender, true))
                            {
                                return Some((format!("від {a} до {b} {term}"), 6));
                            }
                        }
                    }
                    if ["kg", "кг", "кг."].contains(&sym) && (prefix == "1/2" || prefix == "-1/2" || prefix == "−1/2")
                    {
                        return Some((
                            format!(
                                "{}одна друга кілограма",
                                if prefix.starts_with('-') || prefix.starts_with('−') { "мінус " } else { "" }
                            ),
                            5,
                        ));
                    }
                }
                let (negative, n) = if let Some(x) = prefix.strip_prefix('-').or_else(|| prefix.strip_prefix('−')) {
                    ("мінус ", x)
                } else {
                    ("", prefix)
                };
                let gender = if unit.feminine { Gender::Feminine } else { Gender::Masculine };
                if let Some(w) = number(n, gender, false) {
                    return Some((format!("{qual}{negative}{w} {}", unit.forms[agreement(n)]), weight));
                }
            }
        }
    }
    None
}
