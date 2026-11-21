// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Russian number names and morphology, ported from the production NeMo `ru` package.

#[derive(Clone, Copy, PartialEq, Eq, Debug)]
pub enum Gender {
    Masc,
    Fem,
    Neut,
}

#[derive(Clone, Copy, PartialEq, Eq, Debug)]
pub enum Case {
    Nom,
    Gen,
}

#[derive(Clone, Copy, PartialEq, Eq, Debug)]
pub enum PluralCategory {
    One,
    Few,
    Many,
}

pub fn plural_category(val: u64) -> PluralCategory {
    let rem100 = val % 100;
    if (11..=14).contains(&rem100) {
        PluralCategory::Many
    } else {
        match val % 10 {
            1 => PluralCategory::One,
            2..=4 => PluralCategory::Few,
            _ => PluralCategory::Many,
        }
    }
}

const HUNDREDS_NOM: [&str; 10] =
    ["", "сто", "двести", "триста", "четыреста", "пятьсот", "шестьсот", "семьсот", "восемьсот", "девятьсот"];

const HUNDREDS_GEN: [&str; 10] =
    ["", "ста", "двухсот", "трехсот", "четырехсот", "пятисот", "шестисот", "семисот", "восьмисот", "девятисот"];

const TEENS_NOM: [&str; 10] = [
    "десять",
    "одиннадцать",
    "двенадцать",
    "тринадцать",
    "четырнадцать",
    "пятнадцать",
    "шестнадцать",
    "семнадцать",
    "восемнадцать",
    "девятнадцать",
];

const TEENS_GEN: [&str; 10] = [
    "десяти",
    "одиннадцати",
    "двенадцати",
    "тринадцати",
    "четырнадцати",
    "пятнадцати",
    "шестнадцати",
    "семнадцати",
    "восемнадцати",
    "девятнадцати",
];

const TENS_NOM: [&str; 10] =
    ["", "", "двадцать", "тридцать", "сорок", "пятьдесят", "шестьдесят", "семьдесят", "восемьдесят", "девяносто"];

const TENS_GEN: [&str; 10] =
    ["", "", "двадцати", "тридцати", "сорока", "пятидесяти", "шестидесяти", "семидесяти", "восьмидесяти", "девяноста"];

const UNITS_NOM_MASC: [&str; 10] = ["", "один", "два", "три", "четыре", "пять", "шесть", "семь", "восемь", "девять"];
const UNITS_NOM_FEM: [&str; 10] = ["", "одна", "две", "три", "четыре", "пять", "шесть", "семь", "восемь", "девять"];
const UNITS_NOM_NEUT: [&str; 10] = ["", "одно", "два", "три", "четыре", "пять", "шесть", "семь", "восемь", "девять"];

const UNITS_GEN_MASC: [&str; 10] =
    ["", "одного", "двух", "трех", "четырех", "пяти", "шести", "семи", "восьми", "девяти"];
const UNITS_GEN_FEM: [&str; 10] = ["", "одной", "двух", "трех", "четырех", "пяти", "шести", "семи", "восьми", "девяти"];
const UNITS_GEN_NEUT: [&str; 10] =
    ["", "одного", "двух", "трех", "четырех", "пяти", "шести", "семи", "восьми", "девяти"];

fn push_three_digits(n: u64, gender: Gender, case: Case, out: &mut Vec<&'static str>) {
    let h = (n / 100 % 10) as usize;
    let t = (n / 10 % 10) as usize;
    let u = (n % 10) as usize;

    if h > 0 {
        out.push(match case {
            Case::Nom => HUNDREDS_NOM[h],
            Case::Gen => HUNDREDS_GEN[h],
        });
    }

    if t == 1 {
        out.push(match case {
            Case::Nom => TEENS_NOM[u],
            Case::Gen => TEENS_GEN[u],
        });
    } else {
        if t >= 2 {
            out.push(match case {
                Case::Nom => TENS_NOM[t],
                Case::Gen => TENS_GEN[t],
            });
        }
        if u > 0 {
            out.push(match (case, gender) {
                (Case::Nom, Gender::Masc) => UNITS_NOM_MASC[u],
                (Case::Nom, Gender::Fem) => UNITS_NOM_FEM[u],
                (Case::Nom, Gender::Neut) => UNITS_NOM_NEUT[u],
                (Case::Gen, Gender::Masc) => UNITS_GEN_MASC[u],
                (Case::Gen, Gender::Fem) => UNITS_GEN_FEM[u],
                (Case::Gen, Gender::Neut) => UNITS_GEN_NEUT[u],
            });
        }
    }
}

