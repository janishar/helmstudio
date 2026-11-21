use super::numbers::{decimal, number_weighted, split_range, unsigned};
use std::sync::LazyLock;
struct Unit {
    symbols: &'static [&'static str],
    one: &'static str,
    few: &'static str,
    many: &'static str,
    fem: bool,
}
const UNITS: &[Unit] = &[
    Unit {
        symbols: &["km", "kilometr", "kilometry", "kilometrů"],
        one: "kilometr",
        few: "kilometry",
        many: "kilometrů",
        fem: false,
    },
    Unit { symbols: &["m", "metr", "metry", "metrů"], one: "metr", few: "metry", many: "metrů", fem: false },
    Unit {
        symbols: &["cm", "centimetr", "centimetry", "centimetrů"],
        one: "centimetr",
        few: "centimetry",
        many: "centimetrů",
        fem: false,
    },
    Unit {
        symbols: &["mm", "milimetr", "milimetry", "milimetrů"],
        one: "milimetr",
        few: "milimetry",
        many: "milimetrů",
        fem: false,
    },
    Unit { symbols: &["µm", "μm"], one: "mikrometr", few: "mikrometry", many: "mikrometrů", fem: false },
    Unit {
        symbols: &["kg", "kilogram", "kilogramy", "kilogramů", "kilo", "kila", "kil"],
        one: "kilogram",
        few: "kilogramy",
        many: "kilogramů",
        fem: false,
    },
    Unit { symbols: &["g", "gram", "gramy", "gramů"], one: "gram", few: "gramy", many: "gramů", fem: false },
    Unit {
        symbols: &["mg", "miligram", "miligramy", "miligramů"],
        one: "miligram",
        few: "miligramy",
        many: "miligramů",
        fem: false,
    },
    Unit { symbols: &["µg", "μg"], one: "mikrogram", few: "mikrogramy", many: "mikrogramů", fem: false },
    Unit { symbols: &["t", "tuna", "tuny", "tun"], one: "tuna", few: "tuny", many: "tun", fem: true },
    Unit { symbols: &["l", "litr", "litry", "litrů"], one: "litr", few: "litry", many: "litrů", fem: false },
    Unit {
        symbols: &["ml", "mililitr", "mililitry", "mililitrů"],
        one: "mililitr",
        few: "mililitry",
        many: "mililitrů",
        fem: false,
    },
    Unit {
        symbols: &["dl", "decilitr", "decilitry", "decilitrů"],
        one: "decilitr",
        few: "decilitry",
        many: "decilitrů",
        fem: false,
    },
    Unit {
        symbols: &["cl", "centilitr", "centilitry", "centilitrů"],
        one: "centilitr",
        few: "centilitry",
        many: "centilitrů",
        fem: false,
    },
];
const FIXED: &[(&[&str], &str)] = &[
    (&["m²"], "metrů čtverečních"),
    (&["m³"], "metrů krychlových"),
    (&["km/h", "km/hod"], "kilometrů za hodinu"),
    (&["m/s", "m/sec"], "metrů za sekundu"),
    (&["Gbps"], "gigabitů za sekundu"),
    (&["Mbps"], "megabitů za sekundu"),
    (&["mAh"], "miliampérhodin"),
    (&["kWh"], "kilowatthodin"),
    (&["hPa"], "hektopascalů"),
    (&["kcal"], "kilokalorií"),
    (&["dB"], "decibelů"),
    (&["Ω", "Ω"], "ohmů"),
    (&["GB"], "gigabajtů"),
    (&["GHz"], "gigahertz"),
];
static UNIT_END: LazyLock<[bool; 256]> = LazyLock::new(|| {
    let mut bytes = [false; 256];
    for unit in UNITS {
        for symbol in unit.symbols {
            if let Some(last) = symbol.as_bytes().last() {
                bytes[*last as usize] = true;
            }
        }
    }
    for (symbols, _) in FIXED {
        for symbol in *symbols {
            if let Some(last) = symbol.as_bytes().last() {
                bytes[*last as usize] = true;
            }
        }
    }
    for symbol in ["°", "°C", "℃", "st. C"] {
        if let Some(last) = symbol.as_bytes().last() {
            bytes[*last as usize] = true;
        }
    }
    bytes
});
/// Unit spellings bucketed by their final UTF-8 byte, in source order.
static UNIT_SUFFIX: LazyLock<[Vec<(&'static str, &'static Unit)>; 256]> = LazyLock::new(|| {
    let mut rows: [Vec<(&'static str, &'static Unit)>; 256] = std::array::from_fn(|_| Vec::new());
    for unit in UNITS {
        for &symbol in unit.symbols {
            if let Some(last) = symbol.as_bytes().last() {
                rows[*last as usize].push((symbol, unit));
            }
        }
    }
    rows
});
pub fn may_match(s: &str) -> bool {
    if !s.as_bytes().last().is_some_and(|last| UNIT_END[*last as usize]) {
        return false;
    }
    s.ends_with('°')
        || s.ends_with("°C")
        || s.ends_with('℃')
        || s.ends_with("st. C")
        || FIXED.iter().any(|(symbols, _)| symbols.iter().any(|symbol| s.ends_with(*symbol)))
        || s.as_bytes()
            .last()
            .is_some_and(|last| UNIT_SUFFIX[*last as usize].iter().any(|(symbol, _)| s.ends_with(*symbol)))
}
/// A source unit row and its recognized ungrouped integer.
#[derive(Clone, Copy)]
pub struct SimpleReading {
    number: u32,
    unit: &'static Unit,
}
pub fn simple_reading(s: &str) -> Option<SimpleReading> {
    let last = *s.as_bytes().last()? as usize;
    for &(symbol, unit) in &UNIT_SUFFIX[last] {
        let Some(number) = strip_unit(s, symbol) else { continue };
        if number.is_empty()
            || number.len() > 6
            || !number.bytes().all(|b| b.is_ascii_digit())
            || (number.len() > 1 && number.starts_with('0'))
        {
            continue;
        }
        return Some(SimpleReading { number: number.parse().ok()?, unit });
    }
    None
}

