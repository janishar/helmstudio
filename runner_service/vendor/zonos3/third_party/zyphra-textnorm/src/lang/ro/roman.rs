// Copyright (c) 2026 Zyphra, Inc. Licensed under Apache-2.0.
use super::{
    graph_utils::{Gender, number},
    ordinal::ordinal_words,
};
const ROMANS: [&str; 25] = [
    "I", "II", "III", "IV", "V", "VI", "VII", "VIII", "IX", "X", "XI", "XII", "XIII", "XIV", "XV", "XVI", "XVII",
    "XVIII", "XIX", "XX", "XXI", "XXII", "XXIII", "XXIV", "XXV",
];
fn value(s: &str) -> Option<usize> {
    if s == "XXX" { Some(30) } else { ROMANS.iter().position(|v| *v == s).map(|i| i + 1) }
}
fn ord(s: &str, fem: bool) -> Option<String> {
    let n = value(s)?;
    if n > if fem { 21 } else { 22 } {
        return None;
    }
    let o = ordinal_words(n, fem)?;
    Some(o.strip_prefix(if fem { "a " } else { "al " }).unwrap_or(&o).to_owned())
}
pub fn roman(s: &str) -> Vec<String> {
    let mut out = Vec::new();
    for (prefix, spoken) in [
        ("Capitolul ", "Capitolul "),
        ("capitolul ", "capitolul "),
        ("Cap. ", "capitolul "),
        ("cap. ", "capitolul "),
        ("Volumul ", "Volumul "),
        ("volumul ", "volumul "),
        ("Vol. ", "volumul "),
        ("vol. ", "volumul "),
        ("Partea ", "Partea "),
        ("partea ", "partea "),
        ("Actul ", "Actul "),
        ("actul ", "actul "),
        ("secolul ", "secolul "),
        ("Secolul ", "Secolul "),
        ("sec. ", "secolul "),
        ("Sec. ", "secolul "),
    ] {
        if let Some(n) = s.strip_prefix(prefix).and_then(value).and_then(|n| number(&n.to_string(), Gender::Masculine))
        {
            out.push(format!("{}{}", spoken, n));
        }
    }
    if let Some(n) = s.strip_suffix(" secol").and_then(|v| ord(v, false)) {
        out.push(format!("al {} secol", n));
    }
    const NAMES: &[&str] = &[
        "Carol",
        "Ferdinand",
        "Mihai",
        "Ioan",
        "Paul",
        "Ioan Paul",
        "Papa Ioan Paul",
        "Papa",
        "Benedict",
        "Gheorghe",
        "Alexandru",
        "Petru",
        "Ludovic",
        "Henric",
        "Filip",
        "Ștefan",
        "Mircea",
        "Vlad",
        "Regele",
        "Regina",
        "Elisabeta",
    ];
    for &name in NAMES {
        if let Some(rest) = s.strip_prefix(name) {
            if rest == " I" {
                out.push(format!("{} întâi", name));
            }
            if let Some(n) = rest.strip_prefix(" al ").and_then(|v| v.strip_suffix("-lea")).and_then(|v| ord(v, false))
            {
                out.push(format!("{} al {}", name, n));
            }
            if let Some(n) = rest.strip_prefix(" a ").and_then(|v| v.strip_suffix("-a")).and_then(|v| ord(v, true)) {
                out.push(format!("{} a {}", name, n));
            }
        }
    }
    for prefix in ["clasa ", "Clasa "] {
        if let Some(rest) = s.strip_prefix(prefix) {
            if rest == "I" {
                out.push(format!("{}întâi", prefix));
            }
            if let Some(n) = rest.strip_prefix("a ").and_then(|v| v.strip_suffix("-a")).and_then(|v| ord(v, true)) {
                out.push(format!("{}a {}", prefix, n));
            }
        }
    }
    out
}