pub fn cardinal(n: u64, gender: Gender, case: Case) -> String {
    if n == 0 {
        return match case {
            Case::Nom => "ноль".to_string(),
            Case::Gen => "нуля".to_string(),
        };
    }

    let mut words = Vec::new();
    let billions = n / 1_000_000_000 % 1000;
    let millions = n / 1_000_000 % 1000;
    let thousands = n / 1000 % 1000;
    let units = n % 1000;

    if billions > 0 {
        push_three_digits(billions, Gender::Masc, case, &mut words);
        let cat = plural_category(billions);
        let noun = match (case, cat) {
            (Case::Nom, PluralCategory::One) => "миллиард",
            (Case::Nom, PluralCategory::Few) => "миллиарда",
            (Case::Nom, PluralCategory::Many) => "миллиардов",
            (Case::Gen, PluralCategory::One) => "миллиарда",
            (Case::Gen, _) => "миллиардов",
        };
        words.push(noun);
    }

    if millions > 0 {
        push_three_digits(millions, Gender::Masc, case, &mut words);
        let cat = plural_category(millions);
        let noun = match (case, cat) {
            (Case::Nom, PluralCategory::One) => "миллион",
            (Case::Nom, PluralCategory::Few) => "миллиона",
            (Case::Nom, PluralCategory::Many) => "миллионов",
            (Case::Gen, PluralCategory::One) => "миллиона",
            (Case::Gen, _) => "миллионов",
        };
        words.push(noun);
    }

    if thousands > 0 {
        push_three_digits(thousands, Gender::Fem, case, &mut words);
        let cat = plural_category(thousands);
        let noun = match (case, cat) {
            (Case::Nom, PluralCategory::One) => "тысяча",
            (Case::Nom, PluralCategory::Few) => "тысячи",
            (Case::Nom, PluralCategory::Many) => "тысяч",
            (Case::Gen, PluralCategory::One) => "тысячи",
            (Case::Gen, _) => "тысяч",
        };
        words.push(noun);
    }

    if units > 0 {
        push_three_digits(units, gender, case, &mut words);
    }

    words.join(" ")
}

#[derive(Clone, Copy, PartialEq, Eq, Debug)]
pub enum OrdinalClass {
    Stressed,
    Hard,
    Soft,
}

#[derive(Clone, Copy, PartialEq, Eq, Debug)]
pub enum OrdinalForm {
    MascNom,
    FemNom,
    NeutNom,
    PlNom,
    MascGen,
    FemGen,
    MascDat,
    MascIns,
    MascPrep,
    PlGen,
    PlIns,
    FemAcc,
}

