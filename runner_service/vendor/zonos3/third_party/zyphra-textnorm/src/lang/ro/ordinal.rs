// Copyright (c) 2026 Zyphra, Inc. Licensed under Apache-2.0.
const M: [&str; 20] = [
    "",
    "primul",
    "al doilea",
    "al treilea",
    "al patrulea",
    "al cincilea",
    "al șaselea",
    "al șaptelea",
    "al optulea",
    "al nouălea",
    "al zecelea",
    "al unsprezecelea",
    "al doisprezecelea",
    "al treisprezecelea",
    "al paisprezecelea",
    "al cincisprezecelea",
    "al șaisprezecelea",
    "al șaptesprezecelea",
    "al optsprezecelea",
    "al nouăsprezecelea",
];
const F: [&str; 20] = [
    "",
    "prima",
    "a doua",
    "a treia",
    "a patra",
    "a cincea",
    "a șasea",
    "a șaptea",
    "a opta",
    "a noua",
    "a zecea",
    "a unsprezecea",
    "a douăsprezecea",
    "a treisprezecea",
    "a paisprezecea",
    "a cincisprezecea",
    "a șaisprezecea",
    "a șaptesprezecea",
    "a optsprezecea",
    "a nouăsprezecea",
];
const BASE: [&str; 10] =
    ["", "", "douăzeci", "treizeci", "patruzeci", "cincizeci", "șaizeci", "șaptezeci", "optzeci", "nouăzeci"];
const MO: [&str; 10] =
    ["", "unulea", "doilea", "treilea", "patrulea", "cincilea", "șaselea", "șaptelea", "optulea", "nouălea"];
const FO: [&str; 10] = ["", "una", "doua", "treia", "patra", "cincea", "șasea", "șaptea", "opta", "noua"];
const MT: [&str; 10] = [
    "",
    "",
    "douăzecilea",
    "treizecilea",
    "patruzecilea",
    "cincizecilea",
    "șaizecilea",
    "șaptezecilea",
    "optzecilea",
    "nouăzecilea",
];
const FT: [&str; 10] =
    ["", "", "douăzecea", "treizecea", "patruzecea", "cincizecea", "șaizecea", "șaptezecea", "optzecea", "nouăzecea"];
pub fn ordinal_words(n: usize, fem: bool) -> Option<String> {
    if !(1..=1000).contains(&n) {
        return None;
    }
    if n < 20 {
        return Some(if fem { F[n] } else { M[n] }.to_owned());
    }
    if n == 100 {
        return Some(if fem { "a o suta" } else { "al o sutălea" }.to_owned());
    }
    if n == 1000 {
        return Some(if fem { "a o mia" } else { "al o mielea" }.to_owned());
    }
    if n >= 200 {
        return Some(format!("{} {}-{}", if fem { "a" } else { "al" }, n, if fem { "a" } else { "lea" }));
    }
    let rem = n % 100;
    let prefix = if fem { "a" } else { "al" };
    let component = if rem < 20 {
        let s = if fem { F[rem] } else { M[rem] };
        s.strip_prefix(if fem { "a " } else { "al " }).unwrap_or(s).to_owned()
    } else if rem % 10 == 0 {
        if fem { FT[rem / 10] } else { MT[rem / 10] }.to_owned()
    } else {
        format!("{} și {}", BASE[rem / 10], if fem { FO[rem % 10] } else { MO[rem % 10] })
    };
    Some(format!("{} {}{}", prefix, if n >= 100 { "o sută " } else { "" }, component))
}
pub fn ordinal(s: &str) -> Option<String> {
    let s = s.strip_prefix("il ").or_else(|| s.strip_prefix("Il ")).unwrap_or(s);
    let (prefix, s) = if let Some(v) = s.strip_prefix("al ").or_else(|| s.strip_prefix("Al ")) {
        (1, v)
    } else if let Some(v) = s.strip_prefix("a ").or_else(|| s.strip_prefix("A ")) {
        (2, v)
    } else {
        (0, s)
    };
    let (n, fem) =
        if let Some(v) = s.strip_suffix("-lea").or_else(|| s.strip_suffix('°')).or_else(|| s.strip_suffix('º')) {
            (v, false)
        } else if let Some(v) = s.strip_suffix("-ul") {
            (v, false)
        } else if let Some(v) = s.strip_suffix("-a").or_else(|| s.strip_suffix('ª')) {
            (v, true)
        } else {
            return None;
        };
    if n.starts_with('0')
        || n.is_empty()
        || !n.bytes().all(|b| b.is_ascii_digit())
        || prefix == 1 && fem
        || prefix == 2 && !fem
    {
        return None;
    }
    let n: usize = n.parse().ok()?;
    if n == 1 && (prefix != 0 || s.ends_with("-ul") && fem) {
        return None;
    }
    if n != 1 && s.ends_with("-ul") {
        return None;
    }
    ordinal_words(n, fem)
}
