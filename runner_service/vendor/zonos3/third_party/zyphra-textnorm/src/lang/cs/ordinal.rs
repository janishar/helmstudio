use super::graph_utils::{Gender, under_thousand};
const BASE: [&str; 20] = [
    "",
    "první",
    "druhý",
    "třetí",
    "čtvrtý",
    "pátý",
    "šestý",
    "sedmý",
    "osmý",
    "devátý",
    "desátý",
    "jedenáctý",
    "dvanáctý",
    "třináctý",
    "čtrnáctý",
    "patnáctý",
    "šestnáctý",
    "sedmnáctý",
    "osmnáctý",
    "devatenáctý",
];
const TENS: [&str; 10] =
    ["", "", "dvacátý", "třicátý", "čtyřicátý", "padesátý", "šedesátý", "sedmdesátý", "osmdesátý", "devadesátý"];
const HUNDREDS: [&str; 10] =
    ["", "stý", "dvoustý", "třístý", "čtyřstý", "pětistý", "šestistý", "sedmistý", "osmistý", "devítistý"];
fn ordinal(n: usize) -> Option<String> {
    if n == 0 {
        return None;
    }
    if n < 20 {
        return Some(BASE[n].to_owned());
    }
    if n < 100 {
        let mut x = TENS[n / 10].to_owned();
        if n % 10 != 0 {
            x.push(' ');
            x.push_str(BASE[n % 10])
        }
        return Some(x);
    }
    if n < 1000 {
        if n % 100 == 0 {
            return Some(HUNDREDS[n / 100].to_owned());
        }
        return Some(format!("{} {}", under_thousand(n / 100 * 100, Gender::Default), ordinal(n % 100)?));
    }
    if n == 1000 {
        return Some("tisící".to_owned());
    }
    if n == 2000 {
        return Some("dvoutisící".to_owned());
    }
    let lead = match n / 1000 {
        1 => "tisíc",
        2 => "dva tisíce",
        3 => "tři tisíce",
        _ => return None,
    };
    Some(format!("{lead} {}", ordinal(n % 1000)?))
}
fn roman(s: &str) -> Option<usize> {
    Some(match s {
        "I" => 1,
        "II" => 2,
        "III" => 3,
        "IV" => 4,
        "V" => 5,
        "VI" => 6,
        "VII" => 7,
        "VIII" => 8,
        "IX" => 9,
        "X" => 10,
        "XI" => 11,
        "XII" => 12,
        "XIII" => 13,
        "XIV" => 14,
        "XV" => 15,
        "XVI" => 16,
        "XVII" => 17,
        "XVIII" => 18,
        "XIX" => 19,
        "XX" => 20,
        "XXI" => 21,
        "XXII" => 22,
        "XXV" => 25,
        "XXX" => 30,
        _ => return None,
    })
}
fn neut(n: usize) -> Option<String> {
    let x = ordinal(n)?;
    Some(
        x.split(' ')
            .map(|w| if let Some(p) = w.strip_suffix('ý') { format!("{p}é") } else { w.to_owned() })
            .collect::<Vec<_>>()
            .join(" "),
    )
}
fn fem(n: usize) -> Option<String> {
    let x = ordinal(n)?;
    Some(
        x.split(' ')
            .map(|w| if let Some(p) = w.strip_suffix('ý') { format!("{p}á") } else { w.to_owned() })
            .collect::<Vec<_>>()
            .join(" "),
    )
}
fn capitalize(s: &str) -> String {
    let mut c = s.chars();
    match c.next() {
        Some(f) => f.to_uppercase().collect::<String>() + c.as_str(),
        None => String::new(),
    }
}
/// Heads of contextual Roman-numeral branches; all other non-numeric
/// ordinal inputs are single tokens.
pub fn may_cross_space(first: &str) -> bool {
    let roman = first.strip_suffix('.').unwrap_or(first);
    (!roman.is_empty() && roman.bytes().all(|b| matches!(b, b'I' | b'V' | b'X')))
        || matches!(
            first,
            "Karel"
                | "karel"
                | "Ludvík"
                | "ludvík"
                | "Jan"
                | "jan"
                | "Petr"
                | "petr"
                | "Jiří"
                | "jiří"
                | "Václav"
                | "václav"
                | "Rudolf"
                | "rudolf"
                | "František"
                | "františek"
                | "Josef"
                | "josef"
                | "Jindřich"
                | "jindřich"
                | "Vilém"
                | "vilém"
                | "Filip"
                | "filip"
                | "Eduard"
                | "eduard"
                | "Richard"
                | "richard"
                | "Alžběta"
                | "alžběta"
                | "Marie"
                | "marie"
                | "Kateřina"
                | "kateřina"
                | "Kapitola"
                | "kapitola"
                | "Část"
                | "část"
                | "Oddíl"
                | "oddíl"
                | "Hlava"
                | "hlava"
                | "Paragraf"
                | "paragraf"
                | "Section"
                | "Sekce"
        )
}
pub fn ordinal_full(s: &str) -> Option<String> {
    match s {
        "XL" => return Some("X L".into()),
        "MIX" => return Some("M I X".into()),
        "CD" => return Some("C D".into()),
        "Section C" | "Section L" | "Sekce C" | "Sekce L" => return Some(s.to_owned()),
        _ => {}
    }
    if let Some((prefix, r)) = s.rsplit_once(' ') {
        let raw = r.strip_suffix('.').unwrap_or(r);
        if let Some(n) = roman(raw) {
            if [
                "Karel",
                "karel",
                "Ludvík",
                "ludvík",
                "Jan Pavel",
                "jan pavel",
                "Petr",
                "petr",
                "Jiří",
                "jiří",
                "Václav",
                "václav",
                "Rudolf",
                "rudolf",
                "František",
                "františek",
                "Josef",
                "josef",
                "Jindřich",
                "jindřich",
                "Vilém",
                "vilém",
                "Filip",
                "filip",
                "Eduard",
                "eduard",
                "Richard",
                "richard",
                "Jan",
                "jan",
            ]
            .contains(&prefix)
                && n <= 16
            {
                return Some(format!("{prefix} {}", capitalize(&ordinal(n)?)));
            }
            if ["Alžběta", "alžběta", "Marie", "marie", "Kateřina", "kateřina"].contains(&prefix) && n <= 3 {
                return Some(format!("{prefix} {}", capitalize(&fem(n)?)));
            }
            if ["Kapitola", "kapitola", "Část", "část", "Oddíl", "oddíl", "Hlava", "hlava", "Paragraf", "paragraf"]
                .contains(&prefix)
                && n <= 12
            {
                return Some(format!(
                    "{prefix} {}",
                    under_thousand(n, if n == 2 { Gender::Feminine } else { Gender::Default })
                ));
            }
        }
    }
    if let Some((n, noun)) = s.split_once(' ') {
        let raw = n.strip_suffix('.').unwrap_or(n);
        let parsed =
            roman(raw).or_else(|| if raw.bytes().all(|x| x.is_ascii_digit()) { raw.parse().ok() } else { None });
        if let Some(n) = parsed {
            let noun_base = noun.strip_suffix('.').unwrap_or(noun);
            if ["století", "stoleti", "st.", "st"].contains(&noun_base)
                && ((roman(raw).is_some() && n <= 22) || [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 19, 20, 21, 22].contains(&n))
            {
                return Some(format!("{} století", neut(n)?));
            }
            if (noun == "patro" || noun == "místo") && n <= 5 && (noun == "patro" || roman(raw).is_none()) {
                return Some(format!("{} {noun}", neut(n)?));
            }
            if noun == "třída" && n <= 9 && roman(raw).is_none() {
                return Some(format!("{} třída", fem(n)?));
            }
        }
    }
    let raw = s.strip_suffix('.')?;
    if raw.is_empty() || raw.starts_with('0') || !raw.bytes().all(|x| x.is_ascii_digit()) {
        return None;
    }
    ordinal(raw.parse().ok()?)
}