pub fn ordinal_ending(cls: OrdinalClass, form: OrdinalForm) -> &'static str {
    match cls {
        OrdinalClass::Soft => match form {
            OrdinalForm::MascNom => "ий",
            OrdinalForm::FemNom => "ья",
            OrdinalForm::NeutNom => "ье",
            OrdinalForm::PlNom => "ьи",
            OrdinalForm::MascGen => "ьего",
            OrdinalForm::FemGen => "ьей",
            OrdinalForm::MascDat => "ьему",
            OrdinalForm::MascIns => "ьим",
            OrdinalForm::MascPrep => "ьем",
            OrdinalForm::PlGen => "ьих",
            OrdinalForm::PlIns => "ьими",
            OrdinalForm::FemAcc => "ью",
        },
        OrdinalClass::Stressed if form == OrdinalForm::MascNom => "ой",
        _ => match form {
            OrdinalForm::MascNom => "ый",
            OrdinalForm::FemNom => "ая",
            OrdinalForm::NeutNom => "ое",
            OrdinalForm::PlNom => "ые",
            OrdinalForm::MascGen => "ого",
            OrdinalForm::FemGen => "ой",
            OrdinalForm::MascDat => "ому",
            OrdinalForm::MascIns => "ым",
            OrdinalForm::MascPrep => "ом",
            OrdinalForm::PlGen => "ых",
            OrdinalForm::PlIns => "ыми",
            OrdinalForm::FemAcc => "ую",
        },
    }
}

fn unit_ordinal_stem(u: usize) -> (&'static str, OrdinalClass) {
    match u {
        1 => ("перв", OrdinalClass::Hard),
        2 => ("втор", OrdinalClass::Stressed),
        3 => ("трет", OrdinalClass::Soft),
        4 => ("четверт", OrdinalClass::Hard),
        5 => ("пят", OrdinalClass::Hard),
        6 => ("шест", OrdinalClass::Stressed),
        7 => ("седьм", OrdinalClass::Stressed),
        8 => ("восьм", OrdinalClass::Stressed),
        9 => ("девят", OrdinalClass::Hard),
        _ => ("", OrdinalClass::Hard),
    }
}

fn teen_ordinal_stem(u: usize) -> (&'static str, OrdinalClass) {
    match u {
        0 => ("десят", OrdinalClass::Hard),
        1 => ("одиннадцат", OrdinalClass::Hard),
        2 => ("двенадцат", OrdinalClass::Hard),
        3 => ("тринадцат", OrdinalClass::Hard),
        4 => ("четырнадцат", OrdinalClass::Hard),
        5 => ("пятнадцат", OrdinalClass::Hard),
        6 => ("шестнадцат", OrdinalClass::Hard),
        7 => ("семнадцат", OrdinalClass::Hard),
        8 => ("восемнадцат", OrdinalClass::Hard),
        9 => ("девятнадцат", OrdinalClass::Hard),
        _ => ("", OrdinalClass::Hard),
    }
}

fn tens_ordinal_stem(t: usize) -> (&'static str, OrdinalClass) {
    match t {
        2 => ("двадцат", OrdinalClass::Hard),
        3 => ("тридцат", OrdinalClass::Hard),
        4 => ("сороков", OrdinalClass::Stressed),
        5 => ("пятидесят", OrdinalClass::Hard),
        6 => ("шестидесят", OrdinalClass::Hard),
        7 => ("семидесят", OrdinalClass::Hard),
        8 => ("восьмидесят", OrdinalClass::Hard),
        9 => ("девяност", OrdinalClass::Hard),
        _ => ("", OrdinalClass::Hard),
    }
}

fn hundreds_ordinal_stem(h: usize) -> (&'static str, OrdinalClass) {
    match h {
        1 => ("сот", OrdinalClass::Hard),
        2 => ("двухсот", OrdinalClass::Hard),
        3 => ("трехсот", OrdinalClass::Hard),
        4 => ("четырехсот", OrdinalClass::Hard),
        5 => ("пятисот", OrdinalClass::Hard),
        6 => ("шестисот", OrdinalClass::Hard),
        7 => ("семисот", OrdinalClass::Hard),
        8 => ("восьмисот", OrdinalClass::Hard),
        9 => ("девятисот", OrdinalClass::Hard),
        _ => ("", OrdinalClass::Hard),
    }
}

