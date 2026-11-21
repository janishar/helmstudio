// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
use super::graph_utils::{Gender, number};

fn pure(input: &str) -> Vec<String> {
    let fixed = match input {
        "½" | "1/2" => Some("o jumătate"),
        "¼" | "1/4" => Some("un sfert"),
        "¾" | "3/4" => Some("trei sferturi"),
        "⅓" | "1/3" => Some("o treime"),
        "⅔" | "2/3" => Some("două treimi"),
        "⅕" | "1/5" => Some("o cincime"),
        "⅖" | "2/5" => Some("două cincimi"),
        "⅗" | "3/5" => Some("trei cincimi"),
        "⅘" | "4/5" => Some("patru cincimi"),
        "⅙" | "1/6" => Some("o șesime"),
        "⅚" | "5/6" => Some("cinci șesimi"),
        "⅛" | "1/8" => Some("o optime"),
        "⅜" | "3/8" => Some("trei optimi"),
        "⅝" | "5/8" => Some("cinci optimi"),
        "⅞" | "7/8" => Some("șapte optimi"),
        "⅐" => Some("o șeptime"),
        "⅑" => Some("o noime"),
        "⅒" | "1/10" => Some("o zecime"),
        _ => None,
    };
    let mut out = Vec::new();
    if let Some(s) = fixed {
        out.push(s.to_owned());
    }
    if let Some((n, d)) = input.split_once('/') {
        let forms = match d {
            "2" => Some(("doime", "doimi")),
            "3" => Some(("treime", "treimi")),
            "4" => Some(("pătrime", "sferturi")),
            "5" => Some(("cincime", "cincimi")),
            "6" => Some(("șesime", "șesimi")),
            "7" => Some(("șeptime", "șeptimi")),
            "8" => Some(("optime", "optimi")),
            "9" => Some(("noime", "noimi")),
            "10" => Some(("zecime", "zecimi")),
            "100" => Some(("sutime", "sutimi")),
            "1000" => Some(("miime", "miimi")),
            _ => None,
        };
        if let Some((singular, plural)) = forms {
            if n == "1" {
                out.push(format!("o {}", singular));
            }
            if let Some(words) = number(n, Gender::Feminine) {
                out.push(format!("{} {}", words, plural));
            }
        }
    }
    out
}

pub fn fraction(input: &str) -> Vec<String> {
    let (negative, input) = if let Some(s) = input.strip_prefix('-').or_else(|| input.strip_prefix('−')) {
        (true, s)
    } else {
        (false, input)
    };
    let mut out = pure(input);
    for (idx, _) in input.char_indices().skip(1) {
        let (whole, rest) = input.split_at(idx);
        let Some(whole) = number(whole, Gender::Masculine) else {
            continue;
        };
        let unspaced = rest.strip_prefix(' ').unwrap_or(rest);
        let special = match unspaced {
            "½" | "1/2" => Some("jumătate"),
            "¼" | "1/4" => Some("un sfert"),
            "¾" | "3/4" => Some("trei sferturi"),
            _ => None,
        };
        if let Some(s) = special {
            out.push(format!("{} și {}", whole, s));
        }
        if let Some(rest) = rest.strip_prefix(' ') {
            for part in pure(rest) {
                out.push(format!("{} și {}", whole, part));
            }
        }
    }
    if negative {
        for s in &mut out {
            s.insert_str(0, "minus ");
        }
    }
    out
}