pub fn simple_weight(reading: SimpleReading) -> f64 {
    if (1..=4).contains(&reading.number) { -0.01 } else { 0.0 }
}

pub fn simple_render(reading: SimpleReading) -> String {
    let unit = reading.unit;
    let (mut spoken, label) = match reading.number {
        1 => ((if unit.fem { "jedna" } else { "jeden" }).to_owned(), unit.one),
        2 => ((if unit.fem { "dvě" } else { "dva" }).to_owned(), unit.few),
        3 => ("tři".to_owned(), unit.few),
        4 => ("čtyři".to_owned(), unit.few),
        n => (super::graph_utils::positive_value(n as u64).expect("recognized Czech measure"), unit.many),
    };
    if unit.fem && !(1..=4).contains(&reading.number) {
        if spoken == "dva" {
            spoken = "dvě".to_owned();
        } else if spoken.ends_with(" dva") {
            spoken.truncate(spoken.len() - 3);
            spoken.push_str("dvě");
        } else if spoken.ends_with(" jeden") {
            spoken.truncate(spoken.len() - 5);
            spoken.push_str("jedna");
        }
    }
    spoken.push(' ');
    spoken.push_str(label);
    spoken
}
fn strip_unit<'a>(s: &'a str, sym: &str) -> Option<&'a str> {
    let n = s.strip_suffix(sym)?;
    Some(n.strip_suffix(' ').unwrap_or(n))
}
fn choice(s: &str, decimal_first: bool) -> Option<(String, f64)> {
    if decimal_first { decimal(s).or_else(|| number_weighted(s)) } else { number_weighted(s) }
}
fn core(s: &str, decimal_first: bool) -> Option<(String, f64)> {
    for (syms, unit) in FIXED {
        for sym in *syms {
            if let Some(n) = strip_unit(s, sym) {
                if let Some((v, w)) = choice(n, decimal_first) {
                    return Some((format!("{v} {unit}"), w));
                }
            }
        }
    }
    for (sym, celsius) in [("°C", true), ("℃", true), ("st. C", true), ("°", false)] {
        if let Some(n) = strip_unit(s, sym) {
            let (val, w) = match n {
                "1" => ("jeden stupeň".to_owned(), 0.0),
                "2" => ("dva stupně".to_owned(), 0.0),
                "3" => ("tři stupně".to_owned(), 0.0),
                "4" => ("čtyři stupně".to_owned(), 0.0),
                _ => {
                    let number = if decimal_first && celsius {
                        decimal(n).or_else(|| unsigned(n).map(|x| (x, 0.0)))
                    } else {
                        unsigned(n).map(|x| (x, 0.0)).or_else(|| if celsius { decimal(n) } else { None })
                    };
                    let (v, w) = number?;
                    (format!("{v} stupňů"), w)
                }
            };
            return Some((format!("{val}{}", if celsius { " Celsia" } else { "" }), w));
        }
    }
    for u in UNITS {
        for sym in u.symbols {
            let Some(n) = strip_unit(s, sym) else { continue };
            let (v, unit, weight) = match n {
                "1" => ((if u.fem { "jedna" } else { "jeden" }).to_owned(), u.one.to_owned(), -0.01),
                "2" => ((if u.fem { "dvě" } else { "dva" }).to_owned(), u.few.to_owned(), -0.01),
                "3" => ("tři".to_owned(), u.few.to_owned(), -0.01),
                "4" => ("čtyři".to_owned(), u.few.to_owned(), -0.01),
                _ => {
                    if decimal_first {
                        if let Some((v, w)) = decimal(n) {
                            return Some((
                                format!("{v} {}", if u.fem { u.few.to_owned() } else { format!("{}u", u.one) }),
                                w,
                            ));
                        }
                    }
                    if let Some(mut v) = unsigned(n) {
                        if u.fem {
                            if v == "dva" {
                                v = "dvě".to_owned()
                            } else if v.ends_with(" dva") {
                                v.truncate(v.len() - 3);
                                v.push_str("dvě")
                            } else if v.ends_with(" jeden") {
                                v.truncate(v.len() - 5);
                                v.push_str("jedna")
                            }
                        }
                        (v, u.many.to_owned(), 0.0)
                    } else {
                        let Some((v, w)) = decimal(n) else { continue };
                        (v, if u.fem { u.few.to_owned() } else { format!("{}u", u.one) }, w)
                    }
                }
            };
            return Some((format!("{v} {unit}"), weight));
        }
    }
    None
}
fn parse(s: &str, left_decimal: bool, right_decimal: bool) -> Option<(String, f64)> {
    if let Some(x) = core(s, right_decimal) {
        return Some(x);
    }
    if let Some(p) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) {
        if let Some((v, w)) = core(p, right_decimal) {
            return Some((format!("mínus {v}"), w));
        }
    }
    for (a, b) in split_range(s) {
        if let (Some((a, wa)), Some((b, wb))) = (choice(a, left_decimal), core(b, right_decimal)) {
            return Some((format!("{a} až {b}"), wa + wb));
        }
    }
    None
}
pub fn measure(s: &str) -> Vec<(String, f64)> {
    // Only dot notation can be both a grouped cardinal and a decimal;
    // without it all four preference combinations traverse the same path.
    if !s.contains('.') {
        return parse(s, false, false).into_iter().collect();
    }
    let mut out = Vec::new();
    for l in [false, true] {
        for r in [false, true] {
            if let Some(v) = parse(s, l, r) {
                if !out.contains(&v) {
                    out.push(v)
                }
            }
        }
    }
    out
}