fn fused_prefix(multiplier: u64) -> String {
    if multiplier <= 1 {
        return String::new();
    }
    let h = (multiplier / 100 % 10) as usize;
    let t = (multiplier / 10 % 10) as usize;
    let u = (multiplier % 10) as usize;

    let mut s = String::new();
    if h > 0 {
        if h == 1 {
            s.push_str("сто");
        } else {
            s.push_str(HUNDREDS_GEN[h]);
        }
    }
    if t == 1 {
        s.push_str(TEENS_GEN[u]);
    } else {
        if t >= 2 {
            if t == 9 {
                s.push_str("девяносто");
            } else {
                s.push_str(TENS_GEN[t]);
            }
        }
        if u > 0 {
            if u == 1 {
                s.push_str("одно");
            } else {
                s.push_str(UNITS_GEN_MASC[u]);
            }
        }
    }
    s
}

pub fn ordinal_stem(n: u64) -> (String, OrdinalClass) {
    if n == 0 {
        return ("нулев".to_string(), OrdinalClass::Stressed);
    }

    let billions = n / 1_000_000_000 % 1000;
    let millions = n / 1_000_000 % 1000;
    let thousands = n / 1000 % 1000;
    let units = n % 1000;

    // Fused magnitude checks (powers of 1000 ending in zeros)
    if units == 0 && thousands == 0 && millions == 0 && billions > 0 {
        let prefix = fused_prefix(billions);
        return (format!("{prefix}миллиардн"), OrdinalClass::Hard);
    }
    if units == 0 && thousands == 0 && millions > 0 {
        let mut words = Vec::new();
        if billions > 0 {
            if billions != 1 {
                push_three_digits(billions, Gender::Masc, Case::Nom, &mut words);
            }
            let noun = match plural_category(billions) {
                PluralCategory::One => "миллиард",
                PluralCategory::Few => "миллиарда",
                PluralCategory::Many => "миллиардов",
            };
            words.push(noun);
        }
        let prefix = fused_prefix(millions);
        let last_stem = format!("{prefix}миллионн");
        if words.is_empty() {
            return (last_stem, OrdinalClass::Hard);
        } else {
            return (format!("{} {last_stem}", words.join(" ")), OrdinalClass::Hard);
        }
    }
    if units == 0 && thousands > 0 {
        let mut words = Vec::new();
        if billions > 0 {
            if billions != 1 {
                push_three_digits(billions, Gender::Masc, Case::Nom, &mut words);
            }
            let noun = match plural_category(billions) {
                PluralCategory::One => "миллиард",
                PluralCategory::Few => "миллиарда",
                PluralCategory::Many => "миллиардов",
            };
            words.push(noun);
        }
        if millions > 0 {
            if millions != 1 {
                push_three_digits(millions, Gender::Masc, Case::Nom, &mut words);
            }
            let noun = match plural_category(millions) {
                PluralCategory::One => "миллион",
                PluralCategory::Few => "миллиона",
                PluralCategory::Many => "миллионов",
            };
            words.push(noun);
        }
        let prefix = fused_prefix(thousands);
        let last_stem = format!("{prefix}тысячн");
        if words.is_empty() {
            return (last_stem, OrdinalClass::Hard);
        } else {
            return (format!("{} {last_stem}", words.join(" ")), OrdinalClass::Hard);
        }
    }

    // Otherwise: higher parts as cardinal (with_one=false for thousands)
    let mut words = Vec::new();
    if billions > 0 {
        if billions != 1 {
            push_three_digits(billions, Gender::Masc, Case::Nom, &mut words);
        }
        let noun = match plural_category(billions) {
            PluralCategory::One => "миллиард",
            PluralCategory::Few => "миллиарда",
            PluralCategory::Many => "миллиардов",
        };
        words.push(noun);
    }
    if millions > 0 {
        if millions != 1 {
            push_three_digits(millions, Gender::Masc, Case::Nom, &mut words);
        }
        let noun = match plural_category(millions) {
            PluralCategory::One => "миллион",
            PluralCategory::Few => "миллиона",
            PluralCategory::Many => "миллионов",
        };
        words.push(noun);
    }
    if thousands > 0 {
        if thousands == 1 {
            words.push("тысяча");
        } else {
            push_three_digits(thousands, Gender::Fem, Case::Nom, &mut words);
            let noun = match plural_category(thousands) {
                PluralCategory::One => "тысяча",
                PluralCategory::Few => "тысячи",
                PluralCategory::Many => "тысяч",
            };
            words.push(noun);
        }
    }

    // Units block 1..999 is the ordinal group
    let h = (units / 100 % 10) as usize;
    let t = (units / 10 % 10) as usize;
    let u = (units % 10) as usize;

    if t == 1 {
        if h > 0 {
            words.push(HUNDREDS_NOM[h]);
        }
        let (stem, cls) = teen_ordinal_stem(u);
        if words.is_empty() { (stem.to_string(), cls) } else { (format!("{} {stem}", words.join(" ")), cls) }
    } else if u > 0 {
        if h > 0 {
            words.push(HUNDREDS_NOM[h]);
        }
        if t >= 2 {
            words.push(TENS_NOM[t]);
        }
        let (stem, cls) = unit_ordinal_stem(u);
        if words.is_empty() { (stem.to_string(), cls) } else { (format!("{} {stem}", words.join(" ")), cls) }
    } else if t >= 2 {
        if h > 0 {
            words.push(HUNDREDS_NOM[h]);
        }
        let (stem, cls) = tens_ordinal_stem(t);
        if words.is_empty() { (stem.to_string(), cls) } else { (format!("{} {stem}", words.join(" ")), cls) }
    } else if h > 0 {
        let (stem, cls) = hundreds_ordinal_stem(h);
        if words.is_empty() { (stem.to_string(), cls) } else { (format!("{} {stem}", words.join(" ")), cls) }
    } else {
        ("нулев".to_string(), OrdinalClass::Stressed)
    }
}

