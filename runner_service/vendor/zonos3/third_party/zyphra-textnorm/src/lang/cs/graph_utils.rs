//! Czech number morphology shared by the locale's taggers.
//!
//! The scale forms follow `cs/taggers/cardinal.py`, including its distinction
//! between a single-unit scale and a compound tens-and-units scale.

const DIGITS: [&str; 10] = ["nula", "jedna", "dva", "tři", "čtyři", "pět", "šest", "sedm", "osm", "devět"];
const TEENS: [&str; 10] =
    ["deset", "jedenáct", "dvanáct", "třináct", "čtrnáct", "patnáct", "šestnáct", "sedmnáct", "osmnáct", "devatenáct"];
const TENS: [&str; 10] =
    ["", "", "dvacet", "třicet", "čtyřicet", "padesát", "šedesát", "sedmdesát", "osmdesát", "devadesát"];
const HUNDREDS: [&str; 10] =
    ["", "sto", "dvě stě", "tři sta", "čtyři sta", "pět set", "šest set", "sedm set", "osm set", "devět set"];

#[derive(Clone, Copy)]
pub enum Gender {
    Default,
    Feminine,
}

pub fn digit(n: usize, gender: Gender) -> &'static str {
    match (n, gender) {
        (2, Gender::Feminine) => "dvě",
        _ => DIGITS[n],
    }
}

fn word(out: &mut String, text: &str) {
    if !out.is_empty() {
        out.push(' ');
    }
    out.push_str(text);
}

fn append_under_thousand(out: &mut String, n: usize, gender: Gender) {
    debug_assert!(n < 1000);
    if n == 0 {
        word(out, DIGITS[0]);
        return;
    }
    if n >= 100 {
        word(out, HUNDREDS[n / 100]);
    }
    let r = n % 100;
    if (10..20).contains(&r) {
        word(out, TEENS[r - 10]);
    } else {
        if r >= 20 {
            word(out, TENS[r / 10]);
        }
        if r % 10 != 0 {
            word(out, digit(r % 10, gender));
        }
    }
}

pub fn under_thousand(n: usize, gender: Gender) -> String {
    let mut out = String::with_capacity(48);
    append_under_thousand(&mut out, n, gender);
    out
}

fn append_scale(out: &mut String, n: usize, level: usize) {
    let hundreds = n / 100;
    let rem = n % 100;
    if hundreds != 0 {
        word(out, HUNDREDS[hundreds]);
    }
    if rem == 0 {
        word(
            out,
            match level {
                1 => "tisíc",
                2 => "milionů",
                _ => "miliard",
            },
        );
        return;
    }
    if rem < 10 {
        let word = match (level, rem) {
            (1, 1) => "tisíc",
            (1, 2) => "dva tisíce",
            (1, 3) => "tři tisíce",
            (1, 4) => "čtyři tisíce",
            (2, 1) => "jeden milion",
            (2, 2) => "dva miliony",
            (2, 3) => "tři miliony",
            (2, 4) => "čtyři miliony",
            (3, 1) => "jedna miliarda",
            (3, 2) => "dvě miliardy",
            (3, 3) => "tři miliardy",
            (3, 4) => "čtyři miliardy",
            _ => "",
        };
        if !word.is_empty() {
            self::word(out, word);
            return;
        }
    }
    append_under_thousand(out, rem, Gender::Default);
    self::word(
        out,
        match level {
            1 => "tisíc",
            2 => "milionů",
            _ => "miliard",
        },
    );
}

/// Spell an ungrouped, non-negative cardinal accepted by the source graph.
pub fn positive(input: &str) -> Option<String> {
    if input.is_empty() || !input.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    if input.len() > 1 && input.starts_with('0') {
        let zeros = input.bytes().take_while(|&b| b == b'0').count();
        let mut out = String::with_capacity(input.len().saturating_mul(6));
        for _ in 0..zeros {
            word(&mut out, "nula");
        }
        if zeros < input.len() {
            let rest = &input[zeros..];
            if let Some(spoken) = positive(rest) {
                word(&mut out, &spoken);
            } else {
                for digit in rest.bytes() {
                    word(&mut out, DIGITS[(digit - b'0') as usize]);
                }
            }
        }
        return Some(out);
    }
    positive_value(input.parse().ok()?)
}

pub fn positive_value(n: u64) -> Option<String> {
    if n >= 1_000_000_000_000 {
        if n % 1_000_000_000_000 != 0 || n > 9_000_000_000_000 {
            return None;
        }
        let word = match n / 1_000_000_000_000 {
            1 => "jeden bilion",
            2 => "dva biliony",
            3 => "tři biliony",
            4 => "čtyři biliony",
            5 => "pět bilionů",
            6 => "šest bilionů",
            7 => "sedm bilionů",
            8 => "osm bilionů",
            _ => "devět bilionů",
        };
        return Some(word.to_owned());
    }
    if n == 0 {
        return Some("nula".to_owned());
    }
    let mut out = String::with_capacity(if n < 1_000 { 32 } else { 96 });
    for (divisor, level) in [(1_000_000_000, 3), (1_000_000, 2), (1_000, 1)] {
        let part = ((n / divisor) % 1000) as usize;
        if part != 0 {
            append_scale(&mut out, part, level);
        }
    }
    let last = (n % 1000) as usize;
    if last != 0 {
        append_under_thousand(&mut out, last, Gender::Default);
    }
    Some(out)
}
