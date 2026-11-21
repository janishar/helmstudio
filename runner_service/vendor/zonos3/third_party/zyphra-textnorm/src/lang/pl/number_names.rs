// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
//
// Polish number morphology from pl/taggers/number_names.py and CLDR release 49.
#[derive(Clone, Copy, PartialEq, Eq)]
pub enum Gender {
    Masculine,
    Feminine,
}
#[derive(Clone, Copy, PartialEq, Eq)]
pub enum Case {
    Nominative,
    Genitive,
}

// Each source lexicon is a small closed primitive table. Direct numeric
// indexing avoids hashing the entire TSV text on every spoken component.
macro_rules! table {
    ($name:literal) => {{
        static DATA: std::sync::LazyLock<[Option<&'static str>; 1000]> = std::sync::LazyLock::new(|| {
            let mut index = [None; 1000];
            for line in include_str!(concat!("../../../data/pl/numbers/", $name, ".tsv")).lines() {
                if let Some((key, value)) = line.split_once('\t') {
                    if let Ok(n) = key.parse::<usize>() {
                        if let Some(slot) = index.get_mut(n) {
                            *slot = Some(value);
                        }
                    }
                }
            }
            index
        });
        &*DATA
    }};
}

fn lookup(table: &'static [Option<&'static str>; 1000], n: u16) -> Option<&'static str> {
    table.get(n as usize).copied().flatten()
}
fn append_word(out: &mut String, word: &str) {
    if !out.is_empty() {
        out.push(' ');
    }
    out.push_str(word);
}

fn cardinal_group(n: u16, gender: Gender, case: Case) -> Option<String> {
    if n == 0 || n >= 1000 {
        return None;
    }
    let (ones, teens, tens, hundreds) = if case == Case::Genitive {
        (table!("digit_gen"), table!("teen_gen"), table!("ties_gen"), table!("hundreds_gen"))
    } else {
        (
            match gender {
                Gender::Masculine => table!("digit"),
                Gender::Feminine => table!("digit_fem"),
            },
            table!("teen"),
            table!("ties"),
            table!("hundreds"),
        )
    };
    let mut out = String::new();
    if n >= 100 {
        append_word(&mut out, lookup(hundreds, n / 100 * 100)?);
    }
    let rest = n % 100;
    if (10..20).contains(&rest) {
        append_word(&mut out, lookup(teens, rest)?);
    } else {
        if rest >= 20 {
            append_word(&mut out, lookup(tens, rest / 10 * 10)?);
        }
        if rest % 10 != 0 {
            append_word(&mut out, lookup(ones, rest % 10)?);
        }
    }
    Some(out)
}

/// Source accepts canonical decimal input without leading zero, up to twelve digits.
pub fn cardinal(input: &str, gender: Gender, case: Case) -> Option<String> {
    if input.is_empty()
        || input.len() > 12
        || !input.bytes().all(|b| b.is_ascii_digit())
        || (input.len() > 1 && input.starts_with('0'))
    {
        return None;
    }
    let n: u64 = input.parse().ok()?;
    if n == 0 {
        return Some(if case == Case::Genitive { "zera" } else { "zero" }.into());
    }
    if case == Case::Genitive {
        if n < 1000 {
            return cardinal_group(n as u16, gender, case);
        }
        if n == 1000 {
            return Some("tysiąca".into());
        }
        if n < 2000 {
            return Some(format!("tysiąc {}", cardinal_group((n - 1000) as u16, gender, case)?));
        }
        return None;
    }
    if n < 1000 {
        return cardinal_group(n as u16, gender, case);
    }
    let names = [
        ("", "", ""),
        ("tysiąc", "tysiące", "tysięcy"),
        ("milion", "miliony", "milionów"),
        ("miliard", "miliardy", "miliardów"),
    ];
    let mut out = String::new();
    for level in (0..4).rev() {
        let group = ((n / 1000u64.pow(level as u32)) % 1000) as u16;
        if group == 0 {
            continue;
        }
        if level == 0 {
            append_word(&mut out, &cardinal_group(group, gender, case)?);
            continue;
        }
        let form = if group == 1 {
            0
        } else if (2..=4).contains(&(group % 10)) && !(12..=14).contains(&(group % 100)) {
            1
        } else {
            2
        };
        let names = names[level];
        let noun = [names.0, names.1, names.2][form];
        if group == 1 {
            append_word(&mut out, noun);
        } else {
            append_word(&mut out, &cardinal_group(group, Gender::Masculine, case)?);
            append_word(&mut out, noun);
        }
    }
    Some(out)
}

fn ordinal_group(n: u16, gender: Gender, case: Case) -> Option<String> {
    if n == 0 || n >= 1000 {
        return None;
    }
    let (ones, teens, tens, hundreds) = match (gender, case) {
        (Gender::Feminine, Case::Genitive) => (
            table!("ordinal_ones_fem_gen"),
            table!("ordinal_teens_fem_gen"),
            table!("ordinal_tens_fem_gen"),
            table!("ordinal_hundreds_fem_gen"),
        ),
        (_, Case::Genitive) => (
            table!("ordinal_ones_gen"),
            table!("ordinal_teens_gen"),
            table!("ordinal_tens_gen"),
            table!("ordinal_hundreds_gen"),
        ),
        (Gender::Feminine, _) => (
            table!("ordinal_ones_fem"),
            table!("ordinal_teens_fem"),
            table!("ordinal_tens_fem"),
            table!("ordinal_hundreds"),
        ),
        _ => (table!("ordinal_ones"), table!("ordinal_teens"), table!("ordinal_tens"), table!("ordinal_hundreds")),
    };
    let mut out = String::new();
    if n >= 100 {
        append_word(&mut out, lookup(if n % 100 == 0 { hundreds } else { table!("hundreds") }, n / 100 * 100)?);
    }
    let rest = n % 100;
    if (10..20).contains(&rest) {
        append_word(&mut out, lookup(teens, rest)?);
    } else {
        if rest >= 20 {
            append_word(&mut out, lookup(tens, rest / 10 * 10)?);
        }
        if rest % 10 != 0 {
            append_word(&mut out, lookup(ones, rest % 10)?);
        }
    }
    Some(out)
}

/// Source ordinal group permits padded inputs and the two productive thousand prefixes.
pub fn ordinal(input: &str, gender: Gender, case: Case) -> Option<String> {
    if input.is_empty() || input.len() > 4 || !input.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    let n: u16 = input.parse().ok()?;
    if n == 0 {
        return (input == "0").then(|| {
            match (gender, case) {
                (Gender::Feminine, Case::Genitive) => "zerowej",
                (_, Case::Genitive) => "zerowego",
                (Gender::Feminine, _) => "zerowa",
                _ => "zerowy",
            }
            .into()
        });
    }
    if n < 1000 {
        return (input.len() <= 3).then(|| ordinal_group(n, gender, case)).flatten();
    }
    if n >= 3000 {
        return None;
    }
    if n % 1000 == 0 {
        let stem = if n == 1000 { "tysięczn" } else { "dwutysięczn" };
        let ending = match (gender, case) {
            (Gender::Feminine, Case::Genitive) => "ej",
            (_, Case::Genitive) => "ego",
            (Gender::Feminine, _) => "a",
            _ => "y",
        };
        return Some(format!("{stem}{ending}"));
    }
    Some(format!("{} {}", if n < 2000 { "tysiąc" } else { "dwa tysiące" }, ordinal_group(n % 1000, gender, case)?))
}