pub fn ordinal(n: u64, form: OrdinalForm) -> String {
    let (stem, cls) = ordinal_stem(n);
    let end = ordinal_ending(cls, form);
    format!("{stem}{end}")
}

#[derive(Clone, Copy, PartialEq, Eq, Debug)]
pub enum SeparatorKind {
    None,
    Space,
    Nbsp,
    Dot,
}

pub fn parse_grouped_number(s: &str) -> Option<(u64, SeparatorKind)> {
    if s.is_empty() {
        return None;
    }
    let mut value = 0u64;
    let mut all_digits = true;
    let mut nonzero = false;
    for (i, b) in s.bytes().enumerate() {
        if !b.is_ascii_digit() {
            all_digits = false;
            break;
        }
        nonzero |= b != b'0';
        if i < 12 {
            value = value * 10 + u64::from(b - b'0');
        }
    }
    if all_digits {
        if s.len() > 12 && nonzero {
            return None;
        }
        return Some((value, SeparatorKind::None));
    }
    if !s.as_bytes()[0].is_ascii_digit() {
        return None;
    }

    let sep = if s.contains('.') {
        SeparatorKind::Dot
    } else if s.contains('\u{a0}') {
        SeparatorKind::Nbsp
    } else if s.contains(' ') {
        SeparatorKind::Space
    } else {
        return None;
    };

    let sep_char = match sep {
        SeparatorKind::Dot => '.',
        SeparatorKind::Nbsp => '\u{a0}',
        SeparatorKind::Space => ' ',
        SeparatorKind::None => unreachable!(),
    };

    let mut groups = s.split(sep_char);
    let first = groups.next()?;
    if first.is_empty() || first.len() > 3 || !first.chars().all(|c| c.is_ascii_digit()) {
        return None;
    }
    let mut val: u64 = first.parse().ok()?;
    let mut count = 0;
    for g in groups {
        if g.len() != 3 || !g.chars().all(|c| c.is_ascii_digit()) {
            return None;
        }
        let chunk: u64 = g.parse().ok()?;
        val = val.checked_mul(1000)?.checked_add(chunk)?;
        count += 1;
    }
    if count == 0 || (first.len() + 3 * count > 12 && val != 0) {
        return None;
    }
    Some((val, sep))
}
