// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Native equivalent of ro.graph_utils' productive number transducer.

#[derive(Clone, Copy, PartialEq, Eq)]
pub enum Gender {
    Masculine,
    MasculineAdjective,
    Feminine,
    FeminineAdjective,
    Neuter,
    NeuterAdjective,
}

fn write_triplet(value: u16, gender: Gender, out: &mut String) {
    const DIGITS: [&str; 10] = ["zero", "unu", "doi", "trei", "patru", "cinci", "șase", "șapte", "opt", "nouă"];
    const TEENS: [&str; 10] = [
        "zece",
        "unsprezece",
        "doisprezece",
        "treisprezece",
        "paisprezece",
        "cincisprezece",
        "șaisprezece",
        "șaptesprezece",
        "optsprezece",
        "nouăsprezece",
    ];
    const TENS: [&str; 10] =
        ["", "", "douăzeci", "treizeci", "patruzeci", "cincizeci", "șaizeci", "șaptezeci", "optzeci", "nouăzeci"];
    let hundreds = (value / 100) as usize;
    if hundreds > 0 {
        if !out.is_empty() {
            out.push(' ');
        }
        match hundreds {
            1 => out.push_str("o sută"),
            2 => out.push_str("două sute"),
            _ => {
                out.push_str(DIGITS[hundreds]);
                out.push_str(" sute");
            }
        }
    }
    let rem = (value % 100) as usize;
    let unit = |n: usize| -> &'static str {
        match n {
            1 => match gender {
                Gender::Feminine => "una",
                Gender::FeminineAdjective => "o",
                Gender::MasculineAdjective | Gender::NeuterAdjective => "un",
                _ => "unu",
            },
            2 if matches!(
                gender,
                Gender::Feminine | Gender::FeminineAdjective | Gender::Neuter | Gender::NeuterAdjective
            ) =>
            {
                "două"
            }
            _ => DIGITS[n],
        }
    };
    if rem > 0 {
        if !out.is_empty() {
            out.push(' ');
        }
        if rem < 10 {
            out.push_str(unit(rem));
        } else if rem < 20 {
            out.push_str(
                if rem == 12
                    && matches!(
                        gender,
                        Gender::Feminine | Gender::FeminineAdjective | Gender::Neuter | Gender::NeuterAdjective
                    )
                {
                    "douăsprezece"
                } else {
                    TEENS[rem - 10]
                },
            );
        } else {
            out.push_str(TENS[rem / 10]);
            if rem % 10 != 0 {
                out.push_str(" și ");
                out.push_str(unit(rem % 10));
            }
        }
    }
}

/// Parse the exact grouped or ungrouped input accepted by the number graph.
pub fn parse_number(input: &str) -> Option<u64> {
    let mut n = 0u64;
    let mut digits = 0usize;
    let mut first_zero = false;
    let mut group_len = 0usize;
    let mut grouped = false;
    for c in input.chars() {
        if let Some(d) = c.to_digit(10).filter(|_| c.is_ascii_digit()) {
            if digits == 0 {
                first_zero = d == 0;
            }
            digits += 1;
            if digits > 12 {
                return None;
            }
            n = n * 10 + u64::from(d);
            group_len += 1;
        } else if matches!(c, '.' | ',' | ' ' | '\u{a0}') {
            if group_len == 0 || (!grouped && group_len > 3) || (grouped && group_len != 3) {
                return None;
            }
            grouped = true;
            group_len = 0;
        } else {
            return None;
        }
    }
    if digits == 0 || (grouped && group_len != 3) || (digits > 1 && first_zero) {
        return None;
    }
    Some(n)
}

/// Accept exactly the ungrouped and three-digit-grouped spellings in the source graph.
pub fn number(input: &str, gender: Gender) -> Option<String> {
    let n = parse_number(input)?;
    if n == 0 {
        return Some("zero".to_owned());
    }
    let mut out = String::new();
    for (divisor, singular, plural) in
        [(1_000_000_000, "miliard", "miliarde"), (1_000_000, "milion", "milioane"), (1_000, "mie", "mii")]
    {
        let group = ((n / divisor) % 1000) as u16;
        if group == 0 {
            continue;
        }
        if group == 1 {
            if !out.is_empty() {
                out.push(' ');
            }
            out.push_str(if singular == "mie" { "o " } else { "un " });
            out.push_str(singular);
        } else {
            write_triplet(group, Gender::Feminine, &mut out);
            let rem = group % 100;
            if rem == 0 || rem >= 20 {
                out.push_str(" de");
            }
            out.push(' ');
            out.push_str(plural);
        }
    }
    let units = (n % 1000) as u16;
    if units != 0 {
        write_triplet(units, gender, &mut out);
    }
    Some(out)
}
