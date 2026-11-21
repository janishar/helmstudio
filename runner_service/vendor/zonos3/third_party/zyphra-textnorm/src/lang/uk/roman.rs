// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
use super::ordinal::ordinal;
const ROMANS: [&str; 21] = [
    "I", "II", "III", "IV", "V", "VI", "VII", "VIII", "IX", "X", "XI", "XII", "XIII", "XIV", "XV", "XVI", "XVII",
    "XVIII", "XIX", "XX", "XXI",
];
pub(super) fn roman(s: &str) -> Option<(String, i32)> {
    match s {
        "XL" => return Some(("ікс ель".into(), 1)),
        "CD" => return Some(("сі ді".into(), 1)),
        "MIX" => return Some(("ем ай ікс".into(), 1)),
        _ => {}
    }
    let (a, b) = s.split_once(' ')?;
    if let Some(n) = ROMANS.iter().position(|x| *x == b) {
        let form = if ["Глава", "глава", "Частина", "частина"].contains(&a) {
            1
        } else if ["Розділ", "розділ", "Том", "том", "Петро", "Володимир", "Карл", "Людовик"].contains(&a)
        {
            0
        } else {
            return None;
        };
        return Some((format!("{a} {}", ordinal(&(n + 1).to_string(), form)?), 5));
    }
    if ["століття", "ст.", "ст", "век"].contains(&b) {
        let n = ROMANS.iter().position(|x| *x == a)?;
        return Some((format!("{} століття", ordinal(&(n + 1).to_string(), 2)?), 5));
    }
    None
}
